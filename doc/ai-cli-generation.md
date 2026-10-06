# ssql CLI Pipeline Generation Prompt

*Paste this whole file into an LLM, then describe the pipeline you want in plain language.*

---

## System Prompt

```
You are an expert in ssql, a Unix-style CLI tool for tabular data. Generate correct ssql pipelines from natural language descriptions. ssql commands compose with Unix pipes (|) in a Source -> Transform -> Sink pattern. Use only the commands and flags in this reference; when unsure, say `ssql COMMAND -help` shows the authoritative flags.
```

---

## Pipeline Architecture

```
SOURCE -> TRANSFORM(s) -> SINK

ssql from data.csv | ssql where -if age gt 25 | ssql to table
 ^^ source            ^^ transform                 ^^ sink
```

**Three command categories:**

1. **Source** (`from`): reads a file, stdin, or command output; infers column types from a sample of rows
2. **Transform** (`where`, `update`, `sort`, `group-by`, …): reads records from stdin, writes records to stdout
3. **Sink** (`to …`, `count`, `tee`): writes a format to a file or stdout

**Rules:**
- Transform commands read ONLY from stdin; they never take a data file argument
- Every pipeline starts with `ssql from` (or receives stdin from another ssql pipeline)
- Between stages the data is JSON Lines with a `_schema` header line that carries field order and types; you never need to look at it, but it is why a pipeline can only be entered through `ssql from`
- Field names are case-sensitive; a misspelt field stops the pipeline with the list of fields that exist
- A missing value is *absent* (an empty CSV cell), never zero or `""`; comparisons with an absent value are false

---

## Command Reference

### Sources

| Command | Reads | Notes |
|---|---|---|
| `from FILE` | CSV, TSV, JSON, JSONL, Parquet, Arrow, XLSX by extension | the everyday form |
| `from csv [FILE]` / `from tsv [FILE]` | delimited text; stdin when no FILE | `-type FIELD TYPE` overrides an inferred type; `-sample N` reads a random N rows; `-last N` the last N; `-records` prints only the row count |
| `from json [FILE]` / `from jsonl [FILE]` | a JSON array / one object per line; stdin when no FILE | `from jsonl -skip-invalid` drops bad lines and reports the count |
| `from parquet FILE` / `from arrow FILE` / `from xlsx FILE` | columnar and spreadsheet files | `from xlsx -sheet NAME` |
| `from lines FILE` | raw text: one record per line with `line_number` and `line` | pair with `extract` |
| `from wav FILE` | audio samples: `sample`, `amplitude` | for the signal commands |
| `from ssh HOST PATH` | a remote file over SSH | `-- where … + group-by …` pushes stages to the host |
| `from catalog FILE.csv` | many shards listed in a catalog (host, path) | `-if date ge 2026-01-01` prunes shards by metadata |

### Transforms

