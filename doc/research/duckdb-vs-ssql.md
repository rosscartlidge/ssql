# DuckDB vs ssql: A Comparison

**Date:** 2026-03-16

## Overview

DuckDB and ssql solve overlapping problems from different directions. DuckDB is an embedded analytical database that happens to read files. ssql is a stream processor that happens to use SQL-style naming. Understanding where they overlap and diverge helps position ssql and informs the `generate sql` design.

## Philosophy

| | DuckDB | ssql |
|---|---|---|
| Model | SQL query engine | Unix pipeline |
| Execution | Columnar, vectorized, whole-query optimization | Row-by-row streaming, lazy evaluation |
| Interface | SQL dialect | CLI commands + Go library |
| Data model | Tables with schemas | Streams of records |
| State | In-memory database (can persist) | Stateless pipeline stages |
| Composability | Subqueries, CTEs | Unix pipes, process substitution |

## Where DuckDB Wins

### Raw Analytical Performance

DuckDB's columnar engine is designed for analytics. For aggregation over large datasets, it's not even close.

```bash
# ssql: row-by-row, single-threaded
ssql from big.csv | ssql group-by region -sum revenue total
# ~minutes for 100M rows

# DuckDB: columnar, vectorized, parallel
duckdb -c "SELECT region, SUM(revenue) AS total FROM 'big.csv' GROUP BY region"
# ~seconds for 100M rows
```

DuckDB parallelizes across cores, uses SIMD instructions, and operates on compressed columnar batches. ssql processes one record at a time through the pipeline.

### Complex Queries in One Statement

SQL excels at expressing complex joins, subqueries, and window functions in a single declaration:

```sql
-- DuckDB: one statement
SELECT d.name, e.title, e.salary,
       AVG(e.salary) OVER (PARTITION BY d.name) AS dept_avg,
       e.salary - AVG(e.salary) OVER (PARTITION BY d.name) AS vs_avg
FROM 'employees.csv' e
JOIN 'departments.csv' d ON e.dept_id = d.id
WHERE e.start_date >= '2024-01-01'
ORDER BY vs_avg DESC
LIMIT 20;
```

```bash
# ssql: multiple piped stages
ssql from employees.csv \
  | ssql join departments.csv -on dept_id id -as id dept_name name dept_name \
  | ssql where -if start_date ge 2024-01-01 \
  | ssql window -partition dept_name -avg salary dept_avg \
  | ssql update -set-expr 'salary - dept_avg' vs_avg \
  | ssql sort -desc vs_avg \
  | ssql limit 20 \
  | ssql to table
```

The SQL version is more concise and easier to optimize (the query planner sees the whole picture).

### File Format Breadth

DuckDB reads Parquet, CSV, JSON, Arrow, Excel, and can query remote files via httpfs — all with automatic schema inference, compression handling, and partition discovery.

### Ecosystem

SQL is universal. DuckDB queries can be shared with analysts, embedded in BI tools, run from Python/R/Node, or converted to PostgreSQL for production.

## Where ssql Wins

### Streaming / Infinite Data

ssql processes data lazily — records flow through the pipeline one at a time. This works for infinite streams, tailing logs, or data that doesn't fit in memory:

```bash
# Tail a log file and aggregate in real time
tail -f /var/log/app.log | ssql from json | ssql group-by status -count cnt | ssql to table

# Process a 500GB file with constant memory
ssql from huge.csv | ssql where -if status eq error | ssql to json errors.jsonl
```

DuckDB needs to materialize data into its columnar format. It can handle large files (larger than memory via disk spill), but it's not designed for infinite streams or live tailing.

### Pipeline Composition

Unix pipes make it natural to compose heterogeneous tools:

```bash
# Mix ssql with other Unix tools
curl -s api.example.com/events | ssql from json | ssql where -if level eq error | wc -l

# Combine with jq, awk, sort, etc.
ssql from data.csv | ssql where -if age gt 30 | jq '.name' | sort -u

# Process substitution for ad-hoc joins
ssql from orders.csv \
  | ssql join <(curl -s api.example.com/products | ssql from json) -using product_id \
  | ssql to table
```

DuckDB is self-contained. Getting data in and out requires its own I/O functions or client libraries.

### Code Generation

ssql pipelines generate standalone, parameterized Go programs:

```bash
SSQLGO=1 ssql from data.csv | ssql where -if age gt 25 | ssql to json out.json \
  | ssql generate go > pipeline.go

go run pipeline.go                    # defaults
go run pipeline.go -age-gt 30         # different threshold
go run pipeline.go -input other.csv   # different file
```

The generated code is readable, compilable, and 10-100x faster than the CLI pipeline. There's no equivalent in DuckDB — you get the query, but not a compiled program.

### Distributed Processing

ssql pushes computation to remote machines via SSH:

```bash
# Filter on the remote machine, aggregate locally
ssql from ssh server /data/logs.csv -- where -if status ge 500 \
  | ssql group-by endpoint -count errors \
  | ssql to table

# Shard catalog with partition pruning
ssql from catalog shards.csv -if date ge 2025-02-01 \
  -- where -if status ge 400 + group-by service -count cnt \
  | ssql to table
```

DuckDB has httpfs for remote files but no SSH integration or push-down to remote machines.

### Visualization

ssql generates self-contained HTML visualizations:

```bash
ssql from data.csv | ssql to chart output.html            # Chart.js
ssql from data.csv | ssql to explore output.html           # AG-Grid + Plotly
ssql from data.csv | ssql spectrogram | ssql to heatmap    # Plotly heatmap
ssql from data.csv | ssql to animate output.html           # Animated frames
```

DuckDB outputs query results. Visualization requires external tools (Python/matplotlib, BI tools, etc.).

### Signal Processing

ssql has native FFT, convolution, spectrogram, and IFFT — with GPU acceleration:

```bash
ssql from audio.wav | ssql fft -field amplitude | ssql to chart spectrum.html
ssql from audio.wav | ssql spectrogram -window 1024 -hop 256 | ssql to heatmap
ssql from signal.arrow | ssql convolve -field amplitude -kernel <(ssql from kernel.csv)
```

This is not something SQL databases do.

### Interactive Completion

ssql's tab completion understands data:

```
ssql from data.csv<TAB>               → caches field names
ssql where -if <TAB>                  → name, age, salary, dept
ssql where -if dept eq <TAB>          → Sales, Engineering, Marketing
ssql from ssh node1 /path<TAB>        → fetches remote headers via SSH
ssql from catalog shards.csv -if <TAB> → date, host, path, format
```

This makes exploratory data analysis fast without leaving the shell.

## Side-by-Side Examples

### Simple Filter + Aggregate

```bash
# ssql
ssql from sales.csv | ssql where -if year eq 2025 | ssql group-by region -sum revenue total | ssql to table

# DuckDB
duckdb -c "SELECT region, SUM(revenue) AS total FROM 'sales.csv' WHERE year = 2025 GROUP BY region"
```

**Verdict:** DuckDB is more concise. ssql is more discoverable (tab completion guides you).

### Window Function

```bash
# ssql
ssql from sales.csv | ssql window -partition region -order date -sum revenue running | ssql to table

# DuckDB
duckdb -c "SELECT *, SUM(revenue) OVER (PARTITION BY region ORDER BY date) AS running FROM 'sales.csv'"
```

**Verdict:** Similar verbosity. ssql's streaming window is O(N); DuckDB's vectorized engine is faster for large datasets but uses more memory.

### Multi-Source Join

```bash
# ssql
ssql from orders.csv \
  | ssql join customers.csv -using customer_id \
  | ssql join products.csv -using product_id \
  | ssql to table

# DuckDB
duckdb -c "
  SELECT * FROM 'orders.csv'
  JOIN 'customers.csv' USING (customer_id)
  JOIN 'products.csv' USING (product_id)
"
```

**Verdict:** Nearly identical. DuckDB optimizes join order automatically; ssql processes left-to-right.

### Live Stream Processing

```bash
# ssql: works
tail -f /var/log/nginx/access.log \
  | ssql from json \
  | ssql where -if status ge 500 \
  | ssql group-by endpoint -count errors \
  | ssql to table

# DuckDB: not designed for this
# Would need to repeatedly query a file or use an external tool to feed data in
```

**Verdict:** ssql wins. DuckDB isn't a stream processor.

### Generate a Reusable Program

