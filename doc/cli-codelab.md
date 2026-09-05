# ssql CLI Codelab

A guided path through `ssql`, the Unix-pipeline data tool. Every command
below runs against the small files in `doc/codelab-data/` — and every
block in this document *is* run, by `scripts/codelab-run.sh`, so what you
read is what happens (DFC125).

**How to use it:** Part 1 gets you doing useful things in about ten
minutes; each block answers a question about the data, and you should
see the answer. Part 2 adds the sophisticated features one at a time,
each introduced by the problem it solves. Type the commands yourself
inside tmux — the completion and help popups are half the experience.

## Table of Contents

**Part 1 — Ten minutes to useful**
1. [Setup](#1-setup)
2. [Look at a file](#2-look-at-a-file)
3. [Answer questions](#3-answer-questions)
4. [Save and share](#4-save-and-share)

**Part 2 — Going further**
5. [Time series and signals](#5-time-series-and-signals)
6. [Make it fast](#6-make-it-fast)
7. [Generate code](#7-generate-code)
8. [Distributed data](#8-distributed-data)
9. [Reference](#9-reference)

---

# Part 1 — Ten minutes to useful

## 1. Setup

Install (Go 1.23+), fetch the codelab data, and step into it:

```bash
# codelab: skip — installation and clone (run once by hand)
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest
git clone https://github.com/rosscartlidge/ssql.git
cd ssql/doc/codelab-data
```

Now the most important line in this tutorial. ssql is designed to be
*discovered from the prompt*: Tab completes commands, flags, field names
and even field values from your data; Ctrl-O completes fields across a
whole pipeline; Alt-h explains whatever is under your cursor. Turn it on
in the shell you're using:

```bash
# codelab: skip — shell setup for your interactive session (add to ~/.bashrc)
eval "$(ssql -shell-init)"
```

**Do this inside tmux.** In tmux the completions and help open as
transient popups over your command line (`tmux display-popup`) and
vanish when you pick; outside tmux they print inline below the prompt,
which works but is noisier. Start `tmux`, run the `eval` line, and try:

```
ssql <TAB>                                 # every command
ssql from employees.csv | ssql where -if <TAB>      # the file's FIELD NAMES
ssql from employees.csv | ssql where -if dept eq <TAB>   # its VALUES
ssql from employees.csv | ssql group-by dept -sum salary<Alt-h>   # what is this flag?
```

From here on, whenever a flag or field name is mentioned, remember you
never have to type it from memory.

Check what you have:

```bash
ssql version
ls
```

## 2. Look at a file

`from` reads a file (format from the extension), `to table` prints it:

```bash
ssql from employees.csv | ssql to table
```

Ten people, eight fields. Before anything else, ask the file to describe
itself — one row per field with type, count, missing values, distinct
values, and the numeric spread:

```bash
ssql from employees.csv | ssql describe | ssql to table
```

`describe` is the first thing to run on *any* unfamiliar file. Then the
everyday moves — take a few rows, filter, sort, pick columns:

```bash
# Who works in Engineering?
ssql from employees.csv | ssql where -if dept eq Engineering | ssql to table
```

```bash
# Highest paid first
ssql from employees.csv | ssql sort -desc salary | ssql include name dept salary | ssql to table
```

```bash
# The three most recent hires (sort by date, keep the last 3)
ssql from employees.csv | ssql sort hire_date | ssql limit -last 3 | ssql to table
```

`limit N` is the first N rows; `limit -last N` is the tail. Conditions
compose: several `-if` in one `where` are AND; separate groups with `+`
for OR; `-if-expr` takes a full expression when the flag form runs out:

```bash
# Engineers over 30, OR anyone in Chicago
ssql from employees.csv | ssql where -if dept eq Engineering -if age gt 30 + -if city eq Chicago | ssql to table
```

```bash
# Expressions: string functions, arithmetic, comparisons
ssql from employees.csv | ssql where -if-expr 'salary / age > 2500 && status == "active"' | ssql include name age salary | ssql to table
```

Operators for `-if`: `eq ne gt ge lt le contains startswith endswith regex`.
Everything in the pipe is JSONL, so any stage can follow any other, and
`to csv`/`to json` swap the output shape at the end:

```bash
ssql from employees.csv | ssql where -if city eq SF | ssql include name salary | ssql to csv
```

## 3. Answer questions

Aggregation is `group-by FIELDS` plus one flag per aggregate, each naming
its result column:

```bash
# Headcount, average and top salary per department
ssql from employees.csv | ssql group-by dept -count n -avg salary avg_salary -max salary top_salary | ssql sort -desc avg_salary | ssql to table
```

```bash
# Top 3 earners without sorting everything (a heap, O(3) memory)
ssql from employees.csv | ssql top 3 -field salary | ssql include name salary | ssql to table
```

```bash
# Distinct cities, and how many rows survive a filter
ssql from employees.csv | ssql include city | ssql distinct | ssql to table
ssql from employees.csv | ssql where -if status eq active | ssql count
```

Two files that share a key join with `join FILE -using KEY`. `orders.csv`
has one order for a customer who does not exist — an inner join drops it,
`-type left` keeps it with the customer fields empty:

```bash
ssql from orders.csv | ssql join customers.csv -using customer_id | ssql include order_id name country amount | ssql to table
```

```bash
# Revenue per country, shipped orders only
ssql from orders.csv | ssql where -if status eq shipped | ssql join customers.csv -using customer_id | ssql group-by country -sum amount revenue -count orders | ssql sort -desc revenue | ssql to table
```

Reshape when the shape is the problem. `pivot` turns a value column into
columns; `unpivot` is its inverse (the SQL name for "melt"):

```bash
# Amount by product and status, as a grid
ssql from orders.csv | ssql pivot -row product -col status -val amount -func sum | ssql to table
```

```bash
# Quarter columns want to be rows before you can group or chart them
ssql from sales_wide.csv | ssql unpivot -id product -col quarter -val revenue | ssql group-by quarter -sum revenue total | ssql sort quarter | ssql to table
```

Real files are ragged. A spreadsheet export writes the region only on
the first row of its block; `fill` carries values down and defaults the
missing (an empty cell *is* missing — never a phantom zero):

```bash
ssql from sheet.csv | ssql fill -down region -default sales 0 | ssql group-by region -sum sales total -count stores | ssql to table
```

And text becomes data with `from lines` + `extract`: named regex groups
become fields, lines that don't match fail loudly unless you `-skip`:

```bash
ssql from lines app.log | ssql extract -field line -re '^(?P<ts>\S+) (?P<lvl>\w+) (?P<msg>.*)$' -skip | ssql to table
```

```bash
# Captures are strings on purpose — cast when you need numbers
ssql from lines app.log | ssql extract -field line -re '^(?P<ts>\S+) (?P<lvl>\w+)' -skip | ssql group-by lvl -count n | ssql sort -desc n | ssql to table
```

Derive new columns with `update`; `rename` and `cast` tidy names and
types:

```bash
ssql from employees.csv | ssql update -set-expr band 'salary > 90000 ? "high" : "standard"' | ssql group-by band -count n -avg age avg_age | ssql to table
```

```bash
ssql from employees.csv | ssql rename -as dept department | ssql cast -type level float | ssql include name department level | ssql limit 3 | ssql to table
```

That is the everyday toolkit. Everything in Part 2 makes these same
pipelines faster, bigger, or shareable — the vocabulary does not change.

## 4. Save and share

Any pipeline ends in a format. `tee` saves a checkpoint *and* passes the
rows on, so you can keep going:

```bash
ssql from employees.csv | ssql where -if dept eq Engineering | ssql tee /tmp/engineers.jsonl | ssql group-by city -count n | ssql to table
head -2 /tmp/engineers.jsonl
```

```bash
ssql from employees.csv | ssql group-by dept -avg salary avg_salary | ssql to json
```

Charts are just another sink — the same pipeline, plus what to plot:

```bash
ssql from employees.csv | ssql group-by dept -avg salary avg_salary | ssql to chart -type bar -x dept -y avg_salary -output /tmp/salary_by_dept.html
```

Open `/tmp/salary_by_dept.html` in a browser. For an interactive view of
a whole dataset — a grid with a pipeline bar, completion, charts, and the
same engine running in the page — serve the directory and open the
workspace:

```bash
# codelab: skip — long-running server; run it in a second terminal
ssql serve -listen-http 127.0.0.1:8080 -dir .
# then open http://127.0.0.1:8080
```

The workspace's pipeline bar speaks exactly this codelab's language;
clicking a column header inserts a `sort` stage, choosing chart axes
writes a `to chart` stage, and **Copy CLI** hands back the pipeline as
text. Nothing you do there is a separate feature — it is these commands.

---

# Part 2 — Going further

## 5. Time series and signals

*Why:* sensor and log data arrive at irregular moments; charts and
joins want a regular grid.

`sensor.csv` has readings every 7–23 seconds. `resample` snaps them to a
regular, epoch-aligned grid and fills the gaps (carry the previous value
by default; `-fill linear` interpolates):

```bash
ssql from sensor.csv | ssql resample -time ts -every 30s -value temp -value rpm | ssql limit 6 | ssql to table
```

Downsampling is a composition, not a second vocabulary: bucket the
timestamp, then group:

```bash
ssql from sensor.csv | ssql update -set-expr minute 'bucket(ts, "1m")' | ssql group-by minute -avg temp avg_temp -max rpm max_rpm | ssql sort minute | ssql to table
```

Window functions rank, lag, and run totals *without collapsing rows*:

```bash
# Salary rank within each department
ssql from employees.csv | ssql window -partition dept -order salary -desc -rank rank | ssql include dept name salary rank | ssql sort dept rank | ssql to table
```

```bash
# Change since the previous reading — the first row has no previous, so guard it
ssql from sensor.csv | ssql window -order ts -lag temp 1 prev_temp | ssql where -if-expr 'prev_temp != nil' | ssql update -set-expr delta 'temp - prev_temp' | ssql include ts temp delta | ssql limit 5 | ssql to table
```

(Without the guard, `update` stops loudly at row one: `float64 - <nil>`.
ssql never invents a value for a missing field.)

Signal processing is built in. `signal.csv` is a 5 Hz + 20 Hz mix sampled
at 100 Hz; the FFT finds both:

```bash
ssql from signal.csv | ssql fft -field amplitude -rate 100 | ssql top 2 -field magnitude | ssql include frequency magnitude | ssql to table
```

`spectrogram`, `convolve`, `correlate`, and `from wav` continue from
here — see [doc/cli-signal-processing.md](cli-signal-processing.md).

## 6. Make it fast

*Why:* the pipelines above are correct on a gigabyte too — but you
shouldn't have to read a gigabyte to look at it.

Ask the source, not the pipeline. These read only what they must:

```bash
# Row count from the parquet footer / a newline scan — no parsing
ssql from employees.parquet -records
ssql from csv employees.csv -records
```

```bash
# The last N rows by seeking to the end of the file (0.01s on 14M rows)
ssql from csv employees.csv -last 3 | ssql include name hire_date | ssql to table
```

```bash
# A fast approximate sample via byte-offset seeks, and only the columns you need
ssql from csv employees.csv -sample 3 -sample-seed 7 | ssql include name | ssql to table
ssql from parquet employees.parquet -columns name salary | ssql limit 3 | ssql to table
```

The rule behind these flags: a flag lives on `from` only when the
*source* can do something the pipe stage cannot (seek, read a footer,
prune a column). `limit` is already lazy, so there is no `-limit`.

For the heavy path, ssql compiles your pipeline to a typed Go program
that runs in parallel — the workspace's ⚡ button does this for the
server-side head, and the next section shows it from the command line.

## 7. Generate code

*Why:* the interpreted pipeline is convenient; a compiled one is fast and
standalone. Same pipeline, one command:

```bash
ssql generate go -run -pipeline 'ssql from employees.csv | ssql where -if dept eq Engineering | ssql group-by city -count n | ssql to table'
```

`generate go` optimises the pipeline before compiling it — the same
rewrites `generate ssql` shows you (column pruning on parquet, predicate
pushdown, dead-sort elimination). The program's header comment records
both the pipeline you typed and the one it implements; `+O` turns the
optimiser off.

That generated a Go program, compiled it, and ran it. `-mode typed` (the
default) uses per-column Go structs and parallel readers where the
pipeline allows; `-mode record` keeps the dynamic record model. Look at
the program instead of running it:

```bash
ssql generate go -pipeline 'ssql from employees.csv | ssql where -if salary gt 90000 | ssql to csv' | head -40
```

The same fragments translate to SQL. DuckDB runs it; results are
byte-identical to the interpreted pipeline — a gate in the test suite
checks every lane agrees:

```bash
ssql generate sql -pipeline 'ssql from employees.csv | ssql where -if status eq active | ssql group-by dept -avg salary avg_salary | ssql to table'
```

```bash
# codelab: skip — needs duckdb on PATH
ssql generate sql -run -pipeline 'ssql from employees.csv | ssql group-by dept -avg salary avg_salary | ssql to table'
```

And ssql can rewrite your pipeline into a better one — merging filters,
turning sort+limit into `top`, removing a sort that a later sort makes
pointless:

```bash
ssql generate ssql -explain -pipeline 'ssql from employees.csv | ssql where -if age gt 30 | ssql where -if status eq active | ssql sort -desc salary | ssql limit 3 | ssql to table'
```

In the workspace this is the **optimise** button; at the prompt, Alt-g
shows the typed Go for the line you're editing and Alt-r compiles and
runs it (both `-shell-init` keys; popups in tmux):

```
ssql from employees.csv | ssql where -if age gt 30 | ssql to table  <Alt-g>
ssql from employees.csv | ssql where -if age gt 30 | ssql to table  <Alt-r>
#   [ssql: compiled in 1.4s, ran in 8ms]
```

*Under the hood:* each command, run with `SSQL_MODE=record|typed`, emits
a code fragment instead of data, and `generate` assembles the stream.
`-pipeline` does that plumbing for you; you only need the raw form
(`export SSQL_MODE=typed; … | ssql generate go`) when scripting it.

## 8. Distributed data

*Why:* the data is often on another machine, or in many files. The
pipeline does not change; where it runs does.

Several files at once — each read in parallel, `-source` tags the origin:

```bash
ssql from csv employees.csv employees.csv -source file | ssql group-by file -count n | ssql to table
```

Push work *into* the reader with `--`: the stages after it run per
file, before the streams merge. Locally that is parallelism; over SSH it
is the difference between shipping a filter and shipping a file:

```bash
ssql from csv employees.csv -- where -if dept eq Sales | ssql to table
```

```bash
# codelab: skip — needs an SSH host with ssql installed
ssql from ssh node1 /data/events.csv -- where -if status ge 500 | ssql group-by service -count n | ssql to table
```

A *catalog* is a CSV of shards (host, path, metadata); `from catalog`
prunes shards by metadata, fans out over SSH, and merges — and the
optimiser from section 7 pushes your `where` and `group-by` into the
shards for you (`generate ssql` shows the rewrite). Run it when you have
more than one machine: [doc/cli-debugging.md](cli-debugging.md) covers
the rig.

```bash
# codelab: skip — needs a shard catalog and SSH hosts
ssql from catalog shards.csv -if date ge 2026-01-01 | ssql where -if status ge 500 | ssql group-by service -count n | ssql to table
```

The SSH operator console is the other direction — leave the data where
it is and log into it: `ssql serve DATA.csv` loads a dataset and answers
over SSH with this same vocabulary (`from-loaded | where … | to table`).
Runbook: [The SSH Operator Console](cli-codelab-serve.md).

## 9. Reference

Sources: `from FILE` (csv/tsv/json/jsonl/parquet/arrow/xlsx/wav by
extension; `.log`/`.txt` as lines) · `from csv|tsv|jsonl|parquet|lines FILE…`
· `from ssh HOST PATH` · `from catalog FILE` — flags `-records`,
`-sample N`, `-last N`, `-columns` (parquet), `-source`, `--` pushdown.

Filter / shape: `where` · `include` · `exclude` · `rename` · `cast` ·
`update` · `distinct` · `limit [-last]` · `offset` · `sample` · `top`.

Aggregate / reshape: `group-by` · `count` · `describe` · `pivot` ·
`unpivot` · `fill` · `extract` · `window` · `resample` · `join` · `union`
· `merge`.

Signals: `fft` · `ifft` · `convolve` · `correlate` · `spectrogram`.

Sinks: `to table|csv|tsv|json|jsonl|parquet|arrow|xlsx|wav|chart|explore|animate` · `tee FILE`.

Codegen & serving: `generate go|sql|ssql [-pipeline '…']` · `serve`.

Every command answers `ssql CMD -help` and `ssql CMD -man`; expression
functions are listed by `ssql functions`; `ssql conventions` explains
the shared rules (types, missing values, field order). The workspace,
the SSH console, and the CLI are one vocabulary.

```bash
ssql where -help | head -20
ssql functions | head -12
```

Next on the [learning path](README.md#learning-path): the
[Signal Processing](cli-signal-processing.md) branch if your data is a
time series; otherwise the [Getting Started Guide](codelab-intro.md) — the
Go library the CLI is built on, and what `generate go` produced in §7.
[ai-code-generation.md](ai-code-generation.md) has an LLM write pipelines
for you; `doc/research/` is the design record.
