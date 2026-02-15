# ssql CLI Tutorial

*Command-line data processing with code generation - Still in active development*

## Table of Contents

### Documentation Navigation
- **[API Reference](api-reference.md)** - Complete function reference
- **[Getting Started Guide](codelab-intro.md)** - Learn the library fundamentals
- **[Advanced Tutorial](advanced-tutorial.md)** - Complex patterns and optimization
- **[Debugging Pipelines](cli-debugging.md)** - Debug with jq, inspect data, profile performance
- **[Troubleshooting Guide](cli-troubleshooting.md)** - Common issues and quick solutions

### Learning Path
- [Quick Start](#quick-start)
- [What is the ssql CLI?](#what-is-the-ssql-cli)
- [Basic Pipeline Operations](#basic-pipeline-operations)
- [Working with Real Data](#working-with-real-data)
- [Grouping and Aggregations](#grouping-and-aggregations)
- [SQL-Like Operations](#sql-like-operations)
- [Signal Processing](#signal-processing)
- [Creating Visualizations](#creating-visualizations)
- [Code Generation](#code-generation)
- [Complete Example](#complete-example)
- [Available Commands](#available-commands)
- [What's Next?](#whats-next)

---

## Quick Start

### Installation

```bash
# Install the CLI tool
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest

# Verify installation
ssql version
```

For GPU-accelerated signal processing (FFT, convolution, correlation), see [GPU Acceleration](#signal-processing-fft-convolution-correlation) below.

### Your First Pipeline

```bash
# Create a sample CSV file
cat > employees.csv << 'EOF'
name,age,department,salary
Alice,30,Engineering,95000
Bob,25,Marketing,65000
Carol,35,Engineering,105000
David,28,Sales,70000
EOF

# Process it with a pipeline
ssql from employees.csv | \
  ssql where -where department eq Engineering | \
  ssql include name salary
```

Output:
```json
{"name":"Alice","salary":95000}
{"name":"Carol","salary":105000}
```

---

## What is the ssql CLI?

The ssql CLI brings Unix pipeline philosophy to structured data processing. It provides:

- **🔗 Pipeline Operations** - Chain commands with Unix pipes
- **📊 Built-in Visualization** - Create charts directly from pipelines
- **🤖 Code Generation** - Convert CLI commands to Go code
- **⚡ Interactive Development** - Prototype fast, then generate production code

### Key Features

**Command Chaining**
```bash
ssql from data.csv | ssql where ... | ssql group ... | ssql to chart ...
```

**Self-Generating Commands**
Every command supports `-generate` flag to emit Go code instead of executing:
```bash
ssql from -generate data.csv | ssql generate-go
```

**Universal Data Format**
All commands use JSONL (JSON Lines) for inter-command communication, enabling complex pipelines.

**Debugging with jq**
Since all commands communicate via JSONL, you can inspect data at any stage with `jq`:
```bash
ssql from data.csv | jq '.' | head -5          # Pretty-print data
ssql from data.csv | jq '.age | type' | head   # Check field types
ssql ... | ssql where ... | jq -s 'length'      # Count results
```
[**See full debugging guide →**](cli-debugging.md)

> ⚠️ **Development Status**: The CLI is under active development. Commands and flags may change. Use `-help` on any command to see current options.

---

## Basic Pipeline Operations

### Reading Data

Read CSV files and output as JSONL:

```bash
ssql from employees.csv
```

Output (JSONL):
```json
{"_row_number":0,"age":30,"department":"Engineering","name":"Alice","salary":95000}
{"_row_number":1,"age":25,"department":"Marketing","name":"Bob","salary":65000}
...
```

### Schema Headers (Automatic)

The `from` command automatically emits a schema header that preserves field order and types through pipelines:

```bash
ssql from employees.csv
```

Output (JSONL with schema header):
```json
{"_schema":{"fields":["name","age","department","salary"],"types":{"name":"string","age":"int","department":"string","salary":"int"}}}
{"_row_number":0,"name":"Alice","age":30,"department":"Engineering","salary":95000}
{"_row_number":1,"name":"Bob","age":25,"department":"Marketing","salary":65000}
...
```

**Benefits of automatic schema:**
- **Field order preservation**: Schema ensures output commands maintain the original CSV column order.
- **Type information**: Schema carries type information (string, int, float, bool) through the pipeline.
- **Better CSV output**: `ssql to csv` uses schema to output columns in the same order as the input.

**Example: Full pipeline with schema**
```bash
# Input CSV has columns: name, age, department, salary
ssql from employees.csv | \
  ssql where -where age gt 25 | \
  ssql to csv output.csv

# Output CSV has same column order: name, age, department, salary
```

**Schema flows through all commands:**
- Transform commands (`where`, `update`, `sort`, etc.) pass schema through unchanged
- Output commands (`to csv`, `to json`, `to table`) use schema for field ordering
- The schema header is automatically consumed and not included in final output

### Filtering Data

Filter records based on conditions:

```bash
# Single condition
ssql from employees.csv | \
  ssql where -where salary gt 70000

# Multiple conditions (AND)
ssql from employees.csv | \
  ssql where -where age gt 25 -where department eq Engineering

# Multiple conditions (OR) - use + separator
ssql from employees.csv | \
  ssql where -where department eq Engineering + -where department eq Sales
```

**Available Operators:**
- `eq` - Equal
- `ne` - Not equal
- `gt` - Greater than
- `lt` - Less than
- `ge` - Greater than or equal
- `le` - Less than or equal
- `contains` - String contains
- `starts` - String starts with
- `ends` - String ends with

### Selecting Fields

Select specific fields or rename them:

```bash
# Select fields
ssql from employees.csv | \
  ssql include name salary

# Rename fields
ssql from employees.csv | \
  ssql include name salary | \
  ssql rename -as name employee_name -as salary annual_salary
```

### Updating Fields

Update record fields conditionally using if-elseif-else logic:

```bash
# Unconditional update - all records
ssql from employees.csv | \
  ssql update -set status "active"

# Conditional update - only matching records
ssql from employees.csv | \
  ssql update -where salary gt 100000 -set bracket "high"

# Multiple conditions (AND logic)
ssql from employees.csv | \
  ssql update -where status eq pending -where priority eq urgent -set assignee "alice"

# If-elseif-else with + separator (first match wins)
ssql from customers.csv | \
  ssql update \
    -where purchases gt 5000 -set tier "Gold" -set discount 0.2 + \
    -where purchases gt 1000 -set tier "Silver" -set discount 0.1 + \
    -set tier "Bronze" -set discount 0.0
```

**How It Works:**
- **Without `-where`**: Updates all records
- **With `-where`**: Only updates records matching conditions, others pass through unchanged
- **Multiple `-where` flags**: AND logic (all must match)
- **`+` separator**: Creates clauses for if-elseif-else logic (first matching clause wins)
- **Default clause**: Clause with no `-where` acts as else (catches all remaining records)

**Type Inference:**
The `update` command automatically infers types from string values:
- `"123"` → integer (`int64`)
- `"99.99"` → float (`float64`)
- `"true"` / `"false"` → boolean
- `"2025-11-04"` → time.Time (if valid date format)
- Everything else → string

**Complex Example:**
```bash
# Set priority based on multiple conditions
ssql from orders.csv | \
  ssql update \
    -where status eq pending -where amount gt 10000 -set priority "critical" -set sla 24 + \
    -where status eq pending -where amount gt 1000 -set priority "high" -set sla 48 + \
    -where status eq pending -set priority "normal" -set sla 72 + \
    -set priority "low" -set sla 168
```

This keeps ALL records while selectively updating fields based on conditions.

### Writing Output

Write results to CSV:

```bash
ssql from employees.csv | \
  ssql where -where department eq Engineering | \
  ssql to csv engineers.csv
```

### Working with Excel Files

Read and write Excel (.xlsx) files:

```bash
# Read an Excel file
ssql from sales.xlsx | ssql to table

# Read a specific sheet
ssql from sales.xlsx -sheet "Q4 Results" | ssql to table

# Filter Excel data and write to a new spreadsheet
ssql from sales.xlsx | \
  ssql where -where revenue gt 50000 | \
  ssql to xlsx top_performers.xlsx

# Convert CSV to Excel
ssql from data.csv | ssql to xlsx data.xlsx

# Write to a named sheet
ssql from data.csv | ssql to xlsx report.xlsx -sheet Summary
```

### Displaying Data as Tables

Display records in a formatted table on the terminal:

```bash
# Simple table display
ssql from employees.csv | ssql to table

# With filtering
ssql from employees.csv | \
  ssql where -where department eq Engineering | \
  ssql to table

# Limit column width to prevent wrapping
ssql from employees.csv | \
  ssql to table -max-width 30

# Complex pipeline with updates and filtering
ssql from customers.csv | \
  ssql update \
    -where purchases gt 5000 -set tier "Gold" + \
    -where purchases gt 1000 -set tier "Silver" + \
    -set tier "Bronze" | \
  ssql where -where tier eq Gold | \
  ssql table
```

**Features:**
- Automatically calculates column widths
- Sorts columns alphabetically for consistent output
- Truncates long values with `...` when exceeding `-max-width`
- Works with all field types (strings, numbers, dates, etc.)
- Supports code generation with `-generate` flag

**Example output:**
```
_row_number   age   city      name      salary
----------------------------------------------
0             30    NYC       Alice     95000
1             25    LA        Bob       75000
2             35    Chicago   Charlie   120000
```

---

## Working with Real Data

### Processing Command Output

Execute shell commands and parse their output:

```bash
# Analyze process information
ssql from -- ps -efl | \
  ssql where -where CMD contains chrome | \
  ssql include PID USER CMD
```

**Note:** The `--` separator is required to prevent ssql from interpreting command flags like `-efl` as its own flags.

### Example: System Monitoring

Find memory-intensive processes:

```bash
# Get top memory users
ssql from -- ps aux | \
  ssql where -where USER eq root | \
  ssql include PID MEM CMD | \
  ssql to csv system_processes.csv
```

---

## Grouping and Aggregations

Group data and calculate statistics:

### Basic Aggregation

```bash
# Count records by department
ssql from employees.csv | \
  ssql group-by department -count total
```

Output:
```json
{"department":"Engineering","total":3}
{"department":"Marketing","total":2}
{"department":"Sales","total":1}
```

### Multiple Aggregations

Specify multiple aggregations in one command:

```bash
ssql from employees.csv | \
  ssql group-by department \
    -count employee_count \
    -avg salary avg_salary \
    -max salary max_salary
```

Output:
```json
{"avg_salary":98333,"department":"Engineering","employee_count":3,"max_salary":105000}
{"avg_salary":65000,"department":"Marketing","employee_count":2,"max_salary":65000}
{"avg_salary":70000,"department":"Sales","employee_count":1,"max_salary":70000}
```

**Available Aggregation Functions:**
- `count` - Count records
- `sum` - Sum values
- `avg` - Average values
- `min` - Minimum value
- `max` - Maximum value

---

## SQL-Like Operations

ssql supports common SQL operations for multi-table queries and data manipulation.

### Pagination with OFFSET and LIMIT

Skip and take records for pagination:

```bash
# Skip first 20 records, take next 10 (records 21-30)
ssql from data.csv | \
  ssql offset 20 | \
  ssql limit 10
```

Equivalent SQL:
```sql
SELECT * FROM data LIMIT 10 OFFSET 20
```

### Remove Duplicates with DISTINCT

Remove duplicate records:

```bash
# Distinct on all fields
ssql from data.csv | ssql distinct

# Distinct by specific fields (select fields first, then distinct)
ssql from employees.csv | \
  ssql include department location | \
  ssql distinct
```

Equivalent SQL:
```sql
SELECT DISTINCT department, location FROM employees
```

### JOIN Operations

Join two data sources on common fields:

```bash
# Inner join on same field name (use -using)
ssql from employees.csv | \
  ssql join <(ssql from departments.csv) -using dept_id

# Join with different field names (use -on LEFT RIGHT)
ssql from orders.csv | \
  ssql join <(ssql from customers.csv) -type left -on customer_id id

# Multiple lookups from same file with field renaming
# (Reads kind.csv once, performs two lookups)
ssql from data.csv | ssql join <(ssql from kind.csv) \
  -on a_kind kind -as kind_name a_kind_name \
  - \
  -on z_kind kind -as kind_name z_kind_name
```

**Join Flags:**
- `-using FIELD` - Join on same field name in both sides
- `-on LEFT RIGHT` - Join on different field names
- `-as OLD NEW` - Rename field from right side when bringing it in
- `-type TYPE` - Join type: inner (default), left, right, full
- `-` - Clause separator for multiple lookups from same file

**Join Types:**
- `inner` - Only matching records (default)
- `left` - All left records, matched right records
- `right` - All right records, matched left records
- `full` - All records from both sides

Equivalent SQL:
```sql
SELECT * FROM employees e
INNER JOIN departments d ON e.dept_id = d.dept_id
```

### UNION Operations

Combine multiple data sources:

```bash
# UNION (remove duplicates)
ssql from customers.csv | \
  ssql union -file suppliers.csv

# UNION ALL (keep duplicates)
ssql from file1.csv | \
  ssql union -all -file file2.csv -file file3.csv
```

Equivalent SQL:
```sql
SELECT * FROM customers
UNION
SELECT * FROM suppliers
```

---

## Signal Processing

ssql includes signal processing commands for frequency analysis and filtering.

### FFT (Fast Fourier Transform)

Compute frequency spectrum from time-domain signals:

```bash
# Basic FFT of a signal field
ssql from signal.csv | \
  ssql fft -field amplitude

# FFT with sample rate for frequency calculation
ssql from audio.csv | \
  ssql fft -field amplitude -rate 44100

# Include phase information
ssql from signal.csv | \
  ssql fft -field value -phase
```

Output includes:
- `index` - Frequency bin index
- `frequency` - Frequency in Hz (if sample rate specified)
- `magnitude` - Signal strength at each frequency
- `phase` - Phase angle in radians (if `-phase` flag used)

**Example: Analyze audio frequencies**
```bash
# Find dominant frequencies in audio data
ssql from audio_samples.csv | \
  ssql fft -field amplitude -rate 44100 | \
  ssql sort magnitude -desc | \
  ssql limit 10 | \
  ssql to table
```

### Convolution

Apply convolution filters for smoothing, edge detection, and custom filtering:

```bash
# Moving average smoothing (5-point)
ssql from sensor.csv | \
  ssql convolve -field reading -kernel avg -size 5

# Gaussian smoothing
ssql from data.csv | \
  ssql convolve -field value -kernel gaussian -size 11 -sigma 2.0

# Edge detection with difference kernel
ssql from signal.csv | \
  ssql convolve -field value -kernel diff

# Custom kernel weights
ssql from data.csv | \
  ssql convolve -field value -custom 0.25,0.5,0.25
```

**Built-in Kernels:**
- `avg` - Moving average (smoothing)
- `gaussian` - Gaussian smoothing (configurable sigma)
- `diff` - First derivative (edge detection)
- `laplacian` - Second derivative
- `sobel` - Sobel operator

**Flags:**
- `-field` / `-f` - Input field name (required)
- `-output` / `-o` - Output field name (default: `field_convolved`)
- `-kernel` / `-k` - Built-in kernel name
- `-size` / `-s` - Kernel size for avg/gaussian (default: 5)
- `-sigma` - Sigma for Gaussian kernel (default: 1.0)
- `-custom` / `-c` - Custom kernel as comma-separated values
- `-same` - Output same length as input (truncate edges)

**Example: Noise reduction pipeline**
```bash
# Read noisy sensor data, smooth it, then visualize
ssql from noisy_sensor.csv | \
  ssql convolve -field reading -kernel gaussian -size 7 -sigma 1.5 -same | \
  ssql chart -x timestamp -y reading_convolved -output smoothed.html
```

### CPU vs GPU Processing

Signal processing works out of the box using CPU implementations - no special setup required:

```bash
# Standard install - works everywhere
go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest

# All signal processing commands work immediately
ssql from signal.csv | ssql fft -field value
ssql from sensor.csv | ssql convolve -field reading -kernel gaussian
```

**Optional GPU Acceleration:**

For large datasets, CUDA GPU acceleration provides 10-50x speedup. This requires:
1. NVIDIA GPU with CUDA support
2. CUDA toolkit installed (`nvcc` compiler)
3. Building with GPU support:

```bash
# Clone and build GPU-accelerated version
git clone https://github.com/rosscartlidge/ssql.git
cd ssql

# Build CUDA library
cd gpu && make && cd ..

# Build ssql with GPU support
go build -tags gpu -o ssql_gpu ./cmd/ssql

# Install to Go bin directory
cp ssql_gpu ~/go/bin/

# Add alias to ~/.bashrc (adjusts library path automatically)
echo 'alias ssql_gpu="LD_LIBRARY_PATH=/path/to/ssql/gpu ~/go/bin/ssql_gpu"' >> ~/.bashrc
source ~/.bashrc
```

GPU is used automatically when beneficial:
- FFT/IFFT: signals >= 16K samples (28-54x faster)
- Convolution: kernels >= 16 points
- Correlation: same as convolution

Smaller signals use CPU (faster due to GPU transfer overhead).

---

## Creating Visualizations

Generate interactive HTML charts with Chart.js:

### Simple Chart

```bash
ssql from employees.csv | \
  ssql to chart -x department -y salary salary_chart.html
```

Opens `salary_chart.html` with an interactive chart featuring:
- Multiple chart types (line, bar, scatter, pie, radar)
- Zoom and pan controls
- Field selection UI
- Data export to PNG/CSV

### Chart with Aggregations

```bash
ssql from employees.csv | \
  ssql group-by department -avg salary avg_salary | \
  ssql to chart -x department -y avg_salary dept_salaries.html
```

### Interactive Data Explorer

Generate a self-contained HTML app with sortable tables, charts, and aggregation UI:

```bash
# Basic exploration
ssql from sales.csv | ssql to explore output.html

# With initial chart fields and dark theme
ssql from sales.csv | ssql to explore -x date -y revenue -theme dark analysis.html
```

### Explorer with WASM Transforms

Build the WASM module once, then use `-wasm` to enable client-side ssql operations
(Where, Sort, GroupBy, Distinct, Limit) powered by the same Go code as the CLI:

```bash
# Build the WASM module (one-time)
make wasm

# Generate explorer with WASM-powered transforms
ssql from sales.csv | ssql to explore -wasm cmd/ssql-wasm/ssql.wasm output.html
```

The explorer falls back to JavaScript if WASM fails to load. A green "WASM" badge
appears in the header when the module is active.

### Animated Frequency Spectrum from WAV

Use `to animate` to watch how the frequency spectrum of an audio file evolves over time.
The spectrogram command splits the signal into overlapping windows; we use 1-second
windows (`-window-size 44100` at 44.1 kHz) so each frame is one second of audio:

```bash
# Animate frequency magnitude in 1-second time slots
ssql from recording.wav | \
  ssql spectrogram -field amplitude -window-size 44100 -hop 44100 -rate 44100 -output db | \
  ssql to animate -frame time -x frequency -y magnitude -type histogram
```

This creates `animate.html` — a self-contained page with play/pause, scrubber, and
speed controls. Each frame shows the frequency spectrum (magnitude in dB) for one
second of audio.

For a heatmap view that scrolls through a long recording, split the spectrogram
into 10-second segments.  Each animated frame shows a full time × frequency
heatmap for that segment:

```bash
ssql from long_recording.wav | \
  ssql spectrogram -field amplitude -window-size 4096 -hop 4096 -rate 44100 -output db | \
  ssql update -set-expr segment 'int(time / 10)' | \
  ssql to animate -frame segment -x time -y frequency -z magnitude \
    -type heatmap -fps 1 -colorscale plasma -loop
```

Smaller windows give finer time resolution at the cost of more frames:

```bash
# 0.5-second windows, overlapping by 50%
ssql from recording.wav | \
  ssql spectrogram -field amplitude -window-size 22050 -hop 11025 -rate 44100 -output db | \
  ssql to animate -frame time -x frequency -y magnitude -type histogram -fps 4
```

---

## Code Generation

Every command supports the `-generate` flag to output Go code instead of executing:

### Generate Code from Pipeline

```bash
ssql from -generate employees.csv | \
  ssql where -generate -where department eq Engineering | \
  ssql include name salary | \
  ssql to csv -generate output.csv | \
  ssql generate-go
```

Output:
```go
package main

import (
	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	records, err := ssql.ReadCSV("employees.csv")
	if err != nil {
		panic(err)
	}
	filtered := ssql.Where(func(r ssql.Record) bool {
		return ssql.GetOr(r, "department", "") == "Engineering"
	})(records)
	selected := ssql.Select(func(r ssql.Record) ssql.Record {
		return ssql.MakeMutableRecord().
			SetAny("name", ssql.GetOr(r, "name", "")).
			SetAny("salary", ssql.GetOr(r, "salary", float64(0))).
			Freeze()
	})(filtered)
	ssql.WriteCSV(selected, "output.csv")
}
```

### Compile and Run Generated Code

```bash
# Generate code to file
ssql from -generate data.csv | \
  ssql group-by -generate region -sum sales total | \
  ssql generate-go > analysis.go

# Add package initialization
cat > go.mod << 'EOF'
module analysis
go 1.23
require github.com/rosscartlidge/ssql/v4 latest
EOF

# Build and run
go mod tidy
go run analysis.go
```

### Advanced Example: Complex Pipeline with Chain()

When you use multiple transformation commands, the generated code automatically uses `ssql.Chain()` for clean, readable code:

```bash
# Complex pipeline: filter, select, sort, limit
ssql from -generate sales.csv | \
  ssql where -where revenue gt 1000 -generate | \
  ssql include salesperson revenue | \
  ssql sort revenue -desc -generate | \
  ssql limit 10 -generate | \
  ssql to csv -generate top_performers.csv | \
  ssql generate-go > report.go
```

Generated code (`report.go`):
```go
package main

import (
	"fmt"
	"os"
	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	records, err := ssql.ReadCSV("sales.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Multiple operations composed with Chain()
	result := ssql.Chain(
		ssql.Where(func(r ssql.Record) bool {
			return ssql.GetOr(r, "revenue", float64(0)) > 1000
		}),
		ssql.Select(func(r ssql.Record) ssql.Record {
			return ssql.MakeMutableRecord().
				SetAny("salesperson", ssql.GetOr(r, "salesperson", "")).
				SetAny("revenue", ssql.GetOr(r, "revenue", float64(0))).
				Freeze()
		}),
		ssql.SortBy(func(r ssql.Record) float64 {
			return -ssql.GetOr(r, "revenue", float64(0))  // Negative for descending
		}),
		ssql.Limit[ssql.Record](10),
	)(records)

	ssql.WriteCSV(result, "top_performers.csv")
}
```

Compile and run:
```bash
# Setup and run
go mod init report
go mod tidy
go build -o report report.go
./report

# View results
cat top_performers.csv
```

**Key Features of Generated Code:**
- ✅ **Clean Chain() pattern** - Multiple operations composed functionally
- ✅ **Type-safe helpers** - `asFloat64()` handles both int64 and float64
- ✅ **Proper error handling** - Exit codes and stderr for errors
- ✅ **Production-ready** - Compiles and runs immediately
- ✅ **Readable** - Easy to understand and modify

### Example with Aggregations

Generate code for GROUP BY with multiple aggregations:

```bash
ssql from -generate sales.csv | \
  ssql group-by -generate region \
    -count num_sales \
    -sum revenue total_revenue \
    -avg revenue avg_revenue | \
  ssql sort -generate total_revenue -desc | \
  ssql to csv -generate region_report.csv | \
  ssql generate-go > region_analysis.go
```

Generated code:
```go
package main

import (
	"fmt"
	"os"
	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	records, err := ssql.ReadCSV("sales.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	aggregated := ssql.Chain(
		ssql.GroupByFields("_group", "region"),
		ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
			"num_sales": ssql.Count(),
			"total_revenue": ssql.Sum("revenue"),
			"avg_revenue": ssql.Avg("revenue"),
		}),
	)(records)

	sorted := ssql.SortBy(func(r ssql.Record) float64 {
		return -ssql.GetOr(r, "total_revenue", float64(0))
	})(aggregated)

	ssql.WriteCSV(sorted, "region_report.csv")
}
```

This workflow enables **rapid prototyping** with the CLI, then **instant production deployment** with generated, type-safe Go code!

---

## Complete Example

Let's build a comprehensive data analysis pipeline:

### Scenario: Analyze Process Counts by User

```bash
# Execute the pipeline
ssql from -- ps -efl | \
  ssql group-by UID -count process_count | \
  ssql chart -x UID -y process_count -output /tmp/processes_by_user.html
```

This will:
1. Execute `ps -efl` and parse the output
2. Group processes by UID (user)
3. Count processes per user
4. Create an interactive chart

Output: `Chart created: /tmp/processes_by_user.html`

### Generate Production Code

Now convert the same pipeline to Go code:

```bash
ssql exec -generate -- ps -efl | \
  ssql group-by -generate UID -count process_count | \
  ssql chart -generate -x UID -y process_count -output processes.html | \
  ssql generate-go > monitor.go
```

Generated code in `monitor.go`:
```go
package main

import (
	"fmt"
	"os"
	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	records, err := ssql.ExecCommand("ps", []string{"-efl"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	grouped := ssql.GroupByFields("_group", "UID")(records)
	aggregated := ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
		"process_count": ssql.Count(),
	})(grouped)
	ssql.QuickChart(aggregated, "UID", "process_count", "processes.html")
}
```

Compile and run:
```bash
# Setup module
go mod init monitor
go get github.com/rosscartlidge/ssql/v4

# Build and run
go build -o monitor monitor.go
./monitor
```

---

## Available Commands

### Data Sources
- `from [file]` - Read data from CSV, TSV, JSON, JSONL, Arrow, WAV, or XLSX file (auto-detects format, always emits schema header)
  - `-type field type` - Override type for a field: `-type zipcode string -type age int`
  - `-default-type type` - Default type for all fields: `auto` (default), `string`, `int`, `float`, `bool`
  - `-format fmt` - Input format for stdin: `csv` (default), `tsv`, `json`, `jsonl`, `arrow`, `wav`
  - `-sheet name` - For XLSX files: sheet name to read (default: first sheet)
- `from -- [command] [args...]` - Execute command and parse output

### Transformations
- `where` - Filter records by conditions (`-where field op value`)
- `include` - Select specific fields
- `exclude` - Remove specific fields
- `rename` - Rename fields (`-as old new`)
- `cast` - Convert field types (`-type field type`)
- `update` - Conditionally update field values (if-elseif-else logic)
- `group-by` - Group and aggregate data
- `sort` - Sort records by field
- `limit` - Take first N records
- `offset` - Skip first N records (SQL OFFSET)
- `distinct` - Remove duplicate records (SQL DISTINCT)

### Multi-Table Operations
- `join` - Join two data sources (SQL JOIN - inner/left/right/full)
- `union` - Combine multiple data sources (SQL UNION/UNION ALL)

### Signal Processing
- `fft` - Fast Fourier Transform for frequency analysis (`-field`, `-rate`, `-phase`)
- `ifft` - Inverse FFT to reconstruct time-domain signal (`-magnitude`, `-phase`)
- `convolve` - Apply convolution filters (`-field`, `-kernel`, `-custom`, `-same`)
- `correlate` - Cross-correlation or autocorrelation (`-field`, `-with`)

### Outputs (using `to` subcommands)
- `to table` - Display records as formatted table
- `to csv [file]` - Write CSV file (or stdout)
- `to json [file]` - Write JSON/JSONL file (or stdout)
- `to arrow [file]` - Write Apache Arrow IPC file (10-20x faster I/O)
- `to xlsx [file]` - Write Excel XLSX file
- `to chart` - Create interactive HTML chart
- `to heatmap` - Create heatmap/spectrogram visualization (Plotly.js)
- `to animate` - Create animated heatmap or histogram with video-player controls
- `to explore [file]` - Create interactive data explorer (table + charts + aggregation)
  - `-wasm path/to/ssql.wasm` - Enable client-side WASM transforms (build with `make wasm`)
- `to wav [file]` - Write WAV audio file

### Code Generation
- `generate-go` - Assemble code fragments into Go program

### Utilities
- `functions` - Show available expression functions and operators
- `version` - Show version information

### Getting Help

```bash
# Show all commands
ssql -help

# Show command-specific help
ssql from -help
ssql where -help
ssql group-by -help
ssql to chart -help
```

### Bash Completion

The CLI supports intelligent tab completion for commands, flags, and even field names:

```bash
# Install bash completion (for current session)
eval "$(ssql -bash-completion)"

# Install permanently
ssql -bash-completion > ~/.local/share/bash-completion/completions/ssql

# Or add to ~/.bashrc
echo 'eval "$(ssql -bash-completion)"' >> ~/.bashrc
source ~/.bashrc
```

Now you can tab-complete:
```bash
ssql <TAB>          # Shows all commands
ssql where <TAB>    # Shows flags like -where, -help
ssql from <TAB>     # Completes .csv, .json, .jsonl files
```

### Understanding Command Structure (Advanced)

ssql CLI uses the **autocli** framework for declarative command definitions. This enables powerful features:

**Clause Pattern:**
Commands that support multiple items use `+` as a separator to create "clauses". Each clause can have its own set of flags:

```bash
# Multiple WHERE conditions (OR logic) - each clause after + is independent
ssql where -where age gt 30 + -where salary gt 100000

# Multiple aggregations - specify multiple flags
ssql group-by department \
  -count total \
  -avg salary avg_salary \
  -max salary max_salary
```

**How it works:**
- **Accumulate pattern**: Use the same flag multiple times for multiple operations
- **Self-documenting**: Flag names show the operation (-count, -sum, -avg)
- **Framework**: Automatically parses and validates all arguments
- **Benefits**: Type safety, auto-completion, consistent error messages

**Example breakdown:**
```bash
ssql group-by department \
  -count total \
  #  └─ count aggregation ─┘
  -avg salary avg_salary \
  #  └─ average aggregation ─┘
  -sum hours total_hours
  #  └─ sum aggregation ─┘
```

Each aggregation is independently specified and validated, giving clear error messages if arguments are missing or incorrect.

**Flag patterns:**
- `-count result-name` - Count records (1 argument)
- `-sum field result-name` - Sum field values (2 arguments)
- `-avg field result-name` - Average field values (2 arguments)
- `-min field result-name` - Minimum field value (2 arguments)
- `-max field result-name` - Maximum field value (2 arguments)

This pattern makes complex aggregations readable while maintaining type safety and completion support.

---

## What's Next?

### Workflow: CLI → Code → Production

1. **Prototype with CLI** - Quickly explore your data
   ```bash
   ssql from data.csv | ssql where ... | ssql to chart ...
   ```

2. **Generate Code** - Convert to Go when satisfied
   ```bash
   ssql from -generate data.csv | ... | ssql generate-go > app.go
   ```

3. **Refine and Deploy** - Edit generated code, add error handling, deploy
   ```go
   // Add your business logic, error handling, logging, etc.
   ```

### Advanced Topics

- **[API Reference](api-reference.md)** - Full library documentation for refining generated code
- **[Getting Started Guide](codelab-intro.md)** - Learn the ssql library directly
- **[Advanced Tutorial](advanced-tutorial.md)** - Production patterns and optimization

### Key Features

ssql supports comprehensive data processing:
- **SQL Operations**: `join`, `distinct`, `offset`, `union`, `sort`, `limit`, `group-by`
- **Signal Processing**: `fft`, `ifft`, `convolve`, `correlate` (with optional GPU acceleration)
- **Multiple Formats**: CSV, TSV, JSON/JSONL, Apache Arrow, XLSX, WAV
- **Code Generation**: Convert CLI pipelines to standalone Go programs
- **Interactive Charts**: Chart.js visualizations with zoom/pan/export

### Need Help?

- **[Debugging Guide](cli-debugging.md)** - Learn to debug pipelines with jq
- **[Troubleshooting](cli-troubleshooting.md)** - Common issues and solutions
- **[GitHub Issues](https://github.com/rosscartlidge/ssql/issues)** - Report bugs
- **Examples** - Check `examples/` directory
- **API Reference** - Full library documentation

---

*Prototype fast with the CLI, deploy with confidence using generated Go code!* ⚡
