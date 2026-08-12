# Compiling an Embedded Expression Language into a Query Pipeline Code Generator: An Experience Report

Reference: DFC104
Created: 2026-08-10
Last modified: 2026-08-10

[Back to Index](./README.md)

**Status:** Research paper / experience report, 2026-08-10. Covers the
expr→Go transpiler work (v4.56.1 → post-Phase-4 main, commits
`6531859..df354e2`). All measurements in §5 are real runs, reproducible
with the commands shown.

---

## Abstract

ssql is a Unix-pipeline query tool whose pipelines run five ways from a
single command line: an interpreted executor and four code-generation
backends (record-mode Go, typed Go, parallel typed Go, and SQL). Its
embedded expression language (`-if-expr`, `-set-expr`, `-expr`,
`-stream-expr`) was, until this work, executed everywhere by a runtime
expression VM (expr-lang) — including inside *generated* programs, where
each row paid a map-materialization plus interpreted dispatch, and where
the mere presence of one expression ejected entire pipelines from the
typed/parallel backend into the slowest one.

We report on a four-phase effort that transpiles the expression language
into native Go inside the generated code, with the VM retained as a
per-expression fallback tier rather than a per-pipeline cliff. On a
5M-row workload, a filter-and-count pipeline improved from 8.4 s / 1.18 GB
peak RSS (previous best generated code) to 0.44 s / 329 MB (19× CPU,
3.6× memory); an aggregation pipeline improved 16× in CPU and 7.5× in
memory; and a streaming-fold aggregation improved 4.1× in CPU and **62×
in memory** (1.80 GB → 29 MB) by replacing group materialization with
compiled accumulators. Per-row predicate cost fell from 1.26 µs and
1,088 B of garbage to 3.0 ns and zero allocations (typed) or 28 ns and
zero allocations (record mode).

The implementation cost ~2,300 lines of Go plus ~1,500 lines of tests
over five increments, with correctness enforced by a differential
harness that executes every corpus expression under both the transpiler
and the VM and compares results exactly. The harness rejected our own
design assumptions twice before they shipped. We conclude the work was
decisively worth it, and argue that the general shape — *curated-subset
transpilation with semantic-diff gating and a graceful tier ladder* — is
a reusable technique for any system that embeds a dynamic expression
language in a compiled or code-generating host.

---

## 1. Introduction

ssql compiles shell pipelines such as

```sh
ssql from big.csv \
  | ssql where -if-expr 'price * qty > 1000 && region != "south"' \
  | ssql group-by region -count n
```

into standalone Go programs (`generate go`), with three emission modes:
*record* (dynamic `Record` values), *typed* (a struct type inferred from
the source), and *parallel* (typed, sharded). A planner chooses parallel
forms where every stage supports them.

The expression language is the escape hatch: predicates and computed
assignments too rich for flag syntax. Before this work it had exactly one
implementation — the expr-lang VM — used by the interpreter *and* pasted
into generated programs as a compiled-at-startup closure. This had two
costs:

1. **Per-row overhead.** Each evaluation materialized the row into a
   `map[string]any` (every field boxed), installed helper closures, and
   dispatched through the VM: ~1.26 µs and 1,088 B across 9 allocations
   per row (§5.1), against ~3 ns for the equivalent native Go.
2. **A capability cliff.** The typed backend could not type-check VM
   results, so `where -if-expr` silently downgraded the *entire
   downstream pipeline* to record mode, and `update -set-expr` /
   `group-by -expr` in typed mode were hard errors. One expression
   anywhere forfeited parallelism everywhere (§5.2, E1: the "parallel"
   mode ran at record-mode speed).

The remedy is classical — compile the expression — but the constraints
are the interesting part: the transpiled code must reproduce the VM's
observable semantics *exactly* (the project's differential testing
regime asserts byte-identical normalized output across all backends), the
VM must remain for expressions outside any curated subset, and the
work has to be incremental in a living codebase.

## 2. Design

### 2.1 The tier ladder

Every expression is classified per-expression at code-generation time:

- **Tier N (native):** `exprToGo` transpiles the expr-lang AST to a Go
  expression with the row's static types. Zero-allocation, inlineable.
