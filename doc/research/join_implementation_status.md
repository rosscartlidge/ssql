# Hash Join Implementation Status

## Completed ✅

### 1. Interface Conversion
- [x] Changed `JoinPredicate` from `func` to `interface`
- [x] Added `Match(left, right Record) bool` method
- [x] Created `fieldsJoinPredicate` struct
- [x] Created `customJoinPredicate` struct
- [x] Implemented `Match()` for both structs
- [x] Implemented `ExtractKey()` for `fieldsJoinPredicate`
- [x] Updated `OnFields()` to return `*fieldsJoinPredicate`
- [x] Updated `OnCondition()` to return `*customJoinPredicate`

**Files Modified:**
- `sql.go` (lines 18-297)

---

## Remaining Work

### 2. Update All Join Functions to Use `predicate.Match()`

Currently all joins call `predicate(left, right)` which won't work with interface.
Need to change to `predicate.Match(left, right)`.

**Files:** `sql.go`

**Functions to Update:**
1. `InnerJoin` - line ~58
2. `LeftJoin` - line ~87
3. `RightJoin` - line ~146
4. `FullJoin` - line ~193

**Example Change:**
```go
// Before
if predicate(left, right) {
    ...
}

// After
if predicate.Match(left, right) {
    ...
}
```

**Locations (approximate line numbers):**
- InnerJoin: line 73 (`for r := range rightSeq`) +  line 77 (`if predicate(left, right)`)
- LeftJoin: line 87 + line 102
- RightJoin: line 146
- FullJoin: line 193

### 3. Implement Hash Join Functions

Add 4 new helper functions:

#### 3.1 `innerJoinHash`
```go
// innerJoinHash performs O(n+m) hash-based inner join
func innerJoinHash(
    leftSeq iter.Seq[Record],
    rightSeq iter.Seq[Record],
    predicate JoinPredicate,
    extractor KeyExtractor,
    yield func(Record) bool,
) {
    // BUILD PHASE: Hash right side
    hashTable := make(map[string][]Record)
    for right := range rightSeq {
        key, ok := extractor.ExtractKey(right)
        if !ok {
            continue
        }
        hashTable[key] = append(hashTable[key], right)
    }

    // PROBE PHASE: Stream left and lookup
    for left := range leftSeq {
        key, ok := extractor.ExtractKey(left)
        if !ok {
            continue
        }

        if matches, found := hashTable[key]; found {
            for _, right := range matches {
                // Verify with Match() for correctness
                if predicate.Match(left, right) {
                    joined := MakeMutableRecord()
                    for k, v := range left.All() {
                        joined.fields[k] = v
                    }
                    for k, v := range right.All() {
                        joined.fields[k] = v
                    }
                    if !yield(joined.Freeze()) {
                        return
                    }
                }
            }
        }
    }
}
```

#### 3.2 `leftJoinHash`
Similar to `innerJoinHash` but track if left matched, yield unmatched left records.

#### 3.3 `rightJoinHash`
Track which right records matched, yield unmatched right records at end.

#### 3.4 `fullJoinHash`
Track both left and right matches, yield unmatched from both sides.

### 4. Update Join Functions to Detect and Dispatch

Each join function needs:

```go
func InnerJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record] {
    return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
        return func(yield func(Record) bool) {
            // Check if predicate supports hash join
            if extractor, ok := predicate.(KeyExtractor); ok {
                innerJoinHash(leftSeq, rightSeq, predicate, extractor, yield)
                return
            }

            // Fallback to nested loop
            innerJoinNested(leftSeq, rightSeq, predicate, yield)
        }
    }
}
```

Need to:
1. Extract current nested loop code into `innerJoinNested` helper
2. Add dispatch logic
3. Repeat for Left/Right/Full joins

### 5. Update Tests

**File:** `sql_test.go`

Search for `JoinPredicate` usage and update:

```bash
# Find test failures
go test ./... -v | grep -i join
```

Common patterns to fix:
```go
// Before - won't compile
var pred JoinPredicate = func(l, r Record) bool { return true }

// After - use OnCondition
pred := OnCondition(func(l, r Record) bool { return true })
```

### 6. Create Benchmarks

**File:** `join_benchmark_test.go` (new file)

