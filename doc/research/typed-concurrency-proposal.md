# `ssql/typed` — Concurrency Proposal

**Status:** PoC SHIPPED (2026-04-27). Measured a **10.1× speedup at compute** and a **6.4× end-to-end speedup** on the 10M × 3-join workload. End-to-end with `ReadCSVParallel`, typed-parallel runs at **2.2× of DuckDB's wall time** — the architectural gap is now narrow enough that further closing requires SIMD or columnar layout (Phase 3+ work). Full numbers in [§9a](#9a-poc-results-2026-04-27).

Predecessor:
[`typed-package-proposal.md`](typed-package-proposal.md) (Phase 1 + 1.5 + 1.6 shipped),
[`typed-performance-notes.md`](typed-performance-notes.md) (single-threaded
optimizations explored).

This document proposes a controlled, opt-in concurrency layer for the
`ssql/typed` package. The single-threaded path stays exactly as it is.
The parallel path is a separate type that users explicitly opt into
when they accept the tradeoffs.

---

## 1. Motivation

The headline numbers:

| Implementation | Time on 10M × 3-join | Multiplier vs Record |
|---|---:|---:|
| `ssql.Record` (current) | 74.8 s | 1× |
| `ssql/typed` (current) | 4.94 s | 15× |
| DuckDB v1.5 CLI | 0.42 s | 177× |

`ssql/typed` is single-threaded; DuckDB uses every core. Two single-thread
optimizations have already been explored — a custom byte-level CSV
reader and reflection avoidance — both either neutral or worse than
the stdlib. **We've reached the realistic single-threaded ceiling in
pure Go.** Closing the remaining gap to DuckDB requires multi-core
execution.

But concurrency is not a free win. Naive fan-out:

- breaks `iter.Seq[T]` ordering
- forces materialization at stage boundaries
- hurts ergonomics (workers, channels, contexts)
- adds complexity that destroys ssql/typed's "~600 LOC, no native deps"
  pitch

The proposal is **a clearly-opt-in parallel pipeline that mirrors the
typed API but lives in its own type**, leaving `iter.Seq[T]` untouched
as the canonical single-threaded path.

## 2. Inspiration: DuckDB's Morsel-Driven Parallelism

DuckDB's parallel execution model is widely regarded as the gold
standard for in-process analytics. Three load-bearing ideas:

1. **Data is split into morsels** — small, homogeneous chunks. A fixed
   number of worker threads (one per core) pull morsels off a global
   queue. Work stealing is implicit because all workers compete for
   the same queue.
2. **Operators are parallelism-aware**, not bolted on via "exchange"
   or "shuffle" operators. Each operator knows how to run in parallel
   if it can.
3. **Blocking operators (joins, group-by) have a three-phase contract:**
   - **Sink** — thread-local accumulation as morsels arrive
   - **Combine** — a thread signals it's done sinking
   - **Finalize** — called once after all threads have combined; produces
     the global result

This avoids the materialization costs and load imbalance that plague
"fan-out then fan-in" parallelism in row-engines like Postgres.

We can't replicate DuckDB's columnar+SIMD execution in pure Go, but
we *can* adopt the morsel-driven shape. The blocking-operator contract
in particular maps cleanly onto Go's `sync.WaitGroup` + `sync.Mutex`
primitives.

