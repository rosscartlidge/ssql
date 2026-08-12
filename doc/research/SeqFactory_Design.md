# SeqFactory Design Document

Reference: DFC001
Created: 2025-09-29
Last modified: 2025-11-08

[Back to Index](./README.md)

## Problem Statement

Current `iter.Seq[T]` types in the `Value` constraint have a fundamental issue: **they are not copyable or reusable**.

```go
// Current problematic usage:
sequence := slices.Values([]int{1, 2, 3})
record := SetField(record, "numbers", sequence)

// Later usage - BROKEN:
seq1 := Get[iter.Seq[int]](record, "numbers")
for val := range seq1 { ... } // Works first time

seq2 := Get[iter.Seq[int]](record, "numbers")
for val := range seq2 { ... } // FAILS - sequence already consumed!
```

## Solution: SeqFactory Pattern

Replace direct `iter.Seq[T]` storage with **factory functions** that can create fresh sequences on demand.

### 1. Define SeqFactory[T] Type

```go
// SeqFactory creates fresh iter.Seq[T] on each call
type SeqFactory[T any] func() iter.Seq[T]
```

**Benefits:**
- ✅ **Copyable**: Can be stored and reused multiple times
- ✅ **Fresh sequences**: Each call creates a new iterator
- ✅ **Lazy evaluation**: Only creates data when needed
- ✅ **Memory efficient**: Doesn't hold materialized data

### 2. Update Value Constraint

**Before:**
```go
type Value interface {
    // ... basic types ...
    iter.Seq[int] | iter.Seq[string] | iter.Seq[Record] | ...
}
```

**After:**
```go
type Value interface {
    // ... basic types ...
    SeqFactory[int] | SeqFactory[int8] | SeqFactory[int16] | SeqFactory[int32] | SeqFactory[int64] |
    SeqFactory[uint] | SeqFactory[uint8] | SeqFactory[uint16] | SeqFactory[uint32] | SeqFactory[uint64] |
    SeqFactory[float32] | SeqFactory[float64] |
    SeqFactory[bool] | SeqFactory[string] | SeqFactory[time.Time] |
    SeqFactory[Record]
}
```

### 3. Helper Functions for Factory Creation

#### 3.1 SetIterFactory - Convert iter.Seq to Factory

```go
// SetIterFactory materializes a sequence and stores it as a reusable factory
func SetIterFactory[T any](r Record, field string, seq iter.Seq[T]) Record {
    // Materialize the sequence to avoid consumption issues
    var data []T
    for item := range seq {
        data = append(data, item)
    }

    // Create factory that returns fresh sequences
    factory := func() iter.Seq[T] {
        return slices.Values(data)
    }

    return SetField(r, field, factory)
}
```

**Usage:**
```go
// Convert any iter.Seq to a reusable factory
numbers := slices.Values([]int{1, 2, 3})
record := SetIterFactory(record, "numbers", numbers)

// Now can be used multiple times
seq1 := Get[SeqFactory[int]](record, "numbers")()
seq2 := Get[SeqFactory[int]](record, "numbers")() // Fresh sequence!
```

#### 3.2 Additional Helper Functions

```go
// SetSliceFactory - Create factory from slice (most common case)
func SetSliceFactory[T any](r Record, field string, data []T) Record {
    factory := func() iter.Seq[T] {
        return slices.Values(data)
    }
    return SetField(r, field, factory)
}

// SetGeneratorFactory - Create factory from generator function
func SetGeneratorFactory[T any](r Record, field string, generator func() iter.Seq[T]) Record {
    return SetField(r, field, SeqFactory[T](generator))
}
```

### 4. Handling Infinite Sequences

**Challenge**: Infinite sequences cannot be materialized.

```go
// Infinite sequence example
func infiniteNumbers() iter.Seq[int] {
    return func(yield func(int) bool) {
        for i := 0; ; i++ {
            if !yield(i) {
                return
            }
        }
    }
}
```

#### 4.1 Detection Strategy

```go
// SetIterFactory with infinite sequence protection
func SetIterFactory[T any](r Record, field string, seq iter.Seq[T]) Record {
    const maxMaterialization = 10000 // Safety limit

    var data []T
    count := 0

    for item := range seq {
        data = append(data, item)
        count++

        if count >= maxMaterialization {
            // Assume infinite - store as generator instead
            return SetGeneratorFactory(r, field, func() iter.Seq[T] {
                // Return the original sequence logic (dangerous!)
                // This is problematic - need better approach
            })
        }
    }

    // Finite sequence - safe to materialize
    factory := func() iter.Seq[T] {
        return slices.Values(data)
    }
    return SetField(r, field, factory)
}
```

#### 4.2 Better Approach for Infinite Sequences

