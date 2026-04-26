# `ssql/typed` — Performance Improvement Opportunities

Notes captured while implementing Phase 1 and Phase 1.5, organized by how
much they could move the headline numbers and how much work they require.

The Phase-1 design hits **15× faster, 34× less memory** end-to-end on
the 10M × 3-join workload. The opportunities below are about pushing
those numbers further or eliminating remaining allocation cost on
specific code paths.

---

## High-impact, medium-effort

### 1. Custom byte-level CSV reader

**Where the cost is now.** `encoding/csv.Reader.Read()` allocates a
fresh string for every cell on every row. Even with `ReuseRecord = true`
(which we use), only the `[]string` slice is reused — the strings
themselves are freshly allocated from the underlying bytes.

In the 10M × 3-join end-to-end benchmark, `ssql/typed` allocates 20M
times for 7.25M output rows — that's about 2.7 allocs/row. The vast
majority are CSV cell strings.

**Improvement.** Write a custom CSV scanner that operates on `[]byte`
ranges and feeds field decoders directly with `unsafe.String(&buf[lo], hi-lo)`
or by passing `(buf, lo, hi)` triples. Field decoders that need a string
copy (the `string` field type) do `string(buf[lo:hi])` — one alloc per
string field, instead of one alloc per *every* field.

**Estimated impact.** End-to-end time drops 10-20%, allocations drop
~3-5×. Combined with the existing wins, end-to-end speedup vs
`ssql.Record` would jump from 5× to roughly 7-10×.

**Effort.** 1-2 days. RFC 4180-correct CSV parsing has corner cases
(quoted fields, escaped quotes, embedded newlines) that need care.

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
