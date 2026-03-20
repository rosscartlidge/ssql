# ssql Future Development

## What ssql Is

ssql is a Unix-pipeline data processing toolkit: a Go library, a CLI, a code generator, and a visualization engine. Its identity is composable pipeline steps — not SQL, not a database, not a notebook. Everything flows left to right through pipes.

The question isn't "how do we become DuckDB?" — it's "what does ssql need to be the best pipeline-oriented analytics tool?"

## Where ssql Stands (v4.19.0, February 2026)

### What works well

**Data pipeline core:**
- 28 CLI commands covering read, filter, transform, aggregate, join, output
- Expression language (`expr-lang`) for computed fields and complex predicates
- Code generation: prototype in CLI, compile to Go for 10-100x speedup
- JSONL schema headers for pipeline field tracking
- Rollup/cube enrichment on group-by (v4.19.0)
- Pivot cross-tabulation (v4.18.0)

**Visualization:**
- Interactive Chart.js output: line, bar, scatter, pie, radar, heatmap, spectrogram
- Zoom, pan, statistical overlays, PNG/CSV export
- Animated time-series playback
- WASM-powered explorer with in-browser pipeline builder (245KB TinyGo binary)
- AG-Grid integration for tabular exploration

**Specialized:**
- Signal processing: FFT, iFFT, convolution, spectrogram
- GPU acceleration for compute-heavy operations (FFT 21-100x, convolution 320x)
- Apache Arrow I/O for high-performance columnar data
- WAV audio file support
- Tab completion with field names and data values from files

### What's missing

The gaps fall into three categories: analytical operations that users expect, ergonomic improvements that reduce friction, and scalability for larger workloads.

---

## Comparison with DuckDB

DuckDB is the closest comparable tool — a single-binary analytical engine that works on local files. Understanding the overlap and differentiation clarifies where ssql should invest.

### Capability Matrix

| Capability | ssql (v4.19) | DuckDB | Notes |
|-----------|-------------|--------|-------|
| **Read CSV/JSON/Parquet/Arrow** | CSV, JSON, JSONL, Arrow | CSV, JSON, Parquet, Arrow, SQLite, PostgreSQL, S3, HTTP | DuckDB has broader format and remote source support |
| **Filter rows** | `where -if field op value` | `WHERE field op value` | Equivalent |
| **Computed columns** | `update -set-expr field 'expr'` | `SELECT *, expr AS field` | Equivalent |
| **Group-by aggregation** | `group-by fields -count/-sum/-avg` | `GROUP BY ... COUNT/SUM/AVG` | Equivalent |
| **Rollup/cube** | `group-by -rollup/-cube` (enriched rows) | `GROUP BY ROLLUP/CUBE` (extra NULL-sentinel rows) | Different output model; ssql's is more pipeline-friendly |
| **Pivot** | `pivot -row x -col y -val z -func sum` | `PIVOT ... ON ... USING ...` | Equivalent |
| **Window functions** | Not yet | Full SQL window support | **Gap** — ssql's biggest analytical limitation |
| **Joins** | `join file -using field` / `-on left right` | Any SQL JOIN variant | ssql supports lookup joins; DuckDB has full relational joins |
| **Subqueries/CTEs** | Not applicable (use pipeline composition) | Full support | Different paradigm — pipelines vs. nested queries |
| **Sorting** | `sort -by field` (single field) | `ORDER BY a, b DESC, c ASC` | **Gap** — ssql needs multi-field ordering |
| **Distinct** | `distinct` / `distinct -by field` | `SELECT DISTINCT` | Equivalent |
| **Set operations** | `union` (with optional `-distinct`) | UNION, INTERSECT, EXCEPT | ssql has union only |
| **Visualization** | Built-in Chart.js: 7 chart types, interactive | None | **ssql advantage** |
| **Code generation** | CLI → standalone Go program | None | **ssql advantage** |
| **Signal processing** | FFT, iFFT, convolution, spectrogram | None (needs UDFs) | **ssql advantage** |
| **GPU acceleration** | CUDA for FFT/convolution | None | **ssql advantage** |
| **WASM explorer** | In-browser pipeline builder | WASM builds for embedding | **ssql advantage** |
| **Tab completion** | Field names + data values | None | **ssql advantage** |
| **Memory model** | Streaming where possible; materializes for group-by/sort/window | Vectorized columnar with spill-to-disk | DuckDB handles larger-than-memory |
| **Binary size** | ~8MB | ~20MB | Both are single-binary, zero-dependency |
| **Language bindings** | Go (native), CLI | Python, R, Java, Node.js, Rust, Go, CLI | DuckDB has broader language support |

### Where DuckDB is definitively better

1. **Complex analytical SQL** — Multi-table joins with subqueries, CTEs, recursive queries. ssql's pipeline model handles linear flows well but can't express graph-shaped query plans.

2. **Larger-than-memory processing** — DuckDB's vectorized engine with disk spill handles datasets that don't fit in RAM. ssql materializes group-by, sort, window, pivot, rollup into memory.

