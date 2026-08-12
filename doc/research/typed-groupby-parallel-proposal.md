# Typed GroupByParallel — Sink/Combine/Finalize Proposal

Reference: DFC086
Created: 2026-04-27
Last modified: 2026-04-27

[Back to Index](./README.md)

**Status:** Design + early implementation in flight (2026-04-27).

This proposal extends the typed parallel runtime
(`typed.Stream[T]`, `Stream.Where`, `HashJoinParallel`,
`ReadCSVParallel`, per-shard CSV sink) with a parallel
group-by-with-aggregation operator: `GroupByParallel`. It is the
next big-win item from `typed-codegen-proposal.md` §5d, after the
per-shard CSV buffer fix (shipped 2026-04-27).

Motivation: group-by is a hot path. The serial `typed.GroupBy`
walks every input row through a single `map[K]Aggregator` plus
the per-row aggregator `Add()`. Both are embarrassingly
parallelisable per shard, with a small Combine phase at the end
because typical workloads have **many rows** but **few groups**
(e.g. 10 M rows × 1 000 dept_ids). The merge cost is proportional
to (#shards × #groups), not (#rows), so we expect close to linear
speedup.

## 1. The three-phase contract

This is the same idea DuckDB uses for parallel aggregating
sinks: every parallel operator on the aggregating path implements
**Sink / Combine / Finalize**.

1. **Sink (per-shard, fully parallel)**

   Each shard maintains its own `map[K]ParallelAggregator[T, S]`.
   For every input row `v`:

   - compute `k := keyFn(v)`
   - look up or create the shard's per-key aggregator with `newAgg()`
   - call `agg.Add(v)` to fold the row into the partial state

   No coordination across shards. No shared map. No locks.

2. **Combine (sequential, small)**

   Once all shards finish, the orchestrator walks the partial maps
   in shard order and folds each into a single `final` map:

   - if a key is new, transfer the partial aggregator to `final` as-is
   - if a key already exists in `final`, call `final[k].Merge(partial[k])`

   Cost: `O(#shards × #groups_per_shard)` map operations plus one
   `Merge` per duplicate key. For 1 000 groups × 32 shards that is
   32 000 ops worst case — negligible compared to scanning 10 M
   rows.

3. **Finalize (sequential, lazy)**

   Returns an `iter.Seq[O]` that walks `final` in insertion order
   (shard-0's keys first, then shard-1's new keys, etc.) and
   yields `build(k, agg.Result())` for each one. Lazy because the
   downstream consumer may early-exit (e.g. `Stream.Limit`
   composes onto this).

This contract is **Stream-typed input, iter.Seq[O]-typed output**.
Group-by is a natural fan-in point — once you have one row per
group, the rest of the pipeline is small enough that single-
threaded composition pays for itself.

## 2. The Aggregator interface decision

The current serial interface (typed/agg.go):

```go
type Aggregator[T, R any] interface {
    Add(T)
    Result() R
}
type AggFunc[T, R any] func() Aggregator[T, R]
```

To merge two partial aggregators we need a third method. There
were three candidate designs:

**A. Add `Merge` to `Aggregator` directly.**
*Rejected.* Breaking change. Every existing aggregator
(internal + user-defined) would need a `Merge` method even when
they're never used in parallel.

**B. Pair a constructor with an external combiner function.**
*Rejected.* `func GroupByParallel(in, key, newAgg, mergeAgg, build)` —
ergonomic loss, easy to mis-pair, and the combiner needs access
to internal aggregator state which violates encapsulation.

**C. Define a new opt-in interface that *extends* `Aggregator`.**
*Selected.* `ParallelAggregator[T, R]` adds `Merge(other Aggregator[T, R])`.
`ParallelAggFunc[T, R]` is the constructor type. `GroupByParallel`
takes only `ParallelAggFunc`; the existing serial `GroupBy` is
unchanged. Aggregators that want to participate in parallel
group-by opt in by providing the extra method (cheap — three of
the prebuilt ones already do, see §3).

Type assertion inside `Merge` to recover the concrete peer type
(`other.(*Counter[T])` etc.) is safe because `GroupByParallel`
always pairs a single `ParallelAggFunc` with itself — every
partial aggregator in the run is built by the same constructor,
so they share the same concrete type.

## 3. Prebuilt parallel aggregators

The three prebuilt aggregators all have trivially commutative,
associative state:

| Type | State | Merge rule |
|---|---|---|
| `Counter[T]` | `N int64` | `N += other.N` |
| `Summer[T, N Number]` | `sum N` | `sum += other.sum` |
| `Averager[T, N Number]` | `sum N`, `n int64` | `sum += other.sum; n += other.n` |

All three got a `Merge(other Aggregator[T, R])` method in the
2026-04-27 edit, plus `New*` constructors that return
`ParallelAggFunc`. Min/Max are not in the prebuilt set today
(they are inlined in the synthesized aggregator that codegen
emits for `group-by … -min … -max …`); §5 covers their merge
rule.

## 4. The runtime API

```go
// In typed/stream.go (or a new typed/groupby_parallel.go).

func GroupByParallel[T, S, O any, K comparable](
    in     Stream[T],
    keyFn  func(T) K,
    newAgg ParallelAggFunc[T, S],
    build  func(K, S) O,
) iter.Seq[O]
```

Optionally exposed as a method `Stream[T].GroupBy(...)` for
ergonomics, mirroring `Stream.Where`.

Edge cases:

- Empty `Stream` (no shards): yields nothing.
- Empty shard: contributes no keys to the merge, still
  `wg.Wait()`-ed.
- Single shard (n=1): degenerates to a single `Add()` loop with
  one trivial Combine — same shape as serial `GroupBy`.

## 5. Codegen: synthesized parallel aggregator

The codegen path
(`cmd/ssql/commands/typed_groupby.go::buildTypedAggregator`)
emits a custom aggregator type per call, e.g.:

```go
type EmployeeRowAggregator struct {
    agg0       int64    // count
    agg1       float64  // sum(salary)
    agg2       float64  // avg(salary) running sum
    agg2_n     int64    //               running count
    agg3       int64    // min(years)
    agg3_have  bool
    agg4       int64    // max(years)
    agg4_have  bool
}

func (a *EmployeeRowAggregator) Add(r EmployeeRow) { ... }
func (a *EmployeeRowAggregator) Result() EmployeeRowAggregatorResult { ... }
```

Parallel codegen will additionally emit:

```go
func (a *EmployeeRowAggregator) Merge(other typed.Aggregator[EmployeeRow, EmployeeRowAggregatorResult]) {
    o, ok := other.(*EmployeeRowAggregator)
    if !ok { return }
    a.agg0 += o.agg0                         // count
    a.agg1 += o.agg1                         // sum
    a.agg2   += o.agg2; a.agg2_n += o.agg2_n // avg
    if o.agg3_have && (!a.agg3_have || o.agg3 < a.agg3) {
        a.agg3 = o.agg3; a.agg3_have = true  // min
    }
    if o.agg4_have && (!a.agg4_have || o.agg4 > a.agg4) {
        a.agg4 = o.agg4; a.agg4_have = true  // max
    }
}
```

Per-aggregation merge rules:

| Function | Merge of (a, b) |
|---|---|
| `count` | `a.N += b.N` |
| `sum` | `a.sum += b.sum` |
| `avg` | `a.sum += b.sum; a.n += b.n` (compute `sum/n` only at `Result`) |
| `min` | `if b.have && (!a.have || b.v < a.v) { a.v = b.v; a.have = true }` |
| `max` | `if b.have && (!a.have || b.v > a.v) { a.v = b.v; a.have = true }` |

The constructor function passed to `GroupByParallel` becomes:

```go
func() typed.ParallelAggregator[EmployeeRow, EmployeeRowAggregatorResult] {
    return &EmployeeRowAggregator{}
}
```

…and the call site flips from `typed.GroupBy(...)` to
`typed.GroupByParallel(...)`. The build function is unchanged.

## 6. Ordering semantics

`typed.GroupBy` (serial) emits keys in **first-seen order across
the input**. `GroupByParallel` cannot match that exactly because
the input is partitioned across shards. The contract:

- **Within a shard:** first-seen order is preserved
- **Across shards:** shard-0's first-seen keys come before shard-1's
  new first-seen keys (i.e. shard order)

This matches what users already get from `Stream.Serial()` and
the per-shard CSV sink, so no new mental model. Tests assert it
deterministically; the order is reproducible across runs (no
goroutine scheduling dependence) because Combine walks shards in
index order.

If a user genuinely needs serial-equivalent ordering they can
fall back to `SSQLGO=typed`.

## 7. Where parallel group-by should win big

The expected speedup is dominated by the row-scan / `Add()`
phase; Combine is constant-ish. So parallel group-by should
deliver close to `min(#cores, useful parallelism)` speedup on
realistic workloads.

| Workload shape | Expected parallel speedup |
|---|---|
| 10M rows × 1k groups, count + sum + avg | close to #cores |
| 10M rows × 10M groups (every row unique key) | poor — Combine becomes the bottleneck |
| 100k rows × 100k groups | poor — too small for parallelism cost |
| 1B rows × 100 groups, count only | excellent — almost embarrassingly parallel |

Rule of thumb: parallel group-by wins when `#rows ≫ #groups`. If
`#groups ≈ #rows`, the workload is closer to a write-everything
sink and the per-shard buffer pattern (already shipped for CSV)
applies — but the *map merge* still makes parallel group-by lose
in that regime because each shard's map is large.

## 8. Failure modes and what we won't ship

- **No global thread-safe map.** Each shard owns its map; we never
  share. Considered and rejected `sync.Map` because the per-row
  contention cost would erase the parallelism.
- **No partial-key partitioning** (i.e. shard-by-hash-of-key).
  Considered: hash the key, route each row to the shard owning
  that hash bucket, no Merge needed. *Rejected for v1*: would
  need a per-row channel send between the upstream `Stream` and
  the group-by, recreating the channel-fan-in cost we eliminated
  in `Where`. Could be a useful future variant for cases where
  `#groups ≈ #rows`.
- **No streaming Result.** The whole input must complete before
  the first output row. This matches DuckDB's aggregating sink
  semantics and matches the existing serial `typed.GroupBy`.

## 9. Benchmark plan

Workload (mirrors `cmd/ssql-typed-scale/data.csv` — already on
disk):

- Input: 10 M rows of `(id, name, dept_id, age, salary)`
- Group: by `dept_id` (1 000 distinct values)
- Aggregations: `count`, `sum(salary)`, `avg(salary)`, `min(age)`, `max(age)`
- Sink: `to csv` (1 000 output rows so the sink is negligible)

Three configurations:

| Config | What | Generator |
|---|---|---|
| `typed-serial` | `SSQLGO=typed` | existing |
| `typed-parallel` | `SSQLGO=parallel` | NEW |
| DuckDB | equivalent SQL | reference |

Run each three times after warm-up; report mean wall time.

Success criteria for v1:

- Parallel **at least 2× faster than serial** on the headline
  workload. (Anything less means the merge cost is closer to the
  scan cost than we modelled — investigate.)
- **Output content identical** (after sort) to typed-serial.
- **Ordering test** validates within-shard + cross-shard
  insertion-order contract.

## 10. Implementation order

1. ✅ Add `ParallelAggregator` and `ParallelAggFunc` types to
   `typed/agg.go`
2. ✅ Add `Merge` to `Counter`, `Summer`, `Averager` and provide
   `New*` constructors that return `ParallelAggFunc`
3. **Implement `GroupByParallel`** in `typed/stream.go` (or new
   file `typed/groupby_parallel.go`)
4. Unit tests in `typed/` verifying parity with serial `GroupBy`
   over the same input (compare results, modulo ordering)
5. Bench in `typed/concurrency_bench_test.go`
6. Codegen: synthesized aggregator gets `Merge`, parallel mode
   emits `typed.GroupByParallel`, drop `rejectParallelMode("group-by")`
7. End-to-end pipeline test (build a tiny 100k-row corpus,
   compare typed vs parallel output)
8. Headline benchmark on the 10M-row corpus
9. Update `typed-codegen-proposal.md` §5d, `typed-reference.md`,
   `claude/concurrency.md`, journal

## 11. Future work (out of scope for v1)

- **Hash-partitioned `Stream` source.** Route each row to the
  shard owning `hash(key) mod nShards`. Eliminates the Merge phase
  entirely (each shard owns disjoint keys), at the cost of a fan-
  out channel. Worth measuring; not in v1.
- **Spill to disk** for groups that don't fit in memory. Not in
  v1.
- **Parallel Combine.** Currently the merge is sequential. For
  cases where #groups is huge (millions), the merge could itself
  be parallelised by partitioning the keyspace. Not in v1.
- **`GroupByOrderedParallel`.** Like `GroupByOrdered` but for
  pre-sorted input. Each shard would still own a contiguous run
  of keys; merge is trivial (just concatenate, no key collisions
  except possibly at shard boundaries). Probably low demand.
- **Streaming sink for Result.** If the downstream is a
  `to count` / `to first` style sink, we could yield per-key
  results as Combine walks them, instead of materialising the
  full map. Not in v1.

## See also

- [`typed-codegen-proposal.md`](typed-codegen-proposal.md) §5d —
  parallel-mode codegen status, including the per-shard CSV
  buffer pattern.
- [`typed-concurrency-proposal.md`](typed-concurrency-proposal.md) —
  the original PoC results that motivated `Stream[T]`.
- [`../../claude/concurrency.md`](../../claude/concurrency.md) §12 —
  the per-shard buffer pattern.
- [`typed/agg.go`](../../typed/agg.go) — current aggregator
  primitives.
- [`typed/stream.go`](../../typed/stream.go) — current parallel
  runtime.
