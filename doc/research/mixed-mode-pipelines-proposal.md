# Mixed-Mode Pipelines Proposal

**Status:** Design exploration only (2026-04-28). Not committed work.

This doc lays out the design space for **mixed-mode pipelines** —
ssql pipelines where some stages run in the typed runtime
(`iter.Seq[T]` over user struct types) and others run in the
Record runtime (`iter.Seq[Record]` over `map[string]any`), with
explicit conversion fragments at the boundaries.

The original `typed-codegen-proposal.md` deferred this in §6 as
"adds significant assembler complexity, defer indefinitely." This
doc revisits that decision in light of:

- **Real-world numbers** showing typed-parallel codegen at 90× the
  CLI baseline on a user corpus (`journal/2026-W18.md`,
  2026-04-28). The performance gap between typed-parallel and
  Record is now too large to leave unbridged.
- **A long tail of useful but non-typed commands** —
  `-if-expr`/`-set-expr` (expression language), `signal`
  (FFT/convolve), `pivot`, `merge -catalog`, `from ssh`,
  `from catalog`. Each one represents users who can't get the
  typed-parallel speedup at all today.
- **A short list of new typed runtime features** that would also
  benefit from being mixable — e.g. typed could have a
  `from parquet` reader but no `pivot`, so a parquet→pivot
  pipeline currently can't use typed at all.

The pitch: stop treating "typed mode" as binary. Allow it to
apply to the *parts of the pipeline that benefit*, with thin
adapter fragments at the boundaries. A pipeline that's 80%
typed-parallel and 20% Record is much faster than 100% Record,
even with conversion overhead — and the alternative ("rewrite
the whole pipeline before any of it gets faster") doesn't
survive contact with the long tail.

## 1. Concrete use cases

These are real pipelines that don't work in pure typed mode today:

### 1a. Parquet read + expr-lang filter + typed group-by

```bash
ssql from parquet events.parquet \
    | ssql where -if-expr "ip in ('10.0.0.0/8') and severity > 3" \
    | ssql group-by service -count n -avg latency mean
```

`from parquet` and `group-by` work great in typed-parallel today.
`where -if-expr` doesn't (Tier 3, deferred — would need
expr-lang → Go AST translation). User wants typed for the
read+aggregate phase but Record for the expression filter.

**Today:** the whole pipeline runs in Record, paying
14× more memory and 3× more wall time than necessary.

**With mixed mode:** Parquet read + filter typed-parallel; one
conversion to Record for the expr-filter; one conversion back
to typed-parallel for the group-by; aggregate result. The
conversions cost ~1 s on 14.6 M rows. Net: the typed-parallel
phases save much more than that.

### 1b. SSH pushdown + typed local processing

```bash
ssql from ssh prod-host /var/log/access.csv \
    | ssql where -if status ge 500 \
    | ssql group-by url -count n
```

`from ssh` is Record-only (it's an ssql-specific distributed
feature; the wire format is JSONL). The post-filter
`group-by` would benefit from typed-parallel.

**Today:** local processing in Record; pays the JSONL parse +
`map[string]any` allocation overhead per row.

**With mixed mode:** Record stage wrapping the SSH pushdown,
conversion to typed at the local boundary, typed-parallel
group-by. The conversion is at the small end of the pipeline
(after SSH-side filtering) — likely well below 1M rows — so
the cost is negligible.

### 1c. Signal processing in the middle of a parallel pipeline

```bash
ssql from parquet sensor.parquet \
    | ssql where -if value gt 0 \
    | ssql signal fft -window 1024 \
    | ssql group-by frequency_band -avg power mean
```

`signal fft` is Record-only — it consumes rows and produces a
different shape (time-domain → frequency-domain). The before
and after stages would benefit from typed-parallel.

**Today:** the whole pipeline is Record-mode.

**With mixed mode:** typed-parallel parquet read + filter →
convert to Record → FFT → convert to typed → typed group-by.
The Record phase is intentionally slow (it's signal
processing); the typed phases reclaim the read and aggregate
wins.

### 1d. Hot column projection + cold full-row update

