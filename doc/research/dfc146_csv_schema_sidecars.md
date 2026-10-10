# CSV schema sidecars: CSVW and Frictionless Table Schema

Reference: DFC146
Created: 2026-10-10
Last modified: 2026-10-10

[Back to Index](./README.md)

## 1. The question

Ross, 2026-10-10: is there a standard for CSV typing that ssql should
support, for import and export and for sample-free schema detection,
and would it be useful?

A CSV carries names and text. Everything ssql knows about a column's
type comes from a sample (`DefaultInferRows` rows in the reader, 200 in
schema mode, 1000 in the typed sampler, 1000 in the SQL prologue) or
from `-type COL TYPE` on the command line. The sample is blind to what
it does not see: a float on row ten thousand of an int column makes a
typed program fail at runtime, a date in a local format stays a string,
and a column that is empty in the sample is a string. Zero-padded
identifiers are already safe (`ZeroPaddedNumber`, DFC133: every
inference site keeps them as text). `-type` fixes each case per
invocation, which is the wrong place for a fact about the file: it is
repeated in every pipeline that reads the file, it is not shared with
the next reader, and `generate sql` does not translate it (the
`castable_from_type` equivalence case skips the duckdb lane for that
reason).

## 2. The standards

Two exist. Neither is a header convention; both are a JSON file beside
the data describing its columns.

**W3C CSV on the Web (CSVW)** — "Model for Tabular Data and Metadata on
the Web" and "Metadata Vocabulary for Tabular Data", W3C
Recommendations, 2015. The metadata is JSON-LD. Discovery is defined:
for `X.csv` look for `X.csv-metadata.json`, then `csv-metadata.json` in
the directory, then an HTTP `Link: <…>; rel="describedby"` header, then
`/.well-known/csvm`. The document has a `url` (or a `tables` array,
each with a `url`), a `tableSchema.columns` array of `{name, titles,
datatype, null, required, …}`, `primaryKey`, `foreignKeys`, and a
`dialect` (`delimiter`, `header`, `quoteChar`, `encoding`). Datatypes
are the XML Schema names: `string`, `integer`, `long`, `decimal`,
`double`, `number`, `boolean`, `date`, `dateTime`, `time`, `json`,
`any`, and an object form `{"base": "date", "format": "dd/MM/yyyy"}`.
Strength: per-file discovery is exact. Weakness: weight; few tools
outside government statistics publishers (ONS, data.gov.uk) read it.

**Frictionless Data Table Schema** — the `datapackage.json` format used
by CKAN and most open-data portals, with libraries in Python (the
`frictionless` package), R, Ruby, Go and JavaScript. A package lists
`resources`, each with a `name`, a `path` (string or array) and a
`schema` (inline, or a path to a separate schema file) whose `fields`
are `{name, type, format, constraints, …}`, plus `missingValues`,
`primaryKey` and `foreignKeys`; a resource may carry a `dialect`
(`delimiter`, `header`, `quoteChar`). Types: `string`, `integer`,
`number`, `boolean`, `date`, `time`, `datetime`, `year`, `yearmonth`,
`duration`, `object`, `array`, `geopoint`, `geojson`, `any`. Strength:
the ecosystem, and a type vocabulary that maps onto ssql's nearly one
to one. Weakness: discovery is by directory; the resource is found by
matching its `path` to the file.

Neither DuckDB, Postgres `COPY` nor pandas reads either format
natively; both are read by their own ecosystems. Arrow and Parquet
carry their schema in the file and need neither; this is about CSV
and TSV that stay CSV and TSV, which is most data in the wild.

## 3. The type mapping

| Sidecar type (either standard) | ssql wire type | Note |
|---|---|---|
| integer, int, long, short, byte | int | |
| number, decimal, double, float | float | |
| boolean | bool | |
| date, datetime, dateTime, time | time | ISO 8601 / RFC 3339 forms (`ParseTime`); a `format` is refused, see §5 |
| object, array, json | json | the cell's JSON text as a nested value (DFC144) |
| string, any, and anything else | string / auto | `any` is "infer"; an unknown type name is an error |

The reverse, for writing: int → `integer`, float → `number`, bool →
`boolean`, time → `datetime`, string → `string`, json → `object` or
`array` by the first value written (`any` when none was seen).

## 4. Decision: read both, write Frictionless

Reading both costs one JSON parse each (both are a column list) and
the CSVW discovery rule is cheap to apply. Writing one keeps the
surface small; Frictionless is the one whose readers exist in the
tools a CSV is handed to next. The user's `-type` always wins over a
sidecar, and a sidecar can be named explicitly or switched off.

**Reading** (`from csv FILE`, `from tsv FILE`, bare `from FILE.csv`):

1. `-sidecar FILE` names the metadata file; `-no-sidecar` ignores any.
2. Otherwise, for a local `X.csv`: `X.csv-metadata.json` (CSVW), then
   `csv-metadata.json` in the directory with a table whose `url` names
   the file, then `datapackage.json` in the directory with a resource
   whose `path` names the file. First hit wins. A URL source is not
   searched (an extra fetch per read; use `-sidecar`). stdin is not
   searched (no name). Multi-file reads are not searched (the files
   could disagree); `-sidecar` applies to all of them.
