# ssql and DuckDB, Feature by Feature: Where Things Stand (October 2026)

Reference: DFC160
Created: 2026-10-31
Last modified: 2026-10-31
Deprecates: [DFC136](./dfc136_duckdb_feature_comparison_2026_09.md)

[Back to Index](./README.md)

Status: **assessment, no decisions.** Ross, 2026-10-31: "probably about
time to update our duckdb feature comparison DFCs. Do we still have major
gaps". [DFC136](./dfc136_duckdb_feature_comparison_2026_09.md) was written
against v4.107.0 (23 September), updated for v4.108.0, and given two
addenda on non-scalar values at v4.113.0. Twenty-one releases later
(v4.134.0, plus what is on main), its matrix, its ranking and one claim in
its first paragraph are out of date, so this is a fresh survey that
replaces it. DFC136 keeps the history (§7 there: what the DFC137 units'
gates found). [DFC060](./duckdb-vs-ssql.md) keeps the philosophy and the
March baseline. The DuckDB column is checked against DuckDB 1.5.0
(`~/.local/bin/duckdb`), the ssql column against the checkout.

## 1. The one-paragraph answer

No major gaps remain in what a pipeline user meets day to day. The two
gaps DFC136 ranked highest are closed: date and time functions (DFC147)
and non-scalar values at the untyped tier (DFC144). One gap DFC136 never
listed is closed too: exact numbers. ssql computed money in float64 and
had no integer wider than int64. It now has `int128` and `decimal:S`, and
an integer overflow is an error in every lane (DFC155). What is left is
depth (the typed nested tier, non-equi joins, intervals as a type),
ecosystem (database connectors, Python bindings, extensions) and engine
(a serial, row-at-a-time interpreter; spilling only on request). The
performance claim in DFC136 §1, "a compiled program per pipeline that
beats DuckDB on the workloads it covers", needs to be more careful:

- On the same machine, the typed Go program is faster than DuckDB 1.5 on
  four of the seven pipelines in `doc/performance.md` §1a and slower on
  three.
- `generate rust`, the same pipeline as a Rust program on DataFusion
  (DFC157), is the fastest of the three engines on six of seven.

So the accurate claim now is "comparable to DuckDB, and one command away
from a program faster than both" (§3). The four properties DFC136 put on
ssql's side still hold: compiled programs, safe construction from data,
many executions checked against each other, and a pipeline that reads in
the order it runs. There is now a fifth: two program targets, Go and
Rust, from one command line.

## 2. Feature matrix

Legend: **●** full, **◐** partial (the note says what), **○** absent.
Rows marked † changed since DFC136; §6 lists them with the release.

### 2.1 Reading data

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| CSV / TSV, header, type inference | ● | ● | sampled inference, zero-padded numbers stay text, strict cells (DFC133); `-type` overrides; † a schema sidecar (`F.csv.schema`, DFC146) declares the types; † `-empty zero` reads blank numeric cells as zero; † `-columns` projection, applied by the optimiser on its own |
| CSV dialect sniffing (quote, delimiter) | ● | ◐ | RFC 4180, delimiter given or inferred for TSV; no quote-character sniffing |
| Time formats on read | ● (`dateformat`, `timestampformat`) | ● † | `-format COL PATTERN` with strftime patterns (DFC147 §6) |
| Malformed rows | ● loud | ● loud | |
| JSON Lines, JSON array | ● | ● | † an array file keeps its key order |
| Parquet | ● | ● | † several files as one stream; † DECIMAL(p,s) is `decimal:s`, a DECIMAL(38,0) is `int128`; † the typed readers decode in batches |
| Arrow IPC | ● | ● | |
| Excel | ● (ext) | ● | |
| Raw text lines + regex extract | ◐ | ● | `from lines`, `extract`; † `to lines FIELD` |
| WAV audio | ○ | ● | |
| Remote files (HTTP, object stores) | ● | ● † | `from https://` with Range reads in every lane since v4.120.1; object stores through presigned URLs (DFC112) |
| Hive-partitioned layouts | ● (`hive_partitioning`) | ● † | `from catalog -hive GLOB` derives the key columns (v4.116.0) |
| Files over SSH, sharded catalogs, push-down | ○ | ● | † catalogs run in the typed lane and translate to SQL |
| Databases (Postgres, MySQL, SQLite) | ● (ext) | ○ | no connector; `generate sql -dialect postgres` goes the other way |
| Row generator | ● (`range`, `generate_series`) | ● † | `from range N [-start S -step K]`, a shard-aware generator in the typed lane (DFC154) |
| Glob / many files as one source | ● | ● | |
| stdin as a source | ◐ | ● | |
| Infinite / live streams | ○ | ● | |
| Lazy, bounded-memory streaming | ◐ | ● | except barriers (sort, group-by, window), of which sort and group-by spill on request |