| Command | Description | Key flags |
|---|---|---|
| `where` | filter | `-if FIELD OP VALUE`, `-if-expr 'EXPR'`, `-if-field FIELD OP OTHERFIELD`, `-not` (negate the clause), `-param NAME TYPE VALUE` (a variable for `-if-expr`); conditions in a clause AND; `+` separates OR clauses |
| `update` | set fields | `-set FIELD VALUE`, `-set-expr FIELD 'EXPR'`, `-set-field FIELD SOURCEFIELD` (copy value and type), `-set-bucket FIELD SOURCE WIDTH` (time bucket), with `-if …` conditions; `+` separates if/else-if/else clauses, first match wins |
| `group-by FIELDS…` | group and aggregate | `-count NAME`, `-sum F NAME`, `-avg F NAME`, `-min F NAME`, `-max F NAME`, `-first F NAME`, `-last F NAME`, `-collect F NAME`, `-count-distinct F NAME`, `-string-agg F SEP NAME`, `-median F NAME`, `-percentile F P NAME`, `-stddev F NAME`, `-variance F NAME`, `-mode F NAME`, `-arg-max F BY NAME`, `-arg-min F BY NAME`, `-expr NAME 'EXPR'`; `-rollup` / `-cube` add parent-level totals; `-presorted` streams input already sorted by the key; `-spill DIR` bounds memory |
| `sort FIELDS…` | sort | `-desc` (or `-asc`); `sort a -desc + b` sorts by a descending then b; `-spill DIR -memory 2G` sorts inputs larger than RAM |
| `top N -field F` | the N largest by F (a bounded heap; cheaper than sort + limit) | `-asc` for the smallest |
| `limit N` / `offset N` | first N / skip N | `limit -last N` keeps the last N |
| `sample N` | a random N rows (or `-percent P`) | `-seed S` for a repeatable sample |
| `distinct` | drop duplicate rows (whole row) | no flags |
| `include F…` / `exclude F…` | keep / drop columns | positional field names |
| `rename -as OLD NEW` | rename columns | repeat `-as` |
| `cast -type FIELD TYPE` | convert types (`string`, `int`, `float`, `bool`, `time`) | `-invalid missing` leaves unconvertible values empty instead of failing |
| `join FILE` | join with a file or `<(pipeline)` | `-using F` (same name both sides), `-on LEFT RIGHT`, `-as RIGHTFIELD NEWNAME`, `-type inner|left|right|full`, `-suffix _r` (for colliding names), `-exclude-left` / `-exclude-right`; `-asof TIMEFIELD [-tolerance 5m] [-after] [-strict]` for as-of joins; `-` separates several lookups from the same file |
| `except -file FILE` / `intersect -file FILE` | rows of stdin not in / also in FILE (SQL EXCEPT / INTERSECT) | `-using F` or `-on L R` compares by key (anti-join / semi-join); `-all` keeps duplicates |
| `union -file FILE` | append another file's rows (SQL UNION) | duplicates removed unless `-all`; repeat `-file` |
| `merge FILE…` | k-way merge of already sorted files | `-by FIELD` |
| `window` | SQL window functions, one output per input row | `-partition F -order F [-desc]`, frame `-preceding N -following N`; `-row-number NAME`, `-rank NAME`, `-dense-rank NAME`, `-ntile N NAME`, `-lag F N NAME`, `-lead F N NAME`, `-first F NAME`, `-last F NAME`, `-sum F NAME`, `-avg F NAME`, `-count NAME`, `-min F NAME`, `-max F NAME` |
| `pivot -row F -col F -val F -func FUNC` | cross-tab; `-func` is count, sum, avg, min or max | |
| `unpivot -id F… -value F…` | wide to long (melt) | `-col NAME -val NAME` name the two output columns |
| `fill` | fill missing values | `-down F` carries the last value forward; `-default F VALUE` |
| `extract -field F -re 'REGEX'` | named groups `(?P<name>…)` become fields | `-skip` drops non-matching rows; `-keep` keeps the source field |
| `resample -time F -every 5m -value F` | snap timestamps to a grid | `-fill previous|next|linear` |
| `describe` | one row per field: type, count, missing, distinct, min/max/mean/median | |
| `fft`, `ifft`, `convolve`, `correlate`, `spectrogram` | signal processing (see below) | |

### Sinks

| Command | Output |
|---|---|
| `to table` | aligned text table on stdout |
| `to csv [FILE]` / `to tsv [FILE]` | CSV / TSV to FILE or stdout |
| `to jsonl [FILE]` | one JSON object per line |
| `to json [FILE]` | a pretty-printed JSON array (not lines) |
| `to markdown` | a GitHub-flavored table |
| `to parquet FILE` / `to arrow FILE` / `to xlsx FILE` | columnar and spreadsheet files (Parquet needs a FILE) |
| `to chart -x F -y F -output FILE.html` | interactive HTML chart; `-type line|bar|scatter|pie|heatmap`, `-z F` for heatmaps; the file goes through `-output` (default `chart.html`), never a positional argument |
| `to explore FILE.html` | a self-contained data explorer page (`-wasm` embeds the engine so the page runs ssql itself) |
| `to animate` / `to wav FILE` | animated heatmap or histogram / audio |
| `count` | prints the number of rows |
| `tee FILE` | saves the stream to FILE (replay with `ssql from FILE`) and passes it on |

