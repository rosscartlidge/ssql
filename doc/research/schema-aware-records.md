# Schema-Aware Records: Performance Optimization Research

Reference: DFC028
Created: 2025-12-13
Last modified: 2025-12-13

[Back to Index](./README.md)

## Executive Summary

This document explores replacing `map[string]any` with a schema-aware `[]any` slice design to improve performance for large datasets. Benchmarks show **2-10x speedup** for record merging operations (joins), with **1.3-4x memory reduction**.

**Recommendation:** Document and defer. The optimization is significant but adds complexity. Current design is adequate for most use cases, and the generated code escape hatch exists for performance-critical workloads.

## Problem Statement

Current `Record` design:
```go
type Record struct {
    fields map[string]any
}
```

For 14.6M records with 3 joins, CPU profiling shows:
- **70% of CPU time in garbage collection** (`runtime.scanobject`)
- Each join creates new `map[string]any` for every output record
- Map operations involve hashing, bucket management, pointer chasing

## Proposed Design

### Core Types

```go
// Schema is shared across all records with the same structure
// Allocated once per unique schema, not per record
type Schema struct {
    index  map[string]int  // field name -> position (for lookups)
    fields []string        // ordered field names (for iteration)
}

// Record stores only the values, references shared schema
type Record struct {
    schema *Schema  // pointer to shared schema (8 bytes)
    values []any    // just the data (slice header: 24 bytes)
}
```

### Why This Is Faster

**Map-based merge (current):**
```go
// Per-record cost: 2 map allocations + N hash operations + N pointer writes
merged := make(map[string]any, len(left)+len(right))  // alloc 1
for k, v := range left.fields {                        // N hash lookups
    merged[k] = v                                      // N hash insertions
}
for k, v := range right.fields {                       // M hash lookups
    merged[k] = v                                      // M hash insertions
}
return Record{fields: merged}
```

**Slice-based merge (proposed):**
```go
// Per-record cost: 1 slice allocation + 2 memcpy operations
values := make([]any, 0, len(left)+len(right))  // alloc 1 (pre-sized)
values = append(values, left.values...)          // memcpy
values = append(values, right.values...)         // memcpy
return Record{schema: mergedSchema, values: values}
```

Key differences:
1. **No hashing** - slice append is O(1) amortized
2. **Better memory locality** - contiguous slice vs scattered map buckets
3. **Shared schema** - schema pointer reused across millions of records
4. **Simpler GC** - slices are easier for GC to scan than maps

## Benchmark Results

### Micro-benchmark: Single Merge Operation

```
Merge 10,000,000 records:
  Map version:   3.08s (308 ns/op)
  Slice version: 0.42s (42 ns/op)
  Speedup:       7.3x
```

### Realistic: Single Join (1M × 500)

```
Map version:   581ms, 676 MB alloc
Slice version: 107ms, 302 MB alloc
Speedup:       5.4x
Memory:        2.2x less
```

### Scaled: 3 Chained Joins (5M records)

```
Map version:   4.09s, 5.30 GB alloc
Slice version: 2.11s, 4.10 GB alloc
Speedup:       1.94x
Memory:        1.29x less
```

### Projected Impact on 14.6M Record Workload

Current generated code: ~70s
Projected with slice design: ~35-50s (based on 1.5-2x improvement at scale)

## Implementation Complexity

### Changes Required

#### 1. Core Types (core.go)

```go
// New types
type Schema struct {
    index  map[string]int
    fields []string
    frozen bool  // immutable after first use
}

type Record struct {
    schema *Schema
    values []any
}

// Schema registry to deduplicate schemas
var schemaCache sync.Map  // map[schemaKey]*Schema

func getOrCreateSchema(fields []string) *Schema {
    key := strings.Join(fields, "\x00")
    if s, ok := schemaCache.Load(key); ok {
        return s.(*Schema)
    }
    s := &Schema{
        index:  make(map[string]int, len(fields)),
        fields: fields,
    }
    for i, f := range fields {
        s.index[f] = i
    }
    s.frozen = true
    schemaCache.Store(key, s)
    return s
}
```

