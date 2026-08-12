# JOIN Performance Analysis and Optimization

Reference: DFC006
Created: 2025-10-30
Last modified: 2025-11-08

[Back to Index](./README.md)

## Current Implementation Analysis

### Algorithm Complexity

The current JOIN implementation uses a **nested loop algorithm**:

```go
// InnerJoin (simplified)
for left := range leftSeq {           // O(n) - iterate left stream
    for _, right := range rightRecords {  // O(m) - iterate right records
        if predicate(left, right) {    // Check each combination
            yield(merge(left, right))
        }
    }
}
```

**Time Complexity:** O(n × m)
- n = number of left records
- m = number of right records
- Predicate evaluated n × m times

**Space Complexity:** O(m)
- Right side fully materialized in memory

### Performance Impact

| Left Size | Right Size | Comparisons | Time (approx) |
|-----------|------------|-------------|---------------|
| 100       | 100        | 10,000      | ~0.1ms        |
| 1,000     | 1,000      | 1,000,000   | ~10ms         |
| 10,000    | 10,000     | 100,000,000 | ~1s           |
| 100,000   | 100,000    | 10,000,000,000 | ~100s      |

The problem scales **quadratically** - not viable for large datasets.

### Current Implementation Strengths

✅ **Generic** - Works with any `JoinPredicate` function
✅ **Simple** - Easy to understand and maintain
✅ **Streaming** - Left side is not materialized (except for RightJoin/FullJoin)
✅ **Correct** - Handles all join types correctly

### Current Implementation Weaknesses

❌ **Slow** - O(n × m) complexity is prohibitive for large datasets
❌ **Inefficient** - No use of indexing or hashing
❌ **Redundant** - Rechecks same conditions millions of times

---

## Optimization Strategy: Hash Join

### Hash Join Algorithm

Database systems use **hash joins** for equi-joins (joins on equality):

```
1. BUILD PHASE:
   - Create hash table from right side
   - Key: join field value(s)
   - Value: list of matching records

2. PROBE PHASE:
   - Stream through left side
   - Look up each left record's join key in hash table
   - Yield matched pairs
```

**Time Complexity:** O(n + m)
- Build phase: O(m) to hash all right records
- Probe phase: O(n) to stream left records with O(1) lookups
- **100x-1000x faster for large datasets**

**Space Complexity:** O(m) - same as current

### Example

```go
// Example data
left:  [{id: 1, name: "Alice"}, {id: 2, name: "Bob"}]
right: [{id: 1, dept: "Eng"}, {id: 3, dept: "Sales"}]

// BUILD PHASE - Hash right side by 'id'
hashTable = {
    1: [{id: 1, dept: "Eng"}],
    3: [{id: 3, dept: "Sales"}]
}

// PROBE PHASE - Stream left and lookup
for each left record:
    key = left["id"]
    matches = hashTable[key]
    for each match in matches:
        yield(merge(left, match))

// Result: [{id: 1, name: "Alice", dept: "Eng"}]
// Only 2 lookups instead of 2×2=4 comparisons
```

---

## Proposed Implementation

### Strategy: Optimized Dispatch

Provide **two implementations**:

1. **Hash Join** - For `OnFields` predicates (equality joins)
2. **Nested Loop** - For custom predicates (fallback)

The join functions detect the predicate type and dispatch accordingly.

### Key Extraction Interface

```go
// KeyExtractor defines how to extract join keys from records
// Used for hash-based joins
type KeyExtractor interface {
    // ExtractKey returns the join key for a record
    // Returns (key, true) if successful, (zero, false) if key missing
    ExtractKey(r Record) (string, bool)

    // IsEquiJoin returns true if this is an equality-based join
    // (allows hash join optimization)
    IsEquiJoin() bool
}

// JoinPredicate can optionally implement KeyExtractor
// If it does, hash join will be used; otherwise nested loop
type JoinPredicate func(left, right Record) bool
```

### Optimized OnFields Implementation

