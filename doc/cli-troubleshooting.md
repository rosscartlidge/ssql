# ssql Troubleshooting Guide

Quick reference for common issues and their solutions when using the ssql CLI tool.

## Table of Contents
- [Quick Diagnostics](#quick-diagnostics) — inspect a pipeline with ssql itself
- [Common Issues](#common-issues) — what an error means and what to do
- [Getting More Help](#getting-more-help)

---

## Quick Diagnostics

ssql can inspect its own pipelines; reach for these before reaching for
`jq`. (A pipeline's raw output starts with a `_schema` header line, so
`head -1 | jq keys` shows `["_schema"]` and `wc -l` is one too many. When
you do want jq, end the pipeline with `ssql to jsonl`, which drops the
header.) The examples use `employees.csv` from `ssql codelab`.

### What fields and types come out of a stage?

```bash
# Field names and types at the end of a pipeline, from the grammar and the
# source's header — the data is not read
ssql generate schema -pipeline 'ssql from employees.csv | ssql group-by dept -count n -avg salary avg_salary' -data | ssql to table

# Type, count, missing and distinct values per field; min/max/mean for numbers
ssql from employees.csv | ssql describe | ssql to table

# A few rows, as a table
ssql from employees.csv | ssql limit 3 | ssql to table
```

### How many records at each stage?

```bash
ssql from csv employees.csv -records          # input count, the cheapest way for the format
ssql from employees.csv | ssql where -if dept eq Engineering | ssql count
```

### Save an intermediate stage and replay it

```bash
ssql from employees.csv | ssql where -if dept eq Engineering | ssql tee eng.jsonl | ssql group-by level -count n | ssql to table
ssql from eng.jsonl | ssql to table           # the saved stage, replayed
```

### Check a pipeline before running it

```bash
ssql generate json -pipeline 'ssql from employees.csv | ssql where -if dept eq Engineering | ssql to table' | ssql run -check -stdin
```

`generate json` turns the shell form into a pipeline document; `run
-check` validates its grammar and every field reference against the
source's schema without reading the data. A misspelt field is reported
with its stage and the fields that do exist:

```
Error: run -check: stage 2 (where): where references unknown field(s): dpt (available: age, city, dept, hire_date, level, name, salary, status)
```

### Prefer jq?

```bash
ssql from employees.csv | ssql to jsonl | head -1 | jq 'keys'
ssql from employees.csv | ssql to jsonl | jq -r '.dept' | sort | uniq -c
```

---

## Common Issues

### Issue 1: "no such file or directory"

```
$ ssql from nosuch.csv
Error: reading file: open nosuch.csv: no such file or directory
```

Check the path (`ls -l nosuch.csv`) or use an absolute one. A file whose
name begins with `-` or `+` is read as a flag, not a name: see Issue 14.

---

### Issue 2: Filter matches zero records

```bash
$ ssql from data.csv | ssql where -if age gt 30 | ssql count
0
```

Two things are *not* the cause. A misspelt field name stops the
pipeline with the fields that exist (Issue 3), and a literal of the wrong
kind for the field stops it too (Issue 12). So the zero is real, and the
question is what the values are:

```bash
ssql from data.csv | ssql describe | ssql to table                    # type, min/max, distinct count per field
ssql from data.csv | ssql group-by status -count n | ssql to table    # the actual values of a text field
```

Common causes: case (`Active` vs `active`), whitespace inside the value,
the wrong operator (`eq` where you meant `gt`). Normalise before you
compare:

```bash
ssql from data.csv | ssql update -set-expr status 'lower(trim(status))' | ssql where -if status eq active
```

---

### Issue 3: "where references unknown field(s)"

```
Error: where references unknown field(s): nonexistent (available: age, city, dept, hire_date, level, name, salary, status)
```

Field names are case-sensitive and come from the header; a header cell
with a trailing space keeps it. The message lists the fields that do
exist, and `ssql generate schema -pipeline '…' -data` shows them for any
stage of a pipeline. `update`, `sort`, `group-by`, `include` and the
other commands refuse unknown fields the same way, and `run -check`
(Quick Diagnostics) reports them before anything runs.

---

### Issue 4: Fewer records than expected

Count at each stage, then look at what the filter removed by inverting
it:

```bash
ssql from data.csv | ssql count
ssql from data.csv | ssql where -if status eq active | ssql count
ssql from data.csv | ssql where -not -if status eq active | ssql group-by status -count n | ssql to table
```

A missing value never equals anything; `ssql describe` reports the
missing count per field.

---

### Issue 5: GROUP BY has more groups than expected

```bash
ssql from data.csv | ssql group-by department -count n | ssql sort department | ssql to table
```

Near-duplicate keys (`Engineering` / `engineering` / `Engineering `)
show up as separate rows here, and `ssql describe`'s distinct count
confirms it. Normalise the key first:

```bash
ssql from data.csv | ssql update -set-expr department 'lower(trim(department))' | ssql group-by department -count n
```

A missing key makes a group of its own.

---

### Issue 6: A column that should be a number is text

`ssql describe` shows `string` for a numeric-looking column when a cell
in the leading sample was not a number (`N/A`, `unknown`, `-`), or when
the values are zero-padded identifiers (`02134`, `007`), which ssql keeps
as text so the zeros survive (as DuckDB does). If the numbers really are
numbers, say so at the source or later:

```bash
ssql from csv data.csv -type code int | …
ssql from data.csv | ssql cast -type code int -invalid missing | …
```

`cast` stops on a placeholder unless you say `-invalid missing` (Issue
11). The opposite mistake, comparing a number column with `abc`, is
Issue 12.

---

### Issue 7: Pipeline is slow, or runs out of memory

Time each stage by cutting the pipeline short (`time (ssql from huge.csv
| ssql where … | ssql count)`), then:

- **Read less while developing:** `ssql from csv huge.csv -sample 100000`
  or `-last 1000`. A Parquet source reads only the columns downstream
  stages use.
- **Stop early:** `ssql where … | ssql limit 100` ends the read once 100
  rows are through.
- **Save a stage:** `ssql tee filtered.jsonl` (Quick Diagnostics) so the
  expensive part runs once.
- **Sorts and group-bys larger than RAM:** `ssql sort -spill DIR -memory
  2G …` and `ssql group-by -spill DIR …` keep memory bounded.
- **Compile it:** `ssql generate go -run -pipeline '…'` runs the same
  pipeline as one parallel program; [Performance, Measured](performance.md)
  has the numbers.

Do not split a CSV with `split -l`: every chunk after the first loses
the header row and is misread.

---

### Issue 8: Empty output file

`ssql to csv out.csv` wrote only a header line: the stream that reached
it was empty. Put `ssql count` where the sink was, and see Issue 2.

### Issue 9: "field … first appears at record N, after the 1000-record sample"

**Symptoms:**
```
Error: field "refund_reason" first appears at record 48211, after the
1000-record sample the schema header was inferred from; …
```

**What it means:** JSON and JSONL records describe only themselves, and a
JSON `null` is an absent field. `ssql from` writes a `_schema` header for
the stages downstream, and that header is authoritative: sinks take their
columns from it. So `from` infers it from the first 1000 records (the
union of their fields, which is why a column that is NULL in the first row
is kept), and a field that turns up later would otherwise vanish from the
output without a word. ssql stops instead.

**Fix:** raise the sample so it covers the first occurrence — the message
gives the number — or make the field present earlier in the data:
```bash
SSQL_SCHEMA_SAMPLE=100000 ssql from jsonl events.jsonl | ssql to csv
```
`SSQL_SCHEMA_SAMPLE=1` restores first-record inference, for a live stream
whose first row must be emitted immediately. CSV, TSV and Parquet are not
affected: they carry their own header.

### Issue 10: "JSON Lines input: line N is not JSON"

**Symptoms:**
```
Error: JSON Lines input: line 4812 is not JSON (expected '{' at position 0): WARN retrying… — fix the input, or …
```

**What it means:** every ssql stage reads JSON Lines, one record per
line. A line that is not JSON used to be skipped without a word, which
loses records silently, so it is an error now, with the line number and
the start of the line.

**Fix:**
- A log or export with stray lines you know about: `ssql from jsonl FILE
  -skip-invalid` reads the good lines and reports how many it skipped.
- The message says **"this looks like a JSON ARRAY"**: the file is one
  `[ … ]`, not one object per line. Read it with `ssql from json FILE`
  (or `… | ssql from json -`), not by piping it into a stage.
- The line is between two ssql stages: that is a bug in ssql — please
  report the pipeline.
- **"line N is longer than 64 MB"**: one record is one line; a `group-by
  -collect` over a very large group can produce one this long.

### Issue 11: "cast: field … value … is not an int"

`cast` does not invent values: `N/A`, `unknown` or `-` in a column you
cast to a number stops the pipeline, naming the field and the value. If
the data really contains such placeholders, `ssql cast -type score int
-invalid missing` leaves them empty (never `0`) and reports the count.
An empty cell is already missing and is never an error.

### Issue 12: "field X is a number but "abc" is not"

A `-if FIELD OP VALUE` literal is read in the field's kind. A numeric
field against `abc` (or `3O` with a letter O), a bool against `maybe`,
a string operator (`contains`, `startswith`, `endswith`, `regex`) on a
number: these cannot be compared, so the pipeline stops rather than
matching nothing. `-if-field` over two kinds (a number against text) is
the same. A fractional literal on an int column and a numeric-looking
literal on a text column are fine (number and text comparison
respectively); an empty literal against a non-text field is simply false.

### Issue 13: "parameter X has the same name as a field" / "-param X: no expression in the clause uses it"

`-param NAME TYPE VALUE` binds NAME as a variable of the clause's
expressions. Two things are refused on purpose: a name that is also a
column of the input (rename the parameter; silently preferring either
would let a new upstream column change your expression), and a parameter
that no `-if-expr` or `-set-expr` in the same clause mentions (usually a
typo in the expression). Parameters are clause-scoped: one written before
`+` or `-` is not visible after it.

### Issue 14: A column or file whose name starts with `-` or `+`

A bare argument that begins with `-` or `+` is read as a flag, and a bare
`-` or `+` separates clauses, so `ssql include -total` fails with "unknown
flag" and `ssql include name -generate` does something else entirely.
Write the argument with `-arg`, which every command accepts:

```bash
ssql from -arg -weird.csv | ssql include -arg name -arg -total | ssql sort -arg -total -desc
```

`-arg VALUE` is exactly the bare argument, in order, whatever VALUE looks
like. **Programs that build pipelines from data should write every
positional this way**, so that no value can be read as a flag. Values of
ordinary flags (`where -if name eq -x`) never needed it: a flag's arguments
are taken by count. `generate sql` does not yet translate such names and
says so; direct execution, `generate go` and `generate ssql` do.

---

### Issue 15: `go install` fails with "package cmp is not in GOROOT"

**Symptoms:** `go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest`
prints several lines like
`package cmp is not in GOROOT (/usr/lib/go-1.18/src/cmp)`,
`package iter is not in GOROOT`, `package maps is not in GOROOT`, and no
binary appears in `~/go/bin`.

**Cause:** The `go` command is older than 1.21. From 1.21 on, `go`
reads the toolchain ssql needs from its module and downloads it; an
older `go` tries to compile ssql and its dependencies itself and cannot
find the standard-library packages added since. Ubuntu 22.04's
`golang-go` is Go 1.18 and Debian 12's is Go 1.19, so this is what
`sudo apt-get install golang-go` gives you there.

**Solution:**

```bash
go version              # 1.18 or 1.19 confirms it

# Ubuntu 22.04: the distribution also ships a newer Go
sudo apt-get install -y golang-1.22-go
export PATH="/usr/lib/go-1.22/bin:$PATH"     # add to ~/.bashrc too
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest

# Debian 12 and others: install Go from https://go.dev/dl/
# No Go at all: use the .deb or a release binary (doc/install.md)
```

The PATH line matters after the install too: `ssql generate go -build`
calls `go`, and only a 1.21+ `go` can dispatch to the downloaded 1.26
toolchain.

## Getting More Help

### Built-in help

```bash
# Command-specific help
ssql where -help
ssql group-by -help
ssql from -help

# General help
ssql -help
```

### Documentation

- [CLI Codelab](./cli-codelab.md) — the tutorial; section 2 explains the JSON Lines stream and its `_schema` header
- [Installing ssql](./install.md) — every install route, and the Go-version pitfalls
- [Expression Language](./EXPRESSIONS.md) — `-if-expr` / `-set-expr`, and what an expression error means
- [GitHub Issues](https://github.com/rosscartlidge/ssql/issues) — for anything this page does not answer
