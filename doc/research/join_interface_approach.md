# JOIN Interface Approach - Breaking Change Analysis

Reference: DFC010
Created: 2025-11-02
Last modified: 2025-11-08

[Back to Index](./README.md)

## Overview

Convert `JoinPredicate` from a function type to an interface to enable clean hash join optimization.

**Change Type:** Breaking (v2.0.0 or v1.0.0 if willing to break early)
**Complexity:** Low
**Benefits:** High - cleaner design, better extensibility
**Migration Effort:** Low - simple pattern replacement

---

## Current Design (Function Type)

```go
// Current: JoinPredicate is just a function
type JoinPredicate func(left, right Record) bool

// Usage
pred := ssql.OnFields("id")
joined := ssql.InnerJoin(orders, pred)(customers)
```

**Problems:**
- Can't attach methods to function types
- Can't detect if predicate supports optimization
- Requires global registry hack to associate metadata

---

## Proposed Design (Interface)

### Interface Definition

```go
// JoinPredicate defines how to match records for joining
type JoinPredicate interface {
    // Match returns true if left and right records should be joined
    Match(left, right Record) bool
}

// KeyExtractor is an optional interface that JoinPredicate can implement
// to enable O(n+m) hash join optimization instead of O(n×m) nested loop
type KeyExtractor interface {
    // ExtractKey returns the join key for a record
    // Returns (key, true) if successful, ("", false) if key missing
    ExtractKey(r Record) (string, bool)
}
```

### Built-in Implementations

#### 1. fieldsJoinPredicate (Hash Join Optimized)

```go
// fieldsJoinPredicate implements both JoinPredicate and KeyExtractor
type fieldsJoinPredicate struct {
    fields []string
}

func (p *fieldsJoinPredicate) Match(left, right Record) bool {
    for _, field := range p.fields {
        leftVal, leftExists := left.fields[field]
        rightVal, rightExists := right.fields[field]
        if !leftExists || !rightExists || leftVal != rightVal {
            return false
        }
    }
    return true
}

func (p *fieldsJoinPredicate) ExtractKey(r Record) (string, bool) {
    var parts []string
    for _, field := range p.fields {
        val, exists := r.fields[field]
        if !exists {
            return "", false
        }
        parts = append(parts, fmt.Sprintf("%v", val))
    }
    return strings.Join(parts, "\x00"), true
}

// OnFields creates an optimized equality-based join predicate
func OnFields(fields ...string) JoinPredicate {
    return &fieldsJoinPredicate{fields: fields}
}
```

#### 2. customJoinPredicate (Nested Loop Fallback)

```go
// customJoinPredicate wraps a custom function (nested loop only)
type customJoinPredicate struct {
    fn func(left, right Record) bool
}

func (p *customJoinPredicate) Match(left, right Record) bool {
    return p.fn(left, right)
}

// OnCondition creates a custom join predicate
func OnCondition(fn func(left, right Record) bool) JoinPredicate {
    return &customJoinPredicate{fn: fn}
}
```

### Join Implementation

```go
func InnerJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record] {
    return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
        return func(yield func(Record) bool) {
            // Check if predicate supports hash join
            if extractor, ok := predicate.(KeyExtractor); ok {
                // Use O(n+m) hash join
                innerJoinHash(leftSeq, rightSeq, predicate, extractor, yield)
                return
            }

            // Fallback to O(n×m) nested loop
            innerJoinNested(leftSeq, rightSeq, predicate, yield)
        }
    }
}

func innerJoinHash(
    leftSeq iter.Seq[Record],
    rightSeq iter.Seq[Record],
    predicate JoinPredicate,
    extractor KeyExtractor,
    yield func(Record) bool,
) {
    // BUILD PHASE
    hashTable := make(map[string][]Record)
    for right := range rightSeq {
        key, ok := extractor.ExtractKey(right)
        if !ok {
            continue
        }
        hashTable[key] = append(hashTable[key], right)
    }

    // PROBE PHASE
    for left := range leftSeq {
        key, ok := extractor.ExtractKey(left)
        if !ok {
            continue
        }

        if matches, found := hashTable[key]; found {
            for _, right := range matches {
                // Still verify with Match() in case of hash collisions
                if predicate.Match(left, right) {
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
}

func innerJoinNested(
    leftSeq iter.Seq[Record],
    rightSeq iter.Seq[Record],
    predicate JoinPredicate,
    yield func(Record) bool,
) {
    var rightRecords []Record
    for r := range rightSeq {
        rightRecords = append(rightRecords, r)
    }

    for left := range leftSeq {
        for _, right := range rightRecords {
            if predicate.Match(left, right) {
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

---

## Migration Guide

### Before (v0.x - Function Type)

```go
// OnFields - returns function
pred := ssql.OnFields("customer_id")
joined := ssql.InnerJoin(orders, pred)(customers)

