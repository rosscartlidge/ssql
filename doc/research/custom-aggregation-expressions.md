# Custom Aggregation Expressions for group-by

## Overview

This document describes the design for adding custom aggregation expressions to the `group-by` command, leveraging ssql's existing expr-lang integration.

### Motivation

Currently, `group-by` only supports built-in aggregations:

```bash
ssql group-by dept -count total
ssql group-by dept -sum salary total
ssql group-by dept -avg salary avg_salary
ssql group-by dept -min salary min_salary
ssql group-by dept -max salary max_salary
ssql group-by dept -collect name all_names
```

Users cannot:
- Combine fields in aggregations (e.g., `sum(salary * bonus_pct)`)
- Apply custom logic (e.g., `sum(salary) / count() where level = 'Senior'`)
- Use computed aggregations (e.g., `max(salary) - min(salary)`)

## API Design

### Two Expression Modes

#### 1. Batch Mode: `-expr <expression> <result-name>`

Collects all records per group into memory, then evaluates expression once.

```bash
ssql group-by dept -expr 'sum(salary)' total
ssql group-by dept -expr 'max(salary) - min(salary)' salary_range
ssql group-by dept -expr 'sum(map(_records, .salary * .bonus_pct))' weighted_total
```

**Expr Environment:**

| Variable | Type | Description |
|----------|------|-------------|
| `salary` | `[]float64` | Array of all salary values in group |
| `bonus` | `[]float64` | Array of all bonus values in group |
| `<field>` | `[]any` | Array of all values for any field |
| `_records` | `[]map` | Array of full record maps |
| `_count` | `int` | Number of records in group |

**Simple vs Complex:**

```bash
# Simple: field arrays (covers most cases)
-expr 'sum(salary)' total
-expr 'avg(bonus)' avg_bonus
-expr 'max(age) - min(age)' age_range

# Complex: _records for multi-field expressions
-expr 'sum(map(_records, .salary * (1 + .bonus_pct)))' adjusted_total
-expr 'len(filter(_records, .status == "active"))' active_count
```

**Memory:** O(records per group) - all records held in memory.

#### 2. Streaming Mode: `-stream-expr <init> <every> <final> <result-name>`

Processes records one at a time, maintaining only accumulator state.

```bash
ssql group-by dept -stream-expr '{s: 0}' '{s: s + salary}' 's' total
ssql group-by dept -stream-expr '{s: 0, n: 0}' '{s: s + salary, n: n + 1}' 's / n' avg_salary
```

**Three Expressions:**

| Position | Purpose | Input | Output |
|----------|---------|-------|--------|
| `<init>` | Initialize state | (none) | State object |
| `<every>` | Update state per record | State + record fields | New state object |
| `<final>` | Compute result | Final state | Result value |

**Execution Model (functional, no mutation):**

```go
state := evalInit()                    // {s: 0, n: 0}
for _, record := range group {
    env := merge(state, record)        // {s: 0, n: 0, salary: 80000, dept: "Eng", ...}
    state = evalEvery(env)             // {s: 80000, n: 1}
}
result := evalFinal(state)             // 80000.0
```

**Memory:** O(1) per group - only accumulator state held.

### Flag Summary

```bash
# Built-in (existing)
-count <result>
-sum <field> <result>
-avg <field> <result>
-min <field> <result>
-max <field> <result>
-collect <field> <result>

# Custom batch (new)
-expr <expression> <result>

# Custom streaming (new)
-stream-expr <init> <every> <final> <result>
```

All flags can be combined and accumulated:

```bash
ssql group-by dept \
  -count num_employees \
  -expr 'sum(salary)' total_salary \
  -stream-expr '{s:0,n:0}' '{s:s+bonus,n:n+1}' 's/n' avg_bonus
```

## Implementation

### 1. Flag Registration (group_by.go)

```go
Flag("-expr", "-e").
    Arg("expression").Completer(cf.NoCompleter{Hint: "<expression>"}).Done().
    Arg("result-name").Completer(cf.NoCompleter{Hint: "<name>"}).Done().
    Accumulate().
    Global().
    Help("Custom aggregation expression: -expr <expression> <result-name>").
Done().

Flag("-stream-expr").
    Arg("init").Completer(cf.NoCompleter{Hint: "<init-expr>"}).Done().
    Arg("every").Completer(cf.NoCompleter{Hint: "<every-expr>"}).Done().
    Arg("final").Completer(cf.NoCompleter{Hint: "<final-expr>"}).Done().
    Arg("result-name").Completer(cf.NoCompleter{Hint: "<name>"}).Done().
    Accumulate().
    Global().
    Help("Streaming aggregation: -stream-expr <init> <every> <final> <result-name>").
Done().
```