```bash
# ssql: generates a compiled Go program
SSQLGO=1 ssql from data.csv | ssql where -if age gt 25 | ssql group-by dept -count cnt \
  | ssql generate go > report.go
go build -o report report.go
./report -input latest.csv -age-gt 30    # parameterized

# DuckDB: the query IS the program
duckdb -c "SELECT dept, COUNT(*) AS cnt FROM 'data.csv' WHERE age > 25 GROUP BY dept"
# To parameterize, write a shell script or use a client library
```

**Verdict:** Different strengths. ssql produces a fast compiled binary. DuckDB's query is simpler to run ad-hoc.

## Why DuckDB Is Fast: Vectorized Execution

Understanding DuckDB's performance advantage helps frame what ssql can and can't close the gap on.

ssql processes **one row at a time** through the whole pipeline:

```
record 1: {age: 25, name: "Alice"} → where → group-by → output
record 2: {age: 30, name: "Bob"}   → where → group-by → output
...one million more times
```

Each record crosses function call boundaries, type-checks `any` values, and touches scattered memory.

DuckDB processes **batches of ~2048 values per column** through each operator:

```
ages:  [25, 30, 22, 41, 19, 35, ...2048 values]   ← contiguous float64 array
names: ["Alice", "Bob", "Carol", ...]               ← contiguous string array

Step 1: where age > 25
  → SIMD compare 2048 ages at once → bitmask [0,1,0,1,0,1,...]

Step 2: group-by on matching rows
  → hash 2048 keys at once, aggregate in batch

Step 3: next batch of 2048...
```

**Why it's fast:**

| Factor | ssql (row-at-a-time) | DuckDB (vectorized) |
|---|---|---|
| **Data layout** | `[]any` — values scattered across heap | Typed arrays — contiguous memory |
| **Cache behavior** | ~2-5 cache misses per record | ~0 (batch fits in L1/L2) |
| **SIMD** | Not possible (`any` prevents it) | 4-8 comparisons per instruction (AVX2/512) |
| **Type dispatch** | Type switch on every field access | One typed loop per batch, no branching |
| **Function calls** | `yield(record)` per row | One operator call per 2048 rows |
| **Compiler optimization** | Loops over `any` can't auto-vectorize | Tight typed loops auto-vectorize |

**Concrete example** — `where age > 25` on 1M rows:

| | ssql | DuckDB |
|---|---|---|
| Time | ~50ms | ~0.5ms |
| Instructions per comparison | ~10 (interface dispatch) | ~0.25 (SIMD, 4-wide) |

**Why ssql can't easily match this:**

ssql's core type is `Record{schema *Schema, values []any}`. Every value is a heap-allocated interface. Vectorized execution requires typed column arrays (`[]float64`, `[]string`) — which is Arrow's data model.

The pragmatic path: for Parquet/Arrow inputs where data is already columnar, ssql could keep it columnar through filter/aggregate operations and only convert to Records at the boundaries. That's a "columnar fast path" — hard to build, but the only way to match DuckDB without becoming DuckDB. See `doc/research/parallel-processing.md` for the full analysis.

## Parquet Column Pruning: Closing the Performance Gap

One of DuckDB's biggest advantages is that it only reads the columns a query needs. With Parquet's columnar format, `SELECT dept, SUM(salary) FROM 'employees.parquet' GROUP BY dept` reads only the `dept` and `salary` columns — skipping the other 48 columns in a 50-column file. CSV reads every byte regardless.

ssql could do the same. The pipeline structure already tells us which fields are needed before execution starts.

### How It Would Work

**Step 1: Field analysis at pipeline parse time**

Each command declares which fields it reads. The pipeline can be statically analyzed:

```bash
ssql from parquet employees.parquet \           # needs: ?
  | ssql where -if dept eq Engineering \        # needs: dept
  | ssql group-by dept -sum salary total -count cnt \  # needs: dept, salary
  | ssql to table                               # needs: dept, total, cnt (output only)
```

Required columns from the Parquet file: `dept`, `salary`. The other 48 columns are never touched.

**Step 2: Pass column list to Parquet reader**

The `pqarrow.FileReader` already supports column selection:

```go
// Current: reads all columns
tbl, err := pqarrow.ReadTable(ctx, r, nil, pqarrow.ArrowReadProperties{}, mem)

// With pruning: reads only needed columns
reader, _ := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, mem)
colIndices := []int{schema.FieldIndex("dept"), schema.FieldIndex("salary")}
tbl, err := reader.ReadRowGroups(ctx, colIndices, nil)  // only dept + salary
```

