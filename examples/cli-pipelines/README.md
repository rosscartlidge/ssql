# StreamV3 CLI Pipeline Examples

This directory contains example bash scripts demonstrating StreamV3 CLI usage patterns.

## Prerequisites

### Install StreamV3

**Option 1: Install from source (latest version with clause support)**
```bash
cd /home/rossc/src/ssql
go install ./cmd/ssql
```

**Option 2: Install from GitHub (once pushed)**
```bash
go install github.com/rosscartlidge/ssql/cmd/ssql@latest
```

**Option 3: Use local binary**
```bash
# Build in project root
cd /home/rossc/src/ssql
go build ./cmd/ssql

# Use with absolute path in examples
export PATH="/home/rossc/src/ssql:$PATH"
```

### Enable Bash Completion (optional)
```bash
eval "$(ssql -bash-completion)"
```

### Verify Installation
```bash
ssql -help
ssql where -help
```

## Examples

### 01-basic-filtering.sh
**Demonstrates**: AND logic with multiple conditions
```bash
# Find Engineering employees over 30
ssql where -match age gt 30 -match department eq Engineering
```
- Multiple `-match` in same command = AND logic
- All conditions must be true

### 02-or-logic.sh
**Demonstrates**: OR logic using `+` separator
```bash
# Find employees who are young OR high earners
ssql where -match age lt 26 + -match salary gt 100000
```
- The `+` separator creates a new clause
- Either clause can match (OR logic)

### 03-complex-and-or.sh
**Demonstrates**: Complex boolean logic combining AND/OR
```bash
# (age > 30 AND dept = Engineering) OR (salary < 70000)
ssql where -match age gt 30 -match department eq Engineering + -match salary lt 70000
```
- Within clause: multiple `-match` = AND
- Between clauses: `+` separator = OR

### 04-select-sort-limit.sh
**Demonstrates**: Field projection, sorting, and limiting results
```bash
# Top 3 earners with just name and salary
ssql include name salary | \
  ssql sort salary -desc | \
  ssql limit 3
```
- `include` selects only specified fields
- `sort -desc` for descending order
- `limit` takes first N records

### 05-full-pipeline.sh
**Demonstrates**: Complete end-to-end data processing pipeline
```bash
# Full pipeline: filter, include, sort, limit, export
ssql from employees.csv | \
  ssql where -match department eq Engineering | \
  ssql include name age salary | \
  ssql sort salary -desc | \
  ssql limit 3 | \
  ssql to csv > output.csv
```
- Chains multiple operations
- Each step transforms the stream
- Outputs to new CSV file

### 06-string-operators.sh
**Demonstrates**: String matching operators
```bash
# Find emails containing 'engineering'
ssql where -match email contains engineering

# Find emails ending with '.org'
ssql where -match email endswith .org

# Find names starting with 'C'
ssql where -match name startswith C
```
- `contains`: substring matching
- `startswith`: prefix matching
- `endswith`: suffix matching

## Running Examples

Make scripts executable:
```bash
chmod +x *.sh
```

Run an example:
```bash
./01-basic-filtering.sh
```

Or run all examples:
```bash
for script in *.sh; do
  echo "Running $script..."
  ./"$script"
  echo
done
```

## Operators Reference

### Comparison Operators
- `eq` - Equal to
- `ne` - Not equal to
- `gt` - Greater than
- `ge` - Greater than or equal
- `lt` - Less than
- `le` - Less than or equal

### String Operators
- `contains` - String contains substring
- `startswith` - String starts with prefix
- `endswith` - String ends with suffix

## Clause Logic

**AND Logic** (within clause):
```bash
# All conditions must match
ssql where -match age gt 30 -match dept eq Engineering
```

**OR Logic** (between clauses):
```bash
# Either clause can match
ssql where -match age gt 40 + -match salary gt 100000
```

**Complex Logic**:
```bash
# (A AND B) OR (C AND D)
ssql where -match a eq 1 -match b eq 2 + -match c eq 3 -match d eq 4
```

## Tips

1. **Use tab completion**: Press TAB after `-match` to see available fields
2. **Pipe to `head`**: Test pipelines quickly with `| head -n 5`
3. **Check data structure**: Use `ssql from file.csv | head -n 1` to see JSONL format
4. **Save pipelines**: Store working pipelines in shell scripts for reuse
5. **Generate Go code**: Convert CLI pipeline to production code with `ssql generate-go`

## Getting Help

View command help:
```bash
ssql -help                # Show all commands
ssql where -help          # Show where command help
ssql include -help        # Show include command help
```

## Next Steps

After mastering CLI pipelines:
1. Save your pipeline to a shell script
2. Generate production Go code: `ssql generate-go < pipeline.sh > main.go`
3. Compile for 10-100x performance: `go build main.go`

See the [CLI Tools Design Document](../../doc/research/cli-tools-design.md) for more details.
