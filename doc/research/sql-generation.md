# Research: SQL Generation from ssql Pipelines

**Status:** Exploratory
**Date:** 2026-03-16

## The Idea

ssql already generates Go code from CLI pipelines (`SSQLGO=1 ... | ssql generate go`). The same fragment-based architecture could generate SQL instead: `ssql generate sql`.

```bash
ssql from sales.csv \
  | ssql where -if region eq us-east -if revenue gt 1000 \
  | ssql group-by product -sum revenue total -count cnt \
  | ssql sort -desc total \
  | ssql limit 10 \
  | ssql to table

# Could generate:
SELECT product, SUM(revenue) AS total, COUNT(*) AS cnt
FROM 'sales.csv'
WHERE region = 'us-east' AND revenue > 1000
GROUP BY product
ORDER BY total DESC
LIMIT 10;
```

## Why This Is Interesting

1. **DuckDB compatibility**: DuckDB reads CSV/JSON/Parquet files directly with `FROM 'file.csv'` syntax — no import step needed. A generated query runs immediately.
2. **Performance**: DuckDB's columnar engine is orders of magnitude faster than row-by-row pipeline processing for large datasets. An ssql pipeline over 100M rows might take minutes; the equivalent DuckDB query takes seconds.
3. **Portability**: SQL is universal. Generated queries work with PostgreSQL, SQLite, MySQL (with minor dialect adjustments).
4. **Explanation**: SQL is a more readable representation of the pipeline for people who know SQL. Useful for documentation, review, or handing off to a DBA.
5. **Validation**: Generating SQL from the same pipeline and comparing results against the ssql execution is a powerful correctness check.

## Pipeline-to-SQL Mapping

### Direct Mappings (Straightforward)

| ssql command | SQL clause | Notes |
|---|---|---|
| `from data.csv` | `FROM 'data.csv'` | DuckDB reads files directly; others need a table name |
| `where -if field op value` | `WHERE field op value` | Operator syntax differs slightly |
| `group-by field -sum f total -count cnt` | `SELECT field, SUM(f) AS total, COUNT(*) AS cnt ... GROUP BY field` | Aggregations become SELECT expressions |
| `sort field` / `sort -desc field` | `ORDER BY field` / `ORDER BY field DESC` | Direct mapping |
| `limit N` | `LIMIT N` | Direct |
| `offset N` | `OFFSET N` | Direct |
| `distinct` | `SELECT DISTINCT ...` | Direct |
| `join file -using field` | `JOIN 'file' USING (field)` | DuckDB reads files in joins too |
| `join file -on left right` | `JOIN 'file' ON a.left = b.right` | Need table aliases |
| `top N -by field` | `ORDER BY field DESC LIMIT N` | Decomposed into ORDER BY + LIMIT |
| `include f1 f2` | `SELECT f1, f2 ...` | Field projection |
| `exclude f1` | `SELECT * EXCLUDE (f1)` | DuckDB-specific; others need explicit column list |

### Window Functions (Good Mapping)

ssql's window functions were designed with SQL semantics, so they map cleanly:

```bash
ssql window -partition dept -sum salary running_total -rank rank_col
```
```sql
SELECT *,
  SUM(salary) OVER (PARTITION BY dept) AS running_total,
  RANK() OVER (PARTITION BY dept) AS rank_col
FROM ...
```

With frames:
```bash
ssql window -partition dept -order date -frame 3 0 -sum revenue rolling_3day
```
```sql
SELECT *,
  SUM(revenue) OVER (
    PARTITION BY dept ORDER BY date
    ROWS BETWEEN 3 PRECEDING AND CURRENT ROW
  ) AS rolling_3day
FROM ...
```

### Operator Translation

| ssql operator | SQL operator |
|---|---|
| `eq` | `=` |
| `ne` | `!=` or `<>` |
| `gt` | `>` |
| `ge` | `>=` |
| `lt` | `<` |
| `le` | `<=` |
| `contains` | `LIKE '%value%'` or `STRPOS(field, value) > 0` |
| `startswith` | `LIKE 'value%'` |
| `endswith` | `LIKE '%value'` |
| `regex` | `REGEXP` (DuckDB/MySQL) or `~` (PostgreSQL) |

### Challenging Mappings