- **Tier V (VM, static env):** the expression stays on the VM, but the
  generated code builds the env map from the typed struct
  (`exprEnv<T>(r)`), so the *stage* keeps its typed shape and downstream
  stages keep their parallel forms. Costs what the VM always cost — but
  only for that expression.
- **Tier R (record fallback):** the pre-existing whole-stage record
  path, kept as the floor for shapes no static type can hold (e.g. a new
  column whose type is unknowable, or a `-set-expr` that would retype a
  column at runtime).

`generate go -explain` reports the tier and the reason for every
expression, which doubles as the telemetry source for deciding what to
add to the native subset next.

### 2.2 The transpiler

`exprToGo` (~700 lines) is an AST walker over expr-lang's parser output
with a four-type lattice (`int64 | float64 | string | bool`). It mirrors
a table of empirically probed VM semantics; the divergence traps that
table encodes are exactly the bugs a naive transpiler would ship:

| expr-lang | naive Go | required emission |
|---|---|---|
| `7 / 2` = `3.5` | integer division | `float64(a) / float64(b)` — always |
| `len("héllo")` = 5 | byte length | rune count (`exprfn.RuneLen`) |
| `2 ** 3` | no operator | `math.Pow` |
| `round(2.5)`, `round(-2.5)` | banker's rounding | half away from zero (`math.Round`) |
| `min(-3, -2.5)` = **int64** −3 | promote to float | **refuse** — result keeps the winner's own type; no static type expresses it |
| `1 < 2 < 3` | parse error / wrong | none needed — the parser desugars to `&&` |

The last two rows were *discovered*, not designed: the differential
harness (§4) falsified the plan's assumption that mixed-type `min`
promotes, on its first execution; and an AST probe showed chained
comparisons and pipe syntax desugar in the parser, deleting two planned
features. Anything outside the subset returns an error naming the
construct, and the caller picks a tier — the error is a routing signal,
not a failure.

### 2.3 Four integration sites, one walker

The same walker serves four progressively harder sites:

1. **Predicates and assignments** (`where -if-expr`,
   `update -if-expr/-set-expr`): direct emission into existing closure
   templates. Assignment typing is conservative — the only inserted
   coercion is the value-preserving `int64→float64` widening; a float
   result into an int column is a *retype* in the interpreter's
   semantics and falls back rather than truncate.
2. **Streaming folds** (`-stream-expr '{s:0,n:0}' '{s:s+x,n:n+1}' 's/n'`):
   the user-declared state object becomes typed accumulator fields with
   inferred (fixpoint-widened) types; the per-row object literal becomes
   **one simultaneous multi-assignment** (`{a:b, b:a}` must swap — the
   interpreter replaces the whole state object computed from the *old*
   state); the finalizer becomes the `Result()` method. Folds are not
   generally associative, so this site forces the serial group-by form.
3. **Batch aggregations** (`-expr 'sum(price*qty)/count()'`): lowered
   from the *interpreter's own* normalized AST
   (`ssql.CompileAggExprPatched` — see §3, lesson 1). Each distinct sum
   term becomes an accumulator with a `+=` and, crucially, a `Merge`
   method — sums and counts are associative — so these aggregations
   *keep* the parallel group-by.
4. **Record mode** (no struct): the source samples column types (the same
   inference the typed backend already trusts) and forwards them as
   *advisory* metadata on the code-fragment stream; the walker then
   emits typed `ssql.GetOr(r, "price", float64(0))` accesses. Advisory
   metadata propagates conservatively — an assignment that could retype
   a column deletes it from the advisory rather than risk a stale type.

### 2.4 What stays out, deliberately

`time.Time` fields, string→int casts, dynamic regex patterns,
sequence-valued fields, fields referenced outside aggregations (the VM
binds those to per-group value *arrays*), and any construct the walker
does not recognize — all fall to Tier V or R with a reason string. The
subset is grown from `-explain` telemetry, not speculation.

## 3. Implementation lessons

Three findings generalize beyond this codebase:

1. **Never re-derive what the interpreter computes — call it.** Bare
   `count()` is a parse error under expr-lang's standalone parser (an
   arity-checked builtin); the interpreter only accepts it because its
   compile env installs a dummy `count` function that shadows the
   builtin. Our first aggregation lowering re-parsed the expression and
   silently fell back on every `count()`. The fix exports the
   interpreter's own compile (same env, same AST patcher) and lowers
   from the *returned program's AST*. One semantics, two consumers, by
   construction.
