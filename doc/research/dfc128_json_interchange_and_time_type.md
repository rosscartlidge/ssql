# JSON Interchange with DuckDB and Postgres, and the Time Type Question

Reference: DFC128
Created: 2026-09-14
Last modified: 2026-09-19

[Back to Index](./README.md)

Status: **all six decisions shipped 2026-09-19** — the coercion fix, D2
and D4 (§6a), D3 (§6b), D5 (§6c), D1 core with the ssql-owned `date()`
(§6d), D6 (§6e), then `resample` over time columns (§6f). Open:
null-key visibility in the JSON parser only.
Ross, 2026-09-14: "Have you actually
checked that duckdb can import json/jsonl from ssql? and what about the
reverse?" — then "I had assumed you needed to use the to/from commands
to exclude the schema", "what is the real use of the line_numbers in
ssql — is it needed?", and "whether ssql should support time fields — I
think the underlying package does". This doc records what was run, what
it found, and the choices.

The codelab (§2) has claimed since 9ecffec (2026-09-12) that `to jsonl`
is what DuckDB's `COPY … TO` writes and `read_json_auto` reads. That
claim was written from a check that never ran: the `duckdb` call took
its SQL as a *database filename* and wrote a 12 KB file called `select 1
as a, 'x' as b` into `doc/codelab-data/`, which then broke the v4.98.0
module zip (see W38 journal). Today's runs are the first real ones.

## 1. What was verified (DuckDB v1.5.0, codelab data)

### ssql → DuckDB

| ssql wrote | DuckDB read with | Result |
|---|---|---|
| `ssql from employees.csv \| ssql to jsonl > e.jsonl` | `read_json_auto('e.jsonl')` | 3 rows; `age/salary/level` BIGINT, `hire_date` **DATE** (inferred from `2018-03-15`), rest VARCHAR |
| `… \| ssql to json > e.json` (one array) | `read_json_auto('e.json')` | same rows and types |
| `… \| ssql limit 3 > bare.jsonl` (no `to`, `_schema` header in) | `read_json_auto('bare.jsonl')` | a `_schema` STRUCT column plus **one all-NULL first row**; the three real rows have `_schema` NULL |
| `update -set-expr t2 'date(ts)' \| to jsonl` | `read_json_auto` | `t2` written as `2026-01-02T10:30:00Z` → **TIMESTAMP** |

So Ross's assumption holds and §2 is right: the bare stream is for
ssql-to-ssql; `to jsonl` / `to json` strip the header for other tools.

### DuckDB → ssql

| DuckDB wrote | ssql read with | Result |
|---|---|---|
| `COPY (q) TO 'd.jsonl'` (NDJSON, the default for `.jsonl`/`.json`) | `ssql from jsonl d.jsonl` or `ssql from d.jsonl` | rows load; see caveats |
| `COPY (q) TO 'd.json' (ARRAY true)` | `ssql from d.json` | rows load |
| `duckdb -json -c "q"` on stdout | `ssql from json -` | rows load |
| NDJSON saved as `x.json` | `ssql from x.json` | auto-detected as lines; loads |
| `COPY (q) TO 'dk.parquet'` with TIMESTAMP and DATE columns | `ssql from dk.parquet` | loads; both columns become **strings** `2026-01-02T10:30:00Z` / `2026-01-02T00:00:00Z`, wire type `string` |

The query `q` had `id INT, s VARCHAR, f DOUBLE, d DATE, ts TIMESTAMP, b
BOOLEAN, n VARCHAR` with `n` NULL in the first row and `'z'` in the
second. DuckDB writes `"d":"2026-01-02"`, `"ts":"2026-01-02 10:30:00"`
(space, no zone), `"n":null`.

### Postgres — verified 2026-09-14 (PostgreSQL 16.15 on ssql-node1)

Ross: "what do you think of installing postgres so we can do some
interoperability tests?" — installed on the LXD rig node the same
afternoon (`ssh-test-environment.md` §PostgreSQL). Run over ssh with
SQL on stdin. Postgres → ssql:

| Postgres wrote | ssql read with | Result |
|---|---|---|
| `select row_to_json(t) …` through `psql -At` (NDJSON) | `ssql from jsonl pg.jsonl` | rows load; **F1 and F2 reproduce** (`n` NULL in row 1 → column gone; `_line_number` added) |
| `select json_agg(t) …` (one array) | `ssql from json -` | rows load |
| `\copy (q) to stdout csv header` | `ssql from csv -` | rows load; NULL → empty string |
| `numeric '12.50'` | | JSON `12.50` → ssql float `12.5` |

