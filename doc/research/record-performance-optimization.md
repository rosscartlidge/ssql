# Record Performance Optimization Plan

Reference: DFC037
Created: 2026-01-14
Last modified: 2026-01-15

[Back to Index](./README.md)

## Problem Statement

The current ssql pipeline has significant performance overhead compared to raw Go CSV operations:

| Operation | Time (14.6M rows) | Rows/sec |
|-----------|-------------------|----------|
| Raw Go CSV read/write | 5.1s | 2.9M |
| ssql generated code | 17.3s | 845K |
| ssql pipeline | 46s | 318K |

The pipeline is **9x slower** than raw CSV. Even generated code (no JSONL serialization) is **3.4x slower**.

## Root Cause Analysis

CPU profiling reveals the bottlenecks:

| Function | % CPU | Notes |
|----------|-------|-------|
| JSON map encoding | ~30% | `encoding/json.mapEncoder.encode` |
| Memory allocation | ~18% | `runtime.mallocgc` |
| Map assignment | ~16% | `runtime.mapassign_faststr` |
| CSV reading | ~6% | `encoding/csv.readRecord` |

**The bottleneck is NOT type parsing** - it's:
1. Creating `map[string]any` for each record (~16% CPU)
2. JSON encoding maps via reflection (~30% CPU)
3. General memory allocation overhead (~18% CPU)

## Proposed Solution

### Current Record Structure

```go
type Record struct {
    fields map[string]any  // Allocated per record
}

// Field access
value := record.fields["field_name"]  // Map lookup
```

### Proposed Record Structure

```go
type Schema struct {
    Fields  []string         // ["name", "age", "salary"]
    Indices map[string]int   // {"name": 0, "age": 1, "salary": 2}
    Types   []FieldType      // [String, Int, Float]
    Width   int              // len(Fields) - for exact allocation
}

type Record struct {
    schema *Schema  // Shared across all records in stream
    values []any    // Allocated per record (slice, not map)
}

// Field access
idx := record.schema.Indices["field_name"]
value := record.values[idx]  // Array index (O(1), no hash)
```

### Allocation Efficiency

With known schema, we allocate exactly the right size:

```go
// Current: map with overhead (buckets, hash table, ~50 bytes + per-entry overhead)
fields := make(map[string]any)  // Unknown size, grows dynamically

// Proposed: exact slice size (24 bytes header + 16 bytes per element)
values := make([]any, schema.Width)  // Exact size, no growth
```

**Memory per record (8 fields):**
- `map[string]any`: ~200-300 bytes (header + buckets + 8 entries with string keys)
- `[]any`: ~152 bytes (24 byte header + 8×16 byte elements)

**~50% memory reduction** plus elimination of hash table operations.

### Custom JSON Encoder

Replace `encoding/json` with direct buffer writing:

```go
func (r *Record) AppendJSON(buf []byte) []byte {
    buf = append(buf, '{')
    for i, v := range r.values {
        if i > 0 {
            buf = append(buf, ',')
        }
        // Pre-computed: `"field_name":`
        buf = append(buf, r.schema.jsonPrefixes[i]...)
        buf = appendJSONValue(buf, v)
    }
    buf = append(buf, '}')
    return buf
}
```

## Benchmark Results

Prototype benchmarks on 14.6M rows:

| Approach | Time | vs Raw | Notes |
|----------|------|--------|-------|
| Raw CSV | 5.1s | 1.0x | Baseline |
| **[]any + fast JSON** | **5.0s** | **1.0x** | Proposed approach |
| map + encoding/json | 19s | 3.7x | Minimal overhead |
| ssql from (current) | 37s | 7.3x | Full current overhead |

**Expected improvement: 4-7x faster I/O operations**

## Implementation Plan

### Phase 1: Core Record Refactor

**Files to modify:**
- `core.go` - New Record and Schema types
- `io.go` - CSV/JSON readers to use new structure

**Changes:**
1. Add `Schema` type with field names, indices, and types
2. Change `Record.fields` from `map[string]any` to `[]any`
3. Add `Record.schema` pointer (shared across records in stream)
4. Update `Get()`, `GetOr()`, `Has()` to use index lookup
5. Update `MutableRecord` to work with slice-based storage

**API compatibility:**
- `Get[T](record, "field")` - unchanged API, different implementation
- `GetOr(record, "field", default)` - unchanged
- `record.All()` - still works, iterates schema + values

### Phase 2: Fast JSON Encoder

**Files to modify:**
- `io.go` - Add `WriteJSONFast()` functions

**Changes:**
1. Pre-compute JSON field prefixes (`"field":`) in Schema
2. Implement `appendJSONValue()` for each type
3. Buffer reuse with `sync.Pool`
4. Direct `[]byte` building without reflection

### Phase 3: Fast JSON Decoder

**Files to modify:**
- `io.go` - Add `ReadJSONFast()` functions

**Changes:**
1. Schema-aware JSON parsing
2. Direct slice population without map intermediate
3. Type-specific parsing based on schema

### Phase 4: CLI Integration