### 2. Batch Mode Implementation

Uses AST patching to transform natural syntax. See `doc/research/expr-ast-patching.md` for full design and POC results.

```go
// AggPatcher transforms natural aggregation syntax to predicate form
// e.g., sum(salary * bonus) → sum(_records, .salary * .bonus)
type AggPatcher struct {
    Fields map[string]bool
}

// Build environment with field arrays and _records
func buildBatchEnv(records []ssql.Record) map[string]any {
    env := make(map[string]any)

    // Collect all field names
    fieldValues := make(map[string][]any)
    recordMaps := make([]map[string]any, 0, len(records))

    for _, r := range records {
        recMap := make(map[string]any)
        for k, v := range r.All() {
            fieldValues[k] = append(fieldValues[k], v)
            recMap[k] = v
        }
        recordMaps = append(recordMaps, recMap)
    }

    // Add field arrays to env
    for field, values := range fieldValues {
        env[field] = values
    }

    // Add _records and _count
    env["_records"] = recordMaps
    env["_count"] = len(records)

    // Dummy functions to satisfy type-checker before patching
    env["count"] = func() int { return 0 }
    env["avg"] = func(arr []float64) float64 { return 0 }

    return env
}

// Evaluate batch expression with AST patching
func evalBatchExpr(expression string, records []ssql.Record, fields map[string]bool) (any, error) {
    env := buildBatchEnv(records)
    patcher := &AggPatcher{Fields: fields}
    program, err := expr.Compile(expression,
        expr.Env(env),
        expr.Patch(patcher),
    )
    if err != nil {
        return nil, err
    }
    return expr.Run(program, env)
}
```

### 3. Streaming Mode Implementation

```go
// Streaming aggregation state
type streamAgg struct {
    initExpr  *vm.Program
    everyExpr *vm.Program
    finalExpr *vm.Program
    resultName string
}

// Process one group with streaming
func evalStreamExpr(agg streamAgg, records iter.Seq[ssql.Record]) (any, error) {
    // 1. Initialize state
    state, err := expr.Run(agg.initExpr, nil)
    if err != nil {
        return nil, fmt.Errorf("init: %w", err)
    }
    stateMap, ok := state.(map[string]any)
    if !ok {
        return nil, fmt.Errorf("init must return object, got %T", state)
    }

    // 2. Process each record
    for record := range records {
        // Merge state with record fields
        env := make(map[string]any)
        for k, v := range stateMap {
            env[k] = v
        }
        for k, v := range record.All() {
            env[k] = v
        }

        // Evaluate every expression
        newState, err := expr.Run(agg.everyExpr, env)
        if err != nil {
            return nil, fmt.Errorf("every: %w", err)
        }
        stateMap, ok = newState.(map[string]any)
        if !ok {
            return nil, fmt.Errorf("every must return object, got %T", newState)
        }
    }

    // 3. Compute final result
    return expr.Run(agg.finalExpr, stateMap)
}
```

### 4. Integration with Existing Aggregations

The handler needs to process all aggregation types together:

```go
// In handler, after collecting all aggregation specs:

// For each group:
for key, groupRecords := range groups {
    result := ssql.MakeMutableRecord()

    // Copy group key fields
    // ...

    // Built-in aggregations (existing code)
    for _, spec := range builtinAggSpecs {
        agg := buildAggregator(spec.function, spec.field)
        result = result.SetAny(spec.result, agg(groupRecords).getValue())
    }

    // Batch expression aggregations (new)
    for _, spec := range exprAggSpecs {
        value, err := evalBatchExpr(spec.expression, groupRecords)
        if err != nil {
            return fmt.Errorf("expr %q: %w", spec.expression, err)
        }
        result = result.SetAny(spec.result, value)
    }

    // Streaming expression aggregations (new)
    for _, spec := range streamAggSpecs {
        value, err := evalStreamExpr(spec, slices.Values(groupRecords))
        if err != nil {
            return fmt.Errorf("stream-expr: %w", err)
        }
        result = result.SetAny(spec.result, value)
    }

    // Output result
}
```

### 5. Code Generation Support

For `-generate` mode, both new flags need code generation:

**Batch mode generates:**

```go
// -expr 'sum(salary)' total
exprEnv := buildBatchEnv(groupRecords)
total, _ := expr.Eval("sum(salary)", exprEnv)
result.fields["total"] = total
```

**Streaming mode generates:**

