# Typed Auto-Parallel Proposal

Reference: DFC091
Created: 2026-05-02
Last modified: 2026-05-02

[Back to Index](./README.md)

**Status:** Prototype + benchmarks, 2026-05-02. Not yet implemented.

This proposal merges `SSQLGO=typed` (serial) and `SSQLGO=parallel`
into a single `SSQLGO=typed` mode that automatically picks the
best execution shape per pipeline. The user no longer chooses;
the codegen does.

A working prototype was built (one command, `sort`) and end-to-end
tested. Findings include one surprising negative result that shapes
the design.

## 1. Motivation

Today users see two typed modes:

- **`SSQLGO=parallel`** — what they want when their workload is
  parallelisable.
- **`SSQLGO=typed`** — what they're *forced* into when the pipeline
  contains a Tier-3-deferred command (`sort`, `distinct`, `limit`,
  `top`, `union`, `cast`, `update`, `include`, `exclude`, `rename`,
  `offset`, plus group-by `-presorted`). Today every one of those
  commands calls `rejectParallelMode("X")` and aborts the
  generation with a fallback message.

The choice between them is "did your pipeline happen to use a
parallel-supported command", not "do you want fast or slow." Wrong
axis. Almost no user opts into single-threaded compute when the
alternative exists; they get serial because they couldn't avoid
it.

The merged mode would:

1. **Default to parallel** for parallel-friendly ops (`from`,
   `where`, `join`, `group-by`).
2. **Auto-insert `Stream.Serial()` boundaries** before any op that
   needs `iter.Seq[T]` input.
3. **Optionally fall back to a non-parallel source** when the
   pipeline has no parallel-friendly work to do (see §3).
4. **Print the chosen plan to stderr** under `--explain`.

Side benefit: the [mixed-mode pipelines proposal](mixed-mode-pipelines-proposal.md)
becomes simpler. The mode-tagging axis collapses from
`{typed-serial, typed-parallel, Record}` to `{typed, Record}`,
and the `Stream.Serial()` boundary mechanism is reused for the
typed→Record adapter.

## 2. Prototype (one command, end to end)

To validate the boundary-insertion mechanism, `cmd/ssql/commands/sort.go`
was modified to auto-insert `Stream.Serial()` instead of
rejecting:

```go
if prevIsStream {
    serialVar := inputVar + "Serial"
    boundaryCode := fmt.Sprintf("%s := %s.Serial()", serialVar, inputVar)
    boundaryFrag := lib.NewStmtFragment(serialVar, inputVar, boundaryCode,
        []string{"github.com/rosscartlidge/ssql/v4/typed"}, "")
    boundaryFrag.InputTypedSchema = prevSchema
    boundaryFrag.OutputTypedSchema = prevSchema
    if err := lib.WriteCodeFragment(boundaryFrag); err != nil {
        return err
    }
    inputVar = serialVar
    fmt.Fprintf(os.Stderr, "[auto-serial] inserted Stream.Serial() before 'sort' …\n")
}
```

Test pipeline (previously rejected by `SSQLGO=parallel`):

```bash
$ (export SSQLGO=parallel
   ssql from parquet shuffled.parquet -columns relationship \
     | ssql sort -desc relationship | ssql to table) | ssql generate go
```

Generated `main()`:

```go
records := typed.ReadParquetParallel[ShuffledRow](*flagInput, runtime.GOMAXPROCS(0), typed.ParquetColumns("relationship"))
recordsSerial := records.Serial()                                     // ← auto-inserted
sorted := typed.SortByDesc(func(r ShuffledRow) int64 { return r.Relationship })(recordsSerial)
typed.WriteTableToWriter(sorted, os.Stdout)
```

The pipeline runs and produces correct output identical to a
pure-serial run. **Mechanism works.**

## 3. The negative result that changes the design

After validating correctness, we benchmarked the auto-serial
approach against pure-serial on the same machine + corpus:

| Mode | Wall | User | Output |
|---|---:|---:|---|
| `SSQLGO=parallel` (auto-serial at sort) | **7.28 s** | 11.9 s | 14.6 M rows |
| `SSQLGO=typed` (pure serial) | **4.80 s** | 5.27 s | 14.6 M rows (identical) |

**Pure serial is 1.52× faster than auto-serial-parallel.**

This is *the same lesson* as `claude/concurrency.md` §1: per-row
channel transit on `Stream.Serial()` costs ~100 ns × N rows. On
14.6 M rows that's ~1.5 s of pure overhead for the fan-in
boundary alone. When the pipeline has no parallel-friendly work
*between* source and the first serial-only operator, parallelism
buys nothing — but you still pay the Serial() cost at the
boundary.

Net: a naive "always go parallel until forced to serialise" rule
makes some pipelines slower. Bad UX trap — the user merged the
modes for simplicity but ended up with a regression on common
shapes.