**Files to modify:**
- `cmd/ssql/commands/*.go` - Use new Record APIs

**Changes:**
1. Pass schema through pipeline (already have `_schema` header)
2. Update commands to use schema-aware operations
3. Ensure backward compatibility with existing pipelines

## Risk Assessment

### Breaking Changes

| Change | Impact | Mitigation |
|--------|--------|------------|
| Record internal structure | Internal only | API unchanged |
| JSON format | None | Output identical |
| Performance characteristics | Positive | Faster is better |

### Compatibility Concerns

1. **External code using Record.fields** - Already private, no impact
2. **Commands assuming map iteration order** - Schema provides consistent order
3. **Dynamic field addition** - MutableRecord needs schema extension support

## Performance Targets

| Metric | Current | Target | Improvement |
|--------|---------|--------|-------------|
| CSV → JSONL | 37s | 8s | 4.6x |
| JSONL → CSV | 9s | 3s | 3x |
| Full pipeline | 46s | 12s | 3.8x |
| Generated code | 17s | 8s | 2.1x |

## Alternative Approaches Considered

### 1. Use faster JSON library (goccy/go-json)
- **Pro**: Drop-in replacement, 2-3x faster
- **Con**: Still uses maps, doesn't address allocation overhead
- **Verdict**: Partial solution, doesn't achieve full potential

### 2. Binary format between pipeline stages (msgpack, gob)
- **Pro**: Faster than JSON
- **Con**: Loses human readability, debugging harder
- **Verdict**: Could be added as option, but JSONL is valuable for debugging

### 3. Columnar storage (Apache Arrow style)
- **Pro**: Maximum performance for analytics
- **Con**: Major architecture change, loses streaming capability
- **Verdict**: Too disruptive, better suited for different tool

### 4. Struct code generation
- **Pro**: Zero allocation for known schemas
- **Con**: Loses flexibility, requires code gen step
- **Verdict**: Against ssql philosophy of dynamic schemas

## Implementation Results (v4.5.0 - v4.6.2)

The optimization was implemented across versions v4.5.0 through v4.6.2. This section documents the actual changes made and results achieved.

### Actual Performance Achieved

| Version | Operation | Time (14.6M rows) | Improvement |
|---------|-----------|-------------------|-------------|
| Pre-v4.5.0 | `from shuffled.csv` | 43s | baseline |
| v4.5.0 | Record refactor + fast JSON | 43s | (no change - write path) |
| v4.5.1 | `AppendJSONOrdered()` | 23.5s | 1.8x faster |
| v4.6.0 | CSV schema sharing | 10.4s | 4.1x faster |
| v4.6.1 | JSONL schema sharing | 15.8s | 2.7x faster (pipeline) |
| v4.6.2 | Comprehensive schema caching | 15.7s | consistent |

**Final result: 43s → 10.4s for CSV read (4.1x faster), 47s → 15.8s for pipeline (3x faster)**

### What Was Actually Implemented

#### Phase 1: Core Record Refactor (v4.5.0)

**Changes to `core.go`:**

```go
// New Schema type with pre-computed JSON prefixes
type Schema struct {
    fields       []string          // Sorted field names
    index        map[string]int    // Field name → index
    jsonPrefixes [][]byte          // Pre-computed `"field":` for each field
    width        int
}

// Record now uses schema + values slice
type Record struct {
    schema *Schema
    values []any
}

// NewRecordFromSchema for efficient record creation with shared schema
func NewRecordFromSchema(schema *Schema, values []any) Record
```

**Key insight**: The schema stores pre-computed JSON field prefixes (`"field":`) to avoid string allocation during JSON encoding.

#### Phase 2: Fast JSON Encoder (v4.5.0)

**Changes to `core.go`:**

```go
// AppendJSON writes JSON directly to buffer without reflection
func (r Record) AppendJSON(buf []byte) []byte {
    buf = append(buf, '{')
    for i, v := range r.values {
        if v == nil { continue }
        if !first { buf = append(buf, ',') }
        buf = append(buf, r.schema.jsonPrefixes[i]...)  // Pre-computed prefix
        buf = appendJSONValue(buf, v)  // Direct type switch, no reflection
    }
    buf = append(buf, '}')
    return buf
}
```

**Benchmark**: 3x faster, 7x less memory, 2238x fewer allocations vs `encoding/json`.

#### Phase 3: Fast JSON Decoder (v4.5.0)

**Changes to `core.go`:**

```go
// ParseJSONLine manually parses JSON without reflection
func ParseJSONLine(line []byte) (MutableRecord, error) {
    // Manual parsing with type detection:
    // - Numbers: detect int64 vs float64 during parse
    // - Strings: direct slice reference when possible
    // - No intermediate map[string]interface{}
}

// ParseJSONLineWithSchema parses directly into shared schema (v4.6.1)
func ParseJSONLineWithSchema(line []byte, schema *Schema) (Record, error) {
    values := make([]any, schema.Width())
    // Parse JSON, placing values directly at schema indices
    return Record{schema: schema, values: values}, nil
}
```

