# Research: Parallel Processing Opportunities in ssql

**Status:** Exploratory
**Date:** 2026-03-16

## Current Architecture

ssql processes records sequentially through a pipeline:

```
from → where → group-by → to
  r1 → r1 → ...
  r2 → r2 → ...
  r3 → r3 → ...
```

Each record flows through every stage one at a time, on a single goroutine. This is elegant for streaming (constant memory, infinite data) but leaves all but one CPU core idle.

DuckDB processes data in parallel batches across all cores using vectorized execution on columnar chunks. For a 16-core machine, that's roughly 16x more throughput for CPU-bound operations.

## The Opportunity Space

### Where Parallelism Helps

Operations where the work per record is independent:

| Operation | Parallelizable? | Why |
|---|---|---|
| `where -if` | Yes (embarrassingly) | Each record evaluated independently |
| `where -if-expr` | Yes | Expression evaluation is per-record |
| `update -set-expr` | Yes | Expression per record |
| `select` / `include` / `exclude` | Yes | Field projection per record |
| `cast` | Yes | Type conversion per record |
| I/O (CSV parse, JSON parse) | Yes | Rows are independent |
| `from parquet` (row groups) | Yes | Row groups are independent chunks |

### Where Parallelism Is Harder

Operations that need to see related records:

| Operation | Challenge | Approach |
|---|---|---|
| `group-by` | Need all records for a group | Parallel partial aggregation → merge |
| `sort` | Global ordering | Parallel sort → merge |
| `distinct` | Global dedup | Parallel hash sets → merge |
| `join` | Build hash table from one side | Parallel build, parallel probe |
| `window` | Needs partition context | Parallel per-partition |
| `limit` / `offset` / `top` | Global position | Not parallelizable (but fast) |

### Where Parallelism Doesn't Help

- **I/O bound operations**: If disk/network is the bottleneck, more CPU won't help
- **Pipeline-bound operations**: If each stage is trivial (e.g., `include`), the overhead of parallelism exceeds the work
- **Small datasets**: Thread coordination overhead dominates below ~100K records

## Low-Hanging Fruit

### 1. Parallel Parquet Row Groups

**Effort:** Low
**Speedup:** Up to Nx (N = number of row groups, typically 4-16)
**Why it's easy:** Parquet files are already partitioned into independent row groups. Each can be read and decoded on a separate goroutine.

```go
// Current: sequential row group reading
for i := 0; i < reader.NumRowGroups(); i++ {
    rowGroup := reader.RowGroup(i)
    // decode and yield records...
}

// Parallel: each row group on its own goroutine, ordered output via channel
results := make(chan []Record, numRowGroups)
for i := 0; i < numRowGroups; i++ {
    go func(idx int) {
        rg := reader.RowGroup(idx)
        records := decodeRowGroup(rg)
        results <- records  // with ordering
    }(i)
}
```

The key constraint: output must maintain row group order to preserve record ordering. Use an ordered fan-in pattern (indexed channels or a priority queue).

**CLI impact:** None — transparent speedup for `from parquet`. Could add `-parallel N` flag to control concurrency.

```bash
# Transparent parallel Parquet reading
ssql from parquet large.parquet | ssql where -if age gt 25 | ssql to table

# Explicit parallelism control
ssql from parquet large.parquet -parallel 8 | ssql to table
```

### 2. Parallel Multi-Source Reading (Catalog/Union)

**Effort:** Low
**Speedup:** Up to Nx (N = number of sources)
**Why it's easy:** Already reading from multiple independent sources — just not concurrently.

```bash
# Current: reads shards sequentially
ssql from catalog shards.csv | ssql to table
# Reads node1, waits, reads node2, waits, reads node3

# Parallel: reads all shards concurrently
ssql from catalog shards.csv -parallel | ssql to table
# Reads node1 + node2 + node3 simultaneously
```

