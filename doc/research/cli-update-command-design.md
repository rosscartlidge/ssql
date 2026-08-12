# CLI Update Command Design

Reference: DFC014
Created: 2025-11-04
Last modified: 2026-03-20

[Back to Index](./README.md)

## Overview

Add an `update` subcommand to the ssql CLI that allows users to modify field values in records. This complements the existing `select` (field projection) and `where` (filtering) commands.

## Use Cases

### 1. Set Constant Values
```bash
# Mark all records as processed
ssql read-csv data.csv | ssql update -set status "processed" | ssql write-csv out.csv

# Set multiple fields
ssql read-csv orders.csv | \
  ssql update -set status "shipped" -set shipped_date "2025-11-04" | \
  ssql write-csv shipped_orders.csv
```

### 2. Computed Values (from other fields)
```bash
# Calculate total from price * quantity
ssql read-csv orders.csv | \
  ssql update -compute total "price * quantity" | \
  ssql write-csv with_totals.csv

# Multiple computed fields
ssql read-csv sales.csv | \
  ssql update \
    -compute subtotal "price * quantity" \
    -compute tax "subtotal * 0.08" \
    -compute total "subtotal + tax" | \
  ssql write-csv invoices.csv
```

### 3. Conditional Updates (with clauses)
```bash
# Update status only for high-value orders
ssql read-csv orders.csv | \
  ssql update -if total gt 1000 -set priority "high" + \
                  -if total le 1000 -set priority "normal" | \
  ssql write-csv prioritized.csv

# Set tier based on purchase amount
ssql read-csv customers.csv | \
  ssql update -if purchases gt 5000 -set tier "Gold" + \
                  -if purchases ge 1000 -if purchases le 5000 -set tier "Silver" + \
                  -set tier "Bronze" | \
  ssql write-csv customers_with_tiers.csv
```

## Command Design

### Basic Structure

```
ssql update [flags] [FILE]
```

### Flags

**Global Flags:**
- `-generate, -g` - Generate Go code instead of executing
- `FILE` - Input JSONL file (or stdin if not specified)

**Local Flags (clause-based with `+` separator):**

**Option 1: Simple -set flag**
- `-set <field> <value>` - Set field to constant value
  - Can be repeated within a clause: `-set status "done" -set date "2025-11-04"`

**Option 2: Add -compute for expressions**
- `-compute <field> <expression>` - Set field to computed value
  - Expression is a simple arithmetic/string expression
  - Examples: `"price * quantity"`, `"subtotal + tax"`, `"field1 + field2"`
  - Can mix with `-set`: `-set status "done" -compute total "price * qty"`

**Option 3: Add -if for conditional updates**
- `-if <field> <op> <value>` - Apply updates only if condition matches
  - Uses same operators as `where` command
  - Can be repeated for AND logic: `-if status eq "pending" -if amount gt 100`
  - Clauses (with `+`) provide OR logic

## Implementation Approaches

### Approach A: Simple -set Only (MVP)

**Pros:**
- Simplest to implement
- Covers basic use case (set constant values)
- Clear, predictable behavior
- Easy to generate code for

**Cons:**
- Can't compute values from other fields
- No conditional updates (need to pipe through `where`)
- Limited usefulness

**Example:**
```bash
ssql update -set status "processed" -set updated_at "2025-11-04"
```

**Implementation:**
```go
// Within clause, accumulate all -set operations
for _, clause := range ctx.Clauses {
    setOps := clause.Flags["-set"].([]any)
    for _, setOp := range setOps {
        field := setOp.(map[string]any)["field"].(string)
        value := setOp.(map[string]any)["value"].(string)
        // Apply: mut = mut.SetAny(field, parseValue(value))
    }
}
```

### Approach B: -set + -compute (Better MVP)

**Pros:**
- Handles constant AND computed values
- Covers 80% of real-world use cases
- Still relatively simple to implement
- Clear separation of concerns

**Cons:**
- Need expression parser for -compute
- Type inference for computed results
- More complex code generation

**Example:**
```bash
ssql update -compute total "price * quantity" -compute tax "total * 0.08"
```

**Implementation Challenges:**
- Parse arithmetic expressions: `"price * quantity"`, `"field1 + field2"`
- Evaluate with record context
- Handle type conversions (string to number)
- Generate equivalent Go code

### Approach C: Full SQL UPDATE syntax (Most Complex)

**Pros:**
- Familiar SQL syntax
- Powerful and expressive
- Handles all cases elegantly

**Cons:**
- Need full SQL parser
- Much more complex implementation
- Harder to generate Go code for
- Overkill for most use cases

**Example:**
```bash
ssql update -set "status = 'processed', total = price * quantity"
ssql update -set "tier = CASE WHEN purchases > 5000 THEN 'Gold' ELSE 'Silver' END"
```

## Recommendation

**Start with Approach A (Simple -set only) as MVP**, then potentially add Approach B features later.

**Rationale:**
1. **-set covers the most common use case**: Setting status flags, dates, categories
2. **Computed values can be done in Go**: Users who need `total = price * qty` can write a simple Go program
3. **Conditional updates can use where**: `ssqlwhere -match amount gt 1000 | ssql update -set priority "high"`
4. **Simpler is better**: Following Unix philosophy - do one thing well
5. **Easy to extend**: Can add -compute later without breaking changes

### MVP Command Spec

```
ssql update -set <field> <value> [-set <field> <value> ...] [FILE]
```

**Flags:**
- `-set <field> <value>` - Set field to constant value (accumulated)
- `-generate, -g` - Generate Go code instead of executing
- `FILE` - Input JSONL file (or stdin)

**Examples:**
```bash
# Single field update
ssql update -set status "processed"

# Multiple fields
ssql update -set status "shipped" -set shipped_date "2025-11-04"

# From file
ssql update -set processed "true" orders.jsonl > updated.jsonl

# In pipeline
cat data.jsonl | ssql where -match amount gt 100 | ssql update -set priority "high"

# With code generation
export SSQLGO=1
ssql read-csv data.csv | ssql update -set status "done" | ssql generate go > program.go
```

**Type Handling:**
The `-set` command will infer types from string values:
- `"123"` → `int64(123)`
- `"45.67"` → `float64(45.67)`
- `"true"` / `"false"` → `bool`
- `"2025-11-04"` → Try parsing as time.Time
- Everything else → `string`

This matches the CSV reader's auto-parsing behavior.

## Implementation Plan

1. Add `update` subcommand to `cmd/ssql/main.go`
2. Add `-set` flag with `.Arg("field")` and `.Arg("value")`
3. Implement handler that:
   - Reads JSONL from stdin/file
   - Builds Update filter with accumulated -set operations
   - Applies filter and writes JSONL output
4. Add `generateUpdateCode()` helper to `cmd/ssql/helpers.go`
5. Add tests to `cmd/ssql/generation_test.go`
6. Document in `doc/codelab-intro.md` and `doc/api-reference.md`

## Future Enhancements (Post-MVP)

If -set proves insufficient, consider adding:

1. **-compute flag**: Simple arithmetic expressions
   ```bash
   ssql update -compute total "price * quantity"
   ```

2. **-if flag for conditional updates**:
   ```bash
   ssql update -if amount gt 1000 -set priority "high"
   ```

3. **Field references in -set**: Allow copying field values
   ```bash
   ssql update -set new_status "@old_status"  # Copy from another field
   ```

But start simple and only add complexity if users actually need it.