Postgres time forms, which are *not* DuckDB's: `timestamp` →
`2026-01-02T10:30:00` (ISO with `T`, **no zone**); `timestamptz` →
`2026-01-01T23:30:00+00:00` (server zone UTC); `date` → `2026-01-02`;
in CSV `2026-01-02 10:30:00` and `2026-01-01 23:30:00+00` (**two-digit
zone**). See F3 for which of these ssql parses.

ssql → Postgres:

| ssql wrote | Postgres read with | Result |
|---|---|---|
| `to csv` | `\copy emp from stdin csv header` into a typed table | 10 rows, `hire_date` a real DATE |
| `to jsonl` | `\copy raw from stdin` into a `jsonb` column, then `jsonb_populate_record(null::emp, j)` | rows and types back |
| `to json` (array) | `jsonb_populate_recordset(null::emp, $j$…$j$::jsonb)` | rows back |
| bare stream (`_schema` in) | `\copy` into `jsonb` | **one phantom row of three**: `j ? '_schema'` — the same phantom DuckDB shows |

So §2's Postgres sentence is now true as written, with one addition
worth making: Postgres has no `COPY … TO … (FORMAT JSON)`; the export is
`row_to_json` / `json_agg` through `psql -At`, and the import is `\copy`
into `jsonb` plus `jsonb_populate_record(set)`, or CSV both ways.

## 2. Findings on the way in (DuckDB → ssql)

### F1. A field that is NULL in the first record is silently lost

`n` is NULL in row 1 and `"z"` in row 2. Every sink dropped it:

```
$ ssql from jsonl d.jsonl | ssql to csv
_line_number,b,d,f,id,s,ts        ← no n
0,true,2026-01-02,2.5,1,x,2026-01-02 10:30:00
1,false,2026-02-03,3,2,y,2026-02-03 11:00:00
```

Mechanism, three pieces that are each reasonable alone:

1. `setValueFromJSON` (`cmd/ssql/lib/jsonl.go`) *skips* nil values —
   the field is absent from record 1, not present-as-null.
2. The `_schema` header for headerless input is
   `lib.InferFromRecord(first record)` — one record, so `n` is not in
   `Fields`.
3. Sinks take the header as the column list (`to_csv.go:56`,
   `to_table.go:101`, `to_markdown.go:84`, `to_json.go:51`) — the
   right behaviour for a header that is *authoritative*, which is the
   whole point of the header (jsonl-schema-header.md).

Put together: data present on the wire never reaches the output and
nothing says so. This violates "fail loudly" and will bite any SQL
export with a nullable column. The same file read as `from csv` with an
empty first cell keeps the column — CSV has a header row to infer from.
`-merge-schemas` is about multiple *files* and does not apply. If the
null is in row 2 instead, everything is fine (the field is in the
schema, row 2 just lacks it).

### F2. `_line_number` — what it is for, and whether it is needed

Where it comes from: `ReadJSONFromReader` / `ReadJSONSafe*` /
`ReadJSONFast*` and the `ReadCommandOutput` / `ExecCommand` readers
(`io.go:409, 552, 678, 1012, 1093, 1420, 1488, 2046, 2127`) inject
`_line_number` into every record. Present since the initial commit
(StreamV3), where it was a debugging aid for "which input line did this
record come from" on raw JSON files and command output.

Who reads it: **nothing in the CLI.** Consumers are two `examples/`
programs (which filter it *out* to compare round-trips), one test
asserting it is *absent* (`io_writejsonl_schema_test.go:104`), and code
comments in `from ssh` / typed codegen that call its appearance "a real
bug fixed by v4.41.2 for users who chain pipelines"
(`codefragment_typed.go:282`, `io.go:823`). `ReadJSONLFromReader` was
written specifically to *not* inject it.

Where users meet it today:

- Any headerless JSON/JSONL file through `from jsonl FILE` / `from
  FILE.jsonl` — i.e. every DuckDB or Postgres NDJSON export — grows a
  `_line_number` column (the array reader does not add it, so the same
  data looks different by export shape).
- **The signal family emits no `_schema` header** — `fft`, `convolve`,
  `correlate`, `spectrogram` (verified: first output line is a record).
  The next stage reads headerless JSONL with the injecting reader, so
  `fft … | to table` shows `_line_number` first. The signal codelab
  documents it as "the row counter every ssql stream carries"
  (`cli-signal-processing.md:126`), which is not true of any stream that
  has a header — a doc written around an artifact.