The catalog `ProcessCatalogShards` function already iterates over entries. Making it launch goroutines per shard with ordered fan-in is straightforward. SSH connections are already multiplexed, so concurrent SSH commands won't fight over connections.

```go
// Current (catalog.go)
for _, entry := range entries {
    cmd := exec.Command("ssh", entry.Host, remoteCmd)
    // read records sequentially...
}

// Parallel: launch all SSH commands, merge results
var wg sync.WaitGroup
ch := make(chan Record, 1000)
for _, entry := range entries {
    wg.Add(1)
    go func(e CatalogEntry) {
        defer wg.Done()
        cmd := exec.Command("ssh", e.Host, remoteCmd)
        for r := range readSSHOutput(cmd) {
            ch <- r
        }
    }(entry)
}
go func() { wg.Wait(); close(ch) }()
```

**Note:** Parallel catalog reading doesn't preserve shard order. If order matters, use `merge -by` after. If it doesn't (aggregation pipelines), parallel is pure win.

```bash
# Order doesn't matter — aggregation pipeline
ssql from catalog shards.csv -parallel -- group-by region -sum revenue total | ssql to table

# Order matters — use merge
ssql from catalog shards.csv -parallel -merge-by timestamp | ssql to table
```

### 3. Parallel CSV/JSON Parsing

**Effort:** Medium
**Speedup:** 2-4x on multi-core
**Why it's medium:** Need to split the file into chunks at line boundaries, parse chunks in parallel, maintain record order.

```go
// Split file into N chunks at newline boundaries
chunks := splitFileAtNewlines(file, runtime.NumCPU())

// Parse each chunk in parallel
var results [N][]Record
var wg sync.WaitGroup
for i, chunk := range chunks {
    wg.Add(1)
    go func(idx int, data []byte) {
        defer wg.Done()
        results[idx] = parseCSVChunk(data, schema)
    }(i, chunk)
}
wg.Wait()

// Yield in order
for _, batch := range results {
    for _, r := range batch {
        yield(r)
    }
}
```

The tricky parts:
- **CSV header**: Only the first chunk has the header. Others need it passed in.
- **Quoted fields with newlines**: Splitting at `\n` can break mid-field. Need to scan for valid split points.
- **Memory**: Materializes chunks. For streaming, use a pipeline of goroutines with bounded buffers instead.

```bash
# Transparent parallel CSV reading for large files
ssql from csv large.csv | ssql where -if status eq error | ssql to json

# Under the hood: auto-parallel when file > 100MB
```

## Medium-Hanging Fruit

### 4. Parallel Where/Filter

**Effort:** Medium
**Speedup:** 2-8x for expensive predicates (expressions, regex)
**Approach:** Batch records, evaluate predicate in parallel, yield matching records in order.

```go
func ParallelWhere(predicate func(Record) bool, batchSize int) Filter[Record, Record] {
    return func(input iter.Seq[Record]) iter.Seq[Record] {
        return func(yield func(Record) bool) {
            batch := make([]Record, 0, batchSize)
            results := make([]bool, batchSize)

            for r := range input {
                batch = append(batch, r)
                if len(batch) == batchSize {
                    // Evaluate predicate in parallel
                    var wg sync.WaitGroup
                    for i := range batch {
                        wg.Add(1)
                        go func(idx int) {
                            defer wg.Done()
                            results[idx] = predicate(batch[idx])
                        }(i)
                    }
                    wg.Wait()

                    // Yield matches in order
                    for i, matched := range results[:len(batch)] {
                        if matched && !yield(batch[i]) { return }
                    }
                    batch = batch[:0]
                }
            }
            // flush remaining...
        }
    }
}
```

For simple predicates (`field > 25`), the overhead exceeds the benefit — the predicate is cheaper than goroutine scheduling. But for expensive predicates (regex, expression evaluation, string operations), parallel evaluation pays off.

**Auto-detection:** Could measure predicate cost on first batch and switch to parallel only if cost per record exceeds a threshold (~1μs).

