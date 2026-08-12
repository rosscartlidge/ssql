# AST Patching for Natural Aggregation Syntax

Reference: DFC036
Created: 2026-01-01
Last modified: 2026-01-01

[Back to Index](./README.md)

## Overview

Use expr-lang's AST visitor and patch API to transform natural SQL-like aggregation expressions into the predicate form that expr-lang requires.

**Goal:**
```bash
# User writes:
ssql group-by dept -expr 'sum(salary * bonus_pct)' total

# Internally transformed to:
sum(_records, .salary * .bonus_pct)
```

## expr-lang AST API

**Key functions:**
- `ast.Walk(node *Node, visitor)` - traverse tree
- `ast.Patch(node *Node, newNode Node)` - replace node in place
- `ast.Find(node, predicate)` - find matching node

**Relevant node types:**
- `CallNode` - function calls: `{Callee: Node, Arguments: []Node}`
- `BuiltinNode` - built-in functions: `{Name: string, Arguments: []Node}`
- `IdentifierNode` - variable names: `{Value: string}`
- `BinaryNode` - operators: `{Operator: string, Left: Node, Right: Node}`
- `MemberNode` - field access: `{Node: Node, Property: Node}`

## Transformation Rules

### Aggregation Functions to Transform

| Function | Predicate Form |
|----------|----------------|
| `sum(expr)` | `sum(_records, .expr)` |
| `avg(expr)` | `mean(_records, .expr)` |
| `mean(expr)` | `mean(_records, .expr)` |
| `median(expr)` | `median(_records, .expr)` (needs custom) |
| `count()` | `len(_records)` |
| `count(pred)` | `count(_records, .pred)` |
| `min(expr)` | `min(_records, .expr)` (needs custom) |
| `max(expr)` | `max(_records, .expr)` (needs custom) |

**Note:** expr-lang's `min`/`max` take two args, not arrays. We'll need custom functions or use `reduce`.

### Identifier Transformation

Within aggregation arguments, bare identifiers become member access:

```
salary           → .salary
salary * bonus   → .salary * .bonus
price > 100      → .price > 100
```

## Implementation

### Step 1: Aggregation Patcher

```go
package agg

import (
    "github.com/expr-lang/expr/ast"
    "github.com/expr-lang/expr/parser"
)

// Aggregation functions that need transformation
var aggFunctions = map[string]string{
    "sum":    "sum",
    "avg":    "mean",
    "mean":   "mean",
    "count":  "count",
    // min/max need special handling
}

type AggPatcher struct {
    // Set of field names available (from schema or first record)
    Fields map[string]bool
}

func (p *AggPatcher) Visit(node *ast.Node) {
    switch n := (*node).(type) {
    case *ast.CallNode:
        p.patchCall(node, n)
    case *ast.BuiltinNode:
        p.patchBuiltin(node, n)
    }
}

func (p *AggPatcher) patchCall(node *ast.Node, call *ast.CallNode) {
    // Get function name
    ident, ok := call.Callee.(*ast.IdentifierNode)
    if !ok {
        return
    }

    exprName, isAgg := aggFunctions[ident.Value]
    if !isAgg {
        return
    }

    // Transform arguments
    if len(call.Arguments) == 0 {
        // count() → len(_records)
        if ident.Value == "count" {
            ast.Patch(node, &ast.CallNode{
                Callee: &ast.IdentifierNode{Value: "len"},
                Arguments: []ast.Node{
                    &ast.IdentifierNode{Value: "_records"},
                },
            })
        }
        return
    }

    // sum(expr) → sum(_records, predicate)
    arg := call.Arguments[0]
    predicate := p.transformToPredicate(arg)

    ast.Patch(node, &ast.CallNode{
        Callee: &ast.IdentifierNode{Value: exprName},
        Arguments: []ast.Node{
            &ast.IdentifierNode{Value: "_records"},
            predicate,
        },
    })
}

func (p *AggPatcher) patchBuiltin(node *ast.Node, builtin *ast.BuiltinNode) {
    exprName, isAgg := aggFunctions[builtin.Name]
    if !isAgg {
        return
    }

    if len(builtin.Arguments) == 0 {
        if builtin.Name == "count" {
            ast.Patch(node, &ast.CallNode{
                Callee: &ast.IdentifierNode{Value: "len"},
                Arguments: []ast.Node{
                    &ast.IdentifierNode{Value: "_records"},
                },
            })
        }
        return
    }

    arg := builtin.Arguments[0]
    predicate := p.transformToPredicate(arg)

    ast.Patch(node, &ast.BuiltinNode{
        Name: exprName,
        Arguments: []ast.Node{
            &ast.IdentifierNode{Value: "_records"},
            predicate,
        },
    })
}
```

### Step 2: Identifier to Member Transformer

```go
// transformToPredicate converts field references to member access
// salary → .salary
// salary * bonus → .salary * .bonus
func (p *AggPatcher) transformToPredicate(node ast.Node) ast.Node {
    // Clone the node to avoid modifying original
    predNode := cloneNode(node)

    // Walk and transform identifiers
    transformer := &IdentifierTransformer{Fields: p.Fields}
    ast.Walk(&predNode, transformer)

    // Wrap in PredicateNode for proper scoping
    return &ast.PredicateNode{Node: predNode}
}

type IdentifierTransformer struct {
    Fields map[string]bool
}

func (t *IdentifierTransformer) Visit(node *ast.Node) {
    ident, ok := (*node).(*ast.IdentifierNode)
    if !ok {
        return
    }

    // Check if this identifier is a known field
    if !t.Fields[ident.Value] {
        return // Not a field, leave as-is (could be a function or constant)
    }

    // Transform: salary → .salary (MemberNode with PointerNode base)
    // In predicate context, # refers to current element
    // .salary is shorthand for #.salary
    ast.Patch(node, &ast.MemberNode{
        Node:     &ast.PointerNode{Name: "#"},
        Property: &ast.StringNode{Value: ident.Value},
    })
}
```