#### Phase 4: Schema Sharing - The Critical Optimization (v4.6.0 - v4.6.2)

**The key insight**: Creating a schema involves sorting field names and building an index map. This is expensive when done per-record.

**v4.6.0 - CSV Schema Sharing (`io.go`):**

```go
func ReadCSVFromReader(reader io.Reader, config ...CSVConfig) iter.Seq[Record] {
    return func(yield func(Record) bool) {
        // Create schema ONCE before the loop
        fieldNames := append(headers, "_row_number")
        slices.Sort(fieldNames)
        schema := NewSchema(fieldNames)

        // Pre-compute field index mapping
        fieldIndices := make([]int, len(headers))
        for i, h := range headers {
            fieldIndices[i] = schema.Index(h)
        }
        rowNumIdx := schema.Index("_row_number")

        // For each row - create values slice, not schema
        for row := range csvReader {
            values := make([]any, schema.Width())
            for i, value := range row {
                values[fieldIndices[i]] = parse(value)
            }
            values[rowNumIdx] = rowNumber
            yield(NewRecordFromSchema(schema, values))  // Reuse schema!
        }
    }
}
```

**Result: 43s → 10.4s (4.1x faster)**

**v4.6.1 - JSONL Schema Sharing (`cmd/ssql/lib/jsonl.go`):**

For JSONL with schema headers, the schema is parsed once and shared:

```go
func readJSONLWithSchema(r io.Reader, schema *Schema) iter.Seq[ssql.Record] {
    // Create ssql.Schema once from lib.Schema
    ssqlSchema := ssql.NewSchema(schema.Fields)

    for line := range lines {
        // Use fast parser that populates shared schema directly
        record, _ := ssql.ParseJSONLineWithSchema(line, ssqlSchema)
        yield(record)
    }
}
```

**Result: 47s → 15.8s for `from | group-by` pipeline (3x faster)**

**v4.6.2 - Comprehensive Schema Caching:**

Extended schema caching to ALL reader functions:

1. **`ReadCSVSafeFromReader`** - Same pattern as `ReadCSVFromReader`
2. **`ReadJSONFastFromReader`** - Cache schema for consecutive records:
   ```go
   var cachedSchema *Schema
   var cachedFields []string

   for line := range lines {
       mutableRecord := ParseJSONLine(line)

       // Check if we can reuse cached schema
       if cachedSchema != nil && fieldsMatch(mutableRecord, cachedFields) {
           values := make([]any, cachedSchema.Width())
           for i, f := range cachedSchema.fields {
               values[i] = mutableRecord.fields[f]
           }
           record = Record{schema: cachedSchema, values: values}
       } else {
           record = mutableRecord.Freeze()  // Creates new schema
           cachedSchema = record.schema
           cachedFields = cachedSchema.fields
       }
   }
   ```
3. **`ReadJSONFastSafeFromReader`** - Same caching pattern
4. **CLI `readJSONLines`** - Fixed double schema creation (was calling Freeze() twice)

### Key Lessons Learned

1. **Schema creation is expensive**: `NewSchema()` sorts fields and builds index map. Do it once, not per-record.

2. **Look for hidden allocations**: The old `readJSONLines` was:
   - Calling `ParseJSONLine()` which creates a MutableRecord
   - Calling `Freeze()` on that to get a Record with schema
   - Then building a NEW MutableRecord with type coercion
   - Calling `Freeze()` AGAIN - creating a SECOND schema!

3. **Profile before optimizing**: CPU profiling (`go tool pprof`) revealed:
   - 28% in `NewSchema` - not JSON encoding as expected
   - Led directly to the schema sharing fix

4. **Buffer reuse matters**: Pre-allocating buffers and using `buf = buf[:0]` to reset without reallocation.

5. **Pre-compute where possible**: JSON field prefixes (`"field":`) stored in schema, computed once.

### Files Modified

| File | Changes |
|------|---------|
| `core.go` | New Schema type, Record refactor, AppendJSON(), AppendJSONOrdered(), ParseJSONLine(), ParseJSONLineWithSchema() |
| `io.go` | ReadCSVFromReader schema sharing, ReadCSVSafeFromReader, ReadJSONFastFromReader caching, ReadJSONFastSafeFromReader caching |
| `cmd/ssql/lib/json.go` | writeJSONLOrdered buffer reuse, readJSONLines schema caching |
| `cmd/ssql/lib/jsonl.go` | readJSONLWithSchema using ParseJSONLineWithSchema, WriteJSONLWithSchemaOrdered buffer reuse |

## Conclusion

The slice-based Record with custom JSON encoding offers the best balance of:
- **Performance**: 4.1x improvement achieved (target was 4.6x)
- **Compatibility**: API unchanged, output identical
- **Maintainability**: Simpler than alternatives
- **Philosophy**: Maintains ssql's dynamic, streaming nature

**Critical takeaway**: Schema sharing was the biggest win. The fast JSON encoder/decoder helped, but sharing schemas across records in a stream was the key optimization that achieved the 4x speedup.
