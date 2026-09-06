# `ssql/typed` — Performance Improvement Opportunities

Reference: DFC084
Created: 2026-04-26
Last modified: 2026-04-26

[Back to Index](./README.md)

Notes captured while implementing Phase 1 and Phase 1.5, organized by how
much they could move the headline numbers and how much work they require.

The Phase-1 design hits **15× faster, 34× less memory** end-to-end on
the 10M × 3-join workload. The opportunities below are about pushing
those numbers further or eliminating remaining allocation cost on
specific code paths.

---

## High-impact, medium-effort

### 1. Custom byte-level CSV reader (TRIED — DID NOT PAY OFF)

**Hypothesis.** `encoding/csv.Reader.Read()` looked like it should
allocate a fresh string for every cell on every row, even with
`ReuseRecord=true`. A custom scanner producing `[]byte` slices into a
reused buffer should let non-string field decoders parse via
`unsafe.String` without allocation, leaving only string fields to copy.

**What happened.** Implemented a full RFC 4180 byte-level scanner
(`csvscan.go`, ~140 LOC) plus a parallel `byteFieldDecoder` for every
type. Threaded through `ReadCSVFromReader` and `ReadCSVSafeFromReader`.
All 67 unit tests still passed.

Measured against the 10M × 3-join scale bench:

|                | Time   | Memory  | Allocs  |
|----------------|-------:|--------:|--------:|
| `csv.Reader`   | 4.94 s | 1.10 GB | 20.0 M  |
| Byte scanner   | 5.76 s | 0.85 GB | 30.0 M  |

The byte reader was **17% slower** and produced **50% more allocations**.
Memory allocated dropped 23%, but that's a small consolation when both
time and alloc-count regressed.

**Why the hypothesis was wrong.** `encoding/csv` with `ReuseRecord=true`
is smarter than I assumed: it reads each row into a single allocated
buffer and slices the field strings out of it, so cell allocation is
already block-allocated rather than per-cell. The "alloc savings" my
byte reader was supposedly capturing didn't exist.

The byte reader's overhead came from elsewhere: per-byte
`bufio.Reader.ReadByte()` calls are slower than `csv.Reader`'s scanned
chunked parsing, and tracking field offsets in parallel `[]int` slices
costs allocation of its own.

**Conclusion.** The byte-level reader was deleted. The lesson: profile
before assuming a stdlib package is wasteful. `csv.Reader` was already
near-optimal for our access pattern.

**Where genuine CSV wins might still come from.** A SIMD-accelerated
delimiter scan (intrinsics or assembly) — but Go's stdlib doesn't
expose these, and pulling in a third-party library would conflict with
the "zero native dependency" pitch. We will likely have to live with
the current ~280 ms / 1M-row CSV overhead.

### 2. Time parsing without `time.Parse`

**Where the cost is now.** `time.Parse(time.RFC3339, s)` runs a
generic format-string interpreter. Its cost dominates any CSV pipeline
with timestamp fields.

**Improvement.** A hand-rolled RFC3339 parser that unrolls the layout —
fixed positions for year/month/day/hour/minute/second, optional fractional,
optional timezone. Profiling repos show 3-5× speedup for the same
correctness.

**Estimated impact.** ~3× faster on time-heavy workloads. No effect on
workloads without time fields.

**Effort.** 2-4 hours, including thorough tests covering all RFC3339
edge cases.

### 3. Faster JSONL via `goccy/go-json` or `bytedance/sonic`

