# ssql and DuckDB, Feature by Feature: Where Things Stand (September 2026)

Reference: DFC136
Created: 2026-09-23
Last modified: 2026-09-23

[Back to Index](./README.md)

Status: **assessment, no decisions.** Ross, 2026-09-23: "a good time to
do a feature compare with DuckDB. What does it look like now?" The
previous comparison is [DFC060](./duckdb-vs-ssql.md) (March 2026, with a
measured appendix from 2026-09-15). Six months of work sit between them:
typed parallel codegen, `generate sql` with three dialects, the
strictness programme (DFC133), pipelines as data (DFC134), field
references (DFC135), consistent column order (v4.107.0). This DFC is the
state as of ssql v4.107.0 against DuckDB 1.5.0, written to be honest in
both directions. DFC060's philosophy section still stands and is not
repeated.

## 1. The one-paragraph answer

DuckDB is a complete analytical database: every SQL feature an analyst
expects, a mature optimiser, extensions, bindings in every language, and
an installed base. ssql is a pipeline tool with a much smaller surface
that has three properties DuckDB does not have and, by design, cannot
easily acquire: a compiled program per pipeline that beats DuckDB on the
workloads it covers (0.23 s to 0.91 s on the README cube), a pipeline
form that is safe to construct from untrusted data without a query
builder, and five interchangeable executions of the same semantics that
are checked against each other and against DuckDB itself. On breadth
ssql is far behind; on those three axes it is ahead; and the two are
complementary, which `generate sql` makes literal.

## 2. Feature matrix

Legend: **●** full, **◐** partial (note says what), **○** absent.

