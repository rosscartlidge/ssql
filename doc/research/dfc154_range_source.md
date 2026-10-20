# A Row Generator: `from range`, Numbers Every Lane Can Make

Reference: DFC154
Created: 2026-10-20
Last modified: 2026-10-21

[Back to Index](./README.md)

Status: **proposal, 2026-10-20.** Ross: "does duckdb have any way of
generating table data in a programmatic way — I am thinking it might
be nice to be able to create tables for testing — if we can produce
line numbers that could be enough as we have a function on the line
number to create fields."

## 1. The question

Every ssql pipeline starts from data that exists: a file, a pipe, a
catalog, a remote host. A test, a demo, a codelab or a benchmark that
wants ten thousand rows of a known shape has to make them somewhere
else first — the test suite has two hand-written generators (the
DFC113 scale fixture, the random differential corpus) and the codelab
fixtures are static CSVs. Ross's observation is that the hard part is
already done: `update -set-expr` can derive any field from a number,
so a source that produces the numbers is enough.

DuckDB has it: `range(n)`, `range(start, stop, step)` and
`generate_series(start, stop, step)` are table functions — `SELECT i,
i * i FROM range(1, 4) t(i)` yields rows from no table — which is
exactly what the SQL lane needs. The question is what the source
looks like in ssql, and what it is in the other lanes.

## 2. Where ssql stands

`from lines` is the nearest thing: one record per line of input with
`line_number` (1-based) and `line`. A shell sequence makes it a row
generator today:

```
seq 1000 | ssql from lines | ssql update -set-expr sq 'line_number * line_number' -set-expr k 'md5(string(line_number))'
```

That runs in exec and in the Go lanes (they read stdin too), but not
in `generate sql`, which cannot read a pipe, and it carries a `line`
column nobody asked for and depends on `seq`. The numeric functions
of DFC153 and the hashes are what turn a number into a column: `%`
for cycling categories, `sin` and `log` for shaped series, `string()`
and `md5()` for names and stable keys.

## 3. The source

```
ssql from range N                      # n = 1 … N
ssql from range N -start 0             # n = 0 … N-1
ssql from range N -step 5              # n = 1, 6, 11, …  (N rows)
ssql from range N -field i             # the column's name (default n)
```

