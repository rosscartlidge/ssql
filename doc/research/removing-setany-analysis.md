# Removing SetAny: Impact Analysis

Reference: DFC017
Created: 2025-11-05
Last modified: 2025-11-08

[Back to Index](./README.md)

## Executive Summary

`SetAny` is a method on `MutableRecord` that bypasses the `Value` type constraint, allowing any type to be stored in a record. It's currently used in 4 places in the codebase, all outside the core package.

**Recommendation**: Removing `SetAny` is feasible but requires refactoring JSONL parsing and the select command to use type switches and typed setters.

---

## Current Usage

### 1. JSONL Reader (`cmd/ssql/lib/jsonl.go:42, 139`)

**Current Code:**
```go
func ReadJSONL(input io.Reader) iter.Seq[ssql.Record] {
    return func(yield func(ssql.Record) bool) {
        scanner := bufio.NewScanner(input)
        for scanner.Scan() {
            var data map[string]interface{}
            if err := json.Unmarshal(scanner.Bytes(), &data); err != nil {
                continue
            }

            record := ssql.MakeMutableRecord()
            for k, v := range data {
                record = record.SetAny(k, convertJSONValue(v))  // ← Uses SetAny
            }

            if !yield(record.Freeze()) {
                return
            }
        }
    }
}

func convertJSONValue(v interface{}) interface{} {
    switch val := v.(type) {
    case float64:
        if val == float64(int64(val)) {
            return int64(val)
        }
        return val
    case bool, string:
        return val
    case nil:
        return nil  // ← INVALID: nil is not a Value type
    case []interface{}:
        return val  // ← INVALID: []interface{} is not a Value type
    case map[string]interface{}:
        record := ssql.MakeMutableRecord()
        for k, subv := range val {
            record = record.SetAny(k, convertJSONValue(subv))  // ← Uses SetAny
        }
        return record.Freeze()
    default:
        return fmt.Sprintf("%v", v)
    }
}
```

**Issues:**
1. `convertJSONValue` returns `nil` and `[]interface{}` which violate `Value` constraint
2. Uses `SetAny` to store these invalid types
3. Nested objects recursively use `SetAny`

**Replacement Strategy:**

Create a new function that uses typed setters:

```go
func setValueFromJSON(record ssql.MutableRecord, key string, v interface{}) ssql.MutableRecord {
    switch val := v.(type) {
    case float64:
        // JSON numbers are always float64
        if val == float64(int64(val)) {
            return record.Int(key, int64(val))
        }
        return record.Float(key, val)

    case bool:
        return record.Bool(key, val)

    case string:
        return record.String(key, val)

    case nil:
        // Option A: Skip nil values (don't set field)
        return record

        // Option B: Set to empty string
        // return record.String(key, "")

    case []interface{}:
        // Option A: Skip arrays
        return record

        // Option B: Convert to comma-separated string
        // strs := make([]string, len(val))
        // for i, item := range val {
        //     strs[i] = fmt.Sprintf("%v", item)
        // }
        // return record.String(key, strings.Join(strs, ","))

        // Option C: Store as JSON string
        // jsonBytes, _ := json.Marshal(val)
        // return record.String(key, string(jsonBytes))

    case map[string]interface{}:
        // Nested object - convert to Record recursively
        nested := ssql.MakeMutableRecord()
        for k, subv := range val {
            nested = setValueFromJSON(nested, k, subv)
        }
        return ssql.Set(record, key, nested.Freeze())

    default:
        // Unknown type - convert to string
        return record.String(key, fmt.Sprintf("%v", v))
    }
}

// Updated ReadJSONL
func ReadJSONL(input io.Reader) iter.Seq[ssql.Record] {
    return func(yield func(ssql.Record) bool) {
        scanner := bufio.NewScanner(input)
        for scanner.Scan() {
            var data map[string]interface{}
            if err := json.Unmarshal(scanner.Bytes(), &data); err != nil {
                continue
            }

            record := ssql.MakeMutableRecord()
            for k, v := range data {
                record = setValueFromJSON(record, k, v)  // ← No SetAny
            }

            if !yield(record.Freeze()) {
                return
            }
        }
    }
}
```

