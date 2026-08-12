# Window / Analytic Functions Design

Reference: DFC049
Created: 2026-02-24
Last modified: 2026-03-12

[Back to Index](./README.md)

## Problem

ssql's `group-by` collapses rows — you get one output row per group. SQL window functions let you compute aggregates, rankings, and offsets **without losing rows**. Every input row comes out enriched with computed values.

Common tasks that need window functions:

| Task | SQL | ssql today |
|------|-----|------------|
| Running total | `SUM(x) OVER (ORDER BY date)` | No CLI command (library has `RunningSum` but not exposed) |
| 7-day moving average | `AVG(x) OVER (ORDER BY date ROWS 6 PRECEDING)` | No CLI command (library has `RunningAverage`) |
| Previous row's value | `LAG(x) OVER (ORDER BY date)` | Not possible |
| Row numbering | `ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC)` | Not possible |
| Rank within group | `RANK() OVER (PARTITION BY dept ORDER BY salary DESC)` | Not possible |
| Compare to group avg | `salary - AVG(salary) OVER (PARTITION BY dept)` | Requires rollup + join workaround |
| Top-N per group | `ROW_NUMBER() OVER (PARTITION BY dept ...) <= 3` | Not possible |
| Period-over-period change | `x - LAG(x) OVER (ORDER BY month)` | Not possible |

## What ssql Already Has

### Library functions (operations.go) — not exposed via CLI

**Streaming aggregations** (emit per-record, carry state forward):
- `RunningSum(field)` — adds `running_sum`, `running_count`, `running_avg`
- `RunningAverage(field, windowSize)` — adds `moving_avg`, `window_size`
- `ExponentialMovingAverage(field, alpha)` — adds `ema`
- `RunningMinMax(field)` — adds `running_min`, `running_max`, `running_range`
- `RunningCount(field)` — frequency tracking

**Windowing** (chunk records into slices):
- `CountWindow[T](size)` — non-overlapping fixed-size chunks
- `SlidingCountWindow[T](windowSize, stepSize)` — overlapping chunks
- `TimeWindow[T](duration, timeField)` — time-based chunks
- `SlidingTimeWindow[T](windowDuration, slideDuration, timeField)` — overlapping time chunks

### Limitations of existing primitives

1. **No PARTITION BY** — running operations work globally, not per-partition
2. **Hardcoded field names** — `RunningSum` always writes `running_sum`, not user-chosen names
3. **No LAG/LEAD** — can't access previous/next rows
4. **No ranking** — no ROW_NUMBER, RANK, DENSE_RANK
5. **No frame specs** — RunningAverage has a window size, but no `ROWS BETWEEN` flexibility
6. **Chunk windows (CountWindow etc.) change the output type** — they return `[]T` instead of enriched `T`

## SQL Window Function Taxonomy

### 1. Ranking functions

Assign a rank/number to each row based on ORDER BY.

| Function | Ties | Gaps | Example output |
|----------|------|------|----------------|
| `ROW_NUMBER()` | broken arbitrarily | no | 1, 2, 3, 4, 5 |
| `RANK()` | same rank | yes | 1, 2, 2, 4, 5 |
| `DENSE_RANK()` | same rank | no | 1, 2, 2, 3, 4 |
| `NTILE(n)` | buckets | no | 1, 1, 2, 2, 3 |

### 2. Offset/value functions

Access values from other rows relative to the current row.

| Function | Description |
|----------|-------------|
| `LAG(x, n)` | Value n rows before current |
| `LEAD(x, n)` | Value n rows after current |
| `FIRST_VALUE(x)` | First value in frame |
| `LAST_VALUE(x)` | Last value in frame |
| `NTH_VALUE(x, n)` | Nth value in frame |

### 3. Aggregate window functions

Standard aggregates (SUM, AVG, COUNT, MIN, MAX) applied over a frame without collapsing rows.

```sql
SUM(salary) OVER (PARTITION BY dept ORDER BY hire_date
                  ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
```

### 4. Distribution functions

| Function | Description |
|----------|-------------|
| `PERCENT_RANK()` | Relative rank as 0..1 |
| `CUME_DIST()` | Cumulative distribution |

## Design

### Approach: `window` CLI command

A single `window` command that accepts multiple window function specs, analogous to how `group-by` accepts multiple aggregation specs. Each spec adds one new field to every output row.

```bash
# Row numbering
ssql from data.csv | ssql window -row-number rn -order salary -desc

# Row number within partition
ssql from data.csv | ssql window -row-number rn -partition dept -order salary -desc

# Lag
ssql from data.csv | ssql window -lag price 1 prev_price -order date

# Running total
ssql from data.csv | ssql window -sum amount running_total -order date

# Moving average (3-row window)
ssql from data.csv | ssql window -avg price ma3 -order date -preceding 2 -following 0

# Multiple window functions in one pass
ssql from data.csv | ssql window \
  -row-number rn \
  -rank salary_rank \
  -lag salary 1 prev_salary \
  -sum salary running_total \
  -partition dept -order salary -desc
```

### CLI Flag Design

**Partitioning and ordering (global to all specs):**

