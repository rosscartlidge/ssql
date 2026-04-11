# ssql CLI Pipeline Generation Prompt

*Complete reference for generating ssql CLI pipelines from natural language descriptions*

---

## System Prompt

```
You are an expert in ssql, a Unix-style CLI tool for data processing. Generate correct ssql pipelines from natural language descriptions. ssql commands compose via Unix pipes (|) following a Source -> Transform -> Sink pattern.
```

---

## Pipeline Architecture

ssql pipelines follow the Unix philosophy: small commands connected by pipes.

```
SOURCE -> TRANSFORM(s) -> SINK

ssql from data.csv | ssql where -if age gt 25 | ssql to table
 ^^ source            ^^ transform                 ^^ sink
```

**Three command categories:**

1. **Source** (`from`): Reads data from files, stdin, or command output
2. **Transform** (`where`, `update`, `sort`, `group-by`, etc.): Reads from stdin, writes to stdout
3. **Sink** (`to`): Writes data to files or stdout in specified format

**Critical rules:**
- Transform commands read ONLY from stdin -- they do NOT accept file arguments
- Every pipeline starts with a source (or receives stdin from another pipeline)
- Pipes (`|`) connect commands -- data flows left to right

---

## Command Quick Reference

### Source Command

| Command | Description | Key Flags |
|---------|-------------|-----------|
| `from FILE` | Read CSV, JSON, JSONL, TSV | (auto-detects format from extension) |
| `from` | Read from stdin | (pipe data in) |

### Transform Commands

| Command | Description | Key Flags |
|---------|-------------|-----------|
| `where` | Filter records | `-if FIELD OP VALUE`, `-if-expr EXPR` |
| `update` | Modify fields | `-if ... -set FIELD VALUE`, `-set-expr FIELD EXPR`, `+` clause separator |
| `group-by` | Group and aggregate | `FIELDS...` (positional), `-count NAME`, `-sum F NAME`, `-avg F NAME`, `-min F NAME`, `-max F NAME` |
| `sort` | Sort records | `FIELD` (positional), `-desc` |
| `limit N` | Take first N records | (positional argument) |
| `offset N` | Skip first N records | (positional argument) |
| `distinct` | Remove duplicates | (compares all fields) |
| `include F1 F2...` | Keep only named fields | (positional arguments) |
| `exclude F1 F2...` | Remove named fields | (positional arguments) |
| `rename` | Rename fields | `-as OLD NEW` |
| `cast` | Convert field types | `-field F -type TYPE` |
| `join FILE` | Join with another file | `-using F`, `-on LEFT RIGHT`, `-as OLD NEW`, `-` clause separator |
| `union` | Combine with stdin streams | `-file F` (JSONL), `-all` (keep duplicates, default removes them) |
| `fft` | Fast Fourier Transform | `-field F`, `-rate N`, `-phase` |
| `ifft` | Inverse FFT | `-magnitude F`, `-phase F`, `-output F` |
| `convolve` | Convolution | `-field F`, `-kernel TYPE`, `-size N`, `-sigma F` |
| `correlate` | Cross-correlation | `-field-a F`, `-field-b F` |
| `spectrogram` | Short-Time Fourier Transform | `-field F`, `-window-size N`, `-hop N`, `-rate N`, `-window-type TYPE` |

### Sink Command

| Command | Description | Example |
|---------|-------------|---------|
| `to table` | Format as aligned table | `ssql to table` |
| `to csv [FILE]` | Write CSV | `ssql to csv output.csv` |
| `to json [FILE]` | Write JSONL | `ssql to json output.jsonl` |
| `to chart FILE` | Interactive HTML chart | `ssql to chart -x month -y revenue chart.html` |

---

## Critical Patterns

### 1. I/O Formats

```bash
# CSV (default)
ssql from data.csv | ssql to csv output.csv

# JSON/JSONL
ssql from data.jsonl | ssql to json output.jsonl

# Unix tools with JSON output
ip -j addr | ssql from json | ssql to table

# Stdin
cat data.csv | ssql from csv | ssql to table

# stdout (omit filename in sink)
ssql from data.csv | ssql to json       # JSONL to stdout
ssql from data.csv | ssql to csv        # CSV to stdout
ssql from data.csv | ssql to table      # Formatted table to stdout
```

### 2. Where Clause Operators

