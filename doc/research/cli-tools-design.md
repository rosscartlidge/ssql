# ssql CLI Tools Design Document

## Executive Summary

This document outlines the design and implementation of command-line tools for ssql, enabling Unix-style pipelines for data processing. The CLI will leverage the existing gs (gogstools) framework for declarative command definition, intelligent tab completion, and clause-based filtering.

**Example Pipeline:**
```bash
cat data.csv |
  ssql read-csv |
  ssql where -match age gt 18 -match status eq active |
  ssql group -by age -function count -result count + -function avg -field score -result avg_score |
  ssql sort -field count -desc |
  ssql write-csv > output.csv
```

## Goals

1. **Unix Philosophy**: Small, composable tools that do one thing well
2. **Streaming Performance**: Process data in constant memory regardless of size
3. **Developer Experience**: Intelligent tab completion for fields, operators, and values
4. **Type Safety**: Maintain ssql's type system in CLI tools
5. **Discoverability**: Self-documenting with -help, -man, -bash-completion

## Architecture

### Binary Structure

**Single binary with subcommands**: `ssql <command> [options]`

- Single `cmd/ssql/main.go` entry point
- Each command in separate package: `cmd/ssql/commands/<command>/`
- Shared utilities in `cmd/ssql/lib/`

**Benefits:**
- Single installation: `go install github.com/rosscartlidge/ssql/cmd/ssql@latest`
- Consistent help system: `ssql -help` shows all commands
- Shared completion script: `ssql -bash-completion`

### Data Interchange Format: JSONL (JSON Lines)

**Format:** One JSON object per line, newline-delimited
```jsonl
{"name":"Alice","age":30,"email":"alice@example.com"}
{"name":"Bob","age":25,"email":"bob@example.com"}
{"name":"Carol","age":35,"email":"carol@example.com"}
```

**Advantages:**
1. **Streaming-friendly**: Parse line-by-line, no need to load entire file
2. **Type preservation**: int64, float64, bool, null preserved (unlike CSV)
3. **Nested structures**: Supports Records and arrays
4. **Error handling**: Malformed line doesn't corrupt entire stream
5. **Human-readable**: Easy to debug with `head`, `tail`, `grep`
6. **Standard**: Well-supported format with libraries in all languages

**Canonical Types in JSONL:**
- Integers: Always encode as JSON numbers representing int64
- Floats: Always encode as JSON numbers representing float64
- Strings: JSON strings
- Booleans: JSON true/false
- Null: JSON null for missing values
- Arrays: JSON arrays for iter.Seq values
- Objects: JSON objects for nested Records

## Core Commands (Phase 1)

### 1. read-csv
**Purpose:** Convert CSV to JSONL stream

**Usage:**
```bash
ssql read-csv data.csv
cat data.csv | ssql read-csv
```

**Config:**
```go
type ReadCSVConfig struct {
    HasHeader bool   `gs:"flag,global,last,help=CSV has header row,default=true"`
    Separator string `gs:"string,global,last,help=Field separator,default=,"`
    Argv      string `gs:"file,global,last,help=Input CSV file,suffix=.csv"`
}
```

**Features:**
- Auto-detect numeric types (consistent with ssql.ReadCSV)
- Read from file or stdin
- Output JSONL to stdout

### 2. write-csv
**Purpose:** Convert JSONL stream to CSV

**Usage:**
```bash
ssql write-csv output.csv
cat data.jsonl | ssql write-csv
```

**Config:**
```go
type WriteCSVConfig struct {
    Header    bool   `gs:"flag,global,last,help=Include header row,default=true"`
    Separator string `gs:"string,global,last,help=Field separator,default=,"`
    Argv      string `gs:"file,global,last,help=Output CSV file,suffix=.csv"`
}
```

**Features:**
- Flatten Records to CSV rows
- Write to file or stdout
- Auto-detect field order from first record

### 3. where
**Purpose:** Filter records with boolean conditions

**Usage:**
```bash
# Single condition
ssql where -match age gt 18

# Multiple AND conditions (multiple -match in same command)
ssql where -match age gt 18 -match status eq active

# OR conditions (+ separator starts new clause)
ssql where -match age gt 65 + -match age lt 18

# Complex: (A AND B) OR C
ssql where -match age gt 18 -match status eq active + -match department eq Engineering
```

**Config:**
```go
type WhereConfig struct {
    // Match uses multi-argument pattern: -match field op value
    // Multiple -match in same clause are ANDed, separate clauses (+) are ORed
    Match []map[string]interface{} `gs:"multi,local,list,args=field:op:value,help=Filter condition: field operator value"`
    Argv  string                   `gs:"file,global,last,help=Input JSONL file (or stdin if not specified),suffix=.jsonl"`
}
```

**Operators:**
- `eq`, `ne`: Equality for all types
- `gt`, `ge`, `lt`, `le`: Comparison for numbers and strings
- `contains`, `startswith`, `endswith`: String matching

**Implementation:**
- Each clause = one Where() filter
- Multiple clauses = Pipe(where1, where2, ...)
- `+` separator = OR logic (separate pipelines merged)

### 4. select
**Purpose:** Project and transform fields

**Usage:**
```bash
# Select specific fields (use + to separate multiple fields)
ssql select -field name + -field age

# Rename fields
ssql select -field name -as fullname + -field age

# Multiple field selection
ssql select -field name + -field age + -field department
```

**Config:**
```go
type SelectConfig struct {
    Field string `gs:"field,local,last,help=Field to select"`
    As    string `gs:"string,local,last,help=Rename field to"`
    Expr  string `gs:"string,local,last,help=Expression to compute"`
    Argv  string `gs:"file,global,last,help=Input JSONL file,suffix=.jsonl"`
}
```

