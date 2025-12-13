# Typed Code Generation: 35x Performance Through Type Specialization

## Executive Summary

This document explores generating **typed struct-based code** instead of `map[string]any` Record-based code. Benchmarks show **35x speedup** and **82,000x less memory allocation** for large datasets.

Two generation paths are proposed:
1. **CLI → Typed Go**: Generate optimized Go from CLI pipelines
2. **ssql Go → Optimized Go**: AST-transform existing ssql programs

**This is the most significant performance optimization opportunity identified.**

## The Insight

Go generics allow type-safe transformations between different struct types:

```go
// Generic filter - works with ANY struct type
func Where[T any](pred func(T) bool) func(iter.Seq[T]) iter.Seq[T]

// Can chain different types with Pipe (not Chain)
result := Pipe(
    Pipe(records, Where[InputRow](...)),      // InputRow -> InputRow
    Join[InputRow, JoinedRow](...),           // InputRow -> JoinedRow
)
```

The key insight: **code generation knows the schema at generation time**, so we can generate specialized struct types and avoid all `map[string]any` overhead.

## Benchmark Results

### 10 Million Records, 3 Chained Joins

```
Map-based (current):
  Time:   12.2 seconds
  Memory: 17.66 GB allocated

Typed structs:
  Time:   348 milliseconds
  Memory: 0.22 MB allocated

Speedup: 35x
Memory:  82,000x less
```

### Why Typed Code Is Faster

| Aspect | Map-based | Typed Structs |
|--------|-----------|---------------|
| Field access | Hash lookup | Direct offset |
| Memory layout | Scattered pointers | Contiguous |
| Allocation per record | New map + entries | Often zero (stack) |
| GC pressure | Massive (70% of CPU) | Minimal |
| Type assertions | Runtime | None (compile-time) |
| Compiler optimization | Limited | Full inlining |

### Projected Real-World Impact

For the 14.6M record, 3-join workload:
- Current generated code: ~70 seconds
- Projected typed code: ~**2 seconds**

## Path 1: CLI → Typed Go Code

### Current Code Generation

```bash
SSQLGO=1 ssql from data.csv | ssql where -where age gt 18 | ssql join lookup.csv -on dept_id | ssql to csv output.csv | ssql generate-go
```

Produces:
```go
func main() {
    records, _ := ssql.ReadCSV("data.csv")
    filtered := ssql.Where(func(r ssql.Record) bool {
        return ssql.GetOr(r, "age", int64(0)) > 18
    })(records)
    lookup, _ := ssql.ReadCSV("lookup.csv")
    joined := ssql.InnerJoin(lookup, ssql.OnFields("dept_id"))(filtered)
    ssql.WriteCSV(joined, "output.csv")
}
```

### Proposed Typed Code Generation

Same CLI command with `--typed` flag or `SSQLGO=typed`:

```bash
SSQLGO=typed ssql from data.csv | ssql where -where age gt 18 | ssql join lookup.csv -on dept_id | ssql to csv output.csv | ssql generate-go
```

Produces:
```go
// Auto-generated struct types based on CSV schemas
type DataRow struct {
    Name   string
    Age    int64
    DeptID string
    Salary float64
}

type LookupRow struct {
    DeptID   string
    DeptName string
    Location string
}

type JoinedRow struct {
    Name     string
    Age      int64
    DeptID   string
    Salary   float64
    DeptName string
    Location string
}

func main() {
    records := readCSVTyped[DataRow]("data.csv")

    filtered := ssql.Where(func(r DataRow) bool {
        return r.Age > 18  // Direct field access!
    })(records)

    lookup := readCSVTyped[LookupRow]("lookup.csv")
    lookupMap := buildLookup(lookup, func(r LookupRow) string { return r.DeptID })

    joined := joinTyped(filtered, lookupMap,
        func(l DataRow) string { return l.DeptID },
        func(l DataRow, r LookupRow) JoinedRow {
            return JoinedRow{
                Name:     l.Name,
                Age:      l.Age,
                DeptID:   l.DeptID,
                Salary:   l.Salary,
                DeptName: r.DeptName,
                Location: r.Location,
            }
        },
    )

    writeCSVTyped(joined, "output.csv")
}

// Generated helper functions
func readCSVTyped[T any](filename string) iter.Seq[T] { ... }
func buildLookup[T any, K comparable](seq iter.Seq[T], key func(T) K) map[K]T { ... }
func joinTyped[L, R, O any, K comparable](...) iter.Seq[O] { ... }
func writeCSVTyped[T any](seq iter.Seq[T], filename string) { ... }
```