### 2.2 Transforming

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| Filter | ● | ● | |
| Projection, rename, exclude | ● | ● | |
| Computed columns | ● | ● | † every assignment of one `update` sees the row as it entered, in every lane (the SQL lane let DuckDB resolve a select-list alias until 2026-10-31, §7) |
| Conditional update | ● (CASE) | ● | first-match-wins clauses |
| Type cast | ● | ● | † `int128`, `decimal:S`, `json` targets |
| Sort, limit, offset, distinct | ● | ● | `limit -last` has no SQL form |
| Sampling | ● | ● | |
| Group-by aggregates | ● (~50 fns) | ● (18) | count, sum, avg, min, max, median, percentile, stddev, variance, mode, count-distinct, string-agg, first, last, any, collect, arg-max, arg-min. † An integer sum is an `int128` and exact (DFC155 C) |
| Expression aggregates | ● | ● | `-expr 'max(price * qty)'`, † nested aggregates in `-expr` |
| Rollup / cube | ● | ● | enriched-detail shape, not grouping-set rows |
| Window functions | ● | ● | 16 by flag (row_number, rank, dense_rank, ntile, percent_rank, cume_dist, lag/lead with defaults, first/last/nth_value, sum, avg, min, max, count) plus † nine registry aggregates over any frame (median, percentile, stddev, variance, mode, count-distinct, string-agg, arg-max, arg-min); ROWS and RANGE frames; † every running aggregate is one pass over the partition, no longer O(n²) |
| Folds / recursive computation | ● (recursive CTE) | ◐ † | `window -stream-expr INIT EVERY FINAL NAME`, a fold over the rows in order (Fibonacci, running state machines), translated to a recursive CTE on DuckDB (DFC156). General recursion over a graph is not expressible |
| Joins | ● (all, incl. ASOF, lateral) | ◐ | inner, left, right, full, ASOF; anti/semi via `except`/`intersect -using`; no non-equi or LATERAL |
| Pivot / unpivot | ● | ● | † a pivot cell keeps its values' kind |
| Set operations | ● | ● | by name, not position |
| Subqueries, CTEs | ● | ○ | by design: pipes and process substitution |
| Time-series resampling with fill | ◐ | ● | `resample` |
| Fill missing | ◐ | ● | `fill -down`, `-default` |
| Describe / profile | ● (`SUMMARIZE`) | ● | `describe` |
| Signal processing | ○ | ● | FFT, convolution, correlation, STFT; GPU build |
| Nested / list / struct types | ● | ◐ | the untyped tier (one `json` type, paths as field names, `explode`, `flatten`, lists and maps in expressions; DFC144); no typed tier (LIST/STRUCT/MAP as column types, `list_*` lambdas across stages; DFC145, deferred) and no `nest` |
| Full-text search, spatial, vector | ● (ext) | ○ | |

