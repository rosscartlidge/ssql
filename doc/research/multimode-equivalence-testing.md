# When one pipeline has five implementations: a divergence bug and the differential harness that kills it

**Status:** Retrospective + design note, 2026-07-01. A correctness bug that hid
in plain sight across our multiple execution/codegen backends, why our existing
tests missed it, and the N-way differential equivalence harness we built so this
whole *class* of bug fails loudly. Written to be readable without deep ssql
knowledge — the lessons generalize to any system that runs the "same" program
more than one way (interpreters + compilers, query engines + codegen, multiple
targets).

## TL;DR

- ssql runs one pipeline **five ways** (interpreted, three flavours of generated
  Go, and via generated SQL). A `top`-by-string-field operation was **correct in
  two of them and silently wrong in the other three** — same logic bug, copied
  into paths that were fixed at different times.
- Our regression tests didn't catch it for two independent reasons: the oracle
  was too weak (substring `Contains`, not equality) **and** the test fixture
  didn't discriminate a wrong answer from a right one (the data was already
  sorted, so "return the first N rows" happened to equal "return the sorted N").
- Fix for the bug: one type-aware comparison shared by all paths. Fix for the
  *class*: a **differential (metamorphic) test harness** that runs every path,
  normalizes away legitimate differences, and asserts byte-identical output —
  with implementation-independent "golden" answers on deliberately shuffled
  data. We proved it has teeth by reintroducing the bug and watching it fail.

## Context: the same pipeline, five ways to run it

ssql is a Unix-style data tool. You write a pipeline once:

```
ssql from csv data.csv | ssql top -asc 10 -field name | ssql to table
```

and it can be *executed* five different ways — this multiplicity is a feature
(prototype interactively, then compile the hot path to native Go), but it's also
the setup for the bug:

| # | Path | What runs |
|---|------|-----------|
| 1 | **exec** | the interpreted CLI pipeline (dynamic `Record` values) |
| 2 | **go / record** | `generate go` → Go over `map`-like `Record`s, compiled |
| 3 | **go / typed** | `generate go` → Go over generated `struct` types, compiled |
| 4 | **go / parallel** | same, but the parallel `Stream[T]` runtime |
| 5 | **generate sql** | translated to SQL, run by DuckDB |

(There's also *generate ssql*, which rewrites the pipeline to an optimised
pipeline that is itself then run one of the above ways — a sixth surface.)

Every one of these is a separate implementation of the same semantics. That is
`commands × backends` cells, and **any cell can drift from the others.**

## The bug

`top -field name` returns the N rows with the largest (or, with `-asc`,
smallest) value of `name`. A user ran it over a real, unsorted dataset and
noticed the **interpreted pipeline and the generated Go disagreed**:

```
# generated typed Go — correct, lexicographic:
-52543054, 10-10:D.chs02, 10-5:J.mrn02, ...

# generated record-mode Go — arbitrary rows, NOT sorted:
-52543054, ar01.lis01,..., ju1crack3mux8.sin05, bb01.mia09, ...
```

Root cause: `top` ranked by coercing each value to a number. For a **string**
field that coercion returned `0` for *every* row, so all rows tied and the
selection returned whatever happened to come first — arbitrary. The typed path
had been rewritten to compare by the field's real type; the record path (and,
it turned out, the SQL path) still had the old numeric-only key.

It was really **four bugs wearing one costume**:

1. **exec** — had the numeric coercion. *(Fixed first.)*
2. **go / typed** — had it, then got a type-aware fix. *(Fixed second.)*
3. **go / record** — still emitted `BottomBy(…, func(r) float64 { return getOr(r,"name",0.0) })`. **Wrong.**
4. **generate sql** — worse: the SQL translator still looked for a **long-renamed
   flag** (`-by`, since renamed to `-field`), so it emitted **no `ORDER BY` at
   all**, and it treated the first token as the row limit, so `-asc` became
   `LIMIT -asc`. **Wrong, and had been for ages.**

