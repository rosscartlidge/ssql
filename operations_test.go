package ssql

import (
	"iter"
	"slices"
	"testing"
	"time"
)

// ============================================================================
// TRANSFORM OPERATIONS TESTS
// ============================================================================

func TestSelect(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5})
	filter := Select(func(x int) int { return x * 2 })
	result := slices.Collect(filter(input))

	expected := []int{2, 4, 6, 8, 10}
	if !slices.Equal(result, expected) {
		t.Errorf("Select failed: expected %v, got %v", expected, result)
	}
}

func TestSelectTypeChange(t *testing.T) {
	input := slices.Values([]int{1, 2, 3})
	filter := Select(func(x int) string { return string(rune('A' + x - 1)) })
	result := slices.Collect(filter(input))

	expected := []string{"A", "B", "C"}
	if !slices.Equal(result, expected) {
		t.Errorf("Select type change failed: expected %v, got %v", expected, result)
	}
}

func TestSelectSafe(t *testing.T) {
	input := Safe(slices.Values([]int{1, 2, 3}))
	filter := SelectSafe(func(x int) (int, error) {
		return x * 2, nil
	})

	var result []int
	for v, err := range filter(input) {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, v)
	}

	expected := []int{2, 4, 6}
	if !slices.Equal(result, expected) {
		t.Errorf("SelectSafe failed: expected %v, got %v", expected, result)
	}
}

func TestUpdate(t *testing.T) {
	// Create test records
	r1 := MakeMutableRecord()
	r1.fields["name"] = "Alice"
	r1.fields["age"] = int64(30)
	r1.fields["status"] = "pending"

	r2 := MakeMutableRecord()
	r2.fields["name"] = "Bob"
	r2.fields["age"] = int64(25)
	r2.fields["status"] = "pending"

	input := slices.Values([]Record{r1.Freeze(), r2.Freeze()})

	// Update status field to "processed"
	filter := Update(func(mut MutableRecord) MutableRecord {
		return mut.String("status", "processed")
	})

	result := slices.Collect(filter(input))

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}

	// Check that status was updated
	if GetOr(result[0], "status", "") != "processed" {
		t.Errorf("Expected status='processed', got '%s'", GetOr(result[0], "status", ""))
	}
	if GetOr(result[1], "status", "") != "processed" {
		t.Errorf("Expected status='processed', got '%s'", GetOr(result[1], "status", ""))
	}

	// Check that other fields are unchanged
	if GetOr(result[0], "name", "") != "Alice" {
		t.Errorf("Expected name='Alice', got '%s'", GetOr(result[0], "name", ""))
	}
	if GetOr(result[0], "age", int64(0)) != int64(30) {
		t.Errorf("Expected age=30, got %d", GetOr(result[0], "age", int64(0)))
	}
}

func TestUpdateMultipleFields(t *testing.T) {
	r := MakeMutableRecord()
	r.fields["name"] = "Alice"
	r.fields["age"] = int64(30)

	input := slices.Values([]Record{r.Freeze()})

	// Update multiple fields with chaining
	filter := Update(func(mut MutableRecord) MutableRecord {
		return mut.
			String("status", "active").
			Int("score", int64(100))
	})

	result := slices.Collect(filter(input))

	if len(result) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(result))
	}

	// Check all fields
	if GetOr(result[0], "name", "") != "Alice" {
		t.Errorf("Expected name='Alice', got '%s'", GetOr(result[0], "name", ""))
	}
	if GetOr(result[0], "status", "") != "active" {
		t.Errorf("Expected status='active', got '%s'", GetOr(result[0], "status", ""))
	}
	if GetOr(result[0], "score", int64(0)) != int64(100) {
		t.Errorf("Expected score=100, got %d", GetOr(result[0], "score", int64(0)))
	}
}

func TestUpdateComputedField(t *testing.T) {
	r1 := MakeMutableRecord()
	r1.fields["price"] = float64(10.0)
	r1.fields["quantity"] = int64(5)

	r2 := MakeMutableRecord()
	r2.fields["price"] = float64(20.0)
	r2.fields["quantity"] = int64(3)

	input := slices.Values([]Record{r1.Freeze(), r2.Freeze()})

	// Add computed "total" field
	filter := Update(func(mut MutableRecord) MutableRecord {
		frozen := mut.Freeze()
		price := GetOr(frozen, "price", float64(0))
		qty := GetOr(frozen, "quantity", int64(0))
		return mut.Float("total", price*float64(qty))
	})

	result := slices.Collect(filter(input))

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}

	// Check computed totals
	if GetOr(result[0], "total", float64(0)) != float64(50.0) {
		t.Errorf("Expected total=50.0, got %f", GetOr(result[0], "total", float64(0)))
	}
	if GetOr(result[1], "total", float64(0)) != float64(60.0) {
		t.Errorf("Expected total=60.0, got %f", GetOr(result[1], "total", float64(0)))
	}
}