## 4. The smarter rule

Look at the whole pipeline before generating code. Pick the
source's parallelism by the parallelism *reach* downstream:

- **Reach = 0** (the very next op is serial-only): use the serial
  source primitive (`typed.ReadParquet`, `typed.ReadCSV`,
  `typed.ReadDelim`). Don't pay for `Parallel*` reading +
  `Serial()` fan-in when nothing benefits.
- **Reach ≥ 1** (at least one parallel-friendly op consumes
  the source): use the parallel source primitive (`*Parallel`).
  Insert `Serial()` at the first serial-only boundary.

For the prototype pipeline (`from parquet | sort | to table`):

- Source: `from parquet`
- Next op: `sort` — serial-only
- Reach = 0 → use `typed.ReadParquet` (serial), no Stream, no
  Serial() boundary

That gives the same 4.8 s as today's `SSQLGO=typed` — no
regression. The user just types `SSQLGO=typed` and gets the
right thing.

For a pipeline like `from parquet | where | join | group-by | sort | to table`:

- Reach = 4 (where/join/group-by all run on Stream/iter.Seq
  parallel forms)
- → use `typed.ReadParquetParallel`
- → run where, join, group-by in parallel
- → Serial() boundary before sort
- → serial sort + sink

That gets the parallel speedup on the heavy stages and pays the
Serial() cost only once, at a point where the upstream
parallelism has already done useful work that exceeds the cost.

## 5. Implementation

### 5a. Per-command capability declarations

Each command's typed-mode codegen path declares what shapes it
accepts and produces:

```go
// In each command's typed-mode codegen helper:

const (
    InputAny     = iota // accepts iter.Seq[T] OR Stream[T]
    InputSeq            // requires iter.Seq[T] (must be serial)
    InputStream         // requires Stream[T] (parallel only)
)

type ShapeRequirements struct {
    Input  int  // InputAny / InputSeq / InputStream
    Output int  // InputAny / InputSeq / InputStream
}
```

Today's parallel-incompatible ops (sort, distinct, limit, etc.)
declare `Input: InputSeq, Output: InputSeq`.

Today's parallel-friendly ops (where, join, group-by, the
sources, the sinks) declare what they currently emit:
`Input: InputAny, Output: matches input shape`.

### 5b. Two-pass codegen

The fragment assembler currently walks fragments once and
produces the Go program. We extend it to pre-walk:

1. **Pass 1 (planning):** for each command, look up its
   capability declaration. For each `InputSeq` op, walk
   *backwards* to find the source. Mark the source as
   "needs serial" if NO `InputAny` op exists between source and
   this `InputSeq` op (= reach == 0). Otherwise, mark the
   `InputSeq` op as "needs Serial() boundary."
2. **Pass 2 (codegen):** sources read their "needs serial" tag
   and emit `ReadParquet` vs `ReadParquetParallel`. Each
   `InputSeq` op with a "needs boundary" tag emits a
   `Stream.Serial()` fragment first, then its existing serial
   codegen.

The capability-declaration list is small (~12 commands; mostly
just InputSeq for the serial-only set). The planner is ~50
lines.

### 5c. Source primitive selection

The currently-rejected commands need a way to influence the
source. Two options:

**Option A:** sources always default to parallel. After Pass 1,
if any `needs serial` is set on the source, the codegen rewrites
the source fragment to use the serial primitive.

**Option B:** sources expose a `wantSerial` field on their
fragment that the planner sets. Source codegen emits the right
primitive based on that field's value.

Option B is cleaner — the source's emit-time decision is local
and explicit. Option A requires fragment surgery after the fact.

### 5d. SSQLGO simplification

After the merge:

- **`SSQLGO=typed`** (default for typed Go) — auto-parallelise
  per-pipeline.
- **`SSQLGO=parallel`** — DEPRECATED alias for `SSQLGO=typed`
  with `--no-fallback-to-serial-source` (i.e. always use the
  parallel source, even when reach=0). Useful for benchmarking
  / when the user knows their workload benefits.
- **`SSQLGO=typed-serial`** (new) — force serial throughout.
  Useful for debugging, reproducibility, or RAM-constrained
  environments. Skip Stream entirely.
- **`SSQLGO=1`** (Record-mode, unchanged).

A `--explain` flag (or `-e` short form) emits the chosen plan
to stderr, mirroring `generate ssql -explain`:

```
[plan] source=ReadParquetParallel(15 shards from row groups)
[plan] where=Stream.Where (parallel)
[plan] sort=serial (no parallel sort impl); inserted Stream.Serial() before
[plan] to table=serial sink
```

So the user can see what was chosen even when they don't ask
for it.

## 6. Migration

External-facing changes:

- `SSQLGO=parallel` keeps working (as a deprecated alias) for
  backwards compat. Print a deprecation note to stderr.
