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
$ ssql from data.csv | ssql where -where age gt 30 | wc -l
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
ssql from data.csv | jq '.age |= tonumber' | ssql where -where age gt 30
```

---

### Issue 3: "Field not found" errors

**Symptoms:**
```bash
# Filter silently excludes all records
ssql from data.csv | ssql where -where nonexistent_field eq value | wc -l
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
$ ssql from data.csv | ssql where -where status eq active | wc -l
95
```

**Diagnosis:**
```bash
# Count at each stage
echo "Input: $(ssql from data.csv | wc -l)"
echo "After filter: $(ssql from data.csv | ssql where -where status eq active | wc -l)"

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
ssql from data.csv | jq '.status |= ascii_downcase' | ssql where -where status eq active

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
ssql from data.csv | ssql where -where age gt 30 | wc -l
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

**Solutions:**
```bash
# CSV auto-parsing should handle this, but verify
ssql from data.csv | jq '.age | type' | sort | uniq -c

# For manual JSONL, convert types
ssql from data.jsonl | jq '.age |= tonumber' | ssql where -where age gt 30

# Filter out non-numeric values first
ssql from data.csv | jq 'select(.age | type == "number")' | ssql where -where age gt 30
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
