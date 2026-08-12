# Typed Code Generation: Compatibility Audit

Reference: DFC030
Created: 2025-12-13
Last modified: 2026-03-12

[Back to Index](./README.md)

## Overview

This document audits ssql's CLI commands and library API for compatibility with typed code generation. The goal is to identify constructs that:

- **✅ Translate well** - Can generate efficient typed Go code
- **⚠️ Need modification** - Can work with small changes
- **❌ Problematic** - Fundamentally incompatible with typed approach

## Key Insight

Typed code generation requires **compile-time knowledge of field names and types**. Any construct that relies on runtime field name resolution or dynamic field access cannot be efficiently typed.

---

## CLI Commands Audit

### ✅ Fully Translatable Commands

These commands use static field names known at CLI parse time:

| Command | Pattern | Why It Works |
|---------|---------|--------------|
| `from` | `ssql from data.csv` | Schema derived from header row |
| `where -if` | `-if age gt 18` | Field name is literal |
| `update -set` | `-set status done` | Field name and value are literals |
| `include` | `ssql include name age` | Field names are literals |
| `exclude` | `ssql exclude id` | Field names are literals |
| `rename` | `-as oldname newname` | Field names are literals |
| `cast` | `-type age int` | Field name and type are literals |
| `sort` | `ssql sort age` | Field name is literal |
| `distinct` | `ssql distinct` | No field names needed |
| `limit` | `ssql limit 10` | No field names needed |
| `offset` | `ssql offset 5` | No field names needed |
| `join` | `-on id` | Join field is literal |
| `union` | Union streams | Schema preserved |
| `group-by` | `-by category -agg count _` | Fields and aggregations are literals |
| `to csv/json/table` | Output formats | Schema known from input |

**Example Typed Generation:**
```go
// CLI: ssql from users.csv | ssql where -if age gt 18 | ssql include name age

type InputRow struct {
    Name string
    Age  int64
    // ... other CSV fields
}

type OutputRow struct {
    Name string
    Age  int64
}

func main() {
    records := readCSV[InputRow]("users.csv")
    filtered := where(records, func(r InputRow) bool { return r.Age > 18 })
    result := project(filtered, func(r InputRow) OutputRow {
        return OutputRow{Name: r.Name, Age: r.Age}
    })
}
```

### ⚠️ Commands Needing Modification

These commands have features that need changes for typed generation:

#### 1. `where -if-expr` (Expression evaluation)

**Current:** Runtime expression evaluation using expr-lang
```bash
ssql where -if-expr 'age > 18 && verified == true'
```

**Problem:** Expressions are evaluated at runtime against `map[string]any`. Cannot generate typed predicates without parsing the expression.

**Solution Options:**
1. **Parse expressions to typed predicates** - Generate Go code from expr syntax
2. **Restrict to simple expressions** - Only support comparisons translatable to typed code
3. **Remove feature** - Force use of `-if field op value` syntax

**Recommendation:** Parse expressions at generation time to produce typed predicates. Most expressions (`age > 18`) can translate directly to struct field access.

#### 2. `update -set-expr` (Expression-based updates)

**Current:** Runtime expression for computed values
```bash
ssql update -set-expr discount 'total * 0.1'
```

**Problem:** Same as `-if-expr` - runtime expression evaluation.

**Solution:** Parse expressions to generate typed update functions:
```go
// Generated from: -set-expr discount 'total * 0.1'
func updateRecord(r InputRow) OutputRow {
    return OutputRow{
        ...r,
        Discount: r.Total * 0.1,
    }
}
```

#### 3. `sort` (Dynamic field key extraction)

**Current:** Extracts field value at runtime for comparison
```bash
ssql sort amount -desc
```

**Problem:** `SortBy` uses `func(r Record) float64` which requires runtime field lookup.

**Solution:** Generate typed sort key function:
```go
// Generated from: ssql sort amount -desc
slices.SortFunc(records, func(a, b Row) int {
    return cmp.Compare(b.Amount, a.Amount) // Descending
})
```

**Impact:** Minor - just need to generate typed accessor instead of `GetOr`.

#### 4. `distinct` (Record equality)

**Current:** Uses `fmt.Sprintf("%v", r)` for equality comparison
```bash
ssql distinct
```

**Problem:** Relies on runtime string conversion for comparison.

**Solution:** Generate typed equality or use struct comparison:
```go
// Option 1: Use struct equality (if all fields comparable)
seen := make(map[Row]bool)

// Option 2: Generate hash function
func rowKey(r Row) string {
    return fmt.Sprintf("%d|%s|%v", r.ID, r.Name, r.Amount)
}
```

### ❌ Problematic Constructs

These constructs are fundamentally incompatible with typed generation:

#### 1. Dynamic Field Iteration (`r.All()`, `for k, v := range record`)

**Pattern:**
```go
for k, v := range record.All() {
    // Process each field dynamically
}
```

**Problem:** Typed structs don't have dynamic field iteration. You can't iterate over struct fields without reflection.

**Impact:** This is used in:
- `include` command (to check which fields to keep)
- `exclude` command (to delete specified fields)
- Output writers (to serialize all fields)

**Solutions:**
1. **Generate explicit field lists** - At code generation time, enumerate all fields
2. **Use reflection** (defeats purpose) - Would lose performance benefit
3. **Redesign operations** - Project to new struct type instead of deleting fields

**Recommendation:** For typed generation, transform `include`/`exclude` to projection operations:
```go
// Instead of: delete fields not in list
// Generate: project to new struct with only those fields

type IncludedRow struct {
    Name string
    Age  int64
}

func project(r FullRow) IncludedRow {
    return IncludedRow{Name: r.Name, Age: r.Age}
}
```