| ssql feature | SQL difficulty | Approach |
|---|---|---|
| `where -if-expr 'price * qty > 1000'` | Easy | Pass through as-is — SQL has its own expression language |
| `update -set field value` | Medium | `SELECT *, value AS field` (computed column) |
| `union -file <(ssql from ...)` | Medium | `UNION` or `UNION ALL` with subqueries |
| `pivot -row r -col c -val v -func sum` | Medium | `PIVOT` (DuckDB) or `CASE WHEN` (standard SQL) |
| `rename -as old new` | Easy | `SELECT old AS new` |
| `cast -type field int` | Easy | `CAST(field AS INTEGER)` |
| Multi-clause OR (`+` separator) | Medium | `WHERE (a AND b) OR (c AND d)` |

### No Direct SQL Equivalent

| ssql feature | Why it's hard | Possible approach |
|---|---|---|
| `from ssh` / `from catalog` | Distributed sources | Out of scope for SQL generation — these are ssql-native features |
| `spectrogram` / `fft` / `convolve` | Signal processing | No SQL equivalent; skip or use UDFs |
| `to chart` / `to explore` / `to animate` | Visualization | Not translatable; skip |
| `-stream-expr` aggregations | Streaming state machines | Some map to standard aggregations; complex ones don't |
| `merge -by field` | k-way merge | `UNION ALL ... ORDER BY field` (different semantics for ties) |

## Architecture Options

### Option A: SQL Fragment System (Parallel to Go Fragments)

Add a `generate sql` command that works like `generate go`:

```bash
export SSQLSQL=1  # or SSQL_TARGET=sql
ssql from data.csv | ssql where -if age gt 25 | ssql generate sql
```

Each command emits SQL fragments instead of Go code fragments. The `generate sql` assembler combines them into a complete query.

**Fragment types:**
```json
{"type": "from", "table": "data.csv", "alias": "t1"}
{"type": "where", "conditions": ["age > 25"]}
{"type": "select", "columns": ["product", "SUM(revenue) AS total"]}
{"type": "group_by", "fields": ["product"]}
{"type": "order_by", "fields": [{"field": "total", "desc": true}]}
{"type": "limit", "n": 10}
```

The assembler restructures fragments into proper SQL clause ordering (SELECT → FROM → WHERE → GROUP BY → ORDER BY → LIMIT).

**Pros:** Reuses the fragment pipeline architecture. Each command independently contributes its SQL clause.
**Cons:** SQL is a single statement, not a pipeline — fragments need to be merged, not chained. Multi-step pipelines (where → group-by → where) need subqueries or CTEs.

### Option B: AST Builder

Instead of text fragments, build an SQL AST during fragment collection:

```go
type SQLQuery struct {
    CTEs       []CTE
    Select     []SelectExpr
    From       FromClause
    Joins      []JoinClause
    Where      []Condition
    GroupBy    []string
    Having     []Condition
    Window     []WindowDef
    OrderBy    []OrderExpr
    Limit      *int
    Offset     *int
}
```

Each command adds to the AST. The `generate sql` command renders the AST as SQL text, handling dialect differences.

**Pros:** Clean separation of structure and rendering. Easy to add dialect support. Can optimize (e.g., push WHERE conditions before GROUP BY).
**Cons:** More code. AST needs to handle CTEs for multi-step pipelines.

### Option C: Direct Translation (Simplest)

Don't use the fragment system at all. Instead, `generate sql` reads the original pipeline commands (from the comment block or from a saved pipeline spec) and translates them directly.

```bash
ssql translate-sql "from data.csv | where -if age gt 25 | group-by dept -count cnt | limit 10"
```

**Pros:** Simplest implementation. No changes to existing commands.
**Cons:** Doesn't leverage the existing fragment pipeline. Can't handle process substitution or complex pipelines.

### Recommendation: Option A with CTE Support

Option A is the most natural fit — it extends the existing fragment architecture. For multi-step pipelines that don't map to a single SELECT (e.g., where after group-by), use CTEs:

```sql
WITH grouped AS (
  SELECT dept, COUNT(*) AS cnt
  FROM 'data.csv'
  WHERE age > 25
  GROUP BY dept
)
SELECT * FROM grouped
WHERE cnt > 5
ORDER BY cnt DESC;
```

The assembler detects when a new WHERE follows a GROUP BY and wraps the preceding query in a CTE.

## SQL Dialects

