//go:build !(js && wasm)

package main

import (
	"testing"
)

// ============================================================================
// Comparison operators
// ============================================================================

func TestApplyOperatorEq(t *testing.T) {
	tests := []struct {
		field any
		value string
		want  bool
	}{
		{"hello", "hello", true},
		{"hello", "world", false},
		{int64(42), "42", true},
		{int64(42), "43", false},
		{float64(3.14), "3.14", true},
		{true, "true", true},
		{false, "false", true},
	}
	for _, tt := range tests {
		got := applyOperator(tt.field, "eq", tt.value)
		if got != tt.want {
			t.Errorf("eq(%v, %q) = %v, want %v", tt.field, tt.value, got, tt.want)
		}
	}
}

func TestApplyOperatorNe(t *testing.T) {
	if !applyOperator("hello", "ne", "world") {
		t.Error("expected hello ne world = true")
	}
	if applyOperator("hello", "ne", "hello") {
		t.Error("expected hello ne hello = false")
	}
}

func TestApplyOperatorNumericComparisons(t *testing.T) {
	tests := []struct {
		field any
		op    string
		value string
		want  bool
	}{
		{int64(10), "gt", "5", true},
		{int64(5), "gt", "10", false},
		{int64(10), "ge", "10", true},
		{int64(5), "lt", "10", true},
		{int64(10), "lt", "5", false},
		{int64(10), "le", "10", true},
		{float64(3.14), "gt", "3.0", true},
		{float64(3.14), "lt", "4.0", true},
	}
	for _, tt := range tests {
		got := applyOperator(tt.field, tt.op, tt.value)
		if got != tt.want {
			t.Errorf("%s(%v, %q) = %v, want %v", tt.op, tt.field, tt.value, got, tt.want)
		}
	}
}

func TestApplyOperatorStringOps(t *testing.T) {
	if !applyOperator("hello world", "contains", "world") {
		t.Error("expected contains match")
	}
	if !applyOperator("hello", "startswith", "hel") {
		t.Error("expected startswith match")
	}
	if !applyOperator("hello", "endswith", "llo") {
		t.Error("expected endswith match")
	}
	if applyOperator("hello", "contains", "xyz") {
		t.Error("expected no contains match")
	}
}

func TestApplyOperatorUnknown(t *testing.T) {
	if applyOperator("hello", "bogus", "hello") {
		t.Error("expected unknown operator to return false")
	}
}

// ============================================================================
// CompareValues (for sorting)
// ============================================================================

func TestCompareValuesNil(t *testing.T) {
	if compareValues(nil, nil) != 0 {
		t.Error("nil == nil should be 0")
	}
	if compareValues(nil, int64(1)) != -1 {
		t.Error("nil < non-nil should be -1")
	}
	if compareValues(int64(1), nil) != 1 {
		t.Error("non-nil > nil should be 1")
	}
}

func TestCompareValuesNumeric(t *testing.T) {
	if compareValues(int64(1), int64(2)) != -1 {
		t.Error("1 < 2")
	}
	if compareValues(int64(2), int64(1)) != 1 {
		t.Error("2 > 1")
	}
	if compareValues(int64(1), int64(1)) != 0 {
		t.Error("1 == 1")
	}
	if compareValues(float64(1.5), float64(2.5)) != -1 {
		t.Error("1.5 < 2.5")
	}
	// Mixed int64/float64
	if compareValues(int64(1), float64(1.5)) != -1 {
		t.Error("1 < 1.5")
	}
}

func TestCompareValuesString(t *testing.T) {
	if compareValues("apple", "banana") != -1 {
		t.Error("apple < banana")
	}
	if compareValues("banana", "apple") != 1 {
		t.Error("banana > apple")
	}
}

// ============================================================================
// Where
// ============================================================================

func TestWhere(t *testing.T) {
	ds := makeTestDataset([]string{"name", "age"}, [][]any{
		{"Alice", int64(30)},
		{"Bob", int64(25)},
		{"Charlie", int64(35)},
	})

	result := ds.where("age", "gt", "28")
	if len(result.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.rows))
	}
	if result.rows[0].mustGet("name") != "Alice" {
		t.Errorf("expected Alice, got %v", result.rows[0].mustGet("name"))
	}
	if result.rows[1].mustGet("name") != "Charlie" {
		t.Errorf("expected Charlie, got %v", result.rows[1].mustGet("name"))
	}
}

func TestWhereNoMatch(t *testing.T) {
	ds := makeTestDataset([]string{"x"}, [][]any{
		{int64(1)},
		{int64(2)},
	})
	result := ds.where("x", "gt", "100")
	if len(result.rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result.rows))
	}
}

// ============================================================================
// Sort
// ============================================================================