Answer to Ross's question: **no real use, not needed.** It is a
side-effect of the headerless path, actively worked around in the
distributed path, and the one place it is "documented" is describing a
different bug (missing header on signal output).

### F3. Time: the library has it, the wire format does not

What the *package* does today:

- Records hold `time.Time` values; `GetOr(r, "ts", time.Time{})`
  converts strings on the fly via `convertToTime` (`core.go:1035`):
  RFC 3339, `2006-01-02 15:04:05` (SQL datetime — DuckDB's export
  form), `2006-01-02T15:04:05` (no zone → UTC), a plain `2006-01-02`
  date as midnight UTC (added 2026-09-16 for RANGE frames over DATE
  columns — DFC130 unit 3), and int64 epochs.
- The JSON writer emits `time.Time` as RFC 3339 Nano (`core.go:1196`),
  which DuckDB infers as TIMESTAMP (§1).
- Expressions: `now()`, `date(str)`, `duration(str)`, `bucket(ts, dur)`
  produce or consume times (`doc/EXPRESSIONS.md:258–262`); `resample`
  parses RFC 3339 / SQL datetime strings and epochs itself, with
  `-time-format` for others.
- The typed lane infers `time.Time` for CSV columns whose samples all
  parse as RFC 3339 (`typed_schema.go:23`, preference order `int64 →
  float64 → bool → time.Time → string`) and reads Parquet TIMESTAMP into
  `time.Time` (`typed/io_parquet.go:382`). typed-performance-notes §2
  has a plan for a faster RFC 3339 parser.

What the *CLI / wire* does not:

- `FieldType` (`io.go:52`) is `auto | string | int | float | bool`;
  the `_schema` vocabulary (`lib/schema.go:15`) is `string | int | float
  | bool | json`. `lib.InferTypeString(time.Time)` falls to the default
  → `"string"`.
- `ssql cast -type ts time` → `unknown field type: "time" (use: auto,
  string, int, float, bool)`. `mapTypeToSQL` already knows `date` and
  `timestamp` (`generate_sql.go:1588`) — the SQL side is ahead of exec.
- `TypedSchemaFromHeader` maps only `int/float/bool/string`; a `time`
  wire type would need one more case (the runtime parser exists).
- The CSV reader never produces `time.Time`; `hire_date` is a string
  end to end. This is why `resample`'s SQL translation refuses string
  timestamps ("numeric epochs only") — there is no typed column to hand
  DuckDB.