### Code generation and pipelines as data

| Command | Output |
|---|---|
| `generate go -pipeline 'PIPELINE'` | a standalone Go program for the pipeline; `-run` compiles and runs it; `-build BIN` writes a binary; `-mode typed` (default: struct types, parallel) or `-mode record`; `-explain` shows what the optimiser did |
| `generate go -package PKG -func NAME -pipeline 'PIPELINE' FILE.go` | an importable Go function instead of a program: rows in (`iter.Seq[Row]`), rows out (`iter.Seq2[Out, error]`), parameters as a struct, the sink dropped, a stage failure returned as the last element; `NAMEFromCSV(io.Reader, params)` for a CSV source |
| `generate sql -pipeline 'PIPELINE'` | DuckDB SQL (`-dialect postgres` or `datafusion`) |
| `generate ssql -pipeline 'PIPELINE'` | the pipeline rewritten with fewer stages; `-explain` says why |
| `generate json -pipeline 'PIPELINE'` | the pipeline as a JSON document for `ssql run` |
| `run FILE.json` | run a pipeline document with no shell; `-check` validates commands, flags and field names without running |

The pipeline string inside `-pipeline '…'` is exactly what you would type at the shell, including its sink.

---

## Critical Patterns

### 1. I/O

```bash
ssql from data.csv | ssql to csv output.csv          # CSV in, CSV out
ssql from data.jsonl | ssql to jsonl output.jsonl    # JSON Lines in and out
ssql from data.csv | ssql to json report.json        # a JSON array (pretty-printed)
ip -j addr | ssql from json | ssql to table          # a command's JSON on stdin
cat data.csv | ssql from csv | ssql to table         # CSV on stdin
ssql from data.csv | ssql to csv                     # no FILE: stdout
ssql from data.csv | ssql to table                   # the default way to look at a result
ssql from csv big.csv -sample 1000 | ssql to table   # a random 1000 rows while developing
```

### 2. Where

```bash
ssql where -if age gt 25              # gt ge lt le eq ne
ssql where -if status eq active
ssql where -if name contains Ali      # contains startswith endswith regex (text fields)
ssql where -if code regex '^[A-Z]{3}'
ssql where -if dept eq Sales -if age gt 30            # AND within a clause
ssql where -if dept eq Sales + -if dept eq Marketing  # OR between + clauses
ssql where -not -if status eq active                  # negate the clause
ssql where -if-field hire_date lt review_date         # compare two fields
ssql where -if-expr 'age > 25 and status == "active"' # expr-lang; and/or/not, functions
ssql where -param min int 40 -if-expr 'age > min'     # a typed parameter used by the expression
```

The literal in `-if FIELD OP VALUE` is read in the field's type: `age gt 30` compares numbers; `age gt abc` is an error, not "no rows". Values with spaces are quoted for the shell: `-if city eq "New York"`.

### 3. Update (if / else-if / else)

```bash
ssql update -set status done                                  # every row
ssql update -set-expr total 'price * quantity'                # computed
ssql update -if revenue gt 10000 -set tier premium \
  + -if revenue gt 1000 -set tier standard \
  + -set tier basic                                           # first matching clause wins; the last is the else
ssql update -if-expr 'amount > 1000' -set-expr discount 'amount * 0.1' + -set-expr discount 'amount * 0.05'
ssql update -set-field customer customer_name                 # copy a field (value and type)
ssql update -set-bucket minute ts 1m                          # time bucket for a later group-by
```

A literal string is `-set FIELD VALUE`. `-set-expr FIELD minor` would read `minor` as a field name; a string in an expression needs its own quotes: `-set-expr tier '"minor"'`.

### 4. Join

The right side is a file (CSV, TSV, JSON, JSONL by extension) or a pipeline in process substitution `<(ssql from … | ssql …)`; the left side is stdin.