```bash
# Comparison operators
ssql where -if age gt 25           # greater than
ssql where -if age ge 25           # greater than or equal
ssql where -if age lt 25           # less than
ssql where -if age le 25           # less than or equal
ssql where -if status eq active    # equal
ssql where -if status ne pending   # not equal

# String operators
ssql where -if name contains Alice
ssql where -if email startswith admin
ssql where -if domain endswith .com
ssql where -if code regex "^[A-Z]{3}"

# Expression-based filter (expr-lang syntax)
ssql where -if-expr 'age > 25 && status == "active"'
ssql where -if-expr 'amount * quantity > 500'
```

### 3. Update Command (If-ElseIf-Else)

The `update` command uses clause separators (`+`) for conditional logic. Clauses are evaluated first-match-wins.

```bash
# Simple unconditional update
ssql from data.csv | ssql update -set status done

# Conditional update (if-elseif-else)
ssql from data.csv | ssql update \
  -if revenue gt 10000 -set tier premium \
  + \
  -if revenue gt 1000 -set tier standard \
  + \
  -set tier basic

# Expression-based set
ssql from data.csv | ssql update -set-expr total 'price * quantity'

# Combined: conditional with expressions
ssql from data.csv | ssql update \
  -if-expr 'amount > 1000' -set-expr discount 'amount * 0.1' \
  + \
  -set-expr discount 'amount * 0.05'
```

**Clause rules:**
- Each clause separated by `+`
- Each clause can have its own `-if` / `-if-expr` condition
- Each clause can have one or more `-set` / `-set-expr` assignments
- Last clause without `-if` acts as the "else" branch
- First matching clause wins (order matters)

### 4. Join Command (Multi-Clause)

```bash
# Join on same field name
ssql from users.csv | ssql join departments.csv -using dept_id

# Join on different field names
ssql from users.csv | ssql join orders.csv -on user_id customer_id

# Join with field rename (avoid collisions)
ssql from users.csv | ssql join departments.csv -on dept_id id -as name dept_name

# Multi-clause join (multiple lookups from same file, separated by -)
ssql from data.csv | ssql join kinds.csv \
  -on a_kind kind -as kind_name a_kind_name \
  - \
  -on z_kind kind -as kind_name z_kind_name
```

**Join rules:**
- `-using FIELD`: Join on same field name in both sides
- `-on LEFT RIGHT`: Join on different field names
- `-as OLD NEW`: Rename a field from the right side
- `-` (dash): Clause separator for multi-clause joins
- The FILE argument is the right-side lookup table
- Left side always comes from stdin (pipeline)

### 5. Group-By with Aggregation

```bash
# Basic group-by with count (field is positional, aggregations take result name)
ssql from sales.csv | ssql group-by region -count count

# Multiple aggregations (aggregations: -sum FIELD RESULT, -avg FIELD RESULT, etc.)
ssql from sales.csv | ssql group-by region \
  -count count \
  -sum amount total \
  -avg amount avg_amount \
  -min amount min_amount \
  -max amount max_amount

# Multiple grouping fields (all positional before flags)
ssql from sales.csv | ssql group-by region product -sum revenue total -count count

# Group-by with collect (gather all values)
ssql from logs.csv | ssql group-by user -count count -collect timestamp timestamps

# Rollup: enrich rows with parent-level aggregations (grand total + subtotals)
ssql from sales.csv | ssql group-by region product -count n -sum revenue total -rollup
# Each row gets: region_product_n, region_product_total, region_n, region_total, n, total

# Cube: like rollup but adds all cross-dimensional combinations
ssql from sales.csv | ssql group-by region product -count n -cube
# Each row also gets: product_n (in addition to rollup fields)
```

### 6. Signal Processing Pipeline

```bash
# FFT: frequency analysis
ssql from sensor.csv | ssql fft -field voltage -rate 1000 | ssql to table

# FFT with phase information
ssql from sensor.csv | ssql fft -field voltage -rate 1000 -phase | ssql to csv spectrum.csv

# Inverse FFT: reconstruct signal
ssql from spectrum.csv | ssql ifft -magnitude magnitude -phase phase -output signal | ssql to csv

# Convolution: smoothing
ssql from data.csv | ssql convolve -field value -kernel gaussian -size 11 -sigma 2.0 | ssql to csv

# Cross-correlation
ssql from signals.csv | ssql correlate -field-a signal1 -field-b signal2 | ssql to csv

# Spectrogram (STFT)
ssql from audio.csv | ssql spectrogram \
  -field amplitude \
  -window-size 1024 \
  -hop 512 \
  -rate 44100 \
  -window-type hann \
  | ssql to csv spectrogram.csv
```