Paths 1 and 2 were fixed in an earlier release; 3 and 4 were found only because
the user compared two outputs side by side. Classic: *fix the bug where you see
it, miss the three copies you don't.*

## Why our tests didn't catch it

We *had* a regression corpus that ran pipelines through the record/typed/parallel
Go backends. It stayed green through all of this. Two reasons, and both are the
real lesson:

**1. The oracle was too weak.** The corpus asserted `Contains`/`Excludes`
substrings, not equality. The string-`top` test expected the output to *contain*
`Alice` and `Bob` — and the buggy output *did* contain them too. A weak oracle
passes wrong answers that happen to overlap the right ones.

**2. The fixture didn't discriminate.** The test data (`employees.csv`) was
already in **alphabetical order**. For alphabetical input, the buggy behaviour
("return the first N rows") produces the *same* rows as the correct behaviour
("return the lexicographically-smallest N"). The test literally *could not* tell
right from wrong, because the only input that distinguishes them is **unsorted**
input. The user's real data was shuffled; that's why it surfaced for them and
not for us.

A test is only as good as `strength_of_oracle × discriminating_power_of_input`.
Ours was weak on both axes at once.

## The immediate fix

Give every path the **same type-aware comparison**: compare two values
numerically when both are numbers, lexicographically otherwise (the same
comparator the `sort` command already used). One shared function, referenced by
exec and emitted verbatim by the record codegen; the typed codegen keys by the
struct field's real Go type (which the compiler orders correctly); the SQL
translator emits `ORDER BY field DESC|ASC LIMIT n`. Now all five agree — verified
against DuckDB for the SQL path.

