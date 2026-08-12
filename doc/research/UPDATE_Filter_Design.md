# UPDATE Filter Design

Reference: DFC013
Created: 2025-11-04
Last modified: 2026-03-20

[Back to Index](./README.md)

## Overview

This document describes the design for adding SQL-style UPDATE operations to ssql. The UPDATE filter will allow modifying record fields within pipelines while maintaining type safety through the `Value` constraint.

## Motivation

ssql currently has SQL-style operations for querying and transforming data:
- `Where` - Filter records (SQL WHERE)
- `Select` - Project/transform fields (SQL SELECT)
- `GroupByFields` + `Aggregate` - Aggregation (SQL GROUP BY)
- `InnerJoin`, `LeftJoin`, etc. - Joins (SQL JOIN)

However, there's no equivalent to SQL's `UPDATE ... SET ...` for modifying record fields in place during pipeline processing. Users currently need to use `Select()` with manual record reconstruction, which is verbose and not semantically clear.

## Current Workaround

```go
// Current way to update a field - verbose and unclear intent
updated := ssql.Select(func(r ssql.Record) ssql.Record {
    mut := ssql.MakeMutableRecord()
    for k, v := range r.All() {
        mut.fields[k] = v  // Copy all fields
    }
    mut.fields["status"] = "processed"  // Update field
    return mut.Freeze()
})(records)
```

## Proposed API

### 1. Core Update Function

```go
// Update returns a Filter that updates multiple record fields.
// Similar to SQL: UPDATE ... SET field1 = expr1, field2 = expr2, ...
//
// The generic parameter V must satisfy the Value constraint, ensuring
// only valid types (int64, float64, string, bool, time.Time, Record,
// iter.Seq[T], JSONString) can be stored in records.
//
// Example:
//   updates := map[string]func(ssql.Record) float64{
//       "total": func(r ssql.Record) float64 {
//           price := ssql.GetOr(r, "price", float64(0))
//           qty := ssql.GetOr(r, "quantity", int64(0))
//           return price * float64(qty)
//       },
//       "tax": func(r ssql.Record) float64 {
//           total := ssql.GetOr(r, "total", float64(0))
//           return total * 0.08
//       },
//   }
//
//   pipeline := ssql.Pipe(records, ssql.Update(updates))
func Update[V Value](updates map[string]func(Record) V) Filter[Record, Record]
```

**Key characteristics:**
- Generic `V Value` constraint ensures type safety at compile time
- Takes a map of field names to update functions
- Each function receives the full record and returns the new field value
- Returns a Filter for use in pipelines
- Creates new records (immutable), never mutates originals

### 2. Typed Helper Functions

For common single-field updates with explicit types:

```go
// UpdateString updates a single field with a string value
func UpdateString(field string, fn func(Record) string) Filter[Record, Record]

// UpdateInt updates a single field with an int64 value (canonical integer type)
func UpdateInt(field string, fn func(Record) int64) Filter[Record, Record]

// UpdateFloat updates a single field with a float64 value (canonical float type)
func UpdateFloat(field string, fn func(Record) float64) Filter[Record, Record]

// UpdateBool updates a single field with a bool value
func UpdateBool(field string, fn func(Record) bool) Filter[Record, Record]

// UpdateTime updates a single field with a time.Time value
func UpdateTime(field string, fn func(Record) time.Time) Filter[Record, Record]
```

**Example usage:**
```go
// Update status field to constant value
pipeline := ssql.UpdateString("status", func(r ssql.Record) string {
    return "processed"
})

// Update total based on other fields
pipeline := ssql.UpdateFloat("total", func(r ssql.Record) float64 {
    price := ssql.GetOr(r, "price", float64(0))
    qty := ssql.GetOr(r, "quantity", int64(0))
    return price * float64(qty)
})
```

### 3. Constant Value Helper

For setting fields to constant values (most common case):

```go
// SetField sets a field to a constant value
// Similar to SQL: UPDATE ... SET field = value
//
// Example:
//   pipeline := ssql.SetField("status", "active")
func SetField[V Value](field string, value V) Filter[Record, Record]
```