#### 2. Dynamic Field Names at Runtime

**Pattern:**
```go
fieldName := someFunction() // Unknown at compile time
value := ssql.GetOr(r, fieldName, "")
```

**Problem:** Cannot generate typed field access when field name isn't a literal.

**Impact:** This pattern is used in:
- Some internal operations
- Tests with dynamic field names

**Recommendation:** Typed generation requires all field names to be literals known at generation time. This is naturally satisfied by CLI usage.

#### 3. `SetAny` / Arbitrary Type Values

**Pattern:**
```go
mut.SetAny("field", arbitraryValue) // Type not known at compile time
```

**Problem:** Typed structs have specific types per field.

**Impact:** Not exposed in CLI, but used internally.

**Recommendation:** Typed records have explicit field types. Values must match:
```go
type Row struct {
    Status string  // Must set string
    Count  int64   // Must set int64
}
```

---

## Library API Audit

### ✅ Translatable API

| API | Typed Equivalent |
|-----|------------------|
| `Select[T,U](fn func(T) U)` | Direct use - already generic |
| `Where[T](fn func(T) bool)` | Direct use - already generic |
| `Limit[T](n)` | Direct use - already generic |
| `Offset[T](n)` | Direct use - already generic |
| `SortBy[T,K](fn func(T) K)` | Generate with typed struct field access |
| `DistinctBy[T,K](fn func(T) K)` | Generate with typed hash function |
| `Pipe[T,U,V]` | Direct use - enables type-changing pipelines |
| `Chain[T]` | Direct use - for same-type operations |
| `Join` (all variants) | Generate typed join with specific struct types |
| `GroupByFields` | Generate typed grouping |
| `Aggregate` | Generate typed aggregations |

### ⚠️ Record-Specific API (Not Applicable to Typed)

These are specific to the dynamic `Record` type:

| API | Typed Equivalent |
|-----|------------------|
| `Record.All()` | Not needed - use struct fields directly |
| `Get[T](r, field)` | Not needed - use `r.FieldName` |
| `GetOr[T](r, field, def)` | Not needed - use `r.FieldName` |
| `MutableRecord` | Not needed - construct struct directly |
| `Record.ToMutable()` | Not needed - structs are value types |
| `MutableRecord.Freeze()` | Not needed - structs are immutable by copy |
| `Update(fn)` | Generate direct struct transformation |

### ❌ Problematic API

| API | Issue |
|-----|-------|
| `DotFlatten` | Requires dynamic field creation |
| `CrossFlatten` | Requires dynamic field expansion |
| `Materialize` | Converts sequences to strings dynamically |
| `ValidateRecord` | Runtime type checking |
| `RecordKey` | Dynamic field serialization |

---

## Recommendations

### 1. Keep: Core Pipeline Operations

All core operations (`Select`, `Where`, `Limit`, `Offset`, `Sort`, `Distinct`, `Join`, `GroupBy`) work perfectly with typed generation.

### 2. Modify: Expression Flags

The `-if-expr` and `-set-expr` flags need expression parsing at generation time. Two options:

**Option A: Parse to typed code (Recommended)**
- Parse expr-lang syntax to generate Go predicates
- Requires mapping expr operations to Go operators
- Covers 90%+ of use cases

**Option B: Keep as fallback**
- Generate typed code for literal flags
- Fall back to `map[string]any` + expr when expressions used
- Loses performance benefit for expression-heavy pipelines

### 3. Remove/Redesign: Dynamic Field Operations

Consider removing or redesigning for typed generation:

| Feature | Recommendation |
|---------|----------------|
| `DotFlatten` | **Remove** - rarely used, complex to type |
| `CrossFlatten` | **Remove** - complex cartesian product |
| `Materialize` | **Remove** - use typed aggregations instead |
| `Record.All()` iteration | **Redesign** - generate explicit field lists |

### 4. Two-Tier System

Consider a two-tier approach:

1. **Typed CLI Generation** - For performance-critical pipelines
   - All field names must be literals
   - Expressions parsed to typed predicates
   - Struct types generated per pipeline stage

2. **Dynamic CLI Execution** - For exploratory/ad-hoc use
   - Keep current `map[string]any` Record system
   - Full flexibility with runtime expressions
   - Used for prototyping before generating typed code

---

## Migration Path

### Phase 1: Audit Complete (This Document)
- ✅ Identified translatable constructs
- ✅ Identified problematic constructs
- ✅ Documented recommendations

### Phase 2: Expression Parser
- Parse expr-lang to Go AST
- Generate typed predicates from expressions
- Test with common expression patterns

### Phase 3: Typed Code Generator
- Generate struct types from CSV headers / pipeline stages
- Generate typed join structs
- Generate typed aggregation structs

### Phase 4: CLI Integration
- Add `ssql generate-typed` command
- Generate complete typed Go programs
- Benchmark vs current generation

---

## Summary

| Category | Count | Action |
|----------|-------|--------|
| ✅ Fully translatable | 14 commands | Keep as-is |
| ⚠️ Need modification | 4 patterns | Parse expressions, generate typed accessors |
| ❌ Problematic | 3 features | Consider removing from typed path |

**Overall Assessment:** The vast majority of ssql functionality (>90%) translates directly to typed code generation. The main work is:

1. Expression parsing for `-if-expr` and `-set-expr`
2. Generating struct types per pipeline stage
3. Typed join result struct generation

The potential 35x performance improvement (from benchmarks) justifies this investment for performance-critical workloads while keeping the dynamic system for exploratory use.