### Step 3: Integration with expr.Compile

```go
func compileAggExpression(exprStr string, fields []string) (*vm.Program, error) {
    // Build field set
    fieldSet := make(map[string]bool)
    for _, f := range fields {
        fieldSet[f] = true
    }

    // Parse to get AST
    tree, err := parser.Parse(exprStr)
    if err != nil {
        return nil, err
    }

    // Apply aggregation patcher
    patcher := &AggPatcher{Fields: fieldSet}
    ast.Walk(&tree.Node, patcher)

    // Compile with patched AST
    // Note: expr.Compile can take expr.Patch() option, or we can compile the tree directly
    program, err := expr.Compile(exprStr, expr.Patch(patcher))

    return program, err
}
```

## Alternative: Use expr.Patch Option

expr-lang supports passing patchers directly to Compile:

```go
patcher := &AggPatcher{Fields: fieldSet}
program, err := expr.Compile(exprStr,
    expr.Env(env),
    expr.Patch(patcher),
)
```

This is cleaner as it handles parsing internally.

## Handling Complex Cases

### Nested Aggregations

```expr
sum(salary) / count()
```

Each aggregation is patched independently:
```expr
sum(_records, .salary) / len(_records)
```

### Conditional Aggregations

```expr
sum(status == "active" ? salary : 0)
```

Becomes:
```expr
sum(_records, .status == "active" ? .salary : 0)
```

### Non-Field Identifiers

We need to distinguish fields from:
- Built-in functions: `len`, `abs`, `lower`
- Constants: `true`, `false`, `nil`
- Already-defined variables

The `Fields` map ensures we only transform known field names.

## Custom Aggregation Functions

For `min`, `max`, and `median` on arrays, we need custom functions since expr-lang's built-ins don't support the predicate form:

```go
env["min"] = func(records []map[string]any, predicate func(map[string]any) float64) float64 {
    if len(records) == 0 {
        return 0
    }
    result := predicate(records[0])
    for _, r := range records[1:] {
        if v := predicate(r); v < result {
            result = v
        }
    }
    return result
}

env["max"] = func(records []map[string]any, predicate func(map[string]any) float64) float64 {
    // Similar to min
}

env["median"] = func(records []map[string]any, predicate func(map[string]any) float64) float64 {
    // Collect values, sort, return middle
}
```

## Testing the Transformation

```go
func TestAggPatching(t *testing.T) {
    tests := []struct {
        input    string
        expected string
    }{
        {"sum(salary)", "sum(_records, .salary)"},
        {"sum(salary * bonus)", "sum(_records, .salary * .bonus)"},
        {"avg(price)", "mean(_records, .price)"},
        {"count()", "len(_records)"},
        {"sum(salary) / count()", "sum(_records, .salary) / len(_records)"},
        {"max(age) - min(age)", "max(_records, .age) - min(_records, .age)"},
    }

    for _, tt := range tests {
        tree, _ := parser.Parse(tt.input)
        patcher := &AggPatcher{Fields: map[string]bool{
            "salary": true, "bonus": true, "price": true, "age": true,
        }}
        ast.Walk(&tree.Node, patcher)

        result := tree.Node.String() // or custom formatter
        if result != tt.expected {
            t.Errorf("got %s, want %s", result, tt.expected)
        }
    }
}
```

## POC Results (Validated)

Proof of concept at `/tmp/agg_patch_poc/main.go` validates this approach.

**What works:**
- `sum(salary)` → `sum(_records, #.salary)` ✓
- `sum(salary * bonus)` → `sum(_records, #.salary * #.bonus)` ✓
- `count()` → `len(_records)` ✓
- `avg(salary)` → `sum(_records, #.salary) / len(_records)` ✓
- `avg(salary * bonus)` → `sum(_records, #.salary * #.bonus) / len(_records)` ✓
- Combined: `sum(salary) + avg(bonus)`, `sum(salary) / count() * 1.1` ✓

**Key Findings:**

1. **Predicate-aware builtins**: Only `sum` and `count` have `Predicate: true` in expr-lang
   - These accept `(array, predicate)` form
   - `mean`, `median`, `min`, `max` do NOT support predicates

2. **avg/mean workaround**: Transform `avg(expr)` to `sum(_records, .expr) / len(_records)`
   - Works for simple fields and complex expressions

3. **Dummy functions required**: Add placeholder functions to environment:
   ```go
   "count": func() int { return 0 },                   // for count() with 0 args
   "avg":   func(arr []float64) float64 { return 0 }, // for avg(field)
   ```
   This satisfies type-checker before patching runs

4. **AST Node construction**:
   - Field access: `MemberNode{Node: PointerNode{}, Property: StringNode{Value: field}}`
   - Predicate wrap: `PredicateNode{Node: transformedExpr}`
   - Use `BuiltinNode` (not `CallNode`) for patched aggregations

## Summary

1. **Parse** expression to AST
2. **Walk** tree, find aggregation function calls
3. **Patch** each aggregation:
   - Add `_records` as first argument
   - Transform field identifiers to member access (`.field`)
   - Wrap in predicate node
4. **Compile** patched expression with appropriate environment

This gives users natural SQL-like syntax while leveraging expr-lang's built-in array functions.

## Sources

- [expr-lang Visitor documentation](https://expr-lang.org/docs/visitor)
- [expr-lang AST package](https://pkg.go.dev/github.com/expr-lang/expr/ast)
- [expr-lang main package](https://pkg.go.dev/github.com/expr-lang/expr)