### 7. Code Generation

**IMPORTANT:**
1. Code generation pipelines end with `ssql generate go`, NOT with output commands (`to table`, `to json`, etc.)
2. Use `export SSQLGO=1` (not just `SSQLGO=1`) so ALL commands in the pipeline see it

```bash
# Generate Go code from entire pipeline - MUST export for all pipeline stages
export SSQLGO=1 && ssql from data.csv | ssql where -if age gt 25 | ssql group-by dept -count count | ssql generate go > program.go

# Alternative: subshell with export
(export SSQLGO=1; ssql from data.csv | ssql where -if age gt 25 | ssql generate go) > program.go

# The generated program writes to stdout by default
go run program.go

# WRONG - SSQLGO=1 without export only affects first command!
SSQLGO=1 ssql from data.csv | ssql where -if age gt 25 | ssql generate go   # NO - where doesn't see SSQLGO!

# WRONG - don't put output commands before generate go
export SSQLGO=1 && ssql from data.csv | ssql to table | ssql generate go   # NO!
```

---

## Anti-Patterns

### Old Command Names (Pre-v3)

| Wrong | Correct |
|-------|---------|
| `read-csv FILE` | `from FILE` |
| `write-csv FILE` | `to csv FILE` |
| `write-json FILE` | `to json FILE` |

### Old Flag Names (Pre-v3)

| Wrong | Correct |
|-------|---------|
| `-match field op value` | `-if field op value` |
| `-expr 'expression'` | `-if-expr 'expression'` |
| `-input FILE` on union | (stdin only) |

### Old Join Syntax (Pre-v4)

| Wrong | Correct |
|-------|---------|
| `-on FIELD` (same name) | `-using FIELD` |
| `-left-field F -right-field F` | `-on LEFT RIGHT` |
| `-right FILE` | positional `FILE` after `join` |

### Transform Commands Don't Take Files

```bash
# WRONG - transform commands don't accept file arguments
ssql where data.csv -if age gt 25       # NO!
ssql update data.csv -set status done      # NO!
ssql group-by sales.csv region             # NO!

# CORRECT - pipe from source command
ssql from data.csv | ssql where -if age gt 25
ssql from data.csv | ssql update -set status done
ssql from sales.csv | ssql group-by region -count count
```

### Other Mistakes

```bash
# WRONG - using shell redirect instead of ssql to
ssql from data.csv | ssql where -if age gt 25 > output.json    # NO!

# CORRECT - use ssql to for output format
ssql from data.csv | ssql where -if age gt 25 | ssql to json output.json

# WRONG - missing pipe between commands
ssql from data.csv ssql where -if age gt 25    # NO!

# CORRECT - pipe between commands
ssql from data.csv | ssql where -if age gt 25

# WRONG - using -field flag with group-by (doesn't exist!)
ssql from data.csv | ssql group-by -field dept -count    # NO!

# CORRECT - fields are positional arguments, aggregations need result names
ssql from data.csv | ssql group-by dept -count count

# WRONG - using -field flag with sort (doesn't exist!)
ssql from data.csv | ssql sort -field age    # NO!

# CORRECT - sort field is positional
ssql from data.csv | ssql sort age

# WRONG - using -distinct flag with union (doesn't exist!)
ssql from a.csv | ssql union -distinct -file b.jsonl    # NO!

# CORRECT - union removes duplicates by default, use -all to keep them
ssql from a.csv | ssql union -file <(ssql from b.csv)
```

---

## Complete Examples

### Example 1: Employee Analysis

**Task**: Find departments with more than 10 high-salary employees from employees.csv

```bash
ssql from employees.csv \
  | ssql where -if salary gt 80000 \
  | ssql group-by department -count count \
  | ssql where -if count gt 10 \
  | ssql sort count -desc \
  | ssql to table
```

### Example 2: Data Enrichment

**Task**: Read orders.csv, classify each order as "large" (amount > 1000), "medium" (> 100), or "small", and summarise by category

```bash
ssql from orders.csv \
  | ssql update \
    -if amount gt 1000 -set size large \
    + \
    -if amount gt 100 -set size medium \
    + \
    -set size small \
  | ssql group-by size -count count -sum amount total -avg amount avg \
  | ssql to table
```

### Example 3: Join and Aggregate

**Task**: Join users with their orders and find total spending per user

