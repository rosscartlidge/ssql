# Autocli Improvements for Multi-Argument Flags

Reference: DFC018
Created: 2025-11-09
Last modified: 2025-11-09

[Back to Index](./README.md)

## Background

While implementing ssql's new `group-by` syntax, we discovered some inconsistencies in how autocli handles multi-argument flags. This document proposes improvements to make the API more consistent and easier to use.

## Current Behavior

When using `.Accumulate()` with `.Arg()` calls, autocli returns different types depending on the number of arguments:

```go
// Single argument flag
Flag("-count").
    Arg("result-name").Done().
    Accumulate().
Done().

// Usage: -count total -count num
// Returns: []any{"total", "num"}
// Each element is a STRING

// Two argument flag  
Flag("-sum").
    Arg("field").Done().
    Arg("result-name").Done().
    Accumulate().
Done().

// Usage: -sum salary total -sum hours worked
// Returns: []any{
//     map[string]any{"field": "salary", "result-name": "total"},
//     map[string]any{"field": "hours", "result-name": "worked"},
// }
// Each element is a MAP
```

## Problem

This inconsistency makes parsing awkward and error-prone:

```go
// Current code required to handle both cases
if countVals, ok := ctx.GlobalFlags["-count"]; ok {
    counts, _ := countVals.([]any)
    for _, countVal := range counts {
        // Single arg returns string directly
        if resultName, ok := countVal.(string); ok {
            // Handle string case
        }
    }
}

if sumVals, ok := ctx.GlobalFlags["-sum"]; ok {
    sums, _ := sumVals.([]any)
    for _, sumVal := range sums {
        // Multi-arg returns map
        if argsMap, ok := sumVal.(map[string]any); ok {
            field, _ := argsMap["field"].(string)
            result, _ := argsMap["result-name"].(string)
            // Handle map case
        }
    }
}
```

Users must remember:
- 1 arg → string in slice
- 2+ args → map in slice

This leads to bugs and confusion.

## Proposed Solutions

### Solution 1: Always Return Maps (Recommended)

**Proposal:** Always return `map[string]any` regardless of argument count.

```go
Flag("-count").
    Arg("result-name").Done().
    Accumulate().
Done().

// Usage: -count total -count num
// Returns: []any{
//     map[string]any{"result-name": "total"},
//     map[string]any{"result-name": "num"},
// }
```

**Benefits:**
- Consistent behavior
- One code path to handle all cases
- Easier to document and learn

**User code becomes:**
```go
if countVals, ok := ctx.GlobalFlags["-count"]; ok {
    counts, _ := countVals.([]any)
    for _, countVal := range counts {
        if argsMap, ok := countVal.(map[string]any); ok {
            result, _ := argsMap["result-name"].(string)
            // Always use map - consistent!
        }
    }
}
```

### Solution 2: Add Helper Methods

**Proposal:** Add type-safe helper methods for common patterns.

```go
// For accumulated flags with named arguments
func (ctx *Context) GetAccumulatedMaps(flag string, argNames ...string) []map[string]string

// For accumulated flags with single string argument
func (ctx *Context) GetAccumulatedStrings(flag string, argName string) []string

// For accumulated flags with positional arguments (no names)
func (ctx *Context) GetAccumulatedArgs(flag string) [][]string
```

**Example usage:**
```go
// Single argument flags
results := ctx.GetAccumulatedStrings("-count", "result-name")
// Returns: []string{"total", "num"}

// Multi-argument flags
sums := ctx.GetAccumulatedMaps("-sum", "field", "result-name")
// Returns: []map[string]string{
//     {"field": "salary", "result-name": "total"},
//     {"field": "hours", "result-name": "worked"},
// }

// Positional arguments (less common)
renames := ctx.GetAccumulatedArgs("-as")
// Returns: [][]string{
//     {"old_name", "new_name"},
//     {"field1", "alias1"},
// }
```

**Benefits:**
- Hides type assertion complexity
- Type-safe returns
- Clear intent in code
- Works with current implementation

**User code becomes:**
```go
for _, result := range ctx.GetAccumulatedStrings("-count", "result-name") {
    // Use result directly - it's already a string
}

for _, sumSpec := range ctx.GetAccumulatedMaps("-sum", "field", "result-name") {
    field := sumSpec["field"]
    result := sumSpec["result-name"]
    // Use field and result directly - already strings
}
```