```bash
ssql from orders.csv | ssql join customers.csv -using customer_id           # same field name both sides
ssql from users.csv  | ssql join orders.csv -on user_id customer_id         # different names
ssql from users.csv  | ssql join departments.csv -on dept_id id -as name dept_name   # rename a right field
ssql from orders.csv | ssql join customers.csv -using customer_id -type left        # keep unmatched orders
ssql from orders.csv | ssql join customers.csv -using customer_id -suffix _cust     # colliding names get a suffix
ssql from data.csv   | ssql join kinds.csv -on a_kind kind -as kind_name a_kind_name \
                                          - -on z_kind kind -as kind_name z_kind_name   # two lookups, `-` separates clauses
ssql from trades.csv | ssql join quotes.csv -using sym -asof ts -tolerance 5m       # the quote in force at each trade's time
ssql from orders.csv | ssql join <(ssql from customers.csv | ssql where -if tier eq gold) -using customer_id  # a filtered right side
```

A field present on both sides with different values is an error (a silent collision would lose data): rename with `-as`, suffix with `-suffix`, or drop a side's non-key fields altogether with `-exclude-left` / `-exclude-right`.

### 5. Group-by, rollup, window

```bash
ssql from sales.csv | ssql group-by region -count n                               # FIELDS are positional; aggregates name their result
ssql from sales.csv | ssql group-by region -count n -sum amount total -avg amount avg -min amount lo -max amount hi
ssql from sales.csv | ssql group-by region product -sum revenue total               # two grouping fields
ssql from logs.csv  | ssql group-by session -first url landing -last url exit -count-distinct url pages -string-agg url " > " path
ssql from data.csv  | ssql group-by dept -median salary med -percentile salary 0.9 p90 -stddev salary sd -arg-max name salary top_earner
ssql from sales.csv | ssql group-by region product -count n -sum revenue total -rollup    # + region_n, region_total, n, total on every row
ssql from sales.csv | ssql group-by region product -count n -cube                          # rollup plus every combination
ssql from orders.csv | ssql window -partition customer_id -order order_id -sum total running -row-number seq   # per-row, nothing collapses
ssql from sales.csv | ssql pivot -row region -col quarter -val revenue -func sum
```

The result field of an aggregate is the NAME you give it: after `-sum amount total` the field is `total`, so sort on `total`, not on `amount_sum`.

### 6. Set operations and deduplication

```bash
ssql from customers.csv | ssql except -file orders.csv -using customer_id       # customers with no order (anti-join)
ssql from customers.csv | ssql intersect -file orders.csv -using customer_id    # customers with at least one order (semi-join)
ssql from today.csv     | ssql except -file yesterday.csv                       # whole rows of today not in yesterday
ssql from a.csv         | ssql union -file b.csv                                # rows of both, duplicates removed (-all keeps them)
ssql from data.csv      | ssql distinct                                         # whole-row duplicates removed
ssql from data.csv      | ssql group-by email -first name name -count n         # one row per key with a count
```

### 7. Text, missing values, time

```bash
ssql from lines app.log | ssql extract -field line -re '^(?P<ts>\S+) (?P<level>\w+) (?P<msg>.*)$' -skip | ssql where -if level eq ERROR | ssql to table
ssql from sheet.csv | ssql fill -down region -default status unknown | ssql to table
ssql from data.csv | ssql cast -type code int -invalid missing | ssql to table
ssql from sensor.csv | ssql resample -time ts -every 5m -value temp -fill linear | ssql to table
ssql from data.csv | ssql describe | ssql to table       # what is in this file?
ssql from data.csv | ssql tee stage1.jsonl | ssql group-by dept -count n | ssql to table    # keep the intermediate result
```

### 8. Signal processing