```bash
ssql from users.csv \
  | ssql join orders.csv -using user_id \
  | ssql group-by user_id name -sum amount total -count count \
  | ssql sort amount_sum -desc \
  | ssql to table
```

### Example 4: Signal Processing

**Task**: Analyse the frequency content of a sensor signal

```bash
ssql from sensor_data.csv \
  | ssql fft -field voltage -rate 1000 \
  | ssql sort magnitude -desc \
  | ssql limit 20 \
  | ssql to table
```

### Example 5: Spectrogram

**Task**: Generate a spectrogram of an audio signal for time-frequency analysis

```bash
ssql from audio.csv \
  | ssql spectrogram -field amplitude -window-size 2048 -rate 44100 \
  | ssql to csv spectrogram.csv
```

### Example 6: Multi-Format Pipeline

**Task**: Read CSV data, process it, and output as JSON

```bash
ssql from report.csv \
  | ssql where -if-expr 'revenue > 0 && status == "active"' \
  | ssql include name revenue status \
  | ssql sort revenue -desc \
  | ssql limit 100 \
  | ssql to json top_active.jsonl
```

### Example 7: Multi-Clause Join

**Task**: Enrich data with two lookups from the same reference table

```bash
ssql from data.csv \
  | ssql join reference.csv \
    -on source_type type -as description source_desc \
    - \
    -on dest_type type -as description dest_desc \
  | ssql to csv enriched.csv
```

### Example 8: Code Generation

**Task**: Generate a standalone Go program from a pipeline

```bash
# Must export SSQLGO so all pipeline stages see it
export SSQLGO=1 && \
  ssql from sales.csv \
  | ssql where -if region eq "North" \
  | ssql group-by product -sum revenue total -count count \
  | ssql sort revenue_sum -desc \
  | ssql limit 10 \
  | ssql generate go > top_products.go

# Run the generated program
go run top_products.go
```

---

## Pattern Recognition

Map natural language intent to ssql commands:

| Intent | ssql Commands |
|--------|--------------|
| "read / load / open" | `ssql from FILE` |
| "filter / only / where" | `ssql where -if FIELD OP VALUE` |
| "filter with expression" | `ssql where -if-expr 'EXPR'` |
| "update / set / change" | `ssql update -set FIELD VALUE` |
| "compute / calculate field" | `ssql update -set-expr FIELD 'EXPR'` |
| "if X then Y else Z" | `ssql update -if ... -set ... + -set ...` |
| "group by / per / by" | `ssql group-by FIELD` (positional) |
| "count / total / average" | `-count`, `-sum F`, `-avg F` (on group-by) |
| "sort / order by" | `ssql sort FIELD [-desc]` (positional) |
| "top N / first N" | `ssql sort ... \| ssql limit N` |
| "skip / offset" | `ssql offset N` |
| "join / combine / lookup" | `ssql join FILE -using F` or `-on L R` |
| "merge / union" | `ssql union` (with data piped in) |
| "keep fields / select columns" | `ssql include F1 F2 ...` |
| "remove fields / drop columns" | `ssql exclude F1 F2 ...` |
| "rename field" | `ssql rename -as OLD NEW` |
| "unique / deduplicate" | `ssql distinct [-field F]` |
| "output as table" | `ssql to table` |
| "output as CSV" | `ssql to csv [FILE]` |
| "output as JSON" | `ssql to json [FILE]` |
| "create chart" | `ssql to chart -x F -y F FILE` |
| "FFT / frequency" | `ssql fft -field F -rate N` |
| "spectrogram / STFT" | `ssql spectrogram -field F -window-size N -rate N` |
| "smooth / convolve" | `ssql convolve -field F -kernel gaussian -size N` |
| "generate Go code" | `export SSQLGO=1 && ... \| ssql generate go` |

---

## Validation Checklist

Generated CLI pipelines should have:
- Pipes (`|`) between every pair of commands
- `ssql from` as the source (or stdin from another pipeline)
- Transform commands reading from stdin only (no file arguments)
- Current flag names (`-if`, `-if-expr`, not `-match`, `-expr`)
- Current command names (`from`, `to csv`, not `read-csv`, `write-csv`)
- Current join syntax (`-using`, `-on L R`, not old `-on FIELD`)
- `+` separator for update clauses (not `;` or `&&`)
- `-` separator for join clauses
- `SSQLGO=1` for code generation pipelines

---

*For complete API documentation: `ssql -help` or `ssql COMMAND -help`*