- **`date()` is expr-lang's builtin, not ours, and it parses fewer
  forms than the library.** `lib/runtime/env.go` registers `bucket`,
  `now`, `duration`; `date` comes from expr-lang v1.17.6
  (`builtin/builtin.go:508`), whose layouts are `2006-01-02`, `15:04:05`,
  `2006-01-02 15:04:05`, RFC 3339, RFC 822/850/1123. Verified: DuckDB's
  `2026-01-02 10:30:00` and `2026-01-02` parse; Postgres's `timestamp`
  form `2026-01-02T10:30:00` (no zone) fails with `invalid date`,
  though `convertToTime` accepts it; Postgres CSV `timestamptz`
  `2026-01-01 23:30:00+00` fails in both. Fix: register an ssql `date`
  in `env.go` that tries `convertToTime`'s forms plus `2006-01-02
  15:04:05-07` and `2006-01-02T15:04:05`, so the expression function
  and `GetOr[time.Time]` agree — and keep the transpiler/SQL lanes in
  step (`expr_go.go`, `generate_sql.go`).
- **Bug, independent of any decision:** writing a time into an
  *existing* string field — `update -set-expr ts 'date(ts)'` — hits
  `applyValueToRecordWithTypeCheck` → `coerceToString` → the `default`
  branch `fmt.Sprintf("%v")`, producing `2026-01-02 10:30:00 +0000 UTC`
  (Go's `Time.String()`), with a "coerced to match existing type"
  warning. That form is not RFC 3339, DuckDB reads it as VARCHAR, and
  ssql's own `convertToTime` cannot parse it back. A *new* field gets
  RFC 3339. `coerceToString` needs a `case time.Time: return
  v.Format(time.RFC3339Nano)` regardless of §6 D1.

## 3. Findings on the way out (ssql → DuckDB, ssql → Postgres)

None beyond the header, on either engine (Postgres tables in §1). DuckDB infers BIGINT/DOUBLE/BOOLEAN/VARCHAR from
ssql's JSON scalars, DATE from `YYYY-MM-DD` strings and TIMESTAMP from
RFC 3339 strings, so a time that ssql *has* as `time.Time` round-trips
as a typed column. Ints written as JSON numbers stay integral (`salary`
→ BIGINT, not DOUBLE) because the writer does not add `.0`.

## 4. Options for the time type (D1)

**A. Status quo plus the coercion fix.** Time stays a string on the
wire; functions convert on use. Cheap: one `case time.Time` in
`coerceToString`. Cost: `cast` cannot make a time; the typed lane only
gets `time.Time` from CSV sampling, never from a header; SQL `resample`
over string timestamps stays refused; `sort` on mixed formats is
lexical.

**B. `time` becomes a wire type.** `_schema` gains `"time"`; the JSON
writer keeps RFC 3339 Nano; the schema-aware reader coerces a `time`
column via `convertToTime` (RFC 3339, SQL datetime, `T` without zone,
epochs — the existing formats); `cast -type ts time` (and `date` as an
alias?); `InferTypeString(time.Time)` → `"time"`; `TypedSchemaFromHeader`
maps `time → time.Time`; `mapTypeToSQL("time")` → TIMESTAMP so
`generate sql` casts and `resample` over a `time` column translate. Five
lanes, so an equivalence case per touched command (`cast`, `update`,
`sort`, `resample`, `bucket`) with a DuckDB lane.

Sub-decision B1, **inference**: should readers *auto-detect* time
strings (RFC 3339 in CSV/JSON → `time`), or only produce `time` on
explicit request (`cast -type ts time`, `from … -type ts time`, `date()`,
`bucket`/`resample` output)? Auto-detect changes existing outputs (a
`2018-03-15` string would print differently only if we reformat — if
the writer emits what it read, nothing visible changes but the header
type) and costs a `time.Parse` per candidate cell on the hot path
(typed-performance-notes §2: this is the dominant cost when it applies).
Explicit-only is safe and matches how `cast` works for everything else.
Recommendation: **explicit first**, RFC 3339 auto-detect in CSV as a
follow-up behind a flag if wanted.

Sub-decision B2, **date-only values**: DuckDB and Postgres distinguish
DATE from TIMESTAMP; Go does not. Treat `2026-01-02` as a `time` at
midnight UTC (what `from parquet` already does for DuckDB DATE) and let
`generate sql` cast to TIMESTAMP; do not add a separate `date` type.

Recommendation: **B with B1 explicit and B2 as above.** The package has
the type, the SQL translator has the names, the typed lane has the
parser; the wire format is the one place time does not exist, and it is
the reason `resample`'s SQL lane refuses the most common input shape.
Size: a day for the core (schema, cast, reader coercion, writer, typed
header mapping, SQL cast) plus the equivalence cases; `resample` SQL
over `time` columns is a second unit.

## 5. Other decisions

**D2. Remove `_line_number` from the CLI path.** Stop injecting it in
the readers `from json`/`from jsonl` use (`ReadJSONFromReader` and
family are public library API — either change them, which only the two
`examples/` programs would notice, or route the CLI through
`ReadJSONLFromReader`, which already does not inject, and leave the
library alone). The `ReadCommandOutput`/`ExecCommand` readers are the
only place a line number has a plausible meaning ("which output line of
the command"); keep it there or drop it too — the CLI has no `from
command`. Fix the signal codelab sentence when the column goes.
Recommendation: route the CLI through the non-injecting reader, keep
the library helpers as they are, add a `-line-numbers` flag to `from
jsonl`/`from lines` only if someone asks.

**D3. Schema inference must not lose fields.** Options: (a) infer the
header from a bounded sample (first N records or first M bytes, the way
the typed sampler already works) and treat a field first seen *after*
the sample as an error ("field X appeared at record N but is not in the
schema; use `-sample` larger or `from json` for the whole file") — loud,
bounded memory; (b) keep NULL as a present field with type `string`
(null → typed later on first non-null) — fixes the null case only, not
"field appears in row 50"; (c) two passes over files (not stdin).
Recommendation: **(a)**, with the sample size shared with the typed
sampler and the array reader (`from json` has the whole document, so it
can scan all of it). Union the field set over the sample; the type of a
field that is only ever null in the sample is `string`.

**D4. Signal commands emit the header.** `fft`, `convolve`,
`correlate`, `spectrogram` (and `ifft`) build fresh records with known
field names and types — write the `_schema` line like every other
command. Independent of D2, but D2 without D4 would only hide the
symptom in `to table`; both are needed for the signal codelab's tables
to stop leading with a counter.

**D5. Codelab §2 gains the reverse direction** — three lines: `from
jsonl f.jsonl` for DuckDB's default export, `from f.json` for `ARRAY
true`, `duckdb -json … | ssql from json -`; timestamps arrive as strings
and `date()` parses DuckDB's `2026-01-02 10:30:00` form (or, after D1,
`cast -type ts time`). For Postgres: `psql -At -c "select row_to_json(t)
from … t" | ssql from jsonl -`, and `\copy` both ways for CSV. Both
engines are now verified, so the paragraph can say so.