**Key Decisions Needed:**
- What to do with `nil` values? (skip, empty string, or something else?)
- What to do with arrays? (skip, stringify, JSON-encode?)

**Files to Change:**
- `cmd/ssql/lib/jsonl.go` (lines 42, 139)

---

### 2. Select Command (`cmd/ssql/main.go:650`)

**Current Code:**
```go
// Build selector function
selector := func(r ssql.Record) ssql.Record {
    result := ssql.MakeMutableRecord()
    for origField, newField := range fieldMap {
        if val, exists := ssql.Get[any](r, origField); exists {
            result = result.SetAny(newField, val)  // ← Uses SetAny
        }
    }
    return result.Freeze()
}
```

**Issue:**
Copying fields of unknown type from one record to another.

**Replacement Strategy:**

Option A: Use type assertion to determine type and call appropriate setter:

```go
selector := func(r ssql.Record) ssql.Record {
    result := ssql.MakeMutableRecord()
    for origField, newField := range fieldMap {
        if val, exists := ssql.Get[any](r, origField); exists {
            result = setTypedValue(result, newField, val)
        }
    }
    return result.Freeze()
}

// Helper function
func setTypedValue(record ssql.MutableRecord, key string, val any) ssql.MutableRecord {
    switch v := val.(type) {
    case int64:
        return record.Int(key, v)
    case float64:
        return record.Float(key, v)
    case bool:
        return record.Bool(key, v)
    case string:
        return record.String(key, v)
    case time.Time:
        return ssql.Set(record, key, v)
    case ssql.Record:
        return ssql.Set(record, key, v)
    default:
        // Handle sequences or unknown types
        // For now, convert to string as fallback
        return record.String(key, fmt.Sprintf("%v", v))
    }
}
```

Option B: Use `ssql.Set[any]` with the `Value` constraint (if that compiles):

```go
selector := func(r ssql.Record) ssql.Record {
    result := ssql.MakeMutableRecord()
    for origField, newField := range fieldMap {
        if val, exists := ssql.Get[any](r, origField); exists {
            // This may not work if val is not guaranteed to be Value type
            result = ssql.Set(result, newField, val)
        }
    }
    return result.Freeze()
}
```

**Files to Change:**
- `cmd/ssql/main.go` (line 650)

---

### 3. Select Command Code Generation (`cmd/ssql/commands/select.go:85, 156, 161`)

**Current Code:**
```go
// Runtime execution
result = result.SetAny(newField, val)

// Code generation
code += fmt.Sprintf("\t\t\tresult = result.SetAny(%q, val)\n", origField)
```

**Issue:**
Both runtime and generated code use `SetAny`.

**Replacement Strategy:**

Generate code with type switch:

```go
// Generated code should look like:
result := ssql.MakeMutableRecord()
for origField, newField := range map[string]string{"age": "age", "name": "full_name"} {
    if val, exists := ssql.Get[any](r, origField); exists {
        switch v := val.(type) {
        case int64:
            result = result.Int(newField, v)
        case float64:
            result = result.Float(newField, v)
        case bool:
            result = result.Bool(newField, v)
        case string:
            result = result.String(newField, v)
        case time.Time:
            result = ssql.Set(result, newField, v)
        case ssql.Record:
            result = ssql.Set(result, newField, v)
        default:
            result = result.String(newField, fmt.Sprintf("%v", v))
        }
    }
}
```

Or use a helper function (cleaner):

```go
// Add helper to generated code:
func setTypedValue(record ssql.MutableRecord, key string, val any) ssql.MutableRecord {
    switch v := val.(type) {
    case int64:
        return record.Int(key, v)
    case float64:
        return record.Float(key, v)
    case bool:
        return record.Bool(key, v)
    case string:
        return record.String(key, v)
    case time.Time:
        return ssql.Set(record, key, v)
    case ssql.Record:
        return ssql.Set(record, key, v)
    default:
        return record.String(key, fmt.Sprintf("%v", v))
    }
}

// Then use it:
for origField, newField := range fieldMap {
    if val, exists := ssql.Get[any](r, origField); exists {
        result = setTypedValue(result, newField, val)
    }
}
```