**Implementation:**
- Maps to ssql.Select() for field projection
- Each clause specifies one output field

### 5. group
**Purpose:** GROUP BY with aggregations

**Usage:**
```bash
# Group by single field with aggregation
ssql group - fields department - agg 'count=count()' - agg 'avg_salary=avg(salary)'

# Group by multiple fields
ssql group - fields department - fields location - agg 'total=sum(sales)'
```

**Config:**
```go
type GroupByConfig struct {
    Fields []string `gs:"field,local,list,help=Fields to group by"`
    Agg    string   `gs:"string,local,last,help=Aggregation expression (name=func(field))"`
    Argv   string   `gs:"file,global,last,help=Input JSONL file,suffix=.jsonl"`
}
```

**Aggregation Functions:**
- `count()`: Count records
- `sum(field)`: Sum numeric field
- `avg(field)`: Average numeric field
- `min(field)`: Minimum value
- `max(field)`: Maximum value
- `first(field)`: First value
- `last(field)`: Last value

**Implementation:**
- Parse aggregation expressions: `name=func(field)`
- Maps to ssql.GroupByFields() + Aggregate()

### 6. sort
**Purpose:** Sort records by fields

**Usage:**
```bash
# Sort ascending
ssql sort -field age

# Sort descending
ssql sort -field age -desc

# Multi-field sort (future enhancement)
ssql sort -field department + -field age -desc
```

**Config:**
```go
type SortConfig struct {
    Field string `gs:"field,local,last,help=Field to sort by"`
    Desc  bool   `gs:"flag,local,last,help=Sort descending"`
    Argv  string `gs:"file,global,last,help=Input JSONL file,suffix=.jsonl"`
}
```

**Implementation:**
- Maps to ssql.SortBy() with field extraction
- Descending = negate numeric values

### 7. limit
**Purpose:** Take first N records

**Usage:**
```bash
ssql limit -n 100
```

**Config:**
```go
type LimitConfig struct {
    N    int    `gs:"number,global,last,help=Number of records to take"`
    Argv string `gs:"file,global,last,help=Input JSONL file,suffix=.jsonl"`
}
```

### 8. distinct
**Purpose:** Remove duplicate records

**Usage:**
```bash
# Distinct by all fields
ssql distinct

# Distinct by specific fields
ssql distinct - field name - field email
```

**Config:**
```go
type DistinctConfig struct {
    Field []string `gs:"field,local,list,help=Fields to check for uniqueness (empty = all fields)"`
    Argv  string   `gs:"file,global,last,help=Input JSONL file,suffix=.jsonl"`
}
```

**Implementation:**
- No fields specified = ssql.Distinct() on entire record
- Fields specified = Distinct by field subset

## gs Framework Enhancements

### 1. JSONL Field Completion

**Current:** gs reads TSV files for field completion
**Enhancement:** Add JSONL support

```go
// In gs/command.go
func (cmd *GSCommand) readJSONLFields(filename string) ([]string, error) {
    file, err := os.Open(filename)
    if err != nil {
        return nil, err
    }
    defer file.Close()

    // Read first record to extract field names
    scanner := bufio.NewScanner(file)
    if !scanner.Scan() {
        return nil, fmt.Errorf("empty JSONL file")
    }

    var record map[string]interface{}
    if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
        return nil, err
    }

    fields := make([]string, 0, len(record))
    for k := range record {
        fields = append(fields, k)
    }
    sort.Strings(fields)
    return fields, nil
}
```

### 2. Stdin Field Completion

**Problem:** When piping data, completion needs to read from stdin without consuming it

**Solution:** Support `-` as filename to read from stdin, cache field names

```go
// In gs/command.go
type CompletionContext struct {
    InputFile   string   // File or "-" for stdin
    CachedFields []string // Cached from stdin peek
}

func (cmd *GSCommand) detectFields(filename string) ([]string, error) {
    if filename == "-" || filename == "" {
        // Peek stdin for field names (read first line, then rewind)
        return cmd.peekStdinFields()
    }

    // Auto-detect format: .csv, .jsonl, .tsv
    if strings.HasSuffix(filename, ".jsonl") {
        return cmd.readJSONLFields(filename)
    }
    // ... existing TSV/CSV logic
}
```

### 3. Dynamic Operator Completion

**Enhancement:** Context-aware operator suggestions based on field type

```go
// In gs/types.go
type FieldMeta struct {
    // ... existing fields ...
    OperatorEnum func(fieldName string) []string `json:"-"` // Dynamic operator list
}

// In command.go
func (cfg *WhereConfig) GetOperators(fieldName string) []string {
    // Could inspect field type in data to suggest relevant operators
    // For now, return all operators
    return []string{"eq", "ne", "gt", "ge", "lt", "le", "contains", "startswith", "endswith"}
}
```

### 4. Generalized Input Detection

**Enhancement:** Auto-detect stdin vs file input

```go
// In cmd/ssql/lib/input.go
func OpenInput(filename string) (iter.Seq[ssql.Record], error) {
    var reader io.Reader

    if filename == "" || filename == "-" {
        // Check if stdin has data
        stat, _ := os.Stdin.Stat()
        if (stat.Mode() & os.ModeCharDevice) == 0 {
            reader = os.Stdin
        } else {
            return nil, fmt.Errorf("no input provided (use file or pipe data to stdin)")
        }
    } else {
        file, err := os.Open(filename)
        if err != nil {
            return nil, err
        }
        reader = file
    }

    return ReadJSONL(reader)
}
```

### 5. Clause-to-Filter Composition

