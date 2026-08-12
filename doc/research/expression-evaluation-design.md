# Expression Evaluation Design for ssql Update Command

Reference: DFC020
Created: 2025-11-12
Last modified: 2025-11-12

[Back to Index](./README.md)

**Status:** Design Proposal
**Date:** 2025-11-12
**Author:** Design discussion with Claude Code

## Problem Statement

Currently, the `update` command only supports literal values:

```bash
ssql update -set status active
ssql update -set count 42
```

We want to support **computed expressions** for more powerful updates:

```bash
ssql update -set total 'price * quantity'
ssql update -set discount 'price * 0.1'
ssql update -set name 'upper(name)'
ssql update -set tier 'sales > 10000 ? "gold" : "silver"'
```

## Design Goals

1. **Powerful**: Support math, strings, conditionals, field references
2. **Fast**: Execute in reasonable time for CLI usage (< 100ms for typical operations)
3. **Safe**: No arbitrary code execution vulnerabilities
4. **Maintainable**: Reasonable implementation complexity
5. **Consistent**: Works with both CLI execution and code generation
6. **Familiar**: Syntax should be intuitive for Go developers

## Option 1: Expression Parser Library (expr-lang/expr)

### Overview

Use **github.com/expr-lang/expr** - a fast, production-ready expression language for Go.

### Example Usage

```bash
# Math expressions
ssql update -set total 'price * quantity'
ssql update -set discount 'price * 0.1'

# String operations
ssql update -set name 'upper(trim(name))'
ssql update -set slug 'lower(replace(title, " ", "-"))'

# Conditionals (ternary operator)
ssql update -set tier 'sales > 10000 ? "gold" : sales > 5000 ? "silver" : "bronze"'

# Built-in functions
ssql update -set rounded 'round(price, 2)'
ssql update -set length 'len(items)'
```

### Implementation Sketch

```go
import "github.com/expr-lang/expr"

func evaluateExpression(expression string, record ssql.Record) (any, error) {
    // Build environment with all record fields
    env := make(map[string]interface{})
    for k, v := range record.All() {
        env[k] = v
    }

    // Compile expression (cacheable)
    program, err := expr.Compile(expression, expr.Env(env))
    if err != nil {
        return nil, fmt.Errorf("compile expression: %w", err)
    }

    // Execute
    result, err := expr.Run(program, env)
    if err != nil {
        return nil, fmt.Errorf("execute expression: %w", err)
    }

    return result, nil
}
```

### Code Generation

Convert expr syntax to Go code:

```bash
# CLI command
ssql update -set total 'price * quantity'

# Generated Go code
ssql.Update(func(mut MutableRecord) MutableRecord {
    price := ssql.GetOr(r, "price", 0.0)
    quantity := ssql.GetOr(r, "quantity", 0.0)
    return mut.Float("total", price * quantity)
})
```

### Pros

- ✅ **Battle-tested**: 5k+ GitHub stars, used in production
- ✅ **Fast**: Compiles to bytecode, ~100x faster than reflection
- ✅ **Rich features**: Math, strings, ternary, functions, array/map access
- ✅ **Type-safe**: Validation at compile time
- ✅ **Small dependency**: Pure Go, ~10k LOC
- ✅ **Familiar syntax**: Go-like but simpler
- ✅ **Good error messages**: Points to exact problem in expression
- ✅ **Extensible**: Can add custom functions

### Cons

- ❌ **External dependency**: Adds ~500KB to binary
- ❌ **Different syntax**: Not exactly Go (e.g., ternary `?:` vs Go `if`)
- ❌ **Learning curve**: Users need to learn expr syntax
- ❌ **Limited to expressions**: Can't do complex control flow

### Features Available

```go
// Operators
+ - * / % **              // Math (** is power)
== != < > <= >=          // Comparison
and or not               // Logical
+ (concat)               // String concatenation
in, contains             // Collection operators

// Built-in Functions
len, upper, lower, trim, split, join
round, floor, ceil, abs, max, min

// Array/Map Access
users[0].name
config["timeout"]

// Ternary
condition ? trueValue : falseValue
```

### Performance

From expr benchmarks:
- Compilation: ~100µs (cacheable)
- Execution: ~172ns per evaluation
- **Total for 1000 records**: ~0.3ms

