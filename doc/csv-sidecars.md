# CSV Schema Sidecars

A CSV file holds names and text, nothing more. Every reader has to guess the types from a sample of rows, and a sample can miss things: a float that first appears on row ten thousand, a date in a local format, or a code like `007` or `1e3` that is really text. A **schema sidecar** is a small JSON file kept next to the data that states the column types once. ssql reads two standard formats for it, both described below, and writes one of them.

For a walkthrough, see "Keep the types with the CSV" in the [CLI Codelab](cli-codelab.md). The Go functions are under "Schema sidecars" in the [API Reference](api-reference.md). The design is in [DFC146](research/dfc146_csv_schema_sidecars.md).

## The smallest one that works

A Frictionless Data Package is a file named `datapackage.json`, kept in the same directory as the data:

```json
{
  "resources": [
    {
      "path": "sales.csv",
      "schema": {
        "fields": [
          {"name": "code",   "type": "string"},
          {"name": "amount", "type": "number", "ssqlType": "decimal:2"},
          {"name": "sold",   "type": "date"}
        ]
      }
    }
  ]
}
```

Things to know about it:

- **`path`** names the data file, relative to the package's directory.
- **One package can describe many files.** A resource that names a different file is ignored.
- **`name` and `profile`** at either level are optional; `to csv -sidecar` writes them, but ssql doesn't need them to read the file.
- **Columns the sidecar leaves out** are typed by sampling, as usual.

The W3C CSV on the Web form works the same way. It is named `sales.csv-metadata.json`, or `csv-metadata.json` for a whole directory:

```json
{
  "url": "sales.csv",
  "tableSchema": {
    "columns": [
      {"name": "code",   "datatype": "string"},
      {"name": "amount", "datatype": {"base": "decimal", "ssqlType": "decimal:2"}},
      {"name": "sold",   "datatype": {"base": "date", "format": "dd/MM/yyyy"}}
    ]
  }
}
```

In this form a column can give `titles` instead of `name`.

A bare Table Schema works too: the `{"fields": [...]}` object on its own, with no package around it. ssql never finds one of those by itself, so you name it with `-sidecar FILE`.

## Where ssql looks, and who wins

The sidecar is read by `from FILE`, `from csv` and `from tsv`. It applies wherever the pipeline runs: executing, schema mode and completion, and `generate go` and `generate sql`. For a source of **one local file**, ssql takes the first of these that describes the file:

1. `FILE-metadata.json` (for example `sales.csv-metadata.json`)
2. `csv-metadata.json` in the same directory
3. `datapackage.json` in the same directory

What overrides what:

- **Your flags beat the sidecar, column by column.** `-type COL TYPE` and `-format COL PATTERN` replace what the sidecar says for that column.
- **`-no-sidecar`** ignores any sidecar.
- **`-sidecar FILE`** names one explicitly. You need it when:
  - the sidecar lives somewhere else;
  - the data comes from stdin, in which case the package's first resource is used;
  - the source is several files.
- **No sidecar is ever looked for** next to several files, stdin or a URL.

A file that `join`, `traverse -start-file`, `union`, `merge`, `except` or `intersect` opens directly is read the way `from FILE` reads it, sidecar included, in every lane: `join sales.csv` gives the same types as `join <(ssql from csv sales.csv)`.

## Types

| Sidecar type | ssql type |
|---|---|
| `integer` (also `int`, `long`, `short`, `byte`, `year`) | int |
| `number` (also `decimal`, `double`, `float`) | float |
| `boolean` | bool |
| `date`, `datetime`, `time`, `timestamp` | time |
| `object`, `array`, `json`, `geojson` | json |
| `string` (also `yearmonth`, `duration`, `geopoint`, `anyURI`) | string: the cells stay text, whatever they look like |
| `any` | not set: sampled as usual |

Any other type name is refused, and the error names the column.

### The `ssqlType` extension

Some ssql types have no equivalent in either standard. For those, ssql adds its own `ssqlType` key next to the standard type:

- in a Frictionless field: `{"name": "amount", "type": "number", "ssqlType": "decimal:2"}`
- in a CSVW `datatype` object: `{"base": "decimal", "ssqlType": "decimal:2"}`

When it is present, ssql reads `ssqlType` instead of the standard type. Other tools ignore keys they don't know, so they still see an ordinary number, integer or string. These are the types that get one:

| `ssqlType` | Standard type written beside it | Meaning |
|---|---|---|
| `decimal:S` | `number` | an exact decimal with S places (money) |
| `int128` | `integer` | a 128-bit integer (a sum, or a cast past int64) |
| `inet` | `string` | an IP address or prefix |

A decimal is only ever a decimal because something declared it: a sidecar like this, `-type`, a cast, a `_schema` header or a Parquet DECIMAL. ssql never infers one from a value like `12.34` (see [EXPRESSIONS.md](EXPRESSIONS.md)).

### Dates and times

A date or time column without a `format`, or with an ISO one, is read as RFC 3339 / ISO 8601. Any other format is honoured as the column's cell pattern, the same as `-format COL PATTERN`:

- **Frictionless** formats are strftime (`"%d/%m/%Y"`).
- **CSVW** formats are Unicode date patterns (`"dd/MM/yyyy"`), which ssql translates.

A pattern ssql cannot parse is refused, and the error suggests reading the column as text with `-type COL string`. The cells are read in the source's zone: `from … -tz ZONE`, else `TZ`, else the system zone.

## What is refused

Each of these stops the command, with the reason and the remedy:

- a `dialect` that says the file has no header row (ssql reads headed files);
- a `dialect` delimiter other than comma or tab (`from tsv` detects other delimiters from the header);
- a type name ssql does not know, or a date format it cannot parse;
- a sidecar column that is not in the file's header. The message names the sidecar, and gives the way past it: `-no-sidecar` on a source, `<(ssql from csv FILE -no-sidecar)` for a file given to `join` and the other commands.

## Writing one

`to csv FILE -sidecar` writes the CSV, plus a `datapackage.json` beside it describing the pipeline's types. If a package is already there, ssql adds or updates this file's resource and keeps the others. To see the schema a pipeline would write, without writing any data:

```bash
ssql generate schema -datapackage -pipeline 'ssql from sales.csv | ssql group-by region -sum amount total'
```

The written types follow the tables above:
- a time is written as `datetime`;
- a json field is written as `object` or `array` when its shape is known;
- decimals, int128 and inet carry an `ssqlType`.

Reading the file back therefore gives the same types without sampling. `ssql conventions -category sidecar` prints a short summary of this page.