**Enhancement:** Helper to convert gs.ClauseSet to ssql Filter

```go
// In cmd/ssql/lib/compose.go
type ClauseFilter func(clause gs.ClauseSet) ssql.Filter[ssql.Record, ssql.Record]

func ComposeFilters(clauses []gs.ClauseSet, makeFilter ClauseFilter) ssql.Filter[ssql.Record, ssql.Record] {
    if len(clauses) == 0 {
        return func(seq iter.Seq[ssql.Record]) iter.Seq[ssql.Record] {
            return seq // Identity filter
        }
    }

    // Each clause = one filter, compose with Pipe()
    filters := make([]ssql.Filter[ssql.Record, ssql.Record], len(clauses))
    for i, clause := range clauses {
        filters[i] = makeFilter(clause)
    }

    return ssql.Pipe(filters...)
}
```

## Implementation Plan

### Phase 1: Foundation (Week 1)
1. **gs Framework Enhancements**
   - Add JSONL field reading
   - Add stdin detection and peeking
   - Add dynamic operator completion
   - Test with existing gogstools examples

2. **CLI Infrastructure**
   - Create `cmd/ssql/main.go` with command router
   - Create `cmd/ssql/lib/` with shared utilities:
     - `input.go`: JSONL reading from stdin/file
     - `output.go`: JSONL writing to stdout/file
     - `compose.go`: Clause-to-Filter helpers
   - Implement subcommand registration system

3. **Basic I/O Commands**
   - Implement `read-csv` command
   - Implement `write-csv` command
   - Test: `cat data.csv | ssql read-csv | ssql write-csv`

### Phase 2: Core Filtering (Week 2)
1. **where command**
   - Implement all operators (eq, ne, gt, lt, ...)
   - Support AND logic (multiple clauses with -)
   - Support OR logic (+ separator)
   - Test complex conditions

2. **select command**
   - Field projection
   - Field renaming
   - Test: `ssql read-csv data.csv | ssql select - field name - field age`

3. **limit command**
   - Simple record limiting
   - Test with large datasets

### Phase 3: Aggregation (Week 3)
1. **group command**
   - Parse aggregation expressions
   - Implement all aggregation functions
   - Support multi-field grouping
   - Test: `ssql group - fields dept - agg 'count=count()' - agg 'avg=avg(salary)'`

2. **sort command**
   - Single and multi-field sorting
   - Ascending and descending
   - Test with numeric and string fields

3. **distinct command**
   - Full record deduplication
   - Field-specific deduplication

### Phase 4: Polish (Week 4)
1. **Documentation**
   - Command reference docs
   - Tutorial with real-world examples
   - Performance benchmarks

2. **Testing**
   - Integration tests for each command
   - End-to-end pipeline tests
   - Performance tests with large datasets

3. **Completion Enhancement**
   - Ensure field completion works in all commands
   - Test operator completion in where
   - Test value completion in where

## Example Use Cases

### Use Case 1: Data Cleaning
```bash
# Remove invalid records, select relevant fields, deduplicate
cat users.csv |
  ssql read-csv |
  ssql where -match age gt 0 -match email contains '@' |
  ssql select -field name + -field email + -field age |
  ssql distinct -field email |
  ssql write-csv > clean_users.csv
```

### Use Case 2: Analytics
```bash
# Find top Engineering employees by salary
cat employees.csv |
  ssql read-csv |
  ssql where -match department eq Engineering |
  ssql select -field name + -field salary + -field age |
  ssql sort -field salary -desc |
  ssql limit -n 10 |
  ssql write-csv > top_engineers.csv
```

### Use Case 3: Complex Filtering
```bash
# Find employees who are either senior (age > 40) or high earners (salary > 100k)
ssql read-csv employees.csv |
  ssql where -match age gt 40 + -match salary gt 100000 |
  ssql sort -field salary -desc |
  ssql write-csv > senior_or_high_earners.csv
```

### Use Case 4: Multi-criteria Filtering
```bash
# Find young Engineering employees OR any Sales employees
ssql read-csv employees.csv |
  ssql where -match age lt 30 -match department eq Engineering + -match department eq Sales |
  ssql select -field name + -field department + -field age |
  ssql write-csv > filtered_employees.csv
```

## Design Decisions

### Why JSONL instead of CSV for interchange?
- **Type preservation**: CSV loses type information (everything is a string)
- **Streaming**: Both are line-delimited and stream-friendly
- **Nested data**: JSONL supports Records and arrays
- **Error handling**: Malformed line in JSONL doesn't corrupt stream

### Why single binary with subcommands?
- **Installation**: Single `go install` command
- **Discoverability**: `ssql -help` shows all commands
- **Consistency**: Shared flags (-help, -bash-completion, -man)
- **Completion**: Single completion script for all commands

### Why clause-based syntax with - and +?
- **Composability**: Matches ssql's Pipe() mental model
- **Readability**: Clear separation of arguments
- **Power**: OR logic (+) and AND logic (-) in same command
- **Completion**: gs framework handles parsing automatically

### Why enhance gs framework instead of starting fresh?
- **Proven design**: Already works well in gogstools
- **Tab completion**: Field and content completion already implemented
- **Clause parsing**: Handles - and + separators correctly
- **Extensibility**: Easy to add new field types and completion modes
- **No users yet**: Free to modify without breaking changes

## Code Generation: CLI to Production Go

### Overview

One of the most powerful features of the ssql CLI is the ability to **prototype pipelines interactively**, then convert them to **production-quality Go code** for deployment. This workflow combines the rapid iteration of CLI tools with the performance and type safety of compiled Go programs.