Sources:
- [DuckDB GitHub Discussion #6632 — Morsel-Driven Parallelism](https://github.com/duckdb/duckdb/discussions/6632)
- [Morsel-Driven Execution Framework in DuckDB](https://blog.qsliu.dev/post/duckdb-morsel-driven/)
- [DuckDB: An Architectural Deep Dive](https://thinhdanggroup.github.io/duckdb/)

## 3. Soul-Preserving Constraints

Things we will **not** do, no matter how much performance is on the table:

1. **Don't change `iter.Seq[T]`.** The single-threaded API is the
   library's canonical surface. It stays serial, ordered, and
   stack-friendly.
2. **Don't introduce a hidden `Parallelize()` flag** that secretly
   re-orders results. Surprising the user is worse than slow.
3. **Don't bring in CGO or third-party concurrency libraries.** Pure
   Go, stdlib only.
4. **Don't conflate I/O parallelism with compute parallelism.** They
   have different bottlenecks. A `concurrent CSV reader` and a
   `parallel hash join` are separate features even if they share a
   worker pool implementation.
5. **Don't go below ~600 LOC of new data-path code.** If the parallel
   layer doubles the package size, we've lost the "trivially auditable"
   pitch that justifies the project.

## 4. Proposed API: `typed.Stream[T]`

A new type `typed.Stream[T]` represents a parallel pipeline of T.
Internally it's `[]iter.Seq[T]` — one shard per worker — plus
metadata about partitioning. The boundaries to and from `iter.Seq[T]`
are explicit.

### Type and conversions

```go
// Stream is a parallel pipeline of T, partitioned across N workers.
// Operations on Stream run in parallel where possible.
//
// Stream is a separate type from iter.Seq[T] because its execution
// model is different — there is no guaranteed input-order = output-order.
type Stream[T any] struct {
    shards []iter.Seq[T]
    // ... book-keeping for ordered output, error fan-in, etc.
}

// Parallel converts a single iter.Seq[T] into a Stream[T] partitioned
// across n shards. n=0 means runtime.GOMAXPROCS(0).
//
// The input is consumed by a single goroutine that round-robin
// distributes rows to per-shard channels. Use ReadCSVParallel for
// the common case where I/O parallelism is what you want.
func Parallel[T any](in iter.Seq[T], n int) Stream[T]

// Serial collects a Stream back into a single iter.Seq[T].
//
// By default, output is unordered: each shard is consumed in turn
// using fair scheduling. Use SerialOrdered when you need deterministic
// output (at the cost of head-of-line blocking when one shard runs slow).
func (s Stream[T]) Serial() iter.Seq[T]
func (s Stream[T]) SerialOrdered() iter.Seq[T]
```

### Parallel I/O

```go
// ReadCSVParallel reads a CSV file using N reader goroutines. Each
// goroutine seeks to a byte boundary, scans forward to the next
// newline, and reads its slice. Header is parsed once before workers
// start.
//
// The returned Stream is partitioned by file offset, so the order is
// "as the file lays them out" — but inter-shard order is not
// guaranteed unless you call SerialOrdered.
func ReadCSVParallel[T any](filename string, n int, opts ...CSVOption) Stream[T]
```

### Parallel-aware operations

```go
// Where on a Stream runs the predicate independently per shard.
// Pure embarrassingly parallel.
func (s Stream[T]) Where(pred func(T) bool) Stream[T]

// Select on a Stream runs fn independently per shard.
func StreamSelect[T, U any](s Stream[T], fn func(T) U) Stream[U]

// HashJoinParallel is the morsel-driven hash join.
//
// Build phase: right is fully consumed by ONE worker (the build phase
// is serial because the hash table is shared). For tiny right-sides
// this is fine; for large right-sides see HashJoinParallelPartitioned
// below.
//
// Probe phase: every left shard runs against the shared read-only
// hash table in parallel.
func HashJoinParallel[L, R, O any, K comparable](
    left Stream[L],
    right iter.Seq[R],
    leftKey func(L) K,
    rightKey func(R) K,
    merge func(L, R) O,
) Stream[O]

// HashJoinParallelPartitioned uses a radix hash join. Both sides are
// partitioned by key hash into the same N buckets; each worker
// processes one bucket independently. Higher build-phase parallelism
// at the cost of one extra materialization pass.
//
// Worth it when the right side is large enough that single-threaded
// hash-table construction becomes the bottleneck (~500k+ rows).
func HashJoinParallelPartitioned[L, R, O any, K comparable](
    left Stream[L],
    right Stream[R],
    leftKey func(L) K,
    rightKey func(R) K,
    merge func(L, R) O,
) Stream[O]

// GroupByParallel partitions by hash(key) so each worker processes
// a disjoint slice of keys, then concatenates partial results.
// Maps directly onto DuckDB's Sink/Combine/Finalize phases.
func GroupByParallel[T, S, O any, K comparable](
    s Stream[T],
    keyFn func(T) K,
    newAgg AggFunc[T, S],
    build func(K, S) O,
) Stream[O]
```

### Operator parallelism summary

| Operator | Parallelizable? | Notes |
|---|---|---|
| `ReadCSV` | Yes (per byte-range) | Need to scan to next newline at boundaries |
| `Where` | Yes (embarrassing) | Pure per-row predicate |
| `Select` / `Map` | Yes (embarrassing) | Pure per-row transform |
| `Limit` / `Skip` | **No** (serial-only) | Counter must be shared; trivial to do at the Serial boundary |
| `HashJoin` | Probe yes, build with `*Partitioned` variant | Default keeps build serial |
| `GroupBy` | Yes (partition by hash(key)) | Three-phase Sink/Combine/Finalize |
| `Sort` | Yes (parallel sort + k-way merge) | Out of scope for first PoC |
| `Distinct` | Yes (partition by hash(value)) | Out of scope for first PoC |

## 5. Ordering Semantics

The big design question. Three positions:

**Position A — Always preserve order.** Every Stream operation maintains
input-order = output-order. Implementation forces head-of-line waits
between shards; effectively serializes anything that crosses shards.
*Performance ceiling: ~2× over single-threaded.*

**Position B — Never preserve order.** All Stream operations explicitly
non-deterministic. Forces users who care about order to convert to
`iter.Seq[T]` and re-sort. *Performance ceiling: full N× scaling.*

**Position C — Explicit at the boundary.** Default `Stream` operations
are unordered (Position B). The Serial-side conversion offers two
methods: `Serial()` (unordered, fast) and `SerialOrdered()` (ordered,
slower). Users opt into ordering only when they need it.
*Performance ceiling: full N× scaling for unordered consumers; ~2-3×
for ordered consumers (small-buffer round-robin merge).*

**Recommendation: Position C.** It mirrors how channel `select` works
in Go (no guaranteed order between channels) and matches DuckDB's
default behavior. Documented prominently; opt-in via method name, not
flag.

## 6. Worked Example

The same workload from §5 of the typed-package-proposal — read
employees, filter by years, join with departments, write output —
expressed serially and in parallel.

### 6.1 Serial (current `iter.Seq[T]` API)

```go
employees := typed.ReadCSV[Employee]("employees.csv")
depts     := typed.ReadCSV[Department]("departments.csv")

seniors := typed.Where(func(e Employee) bool {
    return e.Years >= 5
})(employees)

joined := typed.HashJoin(seniors, depts,
    func(e Employee) string   { return e.DeptID },
    func(d Department) string { return d.DeptID },
    func(e Employee, d Department) Senior { ... })

typed.WriteCSV(joined, "seniors.csv")
```

### 6.2 Parallel (proposed `Stream[T]` API)

```go
// ReadCSVParallel partitions the file across N workers.
employees := typed.ReadCSVParallel[Employee]("employees.csv", 0) // 0 → GOMAXPROCS
depts     := typed.ReadCSV[Department]("departments.csv")        // small lookup, stays serial

// Where runs the predicate in parallel across shards.
seniors := employees.Where(func(e Employee) bool {
    return e.Years >= 5
})

// Build phase serial (depts is small); probe phase runs across shards.
joined := typed.HashJoinParallel(seniors, depts,
    func(e Employee) string   { return e.DeptID },
    func(d Department) string { return d.DeptID },
    func(e Employee, d Department) Senior { ... })

// Convert back to iter.Seq[T] at the boundary.
// Serial() — unordered, fast.   SerialOrdered() — preserves source order.
typed.WriteCSV(joined.Serial(), "seniors.csv")
```

The diff from serial to parallel is three changes: one function name
on read (`ReadCSV` → `ReadCSVParallel`), one on join
(`HashJoin` → `HashJoinParallel`), and one explicit boundary at the
end (`.Serial()`). The pipeline shape and predicate code are
identical.

## 7. Estimated Performance

For the 10M × 3-join headline workload on a 16-thread Core Ultra 9:

| Stage | Single-threaded | Embarrassingly parallel ceiling | Realistic with overhead |
|---|---:|---:|---:|
| CSV read (10M rows) | ~2.5 s | ~0.16 s (16×) | ~0.5 s (5×) |
| Filter | ~0.2 s | ~0.012 s | ~0.05 s |
| 3 hash joins (probe) | ~1.5 s | ~0.09 s | ~0.4 s |
| Build phase (small lookups) | ~0.1 s | (stays serial) | ~0.1 s |
| Coordination overhead | — | — | ~0.2 s |
| **Total** | **~4.9 s** | **~0.3 s** | **~1.2-1.5 s** |

Realistic estimate: **3-4× speedup, putting us at ~1.5 s on the
headline workload — within ~4× of DuckDB.** Good enough to genuinely
compete on workloads where you can't or won't pay DuckDB's
operational cost (CGO, native binary, separate process).

The estimate assumes:
- Disk I/O is not the bottleneck (NVMe / page cache hot)
- Right-side hash tables fit comfortably in CPU cache per partition
- Workload skew is moderate (no single hot key dominating)

## 8. Open Questions

1. **Stream representation.** `[]iter.Seq[T]` is the obvious shape but
   means users see N goroutines internally. An alternative is a single
   `<-chan []T` (batched morsels) — more efficient at the cost of a
   different shape from the serial API. Which makes the user code
   read better?
2. **Error propagation.** Serial paths use `iter.Seq2[T, error]`.
   Parallel paths need a multi-shard error story. Options: (a) yield
   errors per shard until they're collected at `Serial()`, (b)
   first-error-fails-fast, cancelling other shards via `context`.
   Should be (b) by default with (a) as opt-in?
3. **`ReadCSVParallel` byte-range scan.** Need to find safe newline
   boundaries. Quoted-field-with-embedded-newline edge case is
   fiddly; a wrong split breaks the row. Is the complexity worth it,
   or should we partition at higher granularity (e.g. one whole file
   per shard, when reading multiple files)?
4. **Default `n`.** `runtime.GOMAXPROCS(0)` is the obvious choice but
   may oversubscribe if the user is calling us from inside a worker
   pool. Same problem DuckDB has — they let users override.
5. **Where does this live?** New subpackage `ssql/typed/parallel`?
   Or integrated into `ssql/typed`? The latter is simpler but
   pollutes the namespace; the former is cleaner but means a longer
   import path.
6. **Compatibility with the Phase 2 codegen vision.** The `ssql
   generate go -typed` codegen will need to choose between emitting
   serial or parallel calls. Most likely a `-parallel` flag on top
   of `-typed`, but the proposal should be clear that the codegen
   integration is a separate question.

## 9. PoC Plan

Same shape as the typed-package PoC: build the smallest credible thing,
benchmark it, decide whether the design holds up before investing in
more.

| Step | Goal | Effort |
|---|---|---|
| 1 | Implement `Stream[T]` with `Parallel(in, n)` and `Serial()` (unordered). | 2-4 hours |
| 2 | Add `Stream.Where` (embarrassingly parallel). | 30 min |
| 3 | Add `HashJoinParallel` (serial build, parallel probe). | 2-3 hours |
| 4 | Benchmark vs single-threaded `ssql/typed` on the 10M × 3-join workload. | 1 hour |
| 5 | Compare against the §7 estimates. Decide: ship it, iterate, or shelve. | — |
| 6 (if continuing) | Add `ReadCSVParallel` with byte-range scan. | 1 day |
| 7 (if continuing) | Add `GroupByParallel` (Sink/Combine/Finalize). | 1 day |
| 8 (if continuing) | Add `SerialOrdered`, error propagation, context cancellation. | 1 day |

Total budget for the PoC (steps 1-5): half a day. If the realistic
speedup is less than 2×, we shelve concurrency and document the
finding alongside the failed byte-CSV experiment.

## 9a. PoC Results (2026-04-27)

The minimum credible parallel pipeline (Steps 1-3 from §9) was built
in `typed/stream.go` (~120 LOC) and benchmarked against the same 10M
× 3-join workload as the rest of the typed bench suite.

### What the PoC ships

- `Stream[T any]` — a parallel pipeline of T (just a `[]iter.Seq[T]` plus a shard count)
- `Parallel(in, n)` — channel-based source, kept as reference (see "channel cost" lesson below)
- `ParallelFromSlice(data, n)` — slice-partitioned source, NO channels on the input side
- `Stream.Where`, `StreamSelect`, `HashJoinParallel`
- `Stream.Serial()` — fan-in to a single iter.Seq[T] via a goroutine pool + buffered channel
- `Stream.SerialCount()` — count-only sink, no fan-in channel (atomic add per shard)

### Headline numbers

10M × 3 chained joins, 7.25 M output rows. Two comparisons:

**Compute only (preloaded into memory):**

| Implementation | Wall time | Speedup vs serial |
|---|---:|---:|
| typed serial (single-threaded) | 1,259 ms | 1.0× |
| **typed parallel (24 shards)** | **124 ms** | **10.1×** |

**End-to-end (CSV read → filter → 3 joins → count):**

| Implementation | Wall time | Speedup vs serial | vs DuckDB |
|---|---:|---:|---:|
| typed serial | 4,997 ms | 1.0× | 14× behind |
| **typed parallel (`ReadCSVParallel` + `HashJoinParallel` + `SerialCount`)** | **774 ms** | **6.4×** | **2.2× behind** |
| DuckDB CLI | 356 ms | — | — |

Reproduce:
```bash
go test -bench='ScaleTypedReadCSVParallel|ScaleTypedSerialCompute|ScaleTypedSliceParallel|ScaleTyped3Join$|ScaleDuckDB3Join$' -benchtime=3x -run=^$ -timeout=15m ./typed/...
```

10.1× scaling at compute on 24 logical cores is Amdahl-bounded
(filter selectivity ~70%, output cardinality varies per shard,
memory bandwidth saturates at the build/probe boundary). 6.4×
end-to-end means **80% of the original DuckDB gap closed** with a
~350-line PoC — no SIMD, no columnar storage, no CGO. The
remaining 2.2× requires architectural changes: vectorized SIMD
parsing, columnar in-memory representation, or Apache Arrow as
the runtime — all of which trade away the "pure Go, no native deps,
~600 LOC data path" pitch.

### Channel design failure (and the lesson)

The first PoC variant used a single shared work channel: a
distributor goroutine pulls from `in` and pushes to `chan T`; N
workers pull from the same channel. Measured at 11.65 s for the
end-to-end 10M × 3-join workload — **3× slower than single-threaded**.

The math: ~100ns of channel transit per row, 4 pipeline stages, 7M
rows surviving the filter ≈ 2.8 s of pure coordination overhead. On a
workload where the per-row compute cost is in the tens of ns,
channel transit dominates wall time.

This is captured as `BenchmarkScaleTypedParallel3Join` (kept in the
test file as a reference negative result) and led to the slice-based
`ParallelFromSlice` design that bypasses the channel entirely. Per-shard
slices give workers independent work that's purely in-stack iteration
plus the build-once-shared-read hashmap from `HashJoinParallel`.

### What the PoC validates

1. **Compute parallelism scales well.** 10.1× on 24 cores is good.
2. **typed-parallel beats DuckDB at compute.** When CSV I/O is
   excluded, typed-parallel (124 ms) outpaces DuckDB end-to-end
   (345 ms). The remaining gap to a fully-fair end-to-end
   comparison is whatever DuckDB's parallel CSV reader buys it.
3. **Channels are too expensive for row streams.** Per-row
   coordination must be amortized — slice partitioning, byte-range
   scans, or batched morsels. Single shared channels lose to single-
   threaded execution.

### What the PoC doesn't yet have

- **Parallel CSV reading.** ✅ SHIPPED — `ReadCSVParallel[T]`. Reads
  the file into memory, scans newlines via SIMD `bytes.IndexByte`,
  partitions data lines across shards, each shard runs its own
  `csv.Reader` over its byte range. Documented limitation: assumes
  no quoted fields with embedded newlines (files produced by
  `typed.WriteCSV` satisfy this; arbitrary external files may not).
  This took the end-to-end gap to DuckDB from 14× to 2.2×.
- **Ordering preservation.** `Serial()` fan-in is unordered.
  `SerialOrdered()` (round-robin merge) is sketched in §5 but
  not built.
- **Error propagation.** Workers don't yet have a context.Context or
  shared error channel. Single-error-fail-fast is the right v1.
- **Three-phase aggregation (Sink / Combine / Finalize).**
  GroupByParallel is in the proposal but not the PoC.
- **Codegen integration.** `SSQLGO=typed` still emits serial code.
  A `-parallel` flag could opt in.

### Recommendation

The PoC's measured numbers — 10.1× at compute, 6.4× end-to-end,
2.2× from DuckDB — justify promoting the parallel runtime into the
package proper. Next investments, in order:

1. ✅ ~~`ReadCSVParallel[T]`~~ — done; closes 80% of the DuckDB gap.
2. **Codegen `-parallel` flag** — surface the speedup to users
   without requiring them to rewrite as Go. Now the highest-leverage
   item: the runtime is fast enough; the codegen integration is what
   exposes it.
3. **`Stream.SerialOrdered`** — for users who need
   input-order = output-order.
4. **`GroupByParallel` with Sink / Combine / Finalize** —
   completes the parallel-aware operator set.
5. **Beyond 2.2× of DuckDB** would require SIMD-vectorized parsing
   (Go stdlib gives us `bytes.IndexByte` but no vectorized struct
   decode), columnar layout, or Apache Arrow. Each trades away
   "pure Go, no native deps". Defer indefinitely unless a real user
   workload demands it.

## 10. What This Proposal Doesn't Try to Do

- It doesn't propose changing the single-threaded `ssql/typed` API.
- It doesn't introduce a query planner or cost-based optimizer.
- It doesn't try to match DuckDB's columnar+SIMD execution. That's
  a different system, and getting there would mean restructuring
  around Apache Arrow.
- It doesn't address distributed execution. ssql's existing SSH-based
  distributed processing is orthogonal.
- It doesn't commit to operators outside the §4 list. Sort, Distinct,
  and outer-join parallelization can be added later if they're
  needed.

The proposal is deliberately scoped to the smallest credible
parallelism story that closes a meaningful chunk of the DuckDB gap.
If the PoC shows the gap is real, we expand. If not, we know.

---

## Sources

- [DuckDB GitHub Discussion #6632 — Morsel-Driven Parallelism](https://github.com/duckdb/duckdb/discussions/6632)
- [Morsel-Driven Execution Framework in DuckDB (qsliu.dev)](https://blog.qsliu.dev/post/duckdb-morsel-driven/)
- [DuckDB: An Architectural Deep Dive](https://thinhdanggroup.github.io/duckdb/)
- [Go Concurrency Patterns: Pipelines and Cancellation](https://go.dev/blog/pipelines)
- [Go Concurrency Patterns: Worker Pool, Fan-In/Fan-Out & Pipeline](https://dev.to/serifcolakel/go-concurrency-patterns-worker-pool-fan-in-fan-out-pipeline-49pd)
- [Multi-core, Main-Memory Joins: Sort vs. Hash Revisited (VLDB 2014)](http://www.vldb.org/pvldb/vol7/p85-balkesen.pdf)
- [CMU 15-721 — Hash Join Algorithms (Spring 2024)](https://15721.courses.cs.cmu.edu/spring2024/notes/09-hashjoins.pdf)
- [Container-aware GOMAXPROCS (Go 1.25)](https://go.dev/blog/container-aware-gomaxprocs)
- [Apache Arrow Go CSV Reader](https://pkg.go.dev/github.com/apache/arrow/go/arrow/csv)