// OnCondition - returns function
pred := ssql.OnCondition(func(left, right ssql.Record) bool {
    return ssql.GetOr(left, "amount", 0.0) > 100.0
})
```

### After (v1.0/v2.0 - Interface)

```go
// OnFields - returns interface (SAME USAGE!)
pred := ssql.OnFields("customer_id")
joined := ssql.InnerJoin(orders, pred)(customers)

// OnCondition - returns interface (SAME USAGE!)
pred := ssql.OnCondition(func(left, right ssql.Record) bool {
    return ssql.GetOr(left, "amount", 0.0) > 100.0
})
```

**Migration Required:** NONE for users of OnFields/OnCondition! ✅

**Breaking Change Only Affects:**
- Code that directly creates JoinPredicate functions
- Type assertions on JoinPredicate
- Tests that inspect JoinPredicate type

### Example: Custom Predicate Migration

**Before:**
```go
// Direct function - BREAKS
var customPred ssql.JoinPredicate = func(left, right ssql.Record) bool {
    return left["id"] == right["id"]
}
```

**After:**
```go
// Must use OnCondition wrapper
customPred := ssql.OnCondition(func(left, right ssql.Record) bool {
    return ssql.GetOr(left, "id", "") == ssql.GetOr(right, "id", "")
})
```

---

## Advantages of Interface Approach

### 1. **Clean Type Detection** ✅
```go
// Simple, idiomatic type assertion
if extractor, ok := predicate.(KeyExtractor); ok {
    // Use hash join
}
```
No reflection, no global registry, no function pointer magic.

### 2. **Extensibility** ✅
Users can create their own optimized predicates:

```go
type geoPredicate struct {
    radiusKm float64
}

func (p *geoPredicate) Match(left, right Record) bool {
    // Custom geo-distance logic
}

func (p *geoPredicate) ExtractKey(r Record) (string, bool) {
    // Grid-based spatial indexing
    lat := GetOr(r, "lat", 0.0)
    lon := GetOr(r, "lon", 0.0)
    gridKey := fmt.Sprintf("%d,%d", int(lat), int(lon))
    return gridKey, true
}
```

### 3. **Better Testing** ✅
```go
// Can create mock predicates
type mockPredicate struct {
    matchCount int
}

func (m *mockPredicate) Match(left, right Record) bool {
    m.matchCount++
    return true
}
```

### 4. **Future Optimizations** ✅
Can add more optional interfaces:

```go
// Parallel hash join
type ParallelHasher interface {
    Hash(r Record) uint64  // Better than string key
    Partition() int        // Number of hash partitions
}