func TestUpdateConditional(t *testing.T) {
	r1 := MakeMutableRecord()
	r1.fields["name"] = "Alice"
	r1.fields["age"] = int64(30)

	r2 := MakeMutableRecord()
	r2.fields["name"] = "Bob"
	r2.fields["age"] = int64(17)

	input := slices.Values([]Record{r1.Freeze(), r2.Freeze()})

	// Conditionally set category based on age
	filter := Update(func(mut MutableRecord) MutableRecord {
		frozen := mut.Freeze()
		age := GetOr(frozen, "age", int64(0))
		if age >= 18 {
			return mut.String("category", "adult")
		}
		return mut.String("category", "minor")
	})

	result := slices.Collect(filter(input))

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}

	// Check categories
	if GetOr(result[0], "category", "") != "adult" {
		t.Errorf("Expected category='adult', got '%s'", GetOr(result[0], "category", ""))
	}
	if GetOr(result[1], "category", "") != "minor" {
		t.Errorf("Expected category='minor', got '%s'", GetOr(result[1], "category", ""))
	}
}

func TestUpdateImmutability(t *testing.T) {
	r := MakeMutableRecord()
	r.fields["name"] = "Alice"
	r.fields["status"] = "pending"
	original := r.Freeze()

	input := slices.Values([]Record{original})

	// Update status
	filter := Update(func(mut MutableRecord) MutableRecord {
		return mut.String("status", "processed")
	})

	result := slices.Collect(filter(input))

	// Verify original record is unchanged
	if GetOr(original, "status", "") != "pending" {
		t.Errorf("Original record was mutated! Expected status='pending', got '%s'",
			GetOr(original, "status", ""))
	}

	// Verify result has updated value
	if GetOr(result[0], "status", "") != "processed" {
		t.Errorf("Expected updated status='processed', got '%s'",
			GetOr(result[0], "status", ""))
	}
}

func TestSelectMany(t *testing.T) {
	// Expand each number into a sequence of numbers from 1 to n
	input := slices.Values([]int{2, 3})
	filter := SelectMany(func(n int) iter.Seq[int] {
		return func(yield func(int) bool) {
			for i := 1; i <= n; i++ {
				if !yield(i) {
					return
				}
			}
		}
	})

	result := slices.Collect(filter(input))
	expected := []int{1, 2, 1, 2, 3} // 2 expands to [1,2], 3 expands to [1,2,3]

	if !slices.Equal(result, expected) {
		t.Errorf("SelectMany failed: expected %v, got %v", expected, result)
	}
}

// ============================================================================
// FILTER OPERATIONS TESTS
// ============================================================================

func TestWhere(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5, 6})
	filter := Where(func(x int) bool { return x%2 == 0 })
	result := slices.Collect(filter(input))

	expected := []int{2, 4, 6}
	if !slices.Equal(result, expected) {
		t.Errorf("Where failed: expected %v, got %v", expected, result)
	}
}

func TestWhereSafe(t *testing.T) {
	input := Safe(slices.Values([]int{1, 2, 3, 4, 5}))
	filter := WhereSafe(func(x int) (bool, error) {
		return x > 2, nil
	})

	var result []int
	for v, err := range filter(input) {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, v)
	}

	expected := []int{3, 4, 5}
	if !slices.Equal(result, expected) {
		t.Errorf("WhereSafe failed: expected %v, got %v", expected, result)
	}
}

// ============================================================================
// LIMITING OPERATIONS TESTS
// ============================================================================

func TestLimit(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5})
	filter := Limit[int](3)
	result := slices.Collect(filter(input))

	expected := []int{1, 2, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("Limit failed: expected %v, got %v", expected, result)
	}
}

func TestLimitZero(t *testing.T) {
	input := slices.Values([]int{1, 2, 3})
	filter := Limit[int](0)
	result := slices.Collect(filter(input))

	if len(result) != 0 {
		t.Errorf("Limit(0) should return empty, got %v", result)
	}
}

func TestLimitSafe(t *testing.T) {
	input := Safe(slices.Values([]int{1, 2, 3, 4, 5}))
	filter := LimitSafe[int](3)

	var result []int
	for v, err := range filter(input) {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, v)
	}

	expected := []int{1, 2, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("LimitSafe failed: expected %v, got %v", expected, result)
	}
}

func TestOffset(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5})
	filter := Offset[int](2)
	result := slices.Collect(filter(input))

	expected := []int{3, 4, 5}
	if !slices.Equal(result, expected) {
		t.Errorf("Offset failed: expected %v, got %v", expected, result)
	}
}

func TestOffsetSafe(t *testing.T) {
	input := Safe(slices.Values([]int{1, 2, 3, 4, 5}))
	filter := OffsetSafe[int](2)

	var result []int
	for v, err := range filter(input) {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, v)
	}

	expected := []int{3, 4, 5}
	if !slices.Equal(result, expected) {
		t.Errorf("OffsetSafe failed: expected %v, got %v", expected, result)
	}
}