```go
// fieldsJoinPredicate implements both JoinPredicate and KeyExtractor
type fieldsJoinPredicate struct {
    fields []string
}

func (p *fieldsJoinPredicate) Call(left, right Record) bool {
    // Same logic as current OnFields
    for _, field := range p.fields {
        leftVal, leftExists := left[field]
        rightVal, rightExists := right[field]
        if !leftExists || !rightExists || leftVal != rightVal {
            return false
        }
    }
    return true
}

func (p *fieldsJoinPredicate) ExtractKey(r Record) (string, bool) {
    var parts []string
    for _, field := range p.fields {
        val, exists := r[field]
        if !exists {
            return "", false
        }
        parts = append(parts, fmt.Sprintf("%v", val))
    }
    return strings.Join(parts, "|"), true
}

func (p *fieldsJoinPredicate) IsEquiJoin() bool {
    return true
}

// Updated OnFields returns the optimized predicate
func OnFields(fields ...string) JoinPredicate {
    p := &fieldsJoinPredicate{fields: fields}
    return p.Call
}
```

### Hash-Based InnerJoin

```go
func InnerJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record] {
    return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
        return func(yield func(Record) bool) {
            // Check if we can use hash join optimization
            if extractor, ok := predicate.(interface{ ExtractKey(Record) (string, bool) }); ok {
                innerJoinHash(leftSeq, rightSeq, extractor, yield)
                return
            }

            // Fallback to nested loop for custom predicates
            innerJoinNested(leftSeq, rightSeq, predicate, yield)
        }
    }
}

func innerJoinHash(
    leftSeq iter.Seq[Record],
    rightSeq iter.Seq[Record],
    extractor interface{ ExtractKey(Record) (string, bool) },
    yield func(Record) bool,
) {
    // BUILD PHASE: Hash right side
    hashTable := make(map[string][]Record)
    for right := range rightSeq {
        key, ok := extractor.ExtractKey(right)
        if !ok {
            continue // Skip records without join keys
        }
        hashTable[key] = append(hashTable[key], right)
    }

    // PROBE PHASE: Stream left and lookup
    for left := range leftSeq {
        key, ok := extractor.ExtractKey(left)
        if !ok {
            continue // Skip records without join keys
        }

        // O(1) lookup instead of O(m) iteration
        if matches, found := hashTable[key]; found {
            for _, right := range matches {
                joined := make(Record)
                maps.Copy(joined, left)
                maps.Copy(joined, right)
                if !yield(joined) {
                    return
                }
            }
        }
    }
}

func innerJoinNested(
    leftSeq iter.Seq[Record],
    rightSeq iter.Seq[Record],
    predicate JoinPredicate,
    yield func(Record) bool,
) {
    // Current nested loop implementation
    var rightRecords []Record
    for r := range rightSeq {
        rightRecords = append(rightRecords, r)
    }

    for left := range leftSeq {
        for _, right := range rightRecords {
            if predicate(left, right) {
                joined := make(Record)
                maps.Copy(joined, left)
                maps.Copy(joined, right)
                if !yield(joined) {
                    return
                }
            }
        }
    }
}
```

---

## Implementation Approach

### Option 1: Type Assertion (Recommended)

**Pros:**
- ✅ Backward compatible - existing code works unchanged
- ✅ Automatic optimization - no API changes needed
- ✅ Still supports custom predicates
- ✅ Clean separation of concerns

**Cons:**
- ⚠️ Uses reflection/type assertion (minimal overhead)
- ⚠️ Predicate implementation is more complex

### Option 2: Separate Functions

Provide explicit hash join functions:

```go
// HashInnerJoin for equality joins (optimized)
func HashInnerJoin(rightSeq iter.Seq[Record], fields ...string) Filter[Record, Record]

// InnerJoin for custom predicates (current implementation)
func InnerJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record]
```

**Pros:**
- ✅ Explicit - users know when optimization is used
- ✅ Simpler implementation
- ✅ No reflection needed

**Cons:**
- ❌ Breaking change - users must update code
- ❌ Two APIs for same operation
- ❌ CLI commands need to choose which to use

---

## Benchmarking Results (Projected)