**Files to Change:**
- `cmd/ssql/commands/select.go` (lines 85, 156, 161)

---

### 4. Examples (`examples/*.go`)

**Current Code:**
```go
// examples/json_process_chain.go:149, 288
mutable.SetAny(k, v)

// examples/unix_pipes_demo.go:26, 113
result = result.SetAny(k, v)
```

**Issue:**
Examples show users patterns that bypass type safety.

**Replacement Strategy:**

Replace with type switches as shown above. This will make examples longer but more type-safe and educational.

**Files to Change:**
- `examples/json_process_chain.go` (lines 149, 288)
- `examples/unix_pipes_demo.go` (lines 26, 113)

---

## Additional Considerations

### 1. Backwards Compatibility

Removing `SetAny` is a **BREAKING CHANGE** for any external users who are using it.

**Migration Path:**
- Release a version that deprecates `SetAny` with a clear deprecation warning
- Provide a helper function or documentation showing how to replace it
- In next major version (v2.0.0?), remove it entirely

### 2. Performance

Type switches add minimal overhead compared to the current approach. The generated code will be slightly more verbose but equally performant at runtime.

### 3. Testing

All existing tests should continue to pass since the behavior is the same (just more type-safe).

New tests may be needed for edge cases:
- Handling of nil values in JSON
- Handling of arrays in JSON
- Nested object conversion

### 4. Documentation Updates

Need to update:
- API reference to remove `SetAny` documentation
- Examples to use new patterns
- Migration guide for users upgrading

---

## Implementation Plan

### Phase 1: Add Helper Functions (Non-Breaking)
1. Add `setTypedValue()` helper to `cmd/ssql/helpers.go`
2. Add `setValueFromJSON()` helper to `cmd/ssql/lib/jsonl.go`
3. Test that they work correctly

### Phase 2: Refactor Internal Usage (Non-Breaking)
1. Update JSONL reader to use `setValueFromJSON()`
2. Update select command to use `setTypedValue()`
3. Update select code generation to emit type switches
4. Update examples to use typed setters
5. Run all tests and fix any issues

### Phase 3: Deprecate SetAny (Non-Breaking)
1. Add deprecation comment to `SetAny`:
   ```go
   // SetAny is deprecated and will be removed in v2.0.0.
   // Use typed setters (Int, Float, Bool, String) or ssql.Set[V Value]() instead.
   // For dynamic types, use a type switch to determine the appropriate setter.
   func (m MutableRecord) SetAny(field string, value any) MutableRecord {
   ```
2. Release as v1.6.0 (or similar minor version bump)

### Phase 4: Remove SetAny (BREAKING)
1. Delete the `SetAny` method from `core.go`
2. Verify no internal usage remains
3. Release as v2.0.0

---

## Risks

1. **User Code Breakage**: Any external code using `SetAny` will break
2. **JSON Edge Cases**: Deciding what to do with nil and arrays may surprise users
3. **Complexity**: Code becomes more verbose (but more type-safe)

---

## Benefits

1. **Type Safety**: Eliminates bypass of `Value` constraint
2. **Bug Prevention**: Can't accidentally store invalid types
3. **Consistency**: All code uses the same typed setter patterns
4. **Teaching**: Examples show best practices instead of shortcuts

---

## Decision Required

**Key Question**: What should happen to nil and array values in JSON?

**Option A: Skip them** (field not set)
- Pro: Cleanest, no invalid types
- Con: Data loss (silent)

**Option B: Convert to string**
- Pro: No data loss
- Con: Loses semantic meaning, unexpected for users

**Option C: Error/warning**
- Pro: User knows about the issue
- Con: More complex error handling in streaming context

**Recommendation**: **Option A (skip)** for nil, **Option B (stringify)** for arrays, with clear documentation about the behavior.

---

## Estimated Effort

- **Phase 1**: 2-3 hours (helper functions + tests)
- **Phase 2**: 3-4 hours (refactor all usage + tests)
- **Phase 3**: 30 minutes (deprecation annotation + release)
- **Phase 4**: 30 minutes (removal + release)

**Total**: ~6-8 hours of development + testing time