#### 2. Field Access

```go
// Get field by name - still O(1) via map lookup
func (r Record) Get(field string) (any, bool) {
    if idx, ok := r.schema.index[field]; ok {
        return r.values[idx], true
    }
    return nil, false
}

// Iterate all fields
func (r Record) All() iter.Seq2[string, any] {
    return func(yield func(string, any) bool) {
        for i, field := range r.schema.fields {
            if !yield(field, r.values[i]) {
                return
            }
        }
    }
}
```

#### 3. Record Creation (MutableRecord)

```go
type MutableRecord struct {
    fields map[string]any  // still use map for building
}

func (m MutableRecord) Freeze() Record {
    // Convert to schema-based record
    fields := make([]string, 0, len(m.fields))
    for k := range m.fields {
        fields = append(fields, k)
    }
    sort.Strings(fields)  // deterministic ordering

    schema := getOrCreateSchema(fields)
    values := make([]any, len(fields))
    for i, f := range fields {
        values[i] = m.fields[f]
    }
    return Record{schema: schema, values: values}
}
```

#### 4. Join Operations (sql.go)

```go
func mergeSchemas(left, right *Schema) *Schema {
    fields := make([]string, 0, len(left.fields)+len(right.fields))
    fields = append(fields, left.fields...)
    fields = append(fields, right.fields...)
    return getOrCreateSchema(fields)
}

func mergeRecords(left, right Record, schema *Schema) Record {
    values := make([]any, 0, len(left.values)+len(right.values))
    values = append(values, left.values...)
    values = append(values, right.values...)
    return Record{schema: schema, values: values}
}

func innerJoinHash(...) {
    // Compute merged schema once
    var mergedSchema *Schema

    // ... build hash table ...

    for left := range leftSeq {
        // Initialize merged schema on first match
        if mergedSchema == nil && len(hashTable) > 0 {
            // Get a sample right record to determine schema
            for _, rights := range hashTable {
                if len(rights) > 0 {
                    mergedSchema = mergeSchemas(left.schema, rights[0].schema)
                    break
                }
            }
        }

        if matches, found := hashTable[key]; found {
            for _, right := range matches {
                if !yield(mergeRecords(left, right, mergedSchema)) {
                    return
                }
            }
        }
    }
}
```

#### 5. CSV Reading (io.go)

```go
func ReadCSVFromReader(reader io.Reader, config ...CSVConfig) iter.Seq[Record] {
    return func(yield func(Record) bool) {
        // ... read headers ...

        // Create schema once from headers
        schema := getOrCreateSchema(headers)

        for {
            row, err := csvReader.Read()
            if err != nil {
                return
            }

            // Create record with shared schema
            values := make([]any, len(row))
            for i, val := range row {
                values[i] = fieldParsers[i](val)
            }

            if !yield(Record{schema: schema, values: values}) {
                return
            }
        }
    }
}
```

### Complexity Assessment

| Area | Change Level | Risk |
|------|--------------|------|
| core.go (Record type) | **High** | Breaking change to fundamental type |
| MutableRecord.Freeze() | Medium | Internal change, API preserved |
| Field access (Get, All) | Medium | Behavior preserved, implementation changes |
| Join operations | Medium | Internal optimization |
| CSV/JSON reading | Medium | Create schema once, reuse |
| CSV/JSON writing | Low | Iterate via All(), unchanged |
| CLI commands | Low | Use Get/All API, mostly unchanged |
| Tests | **High** | Many tests create Records directly |

### Breaking Changes

1. **Record struct layout** - Any code accessing `record.fields` directly breaks
2. **Record literals** - Can't use `Record{fields: map[string]any{...}}`
3. **Field ordering** - Schema imposes ordering (but All() preserves it)

### Migration Path

**Phase 1: Internal optimization only**
- Keep `map[string]any` as primary storage
- Add optional schema for operations that benefit (joins)
- No breaking changes

**Phase 2: Dual representation**
- Record can be either map-based or schema-based
- Convert on demand
- Gradual migration

**Phase 3: Full migration**
- Schema-based as default
- Map-based only for MutableRecord building phase
- Breaking change, major version bump