2. **Shadowing/order details must be read from the interpreter, not the
   design doc.** The plan asserted stream-fold state shadows record
   fields; the code (`maps.Copy(state)` then `maps.Insert(record)`)
   says record wins. The plan had — wisely — marked this "VERIFY at
   impl."
3. **Loud vs fallback is a per-construct judgement with real stakes.**
   An unknown field in a typed pipeline is a typo: fail at codegen,
   listing the schema. An unknown field in record mode may be a
   legitimately reshaped row: fall back to the VM, which validates
   against the real first record. Worked through in §3.1: the same
   mechanical signal — an unresolved identifier — demands *opposite*
   responses depending on where it occurs, and getting it wrong in
   either direction is a user-visible regression.

### 3.1 Worked example: `sum(salary) / len(salary)`

This aggregation expression is the sharpest instance of lesson 3, and it
turns on a quirk of the interpreter's evaluation environment. When exec
evaluates `group-by region -expr '…'`, it materializes each group and
builds the env (`buildAggBatchEnv`) for a group of, say, three rows with
salaries 100, 200, 300 as:

```
_records = [{salary:100,…}, {salary:200,…}, {salary:300,…}]
salary   = [100, 200, 300]     ← every field is ALSO bound to the
region   = ["east","east","east"]  array of its values across the group
```

The array binding is deliberate, and it is ssql's own code, not
expr-lang's: `buildAggBatchEnv` (expr_agg.go), called per group from
`ssql.ExprAgg`'s aggregation function, makes one pass over the group's
rows building *both* representations at once —

```go
for _, r := range records {
    recMap := make(map[string]any)
    for k, v := range r.All() {
        fieldValues[k] = append(fieldValues[k], v) // the column array
        recMap[k] = v
    }
    recordMaps = append(recordMaps, recMap)
}
// "Add field arrays to env"
for field, values := range fieldValues { env[field] = values }
env["_records"] = recordMaps
```

— so a bare field name used *outside* an aggregation function evaluates
to the group's **value array**, enabling column-as-array idioms
(`len(salary)`, `salary[0]`). Note the memory shape this implies: per
group, per expression evaluation, the data exists three times over — the
materialized `[]Record`, the `recordMaps` the patched `sum(_records, …)`
iterates, and a boxed `[]any` copy of every column. This
triple-representation is a substantial part of E2's 2.4 GB exec/record
peak RSS (§5.3); the transpiled accumulators build none of it.

Tracing the expression:

- `sum(salary)`: `sum` is a recognized aggregation, so the AST patcher
  rewrites it to `sum(_records, #.salary)` — iterate the rows, take each
  row's salary → 600.
- `len(salary)`: `len` is *not* an aggregation function, so the patcher
  leaves it alone; `salary` stays a bare identifier and resolves to the
  array `[100, 200, 300]`; `len` of it is 3 — the group size.
- `600 / 3 = 200`: average salary. Accidental idiom or not, this is
  *legal, working interpreter behaviour* that a user's pipeline may rely
  on.

The typed lowering cannot express it. Accumulators exist precisely so
the per-group value arrays are *never materialized* (that is where E2's
2.45 GB → 328 MB comes from); the outer expression runs once per group
over accumulated scalars, with no row and no array in scope. Supporting
the array binding natively would require building the very structure the
lowering exists to eliminate.

So when the outer walk hits the bare `salary`, it sees an unresolved
identifier — mechanically indistinguishable from a typo. The reflexive
response under the project's fail-loudly rule would be a codegen error.
That would have **broken a working pipeline**. Instead the lowering
treats an unresolved identifier in the *outer* position as a quiet
refusal: the stage falls back to record codegen, which reproduces the
array semantics exactly, and `-explain` states the reason:

```
[plan] … : record fallback (-expr v: aggregation expression references a
       field outside sum()/avg() — the VM binds it to the group's value
       ARRAY, which has no typed lowering: unknown field "salary" …)
```

