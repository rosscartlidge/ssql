# ssql Performance Improvement Plan

Reference: DFC064
Created: 2026-03-20
Last modified: 2026-04-07

[Back to Index](./README.md)

**Date:** 2026-03-16 (updated 2026-04-07)
**Related:** [DuckDB Comparison](duckdb-vs-ssql.md), [Parallel Processing](parallel-processing.md)

## Current State

ssql processes records sequentially, one at a time, on a single core. For a 16-core machine processing a 50-column, 100M-row Parquet file, this leaves ~94% of available compute idle and reads ~25x more data than necessary.

DuckDB handles the same query in seconds using columnar batches, SIMD, and all cores. This plan identifies how to close that gap incrementally, ordered by effort vs impact.

## Status

| Phase | Feature | Status | Notes |
|-------|---------|--------|-------|
| 1 | Parquet column pruning | **DONE** | `-columns` flag, `ReadParquetColumns()` |
| 2a | Parallel multi-file | **DONE** | 4.4x speedup, ordered + unordered modes |
| 2b | Parallel Parquet row groups | Not done | |
| 3 | Parquet row group pruning | Not done | |
| 4 | Parallel CSV/JSON parsing | Not done | |
| 5 | Automatic column inference | **Partial** | `ruleParquetColumnPruning` in optimizer; CSV column drop benchmarked as counterproductive |
| 6 | Parallel compute | Not done | |
| 7 | Pipeline parallelism | Not done | |
| 8 | Columnar fast path | Not done | |
| NEW | PGO (profile-guided optimization) | Not done | |
| NEW | sync.Pool for records/buffers | Not done | |
| NEW | Parquet bloom filter pushdown | Not done | |
| NEW | SIMD filtering (Go 1.26 archsimd) | Not done | |

## Recommended Implementation Order

Based on effort/impact analysis, here's what to do next:

### Tier 1: Free Wins (hours, no code risk)

#### PGO — Profile-Guided Optimization
**Effort:** 1 hour | **Impact:** 5-14% across all operations | **Risk:** None

Run ssql under a representative workload, save the CPU profile, build with PGO. Go 1.19+ picks up `default.pgo` automatically. Uber reports up to 14% CPU savings fleet-wide.

```bash
# Generate profile from representative workload
ssql from large.csv | ssql where -if age gt 25 | ssql group-by dept -count n | ssql to csv > /dev/null
# Save as default.pgo in cmd/ssql/
go build -pgo=auto ./cmd/ssql
```

No code changes. Worth doing for every release.

### Tier 2: High Impact, Low Effort (days)

#### Parquet Row Group Pruning (Phase 3)
**Effort:** 2-3 days | **Impact:** 10-100x less data read | **Risk:** Low

Reuse `-if` syntax from catalog pruning. Check min/max statistics per column per row group, skip groups that can't match.

```bash
ssql from parquet events.parquet -if timestamp ge 2025-02-01 -if timestamp le 2025-02-28 | ssql to table
```

Combined with column pruning (Phase 1): read 2 of 50 columns from 1 of 10 row groups = 0.4% of the file.

#### Parquet Bloom Filter Pushdown
**Effort:** 2-3 days | **Impact:** 10-30x for selective queries | **Risk:** Low

Parquet spec supports Split Block Bloom Filters. An 8KB bloom filter per row group prunes 90%+ of row groups for equality predicates. The parquet-go library has bloom filter support.

```bash
# Equality filter on indexed column — bloom filter skips most row groups
ssql from parquet events.parquet | ssql where -if user_id eq U12345 | ssql to table
```

Can be combined with row group pruning for compound predicates.

#### sync.Pool for Records and Buffers
**Effort:** 2-3 days | **Impact:** 2-4x less GC pressure | **Risk:** Low

Pool `Record` objects, `[]byte` buffers, and CSV field slices. Reset and return after each record. Directly reduces GC pause frequency on high-throughput pipelines.

Key locations:
- `ParseJSONLine` — pool the intermediate `map[string]any`
- CSV reader — pool field slices (`ReuseRecord` is already set)
- JSONL writer — pool `json.Encoder` buffers

### Tier 3: Medium Effort, High Impact (1-2 weeks)

#### Parallel Parquet Row Groups (Phase 2b)
**Effort:** 2-3 days | **Impact:** 4-8x | **Risk:** Low

Each row group is independent. Read and decode on separate goroutines, ordered fan-in. Most Parquet files have multiple row groups.

```bash
ssql from parquet large.parquet | ssql where -if age gt 25 | ssql to table
# Automatically reads row groups in parallel
```

#### Parallel CSV Chunk Parsing (Phase 4)
**Effort:** 3-5 days | **Impact:** 3-8x for large files | **Risk:** Medium

mmap the file, find safe split points (newlines outside quotes), parse chunks on separate goroutines. Auto-enable for files > 100MB.

The new `go-simdcsv` library (SIMD delimiter detection via Go 1.26 archsimd) is worth evaluating as a drop-in for `encoding/csv`.

#### SIMD Filtering (Go 1.26 archsimd)
**Effort:** 1 week | **Impact:** 10-25x for numeric predicates | **Risk:** Medium (experimental API)

Go 1.26 ships `simd/archsimd` with 128/256/512-bit vector types. Compare 8 float64s simultaneously, batch-filter numeric columns.

```go
// Filter: where salary > 100000
// Process 8 records at a time with Float64x8
vec := archsimd.LoadFloat64x8(salaries[i:])
mask := archsimd.GreaterThanFloat64x8(vec, threshold)
```

Currently AMD64 only, behind `GOEXPERIMENT=simd`. The API may change.

### Tier 4: High Effort (weeks)

#### Parallel Compute (Phase 6)
**Effort:** 1-2 weeks | **Impact:** 2-8x CPU-bound | **Risk:** Medium

- **Parallel where:** Batch records, evaluate predicates on multiple goroutines
- **Parallel group-by:** Hash-partition by key, aggregate each partition independently
- **Parallel join probe:** Build hash table single-threaded, probe from multiple goroutines

Only beneficial when operation cost > goroutine overhead (~1μs per record).

#### Columnar Fast Path (Phase 8)
**Effort:** 2-4 weeks | **Impact:** 5-20x for Parquet/Arrow | **Risk:** High

Keep data columnar through filter and aggregate for Parquet→Parquet pipelines. Only convert to Records at boundaries. This is the only way to reach DuckDB-level performance.

## Implementation Plan (Next Steps)

**Week 1:**
1. Set up PGO for release builds (1 hour)
2. Parquet row group pruning with `-if` (2-3 days)
3. Benchmark and document results

**Week 2:**
4. sync.Pool for records/buffers (2-3 days)
5. Parquet bloom filter pushdown (2-3 days)

**Week 3-4:**
6. Parallel Parquet row groups (2-3 days)
7. Evaluate go-simdcsv for CSV parsing (1-2 days)

**After GopherCon (optional):**
8. SIMD filtering with archsimd
9. Parallel compute (where/group-by)
10. Columnar fast path

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

## New Techniques to Watch

- **Go 1.26 archsimd** — SIMD vectors in Go, still experimental but promising for numeric filtering
- **go-simdcsv** — SIMD-accelerated CSV parsing using archsimd
- **Parquet bloom filters** — already in parquet-go, just needs wiring
- **PGO** — proven 5-14% improvement, zero code changes
- **GOMEMLIMIT** — cap heap to reduce GC frequency for known-budget batch work