### 2.1 Reading data

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| CSV / TSV, header, type inference | ● | ● | ssql: sampled inference, zero-padded numbers stay text, `-type` overrides; strict cells (DFC133) |
| CSV dialect sniffing (quote, delimiter) | ● | ◐ | ssql: RFC 4180, delimiter given or inferred for TSV; no sniffing of quote char |
| Malformed rows | ● loud | ● loud | both stop; ssql since v4.105.0 |
| JSON Lines, JSON array | ● | ● | ssql: `_schema` header for typed wire; strict lines with `-skip-invalid` opt-out |
| Parquet | ● | ● | ssql reads and writes Snappy; column pruning in typed codegen |
| Arrow IPC | ● | ● | |
| Excel | ● (ext) | ● | |
| Raw text lines + regex extract | ◐ (`read_text`, regexp fns) | ● | `from lines`, `extract -skip` |
| WAV audio | ○ | ● | for the signal commands |
| Remote files (HTTP, S3-style object stores) | ● (httpfs, signs S3 requests itself) | ● | `from https://` with Range, parquet column pruning over Range, `-sample` by Range draws; object stores via presigned URLs by design (DFC112: no SDK, the user's own credential flow) |
| Files over SSH, sharded catalogs, push-down | ○ | ● | `from ssh`, `from catalog`, `-- PIPELINE` push-down |
| Databases (Postgres, MySQL, SQLite) | ● (ext) | ○ | ssql has no connector; `generate sql -dialect postgres` runs the other way |
| Glob / many files as one source | ● | ● | `from csv a.csv b.csv -source file` |
| stdin as a source | ◐ | ● | ssql: every command reads stdin |
| Infinite / live streams | ○ | ● | the pipeline model; `merge` for k-way sorted streams |
| Lazy, bounded-memory streaming | ◐ (pipelined, vectorised; a query is over a finite input) | ● | ssql: except barriers (sort, group-by), which DuckDB spills and ssql holds in memory (DFC137) |

### 2.2 Transforming

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| Filter | ● | ● | `where -if`, `-if-field`, `-if-expr`, clauses with `+` (OR), `-not`, `-invert` |
| Projection, rename, exclude | ● | ● | `include` order is the output order (v4.107.0) |
| Computed columns | ● | ● | `update -set`, `-set-expr`, `-set-field`, `-set-bucket` |
| Conditional update (if/else chains) | ● (CASE) | ● | first-match-wins clauses |
| Type cast | ● | ● | strict; `-invalid missing` opt-out; `time` type |
| Sort, limit, offset, distinct | ● | ● | `limit -last` (tail) has no SQL form |
| Sampling | ● | ● | `sample N`, `-percent`; seeded form has no SQL equivalent |
| Group-by aggregates | ● (~50 fns) | ● (16) | count, sum, avg, min, max, median, percentile, stddev, variance, mode, count-distinct, string-agg, first, last, any, collect, arg-max/min |
| Expression aggregates | ● (any expression) | ● | `-expr 'max(price * qty)'`, `-stream-expr` folds; no SQL translation |
| Rollup / cube | ● | ● | ssql's enriched-detail shape, not grouping-set rows |
| Window functions | ● (all) | ● (17) | row_number, rank, dense_rank, ntile, percent_rank, cume_dist, lag/lead, first/last/nth_value, running aggregates; ROWS and RANGE frames (DFC130) |
| Joins | ● (all, incl. ASOF, lateral) | ◐ | inner, left, right, full; equi-join only; no ASOF, no anti/semi, no non-equi |
| Pivot / unpivot | ● | ● | `pivot -func` with one aggregate per call; `unpivot` |
| Set operations | ● | ◐ | `union` (all); no INTERSECT/EXCEPT |
| Subqueries, CTEs | ● | ○ | ssql's answer is pipes and process substitution |
| Recursive queries | ● | ○ | |
| Time-series resampling with fill | ◐ (`time_bucket`; `generate_series` + ASOF join gives previous-fill) | ● | `resample` to a grid with previous/next/linear fill in one command |
| Fill missing (carry down, default) | ◐ (window tricks) | ● | `fill -down`, `-default` |
| Describe / profile | ● (`SUMMARIZE`) | ● | `describe` |
| Signal processing (FFT, convolution, correlation, STFT) | ○ | ● | GPU-accelerated build for the heavy ones |
| Nested / list / struct types | ● | ◐ | `group-by -collect` builds lists; expressions have list/map functions (`map`, `filter`, `split`, `fromJSON`, `flatten`); no unnest command, and typed codegen and `generate sql` do not carry nested values |
| Full-text search, spatial, vector | ● (ext) | ○ | |

### 2.3 Expressions

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| Expression language | SQL | expr-lang | ssql: ~70 documented functions, arrays, lambdas; five lanes must agree (transpiler differential gate) |
| Regular expressions | ● | ● | Go RE2 vs DuckDB RE2: the same engine |
| Date/time functions | ● (rich) | ◐ | one `time` type, `date()`, `bucket()`, duration arithmetic; formatting and calendar functions are thin |
| String functions | ● | ● | |
| Parameters (prepared statements) | ● values only | ● | `-param NAME TYPE VALUE`, and `-param-field` for a column; no host binding API yet |
| Field references in conditions | ● | ● | `-if-field a gt b` (DFC135) |
| User-defined functions | ● (Python, macros) | ○ | ssql: generate Go and edit it |

### 2.4 Writing data

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| CSV, TSV, JSON, JSONL, Parquet, Arrow, Excel, Markdown | ● | ● | |
| Table to terminal | ● | ● | |
| Charts (HTML) | ○ | ● | `to chart`, `to animate` |
| Interactive explorer | ○ (UI extension, separate) | ● | `to explore`, `-wasm` embeds the engine in the page |
| Write to a database | ● | ○ | |
| Tee (save and continue) | ○ | ● | |

### 2.5 Execution

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| Vectorised columnar engine | ● | ○ | ssql is row-at-a-time |
| Parallelism | ● automatic | ● in typed codegen | `Stream[T]` sharding for source, filter, join, group-by; interpreter is serial |
| Query optimiser | ● cost-based | ◐ rule-based | `generate ssql`: pushdown, column pruning, dead-sort elimination, sort+limit to top, expression canonicalisation |
| Compile a query to a native program | ○ | ● | `generate go`: typed structs, no reflection, parallel; parameters become flags |
| Out-of-core (spill to disk) | ● | ○ | barriers (sort, group-by) are in memory |
| Persistent database | ● | ○ | ssql is stateless; `serve` holds one dataset in memory |
| Client/server | ○ (embedded) | ● | `serve`: SSH console and HTTP API over an in-memory dataset |
| Distributed execution | ○ | ● | catalog of shards over SSH, pipeline pushed to each |
| GPU | ○ | ◐ | FFT, convolution, correlation |
| Browser (WASM) | ● (DuckDB-Wasm) | ● | playground; the explorer embeds it |

### 2.6 Interfaces and safety

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| CLI | ● (SQL REPL) | ● (Unix commands) | |
| Library bindings | ● Python, R, Java, Node, Go, Rust, … | ● Go only | DFC132 chose not to add Rust |
| Tab completion | ◐ (keywords) | ● | commands, flags, field names and values, across the pipe |
| Help at cursor | ○ | ● | `Alt-h`, `-help-at` |
| Machine-readable grammar | ◐ (`duckdb_functions()`, `duckdb_keywords()` catalogs; no grammar) | ● | `-spec-json`; UIs render from it |
| Safe programmatic construction | ◐ prepared statements bind values only; `json_serialize_sql` / `json_execute_serialized_sql` give a JSON AST to build against | ● | a pipeline document is argv; `-arg`, `-param`, `ssql run`, `generate json` (DFC134); injection fuzz at volume |
| Read-only / policy | ● (read-only mode) | ◐ | `serve -readonly`; document policy (DFC134 §5.4) not built |
| Emit SQL for another engine | ○ | ● | `generate sql -dialect duckdb\|postgres\|datafusion` |
| Emit another engine's program | ○ | ● | generated Go; Rust harness for DataFusion documented (DFC132) |

### 2.7 Correctness and testing (as a user-visible property)

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| Absent vs null vs empty | SQL NULL | absent, null, `""` distinct (DFC124) | the rules are written down and pinned in every lane |
| Strict typing of cells | ● | ● | since DFC133 |
| Differential testing against another engine | (DuckDB tests against Postgres/SQLite) | ● | five lanes plus DuckDB, Postgres, DataFusion as oracles; random pipelines; hostile fixtures |
| Loud on invalid input | ● | ● | after DFC133/135: unknown fields, bad literals, mixed kinds |

## 3. What changed since DFC060 (March)

- **Performance claim reversed on the covered workloads.** DFC060 said
  "for aggregation over large datasets, it's not even close" in DuckDB's
  favour. The 2026-09-15 measurement has compiled ssql at 0.23 s against
  DuckDB's 0.91 s on the README cube over 14.6 M rows (Parquet), and
  1.8 s against 1.4 s on CSV. The interpreter is still slower than
  DuckDB; the compiled program is not. The caveat stands: this is one
  query shape, chosen to suit typed codegen, and DuckDB's optimiser
  covers shapes ssql cannot express at all.
- **`generate sql` exists**, with three dialects, and is the second-
  engine oracle in the equivalence gate. DFC060 listed it as design
  input; it is now the bridge that makes "complementary" concrete.
- **Window analytics** went from four functions to the standard set with
  ROWS and RANGE frames (DFC130).
- **Strictness and loudness** (DFC133, 21+ defects; DFC135 §9) closed the
  gap where DuckDB was simply more careful.
- **Pipelines as data** (DFC134) is new ground DuckDB does not cover:
  `-arg`, `-param`, `ssql run`, `generate json`, the injection fuzz.
- **Column order** is now a defined, cross-lane property (v4.107.0);
  before, only exec had it.

## 4. Where DuckDB is ahead, and whether it matters

In rough order of how often a user would hit it:

1. **Joins.** Equi-join with four types is the whole story. No ASOF join
   (the time-series join DuckDB is famous for), no anti/semi, no
   inequality joins. ASOF is the one worth building: `resample` and
   `window` show the time-series direction, and ASOF is the missing
   verb. Likely a unit; the typed hash join is the template.
2. **Date and time functions.** One `time` type and a handful of
   functions against DuckDB's full calendar. `strftime`-style
   formatting, `date_trunc` beyond `bucket`, timezone conversion,
   intervals as values. Each is small; together they are a gap people
   feel daily.
3. **Set operations and subquery shapes.** INTERSECT/EXCEPT are a day;
   correlated subqueries are not expressible in a pipeline and never
   will be, which is a deliberate limit.
4. **Out-of-core barriers.** A `sort` or `group-by` over data larger than
   memory fails in ssql and spills in DuckDB. `merge` and `-presorted`
   are the workarounds; a spilling sort is a real unit.
5. **Bindings.** Go only. DFC132 decided against Rust; Python is the one
   that would change adoption, and `ssql run` plus `generate json` make
   a thin binding possible without a second implementation.
6. **Ecosystem.** Extensions, database connectors, an installed base.
   (Remote storage is not a gap: `from https://` with Range and
   presigned URLs covers object stores, DFC112; DuckDB additionally
   signs S3 requests itself.) Not a feature gap to close so much as a fact.

Not gaps, by design: SQL as the interface, a persistent database, the
columnar engine. DFC060's argument holds: these are what DuckDB is, and
ssql is the tool you reach for when the data is a stream, the pipeline
is built by a program, or the result must be a program.

## 5. Where ssql is ahead

1. **A compiled program per pipeline** that runs without ssql, faster
   than DuckDB on its shapes, with parameters as flags. Nothing in the
   DuckDB world corresponds.
2. **Safe construction from untrusted data** without a query builder,
   with the property fuzzed rather than argued (DFC134 §6).
3. **Five executions, one semantics, checked against each other and
   against DuckDB, Postgres and DataFusion.** A user gets exec, three Go
   forms and three SQL dialects from one command line, and the project's
   tests are the reason to trust that they agree.
4. **Streams, SSH, shards, live data, signal processing, charts, an
   explorer, a served console.** The Unix-tool half of the design.
5. **Completion and help that know the data**: field names and values
   across the pipe, help at the cursor, a grammar UIs can render.

## 6. Suggested next units, from this comparison

In the order I would take them, none started:

1. ASOF join (§4.1): the largest single gap for the time-series users
   ssql already serves; typed hash join as the template; SQL lane has
   `ASOF JOIN` in DuckDB and a `LATERAL` emulation elsewhere.
2. Date/time function set (§4.2): formatting, truncation, timezone,
   interval values; each with transpiler and SQL translation and a
   differential entry.
3. Spilling sort and group-by (§4.4), with the scale gate deciding the
   ceiling.
4. INTERSECT/EXCEPT (§4.3).
5. A Python binding over `ssql run` documents (§4.5), after DFC134's
   JSON Schema (§5.5), so the binding is generated, not written.

## 7. References

- [DFC060](./duckdb-vs-ssql.md) — the March comparison and the
  2026-09-15 measurements this DFC builds on.
- [DFC128](./dfc128_json_interchange_and_time_type.md) — JSON
  interchange with DuckDB and Postgres; the time type.
- [DFC130](./dfc130_window_analytics_gaps.md) — window functions.
- [DFC132](./dfc132_rust_target_datafusion.md) — the DataFusion dialect
  and the decision not to generate Rust.
- [DFC133](./dfc133_finding_unknown_bugs.md) — strictness; the
  instruments that check the lanes against DuckDB.
- [DFC134](./dfc134_pipelines_as_data.md) — pipelines as data; why SQL
  cannot have it.
- [DFC135](./dfc135_field_references_in_value_slots.md) — field
  references in value slots.