Contrast `sum(nope * 2)`: an unknown name *inside* the aggregation,
where scope is per-row and the interpreter's own compiler rejects it
too. Invalid in every mode → loud codegen error. Same signal, opposite
correct responses, distinguished only by evaluation context — which is
why this classification cannot be a blanket policy and had to be decided
construct by construct against the interpreter's actual behaviour.

The practical guidance that falls out: write `sum(salary) / count()` (or
`avg(salary)`) — `count()` lowers to the shared counter and the whole
expression stays on the native, parallel path; `len(field)` as a
group-size idiom keeps working, on the VM fallback. A future
canonicalization could rewrite `len(field)` → `count()` automatically
(safe, since every row contributes a value to the field array), at which
point the idiom would regain the native path without user action.

## 4. Correctness methodology

The project's standing rule — *the same pipeline runs five ways; a bug
fixed in one backend is live in the others until a test proves
otherwise* — shaped the gates:

- **Differential expression harness** (62 expressions): one generated
  program evaluates each expression under the transpiled closure *and*
  `runtime.CompileExpr`, over rows engineered to expose divergence
  (negatives for truncation, ±2.5 for rounding, a zero divisor for ±Inf
  parity, multi-byte runes, mixed cases). Types are compared as well as
  values. It caught the mixed-`min` semantics error on its first run,
  and it visibly fails when division is sabotaged to integer division —
  a gate is only trusted after it has been watched failing.
- **N-way equivalence corpus** (35 pipelines + 30 generated
  permutations): every result-producing backend, byte-identical
  normalized output, with hand-computed goldens on shuffled fixtures so
  unanimous-but-wrong cannot pass. The previously *skipped* typed lanes
  for expression pipelines becoming unskipped was Phase 1's acceptance
  criterion. New goldens pin the discriminating semantics: float
  division through every lane, integer modulo fidelity
  (`sum(pop) % 5 = 4`), and the fold-swap case (`{a:b, b:a}` → 0, where
  sequential assignment converges to 1) — the last watched failing under
  a deliberately sabotaged emission in both typed lanes.
- **Sabotage tests**: the Merge method of parallel aggregations was
  deleted and the go-parallel lane diverged; predicate emission was
  flipped to integer division and the harness flagged `native=3
  vm=3.5`. Each guard's failure mode was demonstrated, not assumed.

One honest caveat surfaced by the campaign itself: parallel float
aggregation sums per-shard then merges, so results can differ from the
serial order in the last ~4 of 16 significant digits (§5.2, E2).
Floating-point addition is not associative; the equivalence gate's
normalization accepts this, and serial modes remain bit-identical.

## 5. Evaluation

**Setup.** Intel Core Ultra 9 275HX (24 hardware threads), 62 GB RAM,
Linux 6.17, Go 1.26.1. Dataset: 5,000,000-row CSV (115 MB), columns
`id:int, region:string(5 values), price:float, qty:int`, seeded PRNG.
Generated programs built once (`go build`, 1.4 s warm) and timed with
`/usr/bin/time -v`; wall clock and peak RSS are medians of 3 runs
(spread <5% except where noted). "Pre" binaries are built from the last
commit before the transpiler (`b1adab9`); "post" from current main
(`df354e2`). Output equality across variants was verified before timing
(E1, E3 exact; E2 to 12 significant digits, see §4).

### 5.1 Worked example 0: the per-row cost, isolated

The same predicate, `price * qty > 1000 && region != "south"`, compiled
four ways and measured with `testing.Benchmark`:

| implementation | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| Tier N, typed (`r.Price*float64(r.Qty) > 1000 && …`) | **3.0** | 0 | 0 |
| Tier N, record (`ssql.GetOr(r,"price",float64(0))*… `) | **28.0** | 0 | 0 |
| VM on Record (pre-existing path) | 1,261 | 1,088 | 9 |
| VM with static env (Tier V) | 1,245 | 1,096 | 12 |

The typed emission is 420× faster than the VM; the record emission —
which pays two schema lookups per field access — is 45× faster. Both
eliminate all per-row garbage: at 5M rows, the VM path allocates
**5.4 GB** of transient env maps for this one predicate; the native
paths allocate nothing. Tier V's value is *not* speed — it is that the
1.2 µs stays confined to the expression instead of degrading the
pipeline around it.