func TestSortAscending(t *testing.T) {
	ds := makeTestDataset([]string{"name", "age"}, [][]any{
		{"Charlie", int64(35)},
		{"Alice", int64(30)},
		{"Bob", int64(25)},
	})

	result := ds.sortBy("age", false)
	if result.rows[0].mustGet("name") != "Bob" {
		t.Errorf("expected Bob first, got %v", result.rows[0].mustGet("name"))
	}
	if result.rows[2].mustGet("name") != "Charlie" {
		t.Errorf("expected Charlie last, got %v", result.rows[2].mustGet("name"))
	}
}

func TestSortDescending(t *testing.T) {
	ds := makeTestDataset([]string{"name", "age"}, [][]any{
		{"Alice", int64(30)},
		{"Bob", int64(25)},
		{"Charlie", int64(35)},
	})

	result := ds.sortBy("age", true)
	if result.rows[0].mustGet("name") != "Charlie" {
		t.Errorf("expected Charlie first, got %v", result.rows[0].mustGet("name"))
	}
}

func TestSortWithNils(t *testing.T) {
	ds := makeTestDataset([]string{"x"}, [][]any{
		{int64(3)},
		{nil},
		{int64(1)},
	})
	result := ds.sortBy("x", false)
	// nil sorts before numbers
	if result.rows[0].mustGet("x") != nil {
		t.Errorf("expected nil first, got %v", result.rows[0].mustGet("x"))
	}
}

func TestSortDoesNotMutateOriginal(t *testing.T) {
	ds := makeTestDataset([]string{"x"}, [][]any{
		{int64(3)},
		{int64(1)},
		{int64(2)},
	})
	_ = ds.sortBy("x", false)
	// Original should be unchanged
	if ds.rows[0].mustGet("x") != int64(3) {
		t.Error("sort mutated the original dataset")
	}
}

// ============================================================================
// GroupBy
// ============================================================================

func TestGroupByCount(t *testing.T) {
	ds := makeTestDataset([]string{"dept", "salary"}, [][]any{
		{"eng", int64(100)},
		{"eng", int64(120)},
		{"sales", int64(80)},
	})

	result := ds.groupBy("dept", "salary", "count")
	if len(result.rows) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(result.rows))
	}
	// eng first (first-seen order)
	if result.rows[0].mustGet("dept") != "eng" {
		t.Errorf("expected eng first, got %v", result.rows[0].mustGet("dept"))
	}
	if result.rows[0].mustGet("count") != int64(2) {
		t.Errorf("expected count 2, got %v", result.rows[0].mustGet("count"))
	}
	if result.rows[1].mustGet("count") != int64(1) {
		t.Errorf("expected count 1, got %v", result.rows[1].mustGet("count"))
	}
}

func TestGroupBySum(t *testing.T) {
	ds := makeTestDataset([]string{"dept", "salary"}, [][]any{
		{"eng", int64(100)},
		{"eng", int64(120)},
		{"sales", int64(80)},
	})

	result := ds.groupBy("dept", "salary", "sum")
	engSum := result.rows[0].mustGet("sum")
	if f, ok := engSum.(float64); !ok || f != 220 {
		t.Errorf("expected sum 220, got %v (%T)", engSum, engSum)
	}
}

func TestGroupByAvg(t *testing.T) {
	ds := makeTestDataset([]string{"dept", "salary"}, [][]any{
		{"eng", int64(100)},
		{"eng", int64(200)},
	})

	result := ds.groupBy("dept", "salary", "avg")
	avg := result.rows[0].mustGet("avg")
	if f, ok := avg.(float64); !ok || f != 150 {
		t.Errorf("expected avg 150, got %v (%T)", avg, avg)
	}
}

func TestGroupByMin(t *testing.T) {
	ds := makeTestDataset([]string{"dept", "val"}, [][]any{
		{"a", int64(10)},
		{"a", int64(5)},
		{"a", int64(20)},
	})

	result := ds.groupBy("dept", "val", "min")
	minVal := result.rows[0].mustGet("min")
	if minVal != int64(5) {
		t.Errorf("expected min 5, got %v (%T)", minVal, minVal)
	}
}

func TestGroupByMax(t *testing.T) {
	ds := makeTestDataset([]string{"dept", "val"}, [][]any{
		{"a", int64(10)},
		{"a", int64(5)},
		{"a", int64(20)},
	})

	result := ds.groupBy("dept", "val", "max")
	maxVal := result.rows[0].mustGet("max")
	if maxVal != int64(20) {
		t.Errorf("expected max 20, got %v (%T)", maxVal, maxVal)
	}
}