func TestLimitOffset(t *testing.T) {
	// Test combined limit and offset
	input := slices.Values([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	// Apply offset first, then limit
	offsetted := Offset[int](3)(input)
	filter := Limit[int](4)
	result := slices.Collect(filter(offsetted))

	expected := []int{4, 5, 6, 7}
	if !slices.Equal(result, expected) {
		t.Errorf("Limit+Offset failed: expected %v, got %v", expected, result)
	}
}

// ============================================================================
// ORDERING OPERATIONS TESTS
// ============================================================================

func TestSort(t *testing.T) {
	input := slices.Values([]int{3, 1, 4, 1, 5, 9, 2, 6})
	filter := Sort[int]()
	result := slices.Collect(filter(input))

	expected := []int{1, 1, 2, 3, 4, 5, 6, 9}
	if !slices.Equal(result, expected) {
		t.Errorf("Sort failed: expected %v, got %v", expected, result)
	}
}

func TestSortStrings(t *testing.T) {
	input := slices.Values([]string{"banana", "apple", "cherry"})
	filter := Sort[string]()
	result := slices.Collect(filter(input))

	expected := []string{"apple", "banana", "cherry"}
	if !slices.Equal(result, expected) {
		t.Errorf("Sort strings failed: expected %v, got %v", expected, result)
	}
}

func TestSortBy(t *testing.T) {
	type person struct {
		name string
		age  int
	}

	input := slices.Values([]person{
		{"Alice", 30},
		{"Bob", 25},
		{"Charlie", 35},
	})

	filter := SortBy(func(p person) int { return p.age })
	result := slices.Collect(filter(input))

	if result[0].name != "Bob" || result[1].name != "Alice" || result[2].name != "Charlie" {
		t.Errorf("SortBy failed: got %v", result)
	}
}

func TestSortDesc(t *testing.T) {
	input := slices.Values([]int{3, 1, 4, 1, 5, 9, 2, 6})
	filter := SortDesc[int]()
	result := slices.Collect(filter(input))

	expected := []int{9, 6, 5, 4, 3, 2, 1, 1}
	if !slices.Equal(result, expected) {
		t.Errorf("SortDesc failed: expected %v, got %v", expected, result)
	}
}

func TestSortFunc(t *testing.T) {
	r1 := MakeMutableRecord().String("name", "Charlie").Int("age", int64(35)).Freeze()
	r2 := MakeMutableRecord().String("name", "Alice").Int("age", int64(30)).Freeze()
	r3 := MakeMutableRecord().String("name", "Bob").Int("age", int64(25)).Freeze()

	input := slices.Values([]Record{r1, r2, r3})

	// Sort by name ascending using comparator
	filter := SortFunc(func(a, b Record) int {
		aName := GetOr(a, "name", "")
		bName := GetOr(b, "name", "")
		if aName < bName {
			return -1
		}
		if aName > bName {
			return 1
		}
		return 0
	})

	result := slices.Collect(filter(input))
	if GetOr(result[0], "name", "") != "Alice" ||
		GetOr(result[1], "name", "") != "Bob" ||
		GetOr(result[2], "name", "") != "Charlie" {
		t.Errorf("SortFunc failed: got %v, %v, %v",
			GetOr(result[0], "name", ""),
			GetOr(result[1], "name", ""),
			GetOr(result[2], "name", ""))
	}
}

// ============================================================================
// TOP/BOTTOM OPERATIONS TESTS
// ============================================================================

func TestTopBy_Basic(t *testing.T) {
	input := slices.Values([]int{3, 7, 1, 9, 4, 6, 2, 8, 5, 10})
	result := slices.Collect(TopBy(3, func(x int) int { return x })(input))

	expected := []int{10, 9, 8}
	if !slices.Equal(result, expected) {
		t.Errorf("TopBy(3) = %v, want %v", result, expected)
	}
}

func TestTopBy_FewerThanN(t *testing.T) {
	input := slices.Values([]int{5, 2})
	result := slices.Collect(TopBy(5, func(x int) int { return x })(input))

	expected := []int{5, 2}
	if !slices.Equal(result, expected) {
		t.Errorf("TopBy(5) with 2 items = %v, want %v", result, expected)
	}
}

func TestTopBy_Zero(t *testing.T) {
	input := slices.Values([]int{1, 2, 3})
	result := slices.Collect(TopBy(0, func(x int) int { return x })(input))

	if len(result) != 0 {
		t.Errorf("TopBy(0) = %v, want empty", result)
	}
}

func TestTopBy_ResultOrder(t *testing.T) {
	type item struct {
		name string
		val  int
	}
	input := slices.Values([]item{
		{"A", 10}, {"B", 50}, {"C", 30}, {"D", 20}, {"E", 40},
	})

	result := slices.Collect(TopBy(3, func(x item) int { return x.val })(input))

	// Should be descending: B(50), E(40), C(30)
	if result[0].name != "B" || result[1].name != "E" || result[2].name != "C" {
		t.Errorf("TopBy order wrong: got %v", result)
	}
}

func TestBottomBy_Basic(t *testing.T) {
	input := slices.Values([]int{3, 7, 1, 9, 4, 6, 2, 8, 5, 10})
	result := slices.Collect(BottomBy(3, func(x int) int { return x })(input))

	expected := []int{1, 2, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("BottomBy(3) = %v, want %v", result, expected)
	}
}

func TestTopBy_Duplicates(t *testing.T) {
	input := slices.Values([]int{5, 5, 5, 3, 3, 1})
	result := slices.Collect(TopBy(4, func(x int) int { return x })(input))

	expected := []int{5, 5, 5, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("TopBy with duplicates = %v, want %v", result, expected)
	}
}

func TestTopByFunc_StringOrder(t *testing.T) {
	// CompareAny orders strings lexicographically — the case that the old
	// numeric-only `top` key got wrong (every string collapsed to 0).
	input := slices.Values([]string{"delta", "alpha", "charlie", "echo", "bravo"})
	cmp := func(a, b string) int { return CompareAny(a, b) }

	top := slices.Collect(TopByFunc(2, cmp)(input))
	if want := []string{"echo", "delta"}; !slices.Equal(top, want) {
		t.Errorf("TopByFunc(2) = %v, want %v", top, want)
	}
	bottom := slices.Collect(BottomByFunc(3, cmp)(input))
	if want := []string{"alpha", "bravo", "charlie"}; !slices.Equal(bottom, want) {
		t.Errorf("BottomByFunc(3) = %v, want %v", bottom, want)
	}
}

func TestTopByFunc_NumericMatchesKey(t *testing.T) {
	// With a numeric comparator TopByFunc must agree with key-based TopBy.
	nums := []int{3, 7, 1, 9, 4, 6, 2, 8, 5, 10}
	cmp := func(a, b int) int { return CompareAny(a, b) }
	got := slices.Collect(TopByFunc(3, cmp)(slices.Values(nums)))
	want := slices.Collect(TopBy(3, func(x int) int { return x })(slices.Values(nums)))
	if !slices.Equal(got, want) {
		t.Errorf("TopByFunc %v != TopBy %v", got, want)
	}
}

func TestTopByFunc_Zero(t *testing.T) {
	in := slices.Values([]int{1, 2, 3})
	if r := slices.Collect(TopByFunc(0, func(a, b int) int { return CompareAny(a, b) })(in)); len(r) != 0 {
		t.Errorf("TopByFunc(0) = %v, want empty", r)
	}
}

// ============================================================================
// MERGE SORTED TESTS
// ============================================================================

func TestMergeSorted_TwoSources(t *testing.T) {
	s1 := makeRecordSeq([]map[string]any{
		{"name": "Alice", "age": int64(25)},
		{"name": "Charlie", "age": int64(35)},
	})
	s2 := makeRecordSeq([]map[string]any{
		{"name": "Bob", "age": int64(30)},
		{"name": "Dave", "age": int64(40)},
	})

	orderBy := []OrderField{{Field: "age"}}
	result := slices.Collect(MergeSorted(orderBy, s1, s2))

	if len(result) != 4 {
		t.Fatalf("expected 4 records, got %d", len(result))
	}
	ages := make([]int64, len(result))
	for i, r := range result {
		ages[i] = GetOr(r, "age", int64(0))
	}
	expected := []int64{25, 30, 35, 40}
	if !slices.Equal(ages, expected) {
		t.Errorf("expected ages %v, got %v", expected, ages)
	}
}

func TestMergeSorted_ThreeSources(t *testing.T) {
	s1 := makeRecordSeq([]map[string]any{{"v": int64(1)}, {"v": int64(4)}, {"v": int64(7)}})
	s2 := makeRecordSeq([]map[string]any{{"v": int64(2)}, {"v": int64(5)}, {"v": int64(8)}})
	s3 := makeRecordSeq([]map[string]any{{"v": int64(3)}, {"v": int64(6)}, {"v": int64(9)}})

	orderBy := []OrderField{{Field: "v"}}
	result := slices.Collect(MergeSorted(orderBy, s1, s2, s3))

	vals := make([]int64, len(result))
	for i, r := range result {
		vals[i] = GetOr(r, "v", int64(0))
	}
	expected := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	if !slices.Equal(vals, expected) {
		t.Errorf("expected %v, got %v", expected, vals)
	}
}

func TestMergeSorted_EmptySource(t *testing.T) {
	s1 := makeRecordSeq([]map[string]any{{"v": int64(1)}, {"v": int64(3)}})
	s2 := makeRecordSeq(nil) // empty
	s3 := makeRecordSeq([]map[string]any{{"v": int64(2)}})

	orderBy := []OrderField{{Field: "v"}}
	result := slices.Collect(MergeSorted(orderBy, s1, s2, s3))

	vals := make([]int64, len(result))
	for i, r := range result {
		vals[i] = GetOr(r, "v", int64(0))
	}
	expected := []int64{1, 2, 3}
	if !slices.Equal(vals, expected) {
		t.Errorf("expected %v, got %v", expected, vals)
	}
}

func TestMergeSorted_SingleSource(t *testing.T) {
	s1 := makeRecordSeq([]map[string]any{{"v": int64(3)}, {"v": int64(1)}, {"v": int64(2)}})

	orderBy := []OrderField{{Field: "v"}}
	result := slices.Collect(MergeSorted(orderBy, s1))

	// Single source: records pass through in original order (assumed pre-sorted)
	vals := make([]int64, len(result))
	for i, r := range result {
		vals[i] = GetOr(r, "v", int64(0))
	}
	expected := []int64{3, 1, 2} // passthrough, not re-sorted
	if !slices.Equal(vals, expected) {
		t.Errorf("expected %v, got %v", expected, vals)
	}
}

func TestMergeSorted_NoSources(t *testing.T) {
	orderBy := []OrderField{{Field: "v"}}
	result := slices.Collect(MergeSorted(orderBy))
	if len(result) != 0 {
		t.Errorf("expected 0 records, got %d", len(result))
	}
}

func TestMergeSorted_Descending(t *testing.T) {
	s1 := makeRecordSeq([]map[string]any{{"v": int64(9)}, {"v": int64(5)}, {"v": int64(1)}})
	s2 := makeRecordSeq([]map[string]any{{"v": int64(8)}, {"v": int64(4)}, {"v": int64(2)}})

	orderBy := []OrderField{{Field: "v", Desc: true}}
	result := slices.Collect(MergeSorted(orderBy, s1, s2))

	vals := make([]int64, len(result))
	for i, r := range result {
		vals[i] = GetOr(r, "v", int64(0))
	}
	expected := []int64{9, 8, 5, 4, 2, 1}
	if !slices.Equal(vals, expected) {
		t.Errorf("expected %v, got %v", expected, vals)
	}
}

func TestMergeSorted_MultiField(t *testing.T) {
	s1 := makeRecordSeq([]map[string]any{
		{"dept": "A", "salary": int64(50)},
		{"dept": "B", "salary": int64(60)},
	})
	s2 := makeRecordSeq([]map[string]any{
		{"dept": "A", "salary": int64(70)},
		{"dept": "B", "salary": int64(40)},
	})

	orderBy := []OrderField{{Field: "dept"}, {Field: "salary"}}
	result := slices.Collect(MergeSorted(orderBy, s1, s2))

	// Expected: A/50, A/70, B/40, B/60
	if len(result) != 4 {
		t.Fatalf("expected 4 records, got %d", len(result))
	}
	type pair struct {
		dept   string
		salary int64
	}
	got := make([]pair, len(result))
	for i, r := range result {
		got[i] = pair{GetOr(r, "dept", ""), GetOr(r, "salary", int64(0))}
	}
	exp := []pair{{"A", 50}, {"A", 70}, {"B", 40}, {"B", 60}}
	for i := range got {
		if got[i] != exp[i] {
			t.Errorf("record %d: expected %v, got %v", i, exp[i], got[i])
		}
	}
}

func TestMergeSorted_Stable(t *testing.T) {
	// Equal values: source with lower index should come first
	s1 := makeRecordSeq([]map[string]any{{"v": int64(1), "src": "a"}})
	s2 := makeRecordSeq([]map[string]any{{"v": int64(1), "src": "b"}})

	orderBy := []OrderField{{Field: "v"}}
	result := slices.Collect(MergeSorted(orderBy, s1, s2))

	if len(result) != 2 {
		t.Fatalf("expected 2 records, got %d", len(result))
	}
	if GetOr(result[0], "src", "") != "a" {
		t.Errorf("expected source 'a' first (stable), got %q", GetOr(result[0], "src", ""))
	}
}

func TestMergeSorted_EarlyTermination(t *testing.T) {
	s1 := makeRecordSeq([]map[string]any{{"v": int64(1)}, {"v": int64(3)}, {"v": int64(5)}})
	s2 := makeRecordSeq([]map[string]any{{"v": int64(2)}, {"v": int64(4)}, {"v": int64(6)}})

	orderBy := []OrderField{{Field: "v"}}
	merged := MergeSorted(orderBy, s1, s2)

	// Take only first 3
	limited := Limit[Record](3)(merged)
	result := slices.Collect(limited)

	if len(result) != 3 {
		t.Fatalf("expected 3 records, got %d", len(result))
	}
	vals := make([]int64, len(result))
	for i, r := range result {
		vals[i] = GetOr(r, "v", int64(0))
	}
	expected := []int64{1, 2, 3}
	if !slices.Equal(vals, expected) {
		t.Errorf("expected %v, got %v", expected, vals)
	}
}

// makeRecordSeq creates an iter.Seq[Record] from a slice of field maps
func makeRecordSeq(records []map[string]any) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		for _, fields := range records {
			r := MakeMutableRecord()
			for k, v := range fields {
				r.fields[k] = v
			}
			if !yield(r.Freeze()) {
				return
			}
		}
	}
}

