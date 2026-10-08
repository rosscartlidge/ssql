# ssql and DuckDB, Feature by Feature: Where Things Stand (September 2026)

Reference: DFC136
Created: 2026-09-23
Last modified: 2026-10-08

[Back to Index](./README.md)

Status: **assessment, no decisions; updated 2026-09-28 for v4.108.0;
addendum §7a on non-scalar values 2026-10-08 (see DFC144); §7b
re-measures that row against v4.113.0, which shipped DFC144 Levels 0–2
the same day.**
Ross, 2026-09-23: "a good time to do a feature compare with DuckDB. What
does it look like now?" The previous comparison is
[DFC060](./duckdb-vs-ssql.md) (March 2026, with a measured appendix from
2026-09-15). Six months of work sit between them: typed parallel
codegen, `generate sql` with three dialects, the strictness programme
(DFC133), pipelines as data (DFC134), field references (DFC135),
consistent column order (v4.107.0). Written against v4.107.0 and DuckDB
1.5.0; the first draft's §4 ranked the gaps, [DFC137](./dfc137_spill_asof_set_ops.md)
proposed three of them, and all three shipped in v4.108.0 four days
later (§8 records what moved). The matrix below is the v4.108.0 state.
DFC060's philosophy section still stands and is not repeated.

## 1. The one-paragraph answer