**D6. An interchange gate.** `TestDuckDBInterchange` in `cmd/ssql`,
skipped without the binary like the equivalence DuckDB lane, and a
`TestPostgresInterchange` twin gated on `SSQL_TEST_PG_HOST` (psql over
ssh on the rig node, SQL on stdin): `to jsonl`
→ `read_json_auto` row count and column types; `COPY … TO` NDJSON and
ARRAY → `from jsonl`/`from json` → row count *and field set*, with a
NULL-in-first-row column (fails today on F1). The doc claim then has a
test behind it instead of a check that never ran.

## 6. Decisions to make

| # | Question | Recommendation |
|---|---|---|
| D1 | Add `time` to the wire schema? | Yes (B), explicit only (B1), no separate date type (B2); fix `coerceToString` and register ssql's own `date()` now either way |
| D2 | Keep `_line_number`? | No — CLI reads through the non-injecting reader; library helpers unchanged |
| D3 | Schema from the first record? | Infer from a bounded sample; late fields are a loud error |
| D4 | Signal commands without a header? | Emit it |
| D5 | Codelab reverse direction | Add, both engines verified |
| D6 | Interchange tests | Add: DuckDB gated on the binary, Postgres gated on `SSQL_TEST_PG_HOST` |

Order if all yes: coercion fix + D2 + D4 (small, unblock the codelab
text) → D3 → D5 → D1 → D6 alongside each.

## 6a. Shipped 2026-09-19: coercion fix, D2, D4

Ross, reviewing the agenda: "let's go with that" (the small trio first,
because F1/F2 are silent loss, not features).

- **Coercion (F3).** `coerceToString` has a `time.Time` case: RFC 3339
  Nano, the form a new field already got. `update -set-expr d 'date(d)'`
  on a string column now round-trips through `convertToTime`, DuckDB
  and Postgres.
- **D2.** `lib.ReadJSONL` — the headerless branch every CLI stage and
  `from jsonl` fall into — reads through `ssql.ReadJSONLFromReader`
  (no `_line_number`, schema cached across records) instead of
  `ReadJSONFastFromReader`. The library helpers are untouched, as
  recommended. This also retires one of the per-record-schema reader
  sites on the TODO's performance list.
- **D4.** `fft`, `ifft`, `convolve`, `correlate`, `spectrogram` write
  through `writeWithInferredSchema` (which gained a writer option so
  they keep `ctx.Stdout()`); the signal codelab's two tables and its
  "row counter every ssql stream carries" sentence are corrected.
- **Gate:** `cmd/ssql/wire_header_test.go` — all five signal commands'
  first line is a header and `to table` shows no `_line_number`; a
  headerless NDJSON file yields exactly `a,b` through four entry points
  (`from jsonl FILE`, `from FILE`, stdin `from jsonl -`, a bare stage);
  the time coercion renders RFC 3339. Watched failing on all eight
  assertions with the fixes stashed.
- **What the header exposed.** `TestCodelabRuns` failed on the signal
  codelab's cross-correlation block: `to chart -x lag` over full
  `correlate` output, which has `index`, never `lag`. Headerless, `to
  chart` could not validate and drew an empty axis; with a schema it
  refuses. Doc corrected; TODO notes full mode should emit `lag`.

## 6b. Shipped 2026-09-19: D3, sampled schema inference

- **Where.** `writeWithInferredSchema` (`cmd/ssql/commands/from.go`) is
  the hub every `from` source writes through. With a `fieldOrder` (CSV,
  TSV, Parquet — the source has its own header) nothing changed: first
  record, no buffering, no per-row check. Without one it pulls up to
  `schemaSampleRowsDefault` = 1000 records (`SSQL_SCHEMA_SAMPLE`
  overrides; 1 = the old behaviour for live streams), builds the header
  with `lib.InferFromSample` — union in first-seen order, int+float →
  float, other disagreement → string — then streams the sample and the
  rest.