### Solution 3: Struct Binding

**Proposal:** Allow binding accumulated flags directly to structs.

```go
type AggSpec struct {
    Field  string `arg:"field"`
    Result string `arg:"result-name"`
}

type CountSpec struct {
    Result string `arg:"result-name"`
}

// In handler
var counts []CountSpec
ctx.BindAccumulated("-count", &counts)

var sums []AggSpec
ctx.BindAccumulated("-sum", &sums)
```

**Benefits:**
- Type safety at compile time
- No manual parsing
- Clear struct definition documents expected args
- Familiar pattern (similar to JSON/form binding)

**Full example:**
```go
Flag("-sum").
    Arg("field").Completer(cf.NoCompleter{Hint: "<field>"}).Done().
    Arg("result-name").Completer(cf.NoCompleter{Hint: "<name>"}).Done().
    Accumulate().
    Global().
Done().

Handler(func(ctx *cf.Context) error {
    // Define the structure
    type SumSpec struct {
        Field  string `arg:"field"`
        Result string `arg:"result-name"`
    }
    
    // Bind directly
    var sums []SumSpec
    if err := ctx.BindAccumulated("-sum", &sums); err != nil {
        return err
    }
    
    // Use with type safety
    for _, s := range sums {
        fmt.Printf("Sum %s as %s\n", s.Field, s.Result)
    }
    
    return nil
})
```

## Real-World Example: group-by Command

Here's how the three solutions compare for our actual use case:

### Current Implementation (Awkward)

```go
// Parse -count flags (only result name)
if countVals, ok := ctx.GlobalFlags["-count"]; ok {
    counts, _ := countVals.([]any)
    for _, countVal := range counts {
        // Single arg = string
        if resultName, ok := countVal.(string); ok {
            aggregations = append(aggregations, 
                Aggregation{Function: "count", Result: resultName})
        }
    }
}

// Parse -sum flags (field and result name)
if sumVals, ok := ctx.GlobalFlags["-sum"]; ok {
    sums, _ := sumVals.([]any)
    for _, sumVal := range sums {
        // Multi-arg = map
        if argsMap, ok := sumVal.(map[string]any); ok {
            field, _ := argsMap["field"].(string)
            result, _ := argsMap["result-name"].(string)
            if field != "" && result != "" {
                aggregations = append(aggregations,
                    Aggregation{Function: "sum", Field: field, Result: result})
            }
        }
    }
}
```

### With Solution 1: Always Maps

```go
// Parse -count flags
for _, countVal := range ctx.GetAccumulatedValues("-count") {
    if argsMap, ok := countVal.(map[string]any); ok {
        result, _ := argsMap["result-name"].(string)
        aggregations = append(aggregations,
            Aggregation{Function: "count", Result: result})
    }
}

// Parse -sum flags - same pattern!
for _, sumVal := range ctx.GetAccumulatedValues("-sum") {
    if argsMap, ok := sumVal.(map[string]any); ok {
        field, _ := argsMap["field"].(string)
        result, _ := argsMap["result-name"].(string)
        aggregations = append(aggregations,
            Aggregation{Function: "sum", Field: field, Result: result})
    }
}
```

### With Solution 2: Helper Methods

```go
// Parse -count flags
for _, result := range ctx.GetAccumulatedStrings("-count", "result-name") {
    aggregations = append(aggregations,
        Aggregation{Function: "count", Result: result})
}

// Parse -sum flags
for _, spec := range ctx.GetAccumulatedMaps("-sum", "field", "result-name") {
    aggregations = append(aggregations,
        Aggregation{Function: "sum", Field: spec["field"], Result: spec["result-name"]})
}
```

### With Solution 3: Struct Binding

```go
type CountAgg struct {
    Result string `arg:"result-name"`
}

type FieldAgg struct {
    Field  string `arg:"field"`
    Result string `arg:"result-name"`
}

// Parse -count flags
var counts []CountAgg
ctx.BindAccumulated("-count", &counts)
for _, c := range counts {
    aggregations = append(aggregations,
        Aggregation{Function: "count", Result: c.Result})
}

// Parse -sum flags
var sums []FieldAgg
ctx.BindAccumulated("-sum", &sums)
for _, s := range sums {
    aggregations = append(aggregations,
        Aggregation{Function: "sum", Field: s.Field, Result: s.Result})
}
```