**Step 3: CLI integration**

Two approaches — explicit and automatic:

**Explicit (simple, immediate):**
```bash
# New -columns flag on from parquet
ssql from parquet employees.parquet -columns dept,salary \
  | ssql where -if dept eq Engineering \
  | ssql group-by dept -sum salary total -count cnt \
  | ssql to table
```

The user specifies which columns to read. Fast to implement, no pipeline analysis needed.

**Automatic (smarter, more work):**
```bash
# ssql analyzes the pipeline and prunes automatically
ssql from parquet employees.parquet \
  | ssql where -if dept eq Engineering \
  | ssql group-by dept -sum salary total -count cnt \
  | ssql to table
```

Each command passes its required fields upstream via an environment variable or fragment metadata. The `from parquet` command reads only those columns. This requires a "planning pass" before execution — either by scanning the pipeline structure or by running a two-phase protocol (plan then execute).

**Hybrid (pragmatic):**
```bash
# -columns flag with a helper that computes the needed columns
ssql from parquet employees.parquet -columns $(ssql plan-columns "where -if dept eq Engineering | group-by dept -sum salary total") \
  | ssql where -if dept eq Engineering \
  | ssql group-by dept -sum salary total -count cnt \
  | ssql to table
```

A `plan-columns` utility parses the downstream pipeline and outputs the column list. This keeps `from parquet` simple while enabling optimization.

### Predicate Pushdown to Parquet Row Groups

Beyond column pruning, Parquet supports row group statistics (min/max per column per row group). DuckDB uses these to skip entire row groups:

```sql
-- DuckDB skips row groups where min(date) > '2025-06-30'
SELECT * FROM 'events.parquet' WHERE date >= '2025-01-01' AND date <= '2025-06-30'
```

ssql could do the same with the `pqarrow.FileReader` API, which provides access to row group metadata. A `-if` condition on the `from parquet` command could skip row groups whose statistics don't overlap the filter range:

```bash
# Reads only row groups where timestamp might be in February
ssql from parquet events.parquet -if timestamp ge 2025-02-01 -if timestamp le 2025-02-28 \
  | ssql to table
```

This mirrors the catalog partition pruning design — the same `-if` syntax, just applied to Parquet row group metadata instead of catalog CSV metadata.

### Performance Impact Estimate

For a 50-column, 100M-row Parquet file (~2GB compressed):

| Approach | Data read | Relative speed |
|---|---|---|
| CSV (baseline) | All bytes, all columns | 1x |
| Parquet (all columns) | All columns, compressed | ~3-5x faster (compression + binary format) |
| Parquet (column pruning, 2 of 50) | 4% of columns | ~25-50x faster |
| Parquet (column pruning + row group skip) | 4% of columns, ~10% of rows | ~250-500x faster |
| DuckDB (full optimization) | Same as above + vectorized execution | ~500-1000x faster |

Column pruning alone gets ssql within an order of magnitude of DuckDB for I/O-bound queries. The remaining gap is DuckDB's vectorized execution engine — but for many workflows, I/O is the bottleneck, not compute.

### Implementation Priority

1. **`-columns` flag on `from parquet`** — immediate, simple, useful today
2. **Row group predicate pushdown with `-if`** — medium effort, reuses catalog pruning patterns
3. **Automatic column inference from pipeline** — harder, needs planning pass, but the ultimate UX

## Complementary, Not Competing

The two tools serve different niches:

| Use case | Better tool |
|---|---|
| Ad-hoc analytics on files | DuckDB |
| Streaming / real-time processing | ssql |
| Complex multi-join queries | DuckDB |
| Unix pipeline composition | ssql |
| Large-scale aggregation (>10M rows) | DuckDB |
| Code generation for production | ssql |
| Signal processing (FFT, spectrogram) | ssql |
| Interactive data exploration | Both (ssql → HTML, DuckDB → BI tools) |
| Distributed SSH processing | ssql |
| Quick shell one-liners | Both (ssql with completion, DuckDB with SQL) |

The `generate sql` feature would bridge them: prototype in ssql with tab completion and streaming, then generate the DuckDB query for production-scale execution.