- **Late fields are loud.** Past the sample each record's fields must be
  in the header. Same-shaped records share a Schema pointer (the reader
  cache, §6a correction), so the check is a pointer compare per row and a
  field walk per new shape. A miss ends the stream and the command exits
  non-zero: `field "late" first appears at record 3, after the 2-record
  sample … set SSQL_SCHEMA_SAMPLE to at least 3, or give the field a
  value in an earlier record`.
- **Only exec was wrong.** Headerless input piped straight to a sink
  already unioned all fields (sinks collect everything when there is no
  header); record codegen and DuckDB kept the column; typed renders the
  absent cell as a zero value (the known DFC124 §3 divergence). The loss
  needed `from`'s one-record header.
- **The gate found a second bug on its first run.** Equivalence case
  `jsonl_null_in_first_record` (fixture `null_first.jsonl`, Golden rows)
  failed in the go-record lane: `ssql.ReadJSONAuto`, which generated Go
  reads files through, still injected `_line_number` on its JSONL branch.
  It now goes through `ReadJSONLFromReader` from the bytes it has already
  read. D2 had fixed exec only — the "fixed in one lane, live in the
  others" rule, caught by the differential gate as designed.
- **Tests.** `lib.TestInferFromSample`; `TestNullInFirstRecordSurvives`
  (four entry points × `to csv` exact output, three more sinks, the late
  field failure with its message); the equivalence case.
- **Not done: a column that is NULL in every sampled record.** The
  parser drops nulls before anyone sees the key, so such a field is
  invisible until its first value — at which point it is either inside
  the sample (kept) or a loud late-field error. A column that is NULL in
  the WHOLE file still yields no column; no data is lost, only the name.
  Typing it `string` as §5 D3 suggested needs the parser to report null
  keys — left for D1, which touches the same code.

## 6c. Shipped 2026-09-19: D5, the codelab's reverse direction

Codelab §2 has "Coming back the other way": DuckDB `COPY … TO
'emp.json'` and `(ARRAY true)` read by extension, `duckdb -json … |
ssql from json -`, Postgres `row_to_json … | ssql from jsonl -`,
`\copy … TO STDOUT CSV HEADER | ssql from csv -`, and `ssql … to csv |
psql -c "\copy … FROM STDIN CSV HEADER"`. **Every command was run as
written** — DuckDB 1.5 locally, PostgreSQL 16 on the rig — with a NULL in
the first row and both Postgres timestamp types in the data. That is the
point of this DFC: the paragraph it replaces was written from a check
that never ran.

What the runs found:

- **`from csv -` sorted the columns.** `id,note,ts,tz,amount` arrived as
  `amount,id,note,ts,tz`. The CSV reader's schema is name-sorted by
  design; the file path compensated by re-reading the header for
  `fieldOrder`, stdin and HTTP could not, and TSV never did, even for
  files. Fixed with `peekDelimitedHeader` — a non-consuming peek that
  grows only until the first newline, so a live stream is not held for a
  buffer. Test `TestDelimitedColumnOrderFromPipe` (quoted header cell,
  TSV file and pipe, a header that arrives before its data).
