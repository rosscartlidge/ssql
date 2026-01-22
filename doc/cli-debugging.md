# Debugging ssql Pipelines

This guide shows how to debug and troubleshoot ssql CLI pipelines using built-in tools and standard Unix utilities.

## Table of Contents
- [Quick Debugging Tips](#quick-debugging-tips)
- [Using jq for Pipeline Inspection](#using-jq-for-pipeline-inspection)
- [Common Debugging Patterns](#common-debugging-patterns)
- [Performance Debugging](#performance-debugging)
- [Troubleshooting Common Issues](#troubleshooting-common-issues)

---

## Quick Debugging Tips

### 1. Inspect Data at Any Stage

ssql uses JSONL (JSON Lines) for inter-process communication, making it easy to inspect data flowing through your pipeline:

```bash
# See first 5 records after reading CSV
ssql from data.csv | head -5

# Pretty-print with jq
ssql from data.csv | jq '.' | head -5

# Check what's passing through a filter
ssql from data.csv | ssql where -where age gt 30 | jq '.'
```

### 2. Count Records at Each Stage

```bash
# Count input records
ssql from data.csv | wc -l

# Count after filtering
ssql from data.csv | ssql where -where status eq active | wc -l

# Use jq for exact count (handles empty lines)
ssql from data.csv | jq -s 'length'
```

### 3. Sample Data During Development

```bash
# Work with small dataset while developing
ssql from large_file.csv | ssql limit 100 | \
  ssql where -where ... | \
  jq '.'

# Test with just first 10 records
ssql from data.csv | head -10 | ssql where ...
```

---

## Using jq for Pipeline Inspection

[jq](https://jqlang.github.io/jq/) is a powerful JSON processor that pairs perfectly with ssql's JSONL format.

### Installation

```bash
# macOS
brew install jq

# Ubuntu/Debian
sudo apt-get install jq

# Other platforms: https://jqlang.github.io/jq/download/
```

### Basic jq Patterns

#### View Records (Pretty-Printed)

```bash
# Pretty-print all records
ssql from data.csv | jq '.'

# With color, paginated
ssql from data.csv | jq -C '.' | less -R

# Compact format (one line per record)
ssql from data.csv | jq -c '.'
```

#### Extract Specific Fields

```bash
# Single field
ssql from data.csv | jq '.name'

# Multiple fields
ssql from data.csv | jq '{name, age, department}'

# Field with computed value
ssql from data.csv | jq '{name, annual_salary: (.salary * 12)}'
```

#### Filter Records

```bash
# Filter in jq (alternative to ssql where command)
ssql from data.csv | jq 'select(.age > 30)'

# Combine with ssql filters
ssql from data.csv | \
  ssql where -where department eq Engineering | \
  jq 'select(.salary > 80000)'
```

#### Inspect Data Types

```bash
# See type of entire record
ssql from data.csv | head -1 | jq 'type'

# See types of all fields
ssql from data.csv | head -1 | jq 'to_entries | map({key, type: .value | type})'

# Check specific field type
ssql from data.csv | jq '.age | type' | head -5
```

#### Analyze Arrays and Nested Data

```bash
# Inspect GROUP BY results
ssql from data.csv | \
  ssql group-by department -count total | \
  jq '.'

# Extract nested fields
ssql from data.csv | jq '.metadata.created'

# Flatten nested structure
ssql from data.csv | jq 'flatten'
```

#### Statistical Analysis

```bash
# Count records
ssql from data.csv | jq -s 'length'

# Sum a field
ssql from data.csv | jq -s 'map(.salary) | add'

# Average
ssql from data.csv | jq -s 'map(.salary) | add / length'

# Min/Max
ssql from data.csv | jq -s 'map(.age) | min'
ssql from data.csv | jq -s 'map(.age) | max'

# Unique values
ssql from data.csv | jq -r '.department' | sort -u
```

### Advanced jq Debugging

#### Compare Before/After Filter

```bash
# Count before filter
echo "Before: $(ssql from data.csv | wc -l)"

# Count after filter
echo "After: $(ssql from data.csv | ssql where -where age gt 30 | wc -l)"

# See what was filtered out
ssql from data.csv | jq 'select(.age <= 30)'
```

#### Validate Field Presence

```bash
# Find records missing required fields
ssql from data.csv | jq 'select(.email == null or .email == "")'

# Check for unexpected nulls
ssql from data.csv | jq 'select(has("required_field") | not)'
```

#### Debug Type Mismatches

```bash
# Find non-numeric values in numeric field
ssql from data.csv | jq 'select(.age | type != "number")'

# Show records where field has unexpected type
ssql from data.csv | jq 'select(.salary | type == "string")'
```

---

## Common Debugging Patterns

### Pattern 1: "Why is my filter not matching?"

```bash
# Step 1: See what data looks like
ssql from data.csv | jq '.' | head -3

# Step 2: Check the specific field
ssql from data.csv | jq '.age' | head -10

# Step 3: Check field type
ssql from data.csv | jq '.age | type' | head -5

# Step 4: Test filter manually
ssql from data.csv | jq 'select(.age > 30)' | head -5

# Step 5: Compare with ssql filter
ssql from data.csv | ssql where -where age gt 30 | jq '.' | head -5
```

### Pattern 2: "My GROUP BY results look wrong"

```bash
# Step 1: Verify grouping field values
ssql from data.csv | jq -r '.department' | sort | uniq -c

# Step 2: See raw GROUP BY output
ssql from data.csv | \
  ssql group-by department -count total | \
  jq '.'

# Step 3: Check specific group
ssql from data.csv | \
  ssql group-by department -count total | \
  jq 'select(.department == "Engineering")'

# Step 4: Verify counts manually
ssql from data.csv | jq 'select(.department == "Engineering")' | wc -l
```

### Pattern 3: "Field values are wrong type"

```bash
# Diagnose: Check CSV parsing
ssql from data.csv | head -1 | jq '.'

# Common issue: Numbers parsed as strings
# Solution: CSV auto-parses to int64/float64, but manual data might not

# Fix in jq if needed:
ssql from data.csv | jq '.age = (.age | tonumber)' | \
  ssql where -where age gt 30
```

### Pattern 4: "Pipeline is too slow"

```bash
# Test with small sample first
time (ssql from large.csv | head -1000 | ssql where ...)

# Profile each stage
time ssql from large.csv > /dev/null
time (ssql from large.csv | ssql where -where age gt 30 > /dev/null)
time (ssql from large.csv | ssql where ... | ssql group ... > /dev/null)

# Use limit to stop early
ssql from large.csv | ssql where ... | ssql limit 100
```

---

## Performance Debugging

### Measure Pipeline Stages

```bash
# Time entire pipeline
time (ssql from data.csv | \
  ssql where -where age gt 30 | \
  ssql group-by department -count total | \
  ssql to csv output.csv)

# Time individual commands
time ssql from data.csv > /tmp/stage1.jsonl
time (cat /tmp/stage1.jsonl | ssql where -where age gt 30 > /tmp/stage2.jsonl)
time (cat /tmp/stage2.jsonl | ssql group ... > /tmp/stage3.jsonl)
```

### Check Record Counts

```bash
# Ensure no data loss between stages
echo "Input: $(ssql from data.csv | wc -l)"
echo "After filter: $(ssql from data.csv | ssql where -where age gt 30 | wc -l)"
echo "After group: $(ssql from data.csv | ssql where -where age gt 30 | ssql group-by dept -count n | wc -l)"
```

### Sample Large Datasets

```bash
# Random sample (requires gnu coreutils shuf)
ssql from huge.csv | shuf | head -1000 | ssql where ...

# Every Nth record
ssql from huge.csv | awk 'NR % 100 == 0' | ssql where ...

# First N records
ssql from huge.csv | ssql limit 1000 | ssql where ...
```

---

## Troubleshooting Common Issues

### Issue: "No output from pipeline"

**Diagnosis:**
```bash
# Check if input file exists
ls -lh data.csv

# Check if input is readable
ssql from data.csv | head -1

# Check each stage
ssql from data.csv | tee /tmp/stage1.jsonl | \
  ssql where -where age gt 30 | tee /tmp/stage2.jsonl | \
  ssql to csv output.csv
```

**Common causes:**
- Empty input file
- Filter matches zero records
- Typo in field name
- Wrong operator (e.g., using `eq` instead of `gt`)

### Issue: "Wrong number of records"

**Diagnosis:**
```bash
# Count at each stage
ssql from data.csv | tee >(wc -l >&2) | \
  ssql where -where age gt 30 | tee >(wc -l >&2) | \
  ssql to csv output.csv

# Or step by step
echo "Input: $(ssql from data.csv | wc -l)"
echo "Filtered: $(ssql from data.csv | ssql where -where age gt 30 | wc -l)"
```

### Issue: "Field not found errors"

**Diagnosis:**
```bash
# List all field names
ssql from data.csv | head -1 | jq 'keys'

# Check for whitespace in field names
ssql from data.csv | head -1 | jq 'keys | map(length)'

# Look for the field you think exists
ssql from data.csv | head -1 | jq 'keys | map(select(contains("age")))'
```

**Common causes:**
- Case sensitivity: `Age` vs `age`
- Extra spaces: `"name "` vs `"name"`
- Different separator in CSV (e.g., semicolon instead of comma)

### Issue: "Type comparison errors"

**Diagnosis:**
```bash
# Check field types
ssql from data.csv | jq '.age | type' | sort | uniq -c

# Find mixed types
ssql from data.csv | jq 'select(.age | type != "number")'
```

**Solution:**
```bash
# CSV auto-parsing should handle this, but if you have manual JSONL:
ssql from data.jsonl | jq '.age |= tonumber' | ssql where -where age gt 30
```

### Issue: "GROUP BY produces unexpected results"

**Diagnosis:**
```bash
# Check grouping key values
ssql from data.csv | jq -r '.department' | sort | uniq -c

# Look for null/empty values
ssql from data.csv | jq 'select(.department == null or .department == "")'

# Verify manual count matches GROUP BY count
DEPT="Engineering"
echo "Manual count: $(ssql from data.csv | jq "select(.department == \"$DEPT\")" | wc -l)"
echo "GROUP BY count: $(ssql from data.csv | ssql group-by department -count n | jq "select(.department == \"$DEPT\") | .n")"
```

---

## Interactive Debugging

### Use jq's Interactive Mode

```bash
# Save intermediate results
ssql from data.csv | ssql where -where age gt 30 > /tmp/filtered.jsonl

# Explore interactively with jq
jq '.' /tmp/filtered.jsonl | less

# Try different queries
jq 'select(.department == "Engineering")' /tmp/filtered.jsonl
jq 'group_by(.department) | map({dept: .[0].department, count: length})' /tmp/filtered.jsonl
```

### Build Pipeline Incrementally

```bash
# Start simple
ssql from data.csv | jq '.'

# Add one filter
ssql from data.csv | ssql where -where age gt 30 | jq '.'

# Add another filter
ssql from data.csv | \
  ssql where -where age gt 30 | \
  ssql where -where department eq Engineering | \
  jq '.'

# Add aggregation
ssql from data.csv | \
  ssql where -where age gt 30 | \
  ssql where -where department eq Engineering | \
  ssql group-by department -avg salary avg_salary | \
  jq '.'
```

---

## Best Practices

### 1. Always Inspect Sample Data First

```bash
# Before building complex pipeline, look at the data
ssql from data.csv | jq '.' | head -3
```

### 2. Test Filters Incrementally

```bash
# Add filters one at a time, checking counts
ssql from data.csv | wc -l
ssql from data.csv | ssql where -where age gt 30 | wc -l
ssql from data.csv | ssql where -where age gt 30 | ssql where -where status eq active | wc -l
```

### 3. Use Limit During Development

```bash
# Work with small sample while developing
ssql from huge.csv | ssql limit 100 | \
  ssql where ... | \
  ssql group ... | \
  jq '.'
```

### 4. Save Intermediate Results

```bash
# Save stages for debugging
ssql from data.csv > /tmp/1-input.jsonl
cat /tmp/1-input.jsonl | ssql where -where age gt 30 > /tmp/2-filtered.jsonl
cat /tmp/2-filtered.jsonl | ssql group-by dept -count n > /tmp/3-grouped.jsonl

# Inspect any stage
jq '.' /tmp/2-filtered.jsonl | less
```

### 5. Use Verbose/Debug Output

```bash
# Count records at each stage
ssql from data.csv | tee >(wc -l >&2) | \
  ssql where -where age gt 30 | tee >(wc -l >&2) | \
  ssql group-by dept -count n | tee >(wc -l >&2) | \
  ssql to csv output.csv
```

---

## Quick Reference

### jq One-Liners

```bash
# Pretty-print
jq '.'

# Extract field
jq '.fieldname'

# Select multiple fields
jq '{field1, field2, field3}'

# Filter records
jq 'select(.age > 30)'

# Count records
jq -s 'length'

# List all keys
jq 'keys'

# Check type
jq '.field | type'

# Convert to number
jq '.field | tonumber'

# Sum values
jq -s 'map(.field) | add'

# Average
jq -s 'map(.field) | add / length'

# Unique values
jq -r '.field' | sort -u

# Group and count
jq -s 'group_by(.field) | map({key: .[0].field, count: length})'
```

### ssql + jq Patterns

```bash
# Inspect pipeline
ssql from data.csv | ssql where ... | jq '.'

# Count results
ssql from data.csv | ssql where ... | jq -s 'length'

# Extract single field
ssql from data.csv | jq -r '.name'

# Validate GROUP BY
ssql ... | ssql group ... | jq 'select(.department == "Engineering")'

# Debug type issues
ssql from data.csv | jq '.age | type' | sort | uniq -c
```

---

## Additional Resources

- [jq Manual](https://jqlang.github.io/jq/manual/)
- [jq Cookbook](https://github.com/stedolan/jq/wiki/Cookbook)
- [ssql README](../README.md)
- [ssql Examples](../examples/)

---

## Getting Help

If you encounter issues not covered here:

1. Check the [troubleshooting guide](./cli-troubleshooting.md)
2. Look at [examples](../examples/)
3. File an issue: https://github.com/rosscartlidge/ssql/issues