That closes the specific hole. But the same shape ("logic in N backends, fixed
in some") will recur for every command we touch. The durable fix is a test that
makes divergence *impossible to merge*.

## The systemic fix: an N-way differential harness

The idea is **differential (a.k.a. metamorphic) testing**: don't assert a single
expected string per pipeline; run *every* backend and assert they all produce
**the same** answer. Concretely, for each pipeline:

1. **Run every lane** — exec, go-record, go-typed, go-parallel, generate-ssql —
   capturing output as line-delimited JSON (a canonical, parseable sink).
2. **Normalize** each lane's output (see below).
3. **Assert all lanes are byte-identical** to the reference (the interpreted
   `exec` lane), and — where provided — to an implementation-independent
   **golden** answer.

The plumbing is easy. The three things that make it *correct and useful* are the
hard part, and they're the transferable design:

### (a) Normalize the *legitimate* differences — but only those

The backends differ in ways that are correct-by-design, and a naive `diff` would
drown in them:

- **Column order.** Record mode emits columns alphabetically; typed mode keeps
  struct-definition order. Same data, different key order.
- **Number formatting.** `1` vs `1.0` across encoders.
- **Row order.** The parallel backend is unordered — rows come back in
  whatever order the shards finish.

We fold these out by parsing each row to a map and re-serializing with **sorted
keys** (kills column-order differences), letting the JSON decoder coerce all
numbers to one type (kills `1` vs `1.0`), and — for row order — see (b). The
guiding rule: **normalize exactly the differences that are semantically
irrelevant, and not one bit more**, or you normalize away the bugs too.

### (b) Ordered vs. multiset comparison

Whether row order is part of the answer depends on the pipeline. `... | sort` or
`... | top` *defines* an order; a bare `... | where` does not (and the parallel
backend will reorder it). So each test case is tagged `Ordered: true/false`:
ordered cases compare as a **sequence**, unordered cases compare as a
**multiset** (sort the canonical rows, then compare). Get this wrong in either
direction and you get false failures or false passes.

### (c) The oracle problem — who watches the watchmen

"All lanes agree with the exec lane" is a *metamorphic* oracle: cheap, and it
catches any *divergence*. But it has a blind spot — if the reference is *also*
wrong (as exec was, at the very start of this saga), then everything agrees and
everything is wrong, and the test is happily green. So we pair it with two
independent checks:

- **Golden outputs** — a handful of hand-written expected results, derived from
  the spec by a human, not produced by any implementation. These catch
  "unanimously wrong."
- **A second engine** — the SQL lane runs on **DuckDB**, a completely
  independent implementation of the relational semantics. Agreement with DuckDB
  is strong evidence the semantics themselves are right, not just internally
  consistent.

Metamorphic for breadth (every command, cheaply); golden + DuckDB for ground
truth on the cases that matter.

*(Status: when this doc was written the DuckDB check was manual. Since v4.56.0
it is a real harness lane — `TestPipelineEquivalence` runs `generate sql`
through `duckdb -json` alongside the Go lanes, gated on a `duckdb` binary being
present. Its first catch was immediate: `-if-expr` passthrough emitted SQL that
DuckDB rejected (`&&`) or mis-parsed (`"double quotes"` are identifiers in
SQL), and `update -set-expr` was silently dropped by the translator — the lane
flagged both, and both were fixed by a real expr→SQL translation that fails
loudly on untranslatable constructs.)*

### (d) Fixtures that can actually fail

The alphabetical fixture is the whole reason the bug hid. So the differential
fixtures are **deliberately shuffled, with distinct values**, so that a wrong
selection or a wrong order produces a *different set of rows* — the property that
makes the test able to fail. This is the cheapest, highest-leverage change of
all: **adversarial input beats more assertions.**

## Proof it has teeth

A test you haven't seen fail is a test you don't trust. We reintroduced the
record-codegen bug on purpose and reran the harness:

```
lane "go-record" disagrees with exec (ordered):
  exec:      {"city":"Tokyo",...} {"city":"Quito",...} {"city":"Paris",...}
  go-record: {"city":"Cairo",...} {"city":"Lima",...}  {"city":"Mumbai",...}
```

It failed, named the divergent lane, and printed the exact rows. Then we
restored the fix and it went green (8 pipelines × 5 lanes). That
reintroduce-and-watch-it-fail step is not optional — it's how you know the gate
isn't a no-op.

## Lessons (that generalize beyond ssql)

1. **N implementations of one semantics is N places to drift.** Interpreter +
   compiler, query planner + codegen, multiple output targets — the moment you
   have more than one, "fix it where you see it" leaves silent copies. Assume the
   bug is in *all* the backends until a test proves otherwise.
2. **Differential testing is the natural fit.** When you have multiple
   implementations, you don't need to hand-write the expected answer for every
   case — you make the implementations check *each other*. The expected-output
   maintenance cost drops to near zero for breadth.
3. **A test's power is `oracle strength × input discrimination`.** Both were weak
   here, independently. Substring matching on pre-sorted data was doubly blind.
   Strengthen both: exact normalized equality, and adversarial (shuffled,
   distinct) inputs.
4. **Normalize only the differences that don't matter.** The engineering is
   entirely in separating "legitimately different" (column order, row order for
   unordered ops, number formatting) from "must be identical" (the data). Too
   little normalization = noise; too much = you erase the bug.
5. **Guard the metamorphic oracle with an independent one.** "They all agree"
   can mean "they're all wrong." A few human-written goldens and a second engine
   (here, DuckDB) cover the unanimous-wrong case.
6. **A gate you haven't watched fail isn't a gate.** Reintroduce the original bug
   once and confirm the harness catches it.

## Pointers (in this repo)

- `cmd/ssql/equivalence_test.go` — the harness (`TestPipelineEquivalence`):
  lanes, normalization, ordered/multiset compare, golden oracles, shuffled
  fixture.
- `cmd/ssql/corpus_test.go` — the older substring smoke test it complements (and
  a note on why the smoke test wasn't enough).
- `cmd/ssql/commands/top.go`, `generate_sql.go` — the four `top` paths, now
  sharing one comparator / emitting a correct `ORDER BY`.
- Shipped in v4.55.0.