DuckDB is a complete analytical database: every SQL feature an analyst
expects, a mature optimiser, extensions, bindings in every language, and
an installed base. ssql is a pipeline tool with a smaller surface
that has four properties DuckDB does not have and, by design, cannot
easily acquire: a compiled program per pipeline that beats DuckDB on the
workloads it covers (0.23 s to 0.91 s on the README cube); a pipeline
form that is safe to construct from untrusted data without a query
builder; five interchangeable executions of the same semantics that
are checked against each other and against DuckDB itself; and a
simpler mental model: a pipeline reads in the order it runs, each stage
sees only what the previous one produced, and any prefix can be cut and
inspected, so a pipeline grows by appending a stage, completion and help
know the data at the cursor's position, and the line a person builds by
Tab is the same document a program builds by JSON. On breadth
ssql is narrower: the gaps are ecosystem (connectors, extensions,
bindings), engine (automatic spilling, vectorisation) and the calendar
functions; the relational list DFC137 named is closed (set operations,
anti/semi-join, ASOF join, out-of-core barriers on request), and on the
core analyst workload the two columns match. On those four axes it is
ahead; and the two are complementary, which `generate sql` makes
literal.

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
| Lazy, bounded-memory streaming | ◐ (pipelined, vectorised; a query is over a finite input) | ● | ssql: except barriers (sort, group-by), which spill on request (`-spill`, DFC137 §1) |

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
| Joins | ● (all, incl. ASOF, lateral) | ◐ | inner, left, right, full, ASOF (`join -asof`, DFC137 §2, built 2026-09-27); anti/semi via `except`/`intersect -using`; no general non-equi or lateral |
| Pivot / unpivot | ● | ● | `pivot -func` with one aggregate per call; `unpivot` |
| Set operations | ● | ● | `union`, `except`, `intersect` (ALL forms; keyed forms are the anti/semi-join; DFC137 §3, built 2026-09-26) |
| Subqueries, CTEs | ● | ○ | ssql's answer is pipes and process substitution |
| Recursive queries | ● | ○ | |
| Time-series resampling with fill | ◐ (`time_bucket`; `generate_series` + ASOF join gives previous-fill) | ● | `resample` to a grid with previous/next/linear fill in one command |
| Fill missing (carry down, default) | ◐ (window tricks) | ● | `fill -down`, `-default` |
| Describe / profile | ● (`SUMMARIZE`) | ● | `describe` |
| Signal processing (FFT, convolution, correlation, STFT) | ○ | ● | GPU-accelerated build for the heavy ones |
| Nested / list / struct types | ● | ◐ | v4.113.0 (§7b, [DFC144](./dfc144_non_scalar_values.md) Levels 0–2): one `json` type every stage honours; expressions see lists and maps (`len(tags)` is 2, `tags[0]`, `addr.city`, `"go" in tags`, results stored as `json`); `explode` / `flatten`; dotted paths wherever a field name goes, completable; `-collect` round-trips. No typed nested tier (DuckDB's LIST/STRUCT/MAP/UNION with lambdas and recursive UNNEST) — [DFC145](./dfc145_nested_tables.md) records the design and "not now". Was ○ in §7a (2026-10-08 morning), ◐ in September on weaker grounds |
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
| Out-of-core (spill to disk) | ● automatic | ◐ opt-in | `sort`/`group-by -spill DIR -memory SIZE` (DFC137 §1, built 2026-09-27); `top`, `distinct`, `window` stay in memory |
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

As of v4.108.0. The 2026-09-23 list had six items; three of them
(joins, set operations, out-of-core) were DFC137 and are done, so this
is the list after it. The original ranking is kept in §8.

1. **Date and time functions.** One `time` type and a handful of
   functions against DuckDB's full calendar. `strftime`-style
   formatting, `date_trunc` beyond `bucket`, timezone conversion,
   intervals as values. Each is small; together they are a gap people
   feel daily. Now the largest remaining one.
2. **Non-scalar values** (added 2026-10-08, §7a; narrowed the same
   day, §7b). The first-question failures are gone in v4.113.0:
   `len(tags)` counts elements, `addr.city` is a field name, `explode`
   is UNNEST. What DuckDB still has and ssql does not is the **typed**
   nested tier — LIST/STRUCT/MAP/UNION as column types with a function
   family, lambdas and recursive UNNEST — and the `nest` inverse of
   `flatten`. [DFC144](./dfc144_non_scalar_values.md) has the survey
   and the "Built" notes; [DFC145](./dfc145_nested_tables.md) the
   design for the typed tier and the decision not to build it yet. The
   remaining gap is one of depth, met by fewer people than the
   date/time one.
3. **Joins beyond equality and ASOF.** General non-equi joins (`ON a.x <
   b.y`) and LATERAL. Rare in pipeline work; no plan.
4. **Automatic spilling.** ssql spills when asked (`-spill DIR`), DuckDB
   decides for itself. DFC137 §1.4 chose opt-in because a wrong estimate
   either spills needlessly or dies; a `-memory` budget that fails early
   rather than spills is the next step if anyone wants it. `top`,
   `distinct` and `window` stay in memory.
5. **Correlated subqueries.** Not expressible in a pipeline and never
   will be, which is a deliberate limit.
6. **Bindings.** Go only. DFC132 decided against Rust; Python is the one
   that would change adoption, and `ssql run` plus `generate json` make
   a thin binding possible without a second implementation.
7. **Ecosystem.** Extensions, database connectors, an installed base.
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
4. **A simpler mental model, and the construction that follows from
   it.** A pipeline reads in the order it runs and each stage sees only
   the previous stage's output; SQL's written order is not its
   evaluation order (SELECT first, evaluated near last), and what a pipe
   says by adjacency SQL says by nesting, which is what CTEs and
   subqueries are for. So the state at any point is a prefix you can cut
   and look at (`| ssql to table`), a pipeline is built by appending a
   stage, not by editing a statement; each stage is one small grammar; Tab completes commands, flags, field names and values
   with knowledge of the pipe so far; `Alt-h` explains the word under
   the cursor; `-spec-json` lets a UI render the same grammar. DuckDB's
   shell completes keywords and table names, and SQL is edited in place.
   This is the same property as (2) seen from the keyboard: the argv a
   person builds interactively is the document a program builds.
5. **Streams, SSH, shards, live data, signal processing, charts, an
   explorer, a served console.** The Unix-tool half of the design.
6. **Deterministic where SQL leaves it open.** `sort` is stable; an ASOF
   tie takes the last right row in input order; `union`, `except` and
   `intersect` match columns by name, not position. Each is a place
   where DuckDB's answer is "any of these", and where the five-lane gate
   needs one answer to compare against.

## 6. Suggested next units, from this comparison

The 2026-09-23 list was: ASOF join, date/time functions, spilling sort
and group-by, INTERSECT/EXCEPT, a Python binding. Three of the five are
DFC137 and shipped in v4.108.0. What remains, in the order I would take
them:

1. Date/time function set (§4.1): formatting, truncation, timezone,
   interval values; each with transpiler and SQL translation and a
   differential entry. The one gap on the list that an analyst meets
   daily.
2. ~~Non-scalar values, Levels 0–2 of
   [DFC144](./dfc144_non_scalar_values.md)~~ — shipped in v4.113.0 the
   day it was added (§7b). What is left of the unit: `nest FIELD COL…`
   (flatten's inverse, DuckDB `struct_pack`), dotted paths and
   `flatten` in `generate sql`, `window -collect` and a typed
   `-collect`; the typed nested tier itself is
   [DFC145](./dfc145_nested_tables.md), deferred.
3. A Python binding over `ssql run` documents (§4.6), after DFC134's
   JSON Schema (§5.5), so the binding is generated, not written.
4. A faster run codec for `-spill` (DFC137 §1a: gob over `[]any` is
   ~4 µs per record each way and is the whole 1.7× over the in-memory
   sort); typed spill; a `-memory` that fails early instead of spilling.
   Polish, not gaps.

## 7. What moved between the 23rd and v4.108.0

The first draft of this DFC was written on 2026-09-23 against
v4.107.0. DFC137 was written the same day from §4, agreed on the 26th,
and built on the 26th and 27th. Rows that changed:

| Row | 23 Sept | v4.108.0 |
|---|:-:|:-:|
| Set operations | ◐ `union` only | ● `union`, `except`, `intersect`, ALL forms; keyed = anti/semi-join |
| Joins | ◐ equi only | ◐ equi + ASOF (`join -asof`); non-equi and LATERAL remain |
| Out-of-core | ○ | ◐ opt-in `-spill DIR -memory SIZE` on `sort` and `group-by` |
| Lazy streaming | ● except barriers | ● barriers spill on request |

And what the three units' gates found in the code around them, all
fixed in the same release: `RecordKey` keyed by schema order since
v4.107.0 (so `union`/`distinct` missed duplicates across reordered
headers); `generate sql` matched `union` columns by position, dropped
every stage but `from`/`where` inside a `<(…)>` side, ignored `join
-type`, rendered `-on` with undefined aliases, and lacked reserved
words like `at`; typed `join` kept one right row per key and ignored
`-type`; a join field collision was refused by exec alone; `join
FILE.json` read an array file as lines; the optimiser pushed
predicates into ASOF and outer joins' sources. None of these were in
the 23rd's matrix because none were visible without the discriminating
fixtures the new units brought. The lesson for the next comparison:
the matrix counts features; the gates measure whether the lanes agree
on them, and a ● in one lane is not a ●.

## 7a. Addendum, 2026-10-08: non-scalar values were under-weighted here

Ross, 2026-10-08: "I don't think we talked enough about the lack of
non-scalar values in our DFC136 survey." He is right. §2.2 gave the
row a ◐ on the strength of `-collect` and the expression language's
list functions, and §4 did not rank it at all. Measured on v4.112.0
for [DFC144](./dfc144_non_scalar_values.md), the real position is:

- **Nested JSON passes through the pipeline unchanged, as text.** The
  wire reader captures an array or object as a `JSONString`, so after
  one pipe hop every nested value is opaque text whatever it was
  before. `to table` and `to csv` show the JSON; that is the whole of
  the support.
- **Nothing can look inside it.** `len(tags)` on `["go","rust"]` is
  13, `tags[0]` is 91 (the byte `[`), `addr.city` and `"go" in tags`
  are errors. The expression language's thirty list and map functions
  work only on values built inside the expression (`split`,
  `fromJSON(string(x))`), and a non-scalar result is stored as
  `fmt.Sprintf("%v")` text.
- **`-collect` is a list for one stage.** It writes wire type `json`,
  the next stage reads it as text, and `len(names)` is a character
  count. It has no typed form and no `window` form.
- **Three in-memory representations exist**, chosen by the reader
  (`JSONString`; `[]any` plus nested `Record` from `from json` on an
  array file, with random key order; `[]any` plus `map` in the legacy
  library reader), and the lanes disagree: generated code holds
  `JSONString`, DuckDB reads the same file into native STRUCT/LIST, and
  `len` means characters in one and elements in the other.
- **The `_schema` type `json` exists but is not honoured**: it flips to
  `string` after a hop, `cast` and `ParseFieldType` reject it, typed
  codegen refuses it, `SSQL_MODE=schema` says `any`.
- Six concrete bugs sit under this (DFC144 §1.5): `to json`
  double-encodes nested values, `Record.Equal` and group-by would panic
  on an in-memory `[]any`, `isValueType` disagrees with the `Value`
  constraint.

Against DuckDB this is not a ◐. DuckDB has a **typed tier** — LIST
(1-based, sliceable), STRUCT (fixed keys, dot access, `s.*`), MAP,
UNION, a complete `list_*` family with lambdas, `UNNEST` with
recursion — and an **untyped tier**, the JSON type with `->`/`->>` and
JSONPath, and `read_json` infers the first and falls back to the second
per column. ssql has the fallback tier's storage without its
operations.

What this changes in this document: the matrix row is ○ (§2.2), the
gap is ranked second in §4 (after date and time functions, which more
people meet, and ahead of everything else), and §6 gains the unit.
What it does not change: the one-paragraph answer (§1) and the
"where ssql is ahead" list (§5) stand; this is a gap in the data model,
not in the pipeline idea, and DFC144's Level 0–2 close it without
touching the scalar fast path. The design discussion — one
representation (`JSONString`), `json` as the schema type every stage
honours, expressions that parse referenced fields lazily,
`explode`/`flatten`, dotted paths as field names completable from
`SSQL_MODE=schema`, and the typed nested tier deferred — is in DFC144
§§4–7; its seven decisions are §8 there. §7b, written after the build
the same day, re-measures the row.

## 7b. Addendum, 2026-10-08 (evening): Levels 0–2 shipped in v4.113.0

Ross: "could you update the DuckDB comparison DFC now that we have done
the 0–2 work from DFC144." The probe table of DFC144 §1.1, re-run on
the installed v4.113.0 against a JSON array file with `tags` a list
and `addr` an object, in every lane that applies:

| Probe | v4.112.0 (§7a) | v4.113.0 | DuckDB |
|---|---|---|---|
| `len(tags)` on `["go","rust"]` | 13 (characters) | 2 | 2 |
| `tags[0]` | 91 (the byte `[`) | `go` | `tags[1]` is `go` |
| `addr.city` in an expression | error | `NYC` | `NYC` |
| `"go" in tags` | error | true | `list_contains` |
| `where -if addr.city eq NYC`, `include id addr.city`, `sort`/`group-by` on a path | unknown field | works; a literal `a.b` column wins | `struct_extract` |
| `explode tags` → `group-by tags -count` | no command | 3 rows, counts | `UNNEST` |
| `flatten addr` | no command | `addr.city`, `addr.zip` columns | `addr.*` |
| `-collect` then `len()` next stage | characters | elements (round-trips as `json`) | `list()` |
| `SSQL_MODE=schema` on a JSON file | names only, `any` | `addr: json`, `tags: json`, `addr.city: string`, `addr.zip: string` | `DESCRIBE` gives STRUCT/LIST |
| In-memory representations | three | one (`JSONString`) | one typed |
| `to json` of a nested value | double-encoded | emitted as JSON | — |

Every lane agrees on these: exec, record and typed Go, the library
form, and DuckDB through `generate sql` (which now emits
`struct_extract`, `list_extract` with the 0→1 shift, `list_contains`,
`list_sort`/`list_distinct`/`flatten`/`array_to_string` and `unnest`).
The `TestPipelineEquivalence` cases named in DFC144 §8 "Built" pin
them. Writing this addendum found one more lane disagreement:
`generate sql` seeded a JSON **array** file's columns by reading it as
JSON Lines, knew none, and emitted `* REPLACE (… AS city)` for a new
path column — fixed by sharing the array-or-lines detection
(`openJSONSource`) between schema mode and the translator, with an
equivalence case watched to fail first. One more instance of "a bug
fixed in one path is still live in the others".

**What the row's ◐ means now.** ssql has the whole of DuckDB's untyped
tier and more than DuckDB's JSON type offers at the command line:
paths are field names everywhere a field name goes and Ctrl-O completes
them; `explode` and `flatten` are one word each; the scalar fast path
is untouched (a `JSONString` is parsed only when an expression names
it). What ssql does not have is DuckDB's **typed** tier:

- LIST/STRUCT/MAP/UNION as column types, so a nested field's shape is
  known to the schema and to typed codegen. ssql types every nested
  value `json`; a typed program holds the text in a `string` and opens
  it per row.
- The `list_*` family with lambdas (`list_transform`, `list_filter`,
  `list_reduce`). ssql's expression language has `map`/`filter`/`sum`
  closures over a parsed list inside one expression, which covers the
  row-local cases, but nothing composes across stages the way a
  `list<struct>` column does.
- Recursive UNNEST and `struct_pack` (the inverse of `flatten`; ssql
  spells it `update -set-expr addr '{city: city, zip: zip}' | exclude
  city zip` until a `nest` command exists).
- In `generate sql`: `flatten` (keys are data) and `explode -keep-empty`
  (needs LATERAL) are refused by name; Postgres and DataFusion refuse
  nested values entirely (the PG prologue copies CSV text).
- `window -collect` and a typed `-collect` remain record-only.

The honest ranking in §4 moves this gap from second to a tie for third:
the daily failures are closed; what remains is depth that API-export
and log work rarely needs, and [DFC145](./dfc145_nested_tables.md) has
the design (the one structured value is a relation; commands scope into
it with `-in FIELD`; a `table{…}` type tier) and the reasons to wait.

## 8. References

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


- [DFC137](./dfc137_spill_asof_set_ops.md) — the three units this
  comparison led to, each with a "built" subsection.
- [DFC144](./dfc144_non_scalar_values.md) — non-scalar values: the
  survey behind §7a, DuckDB's two tiers, four levels of support, the
  decisions and the "Built" notes behind §7b. Supersedes
  [DFC052](./compound-types-investigation.md).
- [DFC145](./dfc145_nested_tables.md) — nested tables: the typed tier
  as a relation-valued field, with the nested relational research; the
  "not now" that keeps §7b's row at ◐.