### Implementation Requirements

#### 1. Schema Discovery

The generator needs to know field names and types. Options:

**Option A: Sample the files at generation time**
```bash
# Generator reads first N rows of each CSV to infer schema
SSQLGO=typed ssql from data.csv ...
# Reads data.csv header + sample rows to determine types
```

**Option B: Explicit schema flags**
```bash
ssql from data.csv --schema "name:string,age:int64,dept_id:string,salary:float64" ...
```

**Option C: Schema file**
```bash
ssql from data.csv --schema-file data.schema.json ...
```

**Recommendation:** Option A (sample files) for ease of use, with Option B/C for cases where files aren't available at generation time.

#### 2. Type Flow Tracking

Each command must track how types transform:

| Command | Type Transformation |
|---------|---------------------|
| `from` | → `SourceRow` |
| `where` | `T` → `T` (unchanged) |
| `include` | `T` → `T'` (subset of fields) |
| `exclude` | `T` → `T'` (subset of fields) |
| `rename` | `T` → `T'` (field renamed) |
| `update -set` | `T` → `T'` (field type may change) |
| `join` | `T` + `U` → `Joined_T_U` |
| `group-by` | `T` → `GroupedRow` |
| `union` | `T` + `T` → `T` |

#### 3. Code Fragment Extension

Current code fragments pass via JSONL:
```json
{"type": "stmt", "var": "filtered", "input": "records", "code": "...", "imports": [...]}
```

Extended for typed generation:
```json
{
  "type": "stmt",
  "var": "filtered",
  "input": "records",
  "code": "...",
  "imports": [...],
  "inputType": {"name": "DataRow", "fields": [{"name": "Age", "type": "int64"}, ...]},
  "outputType": {"name": "DataRow", "fields": [...]},
  "structDefs": ["type DataRow struct { ... }"]
}
```

#### 4. Helper Library

A new package `ssql/typed` with generic helpers:

```go
package typed

// Generic CSV reader - uses reflection to map columns to struct fields
func ReadCSV[T any](filename string) iter.Seq[T]

// Generic operations that work with any struct
func Where[T any](pred func(T) bool) func(iter.Seq[T]) iter.Seq[T]
func Limit[T any](n int) func(iter.Seq[T]) iter.Seq[T]
func Offset[T any](n int) func(iter.Seq[T]) iter.Seq[T]

// Join helper
func HashJoin[L, R, O any, K comparable](
    left iter.Seq[L],
    right iter.Seq[R],
    leftKey func(L) K,
    rightKey func(R) K,
    merge func(L, R) O,
) iter.Seq[O]

// Generic CSV writer
func WriteCSV[T any](seq iter.Seq[T], filename string) error
```

### Code Generation Changes

#### generate-go command

```go
// In generate-go handler
if typedMode {
    // Collect all struct definitions from fragments
    structDefs := collectStructDefs(fragments)

    // Generate imports including reflect (for CSV parsing)
    imports := mergeImports(fragments)
    imports = append(imports, "github.com/rosscartlidge/ssql/v3/typed")

    // Output struct definitions first
    for _, def := range structDefs {
        fmt.Println(def)
    }

    // Then the main function with typed operations
    generateTypedMain(fragments)
}
```

#### Per-command changes

Each command's generation function needs a typed variant:

```go
// where command
func generateWhereTyped(field, op, value string, inputType TypeInfo) CodeFragment {
    // Generate direct field access instead of GetOr
    var condition string
    switch op {
    case "gt":
        condition = fmt.Sprintf("r.%s > %s", toPascalCase(field), value)
    case "eq":
        condition = fmt.Sprintf("r.%s == %s", toPascalCase(field), formatValue(value, inputType.FieldType(field)))
    // ...
    }

    code := fmt.Sprintf(`typed.Where(func(r %s) bool {
    return %s
})`, inputType.Name, condition)

    return CodeFragment{
        Code:       code,
        InputType:  inputType,
        OutputType: inputType, // Where doesn't change type
    }
}
```

## Path 2: ssql Go → Optimized Go (AST Transformation)

### Overview

A source-to-source compiler that transforms ssql Record-based code into typed struct code.

```bash
ssql-optimize input.go > optimized.go
```

### Example Transformation

**Input (user-written):**
```go
package main

import "github.com/rosscartlidge/ssql/v3"

func main() {
    records, _ := ssql.ReadCSV("employees.csv")

    seniors := ssql.Where(func(r ssql.Record) bool {
        return ssql.GetOr(r, "years", int64(0)) > 5
    })(records)

    depts, _ := ssql.ReadCSV("departments.csv")

    joined := ssql.InnerJoin(depts, ssql.OnFields("dept_id"))(seniors)

    ssql.WriteCSV(joined, "output.csv")
}
```

**Output (generated):**
```go
package main

import (
    "github.com/rosscartlidge/ssql/v3/typed"
    "iter"
)

// Inferred from employees.csv
type EmployeeRow struct {
    Name   string  `csv:"name"`
    DeptID string  `csv:"dept_id"`
    Years  int64   `csv:"years"`
    Salary float64 `csv:"salary"`
}

// Inferred from departments.csv
type DepartmentRow struct {
    DeptID   string `csv:"dept_id"`
    DeptName string `csv:"dept_name"`
    Location string `csv:"location"`
}

// Generated for join result
type EmployeeDeptRow struct {
    Name     string
    DeptID   string
    Years    int64
    Salary   float64
    DeptName string
    Location string
}

func main() {
    records := typed.ReadCSV[EmployeeRow]("employees.csv")

    seniors := typed.Where(func(r EmployeeRow) bool {
        return r.Years > 5  // Direct field access
    })(records)

    depts := typed.ReadCSV[DepartmentRow]("departments.csv")
    deptMap := typed.BuildIndex(depts, func(r DepartmentRow) string { return r.DeptID })

    joined := typed.HashJoin(seniors, deptMap,
        func(e EmployeeRow) string { return e.DeptID },
        func(e EmployeeRow, d DepartmentRow) EmployeeDeptRow {
            return EmployeeDeptRow{
                Name:     e.Name,
                DeptID:   e.DeptID,
                Years:    e.Years,
                Salary:   e.Salary,
                DeptName: d.DeptName,
                Location: d.Location,
            }
        },
    )

    typed.WriteCSV(joined, "output.csv")
}
```

### AST Analysis Requirements

#### 1. Pattern Detection

Detect ssql usage patterns:

```go
// Patterns to detect:
ssql.ReadCSV(filename)                           // → infer schema from file
ssql.GetOr(r, "field", defaultValue)             // → field name + type
ssql.Get[Type](r, "field")                       // → field name + type
ssql.Where(func(r ssql.Record) bool { ... })     // → extract predicate
ssql.InnerJoin(right, ssql.OnFields("f1", "f2")) // → join fields
ssql.Select(func(r ssql.Record) ssql.Record { ... }) // → field transformations
```

#### 2. Type Inference

Build type information from:

1. **CSV files** - read headers and sample data
2. **GetOr calls** - `GetOr(r, "age", int64(0))` tells us `age` is `int64`
3. **Get calls** - `Get[string](r, "name")` tells us `name` is `string`
4. **Literals** - `r.fields["status"] = "active"` tells us `status` is `string`

#### 3. Data Flow Analysis

Track `ssql.Record` through the program:

```go
records, _ := ssql.ReadCSV("data.csv")  // records: Seq[DataRow]
filtered := ssql.Where(...)(records)    // filtered: Seq[DataRow]
joined := ssql.InnerJoin(...)(filtered) // joined: Seq[JoinedRow]
```

#### 4. Code Rewriting

Use `go/ast` and `go/printer` to:
1. Parse source file
2. Build type information
3. Generate struct definitions
4. Rewrite function calls
5. Output transformed code

### Implementation Sketch

```go
package main

import (
    "go/ast"
    "go/parser"
    "go/printer"
    "go/token"
    "go/types"
)

type Optimizer struct {
    fset    *token.FileSet
    file    *ast.File
    info    *types.Info
    schemas map[string]*Schema  // variable name -> schema
}

func (o *Optimizer) Optimize(filename string) (*ast.File, error) {
    // 1. Parse the source
    o.fset = token.NewFileSet()
    file, err := parser.ParseFile(o.fset, filename, nil, parser.ParseComments)
    if err != nil {
        return nil, err
    }
    o.file = file

    // 2. Type check to get type info
    o.info = &types.Info{
        Types: make(map[ast.Expr]types.TypeAndValue),
        Uses:  make(map[*ast.Ident]types.Object),
    }
    // ... type checking ...

    // 3. Find all ssql.ReadCSV calls, infer schemas
    o.inferSchemas()

    // 4. Track data flow to determine types at each point
    o.trackDataFlow()

    // 5. Generate struct definitions
    structDefs := o.generateStructs()

    // 6. Rewrite AST
    o.rewriteCalls()

    // 7. Insert struct definitions
    o.insertStructs(structDefs)

    // 8. Update imports
    o.updateImports()

    return o.file, nil
}

func (o *Optimizer) inferSchemas() {
    ast.Inspect(o.file, func(n ast.Node) bool {
        call, ok := n.(*ast.CallExpr)
        if !ok {
            return true
        }

        // Check if this is ssql.ReadCSV(filename)
        if isSSQLReadCSV(call) {
            filename := extractStringArg(call, 0)
            schema := inferSchemaFromCSV(filename)

            // Find the variable this is assigned to
            // ... and store in o.schemas
        }

        return true
    })
}

func (o *Optimizer) rewriteCalls() {
    // Rewrite ssql.GetOr(r, "field", default) → r.Field
    // Rewrite ssql.Where(...) → typed.Where(...)
    // Rewrite ssql.InnerJoin(...) → typed.HashJoin(...)
    // etc.
}
```

### Limitations and Edge Cases

#### Cannot Optimize

1. **Dynamic field names:**
   ```go
   field := getFieldName()  // runtime value
   ssql.GetOr(r, field, "")  // can't know field at compile time
   ```

2. **Reflection-based access:**
   ```go
   for k, v := range r.All() { ... }  // iterating all fields
   ```

3. **Conditional schemas:**
   ```go
   if useSchemaA {
       records, _ = ssql.ReadCSV("a.csv")
   } else {
       records, _ = ssql.ReadCSV("b.csv")  // different schema!
   }
   ```

4. **External function calls:**
   ```go
   records := loadData()  // can't see inside this function
   ```

#### Fallback Strategy

When optimization isn't possible, keep original code:

```go
// Original (cannot optimize - dynamic field)
value := ssql.GetOr(r, dynamicField, "")

// Generated comment
// ssql-optimize: cannot optimize - dynamic field name at line 42
value := ssql.GetOr(r, dynamicField, "")
```

### CLI Tool Design