**Example usage:**
```go
// Set status to constant
pipeline := ssql.SetField("status", "active")

// Set count to constant
pipeline := ssql.SetField("count", int64(0))

// Chain multiple constant updates
pipeline := ssql.Pipe(
    records,
    ssql.SetField("status", "active"),
    ssql.SetField("updated_at", time.Now()),
)
```

### 4. Conditional Update

For updates with predicates (SQL WHERE clause):

```go
// UpdateWhere conditionally updates fields based on a predicate.
// Similar to SQL: UPDATE ... SET ... WHERE condition
//
// Records matching the predicate are updated.
// Records not matching pass through unchanged.
//
// Example:
//   pipeline := ssql.UpdateWhere(
//       func(r ssql.Record) bool {
//           status := ssql.GetOr(r, "status", "")
//           return status == "pending"
//       },
//       map[string]func(ssql.Record) string{
//           "status": func(r ssql.Record) string { return "processed" },
//       },
//   )
func UpdateWhere[V Value](
    predicate func(Record) bool,
    updates map[string]func(Record) V,
) Filter[Record, Record]
```

**Example usage:**
```go
// Update status only for pending records
pipeline := ssql.UpdateWhere(
    func(r ssql.Record) bool {
        return ssql.GetOr(r, "status", "") == "pending"
    },
    map[string]func(ssql.Record) string{
        "status": func(r ssql.Record) string { return "processed" },
    },
)
```

### 5. Typed Conditional Helpers

Combining conditional and typed updates:

```go
// SetFieldWhere conditionally sets a field to a constant value
func SetFieldWhere[V Value](
    predicate func(Record) bool,
    field string,
    value V,
) Filter[Record, Record]
```

**Example usage:**
```go
// Set status to "expired" only for old records
pipeline := ssql.SetFieldWhere(
    func(r ssql.Record) bool {
        created := ssql.GetOr(r, "created_at", time.Time{})
        return time.Since(created) > 30*24*time.Hour
    },
    "status",
    "expired",
)
```

## Implementation Details

### Internal Implementation Pattern

All UPDATE operations will follow this pattern (similar to existing operations like `RunningSum`):

```go
func Update[V Value](updates map[string]func(Record) V) Filter[Record, Record] {
    return func(source iter.Seq[Record]) iter.Seq[Record] {
        return func(yield func(Record) bool) {
            for record := range source {
                // Create mutable record for efficient field copying
                result := MakeMutableRecord()

                // Copy all existing fields
                for k, v := range record.All() {
                    result.fields[k] = v
                }

                // Apply updates
                for field, updateFn := range updates {
                    result.fields[field] = updateFn(record)
                }

                // Yield frozen (immutable) record
                if !yield(result.Freeze()) {
                    return
                }
            }
        }
    }
}
```

### Type Safety Through Value Constraint

The `Value` interface constraint (defined in core.go:233) ensures only valid types can be stored:

```go
type Value interface {
    // Canonical scalar types only
    ~int64 | ~float64 |

    // Other basic types
    ~bool | string | time.Time |

    // JSON and Record types for structured data
    JSONString | Record |

    // Iterator types - allow all numeric variants
    iter.Seq[int] | iter.Seq[int8] | ... | iter.Seq[Record]
}
```