```bash
ssql from sensor.csv | ssql fft -field voltage -rate 1000 | ssql to table             # frequency, magnitude
ssql from sensor.csv | ssql fft -field voltage -rate 1000 -phase | ssql to csv spectrum.csv
ssql from spectrum.csv | ssql ifft -magnitude magnitude -phase phase -output signal | ssql to csv
ssql from data.csv | ssql convolve -field value -kernel gaussian -size 11 -sigma 2.0 | ssql to csv
ssql from signals.csv | ssql correlate -field signal1 -with signal2 | ssql to csv       # two fields of the same records
ssql from signal.csv | ssql correlate -field value -auto -max-lag 100 | ssql to table   # autocorrelation
ssql from audio.csv | ssql spectrogram -field amplitude -window-size 1024 -hop 512 -rate 44100 -window-type hann | ssql to csv spectrogram.csv
```

### 9. Code generation

The pipeline is passed as one string; the sink stays in it. This is the form to prefer.

```bash
# Generate a Go program (typed structs, parallel by default) and keep the source
ssql generate go -pipeline 'ssql from data.csv | ssql where -if age gt 25 | ssql group-by dept -count n | ssql to table' > program.go
go run program.go

# Compile and run in one step
ssql generate go -run -pipeline 'ssql from data.csv | ssql where -if age gt 25 | ssql to csv out.csv'

# Record mode (dynamic schema, map-based rows) when asked for it
ssql generate go -pipeline '…' -mode record > program.go

# A library for a service: func Headcount(in, p) iter.Seq2[…, error], no main, no flags, no exit
ssql generate go -package reports -func Headcount -pipeline 'ssql from data.csv | ssql where -param min int 30 -if-expr "age > min" | ssql group-by dept -count n' reports/headcount.go

# SQL for DuckDB, or the pipeline rewritten by the optimiser
ssql generate sql  -pipeline 'ssql from data.parquet | ssql group-by dept -count n | ssql to csv' | duckdb
ssql generate ssql -explain -pipeline 'ssql from data.csv | ssql sort -desc x | ssql limit 5 | ssql to table'
```

The older form runs the pipeline with `SSQL_MODE` exported so every stage emits code instead of running; it still works and `ssql generate go` reads the fragments from stdin:

```bash
(export SSQL_MODE=typed; ssql from data.csv | ssql where -if age gt 25 | ssql to table) | ssql generate go -run
```

`SSQL_MODE=record` selects record mode there; `SSQL_MODE=parallel` is a deprecated alias of `typed`. Without the `export` (or the subshell) only the first command sees the variable and the pipeline runs instead of generating.

---

## Anti-Patterns

### Commands and flags that do not exist

| Wrong | Correct |
|---|---|
| `read-csv FILE`, `write-csv FILE`, `write-json FILE` | `from FILE`, `to csv FILE`, `to jsonl FILE` |
| `-match FIELD OP VALUE` | `-if FIELD OP VALUE` |
| `where -expr '…'` | `where -if-expr '…'` |
| `group-by -field dept` | `group-by dept` (fields are positional) |
| `sort -field age` | `sort age` |
| `distinct -field email` | `distinct` has no flags; use `group-by email -first …` for one row per key |
| `correlate -field-a x -field-b y` | `correlate -field x -with y` |
| `to chart -x a -y b chart.html` | `to chart -x a -y b -output chart.html` |
| `to json out.jsonl` for JSON Lines | `to jsonl out.jsonl` (`to json` writes a JSON array) |
| `join -on FIELD` (same name) | `join -using FIELD` |
| `join -left-field a -right-field b` / `-right FILE` | `join FILE -on a b` |
| `union -distinct` | `union` already removes duplicates; `-all` keeps them |
| `ssql group …` | `ssql group-by …` |

### Transforms never take a data file

```bash
ssql where data.csv -if age gt 25          # NO
ssql from data.csv | ssql where -if age gt 25
```

### Sorting on a name the aggregate did not produce

```bash
ssql group-by user -sum amount total | ssql sort amount_sum -desc    # NO: the field is `total`
ssql group-by user -sum amount total | ssql sort -desc total
```

### Shell redirection instead of a sink

```bash
ssql from data.csv | ssql where -if age gt 25 > out.json     # NO: raw wire format with a _schema line
ssql from data.csv | ssql where -if age gt 25 | ssql to jsonl out.jsonl
```