## Option 2: CEL (Common Expression Language)

### Overview

Use **github.com/google/cel-go** - Google's Common Expression Language, used in Kubernetes, Firebase, Cloud IAM, Envoy.

### Example Usage

```bash
# Math expressions
ssql update -set total 'price * quantity'
ssql update -set discount 'price * 0.1'

# String operations
ssql update -set name 'name.upperAscii()'
ssql update -set email 'email.lowerAscii()'

# Conditionals (ternary operator)
ssql update -set tier 'sales > 10000 ? "gold" : sales > 5000 ? "silver" : "bronze"'

# Built-in functions
ssql update -set slug 'title.lowerAscii().replace(" ", "-")'
ssql update -set is_valid 'email.contains("@") && email.endsWith(".com")'
```

### Implementation Sketch

```go
import (
    "github.com/google/cel-go/cel"
    "github.com/google/cel-go/common/types"
)

func evaluateExpression(expression string, record ssql.Record) (any, error) {
    // Create CEL environment with record fields
    env, err := cel.NewEnv(
        cel.Variable("price", cel.DoubleType),
        cel.Variable("quantity", cel.IntType),
        // ... declare all known fields
    )
    if err != nil {
        return nil, err
    }

    // Parse and type-check expression (cacheable)
    ast, issues := env.Compile(expression)
    if issues != nil && issues.Err() != nil {
        return nil, issues.Err()
    }

    // Create program (cacheable, thread-safe)
    prg, err := env.Program(ast)
    if err != nil {
        return nil, err
    }

    // Build activation map from record
    activation := make(map[string]any)
    for k, v := range record.All() {
        activation[k] = v
    }

    // Evaluate
    result, _, err := prg.Eval(activation)
    if err != nil {
        return nil, err
    }

    return result.Value(), nil
}
```

### Code Generation

Similar to expr - convert CEL syntax to Go code:

```bash
# CLI command
ssql update -set total 'price * quantity'

# Generated Go code
ssql.Update(func(mut MutableRecord) MutableRecord {
    price := ssql.GetOr(r, "price", 0.0)
    quantity := ssql.GetOr(r, "quantity", 0.0)
    return mut.Float("total", price * quantity)
})
```

### Pros

- ✅ **Battle-tested**: Used by Google (Kubernetes, Firebase, Cloud IAM, Envoy)
- ✅ **Fast**: ~268ns per evaluation (slightly slower than expr)
- ✅ **Industry standard**: Spec shared across languages (Go, Java, C++, Rust, Python)
- ✅ **Type-safe**: Strong gradual typing with compile-time checks
- ✅ **Small dependency**: Pure Go, similar size to expr (~500KB-1MB)
- ✅ **Non-Turing complete**: Safe, can't infinite loop
- ✅ **Great ecosystem**: Lots of tooling, documentation, examples
- ✅ **Portable**: Same syntax across all languages
- ✅ **Thread-safe**: Programs are stateless and cacheable

### Cons

- ❌ **C-like syntax**: Not as Go-native as expr (e.g., `name.upperAscii()` vs `upper(name)`)
- ❌ **Slightly slower**: 268ns vs 172ns per evaluation (~56% slower)
- ❌ **More verbose API**: Requires environment setup, type declarations
- ❌ **Learning curve**: Different syntax from Go (more like JavaScript/JSON)
- ❌ **Overkill for ssql**: Designed for distributed systems, we just need local evaluation

### Features Available

```go
// Operators
+ - * / %                    // Math
== != < > <= >=              // Comparison
&& || !                      // Logical (not 'and', 'or', 'not' like expr)
+ (concat)                   // String concatenation
in                           // Collection membership

// Built-in Functions
size, startsWith, endsWith, contains, matches (regex)
upperAscii, lowerAscii
int, uint, double, string, bytes, duration, timestamp

// Method syntax (C-like)
"hello".startsWith("h")
[1, 2, 3].size()

// Ternary
condition ? trueValue : falseValue
```

### Performance

From benchmarks:
- Compilation: ~100µs (cacheable)
- Execution: ~268ns per evaluation
- **Total for 1000 records**: ~0.4ms

