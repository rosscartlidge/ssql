# ssql Troubleshooting Guide

Quick reference for common issues and their solutions when using the ssql CLI tool.

## Table of Contents
- [Quick Diagnostics](#quick-diagnostics)
- [Common Issues](#common-issues)
- [jq Debugging Patterns](#jq-debugging-patterns)
- [Performance Issues](#performance-issues)
- [Data Quality Problems](#data-quality-problems)

---

## Quick Diagnostics

### Pipeline producing no output?

```bash
# Check each stage
ssql from data.csv | wc -l                           # How many input records?
ssql from data.csv | ssql where ... | wc -l      # How many after filter?
ssql from data.csv | head -1 | jq 'keys'             # What fields exist?
```

### Filter not matching records?

```bash
# Inspect the data
ssql from data.csv | jq '.' | head -3

# Check field type
ssql from data.csv | jq '.fieldname | type' | head -5

# Test filter manually in jq
ssql from data.csv | jq 'select(.age > 30)' | head -5
```

### GROUP BY results look wrong?

```bash
# Verify grouping keys
ssql from data.csv | jq -r '.department' | sort | uniq -c

# Check for nulls/empties
ssql from data.csv | jq 'select(.department == null or .department == "")'

# Inspect GROUP BY output
ssql from data.csv | ssql group-by dept -count n | jq '.'
```

---

## Common Issues

### Issue 1: "No such file or directory"

**Symptoms:**
```bash
$ ssql from data.csv
Error: failed to open file data.csv: no such file or directory
```

**Solutions:**
```bash
# Check file exists
ls -lh data.csv

# Use absolute path
ssql from /full/path/to/data.csv

# Or cd to directory first
cd /path/to/data && ssql from data.csv
```

---

### Issue 2: Filter matches zero records

**Symptoms:**
```bash
$ ssql from data.csv | ssql where -if age gt 30 | wc -l
0
```

**Diagnosis:**
```bash
# Step 1: Check if field exists
ssql from data.csv | head -1 | jq 'keys'

# Step 2: Check field type
ssql from data.csv | jq '.age | type' | sort | uniq -c

# Step 3: See actual values
ssql from data.csv | jq '.age' | head -10

# Step 4: Check for type mismatch
ssql from data.csv | jq 'select(.age | type != "number")' | head -5
```

**Common Causes:**
- **Field name typo:** `Age` vs `age` (case-sensitive)
- **Field has extra spaces:** `"age "` vs `"age"`
- **Wrong operator:** Using `eq` for numbers instead of `gt`
- **Field is string not number:** CSV parsing should auto-detect, but verify with jq

**Solutions:**
```bash
# Verify correct field name
ssql from data.csv | head -1 | jq 'keys | map(select(contains("age")))'

# Test filter in jq first
ssql from data.csv | jq 'select(.age > 30)' | head -5

# If numbers are strings (shouldn't happen with CSV), convert in jq
ssql from data.csv | jq '.age |= tonumber' | ssql where -if age gt 30
```

---

### Issue 3: "Field not found" errors

**Symptoms:**
```bash
# Filter silently excludes all records
ssql from data.csv | ssql where -if nonexistent_field eq value | wc -l
0
```

**Diagnosis:**
```bash
# List all field names
ssql from data.csv | head -1 | jq 'keys'

# Look for similar field names
ssql from data.csv | head -1 | jq 'keys | map(select(contains("part_of_name")))'

# Check for whitespace
ssql from data.csv | head -1 | jq 'keys | map({name: ., length: length})'
```

**Common Causes:**
- Case sensitivity
- Extra whitespace in CSV headers
- Different column name in file than expected
- Typo in field name

**Solutions:**
```bash
# Always check field names first
ssql from data.csv | head -1 | jq 'keys'

# Look at raw CSV if suspicious
head -1 data.csv
```

---

### Issue 4: Wrong number of records

**Symptoms:**
```bash
# Expected 100 records, got 95
$ ssql from data.csv | ssql where -if status eq active | wc -l
95
```

**Diagnosis:**
```bash
# Count at each stage
echo "Input: $(ssql from data.csv | wc -l)"
echo "After filter: $(ssql from data.csv | ssql where -if status eq active | wc -l)"

# Find what's being filtered out
ssql from data.csv | jq 'select(.status != "active")' | jq -r '.status' | sort | uniq -c

# Look for nulls/empties
ssql from data.csv | jq 'select(.status == null or .status == "")' | wc -l

# Check for case issues
ssql from data.csv | jq -r '.status' | sort | uniq
```

**Common Causes:**
- Null/empty values in filter field
- Case sensitivity: `"Active"` vs `"active"`
- Leading/trailing whitespace: `"active "` vs `"active"`
- Multiple values that look similar: `"active"` vs `"Active"` vs `"ACTIVE"`

**Solutions:**
```bash
# Case-insensitive match (convert to lowercase in jq first)
ssql from data.csv | jq '.status |= ascii_downcase' | ssql where -if status eq active

# Check actual values
ssql from data.csv | jq -r '.status' | sort | uniq -c
```

---

### Issue 5: GROUP BY produces unexpected results

**Symptoms:**
```bash
# GROUP BY shows more groups than expected
$ ssql from data.csv | ssql group-by department -count n | jq -s 'length'
12  # Expected only 5 departments
```

**Diagnosis:**
```bash
# Check actual grouping key values
ssql from data.csv | jq -r '.department' | sort | uniq -c

# Look for subtle differences
ssql from data.csv | jq -r '.department' | sort | uniq | od -c

# Find nulls/empties
ssql from data.csv | jq 'select(.department == null or .department == "")' | wc -l

# Check for case variations
ssql from data.csv | jq -r '.department' | sort -f | uniq -i -c
```

**Common Causes:**
- Leading/trailing whitespace: `"Engineering "` vs `"Engineering"`
- Case variations: `"engineering"` vs `"Engineering"`
- Null/empty values creating extra groups
- Special characters or unicode issues

**Solutions:**
```bash
# Normalize in jq before GROUP BY
ssql from data.csv | \
  jq '.department |= (. // "" | ascii_downcase | gsub("^\\s+|\\s+$"; ""))' | \
  ssql group-by department -count n

# Check grouping manually
ssql from data.csv | jq -r '.department' | sort | uniq -c
```

---

### Issue 6: Type comparison errors

**Symptoms:**
```bash
# Numeric filter doesn't work
ssql from data.csv | ssql where -if age gt 30 | wc -l
0  # But you know there are records with age > 30
```

**Diagnosis:**
```bash
# Check field types
ssql from data.csv | jq '.age | type' | sort | uniq -c

# Find mixed types
ssql from data.csv | jq 'select(.age | type != "number")'

# See actual values
ssql from data.csv | jq '.age' | head -10
```

**Common Causes:**
- Field contains non-numeric values (`"N/A"`, `"unknown"`, etc.)
- CSV has inconsistent data (some rows numeric, some text)
- Manual JSONL has strings instead of numbers
- The column holds zero-padded values (`02134`, `007`). Those are
  identifiers, and ssql keeps such a column as text so the zeros survive
  (as DuckDB does). If the numbers really are numbers, say so: `ssql from
  csv data.csv -type code int`, or `ssql cast -type code int` later.

**Solutions:**
```bash
# CSV auto-parsing should handle this, but verify
ssql from data.csv | jq '.age | type' | sort | uniq -c

# For manual JSONL, convert types
ssql from data.jsonl | jq '.age |= tonumber' | ssql where -if age gt 30

# Filter out non-numeric values first
ssql from data.csv | jq 'select(.age | type == "number")' | ssql where -if age gt 30
```

---

### Issue 7: Pipeline is slow

**Symptoms:**
```bash
# Pipeline takes minutes instead of seconds
$ time ssql from huge.csv | ssql where ... | ssql group ...
# Takes 5+ minutes
```

**Diagnosis:**
```bash
# Profile each stage
time ssql from huge.csv > /dev/null
time (ssql from huge.csv | ssql where ... > /dev/null)
time (ssql from huge.csv | ssql where ... | ssql group ... > /dev/null)

# Check file size
ls -lh huge.csv
wc -l huge.csv
```

**Solutions:**
```bash
# Test with small sample first
ssql from huge.csv | ssql limit 1000 | ssql where ...

# Use limit after filter to stop early
ssql from huge.csv | ssql where ... | ssql limit 100

# Save intermediate results
ssql from huge.csv | ssql where ... > /tmp/filtered.jsonl
ssql from /tmp/filtered.jsonl | ssql group ...

# For very large files, consider splitting
split -l 10000 huge.csv chunk_
for f in chunk_*; do ssql from $f | ssql where ...; done
```

**Note:** ssql uses buffered I/O for efficient performance with large files.

---

### Issue 8: Empty output file

**Symptoms:**
```bash
$ ssql from data.csv | ssql where ... | ssql to csv output.csv
$ wc -l output.csv
1 output.csv  # Only header, no data
```

**Diagnosis:**
```bash
# Check intermediate stages
ssql from data.csv | tee >(wc -l >&2) | \
  ssql where ... | tee >(wc -l >&2) | \
  ssql to csv output.csv

# Or step by step
echo "Input: $(ssql from data.csv | wc -l)"
echo "Filtered: $(ssql from data.csv | ssql where ... | wc -l)"
```

**Common Causes:**
- Filter matches zero records (see Issue #2)
- Input file is empty
- Wrong field name in filter

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

### Issue 12a: "field X is a number but "abc" is not"

A `-if FIELD OP VALUE` literal is read in the field's kind. A numeric
field against `abc` (or `3O` with a letter O), a bool against `maybe`,
a string operator (`contains`, `startswith`, `endswith`, `regex`) on a
number: these cannot be compared, so the pipeline stops rather than
matching nothing. `-if-field` over two kinds (a number against text) is
the same. A fractional literal on an int column and a numeric-looking
literal on a text column are fine (number and text comparison
respectively); an empty literal against a non-text field is simply false.

### Issue 12: "parameter X has the same name as a field" / "-param X: no expression in the clause uses it"

`-param NAME TYPE VALUE` binds NAME as a variable of the clause's
expressions. Two things are refused on purpose: a name that is also a
column of the input (rename the parameter; silently preferring either
would let a new upstream column change your expression), and a parameter
that no `-if-expr` or `-set-expr` in the same clause mentions (usually a
typo in the expression). Parameters are clause-scoped: one written before
`+` or `-` is not visible after it.

### Issue 13: A column or file whose name starts with `-` or `+`

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

## jq Debugging Patterns

### Pattern: Inspect pipeline at any stage

```bash
# Pretty-print records
command | jq '.'

# Compact format
command | jq -c '.'

# With color and pagination
command | jq -C '.' | less -R

# First N records only
command | jq '.' | head -10
```

### Pattern: Extract specific fields

```bash
# Single field
command | jq '.fieldname'

# Multiple fields
command | jq '{field1, field2, field3}'

# With computed values
command | jq '{name, annual: (.monthly * 12)}'

# Raw string output (no quotes)
command | jq -r '.fieldname'
```

### Pattern: Filter records

```bash
# Simple filter
command | jq 'select(.age > 30)'

# Multiple conditions (AND)
command | jq 'select(.age > 30 and .status == "active")'

# Multiple conditions (OR)
command | jq 'select(.age > 65 or .age < 18)'

# Check for null/empty
command | jq 'select(.field == null or .field == "")'

# Exclude records
command | jq 'select(.status != "inactive")'
```

### Pattern: Analyze data

```bash
# Count records
command | jq -s 'length'

# Unique values
command | jq -r '.field' | sort -u

# Unique values with counts
command | jq -r '.field' | sort | uniq -c

# Sum values
command | jq -s 'map(.field) | add'

# Average
command | jq -s 'map(.field) | add / length'

# Min/Max
command | jq -s 'map(.field) | min'
command | jq -s 'map(.field) | max'
```

### Pattern: Check data types

```bash
# Type of entire record
command | head -1 | jq 'type'

# Type of specific field
command | jq '.field | type' | sort | uniq -c

# All field types
command | head -1 | jq 'to_entries | map({key, type: .value | type})'

# Find type mismatches
command | jq 'select(.field | type != "number")'
```

### Pattern: Debug GROUP BY

```bash
# Inspect grouped results
... | ssql group ... | jq '.'

# Check specific group
... | ssql group ... | jq 'select(.department == "Engineering")'

# Count groups
... | ssql group ... | jq -s 'length'

# Verify grouping keys manually
ssql from data.csv | jq -r '.department' | sort | uniq -c
```

### Pattern: Compare before/after

```bash
# Save intermediate results
command1 > /tmp/before.jsonl
command1 | command2 > /tmp/after.jsonl

# Compare counts
echo "Before: $(wc -l < /tmp/before.jsonl)"
echo "After: $(wc -l < /tmp/after.jsonl)"

# Diff first few records
jq '.' /tmp/before.jsonl | head -3
jq '.' /tmp/after.jsonl | head -3
```

---

## Performance Issues

### Large file processing

```bash
# Don't process entire file if not needed
ssql from huge.csv | ssql limit 1000 | ...

# Use head for quick samples
ssql from huge.csv | head -100 | ...

# Save filtered results
ssql from huge.csv | ssql where ... > filtered.jsonl
ssql from filtered.jsonl | ssql group ...
```

### Memory usage

```bash
# GROUP BY materializes data - watch memory usage
# For very large groups, consider splitting

# Process in chunks
split -l 50000 huge.csv chunk_
for f in chunk_*; do
  ssql from $f | ssql where ... | ssql to csv processed_$f
done
```

### I/O bottlenecks

```bash
# Check version
ssql version

# Use SSD not network drives for temp files
export TMPDIR=/local/ssd/tmp

# Avoid unnecessary pretty-printing in pipelines
# DON'T: ssql from ... | jq '.' | ssql where ...
# DO:    ssql from ... | ssql where ...
```

---

## Data Quality Problems

### Missing values

```bash
# Find records with missing fields
ssql from data.csv | jq 'select(has("required_field") | not)'

# Find null values
ssql from data.csv | jq 'select(.field == null)'

# Find empty strings
ssql from data.csv | jq 'select(.field == "")'

# Count missing values
ssql from data.csv | jq 'select(.field == null or .field == "")' | wc -l
```

### Duplicate records

```bash
# Find duplicates by field
ssql from data.csv | jq -r '.id' | sort | uniq -d

# Count duplicates
ssql from data.csv | jq -r '.id' | sort | uniq -c | awk '$1 > 1'

# Show duplicate records
ssql from data.csv | jq -s 'group_by(.id) | map(select(length > 1))'
```

### Inconsistent formatting

```bash
# Find case variations
ssql from data.csv | jq -r '.status' | sort -f | uniq -i -c

# Find whitespace issues
ssql from data.csv | jq -r '.field' | sed 's/^/[/' | sed 's/$/]/'

# Normalize data
ssql from data.csv | \
  jq '.status |= ascii_downcase | .name |= gsub("^\\s+|\\s+$"; "")' | \
  ssql where ...
```

---

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

- [Debugging Guide](./cli-debugging.md) - Comprehensive debugging techniques
- [README](../README.md) - Quick start and overview
- [Examples](../examples/) - Working code examples

### Online resources

- [jq Manual](https://jqlang.github.io/jq/manual/)
- [jq Cookbook](https://github.com/stedolan/jq/wiki/Cookbook)
- [GitHub Issues](https://github.com/rosscartlidge/ssql/issues)

---

## Quick Reference Card

### Essential Commands

```bash
# Inspect data
| jq '.' | head -5                    # Pretty-print first 5
| head -1 | jq 'keys'                 # List fields
| jq '.field | type' | sort | uniq -c  # Check types

# Count records
| wc -l                               # Fast count
| jq -s 'length'                      # Exact count

# Debug filters
| jq 'select(.field == "value")'     # Manual filter
| jq '.field' | sort | uniq -c        # Value distribution

# Performance
| ssql limit 100               # Work with sample
| tee >(wc -l >&2)                    # Count at stage
time command                          # Measure time
```

### Common jq Patterns

```bash
jq '.'                                # Pretty-print
jq -c '.'                             # Compact format
jq -r '.field'                        # Raw string
jq '{f1, f2}'                         # Select fields
jq 'select(.age > 30)'                # Filter
jq -s 'length'                        # Count
jq -s 'map(.field) | add'             # Sum
jq '.field | type'                    # Check type
jq 'keys'                             # List keys
```