3. **Format breadth** — Parquet, SQLite, PostgreSQL wire protocol, S3 remote reads, HTTP range requests. ssql covers CSV/JSON/Arrow/WAV.

4. **Query optimization** — DuckDB has a cost-based optimizer that reorders joins, pushes predicates, chooses hash vs. sort joins. ssql executes steps in the order written.

### Where ssql is definitively better

1. **Visualization** — DuckDB has no output beyond tabular text. ssql generates interactive HTML charts with zoom, pan, overlays, animation, and a full in-browser explorer. The pipeline-to-chart workflow is zero-friction: `ssql from data.csv | ssql to chart output.html`.

2. **Code generation** — No analytical tool in this space offers "prototype interactively, generate compiled code." ssql's `SSQLGO=1` pipeline → Go program workflow is unique. The generated code runs 10-100x faster than CLI execution and can be deployed without ssql installed.

3. **Signal processing** — FFT, inverse FFT, convolution, spectrogram are first-class operations. Combined with WAV I/O and GPU acceleration (320x for convolution), ssql serves a niche that DuckDB doesn't touch.

4. **Unix pipeline integration** — ssql commands are true stdin→stdout filters that compose with grep, awk, jq, and each other. DuckDB can read stdin but it's a query engine, not a pipeline participant. You don't pipe DuckDB into DuckDB.

5. **Shell ergonomics** — Tab completion of field names and data values, pipeline field tracking via schema headers, contextual help. DuckDB's CLI is a SQL prompt with no data-aware completion.

### The philosophical difference

**DuckDB**: "Your files are a database. Write SQL."
- Strength: expressive, optimized, handles complex queries
- Weakness: SQL is write-heavy for simple transformations; no visualization; no code generation

**ssql**: "Your data flows through pipes. Each step does one thing."
- Strength: composable, visual, generates production code, integrates with Unix tools
- Weakness: can't express complex multi-table query plans; materializes for many operations

They're complementary. A realistic workflow might use DuckDB for complex joins and ssql for visualization and code generation. But the goal is to make ssql self-sufficient for the 80% of analytical tasks that follow a linear pipeline pattern.

---

## Development Priorities

### Priority 1: Window Functions (closes the biggest gap)

**Status:** Design complete — see `window-functions-design.md`

This is the single highest-impact feature. Without it, common analytical tasks require leaving ssql entirely:
- Running totals and cumulative sums
- Rankings within groups (top-N per partition)
- Period-over-period comparisons (this month vs. last month)
- Moving averages
- Percentile rankings

The proposed design adds a `window` command with SQL-equivalent semantics:

```bash
# Running total by department
ssql from sales.csv | ssql window -sum revenue running_total -partition dept -order date

# Top 3 per department
ssql from employees.csv | ssql window -row-number rn -partition dept -order salary -desc \
  | ssql where -if rn le 3

# Month-over-month change
ssql from monthly.csv | ssql window -lag revenue 1 prev_revenue -order month \
  | ssql update -set-expr change 'revenue - prev_revenue'

# 7-day moving average
ssql from prices.csv | ssql window -avg price ma7 -order date -preceding 6 -following 0
```

**Implementation scope:** ~830 lines (library function + tests + CLI command + code generation)

**Open design questions:**
- Multi-field ORDER BY — needed for correct tie-breaking in rankings
- RANGE vs. ROWS frames — start with ROWS only, add RANGE later if needed
- Default result field names — auto-naming like `price_lag_1` reduces typing

### Priority 2: Multi-field Ordering

Several commands accept only a single sort field:
- `sort -by field` — can't sort by (dept ASC, salary DESC)
- `window -order field` — can't break ties in rankings

SQL's `ORDER BY a, b DESC` is expected behavior. This should be addressed alongside window functions since rankings need it for correctness.

Proposed syntax:
```bash
# Multiple sort fields (ascending by default)
ssql from data.csv | ssql sort -by dept salary

# Mixed directions
ssql from data.csv | ssql sort -by dept -asc salary -desc

# Window with multi-field order
ssql from data.csv | ssql window -row-number rn -partition dept -order hire_date -asc salary -desc
```

### Priority 3: Expression Ergonomics

The expression language (`expr-lang`) is powerful but only accessible via specific flags (`-if-expr`, `-set-expr`). Extending expression support to more commands eliminates intermediate pipeline steps.

**Today (requires temporary fields):**
```bash
ssql from sales.csv \
  | ssql update -set-expr line_total 'price * quantity' \
  | ssql group-by dept -sum line_total dept_revenue \
  | ssql exclude line_total
```

**With expression arguments:**
```bash
ssql from sales.csv | ssql group-by dept -sum-expr 'price * quantity' dept_revenue
```

**Targets for expression support:**
- `group-by -sum-expr`, `-avg-expr`, `-min-expr`, `-max-expr` — aggregate computed values
- `sort -by-expr 'len(name)'` — sort by derived value
- `group-by -by-expr 'year(date)'` — group by derived value
- `window -sum-expr 'price * qty' running_rev` — window over computed values

This builds on the existing `custom-aggregation-expressions.md` design.

### Priority 4: Missing Utility Commands