### Self-Generating Command Architecture (IMPLEMENTED)

**Status:** ✅ Implemented and deployed

The ssql CLI uses a revolutionary **self-generating command architecture** where each command generates its own Go code. This eliminates the need for a separate parser and ensures generated code always stays in sync with command implementations.

#### Architecture Design

**Core Concept:** Each CLI command has dual modes:
1. **Execution Mode** (default): Processes data through the pipeline
2. **Generation Mode** (`-generate` flag): Emits Go code fragments

**Data Flow:**
```
┌─────────────┐  fragment  ┌─────────────┐  fragment  ┌─────────────┐  fragments  ┌──────────────┐
│  read-csv   │────JSON───>│    where    │────JSON───>│  write-csv  │─────JSON───>│ generate go  │
│  -generate  │   Line     │  -generate  │   Line     │  -generate  │    Lines    │              │
└─────────────┘            └─────────────┘            └─────────────┘             └──────────────┘
     init                       stmt                       final                    assembles to
   fragment                   fragment                   fragment                   complete Go
```

**Fragment Types:**
- **init**: First command (e.g., read-csv), creates initial variable
- **stmt**: Middle command (e.g., where, select), has input and output variable
- **final**: Last command (e.g., write-csv), has input but no output variable

#### Code Fragment Protocol

**Format:** JSONL (JSON Lines) for inter-command communication

**Fragment Structure:**
```go
type CodeFragment struct {
    Type    string   `json:"type"`    // "init", "stmt", "final"
    Var     string   `json:"var"`     // Output variable name (e.g., "filtered")
    Input   string   `json:"input"`   // Input variable name from previous command
    Code    string   `json:"code"`    // Go code for this operation
    Imports []string `json:"imports"` // Required imports (e.g., ["strings"])
}
```

**Example Fragment Flow:**

1. **read-csv -generate data.csv** outputs:
```json
{"type":"init","var":"records","input":"","code":"records := ssql.ReadCSV(\"data.csv\")","imports":null}
```

2. **where -generate -match age gt 30** outputs:
```json
{"type":"init","var":"records","input":"","code":"records := ssql.ReadCSV(\"data.csv\")","imports":null}
{"type":"stmt","var":"filtered","input":"records","code":"filtered := ssql.Where(func(r ssql.Record) bool {\n\t\treturn r[\"age\"].(float64) > 30\n\t})(records)","imports":null}
```

3. **write-csv -generate** outputs:
```json
{"type":"init","var":"records","input":"","code":"records := ssql.ReadCSV(\"data.csv\")","imports":null}
{"type":"stmt","var":"filtered","input":"records","code":"filtered := ssql.Where(func(r ssql.Record) bool {\n\t\treturn r[\"age\"].(float64) > 30\n\t})(records)","imports":null}
{"type":"final","var":"","input":"filtered","code":"ssql.WriteCSV(\"\", filtered)","imports":null}
```

4. **generate go** assembles all fragments:
```go
package main

import (
	"github.com/rosscartlidge/ssql"
)

func main() {
	records := ssql.ReadCSV("data.csv")
	filtered := ssql.Where(func(r ssql.Record) bool {
		return r["age"].(float64) > 30
	})(records)
	ssql.WriteCSV("", filtered)
}
```

#### Implementation Details

**File Structure:**
- `cmd/ssql/lib/codefragment.go` - Fragment utilities
- Each command implements `generateCode()` method
- Commands use `ReadAllCodeFragments()` and `WriteCodeFragment()`

**Fragment Pass-Through Pattern:**
```go
func (c *WhereConfig) generateCode(clauses []gs.ClauseSet) error {
    // 1. Read ALL previous fragments from stdin
    fragments, err := lib.ReadAllCodeFragments()
    if err != nil {
        return fmt.Errorf("reading code fragments: %w", err)
    }

    // 2. Pass through all previous fragments unchanged
    for _, frag := range fragments {
        if err := lib.WriteCodeFragment(frag); err != nil {
            return fmt.Errorf("writing previous fragment: %w", err)
        }
    }

    // 3. Get input variable from last fragment
    var inputVar string
    if len(fragments) > 0 {
        inputVar = fragments[len(fragments)-1].Var
    } else {
        inputVar = "records"
    }

    // 4. Generate this command's code
    filterCode, imports := generateWhereCode(clauses)
    code := fmt.Sprintf("filtered := ssql.Where(%s)(%s)", filterCode, inputVar)

    // 5. Write this command's fragment
    frag := lib.NewStmtFragment("filtered", inputVar, code, imports)
    return lib.WriteCodeFragment(frag)
}
```

**Variable Chaining:**
- Each fragment specifies its input and output variables
- Variables chain through pipeline: `records → filtered → selected → limited`
- Final command uses last variable but produces no output variable

**Import Tracking:**
- Each fragment specifies required imports
- `generate go` deduplicates and sorts imports
- Example: `where -match name contains Alice` adds `"strings"` import

#### Implementation Example: where Command

**Command with AND/OR Logic:**
```bash
ssql where -match age gt 30 -match department eq Engineering + -match salary gt 100000
# Means: (age > 30 AND department = Engineering) OR (salary > 100000)
```

**Generated Code:**
```go
filtered := ssql.Where(func(r ssql.Record) bool {
    return (r["age"].(float64) > 30 && r["department"] == "Engineering") ||
           r["salary"].(float64) > 100000
})(records)
```

**Key Features:**
- Multiple `-match` in same clause = AND logic
- `+` separator starts new clause = OR logic
- Type detection for numeric vs string comparisons
- Import tracking (e.g., `strings` for `contains` operator)
- Proper parenthesization of complex expressions