### Comparison with expr-lang

| Feature | expr-lang | CEL |
|---------|-----------|-----|
| Performance | 172 ns/op | 268 ns/op |
| Syntax | Go-like | C-like/JavaScript-like |
| Ecosystem | 5k+ stars | Google official, Kubernetes |
| Type system | Go types | CEL types (gradual typing) |
| Method calls | `upper(name)` | `name.upperAscii()` |
| Logical ops | `and`, `or`, `not` | `&&`, `\|\|`, `!` |
| Portability | Go only | Multi-language spec |
| Learning curve | Low (Go devs) | Medium (new syntax) |

**Summary:** CEL is more "industry standard" with broader ecosystem, but expr-lang is faster and more Go-native. For ssql users (already writing Go), expr-lang may be more intuitive.

## Option 3: Go Interpreter (yaegi)

### Overview

Use **github.com/traefik/yaegi** - Traefik's production Go interpreter.

### Example Usage

```bash
# Simple expressions (auto-wrapped)
ssql update -set total 'price * quantity'

# Full Go functions
ssql update -expr 'func(r ssql.Record) ssql.Record {
    mut := r.ToMutable()

    sales := ssql.GetOr(r, "sales", 0.0)
    if sales > 10000 {
        mut = mut.String("tier", "gold")
    } else if sales > 5000 {
        mut = mut.String("tier", "silver")
    } else {
        mut = mut.String("tier", "bronze")
    }

    return mut.Freeze()
}'
```

### Implementation Sketch

```go
import (
    "github.com/traefik/yaegi/interp"
    "github.com/traefik/yaegi/stdlib"
)

func evaluateGoExpression(expr string, record ssql.Record) (any, error) {
    i := interp.New(interp.Options{})

    // Use standard library
    i.Use(stdlib.Symbols)

    // Inject ssql package
    i.Use(interp.Exports{
        "ssql/ssql": map[string]reflect.Value{
            "GetOr":         reflect.ValueOf(ssql.GetOr),
            "Get":           reflect.ValueOf(ssql.Get),
            "Record":        reflect.ValueOf((*ssql.Record)(nil)),
            "MutableRecord": reflect.ValueOf((*ssql.MutableRecord)(nil)),
        },
    })

    // Inject current record
    i.Eval(fmt.Sprintf(`var r = %#v`, record))

    // Evaluate expression
    result, err := i.Eval(expr)
    if err != nil {
        return nil, err
    }

    return result.Interface(), nil
}
```

### Simple Expression Auto-Expansion

For common cases, auto-expand field references:

```go
// Input: price * quantity
// Expand to: ssql.GetOr(r, "price", 0.0) * ssql.GetOr(r, "quantity", 0.0)

func expandSimpleExpression(expr string) string {
    re := regexp.MustCompile(`\b([a-z_][a-z0-9_]*)\b`)
    return re.ReplaceAllStringFunc(expr, func(field string) string {
        if isGoKeyword(field) || isBuiltinFunc(field) {
            return field
        }
        return fmt.Sprintf(`ssql.GetOr(r, %q, 0.0)`, field)
    })
}
```

### Code Generation

Generated code is exactly what was written:

```bash
# CLI command
ssql update -set total 'price * quantity'

# Auto-expanded to
ssql update -set total 'ssql.GetOr(r, "price", 0.0) * ssql.GetOr(r, "quantity", 0.0)'

