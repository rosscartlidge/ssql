# Record Design - Encapsulated Struct (v1.0+)

**BREAKING CHANGE in v1.0:** Record is now an encapsulated struct, not a bare `map[string]any`.

## Record vs MutableRecord

**Record (Immutable):**
- Struct with private `fields map[string]any`
- Immutable - methods return new copies
- Use for function parameters, return values, pipeline data
- Access via `Get()`, `GetOr()`, `.All()` iterator

**MutableRecord (Mutable Builder):**
- Struct with private `fields map[string]any`
- Mutable - methods modify in-place and return self for chaining
- Use for efficient record construction
- Convert to Record via `.Freeze()` (creates copy)

## Creating Records

```go
// Use MutableRecord builder
record := ssql.MakeMutableRecord().
    String("name", "Alice").
    Int("age", int64(30)).
    Float("salary", 95000.50).
    Bool("active", true).
    Freeze()  // Convert to immutable Record

// From map (for compatibility)
record := ssql.NewRecord(map[string]any{
    "name": "Alice",
    "age": int64(30),
})
```

## Accessing Record Fields

**Within ssql package:** Direct `.fields` access is allowed.

**Outside ssql package (CLI commands, tests, user code):**
```go
// Use Get/GetOr
name := ssql.GetOr(record, "name", "")
age := ssql.GetOr(record, "age", int64(0))

// Iterate with .All()
for k, v := range record.All() {
    fmt.Printf("%s: %v\n", k, v)
}

// Build with MutableRecord
mut := ssql.MakeMutableRecord()
mut = mut.String("city", "NYC")           // Chainable
mut = mut.SetAny("field", anyValue)       // For unknown types
frozen := mut.Freeze()                    // Convert to Record
```

## Iterating Over Records

```go
// Use .All() iterator (maps.All pattern)
for k, v := range record.All() { ... }

// Use .KeysIter() for keys only
for k := range record.KeysIter() { ... }

// Use .Values() for values only
for v := range record.Values() { ... }
```

## Record Field Access (CRITICAL)

**ALWAYS use `Get()` or `GetOr()` methods to read fields from Records. NEVER use direct map access or type assertions.**

```go
// CORRECT - Use GetOr with appropriate default
name := ssql.GetOr(r, "name", "")
age := ssql.GetOr(r, "age", int64(0))
price := ssql.GetOr(r, "price", float64(0.0))

// WRONG - Direct map access with type assertion (WILL PANIC!)
name := r["name"].(string)  // Panic if field missing or wrong type
```

**Code Generation Rules:**
- **String operations**: Always use `ssql.GetOr(r, field, "")` with empty string default
- **Numeric operations**: Always use `ssql.GetOr(r, field, float64(0))` or `int64(0)` default
- **Never generate**: Type assertions like `r[field].(string)` or custom helpers like `asFloat64()`