### 5.2 Worked example 1 (E1): filter + count, 5M rows

```
from big.csv | where -if-expr 'price*qty > 1000 && region != "south"'
             | group-by region -count n
```

| variant | wall (median) | peak RSS | vs pre-best |
|---|---:|---:|---:|
| interpreted exec (5-process pipe) | 10.22 s | 843 MB | — |
| pre, record mode (VM predicate) | 8.37 s | 1.18 GB | 1.0× |
| pre, **parallel mode requested** | 8.21 s | 1.11 GB | 1.0× |
| post, record mode (native GetOr) | 3.16 s | 865 MB | 2.7× |
| post, parallel mode (native) | **0.44 s** | **329 MB** | **19×** |

The third row is the cliff made visible: before Phase 1, asking for the
parallel backend with an `-if-expr` present produced record-mode
performance, because the expression ejected the whole pipeline. After,
the same command line runs 19× faster than the previous best generated
code and 23× faster than the interpreter, in 3.6× less memory.

### 5.3 Worked example 2 (E2): expression aggregation, 5M rows

```
from big.csv | group-by region -expr 'sum(price * qty) / count()' avg_rev
```

| variant | wall | peak RSS | notes |
|---|---:|---:|---|
| interpreted exec | 9.08 s | 2.44 GB | groups materialized, VM per element |
| pre, record mode | 6.92 s | 2.45 GB | same machinery, compiled harness |
| pre, typed/parallel mode | **hard error** | — | "-expr aggregations are Tier 3" |
| post, parallel (mergeable accumulators) | **0.43 s** | **328 MB** | 16× CPU, 7.5× memory |

This example is both a speedup and a *capability unlock*: the pre-work
typed backend refused the pipeline outright. The lowering's `Merge`
(sums and counts add across shards) is what lets it keep
`GroupByParallel`; per-group state is two words per accumulator term
instead of a `[]Record` of the group's rows.

### 5.4 Worked example 3 (E3): streaming fold, 5M rows

```
from big.csv | group-by region
    -stream-expr '{s:0, n:0}' '{s: s + price, n: n + 1}' 's/n' avg_price
```

