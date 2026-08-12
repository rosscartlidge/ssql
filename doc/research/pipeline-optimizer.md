# Design: `generate ssql` Pipeline Optimizer

Reference: DFC065
Created: 2026-03-20
Last modified: 2026-03-20

[Back to Index](./README.md)

**Status:** Phase 1+2+3+4 implemented
**Date:** 2026-03-17

## The Idea

SQL databases don't execute queries as written — they rewrite them. A query planner analyzes the full query, applies transformation rules, and produces an optimized execution plan. The user writes what they want; the optimizer figures out how to get it fast.

ssql pipelines could work the same way. `generate ssql` reads the pipeline structure (via the existing fragment system), applies optimization rules, and outputs a rewritten ssql pipeline that produces identical results but runs faster.

```bash
# User writes naive pipeline:
(export SSQLGO=1; ssql from ssh node1 /data/events.csv \
  | ssql where -if status ge 500 \
  | ssql group-by service -count cnt \
  | ssql sort -desc cnt \
  | ssql limit 5 \
  | ssql to table) | ssql generate ssql

# Optimizer outputs:
ssql from ssh node1 /data/events.csv -- where -if status ge 500 + group-by service -count cnt \
  | ssql top 5 -by cnt \
  | ssql to table
```

The filter and aggregation moved to the remote machine. Sort + limit collapsed to `top`. Network transfer drops from millions of rows to 5.

## SQL Query Optimizer Background

### What SQL Optimizers Do

SQL query optimizers apply **rewrite rules** to transform a logical query plan into a more efficient physical plan. The key categories:

**1. Predicate Pushdown (Selection Pushdown)**
Move filter conditions as early as possible — ideally to the data source. This is typically the single most impactful optimization.
- Before: `SELECT * FROM big_table JOIN small_table ON ... WHERE big_table.date > '2025-01-01'`
- After: Filter `big_table` by date BEFORE the join, reducing join input by 90%

**2. Projection Pushdown (Column Pruning)**
Only read the columns that downstream operations need. Critical for columnar formats.
- Before: Read all 50 columns from Parquet, use 3
- After: Read only the 3 needed columns (94% less I/O)

**3. Join Reordering**
Process the smallest table first to minimize intermediate result sizes. Use statistics (row counts, distinct values) to estimate selectivity.

**4. Sort Elimination**
Remove sorts that are made redundant by downstream operations (e.g., sort before group-by that destroys order).

**5. Limit Pushdown**
Push LIMIT through operations that preserve order or don't need full input.
- `SORT BY x LIMIT 10` → top-K algorithm instead of full sort

**6. Common Subexpression Elimination**
Detect repeated computations and compute them once.

**7. Predicate Simplification**
- `WHERE x > 5 AND x > 10` → `WHERE x > 10`
- `WHERE x = 5 AND x = 5` → `WHERE x = 5`

**8. Partition Pruning**
Use partition metadata to skip irrelevant data partitions entirely.

### DuckDB's Optimizer

DuckDB has 26 optimization rules applied in sequence. The most relevant to ssql:

| DuckDB Rule | ssql Equivalent |
|---|---|
| FILTER_PUSHDOWN | SSH/catalog predicate pushdown |
| UNUSED_COLUMNS | Parquet column pruning |
| TOP_N | Sort + limit → top |
| LIMIT_PUSHDOWN | Push limit through filters |
| REORDER_FILTER | Cheap predicates before expensive |
| FILTER_PULLUP | Move filters out of subqueries (join sources) |
| DUPLICATE_GROUPS | Remove redundant GROUP BY columns |
| COMMON_AGGREGATE | Merge duplicate aggregations |
| EXPRESSION_REWRITER | Simplify `x > 5 AND x > 10` → `x > 10` |
| EMPTY_RESULT_PULLUP | Skip pipeline stages that can't produce output |

Additional DuckDB rules not directly applicable: JOIN_ORDER (ssql joins are always left-to-right), BUILD_SIDE_PROBE_SIDE (ssql always builds right side), COMPRESSED_MATERIALIZATION (columnar-specific), LATE_MATERIALIZATION (columnar-specific).

Additional DuckDB rules worth noting:
- **REGEX_RANGE**: Converts regex patterns to range filters where possible (e.g., `regex(name, "^A")` → `name >= 'A' AND name < 'B'`)
- **STATISTICS_PROPAGATION**: Uses column statistics to create new filters and prune branches
- **COLUMN_LIFETIME**: Tracks when columns are no longer needed and drops them early (mid-pipeline projection)
- **SAMPLING_PUSHDOWN**: Pushes SAMPLE into the data source