### 2.3 Expressions and types

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| Expression language | SQL | expr-lang | † 119 catalogued functions (`ssql functions`): 15 string, 41 math, 26 array, 22 date, 3 map, 8 bitwise, 4 hash |
| Numeric functions | ● | ● † | DuckDB's set: trig, hyperbolic, logs, `gcd`/`lcm`, `factorial`, rounding modes, `isnan`/`isinf`; a non-finite result is absent in every lane (DFC153) |
| Exact integers past 2^63 | ● HUGEINT | ● † | `int128`: by cast, a wide literal or an integer sum (DFC155 C) |
| Fixed-point decimals | ● DECIMAL(p,s) | ● † | `decimal:S`, by declaration only; `+ -` at the larger scale, `*` at the summed scale, half-away-from-zero rescale (DFC155 E) |
| Integer overflow | error | error † | the VM, typed Go and every SQL dialect stop with the operands in the message; DataFusion's own operators wrap, so its dialect computes through DECIMAL(38,0) (§7) |
| Regular expressions | ● | ● | both RE2 |
| Date/time functions | ● | ● † | parts (`year` … `epoch`), `date_trunc`, `date_add`, `date_diff`, `tz`, `strftime`, `strptime`, a pipeline time zone in the `_schema` header (DFC147). Missing: an INTERVAL column type, `make_date`/`make_timestamp`, `last_day`, `age` |
| String functions | ● | ● | |
| Hash functions | ● | ● † | `md5`, `sha1`, `sha256`, and a seeded `hash(n)` that agrees across every lane (DFC154 §6) |
| Parameters | ● values only | ● | `-param NAME TYPE VALUE`, `-param-field`; a generated library package takes them as Go arguments (DFC143) |
| Field references in conditions | ● | ● | `-if-field a gt b` |
| User-defined functions | ● (macros, Python UDFs) | ◐ † | expression macros (`ssql macro`, `-set-macro`, DFC152) are DuckDB's scalar macros; no foreign-language UDFs (generate Go and edit it) |

### 2.4 Writing data

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| CSV, TSV, JSON, JSONL, Parquet, Arrow, Excel, Markdown | ● | ● | † `-time-format` on the text sinks; † times are native in Parquet and Arrow |
| Table to terminal | ● | ● | |
| Charts (HTML) | ○ | ● | |
| Interactive explorer | ○ | ● | `to explore -wasm` embeds the engine |
| Write to a database | ● | ○ | |
| Tee | ○ | ● | |

### 2.5 Execution

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| Vectorised columnar engine | ● | ○ | the interpreter and the typed program are row-at-a-time; `generate rust` hands the pipeline to one (DataFusion) |
| Parallelism | ● automatic | ● in typed codegen | † the typed sort and window run on every core and feed parallel consumers (DFC158); the interpreter is serial |
| Query optimiser | ● cost-based | ◐ rule-based | pushdown, column pruning (now CSV/TSV too), dead-sort elimination, sort+limit to top; † applied by `generate sql` and `run` as well |
| Compile a query to a native program | ○ | ● | `generate go` (typed, parallel); † `generate rust` |
| Out-of-core | ● automatic | ◐ opt-in | `sort`/`group-by -spill DIR -memory SIZE`; `top`, `distinct`, `window` stay in memory |
| Persistent database | ● | ○ | by design |
| Client/server | ○ | ● | `serve` |
| Distributed execution | ○ | ● | catalog shards over SSH |
| GPU | ○ | ◐ | FFT, convolution, correlation |
| Browser (WASM) | ● | ● | playground; † Generate JSON and Generate Rust buttons |

### 2.6 Interfaces and safety

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| CLI | ● (SQL REPL) | ● (Unix commands) | |
| Library bindings | ● Python, R, Java, Node, Go, Rust, … | ◐ Go only | `generate go -package` makes a pipeline a Go package with the parallel runtime (library mode, DFC143, v4.112.0); no Python |
| Tab completion | ◐ | ● | † Ctrl-O completes nested names and values |
| Help at cursor | ○ | ● | |
| Machine-readable grammar | ◐ | ● | `-spec-json` |
| Safe programmatic construction | ◐ | ● | pipeline documents (DFC134) |
| Read-only / policy | ● | ◐ | `serve -readonly`; document policy not built |
| Emit SQL for another engine | ○ | ● | DuckDB, Postgres, DataFusion dialects, each with an oracle lane run before every release (`make lanes`) |
| Emit another engine's program | ○ | ● † | `generate rust`: a Rust program on DataFusion's DataFrame API, every stage a DataFrame call, `-run`/`-build` (DFC157). DFC136's "DFC132 chose not to add Rust" is reversed |