**Option A: Explicit infinite sequence handling**
```go
// SetInfiniteFactory - For known infinite sequences
func SetInfiniteFactory[T any](r Record, field string, generator func() iter.Seq[T]) Record {
    factory := SeqFactory[T](generator)
    return SetField(r, field, factory)
}

// Usage:
record := SetInfiniteFactory(record, "numbers", func() iter.Seq[int] {
    return infiniteNumbers() // User knows it's infinite
})
```

**Option B: Lazy factory approach**
```go
// SetLazyFactory - Store the generator, don't materialize
func SetLazyFactory[T any](r Record, field string, generator func() iter.Seq[T]) Record {
    return SetField(r, field, SeqFactory[T](generator))
}
```

### 5. Migration Strategy

#### Phase 1: Add SeqFactory Support
- ✅ Define `SeqFactory[T]` type
- ✅ Update `Value` constraint to include factories
- ✅ Keep existing `iter.Seq[T]` support temporarily

#### Phase 2: Add Helper Functions
- ✅ Implement `SetIterFactory` with finite sequence support
- ✅ Implement `SetSliceFactory`, `SetGeneratorFactory`
- ✅ Add infinite sequence detection/handling

#### Phase 3: Update Examples and Tests
- ✅ Convert examples to use factory pattern
- ✅ Add tests for reusability
- ✅ Test infinite sequence handling

#### Phase 4: Deprecate Direct iter.Seq (Optional)
- ❓ Remove `iter.Seq[T]` from `Value` constraint
- ❓ Force all sequence storage to use factories

### 6. Usage Examples

#### 6.1 Basic Usage
```go
// Create record with sequence data
data := []int{1, 2, 3, 4, 5}
record := SetSliceFactory(Record{}, "numbers", data)

// Use multiple times
factory := Get[SeqFactory[int]](record, "numbers")
seq1 := factory()
seq2 := factory() // Fresh sequence

for n := range seq1 { fmt.Print(n) } // 12345
for n := range seq2 { fmt.Print(n) } // 12345 (works!)
```

#### 6.2 Converting Existing Sequences
```go
// Convert existing iter.Seq
filtered := Where(func(x int) bool { return x > 2 })(slices.Values([]int{1,2,3,4,5}))
record := SetIterFactory(record, "filtered", filtered)

// Use later
factory := Get[SeqFactory[int]](record, "filtered")
for n := range factory() { fmt.Print(n) } // 345
```

#### 6.3 Infinite Sequences
```go
// Infinite sequence - use explicit infinite factory
record := SetInfiniteFactory(record, "naturals", func() iter.Seq[int] {
    return func(yield func(int) bool) {
        for i := 1; ; i++ {
            if !yield(i) { return }
        }
    }
})

// Use with care (e.g., with Limit)
factory := Get[SeqFactory[int]](record, "naturals")
limited := Limit[int](10)(factory())
for n := range limited { fmt.Print(n) } // 12345678910
```

## Implementation Priority

1. **High Priority**: Define `SeqFactory[T]` and update `Value` constraint
2. **High Priority**: Implement `SetSliceFactory` (most common case)
3. **Medium Priority**: Implement `SetIterFactory` with finite sequence support
4. **Medium Priority**: Add infinite sequence detection and `SetInfiniteFactory`
5. **Low Priority**: Consider deprecating direct `iter.Seq[T]` storage

## Open Questions

1. **Memory vs Flexibility**: Should we always materialize sequences, or support both materialized and generator approaches?
2. **Infinite Detection**: What's the best threshold for detecting infinite sequences? 10k? 100k? Configuration?
3. **Error Handling**: How should we handle memory exhaustion during materialization?
4. **Backwards Compatibility**: Should we keep `iter.Seq[T]` in `Value` constraint for compatibility?

### 7. Flattening Operations for SeqFactory Fields

Once SeqFactory is implemented, we'll need to add flattening operations from StreamV2 to handle Records containing sequence fields.

#### 7.1 DotFlatten - Dot Product Expansion

**Purpose**: Expands sequence fields using dot product (linear, one-to-one mapping).

**StreamV2 Behavior**:
```go
// Input Record with streams
{"id": 1, "tags": Stream["a", "b"], "scores": Stream[10, 20]}

// DotFlatten output (pairs corresponding elements)
[
  {"id": 1, "tags": "a", "scores": 10},
  {"id": 1, "tags": "b", "scores": 20}
]
```

**ssql Implementation** (with SeqFactory):
```go
func DotFlatten(separator string, fields ...string) Filter[Record, Record]
```

**Key Features**:
- **Minimum length strategy**: Uses shortest sequence length, discards excess
- **Nested record flattening**: `{"user": {"name": "Alice"}} → {"user.name": "Alice"}`
- **Field-specific**: Can target specific fields for flattening
- **SeqFactory support**: Works with `SeqFactory[T]` fields by calling factory to get fresh sequences

#### 7.2 CrossFlatten - Cartesian Product Expansion

**Purpose**: Expands sequence fields using cross product (cartesian product).