```
-partition FIELD [FIELD ...]   Partition rows (like SQL PARTITION BY)
-order FIELD                   Order within partition (like SQL ORDER BY)
-desc                          Descending order (default ascending)
```

**Ranking functions (each adds one field):**

```
-row-number RESULT             ROW_NUMBER() → result field
-rank RESULT                   RANK() → result field
-dense-rank RESULT             DENSE_RANK() → result field
-ntile N RESULT                NTILE(n) → result field
-percent-rank RESULT           PERCENT_RANK() → result field
```

**Offset functions:**

```
-lag FIELD N RESULT            LAG(field, n) → result field
-lead FIELD N RESULT           LEAD(field, n) → result field
-first FIELD RESULT            FIRST_VALUE(field) → result field
-last FIELD RESULT             LAST_VALUE(field) → result field
```

**Aggregate window functions:**

```
-sum FIELD RESULT              Running SUM(field) → result field
-avg FIELD RESULT              Running AVG(field) → result field
-count RESULT                  Running COUNT(*) → result field
-min FIELD RESULT              Running MIN(field) → result field
-max FIELD RESULT              Running MAX(field) → result field
```

**Frame specification:**

```
-preceding N                   N rows before current row (-1 = unbounded)
-following N                   N rows after current row (-1 = unbounded)
                               Default: -preceding -1 -following 0 (UNBOUNDED PRECEDING to CURRENT ROW)
```

### Examples

**1. Running total by department:**

```bash
ssql from sales.csv | ssql window -sum revenue running_total -partition dept -order date
```

Input:
```
dept,date,revenue
eng,2026-01,100
eng,2026-02,200
eng,2026-03,150
sales,2026-01,80
sales,2026-02,120
```

Output:
```jsonl
{"dept":"eng","date":"2026-01","revenue":100,"running_total":100}
{"dept":"eng","date":"2026-02","revenue":200,"running_total":300}
{"dept":"eng","date":"2026-03","revenue":150,"running_total":450}
{"sales":"eng","date":"2026-01","revenue":80,"running_total":80}
{"sales":"eng","date":"2026-02","revenue":120,"running_total":200}
```

**2. Month-over-month change:**

```bash
ssql from monthly.csv | ssql window -lag revenue 1 prev_revenue -order month \
  | ssql update -set-expr change 'revenue - prev_revenue'
```

**3. Top 3 per department:**

```bash
ssql from employees.csv | ssql window -row-number rn -partition dept -order salary -desc \
  | ssql where -if rn le 3 \
  | ssql exclude rn
```

**4. 7-day moving average:**

```bash
ssql from prices.csv | ssql window -avg price ma7 -order date -preceding 6 -following 0
```

**5. Rank with comparison to group average:**

```bash
ssql from employees.csv | ssql window \
  -rank salary_rank \
  -avg salary dept_avg \
  -partition dept -order salary -desc
```

**6. Cumulative percentage (rollup + window):**

```bash
ssql from sales.csv | ssql group-by product -sum revenue total -rollup \
  | ssql window -sum product_total cum_total -order product_total -desc \
  | ssql update -set-expr pct 'cum_total / total * 100'
```

### Library API

```go
// WindowSpec defines a single window function computation.
type WindowSpec struct {
    Function   WindowFunc    // The window function to apply
    ResultName string        // Output field name
}

// WindowConfig defines the partitioning, ordering, and window functions.
type WindowConfig struct {
    PartitionBy []string      // PARTITION BY fields (empty = whole input)
    OrderBy     string        // ORDER BY field
    Desc        bool          // Descending order
    Frame       WindowFrame   // Frame specification
    Specs       []WindowSpec  // Window functions to compute
}

// WindowFrame specifies the frame boundaries.
type WindowFrame struct {
    Preceding int  // Rows before current (-1 = UNBOUNDED)
    Following int  // Rows after current (-1 = UNBOUNDED)
}

// Window applies window functions to a record stream.
func Window(config WindowConfig) Filter[Record, Record]
```

**Window function constructors:**

```go
// Ranking
func WRowNumber() WindowFunc
func WRank() WindowFunc
func WDenseRank() WindowFunc
func WNtile(n int) WindowFunc
func WPercentRank() WindowFunc

// Offset
func WLag(field string, offset int) WindowFunc
func WLead(field string, offset int) WindowFunc
func WFirst(field string) WindowFunc
func WLast(field string) WindowFunc

// Aggregate
func WSum(field string) WindowFunc
func WAvg(field string) WindowFunc
func WCount() WindowFunc
func WMin(field string) WindowFunc
func WMax(field string) WindowFunc
```

### Algorithm

1. **Materialize all input records** (required for PARTITION BY + ORDER BY + frame access)
2. **Partition**: Group records by partition fields (or one big partition if none)
3. **Sort** each partition by order field
4. **For each row in each partition**, compute all window functions:
   - Ranking functions: iterate through sorted partition, track ties
   - Offset functions: index into the sorted partition (lag = i-n, lead = i+n)
   - Aggregate functions: iterate over the frame [max(0, i-preceding) .. min(len-1, i+following)]