### 2.7 Correctness and testing

| Feature | DuckDB | ssql | Note |
|---|:-:|:-:|---|
| Absent vs null vs empty | SQL NULL | distinct (DFC124) | |
| Strict typing of cells | ● | ● | |
| Differential testing | (against Postgres/SQLite) | ● | † the equivalence gate's lanes are exec, record, typed, parallel, `generate ssql`, DuckDB, Postgres, DataFusion and Rust, compared byte for byte, with integers compared as exact digits |
| Loud on invalid input | ● | ● | |

## 3. Performance, corrected

DFC136 §3 quoted the README cube at 0.23 s for compiled ssql against
0.91 s for DuckDB. That DuckDB figure does not reproduce on DuckDB 1.5.
The engine table in `doc/performance.md` §1a (`scripts/bench-engines.sh`,
same machine, best of three, whole-process wall, after v4.129.0) is the
measurement to quote:

| Pipeline (14.6 M rows; the join 10 M) | DataFusion (`generate rust`) | ssql typed Go | DuckDB 1.5.0 |
|---|---:|---:|---:|
| the README cube | **0.18 s** | 0.26 s | 0.35 s |
| `group-by -count`, Parquet | **0.02 s** | 0.14 s | 0.03 s |
| `group-by -count`, CSV | **0.24 s** | 0.32 s | 0.75 s |
| filtered streaming write to CSV | **0.68 s** | 1.46 s | 2.97 s |
| three chained joins, `count` | **0.25 s** | 0.51 s | 0.35 s |
| decimal cast + sum | **0.05 s** | 0.26 s | 0.06 s |
| window running sum + group-by | 5.8 s | **3.2 s** | 13.2 s |

Reading it honestly:

- **Typed Go is faster than DuckDB on four rows.** The cube, the CSV
  read, the streaming write and the window. These are the shapes where
  a pruned, parallel read and a per-row map with no planning cost do
  well, and the window is the result of DFC158's week of work.
- **DuckDB is faster on three.** The Parquet count (0.03 against 0.14 s),
  the joins and the decimal sum. A vectorised executor over a columnar
  file wins wherever a scan and an aggregate dominate.
- **DataFusion, through `generate rust`, is fastest on six of seven.**
  That is the real answer to "what if I need DuckDB's speed": the same
  pipeline, with `rust` in place of `go`.
- **The interpreter is not in the table.** It is serial and runs per
  record through a map, so an ad-hoc `ssql` command line over millions of
  rows is several times slower than DuckDB. Code generation is how ssql
  goes fast, and the interactive path is not built for speed.
- **Memory.** Peak RSS is usually higher for typed Go than for either
  engine: 1.2 GB against 666 MB for DuckDB on the CSV group-by, 4.3 GB
  against 2.9 GB on the window.

## 4. Where DuckDB is ahead, ranked

In rough order of how often a user would meet it:

1. **Interactive speed.** The serial interpreter (§3). Generated programs
   close the gap; a person exploring at the prompt does not generate
   programs. A parallel interpreter is the obvious lever and has never
   been designed.
2. **Database connectivity.** DuckDB attaches Postgres, MySQL and SQLite
   and writes to them; ssql reads files, URLs and SSH. `generate sql
   -dialect postgres` runs a pipeline *in* Postgres, which is not the
   same as reading a table *from* it. A `from postgres` source is the
   first gap here a user would ask for.
