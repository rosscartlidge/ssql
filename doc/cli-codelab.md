# ssql CLI Tutorial

*Command-line data processing with code generation - Still in active development*

## Table of Contents

### Documentation Navigation
- **[API Reference](api-reference.md)** - Complete function reference
- **[Getting Started Guide](codelab-intro.md)** - Learn the library fundamentals
- **[Advanced Tutorial](advanced-tutorial.md)** - Complex patterns and optimization
- **[Debugging Pipelines](cli-debugging.md)** - Debug with jq, inspect data, profile performance
- **[Troubleshooting Guide](cli-troubleshooting.md)** - Common issues and quick solutions

### Learning Path
- [Quick Start](#quick-start)
- [What is the ssql CLI?](#what-is-the-ssql-cli)
- [Basic Pipeline Operations](#basic-pipeline-operations)
- [Working with Real Data](#working-with-real-data)
- [Grouping and Aggregations](#grouping-and-aggregations)
- [SQL-Like Operations](#sql-like-operations)
- [Signal Processing](#signal-processing)
- [Distributed Processing](#distributed-processing)
- [Creating Visualizations](#creating-visualizations)
- [Code Generation](#code-generation)
- [Complete Example](#complete-example)
- [Available Commands](#available-commands)
- [What's Next?](#whats-next)

---

## Quick Start

### Installation

```bash
# Install the CLI tool
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest

# Verify installation
ssql version
```

For GPU-accelerated signal processing (FFT, convolution, correlation), see [GPU Acceleration](#signal-processing-fft-convolution-correlation) below.

### Your First Pipeline

```bash
# Create a sample CSV file
cat > employees.csv << 'EOF'
name,age,department,salary
Alice,30,Engineering,95000
Bob,25,Marketing,65000
Carol,35,Engineering,105000
David,28,Sales,70000
EOF

# Process it with a pipeline
ssql from employees.csv | \
  ssql where -if department eq Engineering | \
  ssql include name salary
```

Output:
```json
{"name":"Alice","salary":95000}
{"name":"Carol","salary":105000}
```

---

## What is the ssql CLI?

The ssql CLI brings Unix pipeline philosophy to structured data processing. It provides:

- **🔗 Pipeline Operations** - Chain commands with Unix pipes
- **📊 Built-in Visualization** - Create charts directly from pipelines
- **🤖 Code Generation** - Convert CLI commands to Go code
- **⚡ Interactive Development** - Prototype fast, then generate production code

### Key Features

**Command Chaining**
```bash
ssql from data.csv | ssql where ... | ssql group ... | ssql to chart ...
```

**Self-Generating Commands**
Every command supports `-generate` flag to emit Go code instead of executing:
```bash
ssql from -generate data.csv | ssql generate go
```

**Universal Data Format**
All commands use JSONL (JSON Lines) for inter-command communication, enabling complex pipelines.

**Debugging with jq**
Since all commands communicate via JSONL, you can inspect data at any stage with `jq`:
```bash
ssql from data.csv | jq '.' | head -5          # Pretty-print data
ssql from data.csv | jq '.age | type' | head   # Check field types
ssql ... | ssql where ... | jq -s 'length'      # Count results
```
[**See full debugging guide →**](cli-debugging.md)

> ⚠️ **Development Status**: The CLI is under active development. Commands and flags may change. Use `-help` on any command to see current options.

---

## Basic Pipeline Operations

### Reading Data

Read CSV files and output as JSONL:

```bash
ssql from employees.csv
```

Output (JSONL):
```json
{"age":30,"department":"Engineering","name":"Alice","salary":95000}
{"age":25,"department":"Marketing","name":"Bob","salary":65000}
...
```

### Reading Multiple Files

Read multiple files of the same format with explicit subcommands:

```bash
# Read all CSV files (shell expands *.csv)
ssql from csv *.csv | ssql to table

# Different schemas — merge field sets
ssql from csv east.csv west.csv -merge-schemas | ssql to table

# Track which file each record came from
ssql from csv *.csv -source file | ssql group-by file -count n | ssql to table
```

By default, all files must have identical schemas. Use `-merge-schemas` to combine files with different headers (union of all fields).

Supported formats: `csv`, `tsv`, `json`, `jsonl`. The bare `ssql from file.csv` form is single-file only.

### Multi-File Pushdown

Use `--` to push a sub-pipeline into each file before merging. This runs in parallel (one subprocess per file, capped at NumCPU) and is much faster than reading all files then filtering:

```bash
# Filter per file, then merge (4x faster than sequential on 10 files)
ssql from csv *.csv -- where -if age gt 25 | ssql to table

# Multi-stage pushdown with + separator
ssql from csv *.csv -- where -if status eq active + group-by dept -count cnt | ssql to table

# Track source file
ssql from csv *.csv -source file -- where -if value gt 100 | ssql to table

# Skip file-order preservation for max throughput
ssql from csv *.csv -unordered -- where -if age gt 25 | ssql to table
```

### Schema Headers (Automatic)

The `from` command automatically emits a schema header that preserves field order and types through pipelines:

```bash
ssql from employees.csv
```

Output (JSONL with schema header):
```json
{"_schema":{"fields":["name","age","department","salary"],"types":{"name":"string","age":"int","department":"string","salary":"int"}}}
{"name":"Alice","age":30,"department":"Engineering","salary":95000}
{"name":"Bob","age":25,"department":"Marketing","salary":65000}
...
```

**Benefits of automatic schema:**
- **Field order preservation**: Schema ensures output commands maintain the original CSV column order.
- **Type information**: Schema carries type information (string, int, float, bool) through the pipeline.
- **Better CSV output**: `ssql to csv` uses schema to output columns in the same order as the input.

**Example: Full pipeline with schema**
```bash
# Input CSV has columns: name, age, department, salary
ssql from employees.csv | \
  ssql where -if age gt 25 | \
  ssql to csv output.csv

# Output CSV has same column order: name, age, department, salary
```

**Schema flows through all commands:**
- Transform commands (`where`, `update`, `sort`, etc.) pass schema through unchanged
- Output commands (`to csv`, `to json`, `to jsonl`, `to table`) use schema for field ordering
- The schema header is automatically consumed and not included in final output

### Filtering Data

Filter records based on conditions:

```bash
# Single condition
ssql from employees.csv | \
  ssql where -if salary gt 70000

# Multiple conditions (AND)
ssql from employees.csv | \
  ssql where -if age gt 25 -if department eq Engineering

# Multiple conditions (OR) - use + separator
ssql from employees.csv | \
  ssql where -if department eq Engineering + -if department eq Sales
```

**Available Operators:**
- `eq` - Equal
- `ne` - Not equal
- `gt` - Greater than
- `lt` - Less than
- `ge` - Greater than or equal
- `le` - Less than or equal
- `contains` - String contains
- `starts` - String starts with
- `ends` - String ends with

### Selecting Fields

Select specific fields or rename them:

```bash
# Select fields
ssql from employees.csv | \
  ssql include name salary

# Rename fields
ssql from employees.csv | \
  ssql include name salary | \
  ssql rename -as name employee_name -as salary annual_salary
```

### Updating Fields

Update record fields conditionally using if-elseif-else logic:

```bash
# Unconditional update - all records
ssql from employees.csv | \
  ssql update -set status "active"

# Conditional update - only matching records
ssql from employees.csv | \
  ssql update -if salary gt 100000 -set bracket "high"

# Multiple conditions (AND logic)
ssql from employees.csv | \
  ssql update -if status eq pending -if priority eq urgent -set assignee "alice"

# If-elseif-else with + separator (first match wins)
ssql from customers.csv | \
  ssql update \
    -if purchases gt 5000 -set tier "Gold" -set discount 0.2 + \
    -if purchases gt 1000 -set tier "Silver" -set discount 0.1 + \
    -set tier "Bronze" -set discount 0.0
```

**How It Works:**
- **Without `-if`**: Updates all records
- **With `-if`**: Only updates records matching conditions, others pass through unchanged
- **Multiple `-if` flags**: AND logic (all must match)
- **`+` separator**: Creates clauses for if-elseif-else logic (first matching clause wins)
- **Default clause**: Clause with no `-if` acts as else (catches all remaining records)

**Type Inference:**
The `update` command automatically infers types from string values:
- `"123"` → integer (`int64`)
- `"99.99"` → float (`float64`)
- `"true"` / `"false"` → boolean
- `"2025-11-04"` → time.Time (if valid date format)
- Everything else → string

**Complex Example:**
```bash
# Set priority based on multiple conditions
ssql from orders.csv | \
  ssql update \
    -if status eq pending -if amount gt 10000 -set priority "critical" -set sla 24 + \
    -if status eq pending -if amount gt 1000 -set priority "high" -set sla 48 + \
    -if status eq pending -set priority "normal" -set sla 72 + \
    -set priority "low" -set sla 168
```

This keeps ALL records while selectively updating fields based on conditions.

### Writing Output

Write results to CSV:

```bash
ssql from employees.csv | \
  ssql where -if department eq Engineering | \
  ssql to csv engineers.csv
```

### Working with Excel Files

Read and write Excel (.xlsx) files:

```bash
# Read an Excel file
ssql from sales.xlsx | ssql to table

# Read a specific sheet
ssql from xlsx sales.xlsx -sheet "Q4 Results" | ssql to table

# Filter Excel data and write to a new spreadsheet
ssql from sales.xlsx | \
  ssql where -if revenue gt 50000 | \
  ssql to xlsx top_performers.xlsx

# Convert CSV to Excel
ssql from data.csv | ssql to xlsx data.xlsx

# Write to a named sheet
ssql from data.csv | ssql to xlsx report.xlsx -sheet Summary
```

### Displaying Data as Tables

Display records in a formatted table on the terminal:

```bash
# Simple table display
ssql from employees.csv | ssql to table

# With filtering
ssql from employees.csv | \
  ssql where -if department eq Engineering | \
  ssql to table

# Limit column width to prevent wrapping
ssql from employees.csv | \
  ssql to table -max-width 30

# Complex pipeline with updates and filtering
ssql from customers.csv | \
  ssql update \
    -if purchases gt 5000 -set tier "Gold" + \
    -if purchases gt 1000 -set tier "Silver" + \
    -set tier "Bronze" | \
  ssql where -if tier eq Gold | \
  ssql table
```

**Features:**
- Automatically calculates column widths
- Sorts columns alphabetically for consistent output
- Truncates long values with `...` when exceeding `-max-width`
- Works with all field types (strings, numbers, dates, etc.)
- Supports code generation with `-generate` flag

**Example output:**
```
age   city      name      salary
--------------------------------
30    NYC       Alice     95000
25    LA        Bob       75000
35    Chicago   Charlie   120000
```

---

## Working with Real Data

### Unix Tools Integration

Many modern Linux tools support JSON output, which ssql reads directly:

```bash
# Network interfaces (ip -j outputs JSON)
ip -j addr | ssql from json | ssql to table

# Block devices
lsblk -J | ssql from json | ssql to table

# Mount points
findmnt -J | ssql from json | ssql to table

# Journal logs
journalctl -o json --since today | ssql from jsonl | ssql where -if PRIORITY le 3 | ssql to table
```

For tools with fixed-width columnar output, convert to CSV with `tr` or `awk`:

```bash
# ps: simple fields (no spaces in values)
(echo "pid,uid,comm"; ps -eo pid,uid,comm --no-headers | tr -s ' ' ',' | sed 's/^,//') | ssql from csv | ssql to table

# df: POSIX mode gives consistent columns
df -P | tr -s ' ' ',' | ssql from csv | ssql where -if Use% gt 80% | ssql to table
```

For more complex parsing, [miller](https://miller.readthedocs.io/) (`mlr`) handles fixed-width to CSV conversion:

```bash
# General approach for any columnar command output
ps aux | mlr --ipprint --ifs ' ' --ocsv cat | ssql from csv | ssql to table
```

---

## Grouping and Aggregations

Group data and calculate statistics:

### Basic Aggregation

```bash
# Count records by department
ssql from employees.csv | \
  ssql group-by department -count total
```

Output:
```json
{"department":"Engineering","total":3}
{"department":"Marketing","total":2}
{"department":"Sales","total":1}
```

### Multiple Aggregations

Specify multiple aggregations in one command:

```bash
ssql from employees.csv | \
  ssql group-by department \
    -count employee_count \
    -avg salary avg_salary \
    -max salary max_salary
```

Output:
```json
{"avg_salary":98333,"department":"Engineering","employee_count":3,"max_salary":105000}
{"avg_salary":65000,"department":"Marketing","employee_count":2,"max_salary":65000}
{"avg_salary":70000,"department":"Sales","employee_count":1,"max_salary":70000}
```

**Available Aggregation Functions:**
- `count` - Count records
- `sum` - Sum values
- `avg` - Average values
- `min` - Minimum value
- `max` - Maximum value

### Rollup and Cube

Enrich each row with parent-level aggregations using `-rollup` or `-cube`:

```bash
# Hierarchical rollup: grand total + per-department subtotals
ssql from employees.csv | \
  ssql group-by department -count n -sum salary total -rollup
```

Output (each row carries its own subtotal AND the grand total):
```json
{"department":"Engineering","department_n":3,"department_total":295000,"n":6,"total":495000}
{"department":"Marketing","department_n":2,"department_total":130000,"n":6,"total":495000}
{"department":"Sales","department_n":1,"department_total":70000,"n":6,"total":495000}
```

With two grouping fields, rollup adds intermediate subtotals:

```bash
# department + level rollup
ssql from employees.csv | \
  ssql group-by department level -count n -rollup
```

Each row gets: `department_level_n` (detail), `department_n` (subtotal), `n` (grand total).

Use `-cube` for all combinations (adds cross-dimensional subtotals):

```bash
# Cube: adds level_n in addition to rollup fields
ssql from employees.csv | \
  ssql group-by department level -count n -cube
```

Each row gets: `department_level_n`, `department_n`, `level_n`, `n`.

---

## SQL-Like Operations

ssql supports common SQL operations for multi-table queries and data manipulation.

### Pagination with OFFSET and LIMIT

Skip and take records for pagination:

```bash
# Skip first 20 records, take next 10 (records 21-30)
ssql from data.csv | \
  ssql offset 20 | \
  ssql limit 10
```

Equivalent SQL:
```sql
SELECT * FROM data LIMIT 10 OFFSET 20
```

**`limit 0` means no limit** — every record passes through. This makes
the limit a dial you can leave in the pipeline: sample with
`ssql limit 1000` while developing, then set it to `0` for the full run
instead of deleting the stage. `offset 0` is the same kind of no-op. In
code generation (`generate go` / `sql` / `ssql`) a zero-valued stage is
skipped entirely — it leaves no trace in the generated program:

```bash
# Same pipeline, dialled between sampling and full runs
ssql from big.csv | ssql limit 1000 | ssql group-by dept -sum salary total   # sample
ssql from big.csv | ssql limit 0    | ssql group-by dept -sum salary total   # full run
```

### Random Samples with SAMPLE

`limit` gives you the file's HEAD — often one shard, one day, one
region. `sample` gives you a uniform random slice (SQL's TABLESAMPLE):

```bash
# Exactly 1000 uniformly-chosen rows (reservoir sampling)
ssql from big.csv | ssql sample 1000

# ~5% of rows, streaming (each row kept independently)
ssql from big.csv | ssql sample -percent 5

# Reproducible: same seed, same rows — every run, every backend
ssql from big.csv | ssql sample 1000 -seed 42
```

**The `sample` stage reads its entire input** — exact uniformity
requires it (the last row must get its chance), so unlike `limit`
there is no early exit. For big FILES, sample at the source instead:
`from csv -sample N` picks random byte offsets and seeks (Ross's
algorithm) — 14ms vs 21s for 1000 rows of a 1.2GB CSV — at the price
of approximate uniformity (a row's chance is proportional to its byte
length; near-uniform for near-uniform rows):

```bash
# Exact uniform — reads everything (slow on GBs)
ssql from big.csv | ssql sample 1000

# Approximate uniform via byte-offset seeks — milliseconds on GBs
ssql from csv big.csv -sample 1000 [-sample-seed 42]
```

`-sample` works on `from csv`, `from tsv` (delimiter auto-detected),
and `from jsonl` (a leading `_schema` header is honoured; JSON arrays
refuse — they aren't line-oriented). It needs a single seekable file:
stdin and multi-file input are loud errors — use the `sample` stage
there. Parquet source sampling is queued.

When you omit `-seed`, one is chosen and printed to stderr
(`sample: seed 1755741234 (pass -seed … to reproduce)`) so any
exploratory result can be re-run exactly. `sample 0` is the same
pass-through dial as `limit 0`. Output keeps input order — `sample`
selects rows, it doesn't shuffle them.

Seeded samples are byte-identical across execution and generated Go
(the selection is a pure function of the seed and row index).
`generate sql` translates unseeded sampling to DuckDB's
`USING SAMPLE`; a seeded sample has no cross-engine deterministic
equivalent, so `-seed` there is a loud error rather than a silent
approximation.

### Time Series: Align, Downsample, Fill

Real-world measurements arrive at irregular times; charts want a
regular grid. `resample` snaps a timestamp field onto an
epoch-aligned grid (buckets land on :00/:05 boundaries, and two
independently resampled series always align — so they join):

```bash
# One-minute grid, last-observation-carried-forward (the default)
ssql from metrics.csv | ssql resample -time ts -every 1m -value cpu | ssql to chart -type line -x ts -y cpu

# Interpolate two series onto a 5-second grid
ssql from sensors.csv | ssql resample -time ts -every 5s -value temp -value rpm -fill linear
```

Epoch units are auto-detected (announced on stderr; `-time-unit`
overrides); RFC3339 strings come back as RFC3339. Edges clamp to the
nearest observation, loudly.

DOWNSAMPLING (many points per bucket → one) is composition, not a
separate command — `bucket()` snaps to the same grid, and group-by
brings its whole aggregation vocabulary:

```bash
ssql from metrics.csv \
  | ssql update -set-expr b 'bucket(ts, "5m")' \
  | ssql group-by b -avg cpu cpu -max mem mem
```

And a gap-filled downsample is just resample applied after the
group-by — both snap to the same epoch grid, so they compose exactly.

### Cloud Data over HTTPS

Any http(s) URL is a source — no cloud SDKs, no credentials in ssql.
For private buckets, presign with your cloud's own CLI:

```bash
# Public or presigned URL — streaming read
ssql from https://example.com/data/events.csv | ssql group-by service -count n

# S3 via a presigned URL (auth stays aws-cli's problem)
URL=$(aws s3 presign s3://mybucket/events.csv --expires-in 3600)
ssql from csv "$URL" | ssql where -if status ge 400 | ssql to table

# Random access when the server supports Range (S3/GCS/Azure all do):
ssql from csv "$URL" -sample 1000          # byte-offset sample, seconds not minutes
ssql from parquet "$PURL" -columns svc     # footer + one column's byte ranges
ssql from parquet "$PURL" -records         # row count from the footer, instant
```

Format comes from the URL's path extension; use an explicit
subcommand (`from csv URL`) when the path has none. Servers that
ignore Range refuse the random-access forms loudly — plain streaming
still works. Expired presigned URLs produce a clear 403 message. For
heavy repeated work against cloud data, the better pattern is still
`ssql serve` on a VM in the data's region (compute near the data —
see the serve section).

### Fast Row Counts with -records

`from … -records` prints the record count of that exact invocation —
computed the cheap way, without reading data it doesn't have to:

```bash
ssql from big.parquet -records            # footer metadata: instant
ssql from csv big.csv -records            # newline scan: ~0.15s/GB
ssql from csv a.csv b.csv -records        # multi-file: the sum
ssql from csv big.csv -sample 1000 -records   # what -sample would emit
```

Stdin and JSON arrays have no cheap count and refuse loudly — use
`| ssql count` there (which reads everything). The served workspace's
rows/s display is powered by this protocol.

### Count Rows with COUNT

Drain a pipeline and print the row count to stdout (like `wc -l`).
Discoverable shorthand for "how many rows match this filter?":

```bash
# Total rows in the file
ssql from data.csv | ssql count

# After filtering
ssql from employees.csv | ssql where -if status eq active | ssql count

# In code-generation mode the planner picks the right runtime per
# pipeline: a parallel `Stream.SerialCount()` (drains shards
# concurrently with no fan-in cost) when the source can stay
# parallel, falling back to `typed.Count` or a serial loop otherwise.
SSQL_MODE=typed ssql from huge.csv | ssql count | ssql generate go -run
```

### Remove Duplicates with DISTINCT

Remove duplicate records:

```bash
# Distinct on all fields
ssql from data.csv | ssql distinct

# Distinct by specific fields (select fields first, then distinct)
ssql from employees.csv | \
  ssql include department location | \
  ssql distinct
```

Equivalent SQL:
```sql
SELECT DISTINCT department, location FROM employees
```

### JOIN Operations

Join two data sources on common fields:

```bash
# Inner join on same field name (use -using). Files join directly —
# csv/tsv/json inferred from the extension, exactly like `from FILE`:
ssql from employees.csv | \
  ssql join departments.csv -using dept_id

# Process substitution still works, and is how you pre-transform the
# right side (filter, rename, …) before joining:
ssql from employees.csv | \
  ssql join <(ssql from departments.csv) -using dept_id

# Join with different field names (use -on LEFT RIGHT)
ssql from orders.csv | \
  ssql join <(ssql from customers.csv) -type left -on customer_id id

# Multiple lookups from same file with field renaming
# (Reads kind.csv once, performs two lookups)
ssql from data.csv | ssql join <(ssql from kind.csv) \
  -on a_kind kind -as kind_name a_kind_name \
  - \
  -on z_kind kind -as kind_name z_kind_name
```

**Join Flags:**
- `-using FIELD` - Join on same field name in both sides
- `-on LEFT RIGHT` - Join on different field names
- `-as OLD NEW` - Rename field from right side when bringing it in
- `-type TYPE` - Join type: inner (default), left, right, full
- `-suffix SUFFIX` - Add suffix to all right-side non-key fields (e.g. `-suffix _right`)
- `-exclude-left` - Exclude non-key fields from left side
- `-exclude-right` - Exclude non-key fields from right side

**Field collision handling:** If non-key fields exist in both sides, join errors with suggestions. Use `-as`, `-suffix`, or `-exclude-left`/`-exclude-right` to resolve.

**Right-side files must have schema headers.** Use process substitution for CSV: `<(ssql from csv file.csv)`.
- `-` - Clause separator for multiple lookups from same file

**Join Types:**
- `inner` - Only matching records (default)
- `left` - All left records, matched right records
- `right` - All right records, matched left records
- `full` - All records from both sides

Equivalent SQL:
```sql
SELECT * FROM employees e
INNER JOIN departments d ON e.dept_id = d.dept_id
```

### UNION Operations

Combine multiple data sources:

```bash
# UNION (remove duplicates)
ssql from customers.csv | \
  ssql union -file suppliers.csv

# UNION ALL (keep duplicates)
ssql from file1.csv | \
  ssql union -all -file file2.csv -file file3.csv
```

Equivalent SQL:
```sql
SELECT * FROM customers
UNION
SELECT * FROM suppliers
```

### Merge Sorted Inputs

When you have multiple pre-sorted files (e.g., time-series shards, partitioned exports), `merge` combines them in sorted order with O(K) memory — no re-sorting needed:

```bash
# Merge two sorted files by timestamp
ssql from sorted1.csv | ssql merge sorted2.jsonl -by timestamp

# Merge many shards by multiple fields
ssql from chunk1.csv | \
  ssql merge chunk2.jsonl chunk3.jsonl -by dept name

# Merge descending
ssql from data1.csv | ssql merge data2.jsonl -by amount -desc

# Merge → streaming window (ideal for large time-series)
ssql from shard1.csv | \
  ssql merge <(ssql from shard2.csv) <(ssql from shard3.csv) -by date | \
  ssql window -sum revenue running_total -order date -presorted
```

**Why merge instead of union + sort?**
- `union | sort` materializes all records in memory — O(N)
- `merge` streams with O(K) memory (K = number of sources)
- Output is sorted by definition, enabling `-presorted` downstream

### Merge with Catalog

Merge data from distributed shards listed in a catalog CSV. Each shard gets its own SSH connection; the merge heap interleaves records in sort order:

```bash
# Merge all shards sorted by timestamp
ssql merge -catalog shards.csv -by timestamp | ssql to table

# With pushdown — filter on each shard before merging
ssql merge -catalog shards.csv -by timestamp -- where -if level eq ERROR | ssql to table

# Partition pruning — only connect to relevant shards
ssql merge -catalog shards.csv -by timestamp -if date_from le 2024-03-01 | ssql to table

# Track which shard each record came from
ssql merge -catalog shards.csv -by timestamp -shard-field source | ssql to table
```

Catalog paths support glob patterns — each matched file becomes a separate merge source:
```bash
# Catalog with globs — new files are picked up automatically
# shards.csv:  host,path
#              node1,/data/logs-*.csv
#              node2,/data/logs-*.csv
ssql merge -catalog shards.csv -by timestamp | ssql to table

# See which files were actually matched
ssql merge -catalog shards.csv -by timestamp -catalog-used expanded.csv | ssql to table
```

The optimizer automatically pushes downstream filters into the pushdown:
```bash
# Before optimization:
ssql merge -catalog shards.csv -by ts | ssql where -if value gt 200 | ssql to table
# After optimization:
ssql merge -catalog shards.csv -by ts -- where -if value gt 200 | ssql to table
```

---

## Window Functions

Window functions compute rankings, offsets, and aggregates **without collapsing rows** — every input row comes out enriched with computed values. This is the key difference from `group-by`, which reduces rows.

### Row Numbering

Number rows within a partition:

```bash
# Rank employees by salary within each department
ssql from employees.csv | \
  ssql window -row-number rn -partition dept -order salary -desc | \
  ssql to table
```

### Top-N Per Group

Combine `window` with `where` to get top records per group:

```bash
# Top 3 highest-paid per department
ssql from employees.csv | \
  ssql window -row-number rn -partition dept -order salary -desc | \
  ssql where -if rn le 3 | \
  ssql exclude rn | \
  ssql to table
```

### Running Totals and Aggregates

Compute cumulative values using aggregate window functions:

```bash
# Running revenue total per department
ssql from sales.csv | \
  ssql window -sum revenue running_total -partition dept -order date

# 7-day moving average
ssql from prices.csv | \
  ssql window -avg price ma7 -order date -preceding 6 -following 0
```

The `-preceding` and `-following` flags control the window frame. `-preceding N` means N rows before the current row, `-following N` means N rows after. Use `-1` for unbounded. The default is `-preceding -1 -following 0` (unbounded preceding to current row).

### Lag and Lead

Access values from previous or subsequent rows:

```bash
# Month-over-month revenue change
ssql from monthly.csv | \
  ssql window -lag revenue 1 prev_revenue -order month | \
  ssql update -set-expr change 'revenue - prev_revenue'
```

### Multiple Windows with Clauses

Use `+` to define different window specs with different partitions or ordering:

```bash
# Salary rank within dept + lag by date (different orderings)
ssql from employees.csv | \
  ssql window \
    -rank salary_rank -partition dept -order salary -desc \
    + \
    -lag hire_date 1 prev_hire -order hire_date
```

### Available Window Functions

| Flag | SQL Equivalent | Args |
|------|---------------|------|
| `-row-number` | `ROW_NUMBER()` | result |
| `-rank` | `RANK()` | result |
| `-dense-rank` | `DENSE_RANK()` | result |
| `-ntile` | `NTILE(n)` | n result |
| `-percent-rank` | `PERCENT_RANK()` | result |
| `-lag` | `LAG(field, n)` | field n result |
| `-lead` | `LEAD(field, n)` | field n result |
| `-first` | `FIRST_VALUE()` | field result |
| `-last` | `LAST_VALUE()` | field result |
| `-sum` | `SUM()` | field result |
| `-avg` | `AVG()` | field result |
| `-count` | `COUNT(*)` | result |
| `-min` | `MIN()` | field result |
| `-max` | `MAX()` | field result |

Equivalent SQL:
```sql
SELECT *, ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC) AS rn
FROM employees
```

### Streaming Mode (`-presorted`)

When input is already sorted by partition and order fields, add `-presorted` for streaming execution with O(1) memory per aggregate — no materialization needed:

```bash
# Running total on presorted time series (O(1) memory, 71x faster)
ssql from sales.csv | \
  ssql window -sum revenue running_total -order date -presorted

# 3-row moving average (O(frame_size) memory)
ssql from prices.csv | \
  ssql window -avg price ma3 -order date -preceding 2 -following 0 -presorted

# LAG for period-over-period comparison
ssql from monthly.csv | \
  ssql window -lag revenue 1 prev_revenue -order month -presorted | \
  ssql update -set-expr change 'revenue - prev_revenue'

# LEAD for lookahead
ssql from daily.csv | \
  ssql window -lead price 1 next_price -order date -presorted

# Sliding MIN/MAX (monotonic deque, O(1) amortized)
ssql from prices.csv | \
  ssql window -min price low10 -max price high10 -order date -preceding 9 -following 0 -presorted

# RANK on presorted data (134x faster than materialized)
ssql from employees.csv | \
  ssql sort dept - salary -desc | \
  ssql window -rank salary_rank -partition dept -order salary -desc -presorted
```

**When to use `-presorted`:**
- Time series data (already ordered by timestamp)
- After `ssql sort` in the pipeline
- Data from presorted CSV/JSONL files
- Large datasets where materialization is costly

**Limitations of `-presorted`:** Cannot use NTILE or PERCENT_RANK (require partition size). Does not support `Following > 0` frames.

---

## Signal Processing

ssql includes signal processing commands for frequency analysis and filtering.

### FFT (Fast Fourier Transform)

Compute frequency spectrum from time-domain signals:

```bash
# Basic FFT of a signal field
ssql from signal.csv | \
  ssql fft -field amplitude

# FFT with sample rate for frequency calculation
ssql from audio.csv | \
  ssql fft -field amplitude -rate 44100

# Include phase information
ssql from signal.csv | \
  ssql fft -field value -phase
```

Output includes:
- `index` - Frequency bin index
- `frequency` - Frequency in Hz (if sample rate specified)
- `magnitude` - Signal strength at each frequency
- `phase` - Phase angle in radians (if `-phase` flag used)

**Example: Analyze audio frequencies**
```bash
# Find dominant frequencies in audio data
ssql from audio_samples.csv | \
  ssql fft -field amplitude -rate 44100 | \
  ssql sort magnitude -desc | \
  ssql limit 10 | \
  ssql to table
```

### Convolution

Apply convolution filters for smoothing, edge detection, and custom filtering:

```bash
# Moving average smoothing (5-point)
ssql from sensor.csv | \
  ssql convolve -field reading -kernel avg -size 5

# Gaussian smoothing
ssql from data.csv | \
  ssql convolve -field value -kernel gaussian -size 11 -sigma 2.0

# Edge detection with difference kernel
ssql from signal.csv | \
  ssql convolve -field value -kernel diff

# Custom kernel weights
ssql from data.csv | \
  ssql convolve -field value -custom 0.25,0.5,0.25
```

**Built-in Kernels:**
- `avg` - Moving average (smoothing)
- `gaussian` - Gaussian smoothing (configurable sigma)
- `diff` - First derivative (edge detection)
- `laplacian` - Second derivative
- `sobel` - Sobel operator

**Flags:**
- `-field` / `-f` - Input field name (required)
- `-output` / `-o` - Output field name (default: `field_convolved`)
- `-kernel` / `-k` - Built-in kernel name
- `-size` / `-s` - Kernel size for avg/gaussian (default: 5)
- `-sigma` - Sigma for Gaussian kernel (default: 1.0)
- `-custom` / `-c` - Custom kernel as comma-separated values
- `-same` - Output same length as input (truncate edges)

**Example: Noise reduction pipeline**
```bash
# Read noisy sensor data, smooth it, then visualize
ssql from noisy_sensor.csv | \
  ssql convolve -field reading -kernel gaussian -size 7 -sigma 1.5 -same | \
  ssql chart -x timestamp -y reading_convolved -output smoothed.html
```

### CPU vs GPU Processing

Signal processing works out of the box using CPU implementations - no special setup required:

```bash
# Standard install - works everywhere
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest

# All signal processing commands work immediately
ssql from signal.csv | ssql fft -field value
ssql from sensor.csv | ssql convolve -field reading -kernel gaussian
```

**Optional GPU Acceleration:**

For large datasets, CUDA GPU acceleration provides 10-50x speedup. This requires:
1. NVIDIA GPU with CUDA support
2. CUDA toolkit installed (`nvcc` compiler)
3. Building with GPU support:

```bash
# Clone and build GPU-accelerated version
git clone https://github.com/rosscartlidge/ssql.git
cd ssql

# Build CUDA library
cd gpu && make && cd ..

# Build ssql with GPU support
go build -tags gpu -o ssql_gpu ./cmd/ssql

# Install to Go bin directory
cp ssql_gpu ~/go/bin/

# Add alias to ~/.bashrc (adjusts library path automatically)
echo 'alias ssql_gpu="LD_LIBRARY_PATH=/path/to/ssql/gpu ~/go/bin/ssql_gpu"' >> ~/.bashrc
source ~/.bashrc
```

GPU is used automatically when beneficial:
- FFT/IFFT: signals >= 16K samples (28-54x faster)
- Convolution: kernels >= 16 points
- Correlation: same as convolution

Smaller signals use CPU (faster due to GPU transfer overhead).

---

## Distributed Processing

ssql can read data from remote machines via SSH and coordinate reads across multiple shards using a catalog file.

### Reading Remote Files with `from ssh`

Read a file from a remote host over SSH:

```bash
# Read a remote CSV file
ssql from ssh myserver /data/events.csv | ssql to table

# Use ssql_gpu on the remote host
ssql from ssh myserver /data/events.csv -gpu | ssql to table
```

**Push-down filtering** sends filter and aggregation operations to the remote host, reducing the amount of data transferred:

```bash
# Filter on the remote side before streaming results back
ssql from ssh myserver /data/events.csv -- where -if status eq error | ssql to table

# Multi-step push-down: filter then aggregate remotely
ssql from ssh myserver /data/events.csv \
  -- where -if status ge 400 + group-by service -count cnt | \
  ssql to table
```

The `--` separator marks the start of remote pipeline stages. Use `+` to separate multiple stages within the remote pipeline.

### Reading Multiple Shards with `from catalog`

A catalog CSV file maps shards to their locations. It must have `host` and `path` columns, and can include optional metadata columns for partition pruning:

```csv
host,path,date_from,date_to,region
ssql-node1,/data/events/2025-01.csv,2025-01-01,2025-01-31,us
ssql-node2,/data/events/2025-02.csv,2025-02-01,2025-02-28,us
ssql-node3,/data/events/2025-03.csv,2025-03-01,2025-03-31,eu
```

Read all shards:

```bash
# Read every shard listed in the catalog
ssql from catalog shards.csv | ssql to table
```

**Partition pruning** skips shards that can't match your filter, avoiding unnecessary SSH connections:

```bash
# Range pruning: only read shards whose date range overlaps March
ssql from catalog shards.csv -if date ge 2025-03-01 | ssql to table

# Exact-value pruning: only read shards in the "us" region
ssql from catalog shards.csv -if region eq us | ssql to table

# Multiple pruning conditions
ssql from catalog shards.csv -if date ge 2025-02-01 -if region eq us | ssql to table
```

**Push-down filtering** sends operations to each shard:

```bash
# Filter on each remote shard before streaming results
ssql from catalog shards.csv -- where -if status eq error | ssql to table

# Two-level aggregation: aggregate per shard, then merge locally
ssql from catalog shards.csv \
  -- where -if status ge 400 + group-by service -count cnt | \
  ssql group-by service -sum cnt total_errors | \
  ssql to table
```

**Add provenance** to track which shard each record came from:

```bash
ssql from catalog shards.csv -shard-field _shard | ssql to table
# Each record gets a _shard field like "ssql-node1:/data/events/2025-01.csv"
```

Local shards (where `host` is `local` or `localhost`) are read directly without SSH.

**Glob patterns** in the `path` column are expanded on each host before processing. New files matching the glob are picked up automatically without editing the catalog:

```csv
host,path
node1,/data/logs-2024-*.csv
node2,/data/events/*.csv
```

Use `-catalog-used` to see which files were actually matched:

```bash
ssql from catalog shards.csv -catalog-used expanded.csv | ssql to table
cat expanded.csv  # shows one row per matched file
```

---

## Creating Visualizations

Generate interactive HTML charts with Chart.js:

### Simple Chart

```bash
ssql from employees.csv | \
  ssql to chart -x department -y salary salary_chart.html
```

Opens `salary_chart.html` with an interactive chart featuring:
- Multiple chart types (line, bar, scatter, pie, radar)
- Zoom and pan controls
- Field selection UI
- Data export to PNG/CSV

### Chart with Aggregations

```bash
ssql from employees.csv | \
  ssql group-by department -avg salary avg_salary | \
  ssql to chart -x department -y avg_salary dept_salaries.html
```

### Interactive Data Explorer

Generate a self-contained HTML app with sortable tables, charts, and aggregation UI:

```bash
# Basic exploration
ssql from sales.csv | ssql to explore output.html

# With initial chart fields and dark theme
ssql from sales.csv | ssql to explore -x date -y revenue -theme dark analysis.html
```

### Explorer with WASM Transforms

Use `-wasm` to enable client-side ssql operations (Where, Sort, Group By, Window,
Distinct, Limit, Computed Column, Pivot) powered by the same Go code as the CLI.
The WASM module is embedded in the ssql binary — no external files needed:

```bash
ssql from sales.csv | ssql to explore -wasm output.html
```

The explorer falls back to JavaScript if WASM fails to load. A green "WASM" badge
appears in the header when the module is active.

### Animated Frequency Spectrum from WAV

Use `to animate` to watch how the frequency spectrum of an audio file evolves over time.
The spectrogram command splits the signal into overlapping windows; we use 1-second
windows (`-window-size 44100` at 44.1 kHz) so each frame is one second of audio:

```bash
# Animate frequency magnitude in 1-second time slots
ssql from recording.wav | \
  ssql spectrogram -field amplitude -window-size 44100 -hop 44100 -rate 44100 -output db | \
  ssql to animate -frame time -x frequency -y magnitude -type histogram
```

This creates `animate.html` — a self-contained page with play/pause, scrubber, and
speed controls. Each frame shows the frequency spectrum (magnitude in dB) for one
second of audio.

For a heatmap view that scrolls through a long recording, split the spectrogram
into 10-second segments.  Each animated frame shows a full time × frequency
heatmap for that segment:

```bash
ssql from long_recording.wav | \
  ssql spectrogram -field amplitude -window-size 4096 -hop 4096 -rate 44100 -output db | \
  ssql update -set-expr segment 'int(time / 10)' | \
  ssql to animate -frame segment -x time -y frequency -z magnitude \
    -type heatmap -fps 1 -colorscale plasma -loop
```

Smaller windows give finer time resolution at the cost of more frames:

```bash
# 0.5-second windows, overlapping by 50%
ssql from recording.wav | \
  ssql spectrogram -field amplitude -window-size 22050 -hop 11025 -rate 44100 -output db | \
  ssql to animate -frame time -x frequency -y magnitude -type histogram -fps 4
```

---

## Code Generation

Every command supports the `-generate` flag to output Go code instead of executing:

### Generate Code from Pipeline

```bash
ssql from -generate employees.csv | \
  ssql where -generate -if department eq Engineering | \
  ssql include name salary | \
  ssql to csv -generate output.csv | \
  ssql generate go
```

Output:
```go
package main

import (
	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	records, err := ssql.ReadCSV("employees.csv")
	if err != nil {
		panic(err)
	}
	filtered := ssql.Where(func(r ssql.Record) bool {
		return ssql.GetOr(r, "department", "") == "Engineering"
	})(records)
	selected := ssql.Select(func(r ssql.Record) ssql.Record {
		return ssql.MakeMutableRecord().
			SetAny("name", ssql.GetOr(r, "name", "")).
			SetAny("salary", ssql.GetOr(r, "salary", float64(0))).
			Freeze()
	})(filtered)
	ssql.WriteCSV(selected, "output.csv")
}
```

### Shortcut: `-pipeline`

Typing `-generate` on every stage (or `(export SSQL_MODE=record; …) | ssql
generate go`) gets tedious. Every `generate` command takes the whole
pipeline as one quoted string with `-pipeline`:

```bash
ssql generate go -pipeline 'ssql from data.csv | ssql where -if age gt 25 | ssql to csv'
#   → prints the generated Go (typed mode by default)

ssql generate go -mode record -run -pipeline 'ssql from x.csv | ssql to csv'
ssql generate go -optimise -build /tmp/prog -pipeline 'ssql from x.csv | ssql to table'
ssql generate sql -run -pipeline 'ssql from x.csv | ssql group-by dept -count n | ssql to table'
ssql generate ssql -pipeline 'ssql from x.csv | ssql sort -desc n | ssql limit 5 | ssql to table'
```

`generate go` takes `-mode record|typed` (default `typed`); `generate sql`
and `generate ssql` always use record-mode fragments. All the usual flags
(`-run`, `-build`, `-optimise`, `OUTPUT`) compose. The string gets the same
preprocessing as `-script` files — `# comments` and leading-`|` continuation
lines work, so multi-line quoted pipelines are fine. (`-pipeline` replaces the
removed `ssqlgen` shell helper, which needed a one-time bashrc install.)

### Compile and Run Generated Code

```bash
# Generate code to file
ssql from -generate data.csv | \
  ssql group-by -generate region -sum sales total | \
  ssql generate go > analysis.go

# Add package initialization
cat > go.mod << 'EOF'
module analysis
go 1.23
require github.com/rosscartlidge/ssql/v4 latest
EOF

# Build and run
go mod tidy
go run analysis.go
```

### Advanced Example: Complex Pipeline with Chain()

When you use multiple transformation commands, the generated code automatically uses `ssql.Chain()` for clean, readable code:

```bash
# Complex pipeline: filter, select, sort, limit
ssql from -generate sales.csv | \
  ssql where -if revenue gt 1000 -generate | \
  ssql include salesperson revenue | \
  ssql sort revenue -desc -generate | \
  ssql limit 10 -generate | \
  ssql to csv -generate top_performers.csv | \
  ssql generate go > report.go
```

Generated code (`report.go`):
```go
package main

import (
	"fmt"
	"os"
	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	records, err := ssql.ReadCSV("sales.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Multiple operations composed with Chain()
	result := ssql.Chain(
		ssql.Where(func(r ssql.Record) bool {
			return ssql.GetOr(r, "revenue", float64(0)) > 1000
		}),
		ssql.Select(func(r ssql.Record) ssql.Record {
			return ssql.MakeMutableRecord().
				SetAny("salesperson", ssql.GetOr(r, "salesperson", "")).
				SetAny("revenue", ssql.GetOr(r, "revenue", float64(0))).
				Freeze()
		}),
		ssql.SortBy(func(r ssql.Record) float64 {
			return -ssql.GetOr(r, "revenue", float64(0))  // Negative for descending
		}),
		ssql.Limit[ssql.Record](10),
	)(records)

	ssql.WriteCSV(result, "top_performers.csv")
}
```

Compile and run:
```bash
# Setup and run
go mod init report
go mod tidy
go build -o report report.go
./report

# View results
cat top_performers.csv
```

**Key Features of Generated Code:**
- ✅ **Clean Chain() pattern** - Multiple operations composed functionally
- ✅ **Type-safe helpers** - `asFloat64()` handles both int64 and float64
- ✅ **Proper error handling** - Exit codes and stderr for errors
- ✅ **Production-ready** - Compiles and runs immediately
- ✅ **Readable** - Easy to understand and modify

### Example with Aggregations

Generate code for GROUP BY with multiple aggregations:

```bash
ssql from -generate sales.csv | \
  ssql group-by -generate region \
    -count num_sales \
    -sum revenue total_revenue \
    -avg revenue avg_revenue | \
  ssql sort -generate total_revenue -desc | \
  ssql to csv -generate region_report.csv | \
  ssql generate go > region_analysis.go
```

Generated code:
```go
package main

import (
	"fmt"
	"os"
	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	records, err := ssql.ReadCSV("sales.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	aggregated := ssql.Chain(
		ssql.GroupByFields("_group", "region"),
		ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
			"num_sales": ssql.Count(),
			"total_revenue": ssql.Sum("revenue"),
			"avg_revenue": ssql.Avg("revenue"),
		}),
	)(records)

	sorted := ssql.SortBy(func(r ssql.Record) float64 {
		return -ssql.GetOr(r, "total_revenue", float64(0))
	})(aggregated)

	ssql.WriteCSV(sorted, "region_report.csv")
}
```

This workflow enables **rapid prototyping** with the CLI, then **instant production deployment** with generated, type-safe Go code!

---

## Complete Example

### Scenario: Salary Analysis by Department

```bash
# Explore the data
ssql from employees.csv | ssql to table

# Filter and aggregate
ssql from employees.csv | \
  ssql where -if status eq active | \
  ssql group-by dept -count n -avg salary avg_sal | \
  ssql sort -desc avg_sal | \
  ssql to table

# Create a chart
ssql from employees.csv | \
  ssql group-by dept -count n -avg salary avg_sal | \
  ssql to chart -x dept -y avg_sal -type bar
```

### Pipeline Optimizer (`generate ssql`)

`generate ssql` reads the same code fragments as `generate go` and rewrites the pipeline to run faster. It applies 12 optimization rules automatically:

```bash
# Optimize a naive pipeline
(export SSQL_MODE=record; ssql from ssh node1 /data/events.csv \
  | ssql where -if status ge 500 \
  | ssql group-by service -count cnt \
  | ssql sort -desc cnt | ssql limit 10 \
  | ssql to table) | ssql generate ssql

# Output: filters and aggregation pushed to remote, sort+limit collapsed
# ssql from ssh node1 /data/events.csv -- where -if status ge 500 + group-by service -count cnt | ssql top 10 -field cnt | ssql to table
```

Use `-explain` to see which rules fired:
```bash
... | ssql generate ssql -explain
# [ssh-predicate-pushdown] ssql from ssh node1 ... | ssql where ... → ssql from ssh node1 ... -- where ...
# [ssh-aggregation-pushdown] ... | ssql group-by ... → ... + group-by ...
# [sort-limit-to-top] ssql sort -desc cnt | ssql limit 10 → ssql top 10 -field cnt
# [sort-elimination] ssql sort city → (removed — order reset by ssql sort cnt)
```

A sort whose order never matters is removed: if the pipeline re-sorts
(or groups, tops, resamples) before anything order-sensitive sees the
rows, the earlier sort did no observable work. Filters and projections
between the two don't protect it (`sort a | where … | sort b` drops
`sort a`), but `limit`, `window`, `tee`, and output stages do — they
consume the order, so the sort stays. This is the common cleanup for
workspace pipelines that accumulate sort stages from grid clicks.

Use `-run` to execute the optimized pipeline directly:
```bash
... | ssql generate ssql -run
```

Chain with `generate go` to optimize *then* compile:
```bash
(export SSQL_MODE=record; ssql from catalog shards.csv \
  | ssql where -if date ge 2025-02-01 -if status ge 500 \
  | ssql group-by service -count cnt \
  | ssql to table) | ssql generate ssql | ssql generate go
```

**Optimization rules:**
- **SSH/Catalog pushdown** — move `where` and `group-by` into remote `--` push-down args
- **Catalog predicate extraction** — move predicates matching catalog metadata columns into `-if` for shard pruning
- **Sort + limit → top** — replace `sort -desc FIELD | limit N` with `top N -field FIELD`
- **Sort elimination** — remove `sort` before `group-by` (group-by doesn't preserve order)
- **Where merge** — combine consecutive `where` commands into one
- **Predicate reorder** — cheap operators (eq) before expensive ones (regex)
- **Predicate simplification** — tighten redundant ranges, detect contradictions
- **Empty result detection** — contradictory predicates skip the entire pipeline
- **Parquet column pruning** — add `-columns` to `from parquet` based on downstream field usage
- **Join predicate pushdown** — move filters to the appropriate side of a join

### SQL Generation (`generate sql`)

`generate sql` converts an ssql pipeline into DuckDB-compatible SQL:

```bash
(export SSQL_MODE=record; ssql from data.csv \
  | ssql where -if age gt 25 \
  | ssql group-by dept -sum salary total \
  | ssql to table) | ssql generate sql

# Output:
# SELECT dept, SUM(salary) AS total
# FROM 'data.csv'
# WHERE age > '25'
# GROUP BY dept;
```

Supported commands: `from`, `where`, `group-by`, `sort`, `limit`, `offset`, `top`, `distinct`, `join`, `window`, `rename`, `cast`, `update`, `include`, `exclude`.

More examples:
```bash
# Window functions → SQL OVER clauses
(export SSQL_MODE=record; ssql from data.csv \
  | ssql window -row-number rn -partition dept -order salary -desc \
  | ssql to table) | ssql generate sql
# → SELECT *, ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC) AS rn

# Rename → DuckDB RENAME
... | ssql rename -as name full_name | ... | ssql generate sql
# → SELECT * RENAME (name AS full_name)

# Cast → DuckDB REPLACE + CAST
... | ssql cast -type age string | ... | ssql generate sql
# → SELECT * REPLACE (CAST(age AS VARCHAR) AS age)
```

Use `-run` to execute directly with DuckDB:
```bash
... | ssql generate sql -run
```

---

## Available Commands

### Data Sources
- `from [file]` - Read data from CSV, TSV, JSON, JSONL, Arrow, WAV, or XLSX file (auto-detects format, always emits schema header)
- `from csv [file...]` - Read CSV (or stdin). Multi-file: `-merge-schemas`, `-source`, `-unordered`, `-- [pushdown]`
- `from tsv [file...]` - Read TSV (or stdin). Multi-file: `-merge-schemas`, `-source`, `-unordered`, `-- [pushdown]`
- `from json [file...]` - Read JSON. Multi-file: `-merge-schemas`, `-source`, `-unordered`, `-- [pushdown]`
- `from jsonl [file...]` - Read JSONL (or stdin). Multi-file: `-merge-schemas`, `-source`, `-unordered`, `-- [pushdown]`
- `from arrow [file]` - Read Arrow
- `from wav file` - Read WAV audio. Flags: `-channel N`
- `from xlsx file` - Read Excel. Flags: `-sheet name`
- `from ssh HOST PATH` - Read remote file via SSH. Flags: `-gpu`, `-- [push-down stages]`
- `from catalog [file]` - Read shards from catalog CSV. Flags: `-if field op value`, `-shard-field name`, `-- [push-down stages]`

### Transformations
- `where` - Filter records by conditions (`-if field op value`)
- `include` - Select specific fields
- `exclude` - Remove specific fields
- `rename` - Rename fields (`-as old new`)
- `cast` - Convert field types (`-type field type`)
- `update` - Conditionally update field values (if-elseif-else logic)
- `group-by` - Group and aggregate data
- `sort` - Sort records by field (per-field direction: `sort dept - salary -desc`)
- `limit` - Take first N records
- `offset` - Skip first N records (SQL OFFSET)
- `distinct` - Remove duplicate records (SQL DISTINCT)
- `count` - Drain the pipeline and print the row count to stdout (sink, like `wc -l`)

### Analytics
- `window` - SQL-style window functions (ranking, lag/lead, running aggregates)
- `pivot` - Cross-tabulation (pivot table)

### Multi-Table Operations
- `join` - Join two data sources (SQL JOIN - inner/left/right/full). Flags: `-suffix`, `-exclude-left`, `-exclude-right`
- `union` - Combine multiple data sources (SQL UNION/UNION ALL)
- `merge` - K-way merge of pre-sorted inputs (streaming, O(K) memory). Flags: `-catalog`, `-if`, `-shard-field`, `-gpu`, `-- [pushdown]`

### Signal Processing
- `fft` - Fast Fourier Transform for frequency analysis (`-field`, `-rate`, `-phase`)
- `ifft` - Inverse FFT to reconstruct time-domain signal (`-magnitude`, `-phase`)
- `convolve` - Apply convolution filters (`-field`, `-kernel`, `-custom`, `-same`)
- `correlate` - Cross-correlation or autocorrelation (`-field`, `-with`)

- `tee FILE` - Write the stream to FILE (schema-headed JSONL) and pass it through — save intermediate results mid-pipeline; replay with `ssql from FILE`

### Outputs (using `to` subcommands)
- `to table` - Display records as formatted table
- `to markdown [-o file]` - GitHub-flavored Markdown table (numeric columns right-aligned, pipes escaped) — paste results straight into READMEs, issues, and PRs, or write a .md file
- `to csv [file]` - Write CSV file (or stdout)
- `to json [file]` - Write pretty-printed JSON array (or stdout)
- `to jsonl [file]` - Write JSONL, one JSON object per line (or stdout)
- `to arrow [file]` - Write Apache Arrow IPC file (10-20x faster I/O)
- `to xlsx [file]` - Write Excel XLSX file
- `to chart` - Create interactive HTML chart (including heatmaps via `-type heatmap`)
- `to animate` - Create animated heatmap or histogram with video-player controls
- `to explore [file]` - Create interactive data explorer (table + charts + aggregation)
  - Embeds the real ssql engine BY DEFAULT (pipeline bar with completion/help, uploads, downloads — a full offline workspace, ~5MB). `-light` for a ~1MB grid-only viewer.
- `to wav [file]` - Write WAV audio file

### Code Generation
- `generate go` - Assemble code fragments into Go program
- `generate sql` - Generate DuckDB SQL from pipeline. Flags: `-run`, `OUTPUT`
- `generate ssql` - Optimize pipeline (SSH/catalog pushdown, sort→top, Parquet pruning, join pushdown, predicate reorder/simplify). Flags: `-run`, `-explain`

### Utilities
- `functions` - Show available expression functions and operators
- `version` - Show version information

### Getting Help

```bash
# Show all commands
ssql -help

# Show command-specific help
ssql from -help
ssql where -help
ssql group-by -help
ssql to chart -help
```

### Interactive Shell

ssql is designed to be typed *live* at the bash prompt. One line in your
`~/.bashrc` enables everything in this section at once — completion plus the
whole family of single-key actions:

```bash
eval "$(ssql -shell-init)"     # completion + every key binding, in one eval
```

| Key | Action |
|---|---|
| **Ctrl-O** | Complete a field name **or value** from the upstream pipeline — names from the live schema, values sampled from the pipeline's source file (across `\|`, process substitution, and a `join`'s right side) |
| **Alt-h** | Help for the flag/command under the cursor — plus the function reference on an expression argument |
| **Alt-g** | Show the typed Go this pipeline generates (popup) |
| **Alt-r** | Compile the pipeline as typed Go and run it |
| **Ctrl-T** | Optimise the pipeline on the line, in place |
| **Alt-H** | List these key bindings |

Each binding is also available on its own flag (`-field-keybinding`,
`-help-keybinding`, `-code-keybinding`, `-run-keybinding`,
`-optimise-keybinding`) if you'd rather enable pieces individually — run `ssql`
with no arguments to see the full list. The rest of this section walks through
each one.

### Bash Completion

The CLI supports intelligent tab completion for commands, flags, and even field names:

```bash
# Install bash completion (for current session)
eval "$(ssql -completion-script)"

# Or add to ~/.bashrc
echo 'eval "$(ssql -completion-script)"' >> ~/.bashrc
source ~/.bashrc
```

Now you can tab-complete:
```bash
ssql <TAB>          # Shows all commands
ssql where <TAB>    # Shows flags like -if, -help
ssql from <TAB>     # Shows format subcommands (csv, json, ...) AND matching files
ssql from data.csv -if <TAB>   # Shows field names read from data.csv
```

**Pipeline-aware completion (in `ssql serve`).** Inside the interactive
`ssql serve` console, completion follows the schema *through* a pipeline —
completing a field position offers the fields flowing in from the upstream
stages, including ones that earlier commands renamed or added:

```
> from-loaded | rename -as name person | group-by <TAB>
    person  dept  …        (the renamed field, not the stale "name")
```

Bash **Tab** can't do this across a pipe — bash scopes a completion
function's view to the current command, so it can't see the upstream
stages (see
[doc/research/bash-pipeline-completion-options.md](research/bash-pipeline-completion-options.md)).
A **key binding** can, though, because it sees the whole line:

```bash
# Install (add to ~/.bashrc):
eval "$(ssql -field-keybinding)"
```

Then inside a pipeline, at a field position, press **Ctrl-O**:

```bash
ssql from data.csv | ssql rename -as name person | ssql group-by <Ctrl-O>
#   → completes from  person dept …   (the upstream schema, renames and all)
```

It runs the upstream under `SSQL_MODE=schema` (below) and completes the
field from the result — unique prefixes complete in place, ambiguous ones
list. Rebind the key by editing the `bind` lines the command emits.

Under the hood, `SSQL_MODE=schema` is a two-pass mode where each command
transforms a *schema header* instead of data (sources read only the
header, so it's near-instant). You can use it directly to see a pipeline's
output schema without running it:

```bash
(export SSQL_MODE=schema; ssql from data.csv | ssql group-by dept -count n) | ssql generate schema
#   → dept
#     n
```

### Optimise a Pipeline in Place (`Ctrl-O`'s cousin)

A second key binding rewrites the **whole** pipeline on the line to its
optimised form (the same rewrites as `generate ssql` — merge adjacent
`where`s, `sort … | limit` → `top`, push filters into `from ssh`, …):

```bash
eval "$(ssql -optimise-keybinding)"      # add to ~/.bashrc
```

Type a pipeline and press **Ctrl-T**:

```bash
ssql from x.csv | ssql where -if a gt 1 | ssql where -if b eq y | ssql sort -desc s | ssql limit 5  <Ctrl-T>
#   → ssql from x.csv | ssql where -if b eq y -if a gt 1 | ssql top 5 -field s
```

It runs the line through `ssql generate ssql` under `SSQL_MODE=record`, so
it reads no data — instant even on huge files — and replaces the line in
place. Undo with `Ctrl-_` (emacs) or `u` (vi) if you want the original
back. Single key, robust in both editing modes.

### Help at the Cursor (`Alt-h`)

The third key binding shows **contextual help for the flag or command under
the cursor** — what it does and what arguments it expects — without leaving
the line you're editing:

```bash
eval "$(ssql -help-keybinding)"          # add to ~/.bashrc
```

Put the cursor on (or inside the arguments of) any ssql command and press
**Alt-h**:

```bash
ssql from data.csv | ssql group-by dept -sum salary<Alt-h>
#   → -sum, -s  field result-name
#         Sum field values (field name, result name)
#         → result-name (string)        ← the argument under the cursor
```

Where Tab/`Ctrl-O` complete *candidates*, Alt-h *explains* what's there.
The help comes from `ssql -help-at`, the autocli help-at-cursor protocol —
it reads no data, just the command tree, so it's instant. Inside **tmux** it
pops a transient `display-popup` overlay (your command line is untouched);
otherwise it prints inline below the prompt. Single key, works in emacs and
vi. (Implemented as the autocli `Command.HelpAt(args, pos)` primitive, so any
autocli-based CLI can offer the same binding — see
`doc/research/interactive-help-at-cursor.md`.)

**On an expression argument**, Alt-h also appends the expression-function
reference (the same listing as `ssql functions`) — so when you're writing an
`-if-expr` / `-set-expr` / `-expr` / `-stream-expr` you can see every available
function without leaving the line:

```bash
ssql from data.csv | ssql update -set-expr label <Alt-h>
#   → -set-expr, -e  field expression
#         Set field to expression result …
#         → expression (string)
#
#     EXPRESSION FUNCTIONS AVAILABLE:
#       String Functions (16): upper(str), lower(str), trim(str), …
#       Math Functions (6): round(num), floor(num), abs(num), min(a, b), …
#       …  (scroll for Array / Date / Type / Map / Bitwise / Hash / Operators)
```

### Show and Run the Typed Go (`Alt-g`, `Alt-r`)

The same prototype pipeline you're editing can compile to a standalone typed Go
program (see [Code Generation](#code-generation) above). Two key bindings put
that one keystroke away — no need to retype the line into `ssql generate go`:

```bash
eval "$(ssql -code-keybinding)"      # Alt-g — add to ~/.bashrc
eval "$(ssql -run-keybinding)"       # Alt-r
```

- **Alt-g** shows the **typed Go** the pipeline generates, in a `tmux` popup,
  without running it — a fast way to see what the fast path looks like.
- **Alt-r** **compiles and runs** the pipeline as typed Go and streams the
  output, then prints a `[ssql: compiled in …, ran in …]` line so you can see
  how fast the compiled pipeline ran (compile time is reported separately, so
  the run figure is the pipeline's real speed on your data). If generation or
  compilation fails, the error appears in a popup subwindow rather than
  silently doing nothing.

```bash
ssql from data.csv | ssql where -if age gt 30 | ssql to table  <Alt-g>
#   → popup shows the generated typed Go program

ssql from data.csv | ssql where -if age gt 30 | ssql to table  <Alt-r>
#   → compiles the typed Go, prints its output inline, then:
#     [ssql: compiled in 1.43s, ran in 7.5ms]
```

The same timing is available non-interactively with `generate go -run -time`
(the time line goes to stderr, so it never mixes into the data on stdout):

```bash
(export SSQL_MODE=typed; ssql from data.csv | ssql top 10 -field salary | ssql to table) \
    | ssql generate go -run -time
```

Both read the pipeline structure, not the data, to generate — so producing the
code is instant even on huge files (only Alt-r then actually runs it).

### List the Key Bindings (`Alt-H`)

Forgotten which key does what? **Alt-H** (capital) pops a cheat-sheet of the
whole family — it's generated from the same table that defines the bindings, so
it can never drift from what's actually bound:

```bash
ssql from data.csv | ssql to table  <Alt-H>
#   → ssql key bindings
#       Ctrl-O   complete a field name from the upstream pipeline schema
#       Ctrl-T   optimise the ssql pipeline on the line, in place
#       Alt-g    show the typed Go this pipeline generates (without running)
#       Alt-r    compile the pipeline as typed Go and run it
#       Alt-h    help for the flag / command under the cursor
#       Alt-H    list these key bindings
```

### Reference at the Prompt: `ssql functions` and `ssql conventions`

Two in-binary reference commands round out the interactive experience — no need
to leave the terminal for the docs:

```bash
ssql functions                 # ~80 expression functions, by category
ssql functions -category math  # just one category

ssql conventions                       # cross-cutting system semantics
ssql conventions -category evaluation  # e.g. update's SET-snapshot rule
```

`ssql functions` is the same listing Alt-h appends on an expression argument.
`ssql conventions` documents the behaviours that span commands and tend to
surprise people — how `update` evaluates all assignments against the original
row (like SQL `UPDATE … SET`), the JSONL `_schema` header, the `SSQL_MODE`
codegen paths, and process-substitution sourcing — the things that aren't any
single command's `-help`.

### Understanding Command Structure (Advanced)

ssql CLI uses the **autocli** framework for declarative command definitions. This enables powerful features:

**Clause Pattern:**
Commands that support multiple items use `+` as a separator to create "clauses". Each clause can have its own set of flags:

```bash
# Multiple WHERE conditions (OR logic) - each clause after + is independent
ssql where -if age gt 30 + -if salary gt 100000

# Multiple aggregations - specify multiple flags
ssql group-by department \
  -count total \
  -avg salary avg_salary \
  -max salary max_salary
```

**How it works:**
- **Accumulate pattern**: Use the same flag multiple times for multiple operations
- **Self-documenting**: Flag names show the operation (-count, -sum, -avg)
- **Framework**: Automatically parses and validates all arguments
- **Benefits**: Type safety, auto-completion, consistent error messages

**Example breakdown:**
```bash
ssql group-by department \
  -count total \
  #  └─ count aggregation ─┘
  -avg salary avg_salary \
  #  └─ average aggregation ─┘
  -sum hours total_hours
  #  └─ sum aggregation ─┘
```

Each aggregation is independently specified and validated, giving clear error messages if arguments are missing or incorrect.

**Flag patterns:**
- `-count result-name` - Count records (1 argument)
- `-sum field result-name` - Sum field values (2 arguments)
- `-avg field result-name` - Average field values (2 arguments)
- `-min field result-name` - Minimum field value (2 arguments)
- `-max field result-name` - Maximum field value (2 arguments)

This pattern makes complex aggregations readable while maintaining type safety and completion support.

---

## What's Next?

### Workflow: CLI → Code → Production

1. **Prototype with CLI** - Quickly explore your data
   ```bash
   ssql from data.csv | ssql where ... | ssql to chart ...
   ```

2. **Generate Code** - Convert to Go when satisfied
   ```bash
   ssql from -generate data.csv | ... | ssql generate go > app.go
   ```

3. **Refine and Deploy** - Edit generated code, add error handling, deploy
   ```go
   // Add your business logic, error handling, logging, etc.
   ```

### Advanced Topics

- **[API Reference](api-reference.md)** - Full library documentation for refining generated code
- **[Getting Started Guide](codelab-intro.md)** - Learn the ssql library directly
- **[Advanced Tutorial](advanced-tutorial.md)** - Production patterns and optimization

### Key Features

ssql supports comprehensive data processing:
- **SQL Operations**: `join`, `distinct`, `offset`, `union`, `sort`, `limit`, `group-by`, `window`, `pivot`
- **Signal Processing**: `fft`, `ifft`, `convolve`, `correlate` (with optional GPU acceleration)
- **Multiple Formats**: CSV, TSV, JSON/JSONL, Apache Arrow, XLSX, WAV
- **Code Generation**: Convert CLI pipelines to standalone Go programs
- **Interactive Charts**: Chart.js visualizations with zoom/pan/export

### Need Help?

- **[Debugging Guide](cli-debugging.md)** - Learn to debug pipelines with jq
- **[Troubleshooting](cli-troubleshooting.md)** - Common issues and solutions
- **[GitHub Issues](https://github.com/rosscartlidge/ssql/issues)** - Report bugs
- **Examples** - Check `examples/` directory
- **API Reference** - Full library documentation

---

*Prototype fast with the CLI, deploy with confidence using generated Go code!* ⚡