- **N is the row count**, not the stop value: a generator is asked
  for N rows, and `-start`/`-step` say what they hold. (DuckDB's
  `range(start, stop, step)` is a half-open interval; the translation
  computes `stop` from N. A count is what a test wants — "a thousand
  rows" — and never off by one.)
- `n` is an **int** column; `-start` and `-step` are ints. A float or
  time series is `-set-expr` on it (`n * 0.5`, `date_add(since, n,
  "day")`), which is where every other shape lives too.
- No `-seed`, no randomness in the source. Determinism is the point of
  a fixture; noise is an expression on `n`: the hashes today
  (`md5(string(n))`), a seeded `hash(n)` integer function later if a
  numeric spread is wanted (DuckDB has `hash(i)`; Go has `maphash` —
  the two must agree, so that is its own small piece, §6).

The name: `range` after DuckDB's table function; `seq` would read as
the Unix tool, which it is meant to replace. One word, completes as
a `from` format.

## 4. Each lane

| Lane | Form |
|---|---|
| exec | a counting `iter.Seq[Record]`, schema `{n: int}` |
| record codegen | `ssql.RangeRecords(start, step, count, field)` in the root package — the same function exec calls, so there is one implementation |
| typed codegen | `typed.Range(start, step, count)` → `iter.Seq[typed.N]` with `struct { N int64 }`, and `typed.RangeParallel(…, shards)` → `Stream[typed.N]` by splitting the count across shards (the parallel form, with the serial alternative as every source declares: `AltCodeIfSeq`) |
| `generate sql` | `FROM range(start, start + count*step, step) t("n")` on DuckDB and DataFusion (`range` exists in both); Postgres `generate_series(start, start + (count-1)*step, step) AS t("n")` (inclusive) |
| `-records` | `count` — the protocol answer costs nothing |
| schema mode | `n` typed int, so Ctrl-O downstream offers it |
| `-field` other than `n` | renames the column everywhere; typed's struct field is the Go name of it |

Parallel typed: `ParallelFromSlice` is the in-memory precedent
(`typed/stream.go:161`); a range is better than a slice — each shard
counts its own interval and no slice is built — so `RangeParallel`
is a shard-aware generator, the first of its kind in `typed`, and a
pattern for any future synthetic source.

The fragment carries `Op{Kind: "from", Argv: ["range", "1000", …]}`
like any source; `generate ssql` renders it as typed; the optimiser's
source rules (column pruning, catalog filters) do not apply and skip
it by kind.

## 5. What it is for

- **Tests**: a fixture is a pipeline, so it runs in every lane and
  the equivalence gate pins it. The scale gate's cached 200 MB CSV
  could be `from range 3000000 | update …` piped to `to csv` once.
- **Codelabs and demos**: `ssql from range 10 | ssql update -set-expr
  sq 'n * n' | ssql to table` needs no file, which is the first
  pipeline a reader can type anywhere.
- **Benchmarks**: `from range N` is a source with no I/O, so a stage's
  own cost is what is measured.
- **Calendar grids**: `from range 365 -start 0 | update -set-expr day
  'date_add(date("2026-01-01"), n, "day")'` is a date spine to join
  against, the thing `resample` builds internally.

## 6. Not in this DFC

- A seeded numeric `hash(n)` / `rand(n)` function: wanted for
  fixtures with spread, but it needs one hash every lane computes
  identically (DuckDB's `hash` is its own; Go's `maphash` is seeded
  per process). A small follow-up: pick FNV-1a or xxhash, implement in
  `exprfn`, render in SQL as the same arithmetic or refuse by design.
- Cross products (`from range 10 | join <(ssql from range 10)`) already
  work through the join; no `-dims` flag.
- Strings, times, floats in the source: expressions on `n`.

## 7. Plan and size

Half a day: `ssql.RangeRecords` and `typed.Range`/`RangeParallel`
(with a `typed.N` struct); `from range` in `commands/from_range.go`
with the exec, record and typed fragments, `-records`, schema mode;
the SQL case beside `lines`; an equivalence case per shape (`range
basic`, `-start -step`, `-field`, a cross product through join, a
date spine) with Goldens by hand; the catalog of sources in
`doc/cli-codelab.md` and `doc/README.md`; `TestFieldCompletionConfiguration`.

## 8. Questions for Ross — answered 2026-10-21

Plan mode, all three as recommended:

1. **N is a count** (`from range 1000` = a thousand rows; `-start` and
   `-step` say what they hold). The SQL translation computes the stop
   value: `start + N*step` for the half-open `range()` of DuckDB and
   DataFusion, `start + (N-1)*step` for Postgres's inclusive
   `generate_series` (with `WHERE false` when N is 0, since a negative
   step would otherwise put the end past the start and yield rows).
2. **Default start 1**, as `from lines` and `seq`; `-start 0` is
   DuckDB's convention.
3. **The hash follow-up is queued** as its own TODO item, not built.

## 9. Built 2026-10-21

As §4, with one change: the typed runtime is **generic over the row**
— `typed.Range[T](start, step, count, mk func(int64) T)` and
`RangeParallel[T](…, n, mk)` — not a fixed `typed.N`. The typed readers
and writers take a column's name from the struct tag, so a fixed struct
would have written `n` under `-field i`; the generated program renders
a one-field `RangeRow` through `lib.RenderStructDefWithComment` and
passes `func(v int64) RangeRow { return RangeRow{I: v} }`. One runtime,
every column name.

Pieces: `ssql.RangeRecords` (root `io.go`, exec and record share it);
`typed/range.go` (`RangeParallel` splits the count across shards the
way `ParallelFromSlice` splits a slice, each shard counting its own
interval); `commands/from_range.go` (`rangeOpts` + `validate()` is the
one grammar check — exec, codegen and the SQL translator's
`parseRangeArgs` all go through it; `-records` prints the count; schema
mode writes `n:int` through `writeSchemaModeOutputTyped`; `count`,
`start`, `step` are flags of the generated program, so the binary is a
reusable generator); `translateRangeSource` in `generate_sql.go`;
`getFromFileAndFormat` returns `("", "range")` so no `generate ssql`
source rule mistakes the generator for a file.

Gates: `typed/range_test.go` (sharding agrees with serial for every
shape, early stop, race); `TestParseRangeArgs`, `TestRangeSourceSQL`
(three dialects); corpus `range_source_every_mode`; equivalence
`range_identity`, `range_start_step_field`, `range_expr_shapes`,
`range_zero_rows`, `range_date_spine` — the first cases with no
fixture file, Goldens by hand, green in exec, record, typed, parallel,
ssql-opt and DuckDB (the library lane skips: not a file source).