The most impactful for typical queries: **filter pushdown** and **column pruning** — they reduce the data volume before any compute happens.

**Key insight from the literature:** Streaming pipelines naturally lose some optimization opportunities (no random access, no pre-computed statistics, limited join reordering), but gain from Unix pipe back-pressure — a downstream `limit 10` already causes early termination via iterator close. The biggest wins come from **reducing data volume early** (filters and projections pushed to the source).

Sources: [DuckDB Optimizers: The Low-Key MVP](https://duckdb.org/2024/11/14/optimizers), [DuckDB Internals - Optimizer Overview](https://www.alibabacloud.com/blog/duckdb-internals---part-4-optimizer-overview_602677), [DataFusion: Optimizing SQL Part 2](https://datafusion.apache.org/blog/2025/06/15/optimizing-sql-dataframes-part-two/)

## ssql Optimization Rules

### Rule 1: SSH Predicate Pushdown (HIGH IMPACT)

Move `where` filters into `from ssh` push-down arguments.

**Before:**
```bash
ssql from ssh node1 /data/events.csv | ssql where -if status ge 500 | ssql to table
```
All records transferred over network, filtered locally.

**After:**
```bash
ssql from ssh node1 /data/events.csv -- where -if status ge 500 | ssql to table
```
Filter runs on remote machine, only matching records sent over network.

**Estimated speedup:** If 1% of records match, **100x less network transfer**. For a 10M row file on a 1Gbps link, this is the difference between 30 seconds and 0.3 seconds.

**Applicability:** Any `where` immediately after `from ssh` (or after only other pushable operations like `where`).

### Rule 2: SSH Aggregation Pushdown (HIGH IMPACT)

Move `group-by` into SSH push-down when no local operations depend on pre-aggregation data.

**Before:**
```bash
ssql from ssh node1 /data/events.csv | ssql where -if status ge 500 | ssql group-by service -count cnt | ssql to table
```
Filtered records transferred, aggregated locally.

**After:**
```bash
ssql from ssh node1 /data/events.csv -- where -if status ge 500 + group-by service -count cnt | ssql to table
```
Both filter and aggregation run remotely. Only the aggregated result (handful of rows) transfers.

**Estimated speedup:** If input has 10M rows and 100 groups, **100,000x less data transfer**.

### Rule 3: Catalog Predicate Extraction (HIGH IMPACT)

Extract predicates that match catalog metadata columns from downstream `where` into `from catalog -if`.

**Before:**
```bash
ssql from catalog shards.csv | ssql where -if date ge 2025-02-01 -if date le 2025-02-28 -if status ge 500 | ssql to table
```
All shards read, all records filtered locally.

**After:**
```bash
ssql from catalog shards.csv -if date ge 2025-02-01 -if date le 2025-02-28 -- where -if status ge 500 | ssql to table
```
Date predicates prune shards (skip January and March). Status predicate pushed to each remaining shard as remote filter.

**Estimated speedup:** If 1 of 12 monthly shards matches, **12x fewer SSH connections**. Combined with push-down filter: **120x+ less data**.

### Rule 4: Sort + Limit → Top (MEDIUM IMPACT)

Replace `sort -desc field | limit N` with `top N -by field`.

**Before:**
```bash
ssql from data.parquet | ssql sort -desc revenue | ssql limit 10 | ssql to table
```
Full O(N log N) sort, then take first 10.

**After:**
```bash
ssql from data.parquet | ssql top 10 -by revenue | ssql to table
```
O(N log K) heap, K=10. Constant memory instead of materializing all records.

**Estimated speedup:** For 10M rows: sort is ~10 seconds, top-10 is ~1 second. **10x faster, 1000x less memory**.

### Rule 5: Parquet Column Pruning (MEDIUM-HIGH IMPACT)

Analyze downstream operations to determine which columns are needed, add `-columns` to `from parquet`.

**Before:**
```bash
ssql from parquet employees.parquet | ssql where -if dept eq Engineering | ssql group-by dept -sum salary total | ssql to table
```
Reads all 50 columns.

**After:**
```bash
ssql from parquet employees.parquet -columns dept,salary | ssql where -if dept eq Engineering | ssql group-by dept -sum salary total | ssql to table
```
Reads only 2 columns (4% of data).

**Estimated speedup:** For a 50-column file: **~25x less I/O**. For a 100-column file: **~50x**.

**Required fields analysis:**
- `where -if dept eq Engineering` needs: `dept`
- `group-by dept -sum salary total` needs: `dept`, `salary`
- Union of needed fields: `dept`, `salary`

### Rule 6: Predicate Reordering (LOW-MEDIUM IMPACT)

Move cheap predicates before expensive ones within a `where` clause.

**Before:**
```bash
ssql where -if-expr 'regex(email, "^[a-z]+@company\\.com$")' -if status eq active
```
Expensive regex runs on every record, then cheap string comparison.

**After:**
```bash
ssql where -if status eq active -if-expr 'regex(email, "^[a-z]+@company\\.com$")'
```
Cheap string comparison filters first, regex only runs on matches.

**Estimated speedup:** If `status eq active` filters 90% of records: **~10x faster for the expr evaluation**.

### Rule 7: Redundant Sort Elimination (LOW IMPACT)

Remove sorts that are immediately followed by operations that destroy ordering.

**Before:**
```bash
ssql from data.csv | ssql sort name | ssql group-by dept -count cnt | ssql to table
```
Sort is wasted — group-by doesn't preserve or require sorted input.

**After:**
```bash
ssql from data.csv | ssql group-by dept -count cnt | ssql to table
```

**Estimated speedup:** Saves the full sort cost. For 10M rows: **~5-10 seconds saved**.

### Rule 8: Adjacent Where Merge (LOW IMPACT)

Merge consecutive `where` commands into a single one.

**Before:**
```bash
ssql from data.csv | ssql where -if age gt 18 | ssql where -if status eq active | ssql to table
```
Two pipeline stages, two JSONL encode/decode cycles.

**After:**
```bash
ssql from data.csv | ssql where -if age gt 18 -if status eq active | ssql to table
```
One pipeline stage, one encode/decode.

**Estimated speedup:** Saves one JSONL serialization round-trip. **~1.5x for filter-heavy pipelines**.

### Rule 9: Catalog + SSH Combined Pushdown (HIGH IMPACT)

For catalog queries, combine shard pruning with per-shard push-down in a single optimized command.

**Before:**
```bash
ssql from catalog shards.csv | ssql where -if region eq us-east -if status ge 500 | ssql group-by service -count errors | ssql sort -desc errors | ssql limit 5 | ssql to table
```

**After:**
```bash
ssql from catalog shards.csv -if region eq us-east -- where -if status ge 500 + group-by service -count errors | ssql top 5 -by errors | ssql to table
```

Optimizations applied:
1. `region` is a catalog column → extracted to `-if` for shard pruning
2. `status` filter → pushed to each shard as remote filter
3. `group-by` → pushed to each shard (partial aggregation)
4. `sort -desc + limit 5` → collapsed to `top 5 -by`

**Estimated speedup:** 10 shards → 2 (region pruning) × 100x (push-down filter) × 10x (remote aggregation) × 10x (top vs sort). **~20,000x total for large datasets**.

### Rule 10: Distinct Pushdown (LOW-MEDIUM IMPACT)

Push `distinct` through operations that don't add duplicates.

**Before:**
```bash
ssql from data.csv | ssql where -if age gt 18 | ssql include name dept | ssql distinct | ssql to table
```

**After:**
```bash
ssql from data.csv | ssql where -if age gt 18 | ssql distinct | ssql include name dept | ssql to table
```

Moving distinct before projection can reduce the number of records projected. However, distinct after projection with fewer columns may find more duplicates. The optimizer would need cardinality estimates to decide.

## Example: Full Pipeline Optimization

### Example 1: Multi-Node Analytics

**Input (naive):**
```bash
ssql from ssh node1 /data/events/2025-01.csv \
  | ssql union -file <(ssql from ssh node2 /data/events/2025-02.csv) \
  | ssql union -file <(ssql from ssh node3 /data/events/2025-03.csv) \
  | ssql where -if timestamp ge 2025-02-01 -if timestamp le 2025-02-28 \
  | ssql where -if status ge 500 \
  | ssql group-by service -count errors -avg duration avg_dur \
  | ssql sort -desc errors \
  | ssql limit 10 \
  | ssql to table
```

**Optimized:**
```bash
ssql from ssh node2 /data/events/2025-02.csv \
  -- where -if status ge 500 + group-by service -count errors -avg duration avg_dur \
  | ssql top 10 -by errors \
  | ssql to table
```

Optimizations applied:
1. **Timestamp predicate eliminates node1 and node3** entirely (January and March data, query asks for February)
2. **Status filter pushed to node2** (runs remotely)
3. **Group-by pushed to node2** (aggregation runs remotely)
4. **Union eliminated** (only one source remains after pruning)
5. **Sort + limit → top**

**Estimated speedup:** 3 nodes × 10M rows each = 30M rows transferred → ~10 rows transferred. **~3,000,000x less network I/O**.

### Example 2: Wide Parquet File

**Input (naive):**
```bash
ssql from parquet transactions.parquet \
  | ssql where -if amount gt 10000 -if currency eq USD \
  | ssql group-by merchant_category -sum amount total -count cnt -avg amount avg_amount \
  | ssql sort -desc total \
  | ssql limit 20 \
  | ssql to table
```

**Optimized:**
```bash
ssql from parquet transactions.parquet -columns amount,currency,merchant_category \
  | ssql where -if currency eq USD -if amount gt 10000 \
  | ssql group-by merchant_category -sum amount total -count cnt -avg amount avg_amount \
  | ssql top 20 -by total \
  | ssql to table
```

Optimizations applied:
1. **Column pruning**: 3 of 50 columns (94% less I/O)
2. **Predicate reordering**: cheap string compare (`currency eq USD`) before numeric compare
3. **Sort + limit → top**

**Estimated speedup:** 25x (column pruning) × 10x (top vs sort) = **~250x**.

### Example 3: Join Optimization

**Input (naive):**
```bash
ssql from parquet orders.parquet \
  | ssql join customers.parquet -using customer_id \
  | ssql where -if country eq US -if order_date ge 2025-01-01 \
  | ssql group-by category -sum total revenue \
  | ssql to table
```

**Optimized:**
```bash
ssql from parquet orders.parquet -columns customer_id,category,total,order_date \
  | ssql where -if order_date ge 2025-01-01 \
  | ssql join <(ssql from parquet customers.parquet -columns customer_id,country | ssql where -if country eq US) -using customer_id \
  | ssql group-by category -sum total revenue \
  | ssql to table
```

Optimizations applied:
1. **Column pruning on both sides** of the join
2. **Predicate pushdown**: `order_date` filter moved before join (reduces left side)
3. **Predicate pushdown into join source**: `country` filter moved into the right side of the join (reduces right side)

**Estimated speedup:** If date filter keeps 10% and country filter keeps 30%: join input reduced from N×M to 0.1N × 0.3M = **~33x fewer comparisons** plus I/O savings from column pruning.

## Architecture

### How It Works

`generate ssql` uses the same fragment pipeline as `generate go` and `generate sql`:

```bash
(export SSQLGO=1; ssql from ssh node1 /data/events.csv \
  | ssql where -if status ge 500 \
  | ssql to table) | ssql generate ssql
```

Each command emits its fragment with the `Command` field. The optimizer:

1. **Parses** all fragments into a logical pipeline representation
2. **Applies rules** in priority order (highest impact first)
3. **Emits** the optimized pipeline as an ssql command string

### Rule Application Order

Rules are applied in dependency order — some rules create opportunities for others:

1. **Adjacent where merge** (simplifies subsequent analysis)
2. **Catalog predicate extraction** (creates `-if` args from where clauses)
3. **SSH predicate pushdown** (moves where into `--` args)
4. **SSH aggregation pushdown** (moves group-by into `--` args after filters)
5. **Parquet column pruning** (analyzes remaining pipeline for needed fields)
6. **Sort + limit → top** (pattern replacement)
7. **Redundant sort elimination** (removes sorts before group-by)
8. **Predicate reordering** (cheap before expensive within where)

### Output Modes

```bash
# Default: print optimized pipeline
... | ssql generate ssql

# With explanation of what changed
... | ssql generate ssql -explain

# Execute the optimized pipeline directly
... | ssql generate ssql -run

# Show both original and optimized for comparison
... | ssql generate ssql -diff
```

The `-explain` mode would annotate each optimization:
```
-- Optimization: SSH predicate pushdown (where -if status ge 500 → remote)
-- Optimization: Sort + limit → top (sort -desc cnt | limit 5 → top 5 -by cnt)
-- Estimated speedup: ~100x (network I/O reduction)

ssql from ssh node1 /data/events.csv -- where -if status ge 500 | ssql top 5 -by cnt | ssql to table
```

### Rule 11: Empty Result Detection (LOW IMPACT, HIGH VALUE)

Detect contradictory predicates and skip the entire pipeline.

**Before:**
```bash
ssql from data.csv | ssql where -if status eq active -if status eq inactive | ssql group-by dept -count cnt | ssql to table
```

**After:**
```
-- Pipeline produces no results (contradictory predicates: status eq active AND status eq inactive)
```

No computation needed. Simple to detect for equality contradictions on the same field.

### Rule 12: Predicate Simplification (LOW IMPACT)

Simplify redundant or overlapping predicates.

**Before:**
```bash
ssql where -if age gt 5 -if age gt 10
```

**After:**
```bash
ssql where -if age gt 10
```

For range predicates on the same field: keep the tighter bound. `gt 5 AND gt 10` → `gt 10`. `lt 20 AND lt 15` → `lt 15`.

### Rule 13: Limit Pushdown Through Filter (MEDIUM IMPACT)

When a limit follows a filter, the limit can't be pushed through (filter may reduce rows). But when a limit follows operations that don't change row count (rename, cast, include), it can be moved earlier to reduce work.

**Before:**
```bash
ssql from data.csv | ssql rename -as old_name name | ssql cast -type age int | ssql limit 100 | ssql to table
```

**After:**
```bash
ssql from data.csv | ssql limit 100 | ssql rename -as old_name name | ssql cast -type age int | ssql to table
```

Limit moved before rename and cast — processes 100 records instead of all.

### Rule 14: Mid-Pipeline Column Dropping (MEDIUM IMPACT)

Inspired by DuckDB's COLUMN_LIFETIME rule. Drop columns as soon as no downstream operation needs them, reducing JSONL serialization cost through the pipeline.

**Before:**
```bash
ssql from data.csv | ssql where -if dept eq Sales | ssql include name salary | ssql to table
```
All 50 columns flow through `where` as JSONL, even though only `dept`, `name`, `salary` are ever used.

**After:**
```bash
ssql from data.csv | ssql include dept name salary | ssql where -if dept eq Sales | ssql include name salary | ssql to table
```

An early `include` drops 47 columns before they enter the pipeline. The optimizer computes the union of all downstream field references and inserts a projection immediately after `from`.

**Estimated speedup:** For a 50-column file where 3 are needed: **~15x less JSONL serialization** through every pipeline stage.

## New ssql Features Needed

Some optimizations require new features that are simple to add:

### 1. `from parquet -columns` (Column Pruning)
Already designed in `performance-improvement-plan.md`. Required for Rule 5.

### 2. Partial Aggregation Merging
When `group-by` is pushed to remote shards, the local side needs to merge partial aggregations:
- `COUNT` from shards → `SUM` of counts locally
- `SUM` from shards → `SUM` of sums locally
- `AVG` from shards → `SUM(sum)/SUM(count)` locally (need both sum and count)
- `MIN`/`MAX` from shards → `MIN`/`MAX` of mins/maxes locally

This is a new `merge-aggregations` command or a flag on `group-by`:
```bash
ssql from catalog shards.csv -- group-by region -sum revenue total -count cnt \
  | ssql group-by region -merge-sum total -merge-count cnt
```

### 3. `from parquet -if` (Row Group Pruning)
Already designed. Uses Parquet row group statistics to skip non-matching row groups.

## Implementation Phases

### Phase 1: SSH Pushdown + Top Collapse — DONE

Implemented in `cmd/ssql/commands/generate_ssql.go`. Registered via `RegisterGenerateSSQL` in `main.go`.

**Rules implemented (5 total):**
1. **Adjacent where merge** (Rule 8) — consecutive `where` commands combined into one
2. **SSH predicate pushdown** (Rule 1) — `from ssh` + `where` → push filter into SSH `--` args
3. **SSH aggregation pushdown** (Rule 2) — `from ssh` (with pushdown) + `group-by` → append with `+`
4. **Sort + limit → top** (Rule 4) — `sort -desc FIELD` + `limit N` → `top N -field FIELD`
5. **Redundant sort elimination** (Rule 7) — `sort` before `group-by` → removed

**Flags:** `-run` (execute optimized pipeline via bash), `-explain` (print applied rules to stderr).

**Implementation details:**
- Reads JSONL code fragments from stdin (same as `generate go`/`generate sql`)
- Parses each fragment's `Command` string into `pipelineCmd` structs
- Rules applied in dependency order (where-merge first to simplify subsequent analysis)
- Group-by's second fragment (empty Command) is automatically skipped (marked Removed)
- SSH commands with push-down render as `ssql from ssh host path -- where ... + group-by ...`
- Shell quoting applied to args with special characters

**Verified output:**
```bash
# SSH pushdown
ssql from ssh node1 /data/events.csv -- where -if status ge 500 | ssql to table

# SSH agg pushdown
ssql from ssh node1 /data/events.csv -- where -if status ge 500 + group-by service -count cnt | ssql to table

# sort+limit → top
ssql from csv data.csv | ssql top 10 -field revenue | ssql to table

# where merge
ssql from csv data.csv | ssql where -if age gt 25 -if dept eq sales | ssql to table

# sort elimination
ssql from csv data.csv | ssql group-by dept -count cnt | ssql to table
```

### Phase 2: Catalog Extraction + Predicate Improvements — DONE

Added to `cmd/ssql/commands/generate_ssql.go`. All in the same file as Phase 1.

**New rules implemented (5 total, 10 cumulative):**
1. **Catalog predicate extraction** (Rule 3) — reads catalog CSV header at optimization time to identify metadata columns (collapsing `field_from`/`field_to` to `field`), moves matching `where -if` conditions into `from catalog -if` for shard pruning, leaves non-matching conditions for push-down
2. **Catalog predicate pushdown** (Rule 9, part 1) — moves remaining `where` after `from catalog` into `--` push-down args (same pattern as SSH)
3. **Catalog aggregation pushdown** (Rule 9, part 2) — moves `group-by` after `from catalog` (with existing push-down) into `--` args with `+` separator
4. **Predicate simplification** (Rules 11+12) — tightens redundant same-field ranges (`age gt 5 AND age gt 10` → `age gt 10`), detects contradictions (`eq X AND eq Y`, `eq X AND ne X`, `gt 20 AND lt 10`) and reports empty result
5. **Predicate reordering** (Rule 6) — sorts `-if` conditions within each AND-clause by operator cost: `eq`/`ne` (cheapest) → `gt`/`ge`/`lt`/`le` → `contains`/`startswith`/`endswith` → `regex` (most expensive)

**New data structures:**
- `whereCondition` — parsed `{Field, Operator, Value}` for `-if` conditions
- `whereClause` — one OR-group with `[]whereCondition` + `[]string` exprs
- `parseWhereArgs`/`buildWhereArgs` — round-trip where RawArgs ↔ structured clauses
- Catalog fields on `pipelineCmd`: `IsCatalog`, `CatalogFile`, `CatalogFilters`, `CatalogPushDown`, etc.

**Implementation details:**
- Catalog extraction reads actual catalog CSV at optimization time (`readCatalogMetadataColumns`); silently skips if file not accessible
- Only extracts from single-clause where (multi-clause OR is semantically harder to split)
- `errEmptyResult` sentinel error short-circuits pipeline rendering and prints diagnostic
- Predicate simplification handles mixed `gt`/`ge` and `lt`/`le` on the same field using float64 comparison
- Rule application order: where-merge → SSH pushdown → catalog extraction → catalog pushdown → predicate simplification → predicate reorder → sort rules

**Verified output:**
```bash
# Catalog extraction + pushdown
ssql from catalog test-data/test-catalog.csv -if date ge 2025-02-01 -- where -if status ge 500 | ssql to table

# Full combined: extraction + pushdown + aggregation
ssql from catalog test-data/test-catalog.csv -if date ge 2025-02-01 -- where -if status ge 500 + group-by service -count cnt | ssql to table

# Predicate simplification
ssql from csv data.csv | ssql where -if age gt 10 | ssql to table  # was: age gt 5 AND age gt 10

# Empty result detection
pipeline produces no results (contradictory predicates)  # status eq active AND status eq inactive

# Predicate reordering
ssql where -if status eq active -if name regex '^[a-z]+'  # was: regex first, eq second
```

### Phase 3: Parquet Column Pruning — DONE

Added to `cmd/ssql/commands/generate_ssql.go`.

**Rules implemented (1 active, 2 rejected after benchmarking; 11 active cumulative):**
1. **Parquet column pruning** (Rule 5) — analyzes all downstream commands to collect referenced field names, adds `-columns` to `from parquet`. Bails out if any command uses expressions (`-if-expr`, `-set-expr`, `-expr`) or `distinct` (which reference arbitrary fields). Skips if `-columns` already present. **Benchmarked: 4.9x–14.9x faster.**

**Rejected after benchmarking:**
- **Mid-pipeline column dropping** (Rule 14) — inserts `include` after `from csv` to drop unneeded columns. Benchmarked at **1.4x–1.5x SLOWER** because the extra pipeline process (JSONL encode/decode + process spawn) costs more than the serialization savings from fewer columns. Even with 20→2 columns and 5+ downstream stages, the overhead dominates. Code retained but disabled.
- **Limit pushdown** (Rule 13) — moves `limit` before row-preserving commands. Benchmarked at **no measurable difference** because Unix pipe backpressure already provides early termination via SIGPIPE — downstream `limit` causes upstream to stop writing after ~N rows regardless of position. Only relevant for in-process composition (`generate go`), not CLI pipelines. Code retained but disabled.

**New infrastructure (retained — useful for Parquet pruning and future rules):**
- `collectDownstreamFields(cmds, startIdx)` — walks all non-removed commands after a given index, returns `(map[string]bool, allFieldsNeeded bool)`
- `extractReferencedFields(cmd)` — per-command field extraction for: `where`, `group-by`, `sort`, `top`, `include`, `rename`, `cast`, `join`, `update`, `window`. Returns `allNeeded=true` for `distinct`, `exclude`, expressions, or unknown commands (safe fallback)

**Verified output:**
```bash
# Parquet column pruning (3 fields from wide file)
ssql from parquet employees.parquet -columns age -columns dept -columns salary | ssql where -if age gt 25 | ssql group-by dept -sum salary total | ssql to table
```

## Benchmarks

All benchmarks: 1M rows, median of 3 runs.
Host: Intel Core Ultra 9 275HX (24 cores), Linux. ssql v4.28.0.

**Test data:**
- `wide.csv` — 1M rows × 20 cols (131 MB), `wide.parquet` — same (33 MB)
- `events.csv` — 1M rows × 6 cols (49 MB)
- `orders.csv` — 1M rows × 5 cols (38 MB)
- `customers.jsonl` — 50K rows × 4 cols (3.7 MB)

### Active rules

| Test | Naive | Optimized | Speedup | Rule(s) |
|---|---|---|---|---|
| Parquet 20→1 col + sort→top | 2.93s | 0.20s | **14.8x** | parquet-column-pruning + sort-limit-to-top |
| Parquet 20→3 cols | 2.14s | 0.42s | **5.1x** | parquet-column-pruning |
| Join: left+right filter (1M×50K) | 2.67s | 0.79s | **3.4x** | join-predicate-pushdown |
| Join: left-only filter | 2.71s | 0.77s | **3.5x** | join-predicate-pushdown |
| Join: right-only filter | 2.68s | 0.86s | **3.1x** | join-predicate-pushdown |
| Predicate reorder (regex→eq) | 3.26s | 1.31s | **2.5x** | predicate-reorder |
| Sort elimination before group-by | 2.07s | 0.85s | **2.4x** | sort-elimination |
| Sort + limit → top | 3.06s | 1.84s | **1.7x** | sort-limit-to-top |
| Combined (merge+reorder+top) | 2.19s | 1.92s | **1.1x** | where-merge + predicate-reorder + sort-limit-to-top |
| Where merge (2 → 1 command) | 0.77s | 0.74s | **1.04x** | where-merge |

**Rules not measurable locally (network-bound):**
- SSH predicate pushdown, SSH aggregation pushdown, catalog predicate extraction, catalog pushdown — impact is proportional to network transfer reduction, estimated 10x–10,000x+ for remote data.
- Predicate simplification, empty result detection — eliminate redundant or impossible work.

### Rejected rules

| Test | Naive | "Optimized" | Result | Why |
|---|---|---|---|---|
| CSV column drop (20 → 2 cols) | 2.00s | 3.07s | **1.5x SLOWER** | Extra process overhead > serialization savings |
| CSV column drop (long pipeline) | 2.30s | 3.24s | **1.4x SLOWER** | Same — doesn't amortize even with 5+ stages |
| Limit pushdown (cast+rename) | 0.014s | 0.014s | **No change** | Unix backpressure already provides early termination |

### Key insights

- **Parquet column pruning is the biggest local win** (up to 14.8x). It modifies the existing `from` command — no extra process.
- **Join pushdown is consistently 3x+.** Filtering before the join dramatically reduces the number of hash lookups.
- **Predicate reordering is surprisingly impactful** (2.5x). Cheap `eq` short-circuits before expensive `regex` evaluation.
- **Adding pipeline stages is almost never worthwhile.** Each `|` spawns a new process with full JSONL serialization overhead (~0.5–1s for 1M rows). Only rules that modify existing commands win; rules that insert new stages (like `include` for column dropping) lose.
- **Unix backpressure makes limit pushdown a no-op.** Downstream `limit` causes upstream SIGPIPE after N rows regardless of position.

### Phase 4: Join Predicate Pushdown — DONE

Added to `cmd/ssql/commands/generate_ssql.go`.

**Rules implemented (1 total, 12 active cumulative):**
1. **Join predicate pushdown** — splits `where` conditions after a `join` into left-only and right-only predicates based on which file each field belongs to. Left-only predicates are inserted as a `where` before the join (reducing left-side input). Right-only predicates are pushed into a bash process substitution `<(ssql from FILE | ssql where ...)` on the right side (reducing right-side input). Fields present in both sides (e.g., join keys) are left after the join.

**Constraints (bail out if any apply):**
- Right side is already a process substitution (`/dev/fd/N`) — can't restructure
- Where uses `-if-expr` — can't determine field ownership statically
- Where has multiple OR clauses — harder to split across sides
- Can't read either file's headers — skip silently

**Implementation details:**
- `func` fragments (right-side join sources) are marked `Removed` since they're not main pipeline stages
- `readFieldNames(filename, format)` supports CSV, TSV, and JSONL (reads first line, extracts JSON keys)
- `findLeftSource(cmds, joinIdx)` walks backwards to find the `from` command feeding the join
- Process substitution `<(...)` rendered unquoted in shell output — must not be wrapped in single quotes
- Verified with `-run`: optimized pipeline produces identical results to naive pipeline

**Verified output:**
```bash
# Left-only predicate (age from left CSV)
ssql from csv left.csv | ssql where -if age gt 25 | ssql join right.jsonl -using dept | ssql to table

# Right-only predicate (location from right JSONL)
ssql from csv left.csv | ssql join <(ssql from right.jsonl | ssql where -if location eq SF) -using dept | ssql to table

# Both sides: age→left, location→right
ssql from csv left.csv | ssql where -if age gt 25 | ssql join <(ssql from right.jsonl | ssql where -if location eq SF) -using dept | ssql to table

# Join key field (dept in both): left after join (no pushdown)
ssql from csv left.csv | ssql join right.jsonl -using dept | ssql where -if dept eq Eng | ssql to table
```

### Phase 5: Explain Mode + Cost Estimation
Add `-explain` with estimated speedup ratios based on simple heuristics (shard count, column count, filter selectivity). `-explain` already prints rule names and before/after — this would add numeric estimates.

## Summary

| Rule | Impact | Effort | Phase | Status |
|---|---|---|---|---|
| SSH predicate pushdown | 100x network | Low | 1 | **Done** |
| SSH aggregation pushdown | 1000x+ network | Low | 1 | **Done** |
| Sort + limit → top | 1.6x (measured) | Trivial | 1 | **Done** |
| Redundant sort elimination | 2.4x (measured) | Trivial | 1 | **Done** |
| Adjacent where merge | 1.04x (measured) | Low | 1 | **Done** |
| Catalog predicate extraction | 10x connections | Medium | 2 | **Done** |
| Combined catalog + SSH | 10,000x+ | Medium | 2 | **Done** |
| Predicate reordering | 2.5x (measured) | Low | 2 | **Done** |
| Predicate simplification | Minor | Low | 2 | **Done** |
| Empty result detection | Instant skip | Low | 2 | **Done** |
| Parquet column pruning | 5–15x (measured) | Medium | 3 | **Done** |
| Mid-pipeline column dropping | **1.5x SLOWER** | Medium | 3 | **Rejected** |
| Limit pushdown through non-filters | **No effect** | Low | 3 | **Rejected** |
| Predicate pushdown through joins | 3.1–3.5x (measured) | Medium | 4 | **Done** |