3. The sidecar's column types become `-type` overrides for every
   column it names; the user's own `-type` overrides them; columns the
   sidecar does not name are inferred as before. A sidecar column that
   is not in the header is an error, like a `-type` for a column that
   does not exist.
4. Every lane sees the same overrides: exec and schema mode through
   `buildCSVConfig`, record codegen through the config literal, typed
   codegen through `TypeOptions`, and `generate sql` through DuckDB's
   `read_csv(types={…})` and the Postgres `CREATE TABLE` column types —
   which also makes the from-stage `-type` translate for the first time
   (the equivalence skip is removed).
5. Schema mode is sample-free when the sidecar types every column: the
   header names the columns, the sidecar types them, no row is read.

**Writing** (`to csv FILE -sidecar`): writes `datapackage.json` beside
FILE with one resource named after the file's stem, `path` the file's
base name, and the pipeline's schema as `fields`. An existing
`datapackage.json` is kept and the resource replaced or appended, so a
directory of outputs accumulates one package. `generate schema
-datapackage` prints the same Table Schema for a pipeline's output
without writing data. The types come from the `_schema` header in exec
(exact), from the values written in record codegen (observed per
column as rows pass), and from the struct in typed codegen (known at
generation time; a `json` column is `any` there, the struct does not
say object or array).

## 5. What is refused, and why

Loud refusal over silent drift, per the CLI rules. Each of these would
produce a wrong table if ignored:

- `dialect.header: false`. ssql reads headed delimited files
  everywhere (header peek, schema mode, typed sampler, SQL seeding);
  naming columns from the sidecar would touch every site. Refused with
  the hint to add a header row.
- `dialect.delimiter` other than the command's. Refused with the hint
  to read the file with `from tsv`, which detects any single-character
  delimiter from the header.
- A `format` on a date/time column (`dd/MM/yyyy`, `%d/%m/%Y`). The
  reader parses the ISO and RFC 3339 forms only; a format it cannot
  honour would leave the column as unparsed text under a `time` type.
  Refused with the hint to drop the format or override with `-type COL
  string`. Honouring strptime formats needs a per-column layout in
  `CSVConfig`; follow-up.
- An unknown type name. Error, like `-type COL whatever`.

Ignored, documented: `constraints`, `primaryKey`, `foreignKeys`,
`titles`, `description`. `missingValues` beyond the empty string is not
honoured; a column typed `integer` with `NA` cells fails at the cell
(`CellError`), which is loud, with the remedy `-type COL string`.

## 6. Is it useful?

Yes, for four reasons, in order of weight:

1. **Exact types without a sample.** The int column with a late float,
   the date column, the empty-in-sample column: a sidecar states them
   once, next to the data, and every reader of the file gets them.
   Over SSH and HTTP the sample is the expensive part of schema mode.
2. **Lossless CSV round trips.** `to csv -sidecar` then `from csv` gives
   back the pipeline's types, which is what the `_schema` header does
   for JSONL (jsonl-schema-header.md) in a dialect other tools read.
3. **The SQL lane catches up.** From-stage overrides translate, which
   they never did.
4. **Interop.** A data portal's `datapackage.json` is honoured on read;
   an ssql output is consumable by the Frictionless ecosystem and
   documents itself.

Not useful for: Parquet and Arrow (self-describing), JSONL (the
`_schema` header), or a file read once in a throwaway pipeline where
`-type` is shorter.

## 7. Built (2026-10-10)

- Root package `tableschema.go`: `TableSchema` / `TableField`,
  `ParseTableSchema` (CSVW, Frictionless package, bare Table Schema —
  told apart by shape), `FindTableSchema` (the discovery rule),
  `WriteDatapackage` (merge by resource path), `WriteCSVSidecar` (the
  record-mode writer that observes column types as it writes).
- `from csv` / `from tsv` / bare `from`: `-sidecar FILE`, `-no-sidecar`;
  `resolveSidecarTypes` merges sidecar types under `-type` in the
  handler, so every execution path (exec, schema mode, sample, last,
  record and typed codegen) sees one override map; `-type COL json`
  reads a JSON cell as a nested value (`parserForType`, the typed
  sampler's `goTypeFor`), which it did not before (found 2026-10-09).
- `generate sql`: `translateFrom` applies `-type` and the sidecar to
  DuckDB `read_csv(types={…})` and the Postgres column types;
  DataFusion refuses an override it cannot apply.
- `to csv FILE -sidecar`, `generate schema -datapackage`.
- Gates: `TestTableSchemaParse` (root), equivalence
  `sidecar_types_all_lanes` (int column with a late float typed
  `number`, a date column typed `datetime`, through sort and group-by,
  every lane including duckdb), `TestSidecarRoundTrip` (write then
  read in exec, record and typed), schema-mode sample-free case,
  `TestFieldCompletionConfiguration` entries, a codelab section run by
  doc-test.

Follow-ups: strptime/XSD date formats; `header: false` with sidecar
names; `missingValues`; URL discovery via the CSVW `Link` header;
multi-file reads with per-file sidecars; CSVW output (`-sidecar csvw`)
if a consumer turns up.