3. **Bindings.** Go only. Python is the one that would change adoption;
   `ssql run` documents plus `generate json` still make a thin binding
   possible without a second implementation (DFC136 §6.3, unbuilt).
4. **Automatic spilling.** Opt-in on `sort` and `group-by`; `top`,
   `distinct` and `window` hold their input. The typed window holds
   4.3 GB on 14.6 M rows (§3).
5. **The typed nested tier.** LIST/STRUCT/MAP as column types, `list_*`
   lambdas across stages, recursive UNNEST, and `nest` (the inverse of
   `flatten`). DFC145 has the design and the decision to wait. In
   `generate sql`, a dotted path in a flag position (`where -if
   addr.city eq NYC`) is a DuckDB binder error, not a struct access; the
   expression form (`-if-expr 'addr.city == "NYC"'`) works. `flatten`
   is refused by name, and Postgres and DataFusion refuse nested values
   entirely.
6. **Joins beyond equality and ASOF.** Non-equi and LATERAL. Rare in
   pipeline work; no plan.
   **Recursive joins (graph traversal)** belonged here and were missed:
   the matrix row says so, but the ranking did not. Ross ranks it the
   biggest gap (2026-10-31); [DFC161](./dfc161_graph_traversal_join_follow.md)
   explores `join -follow`, a join run to a fixpoint.
7. **Calendar depth.** An INTERVAL column type, `make_date`,
   `make_timestamp`, `last_day`, `age`. Each is small; none has been
   asked for.
8. **Ecosystem.** Extensions (spatial, full-text, vector), an installed
   base. A fact rather than a gap to close.

Not gaps, by design: SQL as the interface, correlated subqueries, a
persistent database, a columnar engine of ssql's own (`generate rust`
borrows one instead).

## 5. Where ssql is ahead

1. **Compiled programs, in two languages.** `generate go` writes a typed,
   parallel Go program that runs without ssql and beats DuckDB on four
   of the seven measured shapes. `generate rust` writes a Rust program on
   DataFusion that beats it on six. Parameters become flags in both.
2. **Safe construction from untrusted data**, fuzzed (DFC134 §6).
3. **Nine executions of one semantics, checked against each other.**
   Exec, three Go forms, `generate ssql`, three SQL dialects and the Rust
   program, compared byte for byte over the equivalence suite before
   every release. The lanes keep finding real bugs: the first DataFusion
   overflow rows found DataFusion's integer operators wrapping
   (2026-10-30), and writing this DFC found two more (§7).
4. **Exact arithmetic, the same in every lane.** An integer sum widens to
   int128 everywhere and an overflow stops everywhere. That includes
   DataFusion, whose own operators wrap and are checked through a
   DECIMAL round-trip.
5. **A simpler mental model.** A pipeline reads in the order it runs,
   each prefix can be cut off and inspected, completion knows the data at
   the cursor, and the line a person builds by Tab is the document a
   program builds (DFC136 §5.4, unchanged).
6. **Streams, SSH, shards, live data, signal processing, charts, an
   explorer, a served console.**
7. **Deterministic where SQL leaves it open.** Stable sort, ASOF ties,
   set operations by name, and an `update` whose assignments all see
   the input row.

## 6. What moved between v4.108.0 and v4.134.0