## Edge Cases and Concerns

### 1. Dynamic Field Addition

Current design allows:
```go
record.fields["new_field"] = value  // just works
```

Schema design requires:
```go
// Must create new record with new schema
newSchema := extendSchema(record.schema, "new_field")
newValues := append(record.values, value)
newRecord := Record{schema: newSchema, values: newValues}
```

**Impact:** Update operations become more complex internally, but API can hide this.

### 2. Field Removal

Current: `delete(record.fields, "field")`

Schema: Must create new schema without field, copy other values.

**Impact:** Exclude operations need schema manipulation.

### 3. Schema Explosion

If every record has unique fields, you get schema proliferation:
```go
// Worst case: N records = N schemas = no benefit
```

**Mitigation:**
- Schema cache with LRU eviction
- Fall back to map-based for highly dynamic schemas
- Warning when schema count exceeds threshold

### 4. Concurrent Access

Schema cache needs synchronization:
```go
var schemaCache sync.Map  // thread-safe but has overhead
```

**Mitigation:** Accept sync.Map overhead (still faster than per-record maps).

### 5. Field Name Collisions in Joins

When joining, right fields may shadow left fields:
```go
// left: {id: 1, name: "Alice"}
// right: {id: 1, dept: "Sales"}
// merged: {id: 1, name: "Alice", id: 1, dept: "Sales"}  // duplicate!
```

**Current behavior:** Right overwrites left (map semantics)
**Schema behavior:** Both preserved in order (slice append)

**Decision needed:** Preserve current semantics or change?

### 6. Serialization

JSON/CSV output uses `All()` iterator - works with both designs.
But direct JSON marshaling of Record struct would change.

## Alternative Approaches Considered

### 1. Object Pooling

```go
var recordPool = sync.Pool{
    New: func() any { return make(map[string]any, 16) },
}
```

**Pros:** No API changes
**Cons:** Complex lifetime management, moderate improvement (~1.5x)

### 2. Arena Allocation (Go 1.23+)

```go
arena := arena.NewArena()
defer arena.Free()
// Allocate all records in arena
```

**Pros:** Batch deallocation, good for pipelines
**Cons:** Experimental API, complex lifetime management

### 3. Columnar Storage

```go
type Table struct {
    schema  *Schema
    columns [][]any  // column-major storage
}
```

**Pros:** Cache-friendly for column operations, SIMD potential
**Cons:** Major redesign, poor for row-oriented operations (joins)

### 4. Code Generation for Known Schemas

Generate typed structs at runtime or compile time:
```go
type GeneratedRecord struct {
    AKind string
    AName string
    // ...
}
```

**Pros:** Maximum performance, type safety
**Cons:** Complexity, loss of dynamism

## Recommendations

### Short Term (Now)
1. **Keep current design** - adequate for most use cases
2. **Document this research** - for future reference
3. **Keep the `mergeRecords()` optimization** - small improvement, no complexity

### Medium Term (If Needed)
1. **Phase 1 implementation** - internal optimization for joins only
2. **Benchmark on real workloads** - validate projected improvements
3. **Profile memory patterns** - understand schema reuse in practice

### Long Term (Major Version)
1. **Consider full migration** if:
   - Users regularly process 10M+ records
   - Join performance is a common complaint
   - Other optimizations exhausted

## Conclusion

The schema-aware design offers **2-10x improvement** for record merging operations, with diminishing returns at scale (2x for large chained joins). The implementation adds **moderate complexity** and requires careful handling of edge cases.

For ssql's current use case (flexible data exploration tool), the **flexibility of `map[string]any` outweighs the performance cost**. The generated code escape hatch provides a path for performance-critical production workloads.

This optimization should be **revisited if**:
1. Join performance becomes a frequent user complaint
2. The 10M+ record use case becomes common
3. A major version bump is planned anyway

## Appendix: Benchmark Code

See `/tmp/schema_bench.go`, `/tmp/realistic_bench2.go`, `/tmp/scale_bench.go` for full benchmark implementations used in this analysis.