Target DuckDB as the primary dialect (it can read files directly), with PostgreSQL as secondary:

| Feature | DuckDB | PostgreSQL | SQLite |
|---|---|---|---|
| Read CSV directly | `FROM 'file.csv'` | No (need COPY) | No |
| Read JSON | `FROM 'file.json'` | No | No |
| Read Parquet/Arrow | `FROM 'file.parquet'` | No | No |
| EXCLUDE columns | Yes | No | No |
| PIVOT | Yes | No (crosstab) | No |
| REGEXP | Yes | `~` operator | No |
| Window frames | Full support | Full support | Limited |
| LIMIT/OFFSET | Yes | Yes | Yes |

DuckDB is the natural target because:
1. It reads the same files ssql reads — no import step
2. Its SQL dialect is the most feature-rich
3. It's embeddable (could be invoked directly from ssql)
4. It handles CSV/JSON/Parquet/Arrow with automatic schema inference

## Implementation Sketch

### Phase 1: Core Pipeline (SELECT/FROM/WHERE/GROUP BY/ORDER BY/LIMIT)

Covers 80% of common pipelines. Each command adds its SQL clause:

```go
type SQLFragment struct {
    Type    string   `json:"type"`    // "from", "where", "select", "group_by", "order_by", "limit", "offset"
    Clause  string   `json:"clause"`  // The SQL text for this clause
    Command string   `json:"command"` // Original ssql command
}
```

The `generate sql` assembler collects fragments and builds the query.

### Phase 2: JOINs and UNIONs

Add JOIN and UNION fragment types. Handle file-based joins (`JOIN 'file.csv'`).

### Phase 3: Window Functions

Map ssql window specs to SQL WINDOW clauses.

### Phase 4: CTEs for Multi-step Pipelines

Detect pipeline patterns that need subqueries (where-after-group-by, group-by-after-group-by) and wrap in CTEs.

### Phase 5: Dialect Rendering

Abstract the SQL renderer to support multiple dialects. Start with DuckDB, add PostgreSQL.

## Open Questions

1. **Should it be `generate sql` or `to sql`?** The `to` prefix implies output format, but this isn't data output — it's query generation. `generate sql` parallels `generate go`.

2. **Should we embed DuckDB?** If ssql could run DuckDB queries directly, the `generate sql` output becomes immediately executable. DuckDB has a Go driver. But this adds a large dependency.

3. **Parameterization?** The Go codegen parameterizes values as flags. SQL could use `$1`, `$2` parameters (prepared statement style) or just emit literal values.

5. **Expressions?** For SQL generation, `-if-expr` expressions can be passed through as-is — most expr-lang syntax is valid SQL. For the few that aren't (e.g., `has()`, `contains()`), emit a warning or map to SQL equivalents (`IS NOT NULL`, `LIKE`). No need for a full translator.

## Example: Full Pipeline Translation

```bash
ssql from sales.csv \
  | ssql where -if date ge 2025-01-01 -if region eq us-east \
  | ssql group-by product -sum revenue total -avg price avg_price -count cnt \
  | ssql sort -desc total \
  | ssql limit 10 \
  | ssql to table
```

### DuckDB Output
```sql
SELECT product,
       SUM(revenue) AS total,
       AVG(price) AS avg_price,
       COUNT(*) AS cnt
FROM 'sales.csv'
WHERE date >= '2025-01-01'
  AND region = 'us-east'
GROUP BY product
ORDER BY total DESC
LIMIT 10;
```

### With Window Functions
```bash
ssql from sales.csv \
  | ssql window -partition region -order date -sum revenue running_total -rank rank \
  | ssql where -if rank le 3 \
  | ssql to table
```
```sql
WITH windowed AS (
  SELECT *,
    SUM(revenue) OVER (PARTITION BY region ORDER BY date) AS running_total,
    RANK() OVER (PARTITION BY region ORDER BY date) AS rank
  FROM 'sales.csv'
)
SELECT * FROM windowed
WHERE rank <= 3;
```

### With JOIN
```bash
ssql from orders.csv \
  | ssql join customers.csv -using customer_id \
  | ssql group-by region -sum amount total \
  | ssql to table
```
```sql
SELECT region, SUM(amount) AS total
FROM 'orders.csv'
JOIN 'customers.csv' USING (customer_id)
GROUP BY region;
```