#### Benefits of Self-Generating Architecture

**1. Self-Describing**
- Each command knows how to generate its own code
- No separate parser that can get out of sync
- Command behavior and code generation always match

**2. Maintainable**
- Adding new command: just implement `generateCode()`
- Changing command: update both execution and generation together
- No fragile regex-based shell parsing

**3. Composable**
- Fragments chain naturally through pipeline
- Each command only knows about itself
- Clean separation of concerns

**4. Correct**
- Generated code uses same logic as CLI execution
- Type handling (int64, float64) matches library
- Import requirements automatically tracked

**5. Testable**
- Can test code generation independently
- Each command's generation can be unit tested
- End-to-end pipeline tests verify complete flow

#### Required Pattern for New Commands

**⚠️ CRITICAL:** Every new CLI command MUST implement `-generate` support or code generation pipelines will break.

**Checklist for Adding New Command:**

1. **Add Generate flag to config:**
```go
type MyCommandConfig struct {
    Generate bool   `gs:"flag,global,last,help=Generate Go code instead of executing"`
    // ... other fields
}
```

2. **Branch in Execute():**
```go
func (c *MyCommandConfig) Execute(ctx context.Context, clauses []gs.ClauseSet) error {
    if c.Generate {
        return c.generateCode(clauses)
    }
    // ... normal execution
}
```

3. **Implement generateCode():**
```go
func (c *MyCommandConfig) generateCode(clauses []gs.ClauseSet) error {
    // Read all previous fragments
    fragments, err := lib.ReadAllCodeFragments()
    if err != nil {
        return fmt.Errorf("reading code fragments: %w", err)
    }

    // Pass through all previous fragments
    for _, frag := range fragments {
        if err := lib.WriteCodeFragment(frag); err != nil {
            return fmt.Errorf("writing previous fragment: %w", err)
        }
    }

    // Get input variable from last fragment
    var inputVar string
    if len(fragments) > 0 {
        inputVar = fragments[len(fragments)-1].Var
    } else {
        inputVar = "records"
    }

    // Generate your code
    code := fmt.Sprintf("output := myFunc(%s)", inputVar)

    // Create and write your fragment
    frag := lib.NewStmtFragment("output", inputVar, code, []string{"import1"})
    return lib.WriteCodeFragment(frag)
}
```

#### Usage Examples

**Simple Pipeline:**
```bash
ssql read-csv -generate data.csv | \
  ssql where -generate -match age gt 18 | \
  ssql write-csv -generate output.csv | \
  ssql generate go > main.go
```

**Complex Filtering:**
```bash
# (age > 30 AND status = active) OR (department = Engineering)
ssql read-csv -generate employees.csv | \
  ssql where -generate \
    -match age gt 30 -match status eq active + \
    -match department eq Engineering | \
  ssql write-csv -generate filtered.csv | \
  ssql generate go > main.go
```

**Build and Run:**
```bash
# Compile generated code
go mod init myproject
go get github.com/rosscartlidge/ssql@latest
go build -o myproject main.go

# Run compiled binary (10-100x faster than CLI)
./myproject
```

#### Comparison: Old vs New Architecture

**Old Approach (Removed):**
- Separate shell pipeline parser (`lib/pipeline.go`)
- Separate code generator (`lib/codegen.go`)
- Fragile regex-based command parsing
- Easy to get out of sync with implementations
- ~540 lines of complex code

**New Approach (Current):**
- Each command generates its own code
- Fragment-based communication protocol
- No parsing - commands know themselves
- Always in sync by design
- Simpler, more maintainable

#### Testing Code Generation

**Test Fragment Output:**
```bash
# Test individual command generation
ssql where -generate -match age gt 30 < input_fragment.json

# Verify output is valid JSON
ssql where -generate -match age gt 30 < input_fragment.json | jq .
```

**Test Complete Pipeline:**
```bash
# Generate code
ssql read-csv -generate data.csv | \
  ssql where -generate -match age gt 30 | \
  ssql generate go > test.go

# Verify it compiles
go build test.go

# Verify it produces correct output
./test
```

#### Future Enhancements

**Potential improvements:**
1. Optimize variable naming (avoid `filtered0`, `filtered1`)
2. Add comments to generated code explaining each step
3. Support for custom variable names via `-as` flag
4. Generate with or without error handling based on flag
5. Output format options (compact vs readable)

**Workflow:**
1. **Prototype**: Use CLI to explore data and build pipeline interactively
2. **Test**: Verify pipeline produces correct results with real data
3. **Convert**: Generate equivalent Go code using ssql library
4. **Optimize**: Add type safety, error handling, custom logic
5. **Deploy**: Compile single binary, 10-100x faster than CLI

### Why Code Generation?

**CLI Advantages (Prototyping):**
- Fast iteration: No compile step
- Interactive: Tab completion, immediate feedback
- Exploratory: Easy to try different operations
- Low overhead: Perfect for one-off data tasks

**Generated Code Advantages (Production):**
- **Performance**: 10-100x faster (no process spawning, JSON parsing per command)
- **Type safety**: Compile-time checks, custom types
- **Single binary**: No shell dependencies, easy deployment
- **Customization**: Add business logic, error handling, monitoring
- **Maintainability**: Code review, version control, testing

### CLI-to-Go Mapping

The ssql CLI has a **1:1 mapping** with the library API, making code generation straightforward:

#### Example 1: Simple Filter Pipeline

**CLI Pipeline:**
```bash
ssql read-csv data.csv | \
  ssql where -field age -op gt -value 18 | \
  ssql select -field name -field email | \
  ssql limit -n 100 | \
  ssql write-csv output.csv
```

