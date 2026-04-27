# Concurrency Patterns and Lessons

**When adding parallelism to ssql/typed (or any iter.Seq[T]-based pipeline), follow these patterns. Most are negative-results: the things that *don't* work cost the most time to discover.**

The typed-parallel runtime (`typed/stream.go`, shipped as a PoC on 2026-04-27) achieved a measured **10.1× speedup over single-threaded** on 24 logical cores for the 10M × 3-join workload. Getting there required three failed approaches first; this doc captures what we learned so the failures don't get re-discovered.

For the design proposal, see [`doc/research/typed-concurrency-proposal.md`](../doc/research/typed-concurrency-proposal.md). For the cumulative knowledge that informs every concurrency change going forward, **read this file**.

---

## 1. Channels Are Too Expensive on Per-Row Hot Paths

**Rule: never put a channel between every row and its next consumer. Per-row coordination must be amortized.**

The first PoC tried the textbook fan-out pattern: a distributor goroutine pulls from `iter.Seq[T]` and pushes to a shared `chan T`; N worker goroutines pull from the same channel.

```go
// THIS WAS 3X SLOWER THAN SINGLE-THREADED.
work := make(chan T, n*64)
go func() {
    for v := range in { work <- v }
    close(work)
}()
shards := make([]iter.Seq[T], n)
for i := 0; i < n; i++ {
    shards[i] = func(yield func(T) bool) {
        for v := range work {
            if !yield(v) { return }
        }
    }
}
```

**Measured:** 11.65 s end-to-end vs single-threaded's 5.30 s on the 10M × 3-join workload.

**The math.** Channel send + receive ≈ 100 ns per pair. The pipeline has 4 stages (source → filter → 3 joins → sink). 7M rows survive the filter. Per row, 4 channel transits × 100 ns = 400 ns. Total: 7,000,000 × 400 ns ≈ **2.8 s of pure channel overhead** — more than half the single-threaded wall time, gone before any actual work happens.

**The fix that worked: `ParallelFromSlice`.** Materialize the input into a slice once, hand each shard a contiguous sub-slice, every shard iterates its own slice in pure stack code. **Zero channel transits on the row path.** Result: 124 ms — 95× faster than the channel-based attempt.

```go
// THIS WORKED. 10.1x faster than serial, no per-row channels.
chunkSize := (len(data) + n - 1) / n
for i := 0; i < n; i++ {
    chunk := data[i*chunkSize : min((i+1)*chunkSize, len(data))]
    shards[i] = func(yield func(T) bool) {
        for _, v := range chunk {
            if !yield(v) { return }
        }
    }
}
```

The only channel in the entire critical path is now the fan-in to `Serial()` — and even that is bypassed by `SerialCount()` for aggregation sinks (atomic add per shard, no transit).

**Practical implication.** Any future "parallelize this command" work must first answer: *where does the per-row channel go?* If the answer is "in the hot path", redesign before benchmarking.

## 2. Slice Partitioning Beats Channel Fan-Out for In-Memory Data

**Rule: when the source is already in memory (slice, hash map, etc.), partition the *data* across workers, not the *iteration* through a channel.**

`ParallelFromSlice(data, n)` divides the slice into n contiguous chunks of size `(len(data)+n-1)/n` (round-up so the last shard gets any remainder). Each shard's iterator is just `for _, v := range chunk { yield(v) }` — pure stack-allocated row, no escape, no synchronization.

This is the morsel-driven design from DuckDB simplified for our use case: the morsels are static slice ranges, the worker pool is implicit (one goroutine per shard, started when `Serial()` or `SerialCount()` is called).

For sources that aren't already in memory (CSV, JSONL), the equivalent is byte-range partitioning — `ReadCSVParallel[T]` (proposed, not yet built).

## 3. Hash Join: Serial Build, Parallel Probe

**Rule: in a hash join where the right side is small (~1 K – 1 M rows), keep the build phase serial and parallelize only the probe.**