This means:
- ✅ `Update(map[string]func(Record) int64{...})` - Compiles (int64 satisfies Value)
- ✅ `Update(map[string]func(Record) string{...})` - Compiles (string satisfies Value)
- ❌ `Update(map[string]func(Record) int{...})` - Compile error (int doesn't satisfy Value, use int64)
- ❌ `Update(map[string]func(Record) float32{...})` - Compile error (float32 doesn't satisfy Value, use float64)

### Handling Multiple Field Types

If you need to update fields with different types, you have two options:

**Option 1: Use `any` with the generic Update (runtime check):**
```go
updates := map[string]func(ssql.Record) any{
    "status": func(r ssql.Record) any { return "active" },
    "count": func(r ssql.Record) any { return int64(42) },
    "rate": func(r ssql.Record) any { return float64(0.5) },
}
pipeline := ssql.Update(updates)
```

Note: While `any` satisfies the `Value` constraint through the underlying types, the responsibility is on the developer to ensure returned values are valid. The type system can't verify this at compile time.

**Option 2: Chain multiple typed updates (recommended for type safety):**
```go
pipeline := ssql.Pipe(
    records,
    ssql.UpdateString("status", func(r ssql.Record) string {
        return "active"
    }),
    ssql.UpdateInt("count", func(r ssql.Record) int64 {
        return int64(42)
    }),
    ssql.UpdateFloat("rate", func(r ssql.Record) float64 {
        return float64(0.5)
    }),
)
```

This approach is more verbose but provides compile-time type safety for each field.

## Usage Examples

### Example 1: Add Computed Field

```go
// Calculate total from price * quantity
records := ssql.ReadCSV("orders.csv")

withTotal := ssql.Pipe(
    records,
    ssql.UpdateFloat("total", func(r ssql.Record) float64 {
        price := ssql.GetOr(r, "price", float64(0))
        qty := ssql.GetOr(r, "quantity", int64(0))
        return price * float64(qty)
    }),
)
```

### Example 2: Normalize Field Values

```go
// Convert status to uppercase
normalized := ssql.Pipe(
    records,
    ssql.UpdateString("status", func(r ssql.Record) string {
        status := ssql.GetOr(r, "status", "")
        return strings.ToUpper(status)
    }),
)
```

### Example 3: Add Multiple Derived Fields

```go
// Add both tax and grand_total
withTaxAndTotal := ssql.Pipe(
    records,
    ssql.UpdateFloat("tax", func(r ssql.Record) float64 {
        total := ssql.GetOr(r, "total", float64(0))
        return total * 0.08
    }),
    ssql.UpdateFloat("grand_total", func(r ssql.Record) float64 {
        total := ssql.GetOr(r, "total", float64(0))
        tax := ssql.GetOr(r, "tax", float64(0))
        return total + tax
    }),
)
```

### Example 4: Conditional Status Update

```go
// Mark orders as "overdue" if created more than 30 days ago
withStatus := ssql.Pipe(
    records,
    ssql.SetFieldWhere(
        func(r ssql.Record) bool {
            created := ssql.GetOr(r, "created_at", time.Time{})
            return time.Since(created) > 30*24*time.Hour
        },
        "status",
        "overdue",
    ),
)
```

### Example 5: Flag Records Based on Threshold

```go
// Add "high_value" flag for orders over $1000
withFlags := ssql.Pipe(
    records,
    ssql.UpdateBool("high_value", func(r ssql.Record) bool {
        total := ssql.GetOr(r, "total", float64(0))
        return total > 1000.0
    }),
)
```

## CLI Integration

Add `ssql update` command for simple field updates:

```bash
# Set constant value
ssql read-csv orders.csv | ssql update -set status active

# Conditional update
ssql read-csv orders.csv | \
  ssql update -set status processed -if status eq pending

# Multiple updates
ssql read-csv orders.csv | \
  ssql update -set status active -set updated_at "2025-01-01"
```

The CLI command will be limited to constant value updates. For computed field updates, users should use the Go API or code generation.

### Code Generation Support

The `update` command should support the `-generate` flag:

```bash
export SSQLGO=1
ssql read-csv data.csv | \
  ssql update -set status active | \
  ssql generate go > program.go
```

This generates:
```go
records := ssql.ReadCSV("data.csv")
updated := ssql.SetField("status", "active")(records)
```

## Testing Strategy

### Unit Tests (operations_test.go)

1. **Type Safety Tests:**
   - Test that valid Value types compile and work
   - Document compile-time failures for invalid types (in comments)

2. **Basic Update Tests:**
   - Update single field with constant value
   - Update multiple fields with constant values
   - Update field with computed value based on other fields

3. **Conditional Update Tests:**
   - UpdateWhere with matching records
   - UpdateWhere with no matching records
   - UpdateWhere with partial matches

4. **Immutability Tests:**
   - Verify original records are not modified
   - Verify updates create new record instances

5. **Edge Cases:**
   - Update non-existent fields (should add them)
   - Update with empty updates map (should pass through unchanged)
   - Chain multiple updates in sequence

### Integration Tests (example_test.go)

- Complete pipeline examples showing real-world usage
- Examples combining Update with Where, Select, GroupBy
- Examples showing both library API and CLI usage

### CLI Tests (cmd/ssql/*_test.go)

- Test basic `-set` flag
- Test conditional updates with `-if`
- Test multiple field updates
- Test code generation with `-generate` flag

## Documentation Updates

### 1. CLAUDE.md
- Add UPDATE operations to API Naming Conventions section
- Add examples to operations documentation
- Note the Value constraint requirement

### 2. README.md (if needed)
- Add UPDATE to feature list if it's a headline feature

### 3. Example Code
- Add comprehensive examples to `example_test.go`
- Show both simple and complex update patterns

### 4. API Reference
- Document all new functions with godoc comments
- Include usage examples in godoc

## Migration Path

This is a new feature with no breaking changes:
- Existing code continues to work unchanged
- Users can migrate from `Select()` workarounds at their own pace
- No changes to Record, MutableRecord, or Value types

## Alternatives Considered

### Alternative 1: Mutable Update
```go
// NOT CHOSEN: Would break immutability guarantees
UpdateInPlace(updates map[string]func(Record))  // Mutates records
```
**Rejected because:** ssql emphasizes immutability. All operations create new records.

### Alternative 2: Non-generic Update with `any`
```go
// NOT CHOSEN: Would lose compile-time type safety
Update(updates map[string]func(Record) any)
```
**Rejected because:** The Value constraint is a core design principle of ssql (see CLAUDE.md "Canonical Numeric Types"). Losing compile-time type checking would be a regression.

### Alternative 3: Builder Pattern
```go
// NOT CHOSEN: Would be verbose for simple cases
ssql.NewUpdater().
    SetField("status", "active").
    SetField("count", int64(42)).
    Build()
```
**Rejected because:** Doesn't fit the Filter-based functional composition style used throughout ssql. The map-based approach is more concise and fits better with Pipe().

## Open Questions

1. **Should Update support deleting fields?**
   - Option: `Update` returns a special sentinel value to indicate deletion
   - Current decision: No, keep Update focused on setting values. Use Select() for field removal.

2. **Should we support bulk updates on sequences?**
   - All records get the same field set to the same value
   - Current decision: Not needed - `SetField()` already does this efficiently

3. **Should we add UpdateJSON for JSON field updates?**
   - Example: `UpdateJSON("config", func(r Record) JSONString { ... })`
   - Current decision: Wait for user demand, can add later without breaking changes

## Implementation Checklist

- [ ] Implement `Update[V Value]` in operations.go
- [ ] Implement typed helpers (UpdateString, UpdateInt, UpdateFloat, UpdateBool, UpdateTime)
- [ ] Implement `SetField[V Value]`
- [ ] Implement `UpdateWhere[V Value]`
- [ ] Implement `SetFieldWhere[V Value]`
- [ ] Add unit tests in operations_test.go
- [ ] Add integration examples in example_test.go
- [ ] Add CLI `update` command in cmd/ssql/main.go
- [ ] Add code generation support to `update` command
- [ ] Add CLI tests for `update` command
- [ ] Update CLAUDE.md documentation
- [ ] Update godoc comments
- [ ] Bump version to v1.3.0 (new feature)
- [ ] Create git tag and release

## Timeline Estimate

- Core implementation: 2-3 hours
- CLI integration: 1-2 hours
- Tests: 2-3 hours
- Documentation: 1 hour
- **Total: 6-9 hours**

## Version

This feature will be released as **v1.3.0** (minor version bump for new functionality).
