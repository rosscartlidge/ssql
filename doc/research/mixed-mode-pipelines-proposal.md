# Mixed-Mode Pipelines Proposal

**Status:** Design (2026-04-28, refined 2026-05-02). Now reframed as
**Phase B of the unified-typed-mode plan**: builds on the planner
abstraction introduced in [`typed-auto-parallel-proposal.md`](typed-auto-parallel-proposal.md)
(Phase A) by adding a second boundary kind to the same planner.

**The unified vision:** any pipeline that ssql can compile in
Record mode should also compile in typed mode, with the codegen
automatically picking — *per pipeline stage* — between
`Stream[T]` (parallel typed), `iter.Seq[T]` (serial typed), and
`iter.Seq[Record]` (Record fallback). Users type `SSQLGO=typed`
and the planner produces "the fastest possible code for this
specific pipeline." As we add typed/parallel implementations of
currently-Record-only commands over time, every user's
pipelines get faster without them touching anything — same shape
as a query optimiser.

This proposal is the second of two phases (the other being
`typed-auto-parallel-proposal.md`). They share the same planner
abstraction; only the *boundary kind* differs:

| Boundary kind | Source shape | Sink shape | Inserted adapter | Phase |
|---|---|---|---|---|
| Parallelism boundary | `Stream[T]` | `iter.Seq[T]` (serial-only typed op) | `Stream.Serial()` | **A** (auto-parallel) |
| Mode boundary | `iter.Seq[T]` | `iter.Seq[Record]` (Record-only op) | reflection-based struct→Record | **B** (this doc) |
| Mode boundary (reverse) | `iter.Seq[Record]` | `iter.Seq[T]` (typed-only op) | `--into` hint or sample-infer | C (deferred) |

The Phase B work mostly reuses Phase A's planner + assembler;
the additions are the new boundary kind, the reflection-based
adapter, and updating the per-command capability declarations to
include "Record-only".

This doc lays out the design space for **mixed-mode pipelines** —
ssql pipelines where some stages run in the typed runtime
(`iter.Seq[T]` over user struct types) and others run in the
Record runtime (`iter.Seq[Record]` over `map[string]any`), with
explicit conversion fragments at the boundaries.

The original `typed-codegen-proposal.md` deferred this in §6 as
"adds significant assembler complexity, defer indefinitely." This
doc revisits that decision in light of:

- **Real-world numbers** showing typed-parallel codegen at 215× the
  CLI baseline on a user corpus (`journal/2026-W18.md`,
  2026-04-29 entry, after the v4.37.3 multi-row-group Parquet
  default landed). The performance gap between typed-parallel and
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

## 3. Implementation sketch (Phase B = adapter + capability extension)

The Phase A planner already walks fragments, reads per-command
capability declarations, and inserts boundary fragments where
shapes don't match. Phase B adds:

### 3a. Capability declarations get a third shape

The Phase A planner declares per-fragment input/output shapes:

```go
// From phase A (auto-parallel proposal):
type Shape int
const (
    ShapeStream    Shape = iota // typed.Stream[T]
    ShapeSeqTyped               // iter.Seq[T]
    ShapeSeqRecord              // iter.Seq[Record]   ← new in Phase B
)

type Capabilities struct {
    AcceptsInput Shape
    Produces     Shape
}
```

Most commands already have a typed-mode codegen path emitting
either `Stream[T]` or `iter.Seq[T]`. **In Phase B we add a
`ShapeSeqRecord` declaration for commands that ONLY have a
Record-mode codegen** — the Tier 3 list: `signal *`,
`pivot`, `merge -catalog`, `from ssh`, `from catalog`, plus
the `-if-expr`/`-set-expr` flag forms of `where` / `group-by` /
`update`.

These commands' typed-mode codegen path doesn't need a real
implementation — they just emit a fragment with
`Capabilities.Produces = ShapeSeqRecord` (or `AcceptsInput`).
The planner takes care of the rest.

### 3b. New boundary type: `iter.Seq[T]` → `iter.Seq[Record]`

The planner already inserts `Stream.Serial()` adapters at the
parallel→serial-typed boundary (Phase A). Phase B registers a
second adapter:

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

Generated once per typed→Record boundary, parameterised by the
upstream struct's `OutputTypedSchema`. Schema-sharing rule
applies (build the `*Schema` once outside the loop). No per-row
allocation beyond the `Record` itself — about 50 ns/row.