**Generated Go Code:**
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql"
)

func main() {
    // Read CSV
    records := ssql.ReadCSV("data.csv")

    // Where: age > 18
    filtered := ssql.Where(func(r ssql.Record) bool {
        if age, ok := r["age"].(float64); ok {
            return age > 18
        }
        return false
    })(records)

    // Select: name, email
    selected := ssql.Select(func(r ssql.Record) ssql.Record {
        return ssql.Record{
            "name": r["name"],
            "email": r["email"],
        }
    })(filtered)

    // Limit: 100
    limited := ssql.Limit[ssql.Record](100)(selected)

    // Write CSV
    if err := ssql.WriteCSV("output.csv", limited); err != nil {
        log.Fatalf("Error writing CSV: %v", err)
    }
}
```

#### Example 2: Group By with Aggregation

**CLI Pipeline:**
```bash
ssql read-csv sales.csv | \
  ssql group -fields region -fields product \
    -agg 'total=sum(amount)' -agg 'count=count()' | \
  ssql sort -field total -desc | \
  ssql limit -n 10 | \
  ssql write-csv top_sales.csv
```

**Generated Go Code:**
```go
package main

import (
    "log"
    "github.com/rosscartlidge/ssql"
)

func main() {
    // Read CSV
    records := ssql.ReadCSV("sales.csv")

    // Group by: region, product
    grouped := ssql.GroupByFields("region", "product")(records)

    // Aggregate: total=sum(amount), count=count()
    aggregated := ssql.Aggregate(
        ssql.Sum("amount", "total"),
        ssql.Count("count"),
    )(grouped)

    // Flatten groups to records
    flattened := ssql.FlattenGroups(aggregated)

    // Sort by: total descending
    sorted := ssql.SortBy(func(r ssql.Record) float64 {
        if total, ok := r["total"].(float64); ok {
            return -total // Negative for descending
        }
        return 0
    })(flattened)

    // Limit: 10
    limited := ssql.Limit[ssql.Record](10)(sorted)

    // Write CSV
    if err := ssql.WriteCSV("top_sales.csv", limited); err != nil {
        log.Fatalf("Error writing CSV: %v", err)
    }
}
```

#### Example 3: Complex Where Conditions

**CLI Pipeline:**
```bash
ssql read-csv users.csv | \
  ssql where -field age -op gt -value 18 -field status -op eq -value active | \
  ssql write-csv adult_active.csv
```

**Generated Go Code:**
```go
package main

import (
    "log"
    "github.com/rosscartlidge/ssql"
)

func main() {
    records := ssql.ReadCSV("users.csv")

    // Where: age > 18 AND status = active
    filtered := ssql.Where(func(r ssql.Record) bool {
        age, ageOk := r["age"].(float64)
        status, statusOk := r["status"].(string)

        return ageOk && age > 18 &&
               statusOk && status == "active"
    })(records)

    if err := ssql.WriteCSV("adult_active.csv", filtered); err != nil {
        log.Fatalf("Error writing CSV: %v", err)
    }
}
```

### Implementation Architecture

There are three approaches to implementing code generation:

#### Approach 1: Simple Code Generator (Recommended for MVP)

**Command:** `ssql generate go`

**Usage:**
```bash
# Generate from shell history
ssql generate go < pipeline.sh > main.go

# Or inline
echo 'ssql read-csv data.csv | ssql where -field age -op gt -value 18' | \
  ssql generate go > main.go

# Then compile and run
go mod init myproject
go get github.com/rosscartlidge/ssql
go build -o myproject main.go
./myproject
```

**Implementation:**
```go
// cmd/ssql/commands/generatego.go
type GenerateGoConfig struct {
    Output string `gs:"file,global,last,help=Output Go file,suffix=.go"`
}

func (c *generateGoCommand) Execute(ctx context.Context, args []string) error {
    // Parse pipeline from stdin or args
    pipeline, err := parsePipeline(os.Stdin)
    if err != nil {
        return fmt.Errorf("parsing pipeline: %w", err)
    }

    // Generate Go code
    code, err := generateGoCode(pipeline)
    if err != nil {
        return fmt.Errorf("generating code: %w", err)
    }

    // Write to output
    if c.config.Output != "" {
        return os.WriteFile(c.config.Output, []byte(code), 0644)
    }
    fmt.Println(code)
    return nil
}
```

#### Approach 2: Interactive Pipeline Recorder

**Command:** `ssql record-pipeline`

**Usage:**
```bash
# Start recording
ssql record-pipeline start my-pipeline

# Run commands normally (recorded in background)
ssql read-csv data.csv | ssql where -field age -op gt -value 18

# Stop and generate code
ssql record-pipeline stop
ssql record-pipeline generate my-pipeline.go
```

**Implementation:** Records commands to temporary file, converts on demand.

#### Approach 3: Built-in Generation Flag

**Usage:**
```bash
# Run pipeline normally, but also generate code
ssql --generate go=output.go \
  read-csv data.csv | \
  ssql where -field age -op gt -value 18 | \
  ssql write-csv result.csv
```

**Implementation:** Each command logs itself to shared context, main() generates code at exit.

### Code Generation Engine

**Core Components:**

1. **Pipeline Parser**: Parse shell command into structured representation
2. **AST Builder**: Build abstract syntax tree of operations
3. **Code Generator**: Convert AST to Go code with proper imports, types, error handling

**Pipeline Parser:**
```go
// lib/pipeline.go
type Command struct {
    Name   string            // "read-csv", "where", "limit"
    Args   map[string]string // Flag values
    Stdin  bool              // True if reads from stdin
    Stdout bool              // True if writes to stdout
}