```bash
ssql from parquet wide_table.parquet \
    | ssql group-by category -count n -sum revenue total \
    | ssql update -if total gt 1000000 -set tier "premium" \
    | ssql to csv summary.csv
```

In a wide-table parquet read, `group-by` only needs
`category`, `revenue`. `update` adds a derived field with a
literal. `to csv` writes the full output.

**Today:** typed mode handles all of these *individually* but
Record-mode is forced on the whole pipeline if the user wants
some unrelated Tier 3 feature elsewhere.

**With mixed mode:** all stages stay typed-parallel. Mixed
mode mostly helps when there *is* a deferred command — but
the cleanest design also avoids regressing pipelines that are
already typed-clean.

## 2. The design space

Two axes worth considering separately:

### 2a. Boundary placement: implicit vs explicit

**Implicit (auto-segment):** the codegen looks at each command's
"preferred mode" and segments the pipeline at boundaries
between non-typed and typed-supported commands. Conversion
fragments are inserted automatically.

- **Pro:** invisible to the user; existing pipelines just get
  faster.
- **Con:** users can't override (e.g. force a typed command into
  Record because Record's version handles a particular edge
  case better). The "auto-detected boundary" is a new place
  for things to go wrong.

**Explicit (user-marked):** users tag each command with `--mode=record`
or `--mode=typed`, defaulting to "the best mode this command
supports." Mixed mode happens when adjacent commands have
different modes.

- **Pro:** predictable; debuggable.
- **Con:** verbose; users now have to think about modes.

**Hybrid:** default to implicit auto-segmentation, but let users
override with `--mode=...` per command. This is what the
research community usually settles on (e.g. SQL `/*+ HINTS */`).
Probably the right answer for ssql.

### 2b. Boundary direction: typed → Record vs Record → typed

The two directions have very different costs and complexities.

**typed → Record:** straightforward.
- Source has a known struct schema (compile-time).
- Conversion: reflection-cached "struct → Record" — build the
  schema and value-extractor closures once at codegen time;
  per-row path is reflection-free.
- Cost per row: ~50 ns (1 map allocation + N field copies).
- Stream→Record: must `Serial()` first (loses parallelism), then
  per-row convert. The `Serial()` is the dominant cost on
  parallel-mode boundaries.

**Record → typed:** harder.
- The Record's runtime schema is what it is — no compile-time
  type information.
- The downstream typed stage *does* need a static struct type.
  Where does it come from?
  - **Option A:** the downstream typed code's first call (e.g.
    `where -if x gt 5`) tells us field `x` is needed; we
    synthesise a minimal struct with just `x`. But then
    further-downstream stages might need fields not in that
    struct → schema explosion.
  - **Option B:** sample the Record stream's first N rows at
    codegen time, infer types like `SampleCSVSchema` does.
    Brittle — first-row inference can be wrong.
  - **Option C:** the user provides the struct via a hint flag
    (`--into MyStruct`). Verbose but explicit.
- Cost per row: ~100 ns (N map lookups + N type assertions +
  field writes).

A reasonable v1 ships **only the typed → Record direction**.
That's enough for use cases 1a and 1b (where the typed phase
is at the start of the pipeline) and most of 1c (typed phase
at start, then Record-mode ending). The harder direction
(Record → typed) lands in v2 once we have running production
experience with the easy direction.

### 2c. Stream[T] (parallel) ↔ Record

This is just Stream[T] → iter.Seq[T] (via `Serial()`) → Record,
i.e. the typed→Record path with an extra hop. Same complexity.

The reverse direction (Record → Stream[T]) is *much* harder —
it would need to materialise the Record stream into a slice,
then partition into shards. Probably defer to v3 unless a
specific use case forces it.

## 3. Implementation sketch

Three pieces of new code:

### 3a. Adapter fragments

Two new fragment kinds in `lib.CodeFragment`:

```go
const FragmentTypeTypedToRecord = "typed_to_record"
const FragmentTypeRecordToTyped = "record_to_typed"  // v2
```

These fragments emit a small adapter function:

```go
// emitted by the typed→Record boundary fragment
func toRecord(seq iter.Seq[ShuffledRow]) iter.Seq[ssql.Record] {
    schema := ssql.NewSchema([]string{"a_kind", "a_name", ...})
    return func(yield func(ssql.Record) bool) {
        for v := range seq {
            r := ssql.NewRecordFromSchema(schema, []any{
                v.AKind, v.AName, ...,
            })
            if !yield(r) {
                return
            }
        }
    }
}
```

Schema-sharing rule applies: build the `*Schema` once outside
the loop; reuse across rows. No per-row allocation beyond the
Record itself.

### 3b. Mode tagging

Each command's codegen function needs to declare what mode(s)
it supports:

```go
// In each command's typed-mode codegen path:
type CommandModeSupport struct {
    Typed    bool  // serial typed (iter.Seq[T])
    Parallel bool  // parallel typed (Stream[T])
    Record   bool  // record runtime (iter.Seq[Record])
}
```

Today this is implicit (each command's `generateXxxCode` checks
`typedMode()` / `parallelMode()` and dispatches). Refactor to
expose this as a structured value so the assembler can decide
where to insert boundaries.

### 3c. Pipeline segmentation pass

A new step in `lib.AssembleCodeFragments`:

1. Walk fragments in order.
2. For each fragment, determine its declared mode (typed /
   parallel / record).
3. Find runs of contiguous same-mode fragments.
4. At each mode-change boundary, insert an adapter fragment.
5. Validate that the adapter direction is supported (v1: only
   typed→Record).
6. Emit the assembled program.

Most of the existing assembler logic doesn't change. The
segmentation pass is ~200 lines of new code. The big risk is
edge cases (e.g. a func fragment for a join right-side
contains its own mini-pipeline that might need its own
segmentation pass).

## 4. Cost analysis

When does mixed mode pay off?

**Conversion cost:** ~50 ns per row for typed→Record. On 14.6 M
rows that's ~700 ms wall on a single core (or ~50 ms on a
shard-parallel converter, but we don't have one yet).

**Typed-mode savings vs Record:** highly workload-dependent. On
the user-corpus group-by benchmark, typed-parallel was 12.6 s
faster than Record (90× speedup vs CLI baseline). For a
filter-heavy pipeline with selective predicates, the savings
are smaller because the data shrinks early.

**Break-even:** a single mixed-mode boundary adds ~1 s on 14 M
rows. As long as the typed-only stages save ≥1 s, the
boundary pays for itself. For most realistic workloads this
is easy to clear — typed-parallel saves 5-10 seconds per
read+aggregate phase, vs 1 second of conversion overhead.

**Anti-pattern:** a pipeline that's 95% Record and 5% typed
shouldn't bother with mixed mode. The savings on the 5%
typed phase will rarely cover the conversion overhead.
Heuristic: only insert boundaries when the typed segment is
"large enough" — measured in expected row count × typed-stage
saving per row. Detect at codegen time.

## 5. Failure modes

### 5a. Schema drift

Record-mode lets fields appear/disappear per row.
Mid-pipeline, a Record might have fields A and B; a few rows
later, just A. The typed→Record adapter handles this fine
(it only writes the fields it knows about), but Record→typed
is broken: the struct expects all fields. Mitigation: use
nullable struct fields (pointer types) for fields that might
be absent. Verbose but works.

### 5b. Type mismatches

Record holds `any`; a typed struct expects `int64`. If the
Record's value is an `int64`, fine; if it's a `string` (CSV
without parser), the conversion either panics or silently
zeroes. The Record→typed adapter must be strict (fail on
mismatch) by default, with a `--lossy` opt-in.

### 5c. Implicit Stream→Serial penalty

A typed-parallel `from parquet` followed by a Record-mode
`pivot` would force a `Stream[T].Serial()` call before
conversion. Users who don't realise this might assume the
whole pipeline runs in parallel. Mitigation: log "inserting
serial boundary at command X" to stderr when `--explain` is
on (mirroring `generate ssql -explain`).

### 5d. Func fragment recursion