The planner inserts this exactly the same way Phase A inserts
`Stream.Serial()`: when the next fragment's `AcceptsInput` is
`ShapeSeqRecord` but the previous fragment produces
`ShapeSeqTyped` (or `ShapeStream`, in which case BOTH a
`Stream.Serial()` and a `toRecord()` get inserted in sequence).

### 3c. Stream→Record (combined boundary)

A `Stream[T]` followed by a Record-only op needs both adapters:

```go
recordsSerial := records.Serial()           // Stream→iter.Seq[T]
recordsAsRecords := toRecord(recordsSerial) // iter.Seq[T]→iter.Seq[Record]
```

The planner emits both fragments. Same mechanism as Phase A
(boundary insertion); just two adapters chained. No new
machinery beyond the per-shape adapter registry.

### 3d. Reverse direction (Record → typed) — deferred to Phase C

Going the other way (a Record-only source like
`from ssh` followed by a typed `where`) needs a struct hint
because Records don't carry a static type. Three sub-options
discussed in §2b of the original draft. Defer to Phase C — the
forward direction (typed→Record) handles all four motivating
use cases below.

## 4. Cost analysis

When does mixed mode pay off?

**Conversion cost:** ~50 ns per row for typed→Record. On 14.6 M
rows that's ~700 ms wall on a single core (or ~50 ms on a
shard-parallel converter, but we don't have one yet).

**Typed-mode savings vs Record:** highly workload-dependent. On
the user-corpus group-by benchmark, typed-parallel-Parquet was
~15 s faster than Record (215× speedup vs CLI baseline once the
multi-row-group Parquet default unlocked the parallelism). For a
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

The phasing has been revised since the original draft. With the
unified-planner framing, the work now lays out as:

**Phase A (auto-parallel):** see
[`typed-auto-parallel-proposal.md`](typed-auto-parallel-proposal.md).
Build the planner abstraction; add the `Stream[T]` ↔
`iter.Seq[T]` boundary; collapse `SSQLGO=parallel` into
`SSQLGO=typed`. Foundation for everything that follows.
Estimate: ~2-3 days. Ships as v4.39.0.

**Phase B (this doc — typed → Record adapter):** add the
`iter.Seq[T]` → `iter.Seq[Record]` boundary kind. Each
Record-only command (the Tier 3 list — `signal`, `pivot`,
`merge -catalog`, `from ssh`, `from catalog`, plus
`-if-expr`/`-set-expr` flag forms) declares
`Capabilities.AcceptsInput = ShapeSeqRecord`. The planner
inserts the `toRecord` adapter automatically. Stream→Record
boundaries chain `Stream.Serial()` + `toRecord()` (no new
machinery). Estimate: ~2-3 days. Ships as v4.40.0.

**Phase C (deferred — Record → typed via explicit hint):**
adds the reverse direction with `--into MyStruct` syntax.
Allows pipelines like `from ssh ... | ssql where -if x gt 5
--into MyRow | typed group-by ...`. Estimate: ~1 week.

**Phase D (deferred indefinitely):** Record → Stream[T]
(parallel typed source from a Record stream) and Record →
typed via first-N-row inference. Both useful but neither is on
a critical path — they unblock fewer real workloads than
Phase B, and the explicit-hint version of Phase C handles the
common case.

After Phases A + B land, **the user's vision is realised**:
any pipeline that compiles in Record mode also compiles in
typed mode, with the planner picking the fastest viable
strategy per stage. Subsequent typed-runtime additions
(parallel sort via k-way merge, parallel distinct via
hash-shuffle, expr-lang → Go translation) are silent
speed-ups for users — no flag changes, no rewrites.

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

- **Typed-parallel codegen shipped** (v4.36.0, with v4.37.3
  multi-row-group Parquet defaults) — turning the performance
  gap between typed and Record from "interesting" into
  "dramatic" (215× CLI → typed-parallel on a real user corpus).
  Mixed mode previously meant trading off "fast typed" against
  "convenient Record"; today the trade-off is "fast typed"
  against "*very slow* Record."
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
  2026-04-28 / 2026-04-29 entries — six-mode comparison on a
  72-thread Xeon, showing the 215× CLI→typed-parallel-Parquet
  speedup (once multi-row-group Parquet was the default) that
  motivates wanting mixed mode in the first place.
