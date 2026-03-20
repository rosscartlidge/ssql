# ssql Performance Improvement Plan

**Date:** 2026-03-16
**Related:** [DuckDB Comparison](duckdb-vs-ssql.md), [Parallel Processing](parallel-processing.md)

## Current State

ssql processes records sequentially, one at a time, on a single core. For a 16-core machine processing a 50-column, 100M-row Parquet file, this leaves ~94% of available compute idle and reads ~25x more data than necessary.

DuckDB handles the same query in seconds using columnar batches, SIMD, and all cores. This plan identifies how to close that gap incrementally, ordered by effort vs impact.

## The Plan

### Phase 1: Parquet Column Pruning (Days)

**Impact:** 10-50x less I/O on wide files
**Effort:** 1-2 days
**Risk:** None — additive feature

Read only the columns the pipeline needs instead of the entire file.

**Implementation:**

Add `-columns` flag to `from parquet`:

```bash
# Read only dept and salary from a 50-column file
ssql from parquet employees.parquet -columns dept,salary \
  | ssql group-by dept -sum salary total \
  | ssql to table
```

Under the hood, use `pqarrow.FileReader.ReadRowGroups` with a column index list instead of `ReadTable` which reads everything.

```go
reader, _ := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, mem)
colIndices := []int{schema.FieldIndex("dept"), schema.FieldIndex("salary")}
tbl, _ := reader.ReadRowGroups(ctx, colIndices, nil)
```

**Future:** Auto-infer needed columns from the pipeline structure (Phase 5).

### Phase 2: Parallel Source Reading (Days)

**Impact:** Nx speedup (N = number of sources/row groups)
**Effort:** 2-3 days
**Risk:** Low — sources are independent

Two sub-tasks:

**2a. Parallel Parquet row groups:**

Each row group is an independent chunk. Read and decode them on separate goroutines, merge with ordered fan-in.

```bash
# Automatic — large Parquet files read row groups in parallel
ssql from parquet large.parquet | ssql where -if age gt 25 | ssql to table

# Explicit control
ssql from parquet large.parquet -parallel 8 | ssql to table
```

Expected speedup: 4-8x for files with multiple row groups (most Parquet files).

**2b. Parallel catalog/SSH shards:**

`ProcessCatalogShards` currently reads shards sequentially. Launch all SSH connections concurrently:

```bash
# All 10 shards read simultaneously
ssql from catalog shards.csv -parallel \
  -- where -if status ge 500 + group-by service -count cnt \
  | ssql to table
```

Expected speedup: Nx where N is shard count. SSH multiplexing already handles connection overhead.

### Phase 3: Parquet Row Group Pruning (Days)

**Impact:** 10-100x less data read for filtered queries
**Effort:** 2-3 days
**Risk:** Low — reuses catalog pruning pattern

Parquet stores min/max statistics per column per row group. Use them to skip row groups that can't match a filter — identical to catalog partition pruning.

```bash
# Reads only row groups where timestamp might be in February
ssql from parquet events.parquet -if timestamp ge 2025-02-01 -if timestamp le 2025-02-28 \
  | ssql to table
```

The `-if` syntax mirrors `from catalog -if` exactly. Implementation reads row group metadata before opening the row group.

Combined with column pruning (Phase 1): read 2 of 50 columns from 1 of 10 row groups = 0.4% of the file.

### Phase 4: Parallel CSV/JSON Parsing (Week)

**Impact:** 2-4x for large text files
**Effort:** 3-5 days
**Risk:** Medium — quoted fields with newlines complicate chunk splitting

Split the file into N chunks at line boundaries, parse each chunk on a separate goroutine, merge in order.

```bash
# Transparent parallel reading for large CSV files
ssql from csv large.csv | ssql where -if status eq error | ssql to json
```

Auto-enable when file size > 100MB. Tricky parts: CSV header only in first chunk, and split points must respect quoted fields.

### Phase 5: Automatic Column Inference (Week)

**Impact:** Makes Phase 1 automatic — no `-columns` flag needed
**Effort:** 1 week
**Risk:** Medium — needs pipeline analysis before execution

Analyze the pipeline to determine which columns are needed:

```bash
# ssql infers only dept and salary are needed
ssql from parquet employees.parquet \
  | ssql where -if dept eq Engineering \
  | ssql group-by dept -sum salary total \
  | ssql to table
```

Two approaches:
- **Environment variable protocol:** Each command exports its needed fields; `from parquet` reads them before starting
- **`plan-columns` helper:** Separate utility parses the pipeline and outputs the column list for shell substitution

### Phase 6: Parallel Compute (1-2 Weeks)

**Impact:** 2-8x for CPU-bound operations
**Effort:** 1-2 weeks
**Risk:** Medium — goroutine overhead can exceed benefit for simple operations

**6a. Parallel where (for expensive predicates):**

Batch records, evaluate predicate on multiple goroutines, yield matches in order. Only beneficial when predicate cost > goroutine overhead (~1μs per record).

```bash
# Regex predicate benefits from parallelism
ssql from parquet data.parquet \
  | ssql where -if-expr 'regex(email, "^[a-z]+@company\\.com$")' \
  | ssql to json
```

**6b. Parallel group-by (hash partition):**

Partition records by group key hash, aggregate each partition on a separate goroutine, merge results. Classic MapReduce pattern — no key conflicts between partitions.

**6c. Parallel join probe:**

Build hash table single-threaded, probe from multiple goroutines (read-only access is thread-safe).

### Phase 7: Pipeline Parallelism (Weeks)

**Impact:** Variable — depends on stage cost balance
**Effort:** 1-2 weeks
**Risk:** High — architectural change to iterator model

Run each pipeline stage on its own goroutine connected by buffered channels. Overlaps I/O-bound and CPU-bound stages.

```
from (I/O) → [chan] → where (CPU) → [chan] → group-by (CPU) → [chan] → to (I/O)
    G1                    G2                      G3                     G4
```

**Challenge:** `iter.Seq` is push-based. Converting to/from channels adds overhead and complicates backpressure. May not be worth it unless stages have very different costs.

### Phase 8: Columnar Fast Path (Months)

**Impact:** 5-20x for Parquet/Arrow pipelines
**Effort:** 2-4 weeks
**Risk:** High — parallel data model alongside row-based

For pipelines where input and output are both columnar (Parquet/Arrow), keep data columnar through filter and aggregate operations. Only convert to Records at boundaries (expressions, output formats).

```
Parquet → [columnar filter] → [columnar aggregate] → Parquet
          no Record conversion, typed arrays, auto-vectorizable
```

This is the only way to approach DuckDB-level performance without becoming DuckDB. But it requires maintaining two parallel execution paths (row-based and columnar).

## Summary

| Phase | Change | Effort | Speedup | Cumulative |
|---|---|---|---|---|
| 1 | Parquet column pruning | 1-2 days | 10-50x I/O | 10-50x |
| 2 | Parallel sources (row groups + SSH) | 2-3 days | 4-8x | 40-400x |
| 3 | Parquet row group pruning | 2-3 days | 2-10x | 80-4000x |
| 4 | Parallel CSV/JSON parsing | 3-5 days | 2-4x | — |
| 5 | Automatic column inference | 1 week | UX (no -columns flag) | — |
| 6 | Parallel compute (where/group-by/join) | 1-2 weeks | 2-8x | 160-32000x |
| 7 | Pipeline parallelism | 1-2 weeks | Variable | — |
| 8 | Columnar fast path | 2-4 weeks | 5-20x | DuckDB-level |

Phases 1-3 alone (about a week of work) get ssql within 5-10x of DuckDB for Parquet-based analytical queries. The remaining gap is CPU-bound vectorized execution — Phases 6-8 close it progressively, with Phase 8 reaching DuckDB-level for columnar pipelines.

## Decision Framework

**When to optimize ssql:**
- Interactive exploration (sub-second response on multi-GB files)
- Pipelines that stay within ssql (no DuckDB handoff)
- Streaming + batch hybrid workflows

**When to use `generate sql` instead:**
- Pure analytical queries on static files
- Queries that DuckDB's optimizer handles better (complex joins, subqueries)
- Maximum performance is the only goal

The two approaches complement each other. Phases 1-3 make ssql fast enough for interactive use. `generate sql` bridges to DuckDB when absolute performance matters.