```go
// -stream-expr '{s:0}' '{s:s+salary}' 's' total
state := map[string]any{"s": float64(0)}
for _, r := range groupRecords {
    env := merge(state, recordToMap(r))
    state = evalExpr("{s:s+salary}", env).(map[string]any)
}
total := evalExpr("s", state)
result.fields["total"] = total
```

## Examples

### Basic Usage

```bash
# Custom sum with transformation
ssql from sales.csv | ssql group-by region \
  -expr 'sum(map(_records, .price * .quantity))' revenue

# Percentage calculation
ssql from employees.csv | ssql group-by dept \
  -expr 'len(filter(_records, .status == "active")) / _count * 100' pct_active

# Complex multi-field aggregation
ssql from orders.csv | ssql group-by customer_id \
  -expr 'sum(map(_records, .amount * (1 - .discount_pct/100)))' net_revenue
```

### Streaming for Large Data

```bash
# Sum (streaming)
ssql from huge_sales.csv | ssql group-by region \
  -stream-expr '{total: 0}' '{total: total + amount}' 'total' sum_amount

# Average (streaming)
ssql from huge_employees.csv | ssql group-by dept \
  -stream-expr '{s: 0, n: 0}' '{s: s + salary, n: n + 1}' 's / n' avg_salary

# Running min/max (streaming)
ssql from huge_data.csv | ssql group-by category \
  -stream-expr \
    '{lo: nil, hi: nil}' \
    '{lo: lo == nil ? value : min(lo, value), hi: hi == nil ? value : max(hi, value)}' \
    'hi - lo' \
    value_range
```

### Mixed Aggregations

```bash
# Combine built-in and custom
ssql from sales.csv | ssql group-by product \
  -count num_sales \
  -sum amount total_amount \
  -expr 'max(amount) - min(amount)' amount_range \
  -stream-expr '{s:0,n:0}' '{s:s+margin,n:n+1}' 's/n' avg_margin
```

## Error Handling

| Error | Cause | Message |
|-------|-------|---------|
| Compile error | Invalid expression syntax | `compiling -expr: <details>` |
| Init type error | Init doesn't return object | `init must return object, got <type>` |
| Every type error | Every doesn't return object | `every must return object, got <type>` |
| Runtime error | Expression evaluation fails | `evaluating -expr: <details>` |
| Field not found | Referenced field missing | (expr-lang default error) |

## Performance Considerations

### When to Use Batch (`-expr`)

- Groups with < 100,000 records
- Need access to multiple fields per record in single expression
- Complex filtering/mapping across records
- Simpler expressions preferred

### When to Use Streaming (`-stream-expr`)

- Groups with > 100,000 records
- Memory-constrained environments
- Simple accumulator patterns (sum, count, min, max)
- Willing to write more verbose expressions

### Benchmarks (estimated)

| Records per group | Batch memory | Streaming memory |
|-------------------|--------------|------------------|
| 1,000 | ~100 KB | ~1 KB |
| 10,000 | ~1 MB | ~1 KB |
| 100,000 | ~10 MB | ~1 KB |
| 1,000,000 | ~100 MB | ~1 KB |

## Future Considerations

### Potential Enhancements

1. **Pre-defined streaming aggregators**: Common patterns as shortcuts
   ```bash
   -stream-sum salary total      # Equivalent to -stream-expr '{s:0}' '{s:s+salary}' 's' total
   -stream-avg salary avg_sal    # Equivalent to streaming average pattern
   ```

2. **Expression validation**: Compile-time checking that every-expr returns same shape as init-expr

3. **Hybrid mode**: Auto-switch between batch and streaming based on group size

4. **Parallel evaluation**: Process multiple groups concurrently

## Testing Plan

1. **Unit tests for batch mode:**
   - Simple field arrays: `sum(salary)`, `avg(bonus)`
   - Multi-field: `sum(map(_records, .a * .b))`
   - Filtering: `len(filter(_records, .x > 0))`
   - Edge cases: empty groups, null values

2. **Unit tests for streaming mode:**
   - Sum, average, count patterns
   - Min/max with nil initialization
   - State shape validation
   - Error handling

3. **Integration tests:**
   - Combined with built-in aggregations
   - Pipeline with from/group-by/to
   - Code generation mode

4. **Performance tests:**
   - Memory usage comparison batch vs streaming
   - Large group handling

## Implementation Order

1. Add flag definitions to group_by.go
2. Implement batch mode (`-expr`)
3. Add tests for batch mode
4. Implement streaming mode (`-stream-expr`)
5. Add tests for streaming mode
6. Update code generation for both modes
7. Update documentation (README, cli docs)
8. Update help examples