```bash
# Expensive predicate benefits from parallelism
ssql from data.parquet | ssql where -if-expr 'regex(email, "^[a-z]+@company\\.com$") and len(name) > 3' | ssql to json

# Simple predicate — parallel overhead not worth it
ssql from data.parquet | ssql where -if age gt 25 | ssql to json
```

### 5. Parallel Group-By (Map-Reduce Style)

**Effort:** Medium-High
**Speedup:** 2-8x for large datasets
**Approach:** Partition records by group key hash, aggregate in parallel, merge results.

```
Input stream → Hash partition → N goroutines aggregate independently → Merge results
```

```go
// Phase 1: Partition by hash of group key
partitions := make([][]Record, numWorkers)
for r := range input {
    key := extractGroupKey(r)
    partition := hash(key) % numWorkers
    partitions[partition] = append(partitions[partition], r)
}

// Phase 2: Aggregate each partition in parallel
results := make([]map[string]Record, numWorkers)
var wg sync.WaitGroup
for i := range numWorkers {
    wg.Add(1)
    go func(idx int) {
        defer wg.Done()
        results[idx] = aggregate(partitions[idx])
    }(idx)
}
wg.Wait()

// Phase 3: Merge (no conflicts — same key always goes to same partition)
for _, partial := range results {
    for _, r := range partial {
        yield(r)
    }
}
```

This is the classic MapReduce pattern. The hash partitioning guarantees no key conflicts between workers — each group is fully contained within one partition.

**Constraint:** Requires materializing the input (or at least buffering into partitions). Doesn't work for streaming group-by. Best paired with parallel Parquet reading where data is already batched.

```bash
# Large aggregation benefits from parallel group-by
ssql from parquet events.parquet \
  | ssql group-by region,service -sum revenue total -count cnt -avg latency avg_lat \
  | ssql to table
```

### 6. Parallel Join (Hash Build + Parallel Probe)

**Effort:** Medium
**Speedup:** 2-4x for probe phase
**Approach:** Build hash table (single-threaded or parallel), probe in parallel.

The existing `JoinHash` implementation builds a hash table from the right side and probes with the left. The build is sequential (one pass), but the probe could be parallelized — batch left-side records and probe the hash table from multiple goroutines (read-only access is safe).

```bash
# Large join benefits from parallel probe
ssql from parquet orders.parquet \
  | ssql join customers.parquet -using customer_id \
  | ssql to parquet enriched.parquet
```

## High-Hanging Fruit

### 7. Pipeline Parallelism (Concurrent Stages)

**Effort:** High
**Speedup:** Variable (depends on stage balance)
**Approach:** Each pipeline stage runs on its own goroutine, connected by buffered channels.

```
from → [chan] → where → [chan] → group-by → [chan] → to
 G1              G2                G3               G4
```

Currently, records flow synchronously through the pipeline — `where` can't process record N+1 until `to` finishes with record N. With pipeline parallelism, all stages run concurrently, overlapping I/O with compute.

```go
func PipelineParallel(stages ...Filter[Record, Record]) Filter[Record, Record] {
    return func(input iter.Seq[Record]) iter.Seq[Record] {
        // Connect stages with buffered channels
        ch := make(chan Record, 1000)
        go func() {
            for r := range input { ch <- r }
            close(ch)
        }()

        for _, stage := range stages[:len(stages)-1] {
            nextCh := make(chan Record, 1000)
            go func(s Filter[Record, Record], in, out chan Record) {
                s(chanToSeq(in))(func(r Record) bool {
                    out <- r; return true
                })
                close(out)
            }(stage, ch, nextCh)
            ch = nextCh
        }

        // Last stage yields to caller
        return stages[len(stages)-1](chanToSeq(ch))
    }
}
```