**Superseded 2026-09-06** by the "alternative" below, without codegen:
`typed/jsonl_fast.go` reflects once per type into a key → fieldDecoder
plan (the CSV reader's closures) and walks each line positionally —
3.6× encoding/json's throughput, a third of the allocations, no new
dependency, and independent of encoding/json/v2's graduation.

**Where the cost is now.** `encoding/json` does reflection per call,
and is generally 5-10× slower than the fastest Go JSON libraries.

**Improvement.** Drop-in replacement with `goccy/go-json` (pure Go,
~3× faster) or `bytedance/sonic` (assembly-accelerated, ~5-10× faster
on amd64). Behind an interface so users can opt out.

**Alternative**: per-type generated unmarshallers via codegen
(closer to the Phase 2 vision, avoids any third-party dep).

**Estimated impact.** JSONL pipelines become 3-5× faster overall.
Memory roughly halves.

**Effort.** 1 day for an interface + adapter. Slightly more if we
want to avoid runtime dependency on the faster lib.

---

## Medium-impact, low-effort

### 4. `HashJoinSized` with capacity hint

**Where the cost is now.** `HashJoin` calls `make(map[K]R)` with no
size hint. Map grows incrementally, triggering rehash allocations as
the right side is consumed.

**Improvement.** Add `HashJoinSized[L,R,O,K](left, right iter.Seq[R], rightCap int, ...)`
that pre-sizes the hash map. When the right side comes from a slice or
a known-size source, the caller can pass `len(right)` and avoid all
rehashes.

**Estimated impact.** ~5-15% faster joins where the right side is
larger than ~10k entries. Reduces tail latency on large lookups.

**Effort.** 30 minutes (variant function + test).

### 5. Eliminate per-field closure indirection

**Where the cost is now.** Each `fieldDecoder` is a closure capturing
`off uintptr`. Per-row decode does N indirect calls (one per column).
Modern CPUs absorb this well, but it still costs ~10% of the per-row
time at small struct sizes.

**Improvement.** When the user opts in (struct + tag pattern stable,
schema known at generation time), emit a single specialized
`decodeRow(p unsafe.Pointer, rec []string)` function that inlines all
field writes. This is fundamentally what Phase 2 codegen will produce.

**Alternative for Phase 1.5**: provide a `Schema[T]` cache the user
explicitly constructs (`s := typed.SchemaOf[T]()`) and pass into
ReadCSV — lets us skip per-call schema construction without affecting
the data path.

**Estimated impact.** 5-15% faster on narrow structs. Negligible on
wide structs (>30 fields) where the per-row work dominates.

**Effort.** Medium for the Schema cache (a few hours). Large for
runtime codegen (Phase 2 territory).

### 6. Strict mode for `ReadCSVSafe`

**Current behaviour.** `ReadCSVSafe` ignores unknown CSV columns and
silently zeros struct fields when the column is absent from the
header. Users who expect strict schema validation can't get it.

**Improvement.** Add a `StrictReadCSVSafe[T]` (or a config option) that
fails the first row when the header is missing required fields or
contains unknown ones.

**Estimated impact.** None on performance — purely a correctness/
ergonomics improvement that users will hit in production.

**Effort.** 1-2 hours.

---

## Smaller wins / micro-optimizations

### 7. Pointer-to-T allocation cost

`*int64` fields allocate one heap value per non-null row. For hot
paths with nullables, the cost adds up. Documented in
`doc/typed-reference.md`; alternatives are `sql.Null*`-style structs
(no extra alloc) or arena-allocated pointers (Phase 2 codegen).

### 8. WriteCSV string allocations

`strconv.FormatInt`, `strconv.FormatFloat`, etc. each allocate a fresh
string. Could be replaced with `strconv.AppendInt(buf, v, 10)` on a
shared `[]byte` to write directly into the CSV output. ~30-50% less
allocation on write-heavy workloads, ~10% faster.

### 9. JSON SetEscapeHTML / SetIndent decisions

Already disabled `SetEscapeHTML(false)` (the default escaping rewrites
`<`/`>`/`&`, slow and unwanted in data pipelines). Indent is always off.

### 10. `GroupBy` map preserves insertion order via separate `[]K`

Current implementation maintains a `[]K` alongside the map for
deterministic output ordering. This costs O(distinct keys) memory but
is essential for tests and user expectations. No change recommended —
just noting the deliberate design.

---

## Already addressed in Phase 1.5

| Improvement | How |
|---|---|
| Many-to-many joins | `HashJoinMulti` with `map[K][]R` |
| Outer joins | `LeftJoin`, `RightJoin`, `FullJoin` with explicit `found bool` flag |
| Streaming aggregation | `GroupByOrdered` for pre-sorted input (O(1) memory) |
| Aggregation building blocks | `Aggregator[T, R]` interface lets users plug in custom accumulators |

---

## Validation checklist before claiming a wider speedup

When implementing any of the above, add a benchmark to
`typed/scale_bench_test.go` covering both:

1. **Same workload, with-vs-without the optimization** — to attribute
   the speedup correctly.
2. **`go test -gcflags='-m'`** — to confirm escape analysis hasn't
   regressed. The Phase-1 compute-only path holds 0 B/op / 0 allocs
   per iteration; any change that pushes a `JoinedRow` to the heap
   would show up here before users notice.

The 15× / 34× headline numbers are conservative: they're measured
end-to-end, including CSV-reader overhead. Optimizations 1-3 above
attack that overhead specifically. Combined, they could move the
end-to-end ratio to roughly 30-40× — close to the original moonshot
projection — without changing the API surface or the design
principle.