| Row | DFC136 (v4.108–v4.113) | Now | Where |
|---|:-:|:-:|---|
| Date/time functions | ◐ | ● (no INTERVAL type) | DFC147, v4.114.0 |
| Time formats on read and write | ○ | ● | DFC147 §6, v4.114.0 |
| Exact integers past 2^63 | ○ (not listed) | ● | DFC155 C, v4.125.0 |
| Fixed-point decimals | ○ (not listed) | ● | DFC155 E, v4.127.0 |
| Integer overflow | wrapped (not listed) | error in every lane | DFC155 B, v4.123.0; DataFusion v4.134.0 |
| Numeric functions | ◐ | ● | DFC153, v4.121.1 |
| Hash functions | ◐ | ● | DFC154, v4.122.0 |
| Folds / recursion | ○ | ◐ | DFC156, v4.121.3 and v4.126.0 |
| Row generator | ○ (not listed) | ● | v4.121.3 |
| User-defined functions | ○ | ◐ (macros) | DFC152, v4.121.0 |
| Hive layouts | ○ (not listed) | ● | v4.116.0 |
| Remote files in every lane | ◐ | ● | v4.120.1 |
| CSV schema sidecars | — | ● | DFC146, v4.114.0 |
| Window aggregates over a frame | 17 functions | 16 + nine registry aggregates, typed, one pass | DFC157/DFC158, v4.129–v4.134 |
| Emit another engine's program | Go + a Rust harness doc | Go + `generate rust` | DFC157, v4.128.0 |
| Performance claim | "beats DuckDB" | comparable; Rust beats both | §3 |

## 7. What writing this found

DFC136 §7's lesson was that "a ● in one lane is not a ●". Writing this
survey checked rows by running them, and found the same thing again:

- **`update` read its own new fields in the DuckDB lane.** `update -set
  t 100 -set-expr u 't + 1'` is an error by the evaluation convention
  (`ssql conventions -category evaluation`) and in exec, record and
  typed. `generate sql` put both assignments in one select list, where
  DuckDB resolves the earlier alias, and returned `u = 101`. Fixed on
  2026-10-31: every dialect refuses a read of a field the same update
  creates.
- **DataFusion wrapped integer arithmetic over a column an earlier
  `update` computed.** The checked form was emitted only for operands
  whose kind the translator could see, and it knew source columns only.
  Fixed on 2026-10-31 (`exprToSQLKind`). Join and fold scopes,
  parameters and unsampled sources remain in TODO.
- **Dotted paths in flag positions in `generate sql`** are still a
  binder error (§4.5). This was already in TODO, and the failure is loud.

## 8. Suggested next units, from this comparison

In the order I would take them:

1. **A `from postgres` source** (§4.2): read a table or a query through
   `psql`/COPY or the wire protocol, typed from the catalog. This is the
   connector gap users would meet first, and the rig (ssql-node1) is
   already there to test it.
2. **Dotted paths at flag positions in `generate sql`** (§4.5):
   `struct_extract` on DuckDB, a refusal by name elsewhere. Small, and
   it closes a ● that is a ◐ in one lane.
3. **A Python binding over `ssql run` documents** (§4.3).
4. **Spilling for `window`** (§4.4), the barrier with the largest
   resident set in §3, and a `-memory` budget that fails early.
5. **`nest`** (§4.5), then the calendar small change (§4.7) when someone
   asks.

## 9. References

- [DFC060](./duckdb-vs-ssql.md): the March comparison and the
  philosophy.
- [DFC136](./dfc136_duckdb_feature_comparison_2026_09.md): the September
  comparison this replaces; its §7 records what the DFC137 units found.
- [DFC137](./dfc137_spill_asof_set_ops.md): spill, ASOF, set operations.
- [DFC143](./dfc143_library_mode.md): library mode, a pipeline as a Go package.
- [DFC144](./dfc144_non_scalar_values.md),
  [DFC145](./dfc145_nested_tables.md): non-scalar values; the typed tier
  deferred.
- [DFC146](./dfc146_csv_schema_sidecars.md): CSV schema sidecars.
- [DFC147](./dfc147_date_time_functions.md): date and time functions.
- [DFC152](./dfc152_expression_macros.md): expression macros.
- [DFC153](./dfc153_numeric_functions.md): numeric functions.
- [DFC154](./dfc154_range_source.md): dialects, `from range`,
  `hash`.
- [DFC155](./dfc155_big_numbers.md): int128, decimals, overflow.
- [DFC156](./dfc156_fold_sql_recursive_cte.md): folds as recursive CTEs.
- [DFC157](./dfc157_generate_rust.md): `generate rust`.
- [DFC158](./dfc158_performance_measured_fixed_next.md): the performance
  work behind §3.