5. **Emit** the original record + all computed fields, preserving the input order

**Key detail**: Records must be emitted in their **original input order**, not the window's sort order. This means we need to track original positions and restore them after processing.

Actually — SQL window functions typically emit rows in the ORDER BY order of the window. But since our window command is a pipeline step, users can pipe to `sort` afterward if they want a different order. We should emit in partition-then-order order (which is the natural processing order).

Wait, reconsider. In SQL, `SELECT *, ROW_NUMBER() OVER (ORDER BY salary) FROM t` returns rows in **unspecified order** unless the outer query has its own ORDER BY. The window ORDER BY only affects the window computation, not the output order.

For ssql pipelines, the most useful behavior is: **preserve input order** when no -order is specified, **emit in partition+order order** when -order is specified. This matches pipeline expectations — adding `-order salary` to a window command implicitly sorts the output.

### Streaming vs. Materialization

Some window functions can work in streaming mode:
- `RunningSum` without PARTITION BY (already exists)
- `LAG(x, 1)` only needs a 1-element buffer
- `ROW_NUMBER()` without PARTITION BY is just a counter

But most require materialization:
- PARTITION BY needs all records to group them
- RANK needs all values to detect ties
- LEAD needs future rows
- Frame specs like `ROWS BETWEEN 2 PRECEDING AND 2 FOLLOWING` need lookahead

**Decision**: Always materialize. Simplicity wins. The existing streaming operations (`RunningSum` etc.) remain available in the library for truly infinite streams.

### Relationship to Existing Operations

| Existing | Window equivalent | Migration |
|----------|------------------|-----------|
| `RunningSum(field)` | `window -sum field running_sum -order <something>` | Keep library function for streaming; CLI uses Window |
| `RunningAverage(field, n)` | `window -avg field ma -order <something> -preceding n-1 -following 0` | Same |
| `RunningMinMax(field)` | `window -min field rmin -max field rmax -order <something>` | Same |

The library's streaming functions serve a different purpose (infinite streams). The `window` command targets batch analytics where you need SQL-style window semantics with PARTITION BY, ORDER BY, and frame specs.

### Code Generation

```go
// Generated code for: ssql window -row-number rn -partition dept -order salary -desc
windowed := ssql.Window(ssql.WindowConfig{
    PartitionBy: []string{"dept"},
    OrderBy:     "salary",
    Desc:        true,
    Specs: []ssql.WindowSpec{
        {Function: ssql.WRowNumber(), ResultName: "rn"},
    },
})(records)
```

### Implementation Order

1. **Library: `Window()` in operations.go or sql.go** — core function with ranking + offset + aggregate support
2. **Library tests** — each function type, partitioning, frames, edge cases
3. **CLI: `window` command** — `cmd/ssql/commands/window.go`
4. **Code generation** — `-generate` support
5. **Generation tests**

### Estimated Scope

- Library function + helpers: ~300 lines
- Tests: ~200 lines
- CLI command: ~250 lines (flag parsing, validation, schema)
- Code generation: ~80 lines
- **Total: ~830 lines**

## Open Questions

1. **Multiple ORDER BY fields?** SQL supports `ORDER BY a, b`. Do we need this initially, or is single-field ordering enough for v1?

2. **Named windows?** SQL allows `WINDOW w AS (PARTITION BY dept ORDER BY salary)` so you can reuse it. For CLI this is implicit — all specs share the same partition/order. But what about different specs needing different orderings? We could use `+` clause separators:
   ```bash
   ssql window -row-number rn -order salary + -lag date 1 prev_date -order date
   ```

3. **RANGE vs ROWS frames?** RANGE uses value-based boundaries (e.g., "all rows within 10 of current value"). This is more complex. Start with ROWS only?

4. **Default field names?** When the user doesn't specify a result name for `-lag price 1`, should we auto-name it `price_lag_1`? This reduces typing for common cases.

5. **Interaction with `-rollup`?** The rollup feature we just added enriches rows with parent-level aggregations. Window functions enrich rows with neighbor/partition context. These compose well:
   ```bash
   # Rollup for subtotals, then window for ranking within each level
   ssql from data.csv | ssql group-by dept -sum salary total -rollup \
     | ssql window -rank total_rank -order dept_total -desc
   ```

## Comparison with Other Tools

| Tool | Window functions? | Notes |
|------|------------------|-------|
| **DuckDB** | Full SQL support | `SELECT *, ROW_NUMBER() OVER (...) FROM ...` |
| **pandas** | `.rolling()`, `.shift()`, `.rank()` | Separate methods, not unified syntax |
| **dplyr** | `mutate()` + `lag()`, `lead()`, `row_number()` | Integrated into tidyverse verbs |
| **awk** | Manual | Track state in variables |
| **jq** | Very limited | Array operations only |
| **Miller** | Some | `decimate`, `step` for streaming |
| **ssql (proposed)** | `window` command | Unified CLI + library + code generation |

The proposed design is closest to dplyr's approach: a single command that adds computed columns. The key advantage over SQL is composability — pipe `window` output to `where`, `sort`, `update`, etc.