Small commands that come up repeatedly in real pipelines:

**`tail` (last N rows):**
```bash
# Last 10 rows of sorted output
ssql from logs.csv | ssql sort -by timestamp | ssql tail 10
```
Currently requires sorting in reverse and using `limit`, which is semantically wrong for unsorted data. Implementation: ring buffer of size N.

**`sample` (random sampling):**
```bash
# Random 100 rows for exploration
ssql from big.csv | ssql sample 100

# 10% sample
ssql from big.csv | ssql sample -pct 10
```
Essential for exploring large datasets before building pipelines. Implementation: reservoir sampling for count mode, probability filter for percentage mode.

**`melt` / `unpivot` (wide to long):**
```bash
# Pivot's inverse
ssql from wide.csv | ssql melt -id name -value-vars jan feb mar -var-name month -value-name revenue
```
Pivot exists (v4.18.0); its complement should too. Common for reshaping data before visualization.

**`transpose`:**
```bash
# Flip rows and columns
ssql from summary.csv | ssql transpose
```
Useful for small summary tables where the natural representation has fields as rows.

### Priority 5: Larger-than-Memory Awareness

ssql materializes data for: `group-by`, `sort`, `distinct`, `window` (proposed), `rollup`/`cube`, `pivot`. For datasets approaching available RAM, this means silent OOM kills.

**Short term (low effort):**
- Track memory usage during materialization
- Emit a clear error with row count and estimated memory when approaching limits
- Suggest `sample` or `limit` as workarounds

**Medium term:**
- External sort (merge-sort with temp files) for `sort` command
- Disk-backed hash tables for `group-by` and `distinct`
- Configurable memory limit (`SSQL_MAX_MEMORY` or `-max-memory` flag)

**Long term:**
- Spill-to-disk for all materializing operations
- Memory-mapped working sets

This isn't about competing with DuckDB's memory management. It's about not failing silently when data gets large.

### Priority 6: Documentation Consolidation

The documentation is thorough but fragmented across 10+ files. A user discovering ssql has no single path through the material.

**Proposed: `doc/tutorial.md` — End-to-end walkthrough:**
1. Install ssql
2. Explore a CSV file (`from` → `to table`)
3. Filter and transform (`where`, `update`, `sort`)
4. Aggregate (`group-by`, rollup)
5. Join with lookup data (`join`)
6. Window functions (when available)
7. Visualize (`to chart`, `to explore`)
8. Generate production Go code (`SSQLGO=1` → `generate go`)

This tells the ssql story: explore → analyze → visualize → ship.

---

## Features ssql Should NOT Build

Knowing what not to build is as important as knowing what to build.

**Full SQL parser / query planner:**
The pipeline model is ssql's identity. Adding SQL syntax would create a second, inferior interface to the same operations. Users who want SQL should use DuckDB. ssql's value is that pipelines read left-to-right and compose with Unix tools.

**Client-server mode / persistent storage:**
ssql processes files and streams. Adding a server, connections, or persistent tables turns it into a database — which it isn't and shouldn't be.

**Plugin / extension system:**
Premature abstraction. The Go library already serves as the extension point — import `ssql/v4` and compose with `Filter[T,U]`. A plugin system adds complexity without clear benefit at this scale.

**More chart types (beyond current 7):**
The visualization suite is already strong. Polish and usability improvements (better defaults, responsive layout, accessibility) matter more than adding box plots or treemaps. If a chart type is needed, it should be motivated by a concrete use case, not completeness.

**Notebook interface:**
ssql's strength is that it works in the terminal and generates files. A notebook would compete with Jupyter (which has DuckDB integration). The WASM explorer already fills the "interactive exploration" niche.

---

## Implementation Sequence

Based on the priorities above and the existing `implementation-priorities.md` roadmap:

| Phase | Feature | Effort | Builds on |
|-------|---------|--------|-----------|
| **A** | Window functions + multi-field ordering | 2-3 weeks | Design complete |
| **B** | Expression ergonomics for aggregation | 1 week | Existing expr-lang, design complete |
| **C** | Utility commands (sample, tail, melt) | 1 week | Independent |
| **D** | Memory awareness / limits | 1 week | Independent |
| **E** | Documentation tutorial | 1 week | After window functions land |
| **F** | Typed code generation | 2-4 weeks | Schema headers |
| **G** | Spill-to-disk for materializing ops | 2-3 weeks | After memory awareness |

Phases A-E bring ssql to analytical self-sufficiency for pipeline workloads. Phases F-G are performance and scalability improvements.

---

## Success Criteria

ssql is "done enough" when a user can:

1. Load any common file format (CSV, JSON, Arrow)
2. Explore interactively with tab completion and the WASM explorer
3. Filter, transform, join, aggregate, rank, and compare across time periods — all in a pipeline
4. Visualize results immediately with one command
5. Generate a compiled Go program from the working pipeline
6. Handle datasets up to available RAM without silent failures

Window functions (Priority 1) and utility commands (Priority 4) are the remaining gaps for criterion 3. Memory awareness (Priority 5) addresses criterion 6. The rest is already in place.