### Code generation without the sink or without export

```bash
SSQL_MODE=record ssql from data.csv | ssql where -if age gt 25 | ssql generate go   # NO: only `from` sees the variable
ssql generate go -pipeline 'ssql from data.csv | ssql where -if age gt 25 | ssql to table' > program.go
```

---

## Complete Examples

### Example 1: Employee analysis

**Task**: Departments with more than 10 employees earning over 80,000, from employees.csv

```bash
ssql from employees.csv \
  | ssql where -if salary gt 80000 \
  | ssql group-by department -count n \
  | ssql where -if n gt 10 \
  | ssql sort -desc n \
  | ssql to table
```

### Example 2: Classification

**Task**: Classify each order in orders.csv as large (amount > 1000), medium (> 100) or small, and summarise by class

```bash
ssql from orders.csv \
  | ssql update -if amount gt 1000 -set size large + -if amount gt 100 -set size medium + -set size small \
  | ssql group-by size -count n -sum amount total -avg amount avg \
  | ssql to table
```

### Example 3: Join and aggregate

**Task**: Total spending per user, joining users.csv with orders.csv

```bash
ssql from users.csv \
  | ssql join orders.csv -using user_id \
  | ssql group-by user_id name -sum amount total -count orders \
  | ssql sort -desc total \
  | ssql to table
```

### Example 4: Anti-join

**Task**: Customers in customers.csv who have never ordered (orders.csv)

```bash
ssql from customers.csv | ssql except -file orders.csv -using customer_id | ssql to table
```

### Example 5: Running total

**Task**: Each order with the running total of `total` per customer, in order_id order

```bash
ssql from orders.csv \
  | ssql window -partition customer_id -order order_id -sum total running_total \
  | ssql to table
```

### Example 6: Frequency analysis

**Task**: The 20 strongest frequencies in a 1 kHz sensor signal

```bash
ssql from sensor_data.csv \
  | ssql fft -field voltage -rate 1000 \
  | ssql top 20 -field magnitude \
  | ssql to table
```

### Example 7: Spectrogram

```bash
ssql from audio.csv | ssql spectrogram -field amplitude -window-size 2048 -rate 44100 | ssql to csv spectrogram.csv
```

### Example 8: CSV in, JSON Lines out

**Task**: The 100 active rows with the highest revenue from report.csv, as JSON Lines

```bash
ssql from report.csv \
  | ssql where -if-expr 'revenue > 0 and status == "active"' \
  | ssql include name revenue status \
  | ssql top 100 -field revenue \
  | ssql to jsonl top_active.jsonl
```

### Example 9: Two lookups from one reference file

```bash
ssql from data.csv \
  | ssql join reference.csv -on source_type type -as description source_desc \
                          - -on dest_type type -as description dest_desc \
  | ssql to csv enriched.csv
```

### Example 10: Code generation

**Task**: A standalone Go program for the top 10 products by revenue in the North region

```bash
ssql generate go -pipeline 'ssql from sales.csv | ssql where -if region eq North | ssql group-by product -sum revenue total -count n | ssql top 10 -field total | ssql to table' > top_products.go
go run top_products.go
```

### Example 11: Logs

**Task**: Count ERROR lines per hour in app.log, whose lines look like `2026-09-29T10:15:02Z ERROR something`

```bash
ssql from lines app.log \
  | ssql extract -field line -re '^(?P<ts>\S+) (?P<level>\w+) (?P<msg>.*)$' -skip \
  | ssql where -if level eq ERROR \
  | ssql cast -type ts time \
  | ssql update -set-bucket hour ts 1h \
  | ssql group-by hour -count n \
  | ssql to table
```

---

## Pattern Recognition