```go
package ssql

import (
	"slices"
	"testing"
)

func BenchmarkInnerJoin_Nested_100x100(b *testing.B) {
	left := generateRecords(100, "id")
	right := generateRecords(100, "id")

	pred := OnCondition(func(l, r Record) bool {
		return GetOr(l, "id", int64(0)) == GetOr(r, "id", int64(0))
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := InnerJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

func BenchmarkInnerJoin_Hash_100x100(b *testing.B) {
	left := generateRecords(100, "id")
	right := generateRecords(100, "id")

	pred := OnFields("id") // Uses hash join

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leftSeq := slices.Values(left)
		rightSeq := slices.Values(right)
		joined := InnerJoin(rightSeq, pred)(leftSeq)
		for range joined {
		}
	}
}

// Repeat for 1Kx1K, 10Kx10K

func generateRecords(n int, keyField string) []Record {
	var records []Record
	for i := 0; i < n; i++ {
		r := MakeMutableRecord()
		r.fields[keyField] = int64(i % (n / 10)) // Create duplicates
		r.fields["value"] = int64(i)
		records = append(records, r.Freeze())
	}
	return records
}
```

### 7. Update Documentation

#### 7.1 CHANGELOG.md
```markdown
## [v1.1.0] - YYYY-MM-DD

### Breaking Changes
- **JOIN API Change**: `JoinPredicate` changed from function type to interface
  - Migration: Use `OnCondition()` to wrap custom join functions
  - `OnFields()` and `OnCondition()` usage remains unchanged
  - See migration guide in docs

### Performance Improvements
- **Hash Join Optimization**: 100-1000x faster joins with `OnFields()`
  - `OnFields()` now uses O(n+m) hash join instead of O(n×m) nested loop
  - Custom predicates via `OnCondition()` still use nested loop
  - Automatic optimization - no code changes needed for `OnFields()` users

### New Features
- Added `KeyExtractor` interface for custom optimized join predicates
```

#### 7.2 Migration Guide
Create `doc/guides/v1.1-migration.md` with examples

#### 7.3 Update sql.go Comments
Add performance notes to InnerJoin/LeftJoin/RightJoin/FullJoin

---

## Implementation Approach

### Option A: All at Once
Implement everything in one go:
1. Update all 4 join functions (predicate() -> predicate.Match())
2. Add all 4 hash join implementations
3. Add dispatch logic to all 4 joins
4. Fix all tests
5. Add benchmarks
6. Update docs

**Time:** 2-3 hours
**Risk:** High (many changes at once)

### Option B: Incremental (Recommended)
1. **Step 1:** Update join functions to use predicate.Match() - TEST
2. **Step 2:** Implement innerJoinHash only - TEST
3. **Step 3:** Add dispatch to InnerJoin - BENCHMARK
4. **Step 4:** Repeat for Left/Right/Full joins
5. **Step 5:** Update docs

**Time:** 3-4 hours (with testing between steps)
**Risk:** Low (validate each step)

---

## Testing Strategy

### Unit Tests
```bash
# Run all tests
go test ./... -v

# Run join tests only
go test ./... -v -run Join

# Run with race detector
go test ./... -race
```

### Benchmarks
```bash
# Run all benchmarks
go test -bench=. -benchmem

# Compare nested vs hash
go test -bench=InnerJoin -benchmem

# Expected results:
# 100x100:   Hash ~10x faster
# 1Kx1K:     Hash ~100x faster
# 10Kx10K:   Hash ~1000x faster
```

### Integration Test
```bash
# Create test data
ssql exec -cmd "seq 1 1000" | ssql write-csv left.csv
ssql exec -cmd "seq 1 1000" | ssql write-csv right.csv

# Test join (should be fast with hash join)
time ssql read-csv left.csv | \
  ssql join -right right.csv -on id | \
  ssql write-csv output.csv
```

---

## Next Steps

**Recommended:** Option B (Incremental)

**Step 1 - Update predicate calls:** (15 min)
- Change `predicate(left, right)` to `predicate.Match(left, right)` in all 4 join functions
- Run tests: `go test ./... -v`
- Fix compilation errors

**Step 2 - Fix tests:** (30 min)
- Update tests to use OnCondition() wrapper
- Verify all tests pass

**Step 3 - Implement InnerJoin hash:** (45 min)
- Add innerJoinHash function
- Add innerJoinNested helper
- Add dispatch logic to InnerJoin
- Test and benchmark

**Step 4 - Expand to other joins:** (60 min)
- Implement leftJoinHash
- Implement rightJoinHash
- Implement fullJoinHash
- Update Left/Right/FullJoin functions
- Test each one

**Step 5 - Polish:** (30 min)
- Add comprehensive benchmarks
- Update documentation
- Update CHANGELOG

**Total Time:** ~3 hours

Ready to proceed with Step 1?
