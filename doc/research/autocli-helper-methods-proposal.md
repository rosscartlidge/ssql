# Proposal: Add Helper Methods for Accumulated Flags

## Summary

Add type-safe helper methods to `Context` for accessing accumulated multi-argument flags, eliminating the current type assertion complexity and inconsistent return types.

## Problem

When using `.Accumulate()` with multi-argument flags, autocli currently returns different types depending on the number of arguments:

```go
// Single argument: returns []any containing strings
Flag("-count").Arg("name").Done().Accumulate()
// ctx.GlobalFlags["-count"] = []any{"total", "average"}

// Multiple arguments: returns []any containing maps
Flag("-sum").Arg("field").Done().Arg("result").Done().Accumulate()
// ctx.GlobalFlags["-sum"] = []any{
//     map[string]any{"field": "salary", "result": "total"},
//     map[string]any{"field": "hours", "result": "worked"},
// }
```

This forces awkward, error-prone parsing code:

```go
// User must handle different types
if vals, ok := ctx.GlobalFlags["-count"]; ok {
    for _, v := range vals.([]any) {
        // Is it a string or a map?
        if str, ok := v.(string); ok {
            // Single arg case
        } else if m, ok := v.(map[string]any); ok {
            // Multi arg case
        }
    }
}
```

## Proposed Solution

Add two helper methods to `Context`:

```go
// GetAccumulatedStrings returns values from a single-argument accumulated flag
// Returns empty slice if flag not present
func (ctx *Context) GetAccumulatedStrings(flag string, argName string) []string

// GetAccumulatedMaps returns values from a multi-argument accumulated flag
// Returns empty slice if flag not present
func (ctx *Context) GetAccumulatedMaps(flag string, argNames ...string) []map[string]string
```

### Example Usage

**Before (Current API):**
```go
// Parse -count flags (single arg)
if countVals, ok := ctx.GlobalFlags["-count"]; ok {
    counts, _ := countVals.([]any)
    for _, countVal := range counts {
        if resultName, ok := countVal.(string); ok {
            fmt.Println("Count:", resultName)
        }
    }
}

// Parse -sum flags (two args)
if sumVals, ok := ctx.GlobalFlags["-sum"]; ok {
    sums, _ := sumVals.([]any)
    for _, sumVal := range sums {
        if argsMap, ok := sumVal.(map[string]any); ok {
            field, _ := argsMap["field"].(string)
            result, _ := argsMap["result"].(string)
            fmt.Printf("Sum %s as %s\n", field, result)
        }
    }
}
```

**After (With Helper Methods):**
```go
// Parse -count flags (single arg) - clean and type-safe
for _, resultName := range ctx.GetAccumulatedStrings("-count", "name") {
    fmt.Println("Count:", resultName)
}

// Parse -sum flags (two args) - clean and type-safe
for _, spec := range ctx.GetAccumulatedMaps("-sum", "field", "result") {
    fmt.Printf("Sum %s as %s\n", spec["field"], spec["result"])
}
```

## Implementation

### Location
Add to `context.go` in the autocli package.

### Implementation Sketch

```go
// GetAccumulatedStrings returns all values from a single-argument accumulated flag.
// Returns empty slice if flag not present.
func (ctx *Context) GetAccumulatedStrings(flag string, argName string) []string {
    vals, ok := ctx.GlobalFlags[flag]
    if !ok {
        return nil
    }

    accumulated, ok := vals.([]any)
    if !ok {
        return nil
    }

    result := make([]string, 0, len(accumulated))
    for _, val := range accumulated {
        // Handle single-arg case (direct string)
        if str, ok := val.(string); ok {
            result = append(result, str)
            continue
        }
        // Handle multi-arg case (map) for backwards compatibility
        if m, ok := val.(map[string]any); ok {
            if str, ok := m[argName].(string); ok {
                result = append(result, str)
            }
        }
    }
    return result
}

// GetAccumulatedMaps returns all values from a multi-argument accumulated flag.
// Returns empty slice if flag not present.
func (ctx *Context) GetAccumulatedMaps(flag string, argNames ...string) []map[string]string {
    vals, ok := ctx.GlobalFlags[flag]
    if !ok {
        return nil
    }

    accumulated, ok := vals.([]any)
    if !ok {
        return nil
    }

    result := make([]map[string]string, 0, len(accumulated))
    for _, val := range accumulated {
        m, ok := val.(map[string]any)
        if !ok {
            continue
        }

        // Convert map[string]any to map[string]string
        strMap := make(map[string]string)
        for k, v := range m {
            if str, ok := v.(string); ok {
                strMap[k] = str
            }
        }
        result = append(result, strMap)
    }
    return result
}
```

## Benefits

1. **Type Safety**: No manual type assertions in user code
2. **Backwards Compatible**: No breaking changes, existing code continues to work
3. **Clear Intent**: Method names clearly communicate what they return
4. **Less Error-Prone**: Eliminates common mistakes with type assertions
5. **Self-Documenting**: Code reads naturally without type conversion noise

## Real-World Impact

From ssql's `group-by` command, this reduces ~30 lines of type assertion code to ~10 lines of clear, readable logic.

## Testing

Add unit tests for:
- Single-argument accumulated flags
- Multi-argument accumulated flags
- Missing flags (should return empty slice)
- Empty accumulated flags
- Mixed types (edge cases)

Example test:

```go
func TestGetAccumulatedStrings(t *testing.T) {
    cmd := NewCommand("test").
        Flag("-count").
            Arg("name").Done().
            Accumulate().
        Done().
        Handler(func(ctx *Context) error {
            results := ctx.GetAccumulatedStrings("-count", "name")
            expected := []string{"total", "average"}
            if !reflect.DeepEqual(results, expected) {
                t.Errorf("got %v, want %v", results, expected)
            }
            return nil
        })

    cmd.Execute([]string{"-count", "total", "-count", "average"})
}
```

## Documentation

Update:
- `Context` godoc with examples
- README.md with accumulated flags section
- Migration guide (optional, since backwards compatible)

## Version

This is backwards compatible, can be added in **v3.1.0** or **v3.2.0** (minor version bump).

## Questions for Review

1. Should `GetAccumulatedMaps` return `map[string]string` or `map[string]any`?
   - Recommendation: `map[string]string` - simpler, covers 99% of use cases

2. Should methods return `nil` or empty slice for missing flags?
   - Recommendation: `nil` - Go idiom, works with `range`

3. Should we also add `GetAccumulatedArgs(flag string) [][]string` for positional args?
   - Recommendation: Not yet - wait for real use case

## Alternative Considered

We considered making accumulated flags always return maps (breaking change requiring v4.0.0), but helper methods solve the same problem without migration pain.

---

**Recommendation**: Approve for implementation in next minor release (v3.1.0 or v3.2.0).