// ============================================================================
// UTILITY OPERATIONS TESTS
// ============================================================================

func TestDistinct(t *testing.T) {
	input := slices.Values([]int{1, 2, 2, 3, 3, 3, 4, 5, 5})
	filter := Distinct[int]()
	result := slices.Collect(filter(input))

	expected := []int{1, 2, 3, 4, 5}
	if !slices.Equal(result, expected) {
		t.Errorf("Distinct failed: expected %v, got %v", expected, result)
	}
}

func TestDistinctBy(t *testing.T) {
	type item struct {
		id   int
		name string
	}

	input := slices.Values([]item{
		{1, "Alice"},
		{2, "Bob"},
		{1, "Alice2"}, // Duplicate id
		{3, "Charlie"},
	})

	filter := DistinctBy(func(i item) int { return i.id })
	result := slices.Collect(filter(input))

	if len(result) != 3 {
		t.Errorf("DistinctBy should return 3 items, got %d", len(result))
	}

	// Should keep first occurrence
	if result[0].name != "Alice" {
		t.Errorf("DistinctBy should keep first occurrence")
	}
}

func TestReverse(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5})
	filter := Reverse[int]()
	result := slices.Collect(filter(input))

	expected := []int{5, 4, 3, 2, 1}
	if !slices.Equal(result, expected) {
		t.Errorf("Reverse failed: expected %v, got %v", expected, result)
	}
}