- **`date()` on Postgres's zoneless `timestamp` JSON form
  (`2026-01-02T10:30:00`) fails — loudly**; DuckDB's `2026-01-02
  10:30:00` and Postgres `timestamptz` (`…+00:00`) parse. Exactly F3's
  prediction; the codelab says so rather than pretending, and D1's
  ssql-owned `date()` closes it.
- **JSON columns come out in name order** (`amount,id,ts,note` for a
  source written `id,note,ts,amount`): `ParseJSONLine` builds a map and
  `NewRecord` sorts its keys, so the object's key order is gone before
  any schema logic runs. Unlike CSV there is no header to peek; keeping
  the order needs the parser to emit fields in encounter order. Logged in
  TODO; the codelab states the behaviour.
- NULL round-trips both ways: JSON `null` ↔ absent field, empty CSV cell
  ↔ SQL NULL under `CSV` mode.

## 6d. Shipped 2026-09-19: D1 core — `time` on the wire, ssql's `date()`

Decision taken as recommended in §4: **B** (a wire type), **B1**
explicit only (no reader auto-detection), **B2** one type (a DATE is
midnight UTC; `timestamp`/`datetime`/`date` are aliases of `time`).

- **One parser.** `ssql.ParseTime` (`time_parse.go`) is the single list
  of layouts behind `GetOr[time.Time]`, the wire type, `cast` and
  `date()`: RFC 3339, SQL datetime, zoneless `T` form, Postgres CSV
  `timestamptz` (`…+00`), bare date, Unix seconds. `MustParseTime` is
  the explicit-request form that panics naming the field.
- **`date()` is ssql's**, registered with `expr.Function` — compile-time
  binding, NOT an environment entry. That distinction matters: the env
  route (how `bucket` is registered) would have been clobbered by, or
  clobbered, a record field named `date`, the most likely column name
  there is. `date(date)` works. The one side effect: the unknown-field
  validator saw the `date` callee as a field reference once it stopped
  being a BuiltinNode; `extractIdentifiers` now counts callee uses of
  `ssql.ExprCompiledFunctions` separately. Neither the Go transpiler nor
  the SQL translator handled `date()` before or now (record fallback;
  loud SQL refusal).
- **Wire type.** `FieldTypeTime` / `lib.TypeTime = "time"`;
  `InferTypeString(time.Time)`; header-aware readers coerce a `time`
  column with `ParseTime` and leave a value they cannot read as it was
  (never a zero time); `from csv -type F time`; `TypedSchemaFromHeader`
  maps it to `time.Time`.
- **cast** in exec, record codegen (`ssql.MustParseTime`) and typed
  (`castTimeExpression`), loud on a non-time in all three.
- **where** on a time field compares instants (`Equal/After/Before`)
  against an operand parsed by `ParseTime`; a non-time operand is an
  error. Typed `where` had `eq`/`ne` only, against
  `time.Parse(RFC3339, lit)` with the error dropped — a silently zero
  time; the literal is now parsed at generation time and emitted as
  `time.Unix(s, ns).UTC()`. Typed `sort` takes a time key (`UnixNano`,
  or `Compare` in the multi-key comparator).
- **SQL.** `mapTypeToSQL("time")` → TIMESTAMP; the assembler records
  which columns a cast made times (`sqlTimeColumns` — the one column
  type it tracks) so `bucket()` over them renders `time_bucket(INTERVAL,
  col, TIMESTAMP '1970-01-01')` / `date_bin` with the epoch origin. The
  engines default to 2000-01-03, which is a different grid for any width
  that does not divide 10959 days.
- **Rendering.** `formatValue` (CSV), the TSV writer, `displayValue`
  (table/markdown), `convertRecordValue` (`to json`) and
  `convertToString` (`GetOr[string]`) all render RFC 3339.

**What the gate found** — four equivalence cases
(`cast_time_where_sort`, `cast_time_mixed_forms_sort`,
`cast_time_bucket_groupby`, `update_date_function_forms`), run on exec,
the Go lanes, DuckDB, Postgres and DataFusion:

1. typed `cast` had no time target; typed `where` no ordering on time;
   typed `sort` refused it; the derived struct lacked the `time` import.
2. Record-mode `update -set-expr` stored any non-scalar result with
   `fmt.Sprintf("%v")` — so `now()` or `date(x)` in generated code had
   ALWAYS produced `2026-01-01 00:00:00 +0000 UTC` where exec stored a
   time. A pre-existing cross-lane bug, invisible until a case grouped
   by a time.
3. DuckDB's `CAST('…+00' AS TIMESTAMP)` shifts into the session time
   zone, so the mixed-forms case is skipped there with that reason; its
   golden is the oracle.
4. The harness needed `equivNormaliseTimes`: `2026-01-20T00:00:00Z`,
   `2026-01-20 00:00:00` and `2026-01-20` are one instant spelled three
   ways; applied to every lane and the goldens alike.

Tests: `time_parse_test.go` (forms, junk, `date()` incl. a field named
`date`, rendering, bucket, field type), `TestTimeWireType` (header,
cross-process round trip, sinks, loud failures), the four cases.

**Second unit, not done:** `resample` over a time column in SQL (it
still refuses string timestamps; a cast upstream now gives it a typed
column to use); null-key visibility in the JSON parser (§6b residual);
RFC 3339 auto-detection behind a flag, if ever wanted.

## 6e. Shipped 2026-09-19: D6, the interchange gates

`cmd/ssql/interchange_test.go`. `TestDuckDBInterchange` is gated on the
binary like the equivalence lane; `TestPostgresInterchange` on
`SSQL_TEST_PG_HOST` (psql over ssh, SQL and `\copy … FROM STDIN` data
on one stdin, a per-process table name dropped in `t.Cleanup`). They
are this document's §1 as a test: every direction that was checked by
hand on 2026-09-14 and again for D5, with the inputs that broke things —
a NULL in the FIRST row, zoneless and zoned timestamps, a whole-number
float — and assertions on row counts, the exact field SET (no
`_line_number`, nothing dropped), column order, `typeof(ts) =
TIMESTAMP` on the DuckDB side and `note IS NULL` on the Postgres side.
One assertion pins the codelab's warning rather than a success: a plain
redirect keeps the `_schema` line and DuckDB reads it as a phantom row.

Watched failing: with `SSQL_SCHEMA_SAMPLE=1` (first-record inference,
the pre-D3 behaviour) both tests fail on the nullable column. The
Postgres test passed on its first run and the DuckDB one needed only a
test fix — expected, since D2–D5 and D1 had already been driven by the
same manual runs; the value now is that the claim cannot rot.

## 6f. Shipped 2026-09-19: `resample` over a time column

The unit was filed as "resample SQL over time columns" and turned out to
start one layer down: the interpreted `resample` rejected a `time.Time`
field outright ("unsupported type"), so there was nothing for SQL to
agree with.

- **Library.** `tsCodec` has a fourth family, `time`: `toNanos` is
  `UnixNano`, `fromNanos` returns a UTC `time.Time`, the output record
  stores it with `.Time`. `-from`/`-to` against a time column parse with
  `ParseTime`. No layout to preserve, no epoch unit to detect, so
  `-time-unit` is irrelevant there. Mixed families stay an error.
  Record codegen calls the same function; nothing to do.
- **Typed.** The template shims T → Record → T around the one
  implementation; it now carries a `time.Time` field through (`.Time`,
  `GetOr(rec, f, time.Time{})`, the `time` import for the synthesized
  struct).
- **SQL.** The numeric lowering is an integer grid (`generate_series`
  over epoch values, ASOF joins). For a column in `sqlTimeColumns` the
  same machinery runs on `epoch_us(col)` with the unit pinned to
  microseconds — the engine's resolution — and the grid point returns as
  `make_timestamp(__g)`. A width finer than a microsecond trips the
  existing "finer than the epoch unit" error. String timestamps remain
  refused; the answer for them is now "cast first".
- **What the gate found.** Three cases (`resample_time_previous` with a
  golden taken from the string-date form, `_next`, `_linear`). Typed
  refused; then `linear` disagreed with DuckDB in the last place at one
  grid point — the SQL computed `(g−p)·Δv / Δt`, Go computes
  `((g−p)/Δt)·Δv`. Aligned to Go's association; the old numeric case had
  simply never hit a value where the two round differently.
- Tests: `TestResampleTimeFamily` (values and grid identical to the
  string family for all three fills, bounds, loud errors),
  `TestTranslateResampleSQLTimeColumn` (and that the numeric lowering is
  untouched), the three cases.

Still open, in the recommended order: ~~D3~~ (shipped, §6b; was: sampled schema inference
so a NULL-in-first-record field is not dropped — the remaining silent
loss), **D5** (codelab reverse direction), **D1** (`time` on the wire)
with the ssql-owned `date()`, **D6** (interchange tests alongside).
The other injecting call sites (`from_json.go` stdin codegen,
`aux_input.go` `.json` side inputs, `completion_sources.go`,
`format_table.go`'s `ReadJSON`) are library readers on the array/auto
path and belong to the JSON-reader TODO item, not to this slice.

## 7. References

- `doc/research/jsonl-schema-header.md` — the header as the authority
  on fields and types (why sinks trust it, F1).
- `doc/research/typed-performance-notes.md` §2 — time parsing cost;
  the argument for explicit rather than auto-detected time.
- `doc/research/dfc121_resample_command.md` — `resample`'s time
  handling and its SQL lane's "numeric epochs only".
- `doc/research/multimode-equivalence-testing.md` — why a type change
  needs a case in every lane.
- `doc/EXPRESSIONS.md` §Date Functions — `now()`, `date()`,
  `duration()`, `bucket()`.
- `doc/cli-codelab.md` §2 — the interchange paragraph this doc checks.
- `doc/research/TODO.md` item 0r — the raw findings of 2026-09-14.
