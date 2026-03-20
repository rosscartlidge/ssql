# Performance-Critical Code Patterns

**When writing code that processes records in a loop, follow these patterns to avoid performance regressions.**

ssql processes millions of records. Small inefficiencies multiply into significant slowdowns. The v4.5.0-v4.6.2 optimization work achieved 4x speedup by applying these principles.

## 1. Schema Sharing - The #1 Performance Rule

Creating a `Schema` involves sorting field names and building an index map. **Never create schemas per-record.**

```go
// BAD - Creates schema for every record (was 28% of CPU time!)
for row := range csvReader {
    record := MakeMutableRecord()
    for i, value := range row {
        record.fields[headers[i]] = parse(value)
    }
    yield(record.Freeze())  // Freeze() calls NewSchema() - expensive!
}

// GOOD - Create schema once, share across all records
schema := NewSchema(headers)
fieldIndices := make([]int, len(headers))
for i, h := range headers {
    fieldIndices[i] = schema.Index(h)
}

for row := range csvReader {
    values := make([]any, schema.Width())
    for i, value := range row {
        values[fieldIndices[i]] = parse(value)
    }
    yield(NewRecordFromSchema(schema, values))  // Reuses schema!
}
```

**Result: 43s -> 10.4s (4.1x faster) for 14.6M records**

## 2. Schema Caching for Variable-Schema Data

When fields might vary between records (like JSONL without schema header), cache the schema and reuse when fields match:

```go
var cachedSchema *Schema
var cachedFields []string

for line := range lines {
    mutableRecord := ParseJSONLine(line)
    if cachedSchema != nil && fieldsMatch(mutableRecord, cachedFields) {
        values := make([]any, cachedSchema.Width())
        for i, f := range cachedSchema.fields {
            values[i] = mutableRecord.fields[f]
        }
        record = Record{schema: cachedSchema, values: values}
    } else {
        record = mutableRecord.Freeze()
        cachedSchema = record.schema
        cachedFields = cachedSchema.fields
    }
}
```

## 3. Buffer Reuse

Pre-allocate buffers outside loops and reset with slice tricks:

```go
// GOOD - Reuse buffer across records
buf := make([]byte, 0, 4096)
for record := range records {
    buf = buf[:0]  // Reset to zero length, keep capacity
    buf = record.AppendJSON(buf)
    buf = append(buf, '\n')
    writer.Write(buf)
}
```

## 4. Pre-compute Where Possible

Store computed values in schemas or outside loops:

```go
// Schema stores pre-computed JSON field prefixes
type Schema struct {
    fields       []string
    jsonPrefixes [][]byte  // Pre-computed `"field":` for each field
}

// Computed once in NewSchema(), used millions of times in AppendJSON()
func (r Record) AppendJSON(buf []byte) []byte {
    for i, v := range r.values {
        buf = append(buf, r.schema.jsonPrefixes[i]...)  // No string alloc!
        buf = appendJSONValue(buf, v)
    }
}
```

## 5. Avoid Hidden Double-Work

Watch for code that does work twice (two Freeze() calls per record, etc.)

## 6. Profile Before Optimizing

```bash
go test -cpuprofile cpu.prof -bench BenchmarkName
go tool pprof cpu.prof
```

The v4.6.0 fix came from profiling showing 28% CPU in `NewSchema` - not where we expected!

## Performance Checklist for Record-Processing Code

- [ ] Is schema created once and shared? (`NewRecordFromSchema`)
- [ ] For variable schemas, is caching implemented?
- [ ] Are buffers pre-allocated and reused?
- [ ] Is there any double-Freeze() or double-schema creation?
- [ ] Have you profiled to verify the optimization works?

**Reference:** See `doc/research/record-performance-optimization.md` for detailed analysis.
