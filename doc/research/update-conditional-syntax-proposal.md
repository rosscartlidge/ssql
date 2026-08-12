# Conditional Update Syntax Proposal

Reference: DFC016
Created: 2025-11-04
Last modified: 2025-11-08

[Back to Index](./README.md)

## Problem

Currently, combining `where` with `update` filters out non-matching records:

```bash
# ❌ This only outputs records where age > 30
ssql where -match age gt 30 | ssql update -set priority high
# Records with age <= 30 are lost!
```

We need conditional updates that **keep all records** but only update those matching conditions.

## Proposed Syntax

### Option 1: Simple -match flag (Single Condition)

```bash
# Update priority for records where age > 30, pass through others unchanged
ssql update -match age gt 30 -set priority high

# Multiple conditions (AND logic)
ssql update -match age gt 30 -match dept eq Sales -set priority high
```

**Semantics:**
- If `-match` conditions are present, only matching records are updated
- Non-matching records pass through unchanged
- Multiple `-match` flags = AND logic (all must match)

**Pros:**
- Simple, intuitive
- Consistent with `where` command syntax
- Easy to understand

**Cons:**
- Can only have one set of conditions → one update operation
- Can't do "if X then set A, else if Y then set B"

---

### Option 2: Clause-Based (Multiple Conditional Updates)

Use `+` separator for multiple conditional update operations:

```bash
# Set tier based on age ranges (first match wins)
ssql update \
  -match age gt 50 -set tier Gold + \
  -match age gt 30 -set tier Silver + \
  -set tier Bronze

# Multiple fields per condition
ssql update \
  -match status eq pending -match priority eq high -set assignee alice -set deadline tomorrow + \
  -match status eq pending -set assignee bob
```

**Clause Evaluation:**
- **Option 2a: First Match Wins** (like switch/case)
  - Evaluate clauses in order
  - Apply first matching clause, skip rest
  - Final clause without `-match` = default case

- **Option 2b: All Matching Apply** (like multiple if statements)
  - Evaluate all clauses
  - Apply every matching clause
  - Later clauses can overwrite earlier ones

**Pros:**
- Very flexible - can handle complex conditional logic
- Consistent with existing clause pattern (group-by, where)
- Can have default case (clause with no -match)

**Cons:**
- More complex
- Need to decide: first-match-wins or all-apply?

---

### Option 3: -if flag (Alternative naming)

```bash
ssql update -if age gt 30 -set priority high
```

Same semantics as Option 1, just different flag name.

**Pros:**
- More readable: "if age > 30 then set priority high"

**Cons:**
- Inconsistent with `where` command (uses -match)

---

## Recommendation

**Start with Option 1 (Simple -match flag)** for MVP:

```bash
ssql update -match age gt 30 -set priority high
ssql update -match age gt 30 -match dept eq Sales -set priority high -set reviewed true
```

**Later add Option 2 (Clauses)** if needed:

```bash
ssql update \
  -match purchases gt 5000 -set tier Gold + \
  -match purchases gt 1000 -set tier Silver + \
  -set tier Bronze
```

---

## Detailed Specification (Option 1 - MVP)

### Command Structure

```
ssql update [-match <field> <op> <value>]... -set <field> <value>... [FILE]
```

### Flags

**Conditional Flags (optional, accumulated):**
- `-match <field> <operator> <value>` - Condition to check
  - Can be repeated for AND logic
  - Uses same operators as `where` command: eq, ne, gt, lt, ge, le, contains, startswith, endswith, regexp

**Update Flags (required, accumulated):**
- `-set <field> <value>` - Field to update

### Behavior

**Without -match:**
```bash
ssql update -set status active
# Updates ALL records
```

**With -match:**
```bash
ssql update -match age gt 30 -set priority high
# Only updates records where age > 30
# Other records pass through unchanged
```

**Multiple -match (AND):**
```bash
ssql update -match age gt 30 -match dept eq Sales -set priority high
# Updates records where (age > 30 AND dept = Sales)
```

**Multiple -set:**
```bash
ssql update -match status eq pending -set status active -set processed_date 2025-11-04
# Updates multiple fields for matching records
```

### Examples

```bash
# Simple conditional update
ssql read-csv employees.csv | \
  ssql update -match salary gt 100000 -set bracket high | \
  ssql write-csv output.csv

# Multiple conditions (AND)
ssql read-csv orders.csv | \
  ssql update -match status eq pending -match amount gt 1000 -set priority urgent

# Update multiple fields
ssql read-csv data.csv | \
  ssql update -match verified eq false -set status unverified -set alert true

# Unconditional update (no -match)
ssql read-csv data.csv | \
  ssql update -set processed_date 2025-11-04
```

### Code Generation

```go
updated := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
    frozen := mut.Freeze()

    // Check conditions
    if ssql.GetOr(frozen, "age", float64(0)) > 30 {
        mut = mut.String("priority", "high")
    }

    return mut
})(records)
```

### Implementation Notes

1. **Reuse operator logic from where command** - Use existing `applyOperator()` function
2. **Type-safe setters** - Continue using typed methods (Int, Float, Bool, String, Set)
3. **Condition evaluation** - Check conditions before applying updates
4. **Pass-through** - Records not matching conditions are passed through unchanged

---

## Future Enhancement: Clause-Based Updates (Option 2)

If we later need "if-elseif-else" logic:

```bash
# Set tier based on purchases (first match wins)
ssql update \
  -match purchases gt 5000 -set tier Gold + \
  -match purchases gt 1000 -set tier Silver + \
  -set tier Bronze

# Equivalent to:
if purchases > 5000:
    tier = "Gold"
elif purchases > 1000:
    tier = "Silver"
else:
    tier = "Bronze"
```

But start simple with Option 1 (AND-only conditions per update).