type Pipeline struct {
    Commands []Command
}

func ParsePipeline(input string) (*Pipeline, error) {
    // Split by | and parse each command
    parts := strings.Split(input, "|")
    commands := make([]Command, 0, len(parts))

    for _, part := range parts {
        cmd, err := parseCommand(strings.TrimSpace(part))
        if err != nil {
            return nil, err
        }
        commands = append(commands, cmd)
    }

    return &Pipeline{Commands: commands}, nil
}

func parseCommand(cmdLine string) (Command, error) {
    // Parse: ssql where -field age -op gt -value 18
    fields := strings.Fields(cmdLine)
    if len(fields) < 2 || fields[0] != "ssql" {
        return Command{}, fmt.Errorf("invalid command: %s", cmdLine)
    }

    cmd := Command{
        Name: fields[1],
        Args: make(map[string]string),
    }

    // Parse flags
    for i := 2; i < len(fields); i += 2 {
        if !strings.HasPrefix(fields[i], "-") {
            continue
        }
        flag := strings.TrimPrefix(fields[i], "-")
        if i+1 < len(fields) {
            cmd.Args[flag] = fields[i+1]
        }
    }

    return cmd, nil
}
```

**Code Generator:**
```go
// lib/codegen.go
type CodeGenerator struct {
    buf    *bytes.Buffer
    indent int
}

func GenerateGoCode(pipeline *Pipeline) (string, error) {
    g := &CodeGenerator{buf: new(bytes.Buffer)}

    // Generate package and imports
    g.writePackage()
    g.writeImports(pipeline)
    g.writeMainFunc(pipeline)

    return g.buf.String(), nil
}

func (g *CodeGenerator) writeMainFunc(p *Pipeline) {
    g.writeln("func main() {")
    g.indent++

    varName := "records"
    for i, cmd := range p.Commands {
        switch cmd.Name {
        case "read-csv":
            g.writeln(fmt.Sprintf("%s := ssql.ReadCSV(%q)",
                varName, cmd.Args["file"]))

        case "where":
            newVar := fmt.Sprintf("filtered%d", i)
            g.writeWhere(varName, newVar, cmd)
            varName = newVar

        case "limit":
            newVar := fmt.Sprintf("limited%d", i)
            g.writeln(fmt.Sprintf("%s := ssql.Limit[ssql.Record](%s)(%s)",
                newVar, cmd.Args["n"], varName))
            varName = newVar

        case "write-csv":
            g.writeWriteCSV(varName, cmd)
        }
    }

    g.indent--
    g.writeln("}")
}

func (g *CodeGenerator) writeWhere(input, output string, cmd Command) {
    field := cmd.Args["field"]
    op := cmd.Args["op"]
    value := cmd.Args["value"]

    g.writeln(fmt.Sprintf("%s := ssql.Where(func(r ssql.Record) bool {", output))
    g.indent++

    // Generate comparison based on operator
    switch op {
    case "gt":
        g.writeln(fmt.Sprintf("if val, ok := r[%q].(float64); ok {", field))
        g.indent++
        g.writeln(fmt.Sprintf("return val > %s", value))
        g.indent--
        g.writeln("}")
    case "eq":
        g.writeln(fmt.Sprintf("return r[%q] == %q", field, value))
    // ... other operators
    }

    g.writeln("return false")
    g.indent--
    g.writeln(fmt.Sprintf("})(%s)", input))
}
```

**Generated Code Template:**
```go
package main

import (
    "fmt"
    "log"
    "github.com/rosscartlidge/ssql"
)

func main() {
    // Generated pipeline code here
    {{range .Commands}}
    {{.Generate}}
    {{end}}
}
```

### Command-Specific Generation

**Conversion Complexity:**

| Command | Complexity | Notes |
|---------|-----------|-------|
| read-csv | Trivial | Direct function call |
| write-csv | Trivial | Direct function call |
| where | Simple | Generate comparison function |
| select | Simple | Generate projection function |
| limit | Trivial | Direct function call with type param |
| sort | Simple | Generate sort key function |
| group | Moderate | Parse aggregation expressions |
| distinct | Simple | Direct function call |

**Where Command Generation:**

```go
func generateWhere(cmd Command) string {
    field := cmd.Args["field"]
    op := cmd.Args["op"]
    value := cmd.Args["value"]

    // Detect value type
    valueType := detectType(value)

    switch op {
    case "eq", "ne":
        return fmt.Sprintf(
            `ssql.Where(func(r ssql.Record) bool {
                return r[%q] %s %s
            })`, field, opToSymbol(op), formatValue(value, valueType))

    case "gt", "ge", "lt", "le":
        return fmt.Sprintf(
            `ssql.Where(func(r ssql.Record) bool {
                if val, ok := r[%q].(float64); ok {
                    return val %s %s
                }
                return false
            })`, field, opToSymbol(op), value)

    case "contains":
        return fmt.Sprintf(
            `ssql.Where(func(r ssql.Record) bool {
                if val, ok := r[%q].(string); ok {
                    return strings.Contains(val, %q)
                }
                return false
            })`, field, value)
    }
}
```

**Group-By Command Generation:**

```go
func generateGroupBy(cmd Command) string {
    fields := strings.Split(cmd.Args["fields"], ",")
    aggs := parseAggregations(cmd.Args["agg"])

    var buf bytes.Buffer

    // Generate GroupByFields
    buf.WriteString("grouped := ssql.GroupByFields(")
    for i, field := range fields {
        if i > 0 {
            buf.WriteString(", ")
        }
        buf.WriteString(fmt.Sprintf("%q", field))
    }
    buf.WriteString(")(records)\n")

    // Generate Aggregate
    buf.WriteString("aggregated := ssql.Aggregate(\n")
    for i, agg := range aggs {
        if i > 0 {
            buf.WriteString(",\n")
        }
        buf.WriteString("    " + generateAggFunc(agg))
    }
    buf.WriteString("\n)(grouped)\n")

    // Flatten
    buf.WriteString("records = ssql.FlattenGroups(aggregated)\n")

    return buf.String()
}

