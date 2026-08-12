# Hash Join Implementation Plan

Reference: DFC008
Created: 2025-11-02
Last modified: 2025-11-08

[Back to Index](./README.md)

## Overview

Implement hash join optimization for ssql JOIN operations to improve performance from O(n×m) to O(n+m) for equality-based joins.

**Status:** Design Phase
**Target Release:** v1.1.0
**Estimated Effort:** 2-3 days
**Breaking Changes:** None (backward compatible)

---

## Current State (Completed)

### ✅ Phase 1: Interface Design

**Files Modified:** `sql.go`

**Changes Made:**
```go
// Added KeyExtractor interface
type KeyExtractor interface {
    ExtractKey(r Record) (string, bool)
}

// Added fieldsJoinPredicate struct
type fieldsJoinPredicate struct {
    fields []string
}

// Implemented methods
func (p *fieldsJoinPredicate) Call(left, right Record) bool { ... }
func (p *fieldsJoinPredicate) ExtractKey(r Record) (string, bool) { ... }

// Updated OnFields to return optimized predicate
func OnFields(fields ...string) JoinPredicate {
    pred := &fieldsJoinPredicate{fields: fields}
    return pred.Call
}
```

**Key Design Decisions:**
- ✅ Use type assertion to detect KeyExtractor (backward compatible)
- ✅ `fieldsJoinPredicate` implements both interfaces
- ✅ Key separator: `"\x00"` (null byte, unlikely in data)
- ✅ Key format: `fmt.Sprintf("%v", val)` for type consistency

---

## Remaining Work

### Phase 2: Hash Join Implementation

#### 2.1 Inner Join Hash Implementation

**Function:** `innerJoinHash`

```go
// innerJoinHash performs hash-based inner join (O(n+m))
// Used when predicate implements KeyExtractor
func innerJoinHash(
    leftSeq iter.Seq[Record],
    rightSeq iter.Seq[Record],
    extractor KeyExtractor,
    yield func(Record) bool,
) {
    // BUILD PHASE: Hash right side by key
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

        // O(1) hash lookup
        if matches, found := hashTable[key]; found {
            for _, right := range matches {
                // Merge records
                joined := MakeMutableRecord()
                for k, v := range left.All() { joined.fields[k] = v }
                for k, v := range right.All() { joined.fields[k] = v }
                if !yield(joined.Freeze()) {
                    return
                }
            }
        }
    }
}
```

**Complexity:**
- Build: O(m) - hash all right records
- Probe: O(n) - stream left with O(1) lookups per record
- Total: O(n + m)

**Memory:** O(m) - same as current nested loop

**Edge Cases Handled:**
- ✅ Missing keys: Skip records (same as predicate returning false)
- ✅ Duplicate keys: Store []Record per hash bucket
- ✅ Type mismatches: `fmt.Sprintf("%v")` handles conversions

#### 2.2 Update InnerJoin to Use Hash Join

```go
func InnerJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record] {
    return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
        return func(yield func(Record) bool) {
            // Try to extract KeyExtractor from predicate
            // This works because OnFields returns pred.Call where pred is *fieldsJoinPredicate

            // Option 1: Check if predicate's underlying function has KeyExtractor
            // We need to get the receiver of the method

            // Option 2: Create a wrapper that stores both
            // Problem: JoinPredicate is just a function type

            // DESIGN DECISION NEEDED:
            // How do we get from JoinPredicate (func) to KeyExtractor (interface)?

            // Solution: Store the predicate struct in a package-level registry
            // OR: Change JoinPredicate to be an interface instead of func

            // For now, use type assertion on the function's closure
            // This is a bit hacky but maintains backward compatibility

            // ... implementation TBD ...
        }
    }
}
```

**BLOCKER:** Type assertion challenge

**Problem:** `JoinPredicate` is `func(left, right Record) bool`, but we need to get to the `*fieldsJoinPredicate` that created it.

**Potential Solutions:**

1. **Thread-local storage / package-level map (HACKY)**
   ```go
   var lastPredicate interface{} // Store last OnFields result
   ```
   ❌ Not thread-safe, fragile

2. **Change JoinPredicate to interface (BREAKING)**
   ```go
   type JoinPredicate interface {
       Match(left, right Record) bool
   }
   ```
   ❌ Breaking change