```go
// HashJoinParallel — proven shape.
func HashJoinParallel[L, R, O any, K comparable](left Stream[L], right iter.Seq[R], ...) Stream[O] {
    idx := make(map[K]R)
    for r := range right {
        idx[rightKey(r)] = r       // SERIAL build
    }
    out := make([]iter.Seq[O], len(left.shards))
    for i, shard := range left.shards {
        out[i] = func(yield func(O) bool) {
            for l := range shard {
                if r, ok := idx[leftKey(l)]; ok {   // PARALLEL probe; idx is read-only here
                    if !yield(merge(l, r)) { return }
                }
            }
        }
    }
    return Stream[O]{shards: out, n: left.n}
}
```

**Why it works.**
- The build phase is O(R) where R is the right-side row count. For our typical lookup sizes (1 K dimensions, 100 cities, etc.), build is ~milliseconds even single-threaded.
- The probe phase is O(L) where L is the left side, often 10×-1000× larger. *That's* what needs parallelizing.
- The hash map is **safe to read concurrently** once the build is complete and the map is never written to again. No `sync.RWMutex`, no `sync.Map`, no atomic.

**When this falls down.** If the right side becomes large (say > 500 K rows), serial build becomes the bottleneck. The proposal §4 discusses `HashJoinParallelPartitioned` — a radix hash join where both sides are partitioned by key hash and each worker processes one bucket independently. **Not yet built**; flag for `claude/concurrency.md` to document if/when it ships.

## 4. SerialCount Bypasses the Fan-In Channel for Aggregations

**Rule: when the consumer only wants an aggregate (count, sum), use a count-only sink rather than fan-in.**

The general `Serial()` method spawns one goroutine per shard, each writing to a shared output channel; the caller reads from the channel. That's a sensible design for "give me an iter.Seq[T] of the merged results", but it costs ~100 ns/row of channel transit on the way out.

For sinks that just want a number, `SerialCount()` does:

```go
var total int64
var wg sync.WaitGroup
for _, shard := range s.shards {
    go func() {
        defer wg.Done()
        var local int64
        for range shard { local++ }
        atomic.AddInt64(&total, local)
    }()
}
wg.Wait()
return total
```

One atomic add per shard at the end (24 adds, not 7M). Same idea applies to `SerialSum`, `SerialMin`, etc. — none of those exist yet, but the pattern is clear: each shard accumulates locally, atomic-merge once at end.

## 5. Early Termination Must Drain the Workers

**Rule: if `Serial()` is consumed with `break` or early `return`, the goroutines pushing into the fan-in channel must still be able to write or they'll deadlock.**

```go
// The drain pattern in Stream.Serial:
for v := range out {
    if !yield(v) {
        // Caller broke out of the range. Drain the channel so the
        // goroutines pushing into `out` can exit. Without this, they
        // block on `out <- v` forever.
        go func() {
            for range out {}
        }()
        return
    }
}
```

Easy to forget. The deadlock manifests as "test runs forever" — `go test -timeout` catches it eventually but the failure isn't obvious from the symptom.

`SerialCount` doesn't have this problem because there's no fan-in channel — workers run to completion locally and merge once.

## 6. Scaling Expectations on Modern Hardware

**Rule: don't expect linear scaling past ~10× on 24-core hardware for memory-bound workloads.**

Measured on Intel Core Ultra 9 275HX (24 logical cores), 10M × 3 chained inner joins, ssql/typed runtime:

| Shards | Wall time | Speedup | Efficiency |
|---:|---:|---:|---:|
| 1 (serial) | 1,259 ms | 1.0× | 100% |
| 24 (parallel) | 124 ms | 10.1× | 42% |

42% Amdahl efficiency on a 24-core machine is pretty normal for this workload class. The remaining 58% goes to:

- **Memory bandwidth**. The hash maps and join-merged structs all live in DRAM; multiple cores reading the same lookup table saturate the memory channel before they saturate compute.
- **Filter selectivity skew**. Some shards finish their slice in less time than others (different fractions of rows survive the filter at different points in the input).
- **GC coordination**. Stop-the-world pauses suspend all shards together; even brief pauses scale poorly past ~16 cores.
- **Final fan-in / SerialCount merge**. Tiny but non-zero per-shard cost.