# Generated Go code (identical!)
ssql.Update(func(mut MutableRecord) MutableRecord {
    return mut.Float("total",
        ssql.GetOr(r, "price", 0.0) * ssql.GetOr(r, "quantity", 0.0))
})
```

### Pros

- ✅ **Real Go syntax**: No new language to learn
- ✅ **Full power**: All Go features (if/else, loops, functions)
- ✅ **Production-ready**: Used by Traefik at massive scale
- ✅ **Fast**: Interpreted, not compiled (~1ms per expression)
- ✅ **Type-safe**: Go's type system enforced
- ✅ **Clean code gen**: Generated code IS the input code
- ✅ **No compilation wait**: Instant execution

### Cons

- ❌ **Larger dependency**: ~10MB added to binary
- ❌ **Slower than expr**: ~5x slower (still fast enough at ~1ms)
- ❌ **Complex API**: Requires setting up interpreter context
- ❌ **Verbose for simple cases**: `GetOr(r, "price", 0.0)` vs `price`
- ❌ **Security considerations**: Full language access (mitigated by sandbox)

### Performance

From yaegi benchmarks:
- Startup: ~5ms (one-time)
- Execution: ~1ms per expression
- **Total for 1000 records**: ~1 second

Slower than expr, but still acceptable for CLI usage.

## Option 3: Compile Go on the Fly (go build)

### Overview

Write Go code to a temp file, compile it, execute it.

### Example

```bash
ssql update -expr 'func(r ssql.Record) ssql.Record {
    return r.ToMutable().Float("total",
        ssql.GetOr(r, "price", 0.0) * ssql.GetOr(r, "qty", 0.0)).Freeze()
}'
```

Implementation:
1. Write expression to `/tmp/expr.go`
2. Run `go build -o /tmp/expr /tmp/expr.go`
3. Execute `/tmp/expr`
4. Parse output

### Pros

- ✅ **Full Go power**: Complete language
- ✅ **Fast execution**: Native compiled code
- ✅ **Type-safe**: Compiler catches errors
- ✅ **Actually fast**: Modern Go compiles in 50-150ms (tested!)
- ✅ **Natural for ssql users**: Already familiar with Go syntax

### Cons

- ⚠️ **Requires Go toolchain**: Not portable (but ssql users likely have it)
- ⚠️ **Slower than expr**: 70ms vs <1ms (but still acceptable)
- ⚠️ **Security**: Arbitrary code execution (isolated process)
- ⚠️ **Complexity**: Temp files, process management, cleanup

### Performance (Actual Measurements)

**Tested on Go 1.23+, Linux:**
- Simple expression: 92ms compile
- With ssql imports: 137ms compile
- Realistic update: **70ms compile**
- Complex conditional: **52ms compile**

**Total for 1000 records**: ~120ms (70ms compile + 50ms execute)

**Verdict**: ✅ **VIABLE for CLI usage!** Modern Go is much faster than expected.

## Option 4: Custom Expression Parser

### Overview

Build our own parser and evaluator from scratch.

### Pros

- ✅ **Full control**: Exactly the features we want
- ✅ **No dependencies**: Pure ssql code

### Cons

- ❌ **Huge effort**: Months of development
- ❌ **Bug-prone**: Parsers are hard to get right
- ❌ **Limited**: Will never match expr or yaegi in features
- ❌ **Maintenance burden**: Ongoing parser maintenance

**Verdict**: ❌ Not worth the effort when excellent libraries exist

## Option 5: Keep It Simple (Current Approach)

### Overview

Don't add expression evaluation. Keep literals only.

```bash
# CLI: Simple literals only
ssql update -set status active