// Index hints
type IndexHint interface {
    PreferIndex() bool
    IndexFields() []string
}
```

### 5. **No Hidden State** ✅
- No global registry to maintain
- No memory leak concerns
- No thread safety issues
- Clear, explicit code

---

## Disadvantages

### 1. **Breaking Change** ❌

**Impact:** Medium

**Who's Affected:**
- Anyone creating JoinPredicate directly (rare)
- Code doing `var pred JoinPredicate = func(...) {...}` (rare)
- Tests asserting on function type (internal only)

**Who's NOT Affected:**
- 99% of users who use `OnFields()` or `OnCondition()` ✅
- All CLI users ✅
- All examples in documentation ✅

### 2. **Slightly More Verbose** ⚠️

**Before (direct function):**
```go
pred := func(l, r Record) bool { return l["id"] == r["id"] }
```

**After (must wrap):**
```go
pred := OnCondition(func(l, r Record) bool {
    return GetOr(l, "id", "") == GetOr(r, "id", "")
})
```

**Counter:** This is actually BETTER - encourages proper field access!

### 3. **Interface Method Call Overhead** ⚠️

**Concern:** `predicate.Match(left, right)` vs `predicate(left, right)`

**Reality:** Negligible
- Method call overhead: ~1-2 ns
- Hash join saves milliseconds to seconds
- Nested loop is the bottleneck, not method calls

---

## Comparison: Function vs Interface

| Aspect | Function Type | Interface |
|--------|---------------|-----------|
| **Type Detection** | Requires global registry + reflection | Simple type assertion |
| **Extensibility** | Can't add methods | Users can implement custom predicates |
| **Thread Safety** | Global state = sync issues | Stateless = safe |
| **Memory Leaks** | Registry holds functions forever | No global state |
| **Testability** | Hard to mock | Easy to mock |
| **Performance** | Same | Same (negligible method call overhead) |
| **Breaking Change** | ✅ No | ❌ Yes |
| **Code Clarity** | ❌ Hidden magic | ✅ Explicit |
| **Future Proof** | ❌ Locked into function | ✅ Can add interfaces |

---

## Recommendation

### If v1.0.0 is NOT released yet:
**👍 STRONGLY RECOMMEND Interface Approach**

Reasons:
- ssql is still pre-1.0, breaking changes are acceptable
- Much cleaner, more maintainable code
- Better foundation for future optimizations
- 99% of users won't notice the change

### If v1.0.0 is already released:
**Consider for v2.0.0**

Options:
1. **Save for v2.0.0** - Do global registry hack for v1.1.0
2. **Do it now in v1.1.0** - Document as breaking change, provide migration guide
3. **Hybrid** - Support both for transition period:
   ```go
   // Accept both function and interface
   func InnerJoin(rightSeq iter.Seq[Record], predicate interface{}) Filter[Record, Record]
   ```

---

## Implementation Checklist

### Core Changes
- [ ] Change `type JoinPredicate` from func to interface
- [ ] Add `Match(left, right Record) bool` method
- [ ] Update `fieldsJoinPredicate` to implement interface
- [ ] Update `OnFields` to return `*fieldsJoinPredicate`
- [ ] Update `OnCondition` to wrap function in struct
- [ ] Update all 4 join functions to use `predicate.Match()`

### Testing
- [ ] Update all tests to use OnFields/OnCondition
- [ ] Remove any direct function assignments
- [ ] Add tests for custom predicates implementing KeyExtractor
- [ ] Verify hash join optimization works

### Documentation
- [ ] Update CHANGELOG with breaking change notice
- [ ] Add migration guide
- [ ] Update examples
- [ ] Document KeyExtractor interface for advanced users

### Migration Support
- [ ] Provide helper to convert old code: `AsCondition(fn)`?
- [ ] Add deprecation warnings in v0.9.x?
- [ ] Create migration script/tool?

---

## Example: Complete Before/After

### Before (Function Type)

```go
// sql.go
type JoinPredicate func(left, right Record) bool

func OnFields(fields ...string) JoinPredicate {
    return func(left, right Record) bool {
        for _, field := range fields {
            if left[field] != right[field] {
                return false
            }
        }
        return true
    }
}

func InnerJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record] {
    return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
        return func(yield func(Record) bool) {
            // Can't detect optimization opportunity!
            // Must use nested loop for everything
            var rightRecords []Record
            for r := range rightSeq { rightRecords = append(rightRecords, r) }

            for left := range leftSeq {
                for _, right := range rightRecords {
                    if predicate(left, right) {
                        yield(merge(left, right))
                    }
                }
            }
        }
    }
}
```

### After (Interface)

```go
// sql.go
type JoinPredicate interface {
    Match(left, right Record) bool
}

type KeyExtractor interface {
    ExtractKey(r Record) (string, bool)
}

type fieldsJoinPredicate struct {
    fields []string
}

func (p *fieldsJoinPredicate) Match(left, right Record) bool {
    for _, field := range p.fields {
        leftVal, leftExists := left.fields[field]
        rightVal, rightExists := right.fields[field]
        if !leftExists || !rightExists || leftVal != rightVal {
            return false
        }
    }
    return true
}

func (p *fieldsJoinPredicate) ExtractKey(r Record) (string, bool) {
    var parts []string
    for _, field := range p.fields {
        val, exists := r.fields[field]
        if !exists {
            return "", false
        }
        parts = append(parts, fmt.Sprintf("%v", val))
    }
    return strings.Join(parts, "\x00"), true
}

func OnFields(fields ...string) JoinPredicate {
    return &fieldsJoinPredicate{fields: fields}
}

func InnerJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record] {
    return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
        return func(yield func(Record) bool) {
            // Clean type detection!
            if extractor, ok := predicate.(KeyExtractor); ok {
                innerJoinHash(leftSeq, rightSeq, predicate, extractor, yield)
                return
            }

            innerJoinNested(leftSeq, rightSeq, predicate, yield)
        }
    }
}
```

---

## Decision Time

**Question:** Are you willing to make this breaking change?

**If YES:**
- Interface approach is superior in every way except compatibility
- Clean code, better performance, future-proof
- Most users won't be affected (OnFields/OnCondition usage is unchanged)

**If NO:**
- Stick with global registry approach (documented in join_implementation_plan.md)
- Works, but less elegant
- Can revisit for v2.0

**My Recommendation:**
- **If pre-v1.0:** Do interface approach NOW
- **If post-v1.0:** Save for v2.0, use registry for v1.1.0

What do you think?