### Small Dataset (1K × 1K)
```
Nested Loop:  1,000,000 comparisons  ~10ms
Hash Join:    1,000 + 1,000          ~1ms
Speedup:      10x
```

### Medium Dataset (10K × 10K)
```
Nested Loop:  100,000,000 comparisons  ~1s
Hash Join:    10,000 + 10,000         ~10ms
Speedup:      100x
```

### Large Dataset (100K × 100K)
```
Nested Loop:  10,000,000,000 comparisons  ~100s
Hash Join:    100,000 + 100,000          ~100ms
Speedup:      1000x
```

---

## Recommendations

### Phase 1: Implement Hash Join (Priority: HIGH)

1. **Implement `KeyExtractor` interface pattern**
2. **Update `OnFields` to return optimized predicate**
3. **Add `innerJoinHash` implementation**
4. **Keep `innerJoinNested` as fallback**
5. **Update all 4 join types (Inner/Left/Right/Full)**

**Estimated Effort:** 1-2 days
**Impact:** 100-1000x performance improvement for equality joins
**Risk:** Low - backward compatible

### Phase 2: Benchmarking and Validation

1. **Create benchmark suite**
   - Small/medium/large dataset sizes
   - Compare nested loop vs hash join
   - Measure memory usage
2. **Add integration tests**
   - Verify correctness on real datasets
   - Test edge cases (missing keys, duplicates, nulls)

**Estimated Effort:** 1 day
**Impact:** High confidence in optimization

### Phase 3: Documentation

1. **Update sql.go comments**
   - Document O(n+m) complexity
   - Explain when hash join is used
2. **Update CLI help**
   - Mention performance characteristics
3. **Add performance guide**
   - When to use JOIN vs nested loops
   - How to optimize join queries

**Estimated Effort:** 0.5 days

---

## Edge Cases to Handle

### Missing Keys
```go
// What if a record doesn't have the join field?
left:  [{id: 1, name: "Alice"}, {name: "Bob"}]  // Missing id
right: [{id: 1, dept: "Eng"}]

// Should skip record with missing key (same as predicate returning false)
```

### Null/Nil Values
```go
// How to handle nil values?
left:  [{id: nil, name: "Alice"}]
right: [{id: nil, dept: "Eng"}]

// Option 1: nil == nil (match)
// Option 2: nil != nil (no match, SQL-style)
// Recommend: SQL-style (nil != nil)
```

### Duplicate Keys (One-to-Many)
```go
right: [{id: 1, dept: "Eng"}, {id: 1, dept: "Sales"}]

// Hash table should store []Record for each key
hashTable[1] = [{id: 1, dept: "Eng"}, {id: 1, dept: "Sales"}]

// Correctly handles one-to-many joins
```

### Type Mismatches
```go
left:  [{id: "1", name: "Alice"}]   // string id
right: [{id: 1, dept: "Eng"}]       // int id

// Current: fmt.Sprintf("%v", val) handles this
// Hash join: same approach (convert to string for key)
```

---

## Alternative Approaches Considered

### 1. Sort-Merge Join
- Pre-sort both sides, merge in one pass
- O(n log n + m log m) complexity
- **Rejected:** Requires full materialization of both sides

### 2. Indexed Nested Loop
- Build index on right side, but still iterate
- O(n × log m) with tree index
- **Rejected:** Hash join is simpler and faster

### 3. Parallel Hash Join
- Build hash table in parallel
- Probe phase parallelized
- **Future consideration:** After hash join baseline established

---

## Conclusion

The current nested loop JOIN implementation is correct but inefficient for large datasets. Implementing a hash join for equality predicates (the 99% use case) will provide **100-1000x speedup** with minimal code complexity and zero breaking changes.

**Recommendation:** Proceed with Option 1 (Type Assertion) for hash join optimization.

**Next Steps:**
1. Implement `KeyExtractor` pattern
2. Update `OnFields` to return optimized predicate
3. Add hash join implementation for all 4 join types
4. Create benchmark suite
5. Update documentation

This optimization is critical for production use of ssql with real-world datasets.