- All 12 `rejectParallelMode("X")` call sites get rewritten to
  the auto-Serial pattern. Mechanical change.
- The new planner is a pre-pass in `lib.AssembleCodeFragments`
  (which already walks the fragment list).
- `SSQLGO=typed` becomes the recommended default. Doc updates
  to the codelab + reference.

Estimated effort:

- Planner pass: ~80 LOC (~1 day with tests).
- Per-command capability declarations: ~10 LOC × 12 commands =
  ~120 LOC, plus removing the `rejectParallelMode` call sites.
  Mechanical.
- `--explain` output: ~30 LOC.
- Doc updates: codelab, reference, journal entry.
- **Total: ~1-2 days for a careful land**, plus benchmarks.

Risk profile is low because the prototype already proved the
mechanism is sound; we're just adding a planner that picks the
best variant.

## 7. Outstanding question — sources are wider than file readers

The "parallel reach" analysis is straightforward for file
sources. But what about `from ssh` (Record-mode-only currently)
or `from catalog` (catalog-aware distributed)? They're not
parallel in the same way; the planner needs to treat them as
"input is whatever the source produces" without trying to
reason about their internals. For v1, those sources stay
unchanged — they don't emit typed fragments today, so the
planner never sees them.

## 8. Caveats and edge cases (from prototype)

1. **`Serial()` channel cost is real on large datasets** — ~100
   ns/row, ~1.5 s on 14.6 M rows. The planner MUST avoid
   inserting it when the source has no parallel work to do.
   Without the smarter rule, naive auto-parallel is *slower*
   than pure serial.
2. **Output IsStream tracking** — fragments after a serial-only
   op emit `IsStream=false`. Subsequent sinks (e.g. `to csv`,
   `to table`) already key off the previous fragment's `IsStream`
   (we fixed this in v4.36 when adding GroupByParallel). No
   change needed here.
3. **Empty inputs** — a `Serial()` on an empty Stream is a
   no-op; the iter.Seq is empty. Handled correctly by Stream
   today.
4. **Process-substitution joins** — `join <(ssql from x.csv)`
   has a func fragment with its own internal pipeline. The
   planner should recurse into func bodies. ~10 LOC; tested by
   adding a join with an inner sort.
5. **Reach calculation has to skip already-serial fragments** —
   if the user writes
   `from parquet | sort | where | sort | to table`, the second
   sort doesn't need a boundary because the first one already
   serialised. Easy: stop reach-walking at the first serial op.

## 9. What the prototype validated and what it didn't

**Validated:**
- The Serial() boundary insertion mechanism works.
- The generated Go compiles and runs.
- Output is identical to the pure-serial reference.
- Each command's capability declaration is small (the prototype
  changed ~10 lines in one command).

**Not validated by the prototype:**
- The two-pass planner — wasn't built; the prototype just
  always emitted `ReadParquetParallel + Serial()` regardless of
  reach. The slow benchmark made the case for the two-pass
  planner.
- `-explain` output — not built.
- Migration of all 12 reject-call-sites — only `sort` was
  converted.
- Func-fragment recursion for joins.

These are all bounded engineering work and the prototype gives
enough confidence to commit to the design.

## 10. Recommendation

Build it. The combined mode is materially simpler for users
(one knob, the right defaults), the per-command refactor is
mechanical, and the planner is small. The negative result from
the prototype taught us the right rule (parallelism reach
analysis); without it we'd have shipped a regression on common
short pipelines.

Sequencing:

1. Land the planner (pass 1) + capability declarations as a
   no-op refactor — the existing reject paths still apply when
   the planner determines reach=0 means "downgrade source to
   serial" and an op declares serial-only. Verify nothing
   regresses.
2. Convert each `rejectParallelMode` call site to insert
   `Stream.Serial()` — one PR per command (or one big PR;
   mechanical).
3. Add `--explain` plan output.
4. Deprecate `SSQLGO=parallel` (keep as alias, print one-line
   warning).
5. Update codelab/reference to recommend `SSQLGO=typed`.

## See also

- [`claude/concurrency.md` §1](../../claude/concurrency.md) —
  "Channels are too expensive on per-row hot paths." The
  prototype's negative result is a fresh instance of this
  rule — Serial() costs ~100 ns/row.
- [`mixed-mode-pipelines-proposal.md`](mixed-mode-pipelines-proposal.md) —
  the "implicit auto-segmentation" pattern is the same one
  applied to typed-vs-Record boundaries; the planner here
  generalises naturally.
- [`typed-codegen-proposal.md`](typed-codegen-proposal.md) §5d —
  current `SSQLGO=parallel` codegen status; this proposal
  collapses it into `SSQLGO=typed`.
- Prototype patch: `cmd/ssql/commands/sort.go` (uncommitted) —
  the auto-Serial insertion for sort. Revert after writeup;
  the real implementation should land via the structured
  planner approach in §5.