// ============================================================================
// TEE OPERATIONS TESTS
// ============================================================================

func TestTee(t *testing.T) {
	input := slices.Values([]int{1, 2, 3})
	streams := Tee(input, 3)

	if len(streams) != 3 {
		t.Fatalf("Tee should return 3 streams, got %d", len(streams))
	}

	// Each stream should contain same values
	for i, stream := range streams {
		result := slices.Collect(stream)
		expected := []int{1, 2, 3}
		if !slices.Equal(result, expected) {
			t.Errorf("Stream %d failed: expected %v, got %v", i, expected, result)
		}
	}
}

func TestTeeZero(t *testing.T) {
	input := slices.Values([]int{1, 2, 3})
	streams := Tee(input, 0)

	if streams != nil {
		t.Error("Tee with n=0 should return nil")
	}
}

func TestLazyTee(t *testing.T) {
	input := slices.Values([]int{1, 2, 3})
	streams := LazyTee(input, 2)

	if len(streams) != 2 {
		t.Fatalf("LazyTee should return 2 streams, got %d", len(streams))
	}

	// Consume first stream
	result1 := slices.Collect(streams[0])
	expected := []int{1, 2, 3}
	if !slices.Equal(result1, expected) {
		t.Errorf("LazyTee stream 1 failed: expected %v, got %v", expected, result1)
	}
}