A `join <(... | ssql signal fft ...)` has a process-substitution
inner pipeline that might itself need mixed-mode handling.
The segmentation pass needs to recurse into func fragments.
Not insurmountable but adds test surface.

## 6. Phased rollout

**Phase A (v1):** typed → Record adapter only. Implicit
auto-segmentation. Stream→Record requires implicit `Serial()`
(documented). Add `--explain` to surface inserted boundaries.
Estimate: 1 week of work + tests + benchmark.

**Phase B (v2):** Record → typed adapter via "explicit struct
hint" (option C from §2b). User passes `--into MyStruct`
where needed. Allows pipelines like SSH → typed-aggregate.
Estimate: ~2 weeks.

**Phase C (v3):** Record → typed via inference (option B from
§2b). Sample first N rows. More magic but lets users skip
the `--into` flag. Optional.

**Phase D (deferred):** Record → Stream[T] (i.e. converting a
sequential Record stream into a parallel typed stream). Useful
for pipelines that produce few rows but want parallel
post-processing. Probably never worth the complexity unless a
specific need surfaces.

## 7. Open questions

- **Is there a 4th mode worth considering?** I.e. some kind of
  *batched columnar* mode that's neither row-by-row Record nor
  row-by-row typed. Arrow batches? Parquet column chunks?
  Could be a different proposal entirely.
- **Should `to csv` after a Record stage just ... work?** Today
  Record-mode's `to csv` calls `ssql.WriteCSV`; typed-mode's
  calls `typed.WriteCSV[T]`. With mixed mode, the choice
  depends on the *actual* upstream — handle this naturally as
  part of the boundary-detection pass.
- **What about generate sql?** The SQL assembler reads the
  same fragment stream as generate go. Mixed-mode's adapter
  fragments would need to be no-ops in the SQL assembler
  (they have no SQL equivalent — SQL is already columnar +
  typed). Should be straightforward.
- **Performance regression test.** Need a benchmark that
  forces mixed mode (e.g. a pipeline with `signal fft` in the
  middle of typed-parallel) to ensure the adapter cost
  matches the model. Without this, performance drift is
  hard to catch.

## 8. Why this might be worth doing now

The original "deferred indefinitely" call was made before:

- **Typed-parallel codegen shipped** (v4.36.0) — turning the
  performance gap between typed and Record from "interesting"
  into "dramatic" (90×). Mixed mode previously meant trading
  off "fast typed" against "convenient Record"; today the
  trade-off is "fast typed" against "*very slow* Record."
- **Real-corpus measurements landed** — we now know what
  typical user pipelines look like and where they spend time.
  The adapter-cost analysis in §4 is grounded in numbers, not
  guesses.
- **The long-tail commands aren't shrinking.** Each new typed
  feature (Tier 3 codegen) is ~weeks of work; the deferred
  commands (`-if-expr`, `signal`, `pivot`, `from ssh`,
  `from catalog`, `merge -catalog`) probably won't all get
  typed implementations even in 2026. Mixed mode is the only
  way to ship typed wins to users of those features without
  waiting indefinitely.

The cost of NOT doing mixed mode is invisible — users with
deferred commands silently take the Record-mode path and don't
report it as a problem. But the headline numbers in `README.md`
won't apply to them, and the gap will widen as typed-parallel
keeps improving.

## See also

- [`typed-codegen-proposal.md`](typed-codegen-proposal.md) §6 —
  the original "Mixed pipelines — defer indefinitely" entry
  that this doc revisits.
- [`typed-codegen-tier3-roadmap.md`](typed-codegen-tier3-roadmap.md) —
  the deferred-command list that mixed mode would unblock.
- [`generate-go-flags-proposal.md`](generate-go-flags-proposal.md) —
  the `-optimise` flag, which would compose with mixed mode
  (the optimiser could re-segment a pipeline after applying
  rewrites).
- [`../../journal/2026-W18.md`](../../journal/2026-W18.md)
  2026-04-28 entry — five-mode comparison on a 72-thread
  Xeon, showing the 90× CLI→typed-parallel speedup that
  motivates wanting mixed mode in the first place.