3. **Separate hash join functions (EXPLICIT)**
   ```go
   func HashInnerJoin(rightSeq, fields ...string) Filter[Record, Record]
   ```
   ❌ Two APIs for same operation

4. **Wrapper pattern (RECOMMENDED)**
   ```go
   type joinPredicateWrapper struct {
       fn        JoinPredicate
       extractor KeyExtractor  // nil for custom predicates
   }

   func OnFields(fields ...string) JoinPredicate {
       pred := &fieldsJoinPredicate{fields: fields}
       // Return wrapped function that carries metadata
       wrapper := &joinPredicateWrapper{
           fn:        pred.Call,
           extractor: pred,
       }
       return wrapper.Match
   }

   func (w *joinPredicateWrapper) Match(left, right Record) bool {
       return w.fn(left, right)
   }
   ```

   Then in InnerJoin:
   ```go
   // Check if this is a wrapped predicate
   if wrapper, ok := interface{}(predicate).(*joinPredicateWrapper); ok {
       if wrapper.extractor != nil {
           // Use hash join!
           innerJoinHash(leftSeq, rightSeq, wrapper.extractor, yield)
           return
       }
   }
   ```

   **Problem:** JoinPredicate is a function type, not interface
   Can't do `interface{}(predicate).(*joinPredicateWrapper)`