func TestGroupByOrderPreserved(t *testing.T) {
	ds := makeTestDataset([]string{"dept", "val"}, [][]any{
		{"zebra", int64(1)},
		{"apple", int64(2)},
		{"mango", int64(3)},
	})

	result := ds.groupBy("dept", "val", "count")
	if result.rows[0].mustGet("dept") != "zebra" {
		t.Errorf("expected first-seen order (zebra), got %v", result.rows[0].mustGet("dept"))
	}
	if result.rows[1].mustGet("dept") != "apple" {
		t.Errorf("expected second (apple), got %v", result.rows[1].mustGet("dept"))
	}
	if result.rows[2].mustGet("dept") != "mango" {
		t.Errorf("expected third (mango), got %v", result.rows[2].mustGet("dept"))
	}
}

// ============================================================================
// Distinct
// ============================================================================

func TestDistinct(t *testing.T) {
	ds := makeTestDataset([]string{"name"}, [][]any{
		{"Alice"},
		{"Bob"},
		{"Alice"},
		{"Charlie"},
		{"Bob"},
	})

	result := ds.distinct("name")
	if len(result.rows) != 3 {
		t.Fatalf("expected 3 distinct rows, got %d", len(result.rows))
	}
	if result.rows[0].mustGet("name") != "Alice" {
		t.Error("expected first-seen order")
	}
}

// ============================================================================
// Limit
// ============================================================================

func TestLimitBasic(t *testing.T) {
	ds := makeTestDataset([]string{"x"}, [][]any{
		{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)},
	})

	result := ds.limit(3, 0)
	if len(result.rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.rows))
	}
	if result.rows[0].mustGet("x") != int64(1) {
		t.Error("expected first row = 1")
	}
}

func TestLimitWithOffset(t *testing.T) {
	ds := makeTestDataset([]string{"x"}, [][]any{
		{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)},
	})

	result := ds.limit(2, 2)
	if len(result.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.rows))
	}
	if result.rows[0].mustGet("x") != int64(3) {
		t.Errorf("expected first row = 3, got %v", result.rows[0].mustGet("x"))
	}
}

func TestLimitOffsetBeyondEnd(t *testing.T) {
	ds := makeTestDataset([]string{"x"}, [][]any{
		{int64(1)}, {int64(2)},
	})

	result := ds.limit(10, 100)
	if len(result.rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result.rows))
	}
}

func TestLimitZeroN(t *testing.T) {
	ds := makeTestDataset([]string{"x"}, [][]any{
		{int64(1)}, {int64(2)}, {int64(3)},
	})

	// n=0 should return all rows (no limit)
	result := ds.limit(0, 0)
	if len(result.rows) != 3 {
		t.Errorf("expected 3 rows with n=0, got %d", len(result.rows))
	}
}

// ============================================================================
// Pipeline
// ============================================================================

func TestPipelineComposition(t *testing.T) {
	ds := makeTestDataset([]string{"name", "age"}, [][]any{
		{"Alice", int64(30)},
		{"Bob", int64(25)},
		{"Charlie", int64(35)},
		{"David", int64(20)},
	})

	ops := []pipelineOp{
		{Op: "where", Field: "age", Operator: "gt", Value: "22"},
		{Op: "sort", Field: "age", Desc: true},
		{Op: "limit", N: 2},
	}

	result, err := ds.pipeline(ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.rows))
	}
	// Sorted descending: Charlie(35), Alice(30)
	if result.rows[0].mustGet("name") != "Charlie" {
		t.Errorf("expected Charlie first, got %v", result.rows[0].mustGet("name"))
	}
	if result.rows[1].mustGet("name") != "Alice" {
		t.Errorf("expected Alice second, got %v", result.rows[1].mustGet("name"))
	}
}

func TestPipelineUnknownOp(t *testing.T) {
	ds := makeTestDataset([]string{"x"}, [][]any{{int64(1)}})
	_, err := ds.pipeline([]pipelineOp{{Op: "bogus"}})
	if err == nil {
		t.Error("expected error for unknown operation")
	}
}

// ============================================================================
// Round-trip integration: parse → operate → serialize
// ============================================================================

func TestRoundTripParseOperateSerialize(t *testing.T) {
	input := `[{"name":"Alice","age":30},{"name":"Bob","age":25},{"name":"Charlie","age":35}]`

	ds, err := parseJSONArray(input)
	if err != nil {
		t.Fatal(err)
	}

	// Filter age > 28, sort descending
	result := ds.where("age", "gt", "28").sortBy("age", true)
	output := result.toJSON()

	// Parse the output back
	ds2, err := parseJSONArray(output)
	if err != nil {
		t.Fatalf("failed to reparse output: %v", err)
	}

	if len(ds2.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(ds2.rows))
	}
	// Charlie(35) first, then Alice(30)
	if ds2.rows[0].mustGet("name") != "Charlie" {
		t.Errorf("expected Charlie first after round trip, got %v", ds2.rows[0].mustGet("name"))
	}
	if ds2.rows[1].mustGet("name") != "Alice" {
		t.Errorf("expected Alice second after round trip, got %v", ds2.rows[1].mustGet("name"))
	}
}
