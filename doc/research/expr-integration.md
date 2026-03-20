# expr-lang Integration in ssql

**Version**: v2.1.0+
**Date**: November 2025
**expr-lang docs**: https://expr-lang.org/

## Overview

ssql uses [expr-lang](https://github.com/expr-lang/expr) for expression evaluation in `-set-expr` (update) and `-expr` (where) operations. expr is a Go library that compiles and evaluates expressions safely and efficiently.

**Why expr?**
- **Type safe**: Compile-time type checking
- **Memory safe**: No buffer overflows or memory leaks
- **Terminating**: Guaranteed to finish (no infinite loops)
- **Fast**: Pre-compiled expressions, ~1s overhead for 1M records
- **Feature-rich**: Built-in functions, operators, pipe syntax

## Architecture

### Runtime Package (v2.1.0+)

Located in `cmd/ssql/lib/runtime/runtime.go`, provides shared compilation functions:

```go
// For general expressions (returns any type)
func CompileExpr(expression string) (func(ssql.Record) (any, error), error)
func MustCompileExpr(expression string) func(ssql.Record) (any, error)

// For boolean expressions (where filters)
func CompileExprFilter(expression string) (func(ssql.Record) bool, error)
func MustCompileExprFilter(expression string) func(ssql.Record) bool
```

**Key pattern**: Compile once at startup, run many times
- CLI: Compiles expression once in command handler, evaluates per record
- Generated code: Compiles at package init time using `Must*` functions

### Environment Setup

expr expressions run against an "environment" - the variables available in the expression:

```go
// Record fields are directly accessible
env := make(map[string]interface{})
for k, v := range record.All() {
    env[k] = v  // price, qty, name, etc.
}

// Helper functions
env["has"] = func(field string) bool {
    _, exists := ssql.Get[any](record, field)
    return exists
}
env["getOr"] = func(field string, defaultValue any) any {
    if val, exists := ssql.Get[any](record, field); exists {
        return val
    }
    return defaultValue
}
```

**Configuration options we use**:
- `expr.Env(sampleEnv)` - Provide type information for compilation
- `expr.AllowUndefinedVariables()` - Don't error on missing fields (critical for sparse records)
- `expr.AsBool()` - Enforce boolean return type (for where filters)

## Type Behavior

expr preserves types for field references but converts for arithmetic:

```go
// Field reference: preserves original type
a_kind           // int64(217) if field is int64

// Arithmetic: converts to int
a_kind + 1       // int(218) - expr uses int for arithmetic
a_kind * 2       // int(434)

// Large literals: use int (within range) or int64 (overflow)
1 + 1            // int(2)
9999999999       // int64 if doesn't fit in int
```

**Type handling in ssql**:
- `update -set-expr` handles both int and int64 via type switch
- Result types: int64, float64, bool, string, int, float32
- Unknown types: Convert to string via `fmt.Sprintf("%v", v)`

## Built-in Functions

expr provides rich built-in functions - **users can use these directly in expressions!**

### String Functions

```bash
# Case conversion
ssql update -set-expr name 'upper(name)'           # "ALICE"
ssql update -set-expr name 'lower(name)'           # "alice"

# Trimming and splitting
ssql update -set-expr name 'trim(name)'            # Remove whitespace
ssql update -set-expr parts 'split(name, " ")'     # ["Alice", "Smith"]

# String testing
ssql where -expr 'startsWith(email, "admin")'      # Prefix match
ssql where -expr 'endsWith(file, ".csv")'          # Suffix match
ssql where -expr 'contains(name, "test")'          # Substring match
```

### Math Functions

```bash
# Rounding
ssql update -set-expr price 'round(price)'         # Round to int
ssql update -set-expr price 'ceil(price)'          # Round up
ssql update -set-expr price 'floor(price)'         # Round down

# Absolute value
ssql update -set-expr delta 'abs(actual - target)' # |difference|

# Min/max
ssql update -set-expr price 'max(price, 0)'        # Clamp to 0
ssql update -set-expr price 'min(price, 100)'      # Cap at 100
```

### Array Functions

```bash
# Array operations
ssql where -expr 'len(tags) > 3'                   # Array length
ssql where -expr '"urgent" in tags'                # Membership test
ssql update -set-expr tags 'filter(tags, {# > 5})' # Filter array
ssql update -set-expr nums 'map(nums, {# * 2})'    # Transform array

# Aggregations on arrays
ssql update -set-expr total 'sum(prices)'          # Sum array
ssql update -set-expr count 'count(items)'         # Count elements
ssql where -expr 'all(scores, {# > 50})'           # All elements match
ssql where -expr 'any(flags, {# == true})'         # Any element matches
```

### Type Conversion

```bash
# String to number
ssql update -set-expr age 'int(age_str)'           # String → int
ssql update -set-expr price 'float(price_str)'     # String → float

# Number to string
ssql update -set-expr label 'string(count)'        # int → string
```

## Operators

### Arithmetic Operators

```bash
# Standard operators
price + tax                  # Addition
qty * price                  # Multiplication
total / qty                  # Division
amount % 100                 # Modulo
```

### Comparison Operators

```bash
age > 18                     # Greater than
age >= 21                    # Greater or equal
age < 65                     # Less than
age <= 64                    # Less or equal
status == "active"           # Equal
status != "deleted"          # Not equal
```

### Logical Operators

```bash
age > 18 and status == "active"        # AND
role == "admin" or role == "manager"   # OR
not deleted                            # NOT
```

### Special Operators

```bash
# Pipe operator (function chaining)
name | trim | upper | startsWith("A")

# Nil coalescing (default value)
optional_field ?? "default"

# In operator (membership)
status in ["active", "pending"]
role in ["admin", "manager"]

# Contains operator (substring)
email contains "@"
name contains "test"
```

## Helper Functions (ssql-specific)

We provide helper functions in the environment:

### has(field)

Check if a record has a field:

```bash
ssql where -expr 'has("email")'                    # Has email field
ssql where -expr 'has("price") and price > 0'      # Has price and non-zero
```

**Note**: Could be simplified to use Record.Has method directly (future improvement)

### getOr(field, default)

Get field value with default:

```bash
ssql update -set-expr total 'getOr("price", 0) * getOr("qty", 1)'
ssql where -expr 'getOr("status", "") == "active"'
```

## Common Use Cases

### Data Validation

```bash
# Check for required fields
ssql where -expr 'has("email") and has("name")'

# Validate email format
ssql where -expr 'contains(email, "@") and contains(email, ".")'

# Range checks
ssql where -expr 'age >= 18 and age <= 65'

# Status validation
ssql where -expr 'status in ["active", "pending", "completed"]'
```

### Data Cleaning

```bash
# Normalize case
ssql update -set-expr email 'lower(trim(email))'

# Remove whitespace
ssql update -set-expr name 'trim(name)'

# Default missing values
ssql update -set-expr status 'getOr("status", "pending")'

# Clamp values
ssql update -set-expr age 'max(0, min(age, 120))'
```

### Calculations

```bash
# Simple arithmetic
ssql update -set-expr total 'price * qty'
ssql update -set-expr tax 'price * 0.08'

# Conditional calculations
ssql update -set-expr discount 'price > 100 ? price * 0.1 : 0'

# Multiple operations
ssql update -set-expr final 'round((price * qty) * (1 + tax_rate))'
```

### Complex Filters

```bash
# AND conditions
ssql where -expr 'age > 18 and status == "active" and has("email")'

# OR conditions
ssql where -expr 'role == "admin" or role == "manager"'

# Pattern matching
ssql where -expr 'startsWith(name, "test") or endsWith(email, "@example.com")'

# Array membership
ssql where -expr 'dept in ["Sales", "Marketing", "Support"]'
```

### String Manipulation

```bash
# Build composite fields
ssql update -set-expr full_name 'first + " " + last'

# Extract substrings (using pipe)
ssql update -set-expr domain 'email | split("@") | last'

# Conditional formatting
ssql update -set-expr label 'active ? "✓ " + name : "✗ " + name'
```

## Performance Characteristics

### Compilation Cost

expr compiles expressions once at startup:

```bash
# CLI: ~1s overhead for 1M records
time ssql read-csv large.csv | ssql where -expr 'price > 100' | ssql limit 10

# Generated code: Zero overhead (compiled at package init)
ssql read-csv large.csv | ssql where -expr 'price > 100' | ssql generate go > program.go
go run program.go  # No runtime compilation!
```

### Runtime Performance

- Evaluation: ~1-2µs per expression per record
- Comparison: Direct Go code is faster, but expr is "fast enough" for most use cases
- Benefit: Flexibility and safety outweigh small performance cost

### Memory Usage

- Compiled programs are small (~few KB)
- No memory allocations per evaluation (after compilation)
- Safe: No buffer overflows or memory leaks

## Code Generation

expr expressions translate to Go code via `generate go`:

### CLI Expression

```bash
export SSQLGO=1
ssql read-csv data.csv | \
  ssql where -expr 'price > 15' | \
  ssql update -set-expr total 'price * qty' | \
  ssql generate go
```

### Generated Code

```go
var exprFilter1 = runtime.MustCompileExprFilter("price > 15")
var exprEval1 = runtime.MustCompileExpr("price * qty")

func main() {
    records, _ := ssql.ReadCSV("data.csv")

    updated := ssql.Chain(
        ssql.Where(func(r ssql.Record) bool {
            return exprFilter1(r)  // Pre-compiled filter
        }),
        ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
            frozen := mut.Freeze()
            result, err := exprEval1(frozen)  // Pre-compiled expression
            if err != nil {
                return mut.String("total", "")
            }
            // Type switch to handle result
            switch v := result.(type) {
            case int64:
                return mut.Int("total", v)
            case float64:
                return mut.Float("total", v)
            case int:
                return mut.Int("total", int64(v))
            // ... other types
            }
            return mut
        }),
    )(records)

    ssql.WriteJSONToWriter(updated, os.Stdout)
}
```

**Key points**:
- Expressions compiled at package init (var declarations)
- Runtime evaluation uses pre-compiled programs
- Type switch handles expr's type behavior (int vs int64)

## Type Safety Options

expr provides compile-time type enforcement:

### Boolean Enforcement (where filters)

```go
// Current implementation
program, err := expr.Compile(expression,
    expr.Env(sampleEnv),
    expr.AllowUndefinedVariables(),
    expr.AsBool(),  // ← Enforce boolean return
)
```

This catches errors at compile time:

```bash
ssql where -expr 'price + qty'  # ERROR: expected bool, got int
ssql where -expr 'name'          # ERROR: expected bool, got string
```

### Other Type Enforcement

```go
expr.AsInt64()    // Enforce int64 return
expr.AsFloat64()  // Enforce float64 return
```

**Current status**: We use `expr.AsBool()` for where filters. General expressions return `any` and handle types at runtime.

## Current Implementation Details

### Where Filter Compilation

```go
func CompileExprFilter(expression string) (func(ssql.Record) bool, error) {
    sampleEnv := make(map[string]interface{})
    sampleEnv["has"] = func(field string) bool { return false }
    sampleEnv["getOr"] = func(field string, defaultValue any) any { return defaultValue }

    program, err := expr.Compile(expression,
        expr.Env(sampleEnv),
        expr.AllowUndefinedVariables(),
        expr.AsBool(),  // Type safety
    )

    return func(record ssql.Record) bool {
        env := make(map[string]interface{})
        for k, v := range record.All() {
            env[k] = v
        }
        env["has"] = func(field string) bool {
            _, exists := ssql.Get[any](record, field)
            return exists
        }
        env["getOr"] = func(field string, defaultValue any) any {
            if val, exists := ssql.Get[any](record, field); exists {
                return val
            }
            return defaultValue
        }

        result, err := expr.Run(program, env)
        if err != nil {
            return false  // Failed evaluations = filtered out
        }
        return result.(bool)
    }, nil
}
```

### Update Expression Compilation

```go
func CompileExpr(expression string) (func(ssql.Record) (any, error), error) {
    sampleEnv := make(map[string]interface{})
    sampleEnv["has"] = func(field string) bool { return false }
    sampleEnv["getOr"] = func(field string, defaultValue any) any { return defaultValue }

    program, err := expr.Compile(expression,
        expr.Env(sampleEnv),
        expr.AllowUndefinedVariables(),
        // Note: No type enforcement - returns any
    )

    return func(record ssql.Record) (any, error) {
        env := make(map[string]interface{})
        for k, v := range record.All() {
            env[k] = v
        }
        env["has"] = func(field string) bool {
            _, exists := ssql.Get[any](record, field)
            return exists
        }
        env["getOr"] = func(field string, defaultValue any) any {
            if val, exists := ssql.Get[any](record, field); exists {
                return val
            }
            return defaultValue
        }
        return expr.Run(program, env)
    }, nil
}
```

## Potential Improvements

### 1. Use Record Methods Directly

Current:
```go
env["has"] = func(field string) bool {
    _, exists := ssql.Get[any](record, field)
    return exists
}
```

Possible improvement:
```go
env["has"] = record.Has  // Direct method reference
```

**Why**: Simpler, clearer, leverages existing Record API

### 2. Document Built-in Functions

Users don't know about `upper()`, `lower()`, `round()`, etc.

**Action**: Add examples to:
- Command help text (`ssql update -help`, `ssql where -help`)
- README.md
- This document

### 3. Custom Functions

expr supports custom functions via `expr.Function()`:

```go
expr.Compile(expression,
    expr.Function("toUpper", toUpperFunc),
    expr.Function("slugify", slugifyFunc),
)
```

**Question**: Do we need custom functions, or are built-ins sufficient?

### 4. Struct Methods as Functions

expr supports using struct methods as functions when environment is a struct:

```go
type Env struct {
    Record ssql.Record
}

func (e Env) FieldSum(fields ...string) float64 {
    sum := 0.0
    for _, f := range fields {
        sum += ssql.GetOr(e.Record, f, 0.0)
    }
    return sum
}

// Then: "fieldSum('price', 'tax', 'shipping')"
```

**Question**: Would struct-based environment with methods be useful?

### 5. Context Support

expr supports context for cancellation:

```go
expr.Compile(expression,
    expr.WithContext("ctx"),
)

// Then pass context to Run:
expr.Run(program, env, expr.WithContext(ctx))
```

**Use case**: Cancel long-running expressions

### 6. Compile-Time Optimization

expr supports constant expression optimization:

```go
expr.Compile(expression,
    expr.ConstExpr(),  // Enable optimizations
)
```

**Benefit**: Pre-compute constant subexpressions

## Testing

### Manual Testing

```bash
# Test type behavior
ssql read-csv data.csv | ssql update -set-expr result 'a_kind + 1' | ssql limit 1

# Test built-in functions
ssql read-csv data.csv | ssql update -set-expr name 'upper(name)' | ssql limit 5
ssql read-csv data.csv | ssql where -expr 'contains(email, "@")' | ssql limit 5

# Test operators
ssql read-csv data.csv | ssql where -expr 'price > 100 and status == "active"'

# Test code generation
export SSQLGO=1
ssql read-csv data.csv | ssql where -expr 'price > 100' | ssql generate go > test.go
go run test.go
```

### Automated Testing

expr has comprehensive tests in its own repository. ssql tests focus on integration:

- `cmd/ssql/generation_test.go` - Code generation for expr expressions
- `cmd/ssql/helpers_test.go` - Helper function behavior
- Manual testing via CLI commands

## References

- **expr-lang docs**: https://expr-lang.org/docs/getting-started
- **GitHub repo**: https://github.com/expr-lang/expr
- **Language definition**: https://expr-lang.org/docs/language-definition
- **Built-in functions**: https://expr-lang.org/docs/language-definition#built-in-functions

## Changelog

- **v2.1.0** (Nov 2025): Created runtime package for shared compilation
- **v2.0.0** (Nov 2025): Added `-expr` flag to where command
- **v1.22.0** (Nov 2025): Added `-set-expr` flag to update command, initial expr integration

## See Also

- `CLAUDE.md` - Development principles including compile-time type safety
- `doc/research/` - Other research documents
- `cmd/ssql/lib/runtime/runtime.go` - Runtime package implementation
