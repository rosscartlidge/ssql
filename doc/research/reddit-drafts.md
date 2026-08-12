# Reddit Post Drafts

Reference: DFC078
Created: 2026-04-08
Last modified: 2026-04-08

[Back to Index](./README.md)

Post 1-2 days after HN. Each community gets a different angle. Don't cross-post identical content.

---

## r/golang

**Title:** I built a stream processing library that generates optimized Go code from CLI pipelines

**Body:**

I've been working on **ssql** — a Go library and CLI for stream processing structured data (CSV, JSON, Parquet, Arrow, XLSX).

The idea: prototype with Unix-style pipelines, then compile to standalone Go code.

```
ssql from data.csv | ssql where -if age gt 25 | ssql group-by dept -count n | ssql to table
```

When you're happy with the pipeline, `generate go` compiles it to a Go program with CLI flags:

```
(export SSQLGO=1; ssql from data.csv | ssql where -if age gt 25 | ssql group-by dept -count n | ssql to table) | ssql generate go > main.go
go build -o analyze main.go
./analyze -input production.csv -age-gt 30
```

The pipeline optimizer rewrites before compiling — merges redundant filters, pushes predicates into SSH connections, collapses sort+limit into top-N. You can see what it does with `generate ssql -explain`.

**Some Go-specific things that might interest this community:**

- Built on Go 1.23+ `iter.Seq[T]` iterators and generics — the library uses `Filter[T,U]` composition (`func(iter.Seq[T]) iter.Seq[U]`)
- The `ssql` Go package works independently of the CLI — use it as a library for data pipelines with type-safe records, composable filters, and window functions
- The CLI framework (`autocli`) uses a fluent builder API that generates help, man pages, and bash completion from a single definition
- GPU acceleration via CUDA for FFT/convolution (320x for convolution, 21x for FFT)
- `generate sql` produces DuckDB-compatible SQL from the same pipeline

**Try it without installing:** https://rosscartlidge.github.io/ssql/playground.html

**Install:**
```
brew tap rosscartlidge/ssql && brew install ssql
```
or `go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest`

**GitHub:** https://github.com/rosscartlidge/ssql

Happy to answer questions about the iterator design, the code generation system, or how the optimizer works.

---

## r/commandline

**Title:** ssql: like awk/jq for structured data, with an automatic pipeline optimizer

**Body:**

I built **ssql** — a set of Unix-style commands for processing CSV, JSON, and other structured data. Each command does one thing, they compose with standard pipes, and they have tab completion for field names.

```bash
# Filter, aggregate, display
ssql from data.csv | ssql where -if age gt 25 | ssql group-by dept -count n | ssql to table

# Multiple files in parallel (4x faster than sequential)
ssql from csv *.csv -- where -if status eq active | ssql to table

# Window functions without collapsing rows
ssql from sales.csv | ssql window -sum revenue running_total -partition dept -order date

# Read from remote hosts — filters run on the remote side
ssql from ssh myserver /data/logs.csv -- where -if level eq ERROR | ssql to table
```

The part that makes it different from awk/jq/csvkit/miller: **it has a pipeline optimizer.**

```bash
# Naive pipeline with redundant steps:
ssql from data.csv | ssql where -if age gt 25 | ssql where -if dept eq Eng | ssql sort salary -desc | ssql limit 3

# Optimizer rewrites to:
ssql from data.csv | ssql where -if dept eq Eng -if age gt 25 | ssql top 3 -field salary
```

It merges filters, reorders predicates, collapses sort+limit to top-N, and pushes operations into SSH connections so filtering happens on the remote host.

You can also compile the pipeline to Go code (`generate go`) or DuckDB SQL (`generate sql`).

**Tab completion** works for commands, flags, field names (reads the CSV header), operators, and even field values. Type `ssql from data.csv | ssql where -if <TAB>` and it shows the column names.

**Try it in the browser:** https://rosscartlidge.github.io/ssql/playground.html

**Install:** `brew tap rosscartlidge/ssql && brew install ssql`

**GitHub:** https://github.com/rosscartlidge/ssql

---

## r/dataengineering

**Title:** ssql: CLI data processing with SSH pushdown, catalog-based distributed merges, and DuckDB SQL generation

**Body:**

I built **ssql** for the kind of data work that's too small for Spark but too complex for awk — processing CSV/JSON/Parquet across multiple servers with filtering, aggregation, and joins.

**The distributed story:**

```bash
# Read from a remote host — filter runs on the remote side (SSH pushdown)
ssql from ssh node1 /data/events.csv -- where -if status ge 500 | ssql to table

# Catalog: list your shards in a CSV, ssql connects to each
ssql from catalog shards.csv -if date ge 2025-03-01 -- where -if level eq ERROR | ssql to table

# K-way merge across distributed shards (streaming, O(K) memory)
ssql merge -catalog shards.csv -by timestamp -- where -if service eq api | ssql to table
```

Catalog paths support globs — new files are picked up automatically without editing the catalog:
```csv
host,path
node1,/data/logs-2025-*.csv
node2,/data/logs-2025-*.csv
```

**The pipeline optimizer** automatically pushes predicates into SSH connections and catalogs:
```bash
# You write:
ssql from catalog shards.csv | ssql where -if level eq ERROR | ssql group-by service -count n

# Optimizer rewrites to:
ssql from catalog shards.csv -- where -if level eq ERROR + group-by service -count n
# Each shard filters and aggregates locally, only summary data comes back
```

**DuckDB bridge:** `generate sql` converts any pipeline to DuckDB-compatible SQL. Multi-file reads use `read_csv_auto([...])`. Good for when you need DuckDB's execution speed on the same data.

**Other features:**
- Window functions (ROW_NUMBER, LAG, running totals)
- Parquet/Arrow support with column pruning
- Interactive charts (`to chart` creates Chart.js HTML)
- GPU acceleration for signal processing (FFT, convolution)
- Tab completion for field names, operators, and values
- Compiles pipelines to standalone Go code (`generate go`)

**Try it:** https://rosscartlidge.github.io/ssql/playground.html

**Install:** `brew tap rosscartlidge/ssql && brew install ssql`

**GitHub:** https://github.com/rosscartlidge/ssql