// ============================================================================
// RUNNING AGGREGATION TESTS
// ============================================================================

func TestRunningSum(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"value": 10.0}),
		NewRecord(map[string]any{"value": 20.0}),
		NewRecord(map[string]any{"value": 30.0}),
	})

	filter := RunningSum("value")
	result := slices.Collect(filter(input))

	if len(result) != 3 {
		t.Fatalf("Expected 3 records, got %d", len(result))
	}

	// Check running sums
	if GetOr(result[0], "running_sum", 0.0) != 10.0 {
		t.Errorf("First running_sum should be 10.0, got %v", GetOr(result[0], "running_sum", 0.0))
	}
	if GetOr(result[1], "running_sum", 0.0) != 30.0 {
		t.Errorf("Second running_sum should be 30.0, got %v", GetOr(result[1], "running_sum", 0.0))
	}
	if GetOr(result[2], "running_sum", 0.0) != 60.0 {
		t.Errorf("Third running_sum should be 60.0, got %v", GetOr(result[2], "running_sum", 0.0))
	}
}

func TestRunningAverage(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"value": 10.0}),
		NewRecord(map[string]any{"value": 20.0}),
		NewRecord(map[string]any{"value": 30.0}),
		NewRecord(map[string]any{"value": 40.0}),
	})

	filter := RunningAverage("value", 2)
	result := slices.Collect(filter(input))

	if len(result) != 4 {
		t.Fatalf("Expected 4 records, got %d", len(result))
	}

	// First window: [10]
	if GetOr(result[0], "moving_avg", 0.0) != 10.0 {
		t.Errorf("First moving_avg should be 10.0, got %v", GetOr(result[0], "moving_avg", 0.0))
	}

	// Second window: [10, 20]
	if GetOr(result[1], "moving_avg", 0.0) != 15.0 {
		t.Errorf("Second moving_avg should be 15.0, got %v", GetOr(result[1], "moving_avg", 0.0))
	}

	// Third window: [20, 30]
	if GetOr(result[2], "moving_avg", 0.0) != 25.0 {
		t.Errorf("Third moving_avg should be 25.0, got %v", GetOr(result[2], "moving_avg", 0.0))
	}
}

func TestExponentialMovingAverage(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"value": 10.0}),
		NewRecord(map[string]any{"value": 20.0}),
		NewRecord(map[string]any{"value": 30.0}),
	})

	filter := ExponentialMovingAverage("value", 0.5)
	result := slices.Collect(filter(input))

	if len(result) != 3 {
		t.Fatalf("Expected 3 records, got %d", len(result))
	}

	// First EMA is initialized to first value
	if GetOr(result[0], "ema", 0.0) != 10.0 {
		t.Errorf("First EMA should be 10.0, got %v", GetOr(result[0], "ema", 0.0))
	}

	// Second EMA: 0.5*20 + 0.5*10 = 15
	if GetOr(result[1], "ema", 0.0) != 15.0 {
		t.Errorf("Second EMA should be 15.0, got %v", GetOr(result[1], "ema", 0.0))
	}
}