**StreamV2 Behavior**:
```go
// Input Record with streams
{"id": 1, "tags": Stream["a", "b"], "colors": Stream["red", "blue"]}

// CrossFlatten output (all combinations)
[
  {"id": 1, "tags": "a", "colors": "red"},
  {"id": 1, "tags": "a", "colors": "blue"},
  {"id": 1, "tags": "b", "colors": "red"},
  {"id": 1, "tags": "b", "colors": "blue"}
]
```

**ssql Implementation**:
```go
func CrossFlatten(separator string, fields ...string) Filter[Record, Record]
```

**Key Features**:
- **Cartesian product**: All possible combinations of sequence elements
- **Exponential expansion**: Can create large result sets with multiple sequences
- **Field-specific**: Can target specific fields for expansion
- **SeqFactory support**: Works with reusable sequence factories

#### 7.3 Implementation Requirements for ssql

**SeqFactory Detection**:
```go
// Detect SeqFactory fields in records
func hasSeqFactoryFields(record Record, fields ...string) bool {
    for _, field := range fields {
        if factory, ok := Get[SeqFactory[any]](record, field); ok {
            // This field contains a sequence factory
            return true
        }
    }
    return false
}
```

**Factory Materialization**:
```go
// Convert SeqFactory to materialized slice for flattening
func materializeFactories(record Record, fields ...string) map[string][]any {
    materialized := make(map[string][]any)

    for _, field := range fields {
        if factory, ok := Get[SeqFactory[any]](record, field); ok {
            var values []any
            for item := range factory() {
                values = append(values, item)
            }
            materialized[field] = values
        }
    }
    return materialized
}
```

**Dot Product Algorithm** (adapted from StreamV2):
```go
func dotExpandRecords(record Record, materializedSeqs map[string][]any) []Record {
    // Find minimum length across all sequences
    minLen := math.MaxInt
    for _, values := range materializedSeqs {
        if len(values) < minLen {
            minLen = len(values)
        }
    }

    // Create paired records using minimum length
    results := make([]Record, minLen)
    for i := 0; i < minLen; i++ {
        result := make(Record)

        // Copy non-sequence fields
        for k, v := range record {
            if _, isSeq := materializedSeqs[k]; !isSeq {
                result[k] = v
            }
        }

        // Add corresponding element from each sequence
        for field, values := range materializedSeqs {
            result[field] = values[i]
        }

        results[i] = result
    }

    return results
}
```

**Cross Product Algorithm** (adapted from StreamV2):
```go
func crossExpandRecords(record Record, materializedSeqs map[string][]any) []Record {
    // Calculate cartesian product of all sequences
    return cartesianProduct(record, materializedSeqs)
}

func cartesianProduct(baseRecord Record, sequences map[string][]any) []Record {
    // Recursive cartesian product implementation
    // Returns all possible combinations
}
```

#### 7.4 Integration with SeqFactory System

**Type Safety**:
- Use generic `SeqFactory[T]` detection
- Support multiple value types in same record
- Preserve type information where possible

**Performance**:
- Only materialize sequences when flattening is needed
- Support lazy evaluation where possible
- Efficient memory usage for large expansions

**Error Handling**:
- Handle empty sequences gracefully
- Memory limits for large cartesian products
- Infinite sequence protection (use materialization limits)

#### 7.5 Usage Examples with SeqFactory

**DotFlatten Example**:
```go
// Create record with sequence factories
record := NewRecord().
    String("user_id", "123").
    Build()

// Add sequence factories
record = SetSliceFactory(record, "tags", []string{"work", "urgent"})
record = SetSliceFactory(record, "scores", []int{85, 92})

// Apply dot flattening
flattened := DotFlatten(".", "tags", "scores")(slices.Values([]Record{record}))

// Result: 2 records with paired elements
// {"user_id": "123", "tags": "work", "scores": 85}
// {"user_id": "123", "tags": "urgent", "scores": 92}
```

**CrossFlatten Example**:
```go
// Same record setup
record := SetSliceFactory(record, "priorities", []string{"high", "low"})
record = SetSliceFactory(record, "types", []string{"bug", "feature"})

// Apply cross flattening
crossed := CrossFlatten(".", "priorities", "types")(slices.Values([]Record{record}))

// Result: 4 records (2 × 2 cartesian product)
// {"user_id": "123", "priorities": "high", "types": "bug"}
// {"user_id": "123", "priorities": "high", "types": "feature"}
// {"user_id": "123", "priorities": "low", "types": "bug"}
// {"user_id": "123", "priorities": "low", "types": "feature"}
```

## Next Steps

1. Implement basic `SeqFactory[T]` type and update `Value` constraint
2. Add `SetSliceFactory` helper function
3. Test reusability with examples
4. Address infinite sequence handling based on feedback
5. **Implement DotFlatten and CrossFlatten for SeqFactory fields**
6. **Add flattening tests and examples**