| Intent | ssql |
|---|---|
| read / load / open | `ssql from FILE` |
| what fields are there / profile | `ssql describe` |
| filter / only / where | `ssql where -if FIELD OP VALUE`; expression: `-if-expr 'EXPR'` |
| not / exclude rows | `ssql where -not -if …` or `ne` |
| update / set / change | `ssql update -set FIELD VALUE` |
| compute / calculate | `ssql update -set-expr FIELD 'EXPR'` |
| if X then Y else Z | `ssql update -if … -set … + -set …` |
| group by / per / by | `ssql group-by FIELD…` |
| count / total / average / min / max | `-count N`, `-sum F N`, `-avg F N`, `-min F N`, `-max F N` on group-by |
| median / percentile / stddev / most common | `-median`, `-percentile F P N`, `-stddev`, `-mode` |
| who has the highest … | `-arg-max FIELD BY NAME` |
| subtotals / grand total | `group-by … -rollup` (or `-cube`) |
| sort / order by | `ssql sort FIELD` or `ssql sort -desc FIELD` |
| top N / largest N | `ssql top N -field F` (smallest: `-asc`) |
| first N / skip N / last N | `ssql limit N` / `ssql offset N` / `ssql limit -last N` |
| a random sample | `ssql sample N` or `from csv FILE -sample N` |
| join / combine / look up | `ssql join FILE -using F` or `-on L R`; keep unmatched: `-type left` |
| as of / latest before / most recent quote | `ssql join FILE -using KEY -asof TIME` |
| not in / never / missing from | `ssql except -file FILE -using KEY` |
| also in / at least one | `ssql intersect -file FILE -using KEY` |
| union / append / stack | `ssql union -file FILE` |
| keep columns / select columns | `ssql include F…` |
| drop columns | `ssql exclude F…` |
| rename | `ssql rename -as OLD NEW` |
| unique rows / deduplicate | `ssql distinct` |
| convert type / to number / to date | `ssql cast -type F int|float|time` |
| running total / rank / previous row | `ssql window -partition … -order … -sum F N` / `-rank N` / `-lag F 1 N` |
| pivot / cross-tab | `ssql pivot -row F -col F -val F -func sum` |
| melt / wide to long | `ssql unpivot -id F… -value F…` |
| fill blanks / carry forward | `ssql fill -down F` / `-default F V` |
| parse log lines / regex fields | `ssql from lines FILE \| ssql extract -field line -re '…'` |
| per minute / per hour buckets | `ssql update -set-bucket minute ts 1m \| ssql group-by minute …` |
| regular time grid / interpolate | `ssql resample -time F -every 1m -value F -fill linear` |
| table on screen | `ssql to table` |
| CSV / TSV | `ssql to csv [FILE]` / `ssql to tsv [FILE]` |
| JSON Lines | `ssql to jsonl [FILE]` |
| JSON array | `ssql to json [FILE]` |
| Parquet / Excel | `ssql to parquet FILE` / `ssql to xlsx FILE` |
| chart | `ssql to chart -x F -y F -output FILE.html` |
| save intermediate result | `ssql tee FILE.jsonl` |
| how many rows | `ssql count` |
| FFT / frequencies | `ssql fft -field F -rate N` |
| smooth | `ssql convolve -field F -kernel gaussian -size N` |
| spectrogram | `ssql spectrogram -field F -window-size N -rate N` |
| generate Go / compile / fast version | `ssql generate go -pipeline '…'` (`-run` to execute) |
| generate SQL | `ssql generate sql -pipeline '…'` |
| optimise the pipeline | `ssql generate ssql -explain -pipeline '…'` |

---

## Validation Checklist

A generated pipeline should have:
- `ssql from` first, `|` between every pair of commands, a sink last (`to …`, `count` or `tee`)
- transforms reading stdin only (no data file after `where`, `sort`, …)
- current flag names (`-if`, `-if-expr`, `-using`, `-on L R`, `-output` for charts)
- aggregate results referred to by the name given (`-sum amount total` → `total`)
- `+` between update/where clauses, `-` between join clauses
- for code generation, `ssql generate go -pipeline '…'` with the sink inside the string

---

*`ssql COMMAND -help` is the authority on every flag; `ssql functions` lists the expression functions.*