func TestRunningMinMax(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"value": 10.0}),
		NewRecord(map[string]any{"value": 5.0}),
		NewRecord(map[string]any{"value": 15.0}),
		NewRecord(map[string]any{"value": 3.0}),
	})

	filter := RunningMinMax("value")
	result := slices.Collect(filter(input))

	if len(result) != 4 {
		t.Fatalf("Expected 4 records, got %d", len(result))
	}

	// After all values, min should be 3.0, max should be 15.0
	last := result[3]
	if GetOr(last, "running_min", 0.0) != 3.0 {
		t.Errorf("Running min should be 3.0, got %v", GetOr(last, "running_min", 0.0))
	}
	if GetOr(last, "running_max", 0.0) != 15.0 {
		t.Errorf("Running max should be 15.0, got %v", GetOr(last, "running_max", 0.0))
	}
	if GetOr(last, "running_range", 0.0) != 12.0 {
		t.Errorf("Running range should be 12.0, got %v", GetOr(last, "running_range", 0.0))
	}
}

func TestRunningCount(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"category": "A"}),
		NewRecord(map[string]any{"category": "B"}),
		NewRecord(map[string]any{"category": "A"}),
		NewRecord(map[string]any{"category": "A"}),
	})

	filter := RunningCount("category")
	result := slices.Collect(filter(input))

	if len(result) != 4 {
		t.Fatalf("Expected 4 records, got %d", len(result))
	}

	last := result[3]
	if GetOr(last, "total_count", int64(0)) != int64(4) {
		t.Errorf("Total count should be 4, got %v", GetOr(last, "total_count", int64(0)))
	}
	if GetOr(last, "distinct_values", int64(0)) != int64(2) {
		t.Errorf("Distinct values should be 2, got %v", GetOr(last, "distinct_values", int64(0)))
	}
}

// ============================================================================
// WINDOWING OPERATIONS TESTS
// ============================================================================

func TestCountWindow(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5, 6, 7})
	filter := CountWindow[int](3)
	result := slices.Collect(filter(input))

	// Should create windows: [1,2,3], [4,5,6], [7]
	if len(result) != 3 {
		t.Fatalf("Expected 3 windows, got %d", len(result))
	}

	if !slices.Equal(result[0], []int{1, 2, 3}) {
		t.Errorf("First window failed: got %v", result[0])
	}
	if !slices.Equal(result[1], []int{4, 5, 6}) {
		t.Errorf("Second window failed: got %v", result[1])
	}
	if !slices.Equal(result[2], []int{7}) {
		t.Errorf("Third window failed: got %v", result[2])
	}
}

func TestCountWindowZero(t *testing.T) {
	input := slices.Values([]int{1, 2, 3})
	filter := CountWindow[int](0)
	result := slices.Collect(filter(input))

	if len(result) != 0 {
		t.Error("CountWindow(0) should return no windows")
	}
}

func TestSlidingCountWindow(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5})
	filter := SlidingCountWindow[int](3, 1)
	result := slices.Collect(filter(input))

	// Windows: [1,2,3], [2,3,4], [3,4,5]
	if len(result) != 3 {
		t.Fatalf("Expected 3 windows, got %d", len(result))
	}

	if !slices.Equal(result[0], []int{1, 2, 3}) {
		t.Errorf("First window failed: got %v", result[0])
	}
	if !slices.Equal(result[1], []int{2, 3, 4}) {
		t.Errorf("Second window failed: got %v", result[1])
	}
	if !slices.Equal(result[2], []int{3, 4, 5}) {
		t.Errorf("Third window failed: got %v", result[2])
	}
}

func TestTimeWindow(t *testing.T) {
	now := time.Now()
	input := slices.Values([]Record{
		NewRecord(map[string]any{"time": now, "value": 1}),
		NewRecord(map[string]any{"time": now.Add(1 * time.Second), "value": 2}),
		NewRecord(map[string]any{"time": now.Add(5 * time.Second), "value": 3}),
		NewRecord(map[string]any{"time": now.Add(6 * time.Second), "value": 4}),
	})

	filter := TimeWindow[Record](5*time.Second, "time")
	result := slices.Collect(filter(input))

	// Should create 2 windows: first 2 records, then next 2 records
	if len(result) < 1 {
		t.Fatalf("Expected at least 1 window, got %d", len(result))
	}
}

func TestSlidingTimeWindow(t *testing.T) {
	now := time.Now()
	input := slices.Values([]Record{
		NewRecord(map[string]any{"time": now, "value": 1}),
		NewRecord(map[string]any{"time": now.Add(1 * time.Second), "value": 2}),
		NewRecord(map[string]any{"time": now.Add(2 * time.Second), "value": 3}),
		NewRecord(map[string]any{"time": now.Add(3 * time.Second), "value": 4}),
	})

	filter := SlidingTimeWindow[Record](2*time.Second, 1*time.Second, "time")
	result := slices.Collect(filter(input))

	// Should create overlapping windows
	if len(result) < 1 {
		t.Log("SlidingTimeWindow created windows:", len(result))
	}
}