## Recommendation

**Implement all three solutions:**

1. **Solution 1** (Always Maps) - Breaking change, but makes API consistent
   - Requires major version bump
   - Migration path: Check if value is string (old) or map (new)

2. **Solution 2** (Helper Methods) - Can be added to current version
   - Backwards compatible
   - Immediate usability improvement
   - Good stepping stone if Solution 1 requires more discussion

3. **Solution 3** (Struct Binding) - Can be added to current version
   - Backwards compatible
   - Most ergonomic for complex cases
   - Requires reflection but worth it

## Migration Path

If implementing Solution 1 (breaking change):

```go
// Old code (autocli v3.0.x)
if countVals, ok := ctx.GlobalFlags["-count"]; ok {
    counts, _ := countVals.([]any)
    for _, countVal := range counts {
        if resultName, ok := countVal.(string); ok {
            // Single arg was string
        }
    }
}

// New code (autocli v4.0.0)
if countVals, ok := ctx.GlobalFlags["-count"]; ok {
    counts, _ := countVals.([]any)
    for _, countVal := range counts {
        if argsMap, ok := countVal.(map[string]any); ok {
            // Single arg now also returns map
            resultName, _ := argsMap["result-name"].(string)
        }
    }
}

// Migration helper (supports both)
func getCountResults(ctx *cf.Context) []string {
    var results []string
    if countVals, ok := ctx.GlobalFlags["-count"]; ok {
        counts, _ := countVals.([]any)
        for _, countVal := range counts {
            // Try old format (string)
            if str, ok := countVal.(string); ok {
                results = append(results, str)
                continue
            }
            // Try new format (map)
            if argsMap, ok := countVal.(map[string]any); ok {
                if str, ok := argsMap["result-name"].(string); ok {
                    results = append(results, str)
                }
            }
        }
    }
    return results
}
```

## Testing

Add tests to ensure consistent behavior:

```go
func TestAccumulatedFlags_SingleArg(t *testing.T) {
    cmd := cf.NewCommand("test").
        Flag("-flag").
            Arg("arg1").Done().
            Accumulate().
        Done().
        Handler(func(ctx *cf.Context) error {
            vals, _ := ctx.GlobalFlags["-flag"].([]any)
            
            // Should be map even for single arg
            for _, val := range vals {
                m, ok := val.(map[string]any)
                if !ok {
                    t.Errorf("Expected map[string]any, got %T", val)
                }
                if _, ok := m["arg1"]; !ok {
                    t.Error("Expected 'arg1' key in map")
                }
            }
            return nil
        }).
        Build()
    
    cmd.Execute([]string{"-flag", "value1", "-flag", "value2"})
}

func TestGetAccumulatedStrings(t *testing.T) {
    cmd := cf.NewCommand("test").
        Flag("-flag").
            Arg("name").Done().
            Accumulate().
        Done().
        Handler(func(ctx *cf.Context) error {
            results := ctx.GetAccumulatedStrings("-flag", "name")
            
            expected := []string{"value1", "value2"}
            if !reflect.DeepEqual(results, expected) {
                t.Errorf("Expected %v, got %v", expected, results)
            }
            return nil
        }).
        Build()
    
    cmd.Execute([]string{"-flag", "value1", "-flag", "value2"})
}
```

## Documentation

Update autocli documentation to show:

1. Consistent behavior for all accumulated flags
2. Helper methods with examples
3. Struct binding with tag documentation
4. Migration guide from v3.x to v4.x

## Implementation Priority

1. **High Priority**: Solution 2 (Helper Methods)
   - Backwards compatible
   - Solves immediate pain points
   - Can be added to v3.1.0

2. **Medium Priority**: Solution 3 (Struct Binding)
   - Nice-to-have for complex cases
   - Can be added to v3.2.0

3. **Long-term**: Solution 1 (Always Maps)
   - Breaking change requires v4.0.0
   - Discuss with community first
   - Plan migration path

## Open Questions

1. Should maps always use `map[string]string` instead of `map[string]any`?
   - Pros: Simpler, no type assertions needed
   - Cons: Less flexible for future extensions

2. Should helper methods panic or return errors for invalid flags?
   - Recommendation: Return empty slice for missing flags, panic for type mismatches

3. For struct binding, support nested structs for complex args?
   - Probably not needed initially, but keep design extensible