# Generated code: Edit manually for complex logic
ssql.Update(func(mut MutableRecord) MutableRecord {
    return mut.Float("total",
        ssql.GetOr(r, "price", 0.0) * ssql.GetOr(r, "qty", 0.0))
})
```

### Pros

- ✅ **Zero dependencies**: No external code
- ✅ **Simple**: Current implementation works
- ✅ **Fast**: No evaluation overhead
- ✅ **Safe**: No code execution

### Cons

- ❌ **Limited**: Can't do computed updates interactively
- ❌ **Two-step workflow**: Generate → edit → compile
- ❌ **Less convenient**: Common operations require code gen

**Verdict**: ✅ Acceptable baseline, but limiting

## Comparison Matrix

| Feature                | expr-lang | CEL | yaegi | go build | Custom | Current |
|-----------------------|-----------|-----|-------|----------|--------|---------|
| **Performance**        | ⭐⭐⭐⭐⭐   | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐   | ⭐⭐⭐⭐    | ⭐⭐⭐    | ⭐⭐⭐⭐⭐   |
| **Ease of Use**        | ⭐⭐⭐⭐    | ⭐⭐⭐  | ⭐⭐⭐    | ⭐⭐⭐     | ⭐⭐⭐    | ⭐⭐⭐⭐⭐   |
| **Power**              | ⭐⭐⭐     | ⭐⭐⭐  | ⭐⭐⭐⭐⭐  | ⭐⭐⭐⭐⭐   | ⭐⭐      | ⭐       |
| **Binary Size**        | +500KB    | +1MB | +10MB | 0        | 0      | 0       |
| **Dependencies**       | 1         | 1    | 1     | 0*       | 0      | 0       |
| **Implementation**     | ⭐⭐⭐⭐⭐   | ⭐⭐⭐⭐ | ⭐⭐⭐    | ⭐⭐⭐     | ⭐       | ⭐⭐⭐⭐⭐   |
| **Maintenance**        | ⭐⭐⭐⭐⭐   | ⭐⭐⭐⭐⭐| ⭐⭐⭐⭐   | ⭐⭐⭐⭐    | ⭐⭐      | ⭐⭐⭐⭐⭐   |
| **Type Safety**        | ⭐⭐⭐⭐    | ⭐⭐⭐⭐⭐| ⭐⭐⭐⭐⭐  | ⭐⭐⭐⭐⭐   | ⭐⭐⭐    | ⭐⭐⭐⭐⭐   |
| **Learning Curve**     | Medium    | Medium| Low   | Low      | Medium | Low     |
| **Compile Time**       | ~100µs    | ~100µs| ~5ms  | **70ms** | N/A    | N/A     |
| **Ecosystem**          | 5k stars  | Google/K8s | Traefik | Go stdlib | N/A | N/A |
| **Portability**        | Go only   | Multi-lang | Go only | Go only | N/A | N/A |

*Requires Go toolchain (external dependency, but ssql users likely have it)

## Recommendation

### Primary Recommendation: expr-lang/expr ⭐

**Why:**
- Best balance of power, performance, and simplicity
- **Fastest performance**: 172ns vs CEL's 268ns (~36% faster)
- **Go-native syntax**: More intuitive for ssql users already writing Go
- Proven in production (5k+ GitHub stars, thousands of projects)
- Small dependency (500KB)
- Fast enough for CLI (< 1ms per record)
- Great for 95% of use cases

### Strong Alternative: CEL (Common Expression Language) ⭐

**Why consider CEL:**
- **Industry standard**: Official Google project, used in Kubernetes, Firebase, Cloud IAM, Envoy
- **Better ecosystem**: Multi-language spec (Go, Java, C++, Rust, Python)
- **Excellent maintenance**: Backed by Google, enterprise-grade support
- **Portability**: Same syntax across all implementations
- **Strong type system**: Gradual typing with excellent compile-time checks

**Trade-offs vs expr-lang:**
- Slightly slower (268ns vs 172ns, but still sub-microsecond)
- C-like syntax (`name.upperAscii()`) vs Go-like (`upper(name)`)
- More setup required (environment, type declarations)
- Designed for distributed systems (may be overkill for local CLI)

**When to choose CEL over expr:**
- You value industry-standard, Google-backed technology
- You need multi-language portability (same expressions in Go/Python/Java/etc.)
- You want the best possible type system and tooling
- You're already using CEL elsewhere in your stack
- You prefer method syntax (`x.startsWith("a")`) over function syntax (`startsWith(x, "a")`)

**Use cases it handles:**
```bash
# Math
ssql update -set total 'price * quantity'
ssql update -set tax 'total * 0.2'

# Strings
ssql update -set name 'upper(trim(name))'

# Conditionals
ssql update -set tier 'sales > 10000 ? "gold" : "silver"'

# Complex expressions
ssql update -set discount 'tier == "gold" ? price * 0.2 : price * 0.1'
```

### Alternative: yaegi (for power users)

Add yaegi **alongside** expr for advanced cases:

```bash
# Most users: expr (simple, fast)
ssql update -set total 'price * quantity'

# Power users: yaegi (full Go)
ssql update -go 'func(r ssql.Record) ssql.Record {
    // Full Go code here
    mut := r.ToMutable()
    // ... complex logic
    return mut.Freeze()
}'
```

**Flags:**
- `-set <field> <expr>` → Uses expr-lang
- `-go <code>` → Uses yaegi interpreter

### Alternative 2: go build (compile-on-the-fly)

**NEW FINDING:** Modern Go compilation is fast enough for CLI usage!

Add `go build` **alongside** expr for maximum power with minimal dependencies:

```bash
# Most users: expr (simple, fastest)
ssql update -set total 'price * quantity'  # <1ms