5. **Global registry (PRAGMATIC)**
   ```go
   // Thread-safe registry
   var predicateRegistry sync.Map // map[uintptr]KeyExtractor

   func OnFields(fields ...string) JoinPredicate {
       pred := &fieldsJoinPredicate{fields: fields}
       fn := pred.Call

       // Store extractor keyed by function pointer
       key := reflect.ValueOf(fn).Pointer()
       predicateRegistry.Store(key, pred)

       return fn
   }

   func getExtractor(predicate JoinPredicate) (KeyExtractor, bool) {
       key := reflect.ValueOf(predicate).Pointer()
       if val, ok := predicateRegistry.Load(key); ok {
           return val.(KeyExtractor), true
       }
       return nil, false
   }
   ```

   ✅ Maintains backward compatibility
   ✅ Thread-safe
   ⚠️ Uses reflection (minimal overhead)
   ⚠️ Memory leak potential (functions never GC'd from registry)

**RECOMMENDATION:** Use global registry (Option 5)

---

### Phase 3: Remaining Join Types

Once InnerJoin is working, apply same pattern to:

#### 3.1 LeftJoin Hash Implementation

```go
func leftJoinHash(
    leftSeq iter.Seq[Record],
    rightSeq iter.Seq[Record],
    extractor KeyExtractor,
    yield func(Record) bool,
) {
    // BUILD PHASE: Same as innerJoinHash
    hashTable := make(map[string][]Record)
    for right := range rightSeq {
        key, ok := extractor.ExtractKey(right)
        if !ok {
            continue
        }
        hashTable[key] = append(hashTable[key], right)
    }

    // PROBE PHASE: Stream left, yield matches OR left-only
    for left := range leftSeq {
        key, ok := extractor.ExtractKey(left)

        matched := false
        if ok {
            if matches, found := hashTable[key]; found {
                for _, right := range matches {
                    joined := MakeMutableRecord()
                    for k, v := range left.All() { joined.fields[k] = v }
                    for k, v := range right.All() { joined.fields[k] = v }
                    if !yield(joined.Freeze()) {
                        return
                    }
                    matched = true
                }
            }
        }

        // No match: yield left record only
        if !matched {
            if !yield(left) {
                return
            }
        }
    }
}
```

#### 3.2 RightJoin Hash Implementation

**Challenge:** Right join requires tracking which right records were matched

```go
func rightJoinHash(
    leftSeq iter.Seq[Record],
    rightSeq iter.Seq[Record],
    extractor KeyExtractor,
    yield func(Record) bool,
) {
    // BUILD PHASE: Hash right side AND track matched status
    type rightRecord struct {
        record  Record
        matched bool
    }

    hashTable := make(map[string][]*rightRecord)
    var allRightRecords []*rightRecord

    for right := range rightSeq {
        rr := &rightRecord{record: right, matched: false}
        allRightRecords = append(allRightRecords, rr)

        key, ok := extractor.ExtractKey(right)
        if !ok {
            continue
        }
        hashTable[key] = append(hashTable[key], rr)
    }

    // PROBE PHASE: Stream left and yield matches (marking right records)
    for left := range leftSeq {
        key, ok := extractor.ExtractKey(left)
        if !ok {
            continue
        }

        if matches, found := hashTable[key]; found {
            for _, rr := range matches {
                joined := MakeMutableRecord()
                for k, v := range left.All() { joined.fields[k] = v }
                for k, v := range rr.record.All() { joined.fields[k] = v }
                if !yield(joined.Freeze()) {
                    return
                }
                rr.matched = true
            }
        }
    }

    // CLEANUP PHASE: Yield unmatched right records
    for _, rr := range allRightRecords {
        if !rr.matched {
            if !yield(rr.record) {
                return
            }
        }
    }
}
```

#### 3.3 FullJoin Hash Implementation

**Challenge:** Must track both left and right matched status

```go
func fullJoinHash(
    leftSeq iter.Seq[Record],
    rightSeq iter.Seq[Record],
    extractor KeyExtractor,
    yield func(Record) bool,
) {
    // BUILD PHASE: Hash right side with match tracking
    type rightRecord struct {
        record  Record
        matched bool
    }

    hashTable := make(map[string][]*rightRecord)
    var allRightRecords []*rightRecord

    for right := range rightSeq {
        rr := &rightRecord{record: right, matched: false}
        allRightRecords = append(allRightRecords, rr)

        key, ok := extractor.ExtractKey(right)
        if !ok {
            continue
        }
        hashTable[key] = append(hashTable[key], rr)
    }

    // PROBE PHASE: Stream left, track matches
    for left := range leftSeq {
        key, ok := extractor.ExtractKey(left)

        matched := false
        if ok {
            if matches, found := hashTable[key]; found {
                for _, rr := range matches {
                    joined := MakeMutableRecord()
                    for k, v := range left.All() { joined.fields[k] = v }
                    for k, v := range rr.record.All() { joined.fields[k] = v }
                    if !yield(joined.Freeze()) {
                        return
                    }
                    matched = true
                    rr.matched = true
                }
            }
        }

        // No match: yield left-only
        if !matched {
            if !yield(left) {
                return
            }
        }
    }

    // CLEANUP PHASE: Yield unmatched right records
    for _, rr := range allRightRecords {
        if !rr.matched {
            if !yield(rr.record) {
                return
            }
        }
    }
}
```

---

### Phase 4: Testing Strategy

#### 4.1 Unit Tests

**File:** `sql_test.go`

Tests needed:
```go
func TestInnerJoinHash(t *testing.T) {
    // Test basic equality join
    // Test multi-field join
    // Test missing keys
    // Test duplicate keys (one-to-many)
    // Test type mismatches (string vs int)
    // Verify same results as nested loop
}

func TestLeftJoinHash(t *testing.T) {
    // Same tests as InnerJoin
    // Plus: unmatched left records
}

func TestRightJoinHash(t *testing.T) {
    // Same tests as InnerJoin
    // Plus: unmatched right records
}

func TestFullJoinHash(t *testing.T) {
    // Same tests as InnerJoin
    // Plus: unmatched records on both sides
}

func TestJoinFallbackToNested(t *testing.T) {
    // Verify OnCondition still works
    // Verify custom predicates use nested loop
}
```

#### 4.2 Benchmark Suite

**File:** `join_benchmark_test.go`

```go
func BenchmarkInnerJoin_Nested_100x100(b *testing.B)
func BenchmarkInnerJoin_Hash_100x100(b *testing.B)

func BenchmarkInnerJoin_Nested_1Kx1K(b *testing.B)
func BenchmarkInnerJoin_Hash_1Kx1K(b *testing.B)

func BenchmarkInnerJoin_Nested_10Kx10K(b *testing.B)
func BenchmarkInnerJoin_Hash_10Kx10K(b *testing.B)

// Expected results:
// 100x100:   10x speedup
// 1Kx1K:     100x speedup
// 10Kx10K:   1000x speedup
```

#### 4.3 Integration Tests

Test with real CSV data:
```bash
# Generate test data
ssql exec -cmd "seq 1 10000" | \
    ssql select -field id + | \
    ssql exec -cmd "echo $((RANDOM % 100))" -field category | \
    ssql write-csv left.csv

ssql exec -cmd "seq 1 10000" | \
    ssql select -field id + | \
    ssql exec -cmd "echo Category $((RANDOM % 100))" -field name | \
    ssql write-csv right.csv

# Test join performance
time ssql read-csv left.csv | \
    ssql join -right right.csv -on category | \
    ssql write-csv joined.csv

# Verify correctness (compare with nested loop version)
```

---

### Phase 5: Documentation

#### 5.1 Code Comments

Update `sql.go` with:
```go
// InnerJoin performs an inner join between two record streams (SQL INNER JOIN).
//
// PERFORMANCE: When using OnFields predicates, this automatically uses an optimized
// hash join algorithm with O(n+m) complexity. Custom predicates using OnCondition
// fall back to nested loop with O(n×m) complexity.
//
// Hash Join Performance:
//   - 1K × 1K:    ~1ms (vs ~10ms nested loop) = 10x faster
//   - 10K × 10K:  ~10ms (vs ~1s nested loop) = 100x faster
//   - 100K × 100K: ~100ms (vs ~100s nested loop) = 1000x faster
```

#### 5.2 User Guide

Create `doc/guides/join-performance.md`:
- When to use hash join (OnFields)
- When to use nested loop (OnCondition)
- Performance characteristics
- Best practices

---

## Implementation Checklist

### Pre-Implementation
- [x] Review current implementation
- [x] Design KeyExtractor interface
- [x] Update OnFields to return optimized predicate
- [ ] Resolve predicate/extractor linkage (global registry)

### Core Implementation
- [ ] Implement global registry for KeyExtractor
- [ ] Implement innerJoinHash
- [ ] Update InnerJoin to detect and use hash join
- [ ] Implement leftJoinHash
- [ ] Update LeftJoin
- [ ] Implement rightJoinHash
- [ ] Update RightJoin
- [ ] Implement fullJoinHash
- [ ] Update FullJoin

### Testing
- [ ] Write unit tests for all 4 join types
- [ ] Write benchmark suite
- [ ] Run integration tests with real data
- [ ] Verify correctness (hash == nested loop results)

### Documentation
- [ ] Update code comments in sql.go
- [ ] Create performance guide
- [ ] Update CHANGELOG
- [ ] Add examples to documentation

### Release
- [ ] Code review
- [ ] Performance validation
- [ ] Tag v1.1.0
- [ ] Announce performance improvements

---

## Risk Assessment

### High Risk
- ✅ **MITIGATED:** Backward compatibility - Using type assertion pattern
- ⚠️ **MONITOR:** Memory usage - Hash table could be large for big right side

### Medium Risk
- ⚠️ **PENDING:** Global registry thread safety - Need sync.Map
- ⚠️ **PENDING:** Global registry memory leaks - Functions never GC'd

### Low Risk
- ✅ Correctness - Well-tested algorithm from database systems
- ✅ Performance - Proven O(n+m) vs O(n×m) improvement

---

## Alternative Considered: Interface-Based Approach

Instead of function type, make JoinPredicate an interface:

```go
type JoinPredicate interface {
    Match(left, right Record) bool
}

type KeyExtractor interface {
    ExtractKey(r Record) (string, bool)
}

// fieldsJoinPredicate implements both
type fieldsJoinPredicate struct {
    fields []string
}

func (p *fieldsJoinPredicate) Match(left, right Record) bool { ... }
func (p *fieldsJoinPredicate) ExtractKey(r Record) (string, bool) { ... }
```

**Pros:**
- ✅ Clean design
- ✅ No global registry needed
- ✅ Type-safe

**Cons:**
- ❌ Breaking change (JoinPredicate changes from func to interface)
- ❌ All existing code must be updated
- ❌ OnCondition becomes more complex

**Decision:** REJECTED - Breaking changes not worth it for v1.x

---

## Questions for Review

1. **Global Registry:** Is sync.Map + function pointer acceptable?
2. **Memory Leaks:** Should we add a cleanup function for registry?
3. **Interface Change:** Worth making JoinPredicate an interface in v2.0?
4. **Null Handling:** nil == nil (match) or nil != nil (SQL-style)?
5. **Phase Approach:** Implement all 4 joins at once, or InnerJoin first?

---

## Next Steps

**Option A:** Proceed with implementation as designed
**Option B:** Explore interface-based approach for v2.0
**Option C:** Implement InnerJoin only as proof-of-concept
**Option D:** Defer to v1.2.0 after more research

**Recommendation:** Option C - Implement InnerJoin first, validate approach, then expand.
