# Record Performance Optimization Plan

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

## Conclusion

The slice-based Record with custom JSON encoding offers the best balance of:
- **Performance**: 4-7x improvement achievable
- **Compatibility**: API unchanged, output identical
- **Maintainability**: Simpler than alternatives
- **Philosophy**: Maintains ssql's dynamic, streaming nature

Recommended implementation order: Phase 1 → Phase 2 → Phase 4 → Phase 3
(Core refactor, then fast writing, then CLI, then fast reading)