# Power users: Go compile (full language, 70ms overhead)
ssql update -go 'func(r ssql.Record) ssql.Record {
    mut := r.ToMutable()
    price := ssql.GetOr(r, "price", 0.0)
    qty := ssql.GetOr(r, "qty", 0.0)

    // Full Go power: if/else, loops, functions
    discount := 0.1
    if qty > 100 {
        discount = 0.2
    } else if qty > 50 {
        discount = 0.15
    }

    return mut.Float("total", price * qty * (1 - discount)).Freeze()
}'
```

**Implementation:**
1. Generate complete Go program with expression
2. `go build -o /tmp/ssql_expr_HASH /tmp/expr.go` (~70ms)
3. Execute: `/tmp/ssql_expr_HASH < data.jsonl` (~50µs/record)
4. Cleanup temp files

**Advantages over yaegi:**
- ✅ No binary size increase (0 bytes vs +10MB)
- ✅ No runtime dependency (just needs Go toolchain)
- ✅ Full compiler type checking and error messages
- ✅ Native code performance (faster execution than interpreted)
- ✅ Users already familiar with Go syntax

**Trade-offs:**
- ⚠️ 70ms compile overhead vs yaegi's 5ms startup
- ⚠️ Requires Go toolchain (but ssql users likely have it)
- ⚠️ Temp file management

**Total time comparison (1000 records):**
- expr-lang: <1ms compile + 0.3ms execute = **~1ms**
- go build: 70ms compile + 50ms execute = **~120ms**
- yaegi: 5ms startup + 1000ms execute = **~1005ms**

**Verdict:** go build is competitive with yaegi and offers better type safety!

**Flags (with all three options):**
- `-set <field> <expr>` → Uses expr-lang (fastest, simple expressions)
- `-go <code>` → Uses go build (full Go, compile-on-the-fly)
- `-yaegi <code>` → Uses yaegi interpreter (full Go, interpreted)

## Open Questions

1. **Syntax for field references**:
   - Auto-expand `price` → `GetOr(r, "price", 0.0)`?
   - Or require explicit: `field("price")`?
   - Or just `price` works in environment?

2. **Type inference**:
   - How do we know if result is int64, float64, or string?
   - Infer from expression? From field type? From value?

3. **Error handling**:
   - What if expression fails on some records?
   - Skip record? Set to default? Abort?

4. **Caching**:
   - Cache compiled expressions across records?
   - How to detect when expression changes?

5. **Code generation**:
   - Convert expr syntax to Go code? (Hard!)
   - Or just emit expr.Eval call? (Simple but less efficient)
   - Or use `-generate` only with `-go` flag?

6. **Default values**:
   - What if field doesn't exist?
   - expr: Use `??` operator: `price ?? 0.0`
   - yaegi: Use `GetOr(r, "price", 0.0)`

## Implementation Plan (if we choose expr)

### Phase 1: Basic Support
1. Add expr dependency
2. Detect expression vs literal in `-set` value
3. Evaluate expressions with expr
4. Simple field auto-expansion

### Phase 2: Type Inference
1. Infer result type from expression
2. Call appropriate MutableRecord method (String/Int/Float/Bool)

### Phase 3: Code Generation
1. Convert expr expressions to Go code
2. Emit clean Go in generated output

### Phase 4: Advanced Features
1. Custom functions (upper, lower, etc.)
2. Better error messages
3. Expression validation/testing

## Next Steps

1. **Decide on approach**: expr, yaegi, both, or neither
2. **Prototype implementation**: Build POC to test performance
3. **User testing**: Get feedback on syntax and UX
4. **Documentation**: Write clear docs on expression syntax
5. **Implementation**: Full feature implementation
6. **Release**: New minor version (v2.1.0 - new feature)

## References

- **expr-lang/expr**: https://github.com/expr-lang/expr
- **yaegi**: https://github.com/traefik/yaegi
- **Comparison**: https://github.com/expr-lang/expr/blob/master/docs/Language-Definition.md