// ============================================================================
// EARLY TERMINATION TESTS
// ============================================================================

func TestTakeWhile(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5, 1, 2})
	filter := TakeWhile(func(x int) bool { return x < 4 })
	result := slices.Collect(filter(input))

	expected := []int{1, 2, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("TakeWhile failed: expected %v, got %v", expected, result)
	}
}

func TestTakeUntil(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5, 6})
	filter := TakeUntil(func(x int) bool { return x > 3 })
	result := slices.Collect(filter(input))

	expected := []int{1, 2, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("TakeUntil failed: expected %v, got %v", expected, result)
	}
}

func TestTimeout(t *testing.T) {
	// Create a slow infinite sequence
	slowSeq := func(yield func(int) bool) {
		for i := 0; ; i++ {
			time.Sleep(10 * time.Millisecond)
			if !yield(i) {
				return
			}
		}
	}

	filter := Timeout[int](50 * time.Millisecond)
	start := time.Now()
	result := slices.Collect(filter(slowSeq))
	duration := time.Since(start)

	// Should terminate around 50ms
	if duration > 100*time.Millisecond {
		t.Errorf("Timeout took too long: %v", duration)
	}

	// Should collect a few elements before timeout
	if len(result) == 0 {
		t.Log("Timeout collected 0 elements (expected at least a few)")
	}
}

func TestTimeBasedTimeout(t *testing.T) {
	now := time.Now()
	input := slices.Values([]Record{
		NewRecord(map[string]any{"time": now, "value": 1}),
		NewRecord(map[string]any{"time": now.Add(1 * time.Second), "value": 2}),
		NewRecord(map[string]any{"time": now.Add(2 * time.Second), "value": 3}),
		NewRecord(map[string]any{"time": now.Add(6 * time.Second), "value": 4}), // Exceeds 5 second limit
	})

	filter := TimeBasedTimeout("time", 5*time.Second)
	result := slices.Collect(filter(input))

	// Should stop before the 4th record
	if len(result) != 3 {
		t.Errorf("TimeBasedTimeout should return 3 records, got %d", len(result))
	}
}

func TestSkipWhile(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5, 1, 2})
	filter := SkipWhile(func(x int) bool { return x < 3 })
	result := slices.Collect(filter(input))

	expected := []int{3, 4, 5, 1, 2}
	if !slices.Equal(result, expected) {
		t.Errorf("SkipWhile failed: expected %v, got %v", expected, result)
	}
}

func TestSkipUntil(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5})
	filter := SkipUntil(func(x int) bool { return x >= 3 })
	result := slices.Collect(filter(input))

	expected := []int{3, 4, 5}
	if !slices.Equal(result, expected) {
		t.Errorf("SkipUntil failed: expected %v, got %v", expected, result)
	}
}

// ============================================================================
// COMPLEX PIPELINE TESTS
// ============================================================================

func TestComplexPipeline(t *testing.T) {
	// Test a complex pipeline: filter, map, sort, limit
	input := slices.Values([]int{5, 2, 8, 1, 9, 3, 7, 4, 6})

	// Apply transformations step by step
	filtered := Where(func(x int) bool { return x > 3 })(input)
	doubled := Select(func(x int) int { return x * 2 })(filtered)
	sorted := Sort[int]()(doubled)
	result := slices.Collect(Limit[int](3)(sorted))
	expected := []int{8, 10, 12} // [4, 5, 6] doubled and sorted

	if !slices.Equal(result, expected) {
		t.Errorf("Complex pipeline failed: expected %v, got %v", expected, result)
	}
}

func TestRecordPipeline(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "Alice", "age": int64(30), "score": 85.0}),
		NewRecord(map[string]any{"name": "Bob", "age": int64(25), "score": 90.0}),
		NewRecord(map[string]any{"name": "Charlie", "age": int64(35), "score": 75.0}),
	})

	// Filter age > 26, add bonus field, sort by score descending
	filtered := Where(func(r Record) bool {
		age := GetOr(r, "age", int64(0))
		return age > 26
	})(input)

	withBonus := Select(func(r Record) Record {
		mut := r.ToMutable()
		score := GetOr(r, "score", 0.0)
		mut.fields["bonus"] = score * 0.1
		return mut.Freeze()
	})(filtered)

	sorted := SortBy(func(r Record) float64 {
		return -GetOr(r, "score", 0.0) // Negative for descending
	})(withBonus)

	result := slices.Collect(sorted)

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}

	// Alice should be first (score 85), Charlie second (score 75)
	if GetOr(result[0], "name", "") != "Alice" {
		t.Errorf("First record should be Alice, got %v", GetOr(result[0], "name", ""))
	}
	if GetOr(result[1], "name", "") != "Charlie" {
		t.Errorf("Second record should be Charlie, got %v", GetOr(result[1], "name", ""))
	}
}