```bash
# Basic usage
ssql-optimize input.go > optimized.go

# With options
ssql-optimize --schema-dir ./schemas input.go > optimized.go
ssql-optimize --verbose input.go  # show what was optimized
ssql-optimize --check input.go    # just report what could be optimized

# Example output with --verbose
$ ssql-optimize --verbose pipeline.go
Analyzing pipeline.go...
  Line 12: ssql.ReadCSV("data.csv") → inferred DataRow (5 fields)
  Line 15: ssql.Where with GetOr("age") → optimized to direct field access
  Line 18: ssql.InnerJoin on "dept_id" → optimized to HashJoin
  Line 21: ssql.WriteCSV → optimized to typed.WriteCSV

Generated types:
  - DataRow (name, age, dept_id, salary, active)
  - LookupRow (dept_id, dept_name)
  - JoinedRow (7 fields)

Optimization summary:
  - 4 operations optimized
  - 0 operations skipped
  - Expected speedup: ~20-35x for large datasets
```

## Implementation Roadmap

### Phase 1: Typed Helper Library

Create `ssql/typed` package with generic operations:

```go
// typed/io.go
func ReadCSV[T any](filename string) iter.Seq[T]
func WriteCSV[T any](seq iter.Seq[T], filename string) error

// typed/operations.go
func Where[T any](pred func(T) bool) func(iter.Seq[T]) iter.Seq[T]
func Limit[T any](n int) func(iter.Seq[T]) iter.Seq[T]
func Select[T, U any](fn func(T) U) func(iter.Seq[T]) iter.Seq[U]

// typed/join.go
func HashJoin[L, R, O any, K comparable](
    left iter.Seq[L],
    right iter.Seq[R],
    leftKey func(L) K,
    rightKey func(R) K,
    merge func(L, R) O,
) iter.Seq[O]
```

**Effort:** 2-3 days
**Risk:** Low

### Phase 2: CLI Typed Generation

Add `--typed` mode to `generate-go`:

1. Schema inference from CSV files
2. Struct type generation
3. Type flow tracking through pipeline
4. Typed code output

**Effort:** 1-2 weeks
**Risk:** Medium (schema inference complexity)

### Phase 3: AST Optimizer Tool

Build `ssql-optimize` source-to-source compiler:

1. AST parsing and analysis
2. Pattern detection for ssql calls
3. Schema inference from files and code
4. Data flow tracking
5. AST rewriting
6. Code generation

**Effort:** 2-4 weeks
**Risk:** High (AST manipulation complexity)

## Comparison of Approaches

| Aspect | CLI → Typed | Go → Optimized Go |
|--------|-------------|-------------------|
| User effort | Just add flag | Run separate tool |
| Flexibility | CLI features only | Full Go expressiveness |
| Debugging | Generated code only | Can debug original |
| Iteration speed | Fast (CLI) | Slower (write Go) |
| Optimization potential | High | Highest |
| Implementation complexity | Medium | High |

## Recommendations

### Short Term
1. **Implement Phase 1** - typed helper library
2. **Benchmark extensively** - validate 35x claim on real workloads
3. **Document patterns** - what code can/cannot be optimized

### Medium Term
4. **Implement Phase 2** - CLI typed generation
5. **User feedback** - is the workflow acceptable?
6. **Iterate on schema inference** - handle edge cases

### Long Term
7. **Consider Phase 3** - only if demand exists
8. **IDE integration** - show optimization suggestions inline
9. **Hybrid mode** - optimize what's possible, fall back for rest

## Conclusion

Typed code generation offers **35x speedup** by leveraging Go's type system instead of fighting it with `map[string]any`. The insight that **code generation knows schemas at generation time** unlocks this optimization.

Two complementary approaches:
1. **CLI → Typed Go**: Easiest path, extends existing code generation
2. **Go → Optimized Go**: Most flexible, allows full Go expressiveness

The workflow becomes:
1. **Prototype** with CLI or Record-based ssql code (flexible, easy)
2. **Optimize** with typed generation when performance matters (fast, efficient)

This represents the best of both worlds - flexibility for exploration, performance for production.

## Appendix: Benchmark Code

The benchmarks used in this analysis are available at:
- `/tmp/typed_bench.go` - 5M record benchmark
- `/tmp/typed_bench2.go` - 10M record benchmark

Key benchmark: 10M records, 3 joins
- Map-based: 12.2s, 17.66 GB
- Typed: 348ms, 0.22 MB
- **Speedup: 35x**