**Why it's hard:**
- `iter.Seq` is a push-based iterator — converting to/from channels adds overhead
- Buffer sizing affects latency vs throughput tradeoff
- Error propagation across goroutines is tricky
- Backpressure: if `to` is slow (disk I/O), channels fill up and block upstream

**When it helps:** When stages have different costs. E.g., `from parquet` (I/O bound) → `where -if-expr` (CPU bound) → `to json` (I/O bound). Pipeline parallelism overlaps all three.

### 8. Vectorized Batch Processing

**Effort:** Very High (architectural change)
**Speedup:** 5-20x (SIMD, cache locality)
**Approach:** Process records in columnar batches instead of one at a time.

This is fundamentally what DuckDB does. Instead of:

```
for each record:
    if record.age > 25:
        yield record
```

Process in batches:

```
ages := column("age")  // contiguous float64 array
mask := simd_gt(ages, 25)  // SIMD comparison, 256 bits at a time
yield batch.filter(mask)
```

**Why it's very hard for ssql:**
- Records are row-oriented (`Schema + []any`) — need to transpose to columnar
- `any` type prevents SIMD — need typed arrays (`[]float64`, `[]string`)
- The entire `Filter[T,T]` pipeline model assumes row-at-a-time
- Would need a parallel "batch mode" pipeline alongside the streaming one

**Where it could work incrementally:**
- Parquet data is already columnar — process it columnar without converting to Records
- Arrow data is already columnar — same opportunity
- GPU path already does this for signal processing

```bash
# Future: columnar fast path when entire pipeline is Parquet → filter → aggregate → Parquet
ssql from parquet events.parquet \
  | ssql where -if status ge 400 \
  | ssql group-by service -count cnt \
  | ssql to parquet errors.parquet
# Could detect all-columnar pipeline and use batch processing
```

## Practical Recommendations

### Phase 1: Parallel I/O (Low effort, high impact)

| Change | Effort | Expected speedup |
|---|---|---|
| Parallel Parquet row groups | 1-2 days | 4-8x for large files |
| Parallel catalog/SSH sources | 1 day | Nx (N = shard count) |
| `-parallel` flag on `from parquet` and `from catalog` | Trivial | User control |

These are the highest-ROI changes. They parallelize I/O without touching the pipeline model.

### Phase 2: Parallel Compute (Medium effort)

| Change | Effort | Expected speedup |
|---|---|---|
| Parallel where for expensive predicates | 2-3 days | 2-4x for regex/expr |
| Parallel CSV parsing | 3-4 days | 2-4x for large CSV |
| Parallel group-by (hash partition) | 3-4 days | 2-4x for large aggregations |

### Phase 3: Architectural (High effort, DuckDB-level performance)

| Change | Effort | Expected speedup |
|---|---|---|
| Pipeline parallelism (concurrent stages) | 1 week | Variable |
| Columnar batch processing for Parquet paths | 2-3 weeks | 5-20x |
| Full vectorized engine | Months | DuckDB-level |

### What NOT to Parallelize

- **Simple predicates** (`field > 25`): Goroutine overhead > predicate cost
- **Small files** (<100K rows): Sequential is faster
- **Already I/O-bound pipelines**: More CPU won't help if the disk/network is saturated
- **Streaming pipelines** (stdin, `tail -f`): Data arrives sequentially, can't parallelize the source

## The 80/20 Path

Parallel Parquet row groups + parallel catalog sources would get ssql within 5-10x of DuckDB for the common case (read large file → filter → aggregate → output). That's Phase 1, achievable in a few days, with no architectural changes.

The remaining gap is DuckDB's vectorized columnar engine — closing that fully would require rearchitecting ssql's core data model. The question is whether that's worth it given that `generate sql` could just hand off to DuckDB for performance-critical queries.

The pragmatic answer: **ssql for streaming, exploration, and code generation. DuckDB (via `generate sql`) for maximum performance on large static datasets.** Parallel I/O in ssql closes the gap enough for most interactive use cases.