| variant | wall | peak RSS |
|---|---:|---:|
| interpreted exec | 9.15 s | 1.91 GB |
| pre, record mode | 6.95 s | 1.80 GB |
| post, typed (serial — folds don't merge) | **1.68 s** | **29 MB** |

The CPU win (4.1×) is the smaller story. The fold ran under the VM *per
element over materialized groups*: 5M records held in memory to be
folded afterwards. The typed accumulator folds as rows stream through —
peak memory drops from 1.80 GB to 29 MB, a **62× reduction**, and is now
O(groups), independent of row count. This variant is serial (a
user-defined fold is not provably associative); it still beats the old
path on every axis.

### 5.5 Costs

- **Effort:** five increments over two days (plan estimate: 5–8 days),
  ~2,300 lines of implementation, ~1,460 lines of tests, ~490 lines of
  documentation. The test:implementation ratio of 0.63 understates the
  gate investment since the pre-existing equivalence harness was reused.
- **Compile cost:** `generate go` + `go build` adds ~1.4 s (warm module
  cache) — amortized in the first second of any 5M-row run, irrelevant
  for the repeated-run use cases code generation targets, real for
  one-shot small inputs (where the interpreter remains the right tool).
- **Complexity carried forward:** the walker's semantics tables must
  track expr-lang upgrades (the differential harness is the tripwire);
  the advisory-type plumbing adds a propagation obligation to record
  commands (unpropagated = safe fallback, so the failure mode of neglect
  is lost performance, not wrong answers).

## 6. Was it worth it?

**Yes, unambiguously — with one qualifier.** The case rests on three
legs:

1. **The cliff mattered more than the constant factor.** The most
   valuable single change was not 3 ns vs 1.2 µs; it was that one
   expression no longer decides the execution strategy of an entire
   pipeline (E1's pre-parallel row). Removing performance *cliffs* is
   worth more than removing performance *cost*, because cliffs are
   invisible at the command line — the user asked for parallel and
   silently got serial-record.
2. **Memory wins compound differently from CPU wins.** E3's 1.8 GB →
   29 MB is the difference between "fits on the laptop" and "doesn't"
   at 50M rows — a capability boundary, not a speedup.
3. **The correctness methodology made the risk acceptable.** A
   transpiler that is 99% semantics-faithful is a liability in a data
   tool. The differential harness converted "hope the subset is right"
   into "the subset is exactly what survives an executable oracle," and
   twice changed the design (mixed-`min`, count()-parsing) before wrong
   code shipped. Without that harness this work would have been
   irresponsible; with it, each phase landed with its gates green and
   watched failing first.

The qualifier: the payoff profile is specific to *generated, repeatedly
executed* code. The interpreter still uses the VM everywhere — a
deliberate scope cut, since interpreter startup dominates its per-row
costs at typical interactive sizes. Teams whose workloads never leave
the interpreter would get little from Phases 1–3 and should start where
we ended (record-mode advisory types) only if profiles say so.

## 7. Is this a generalizable technique?

We believe the *shape* generalizes well beyond ssql. Candidate hosts are
any system embedding a dynamic expression language into an execution
path that is otherwise compiled or code-generated: ORMs emitting native
filters, log/metrics pipelines (VRL-like languages), rule engines, ETL
frameworks, feature-store transforms, template engines.

The reusable recipe:

1. **Curated subset, not a full compiler.** Transpile the constructs
   whose host-language emission is *provably* semantics-identical;
   refuse everything else with a machine-readable reason. The subset is
   a whitelist grown from telemetry (`-explain`), never a best-effort
   translation. Most of our corpus expressions needed under 20 emission
   rules.
2. **A tier ladder, not a binary fallback.** The middle tier (VM with
   statically-built env) is cheap to build and strategically important:
   it decouples "this expression is slow" from "this pipeline lost its
   execution strategy." Any host with a planner that degrades on opaque
   operators has an analogous move.
3. **A differential oracle as the definition of correct.** The
   interpreter is executable ground truth; run both implementations over
   adversarial inputs and diff results *including types*. This is
   cheaper than a formal semantics and catches what code review cannot
   (we know, because it did). Corollary: reuse the interpreter's own
   front-end (parser + normalization) rather than re-implementing it —
   our worst near-miss came from re-parsing.
4. **Sabotage the gates once.** A differential harness you have never
   seen fail proves nothing; each of ours was shown to catch a
   deliberately introduced bug before being trusted.

The technique's limits are equally clear: it needs *static type
knowledge* at generation time (we had it in typed mode; we had to build
advisory plumbing for record mode, and its well-typed-column contract is
a real, documented constraint on messy data); it needs the interpreter
to be *available as an oracle*; and it repays effort only where the
generated artifact runs enough rows to amortize a compile. Within those
bounds — which describe a large class of data infrastructure — the
cost/benefit measured here (≈2,300 lines for 16–23× end-to-end, zero
correctness regressions across a 65-pipeline differential gate) argues
the technique should be a standard tool, not an exotic one.

## 8. Threats to validity

- Single machine, single OS, one dataset shape (uniform random, 5
  low-cardinality groups). High-cardinality group-bys would shift the
  E2/E3 memory ratios (accumulator count grows with groups).
- The 24-thread parallel numbers include I/O-parallel CSV reading;
  machines with fewer cores will see smaller E1/E2 multipliers (the
  record-native 2.7× and E3's serial 4.1× are core-count independent).
- "Pre" measurements use the real predecessor commit, but both were
  built with today's Go toolchain; historical toolchains might differ.
- The interpreter baselines include per-stage process and JSONL
  serialization overhead inherent to its architecture; they are the
  honest user-visible baseline, not a VM-only measurement (that is
  §5.1's job).

## 9. Provenance

Phases and gates: `expr-transpiler-implementation-plan.md` (design and
per-phase shipping notes, §8–§9). Commits `6531859` (Phase 1), `37c5bfd`
(1.5), `ac2f88a` (2), `532fe9e` (3), `df354e2` (4). Benchmark programs
were generated by the shipped `generate go` on the commands shown in §5
and built against the working tree; the micro-benchmark source is
reproduced in the equivalence/bench test suite
(`cmd/ssql/commands/expr_go_bench_test.go`,
`expr_go_differential_test.go`).