func generateAggFunc(agg Aggregation) string {
    switch agg.Func {
    case "count":
        return fmt.Sprintf("ssql.Count(%q)", agg.OutputName)
    case "sum":
        return fmt.Sprintf("ssql.Sum(%q, %q)", agg.Field, agg.OutputName)
    case "avg":
        return fmt.Sprintf("ssql.Avg(%q, %q)", agg.Field, agg.OutputName)
    case "min":
        return fmt.Sprintf("ssql.Min(%q, %q)", agg.Field, agg.OutputName)
    case "max":
        return fmt.Sprintf("ssql.Max(%q, %q)", agg.Field, agg.OutputName)
    }
    return ""
}
```

### Prototype to Production Workflow

**Step 1: Interactive Prototyping**
```bash
# Explore data structure
ssql read-csv data.csv | head -5

# Try different filters
ssql read-csv data.csv | ssql where -field age -op gt -value 18 | head
ssql read-csv data.csv | ssql where -field status -op eq -value active | head

# Build full pipeline
ssql read-csv data.csv | \
  ssql where -field age -op gt -value 18 | \
  ssql select -field name -field email | \
  ssql write-csv > output.csv
```

**Step 2: Save Working Pipeline**
```bash
# Save to script
cat > pipeline.sh <<'EOF'
ssql read-csv data.csv | \
  ssql where -field age -op gt -value 18 | \
  ssql select -field name -field email | \
  ssql write-csv
EOF

chmod +x pipeline.sh
./pipeline.sh > output.csv
```

**Step 3: Generate Go Code**
```bash
# Generate production code
ssql generate go < pipeline.sh > main.go

# Review generated code
cat main.go

# Initialize Go module
go mod init myproject
go get github.com/rosscartlidge/ssql@latest

# Build binary
go build -o myproject main.go
```

**Step 4: Optimize and Deploy**
```go
// Customize generated code
package main

import (
    "flag"
    "log"
    "github.com/rosscartlidge/ssql"
)

func main() {
    // Add command-line flags
    inputFile := flag.String("input", "data.csv", "Input CSV file")
    outputFile := flag.String("output", "output.csv", "Output CSV file")
    minAge := flag.Float64("min-age", 18, "Minimum age")
    flag.Parse()

    // Read CSV
    records := ssql.ReadCSV(*inputFile)

    // Filter by age (using variable instead of hardcoded value)
    filtered := ssql.Where(func(r ssql.Record) bool {
        if age, ok := r["age"].(float64); ok {
            return age > *minAge
        }
        return false
    })(records)

    // Select fields
    selected := ssql.Select(func(r ssql.Record) ssql.Record {
        return ssql.Record{
            "name": r["name"],
            "email": r["email"],
        }
    })(filtered)

    // Write CSV
    if err := ssql.WriteCSV(*outputFile, selected); err != nil {
        log.Fatalf("Error writing CSV: %v", err)
    }
}
```

**Step 5: Benchmark Performance**
```bash
# Time CLI version
time ./pipeline.sh > /dev/null
# Result: 2.5 seconds

# Time compiled version
time ./myproject
# Result: 0.08 seconds (31x faster!)
```

### Benefits Summary

**Development Speed:**
- CLI prototype in minutes
- Generated code in seconds
- Optimization as needed

**Performance Gains:**
- No process spawning overhead
- No JSONL parsing between commands
- Single-pass processing where possible
- Compiled optimizations

**Production Benefits:**
- Single binary deployment
- Type-safe custom logic
- Integrated error handling
- Version controlled source code
- Easy to test and maintain

**Typical Performance Improvements:**
- Simple pipelines (read → filter → write): **10-30x faster**
- Complex pipelines (group, aggregations): **50-100x faster**
- Large datasets (>1GB): **100-200x faster** (memory efficiency)

## Success Metrics

1. **Performance**: Process 1GB CSV file in < 10 seconds on standard hardware
2. **Memory**: Constant memory usage regardless of input size (streaming)
3. **Composability**: All commands work in pipelines with JSONL
4. **Usability**: Tab completion for fields, operators, and values works
5. **Correctness**: All operations match ssql library behavior exactly
6. **Code Generation**: Generate working Go code from any valid pipeline
7. **Performance Gain**: Generated code 10-100x faster than CLI equivalent

## Future Enhancements (Phase 2)

1. **join command**: Merge two JSONL streams
2. **Expression evaluation**: `select - expr 'age * 2 + 10' - as computed`
3. **Window functions**: `over - partition_by dept - order_by salary`
4. **Sampling**: `sample - rate 0.1` (10% of records)
5. **Statistics**: `stats - field salary` (min, max, avg, stddev, percentiles)
6. **chart command**: Generate Chart.js visualizations from CLI
7. **Parallel processing**: `parallel - workers 8` for CPU-intensive operations
8. **SQL mode**: `ssql sql "SELECT name, age FROM stdin WHERE age > 18"`

## References

- ssql Library: `/home/rossc/src/ssql/`
- gs Framework: `/home/rossc/tsv/gogstools/gs/`
- JSONL Specification: https://jsonlines.org/
- Unix Philosophy: https://en.wikipedia.org/wiki/Unix_philosophy