For pure-compute kernels (FFT, hash computation) we'd expect closer to 70-80% efficiency. For I/O-bound work, much worse — see §7.

## 7. CSV I/O Is the Next Bottleneck (Not Yet Tackled)

**Rule: when end-to-end speedups stall, look at where the un-parallelized work lives.**

The end-to-end pipeline (CSV read → filter → 3 joins → output) measures:

| Mode | End-to-end | Compute-only |
|---|---:|---:|
| typed serial | 5,300 ms | 1,259 ms |
| typed parallel (24 shards) | ≈ 5,200 ms (estimate) | 124 ms |

Parallelizing compute didn't help the end-to-end number much because **CSV reading dominates**. The single-threaded CSV reader is now the Amdahl-serial portion limiting end-to-end gains.

Next investment per the proposal §9a: `ReadCSVParallel[T]` — partition the CSV file by byte range, each shard scans forward to its first newline boundary and reads its slice. DuckDB does this; it's why their CSV path is fast.

Until that ships, **don't claim end-to-end speedups from concurrency**. The honest claim is "10× at compute".

## 8. Negative Results Worth Preserving

When a concurrency experiment fails, **keep the failed bench as a reference negative result** rather than deleting it. The next contributor will otherwise re-discover the same dead end.

Currently kept:

- `BenchmarkScaleTypedParallel3Join` (channel-based `Parallel`, 11.65 s vs 5.30 s serial). Demonstrates the per-row-channel-cost problem from §1.

When adding new failed approaches, follow the same pattern: keep the bench, mark it `// FAILED EXPERIMENT — KEPT AS REFERENCE`, and add a paragraph here describing what was tried, what was measured, and why it didn't work.

## 9. Test for Race Conditions Aggressively

**Rule: `go test -race ./typed/...` before any concurrency change ships.**

The race detector caught a couple of issues during PoC development:

- **Closure capture in `for i, shard := range s.shards` loops** — the loop variable was being shared across goroutines until I added `shard := shard` inside the loop body. (Go 1.22+ fixes this for `for i := range ...` loops, but it bit us before the variable redeclaration. Always shadow the loop variable explicitly when you launch a goroutine that captures it.)
- **Build-then-read map sharing in `HashJoinParallel`** — the build goroutine writes the map; probe goroutines read it. Safe only because the build *completes synchronously* before the probe shards are constructed (the build loop `for r := range right { idx[k] = r }` runs in the calling goroutine, not in a goroutine of its own). If we ever parallelize the build, this becomes unsafe — would need `sync.Map` or per-shard maps + a partitioned join.

`go test -race` should be a CI gate for any commit that touches `typed/stream.go`.

## 10. Quick Reference

```go
// Materialized → parallel:
stream := typed.ParallelFromSlice(data, runtime.GOMAXPROCS(0))

// iter.Seq → parallel (only when CHANNEL TRANSIT < per-row work):
stream := typed.Parallel(seq, n)  // channel-based; usually too expensive

// Apply parallel-aware ops:
filtered := stream.Where(pred)
joined := typed.HashJoinParallel(filtered, right, leftKey, rightKey, merge)

// Sink:
count := joined.SerialCount()                // count-only, no channel
result := slices.Collect(joined.Serial())    // unordered fan-in, channel
```

**Defaults to use:**
- `n = runtime.GOMAXPROCS(0)` — match logical cores
- Match shard granularity to L2 cache size when possible — for our struct sizes that's ~10K-100K rows per shard
- Prefer `SerialCount` / `SerialSum` style sinks over `Serial()` when the consumer is an aggregation

## See Also

- [`doc/research/typed-concurrency-proposal.md`](../doc/research/typed-concurrency-proposal.md) — design proposal + measured PoC results in §9a
- [`claude/performance-patterns.md`](performance-patterns.md) — single-threaded performance rules; everything there still applies *inside* each parallel shard
- [`typed/stream.go`](../typed/stream.go) — the actual runtime
- [`typed/stream_test.go`](../typed/stream_test.go) — unit tests including the `TestStreamWhereRunsInParallel` validation
- [`typed/concurrency_bench_test.go`](../typed/concurrency_bench_test.go) — the headline-number benchmarks
