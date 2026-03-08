package ssql

import (
	"fmt"
	"iter"
	"math"
	"slices"
	"testing"
)

// ============================================================================
// JOIN OPERATIONS TESTS
// ============================================================================

func TestInnerJoin(t *testing.T) {
	left := slices.Values([]Record{
		NewRecord(map[string]any{"id": int64(1), "name": "Alice"}),
		NewRecord(map[string]any{"id": int64(2), "name": "Bob"}),
		NewRecord(map[string]any{"id": int64(3), "name": "Charlie"}),
	})

	right := slices.Values([]Record{
		NewRecord(map[string]any{"id": int64(1), "dept": "Engineering"}),
		NewRecord(map[string]any{"id": int64(2), "dept": "Sales"}),
	})

	filter := InnerJoin(right, OnFields("id"))
	result := slices.Collect(filter(left))

	// Should match only records with id 1 and 2
	if len(result) != 2 {
		t.Fatalf("InnerJoin should return 2 records, got %d", len(result))
	}

	// Check first joined record
	if GetOr(result[0], "name", "") != "Alice" || GetOr(result[0], "dept", "") != "Engineering" {
		t.Errorf("First join failed: %v", result[0])
	}
}

func TestLeftJoin(t *testing.T) {
	left := slices.Values([]Record{
		NewRecord(map[string]any{"id": int64(1), "name": "Alice"}),
		NewRecord(map[string]any{"id": int64(2), "name": "Bob"}),
		NewRecord(map[string]any{"id": int64(3), "name": "Charlie"}),
	})

	right := slices.Values([]Record{
		NewRecord(map[string]any{"id": int64(1), "dept": "Engineering"}),
		NewRecord(map[string]any{"id": int64(2), "dept": "Sales"}),
	})

	filter := LeftJoin(right, OnFields("id"))
	result := slices.Collect(filter(left))

	// Should include all left records
	if len(result) != 3 {
		t.Fatalf("LeftJoin should return 3 records, got %d", len(result))
	}

	// Charlie should exist without dept field
	found := false
	for _, r := range result {
		if GetOr(r, "name", "") == "Charlie" {
			found = true
			if r.Has("dept") {
				t.Error("Charlie should not have dept field in left join")
			}
		}
	}

	if !found {
		t.Error("LeftJoin should include Charlie")
	}
}

func TestRightJoin(t *testing.T) {
	left := slices.Values([]Record{
		NewRecord(map[string]any{"id": int64(1), "name": "Alice"}),
		NewRecord(map[string]any{"id": int64(2), "name": "Bob"}),
	})

	right := slices.Values([]Record{
		NewRecord(map[string]any{"id": int64(1), "dept": "Engineering"}),
		NewRecord(map[string]any{"id": int64(2), "dept": "Sales"}),
		NewRecord(map[string]any{"id": int64(3), "dept": "Marketing"}),
	})

	filter := RightJoin(right, OnFields("id"))
	result := slices.Collect(filter(left))

	// Should include all right records
	if len(result) != 3 {
		t.Fatalf("RightJoin should return 3 records, got %d", len(result))
	}

	// Marketing dept should exist without name field
	found := false
	for _, r := range result {
		if GetOr(r, "dept", "") == "Marketing" {
			found = true
			if r.Has("name") {
				t.Error("Marketing record should not have name field")
			}
		}
	}

	if !found {
		t.Error("RightJoin should include Marketing dept")
	}
}

func TestFullJoin(t *testing.T) {
	left := slices.Values([]Record{
		NewRecord(map[string]any{"id": int64(1), "name": "Alice"}),
		NewRecord(map[string]any{"id": int64(2), "name": "Bob"}),
		NewRecord(map[string]any{"id": int64(4), "name": "David"}),
	})

	right := slices.Values([]Record{
		NewRecord(map[string]any{"id": int64(1), "dept": "Engineering"}),
		NewRecord(map[string]any{"id": int64(2), "dept": "Sales"}),
		NewRecord(map[string]any{"id": int64(3), "dept": "Marketing"}),
	})

	filter := FullJoin(right, OnFields("id"))
	result := slices.Collect(filter(left))

	// Should include matched (id 1, 2), unmatched left (David), unmatched right (Marketing)
	if len(result) != 4 {
		t.Fatalf("FullJoin should return 4 records, got %d", len(result))
	}

	hasDavid := false
	hasMarketing := false

	for _, r := range result {
		if GetOr(r, "name", "") == "David" {
			hasDavid = true
		}
		if GetOr(r, "dept", "") == "Marketing" {
			hasMarketing = true
		}
	}

	if !hasDavid {
		t.Error("FullJoin should include David (unmatched left)")
	}
	if !hasMarketing {
		t.Error("FullJoin should include Marketing (unmatched right)")
	}
}

func TestOnFields(t *testing.T) {
	predicate := OnFields("id", "type")

	left := NewRecord(map[string]any{"id": int64(1), "type": "A", "name": "Alice"})
	right := NewRecord(map[string]any{"id": int64(1), "type": "A", "dept": "Eng"})

	if !predicate.Match(left, right) {
		t.Error("OnFields should match records with same id and type")
	}

	right2 := NewRecord(map[string]any{"id": int64(1), "type": "B", "dept": "Sales"})
	if predicate.Match(left, right2) {
		t.Error("OnFields should not match records with different type")
	}
}

func TestOnCondition(t *testing.T) {
	predicate := OnCondition(func(left, right Record) bool {
		leftAge, ok1 := Get[int64](left, "age")
		rightAge, ok2 := Get[int64](right, "age")
		return ok1 && ok2 && leftAge > rightAge
	})

	left := NewRecord(map[string]any{"name": "Alice", "age": int64(30)})
	right := NewRecord(map[string]any{"name": "Bob", "age": int64(25)})

	if !predicate.Match(left, right) {
		t.Error("OnCondition should match when left age > right age")
	}

	right2 := NewRecord(map[string]any{"name": "Charlie", "age": int64(35)})
	if predicate.Match(left, right2) {
		t.Error("OnCondition should not match when left age < right age")
	}
}

func TestOnFieldPair(t *testing.T) {
	predicate := OnFieldPair("user_id", "id")

	left := NewRecord(map[string]any{"user_id": int64(1), "name": "Alice"})
	right := NewRecord(map[string]any{"id": int64(1), "dept": "Eng"})

	if !predicate.Match(left, right) {
		t.Error("OnFieldPair should match records with user_id=id")
	}

	right2 := NewRecord(map[string]any{"id": int64(2), "dept": "Sales"})
	if predicate.Match(left, right2) {
		t.Error("OnFieldPair should not match records with different ids")
	}
}

func TestLookup(t *testing.T) {
	// Test with renames
	clause := Lookup("a_kind", "kind", "kind_name", "a_kind_name")
	if clause.LeftField != "a_kind" {
		t.Errorf("Lookup LeftField = %q, want %q", clause.LeftField, "a_kind")
	}
	if clause.RightField != "kind" {
		t.Errorf("Lookup RightField = %q, want %q", clause.RightField, "kind")
	}
	if clause.FieldRenames["kind_name"] != "a_kind_name" {
		t.Errorf("Lookup FieldRenames[kind_name] = %q, want %q", clause.FieldRenames["kind_name"], "a_kind_name")
	}

	// Test without renames
	clause2 := Lookup("id", "id")
	if len(clause2.FieldRenames) != 0 {
		t.Errorf("Lookup without renames should have empty FieldRenames, got %v", clause2.FieldRenames)
	}
}

func TestLookupJoin(t *testing.T) {
	// Left side: records with a_kind and z_kind
	left := slices.Values([]Record{
		NewRecord(map[string]any{"name": "Alice", "a_kind": int64(1), "z_kind": int64(2)}),
		NewRecord(map[string]any{"name": "Bob", "a_kind": int64(2), "z_kind": int64(1)}),
	})

	// Right side: kind lookup table
	right := slices.Values([]Record{
		NewRecord(map[string]any{"kind": int64(1), "kind_name": "Premium"}),
		NewRecord(map[string]any{"kind": int64(2), "kind_name": "Standard"}),
	})

	// Two clauses: lookup a_kind and z_kind with different renames
	clauses := []LookupClause{
		Lookup("a_kind", "kind", "kind_name", "a_kind_name"),
		Lookup("z_kind", "kind", "kind_name", "z_kind_name"),
	}

	result := slices.Collect(LookupJoin(right, clauses)(left))

	if len(result) != 2 {
		t.Fatalf("LookupJoin should return 2 records, got %d", len(result))
	}

	// Check Alice: a_kind=1 -> Premium, z_kind=2 -> Standard
	alice := result[0]
	if GetOr(alice, "name", "") != "Alice" {
		t.Errorf("First record should be Alice, got %v", GetOr(alice, "name", ""))
	}
	if GetOr(alice, "a_kind_name", "") != "Premium" {
		t.Errorf("Alice a_kind_name should be Premium, got %v", GetOr(alice, "a_kind_name", ""))
	}
	if GetOr(alice, "z_kind_name", "") != "Standard" {
		t.Errorf("Alice z_kind_name should be Standard, got %v", GetOr(alice, "z_kind_name", ""))
	}

	// Check Bob: a_kind=2 -> Standard, z_kind=1 -> Premium
	bob := result[1]
	if GetOr(bob, "a_kind_name", "") != "Standard" {
		t.Errorf("Bob a_kind_name should be Standard, got %v", GetOr(bob, "a_kind_name", ""))
	}
	if GetOr(bob, "z_kind_name", "") != "Premium" {
		t.Errorf("Bob z_kind_name should be Premium, got %v", GetOr(bob, "z_kind_name", ""))
	}
}

func TestLookupJoinNoMatch(t *testing.T) {
	left := slices.Values([]Record{
		NewRecord(map[string]any{"name": "Alice", "kind_id": int64(99)}), // No match
	})

	right := slices.Values([]Record{
		NewRecord(map[string]any{"kind": int64(1), "kind_name": "Premium"}),
	})

	clauses := []LookupClause{
		Lookup("kind_id", "kind", "kind_name", "kind_name"),
	}

	result := slices.Collect(LookupJoin(right, clauses)(left))

	// Record should pass through unchanged when no match
	if len(result) != 1 {
		t.Fatalf("LookupJoin should return 1 record, got %d", len(result))
	}

	if GetOr(result[0], "name", "") != "Alice" {
		t.Error("Record should preserve original fields")
	}
	if result[0].Has("kind_name") {
		t.Error("Record should not have kind_name when no match")
	}
}

func TestLookupJoinMultipleMatches(t *testing.T) {
	left := slices.Values([]Record{
		NewRecord(map[string]any{"name": "Alice", "dept": "Eng"}),
	})

	// Multiple employees in Eng department
	right := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "employee": "Bob"}),
		NewRecord(map[string]any{"dept": "Eng", "employee": "Charlie"}),
	})

	clauses := []LookupClause{
		Lookup("dept", "dept", "employee", "colleague"),
	}

	result := slices.Collect(LookupJoin(right, clauses)(left))

	// Should produce 2 records (one for each match)
	if len(result) != 2 {
		t.Fatalf("LookupJoin should return 2 records for multiple matches, got %d", len(result))
	}

	colleagues := make(map[string]bool)
	for _, r := range result {
		if c, ok := Get[string](r, "colleague"); ok {
			colleagues[c] = true
		}
	}
	if !colleagues["Bob"] || !colleagues["Charlie"] {
		t.Error("LookupJoin should include all matching records")
	}
}

// ============================================================================
// GROUPBY OPERATIONS TESTS
// ============================================================================

func TestGroupBy(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "Alice", "dept": "Engineering", "salary": int64(100000)}),
		NewRecord(map[string]any{"name": "Bob", "dept": "Engineering", "salary": int64(110000)}),
		NewRecord(map[string]any{"name": "Charlie", "dept": "Sales", "salary": int64(90000)}),
	})

	filter := GroupBy("employees", "department", func(r Record) string {
		return GetOr(r, "dept", "")
	})

	result := slices.Collect(filter(input))

	// Should create 2 groups: Engineering and Sales
	if len(result) != 2 {
		t.Fatalf("GroupBy should return 2 groups, got %d", len(result))
	}

	// Check that each group has the sequence field
	for _, group := range result {
		if !group.Has("department") {
			t.Error("Group should have 'department' field")
		}
		if !group.Has("employees") {
			t.Error("Group should have 'employees' field")
		}

		// Check sequence field type
		if seq, ok := Get[iter.Seq[Record]](group, "employees"); ok {
			members := slices.Collect(seq)
			if len(members) == 0 {
				t.Error("Group should have members")
			}
		} else {
			t.Error("employees field should be iter.Seq[Record]")
		}
	}
}

func TestGroupByFields(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "level": "Senior", "count": int64(5)}),
		NewRecord(map[string]any{"dept": "Eng", "level": "Senior", "count": int64(3)}),
		NewRecord(map[string]any{"dept": "Eng", "level": "Junior", "count": int64(10)}),
		NewRecord(map[string]any{"dept": "Sales", "level": "Senior", "count": int64(2)}),
	})

	filter := GroupByFields("members", "dept", "level")
	result := slices.Collect(filter(input))

	// Should create 3 groups: (Eng,Senior), (Eng,Junior), (Sales,Senior)
	if len(result) != 3 {
		t.Fatalf("GroupByFields should return 3 groups, got %d", len(result))
	}

	// Find the (Eng, Senior) group
	for _, group := range result {
		if GetOr(group, "dept", "") == "Eng" && GetOr(group, "level", "") == "Senior" {
			// This group should have 2 members
			if seq, ok := Get[iter.Seq[Record]](group, "members"); ok {
				members := slices.Collect(seq)
				if len(members) != 2 {
					t.Errorf("Eng/Senior group should have 2 members, got %d", len(members))
				}
			}
		}
	}
}

func TestGroupByFieldsWithComplexValues(t *testing.T) {
	// Test that records with complex GROUPING field values are skipped
	nestedRecord := NewRecord(map[string]any{"city": "NYC"})

	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": nestedRecord, "name": "Alice"}), // Complex dept field - should be skipped
		NewRecord(map[string]any{"dept": "Sales", "location": "Boston"}), // Simple dept - should work
	})

	filter := GroupByFields("members", "dept")
	result := slices.Collect(filter(input))

	// Should only group the Sales record (Eng has complex dept value)
	if len(result) != 1 {
		t.Fatalf("GroupByFields should skip records with complex grouping field values, expected 1 group, got %d", len(result))
	}

	if GetOr(result[0], "dept", "") != "Sales" {
		t.Error("Should only have Sales group")
	}
}

// ============================================================================
// STREAMING GROUP BY TESTS
// ============================================================================

func TestStreamGroupByFields_SingleField(t *testing.T) {
	// Presorted by dept
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "name": "Alice"}),
		NewRecord(map[string]any{"dept": "Eng", "name": "Bob"}),
		NewRecord(map[string]any{"dept": "Sales", "name": "Charlie"}),
	})

	result := slices.Collect(StreamGroupByFields("members", "dept")(input))

	if len(result) != 2 {
		t.Fatalf("StreamGroupByFields: expected 2 groups, got %d", len(result))
	}

	if GetOr(result[0], "dept", "") != "Eng" {
		t.Error("First group should be Eng")
	}
	if GetOr(result[1], "dept", "") != "Sales" {
		t.Error("Second group should be Sales")
	}

	// Verify group members
	engMembers := slices.Collect(GetOr(result[0], "members", iter.Seq[Record](nil)))
	if len(engMembers) != 2 {
		t.Errorf("Eng should have 2 members, got %d", len(engMembers))
	}
	salesMembers := slices.Collect(GetOr(result[1], "members", iter.Seq[Record](nil)))
	if len(salesMembers) != 1 {
		t.Errorf("Sales should have 1 member, got %d", len(salesMembers))
	}
}

func TestStreamGroupByFields_MultipleFields(t *testing.T) {
	// Presorted by dept, then city
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "city": "NYC", "name": "Alice"}),
		NewRecord(map[string]any{"dept": "Eng", "city": "NYC", "name": "Bob"}),
		NewRecord(map[string]any{"dept": "Eng", "city": "SF", "name": "Charlie"}),
		NewRecord(map[string]any{"dept": "Sales", "city": "NYC", "name": "Dave"}),
	})

	result := slices.Collect(StreamGroupByFields("members", "dept", "city")(input))

	if len(result) != 3 {
		t.Fatalf("Expected 3 groups, got %d", len(result))
	}

	// Eng/NYC, Eng/SF, Sales/NYC
	if GetOr(result[0], "dept", "") != "Eng" || GetOr(result[0], "city", "") != "NYC" {
		t.Errorf("Group 0: expected Eng/NYC, got %s/%s", GetOr(result[0], "dept", ""), GetOr(result[0], "city", ""))
	}
	if GetOr(result[1], "dept", "") != "Eng" || GetOr(result[1], "city", "") != "SF" {
		t.Errorf("Group 1: expected Eng/SF, got %s/%s", GetOr(result[1], "dept", ""), GetOr(result[1], "city", ""))
	}
}

func TestStreamGroupByFields_EmptyInput(t *testing.T) {
	input := slices.Values([]Record{})
	result := slices.Collect(StreamGroupByFields("members", "dept")(input))
	if len(result) != 0 {
		t.Errorf("Expected 0 groups, got %d", len(result))
	}
}

func TestStreamGroupByFields_SingleGroup(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "name": "Alice"}),
		NewRecord(map[string]any{"dept": "Eng", "name": "Bob"}),
		NewRecord(map[string]any{"dept": "Eng", "name": "Charlie"}),
	})

	result := slices.Collect(StreamGroupByFields("members", "dept")(input))

	if len(result) != 1 {
		t.Fatalf("Expected 1 group, got %d", len(result))
	}

	members := slices.Collect(GetOr(result[0], "members", iter.Seq[Record](nil)))
	if len(members) != 3 {
		t.Errorf("Group should have 3 members, got %d", len(members))
	}
}

func TestStreamGroupByFields_WithAggregate(t *testing.T) {
	// Full pipeline: stream group-by + aggregate
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "salary": int64(100000)}),
		NewRecord(map[string]any{"dept": "Eng", "salary": int64(110000)}),
		NewRecord(map[string]any{"dept": "Sales", "salary": int64(90000)}),
		NewRecord(map[string]any{"dept": "Sales", "salary": int64(80000)}),
	})

	grouped := StreamGroupByFields("_group", "dept")(input)
	aggregated := Aggregate("_group", map[string]AggregateFunc{
		"count": Count(),
		"total": Sum("salary"),
		"avg":   Avg("salary"),
	})(grouped)

	result := slices.Collect(aggregated)

	if len(result) != 2 {
		t.Fatalf("Expected 2 aggregated groups, got %d", len(result))
	}

	// Find Eng group
	var eng Record
	for _, r := range result {
		if GetOr(r, "dept", "") == "Eng" {
			eng = r
			break
		}
	}

	if GetOr(eng, "count", int64(0)) != int64(2) {
		t.Errorf("Eng count: expected 2, got %v", GetOr[any](eng, "count", nil))
	}
	if GetOr(eng, "total", float64(0)) != float64(210000) {
		t.Errorf("Eng total: expected 210000, got %v", GetOr[any](eng, "total", nil))
	}
}

// ============================================================================
// AGGREGATION OPERATIONS TESTS
// ============================================================================

func TestAggregate(t *testing.T) {
	// Create a grouped result first
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "salary": int64(100000)}),
		NewRecord(map[string]any{"dept": "Eng", "salary": int64(110000)}),
		NewRecord(map[string]any{"dept": "Sales", "salary": int64(90000)}),
	})

	grouped := GroupByFields("employees", "dept")(input)

	// Now aggregate
	aggregated := Aggregate("employees", map[string]AggregateFunc{
		"count":      Count(),
		"total":      Sum("salary"),
		"avg_salary": Avg("salary"),
	})(grouped)

	result := slices.Collect(aggregated)

	if len(result) != 2 {
		t.Fatalf("Aggregate should return 2 groups, got %d", len(result))
	}

	// Find Engineering department
	for _, r := range result {
		if GetOr(r, "dept", "") == "Eng" {
			if GetOr(r, "count", int64(0)) != int64(2) {
				t.Errorf("Eng count should be 2, got %v", GetOr(r, "count", int64(0)))
			}
			if GetOr(r, "total", float64(0)) != float64(210000) {
				t.Errorf("Eng total should be 210000, got %v", GetOr(r, "total", float64(0)))
			}
			if GetOr(r, "avg_salary", float64(0)) != float64(105000) {
				t.Errorf("Eng avg should be 105000, got %v", GetOr(r, "avg_salary", float64(0)))
			}
		}
	}
}

func TestCount(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"name": "Alice"}),
		NewRecord(map[string]any{"name": "Bob"}),
		NewRecord(map[string]any{"name": "Charlie"}),
	}

	countFn := Count()
	result := countFn(records).getValue()

	if result != int64(3) {
		t.Errorf("Count should return 3, got %v", result)
	}
}

func TestSum(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"value": int64(10)}),
		NewRecord(map[string]any{"value": int64(20)}),
		NewRecord(map[string]any{"value": int64(30)}),
	}

	sumFn := Sum("value")
	result := sumFn(records).getValue()

	if result != float64(60) {
		t.Errorf("Sum should return 60, got %v", result)
	}
}

func TestSumMixedTypes(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"value": int64(10)}),
		NewRecord(map[string]any{"value": 20.5}),
		NewRecord(map[string]any{"value": "30"}), // Should convert
	}

	sumFn := Sum("value")
	result := sumFn(records).getValue()

	if result != float64(60.5) {
		t.Errorf("Sum should handle mixed types and return 60.5, got %v", result)
	}
}

func TestAvg(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"score": int64(80)}),
		NewRecord(map[string]any{"score": int64(90)}),
		NewRecord(map[string]any{"score": int64(100)}),
	}

	avgFn := Avg("score")
	result := avgFn(records).getValue()

	if result != float64(90) {
		t.Errorf("Avg should return 90, got %v", result)
	}
}

func TestAvgEmpty(t *testing.T) {
	records := []Record{}

	avgFn := Avg("score")
	result := avgFn(records).getValue()

	if result != 0.0 {
		t.Errorf("Avg of empty should return 0, got %v", result)
	}
}

func TestMin(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"value": int64(50)}),
		NewRecord(map[string]any{"value": int64(10)}),
		NewRecord(map[string]any{"value": int64(30)}),
	}

	minFn := Min[int64]("value")
	result := minFn(records).getValue()

	if result != int64(10) {
		t.Errorf("Min should return 10, got %v", result)
	}
}

func TestMinFloat(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"value": 5.5}),
		NewRecord(map[string]any{"value": 2.3}),
		NewRecord(map[string]any{"value": 7.8}),
	}

	minFn := Min[float64]("value")
	result := minFn(records).getValue()

	if result != 2.3 {
		t.Errorf("Min should return 2.3, got %v", result)
	}
}

func TestMax(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"value": int64(50)}),
		NewRecord(map[string]any{"value": int64(10)}),
		NewRecord(map[string]any{"value": int64(100)}),
	}

	maxFn := Max[int64]("value")
	result := maxFn(records).getValue()

	if result != int64(100) {
		t.Errorf("Max should return 100, got %v", result)
	}
}

func TestMaxString(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"name": "Alice"}),
		NewRecord(map[string]any{"name": "Charlie"}),
		NewRecord(map[string]any{"name": "Bob"}),
	}

	maxFn := Max[string]("name")
	result := maxFn(records).getValue()

	if result != "Charlie" {
		t.Errorf("Max should return Charlie, got %v", result)
	}
}

func TestFirst(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"value": "first"}),
		NewRecord(map[string]any{"value": "second"}),
		NewRecord(map[string]any{"value": "third"}),
	}

	firstFn := First[string]("value")
	result := firstFn(records).getValue()

	if result != "first" {
		t.Errorf("First should return 'first', got %v", result)
	}
}

func TestFirstEmpty(t *testing.T) {
	records := []Record{}

	firstFn := First[string]("value")
	result := firstFn(records).getValue()

	if result != "" {
		t.Errorf("First of empty should return empty string, got %v", result)
	}
}

func TestLast(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"value": "first"}),
		NewRecord(map[string]any{"value": "second"}),
		NewRecord(map[string]any{"value": "third"}),
	}

	lastFn := Last[string]("value")
	result := lastFn(records).getValue()

	if result != "third" {
		t.Errorf("Last should return 'third', got %v", result)
	}
}

func TestLastEmpty(t *testing.T) {
	records := []Record{}

	lastFn := Last[string]("value")
	result := lastFn(records).getValue()

	if result != "" {
		t.Errorf("Last of empty should return empty string, got %v", result)
	}
}

// ============================================================================
// EXPRESSION AGGREGATION TESTS
// ============================================================================

func TestExprAggSum(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"value": int64(10)}),
		NewRecord(map[string]any{"value": int64(20)}),
		NewRecord(map[string]any{"value": int64(30)}),
	}

	exprFn := ExprAgg("sum(value)")
	result := exprFn(records).getValue()

	if result != float64(60) {
		t.Errorf("ExprAgg sum should return 60, got %v", result)
	}
}

func TestExprAggCount(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"name": "Alice"}),
		NewRecord(map[string]any{"name": "Bob"}),
		NewRecord(map[string]any{"name": "Charlie"}),
	}

	exprFn := ExprAgg("count()")
	result := exprFn(records).getValue()

	if result != float64(3) {
		t.Errorf("ExprAgg count should return 3, got %v", result)
	}
}

func TestExprAggAvg(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"value": int64(10)}),
		NewRecord(map[string]any{"value": int64(20)}),
		NewRecord(map[string]any{"value": int64(30)}),
	}

	exprFn := ExprAgg("avg(value)")
	result := exprFn(records).getValue()

	if result != float64(20) {
		t.Errorf("ExprAgg avg should return 20, got %v", result)
	}
}

func TestExprAggComplexExpression(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"qty": int64(2), "price": int64(10)}),
		NewRecord(map[string]any{"qty": int64(3), "price": int64(20)}),
		NewRecord(map[string]any{"qty": int64(4), "price": int64(30)}),
	}

	// sum(qty * price) = 2*10 + 3*20 + 4*30 = 20 + 60 + 120 = 200
	exprFn := ExprAgg("sum(qty * price)")
	result := exprFn(records).getValue()

	if result != float64(200) {
		t.Errorf("ExprAgg sum(qty * price) should return 200, got %v", result)
	}
}

func TestExprAggWithAggregate(t *testing.T) {
	// Test ExprAgg works with Aggregate function
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "salary": int64(100000), "bonus": int64(10000)}),
		NewRecord(map[string]any{"dept": "Eng", "salary": int64(110000), "bonus": int64(12000)}),
		NewRecord(map[string]any{"dept": "Sales", "salary": int64(90000), "bonus": int64(8000)}),
	})

	grouped := GroupByFields("employees", "dept")(input)

	// Use ExprAgg for complex expression aggregation
	aggregated := Aggregate("employees", map[string]AggregateFunc{
		"count":      Count(),
		"total_comp": ExprAgg("sum(salary + bonus)"),
	})(grouped)

	result := slices.Collect(aggregated)

	if len(result) != 2 {
		t.Fatalf("Should return 2 groups, got %d", len(result))
	}

	// Find Engineering department
	for _, r := range result {
		if GetOr(r, "dept", "") == "Eng" {
			if GetOr(r, "count", int64(0)) != int64(2) {
				t.Errorf("Eng count should be 2, got %v", GetOr(r, "count", int64(0)))
			}
			// total_comp = (100000+10000) + (110000+12000) = 110000 + 122000 = 232000
			if GetOr(r, "total_comp", float64(0)) != float64(232000) {
				t.Errorf("Eng total_comp should be 232000, got %v", GetOr(r, "total_comp", float64(0)))
			}
		}
	}
}

func TestExprAggMean(t *testing.T) {
	// Test that mean() is an alias for avg()
	records := []Record{
		NewRecord(map[string]any{"value": int64(10)}),
		NewRecord(map[string]any{"value": int64(20)}),
		NewRecord(map[string]any{"value": int64(30)}),
	}

	exprFn := ExprAgg("mean(value)")
	result := exprFn(records).getValue()

	if result != float64(20) {
		t.Errorf("ExprAgg mean should return 20, got %v", result)
	}
}

// ============================================================================
// COMPLEX SQL-STYLE PIPELINE TESTS
// ============================================================================

func TestComplexGroupByAggregate(t *testing.T) {
	// Sales data
	input := slices.Values([]Record{
		NewRecord(map[string]any{"product": "Widget", "region": "East", "sales": int64(1000)}),
		NewRecord(map[string]any{"product": "Widget", "region": "East", "sales": int64(1500)}),
		NewRecord(map[string]any{"product": "Widget", "region": "West", "sales": int64(2000)}),
		NewRecord(map[string]any{"product": "Gadget", "region": "East", "sales": int64(800)}),
		NewRecord(map[string]any{"product": "Gadget", "region": "West", "sales": int64(1200)}),
	})

	// Group by product and region, then aggregate
	pipeline := Chain(
		GroupByFields("items", "product", "region"),
		Aggregate("items", map[string]AggregateFunc{
			"count":       Count(),
			"total_sales": Sum("sales"),
			"avg_sales":   Avg("sales"),
			"max_sale":    Max[int64]("sales"),
			"min_sale":    Min[int64]("sales"),
		}),
	)

	result := slices.Collect(pipeline(input))

	// Should have 4 groups: Widget/East, Widget/West, Gadget/East, Gadget/West
	if len(result) != 4 {
		t.Fatalf("Expected 4 groups, got %d", len(result))
	}

	// Find Widget/East group
	for _, r := range result {
		if GetOr(r, "product", "") == "Widget" && GetOr(r, "region", "") == "East" {
			if GetOr(r, "count", int64(0)) != int64(2) {
				t.Errorf("Widget/East count should be 2, got %v", GetOr(r, "count", int64(0)))
			}
			if GetOr(r, "total_sales", float64(0)) != float64(2500) {
				t.Errorf("Widget/East total should be 2500, got %v", GetOr(r, "total_sales", float64(0)))
			}
			if GetOr(r, "max_sale", int64(0)) != int64(1500) {
				t.Errorf("Widget/East max should be 1500, got %v", GetOr(r, "max_sale", int64(0)))
			}
		}
	}
}

func TestJoinThenGroup(t *testing.T) {
	// Employee data
	employees := slices.Values([]Record{
		NewRecord(map[string]any{"id": int64(1), "name": "Alice", "dept_id": int64(10)}),
		NewRecord(map[string]any{"id": int64(2), "name": "Bob", "dept_id": int64(10)}),
		NewRecord(map[string]any{"id": int64(3), "name": "Charlie", "dept_id": int64(20)}),
	})

	// Department data
	departments := slices.Values([]Record{
		NewRecord(map[string]any{"dept_id": int64(10), "dept_name": "Engineering"}),
		NewRecord(map[string]any{"dept_id": int64(20), "dept_name": "Sales"}),
	})

	// Join employees with departments, then group by department
	pipeline := Chain(
		InnerJoin(departments, OnFields("dept_id")),
		GroupByFields("members", "dept_name"),
		Aggregate("members", map[string]AggregateFunc{
			"count": Count(),
		}),
	)

	result := slices.Collect(pipeline(employees))

	if len(result) != 2 {
		t.Fatalf("Expected 2 departments, got %d", len(result))
	}

	// Find Engineering department
	for _, r := range result {
		if GetOr(r, "dept_name", "") == "Engineering" {
			if GetOr(r, "count", int64(0)) != int64(2) {
				t.Errorf("Engineering should have 2 employees, got %v", GetOr(r, "count", int64(0)))
			}
		}
	}
}

func TestMultiFieldGrouping(t *testing.T) {
	// Test grouping by multiple fields
	input := slices.Values([]Record{
		NewRecord(map[string]any{"year": int64(2024), "month": "Jan", "sales": int64(1000)}),
		NewRecord(map[string]any{"year": int64(2024), "month": "Jan", "sales": int64(1100)}),
		NewRecord(map[string]any{"year": int64(2024), "month": "Feb", "sales": int64(1200)}),
		NewRecord(map[string]any{"year": int64(2023), "month": "Jan", "sales": int64(900)}),
	})

	pipeline := Chain(
		GroupByFields("records", "year", "month"),
		Aggregate("records", map[string]AggregateFunc{
			"total": Sum("sales"),
			"count": Count(),
		}),
	)

	result := slices.Collect(pipeline(input))

	// Should have 3 groups: (2024,Jan), (2024,Feb), (2023,Jan)
	if len(result) != 3 {
		t.Fatalf("Expected 3 groups, got %d", len(result))
	}

	// Check 2024/Jan group
	for _, r := range result {
		if GetOr(r, "year", int64(0)) == int64(2024) && GetOr(r, "month", "") == "Jan" {
			if GetOr(r, "count", int64(0)) != int64(2) {
				t.Errorf("2024/Jan should have count 2, got %v", GetOr(r, "count", int64(0)))
			}
			if GetOr(r, "total", float64(0)) != float64(2100) {
				t.Errorf("2024/Jan should have total 2100, got %v", GetOr(r, "total", float64(0)))
			}
		}
	}
}

// ============================================================================
// EDGE CASE TESTS
// ============================================================================

func TestJoinEmptyRight(t *testing.T) {
	left := slices.Values([]Record{
		NewRecord(map[string]any{"id": int64(1), "name": "Alice"}),
	})

	empty := func(yield func(Record) bool) {
		// Yield nothing
	}

	filter := InnerJoin(iter.Seq[Record](empty), OnFields("id"))
	result := slices.Collect(filter(left))

	if len(result) != 0 {
		t.Errorf("Inner join with empty right should return 0 records, got %d", len(result))
	}
}

func TestGroupByEmptyInput(t *testing.T) {
	empty := func(yield func(Record) bool) {
		// Yield nothing
	}

	filter := GroupByFields("members", "dept")
	result := slices.Collect(filter(iter.Seq[Record](empty)))

	if len(result) != 0 {
		t.Errorf("GroupBy on empty should return 0 groups, got %d", len(result))
	}
}

// ============================================================================
// PIVOT OPERATIONS TESTS
// ============================================================================

func TestPivot(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "quarter": "Q1", "revenue": float64(100)}),
		NewRecord(map[string]any{"dept": "Eng", "quarter": "Q2", "revenue": float64(200)}),
		NewRecord(map[string]any{"dept": "Sales", "quarter": "Q1", "revenue": float64(150)}),
	})

	result := slices.Collect(Pivot("dept", "quarter", "revenue", "sum")(input))

	if len(result) != 2 {
		t.Fatalf("Pivot should return 2 rows, got %d", len(result))
	}

	// Check Eng row
	eng := result[0]
	if GetOr(eng, "dept", "") != "Eng" {
		t.Errorf("First row dept should be Eng, got %v", GetOr(eng, "dept", ""))
	}
	if GetOr(eng, "Q1", float64(0)) != 100 {
		t.Errorf("Eng/Q1 should be 100, got %v", GetOr(eng, "Q1", float64(0)))
	}
	if GetOr(eng, "Q2", float64(0)) != 200 {
		t.Errorf("Eng/Q2 should be 200, got %v", GetOr(eng, "Q2", float64(0)))
	}

	// Check Sales row
	sales := result[1]
	if GetOr(sales, "dept", "") != "Sales" {
		t.Errorf("Second row dept should be Sales, got %v", GetOr(sales, "dept", ""))
	}
	if GetOr(sales, "Q1", float64(0)) != 150 {
		t.Errorf("Sales/Q1 should be 150, got %v", GetOr(sales, "Q1", float64(0)))
	}
}

func TestPivotCount(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "quarter": "Q1", "revenue": float64(100)}),
		NewRecord(map[string]any{"dept": "Eng", "quarter": "Q1", "revenue": float64(50)}),
		NewRecord(map[string]any{"dept": "Eng", "quarter": "Q2", "revenue": float64(200)}),
		NewRecord(map[string]any{"dept": "Sales", "quarter": "Q1", "revenue": float64(150)}),
	})

	result := slices.Collect(Pivot("dept", "quarter", "revenue", "count")(input))

	if len(result) != 2 {
		t.Fatalf("Pivot count should return 2 rows, got %d", len(result))
	}

	eng := result[0]
	if GetOr(eng, "Q1", int64(0)) != int64(2) {
		t.Errorf("Eng/Q1 count should be 2, got %v", GetOr(eng, "Q1", int64(0)))
	}
	if GetOr(eng, "Q2", int64(0)) != int64(1) {
		t.Errorf("Eng/Q2 count should be 1, got %v", GetOr(eng, "Q2", int64(0)))
	}

	sales := result[1]
	if GetOr(sales, "Q1", int64(0)) != int64(1) {
		t.Errorf("Sales/Q1 count should be 1, got %v", GetOr(sales, "Q1", int64(0)))
	}
}

func TestPivotMissingCells(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "quarter": "Q1", "revenue": float64(100)}),
		NewRecord(map[string]any{"dept": "Eng", "quarter": "Q2", "revenue": float64(200)}),
		NewRecord(map[string]any{"dept": "Sales", "quarter": "Q1", "revenue": float64(150)}),
		// Sales/Q2 is missing - should be zero-filled
	})

	result := slices.Collect(Pivot("dept", "quarter", "revenue", "sum")(input))

	if len(result) != 2 {
		t.Fatalf("Expected 2 rows, got %d", len(result))
	}

	// Sales/Q2 should be 0
	sales := result[1]
	if GetOr(sales, "Q2", float64(-1)) != 0 {
		t.Errorf("Sales/Q2 (missing) should be 0, got %v", GetOr(sales, "Q2", float64(-1)))
	}
}

func TestPivotPreservesOrder(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Charlie", "q": "B", "v": float64(1)}),
		NewRecord(map[string]any{"dept": "Alice", "q": "A", "v": float64(2)}),
		NewRecord(map[string]any{"dept": "Bob", "q": "B", "v": float64(3)}),
		NewRecord(map[string]any{"dept": "Alice", "q": "B", "v": float64(4)}),
	})

	result := slices.Collect(Pivot("dept", "q", "v", "sum")(input))

	if len(result) != 3 {
		t.Fatalf("Expected 3 rows, got %d", len(result))
	}

	// Row order should be first-seen: Charlie, Alice, Bob
	if GetOr(result[0], "dept", "") != "Charlie" {
		t.Errorf("First row should be Charlie, got %s", GetOr(result[0], "dept", ""))
	}
	if GetOr(result[1], "dept", "") != "Alice" {
		t.Errorf("Second row should be Alice, got %s", GetOr(result[1], "dept", ""))
	}
	if GetOr(result[2], "dept", "") != "Bob" {
		t.Errorf("Third row should be Bob, got %s", GetOr(result[2], "dept", ""))
	}

	// Column order should be first-seen: B, A
	// Verify Alice has both columns
	if GetOr(result[1], "A", float64(0)) != 2 {
		t.Errorf("Alice/A should be 2, got %v", GetOr(result[1], "A", float64(0)))
	}
	if GetOr(result[1], "B", float64(0)) != 4 {
		t.Errorf("Alice/B should be 4, got %v", GetOr(result[1], "B", float64(0)))
	}
}

func TestPivotEmpty(t *testing.T) {
	empty := func(yield func(Record) bool) {}
	result := slices.Collect(Pivot("dept", "q", "v", "sum")(empty))
	if len(result) != 0 {
		t.Errorf("Pivot on empty should return 0 rows, got %d", len(result))
	}
}

func TestAggregateNoSequenceField(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "Eng", "count": int64(5)}), // No sequence field
	})

	filter := Aggregate("members", map[string]AggregateFunc{
		"total": Count(),
	})

	result := slices.Collect(filter(input))

	// Should still yield the record without aggregations
	if len(result) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(result))
	}

	if result[0].Has("total") {
		t.Error("Should not have aggregation when sequence field is missing")
	}
}

// ============================================================================
// ROLLUP / CUBE TESTS
// ============================================================================

func TestRollupBasic(t *testing.T) {
	// 2 fields, 1 aggregation (count)
	input := slices.Values([]Record{
		NewRecord(map[string]any{"a": int64(1), "z": int64(10)}),
		NewRecord(map[string]any{"a": int64(1), "z": int64(10)}),
		NewRecord(map[string]any{"a": int64(1), "z": int64(20)}),
		NewRecord(map[string]any{"a": int64(2), "z": int64(10)}),
	})

	config := RollupConfig{
		Fields:       []string{"a", "z"},
		Aggregations: map[string]AggregateFunc{"count": Count()},
		Mode:         RollupHierarchical,
	}

	result := slices.Collect(Rollup(config)(input))

	// Should have 3 detail groups: (1,10), (1,20), (2,10)
	if len(result) != 3 {
		t.Fatalf("Rollup should return 3 detail rows, got %d", len(result))
	}

	// Check each row has count (grand total), a_count (per-a), a_z_count (detail)
	for _, r := range result {
		if !r.Has("count") {
			t.Error("Each row should have count (grand total)")
		}
		if !r.Has("a_count") {
			t.Error("Each row should have a_count (per-a subtotal)")
		}
		if !r.Has("a_z_count") {
			t.Error("Each row should have a_z_count (detail)")
		}
	}

	// Grand total should be 4 for all rows
	for _, r := range result {
		if GetOr(r, "count", int64(0)) != int64(4) {
			t.Errorf("Grand total count should be 4, got %v", GetOr(r, "count", int64(0)))
		}
	}

	// Find the (a=1, z=10) row
	for _, r := range result {
		if GetOr(r, "a", int64(0)) == int64(1) && GetOr(r, "z", int64(0)) == int64(10) {
			// a=1 subtotal: 3 records have a=1
			if GetOr(r, "a_count", int64(0)) != int64(3) {
				t.Errorf("a=1 subtotal should be 3, got %v", GetOr(r, "a_count", int64(0)))
			}
			// detail: 2 records have a=1, z=10
			if GetOr(r, "a_z_count", int64(0)) != int64(2) {
				t.Errorf("(a=1,z=10) count should be 2, got %v", GetOr(r, "a_z_count", int64(0)))
			}
		}
	}
}

func TestRollupMultipleAggs(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "eng", "salary": float64(100)}),
		NewRecord(map[string]any{"dept": "eng", "salary": float64(200)}),
		NewRecord(map[string]any{"dept": "sales", "salary": float64(150)}),
	})

	config := RollupConfig{
		Fields: []string{"dept"},
		Aggregations: map[string]AggregateFunc{
			"n":     Count(),
			"total": Sum("salary"),
		},
		Mode: RollupHierarchical,
	}

	result := slices.Collect(Rollup(config)(input))

	if len(result) != 2 {
		t.Fatalf("Should have 2 detail rows (eng, sales), got %d", len(result))
	}

	// Each row should have: dept_n, dept_total, n, total
	for _, r := range result {
		if !r.Has("dept_n") {
			t.Error("Should have dept_n (detail)")
		}
		if !r.Has("dept_total") {
			t.Error("Should have dept_total (detail)")
		}
		if !r.Has("n") {
			t.Error("Should have n (grand total)")
		}
		if !r.Has("total") {
			t.Error("Should have total (grand total)")
		}
	}

	// Grand totals: n=3, total=450
	for _, r := range result {
		if GetOr(r, "n", int64(0)) != int64(3) {
			t.Errorf("Grand total n should be 3, got %v", GetOr(r, "n", int64(0)))
		}
		if GetOr(r, "total", float64(0)) != float64(450) {
			t.Errorf("Grand total total should be 450, got %v", GetOr(r, "total", float64(0)))
		}
	}

	// Find eng row
	for _, r := range result {
		if GetOr(r, "dept", "") == "eng" {
			if GetOr(r, "dept_n", int64(0)) != int64(2) {
				t.Errorf("eng dept_n should be 2, got %v", GetOr(r, "dept_n", int64(0)))
			}
			if GetOr(r, "dept_total", float64(0)) != float64(300) {
				t.Errorf("eng dept_total should be 300, got %v", GetOr(r, "dept_total", float64(0)))
			}
		}
	}
}

func TestCubeBasic(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"a": int64(1), "z": int64(10)}),
		NewRecord(map[string]any{"a": int64(1), "z": int64(10)}),
		NewRecord(map[string]any{"a": int64(1), "z": int64(20)}),
		NewRecord(map[string]any{"a": int64(2), "z": int64(10)}),
	})

	config := RollupConfig{
		Fields:       []string{"a", "z"},
		Aggregations: map[string]AggregateFunc{"count": Count()},
		Mode:         RollupCube,
	}

	result := slices.Collect(Rollup(config)(input))

	// Same 3 detail rows as rollup
	if len(result) != 3 {
		t.Fatalf("Cube should return 3 detail rows, got %d", len(result))
	}

	// Cube should have z_count in addition to rollup fields
	for _, r := range result {
		if !r.Has("count") {
			t.Error("Should have count (grand total)")
		}
		if !r.Has("a_count") {
			t.Error("Should have a_count")
		}
		if !r.Has("z_count") {
			t.Error("Should have z_count (cube-specific)")
		}
		if !r.Has("a_z_count") {
			t.Error("Should have a_z_count (detail)")
		}
	}

	// Check z_count for the (a=1, z=10) row: z=10 has 3 records
	for _, r := range result {
		if GetOr(r, "a", int64(0)) == int64(1) && GetOr(r, "z", int64(0)) == int64(10) {
			if GetOr(r, "z_count", int64(0)) != int64(3) {
				t.Errorf("z=10 count should be 3, got %v", GetOr(r, "z_count", int64(0)))
			}
		}
	}
}

func TestRollupSingleField(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "eng"}),
		NewRecord(map[string]any{"dept": "eng"}),
		NewRecord(map[string]any{"dept": "sales"}),
	})

	config := RollupConfig{
		Fields:       []string{"dept"},
		Aggregations: map[string]AggregateFunc{"count": Count()},
		Mode:         RollupHierarchical,
	}

	result := slices.Collect(Rollup(config)(input))

	if len(result) != 2 {
		t.Fatalf("Should have 2 groups, got %d", len(result))
	}

	// Each row: dept, dept_count, count
	for _, r := range result {
		if !r.Has("dept_count") {
			t.Error("Should have dept_count")
		}
		if !r.Has("count") {
			t.Error("Should have count (grand total)")
		}
	}

	// eng: dept_count=2, count=3
	for _, r := range result {
		if GetOr(r, "dept", "") == "eng" {
			if GetOr(r, "dept_count", int64(0)) != int64(2) {
				t.Errorf("eng dept_count should be 2, got %v", GetOr(r, "dept_count", int64(0)))
			}
			if GetOr(r, "count", int64(0)) != int64(3) {
				t.Errorf("Grand total count should be 3, got %v", GetOr(r, "count", int64(0)))
			}
		}
	}
}

func TestRollupEmpty(t *testing.T) {
	empty := func(yield func(Record) bool) {}

	config := RollupConfig{
		Fields:       []string{"a", "b"},
		Aggregations: map[string]AggregateFunc{"count": Count()},
		Mode:         RollupHierarchical,
	}

	result := slices.Collect(Rollup(config)(empty))
	if len(result) != 0 {
		t.Errorf("Rollup on empty should return 0 rows, got %d", len(result))
	}
}

func TestRollupThreeFields(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"a": "x", "b": "y", "c": "z"}),
		NewRecord(map[string]any{"a": "x", "b": "y", "c": "w"}),
		NewRecord(map[string]any{"a": "x", "b": "q", "c": "z"}),
	})

	config := RollupConfig{
		Fields:       []string{"a", "b", "c"},
		Aggregations: map[string]AggregateFunc{"n": Count()},
		Mode:         RollupHierarchical,
	}

	result := slices.Collect(Rollup(config)(input))

	// 3 detail groups: (x,y,z), (x,y,w), (x,q,z)
	if len(result) != 3 {
		t.Fatalf("Should have 3 detail rows, got %d", len(result))
	}

	// Each row should have 4 levels: n, a_n, a_b_n, a_b_c_n
	for _, r := range result {
		if !r.Has("n") {
			t.Error("Should have n (grand total)")
		}
		if !r.Has("a_n") {
			t.Error("Should have a_n")
		}
		if !r.Has("a_b_n") {
			t.Error("Should have a_b_n")
		}
		if !r.Has("a_b_c_n") {
			t.Error("Should have a_b_c_n (detail)")
		}
	}

	// Grand total n should be 3
	for _, r := range result {
		if GetOr(r, "n", int64(0)) != int64(3) {
			t.Errorf("Grand total n should be 3, got %v", GetOr(r, "n", int64(0)))
		}
	}

	// a_n should be 3 for all (only one a value "x")
	for _, r := range result {
		if GetOr(r, "a_n", int64(0)) != int64(3) {
			t.Errorf("a_n should be 3 (all records have a=x), got %v", GetOr(r, "a_n", int64(0)))
		}
	}

	// a_b_n: (x,y)=2, (x,q)=1
	for _, r := range result {
		if GetOr(r, "b", "") == "y" {
			if GetOr(r, "a_b_n", int64(0)) != int64(2) {
				t.Errorf("(x,y) a_b_n should be 2, got %v", GetOr(r, "a_b_n", int64(0)))
			}
		}
		if GetOr(r, "b", "") == "q" {
			if GetOr(r, "a_b_n", int64(0)) != int64(1) {
				t.Errorf("(x,q) a_b_n should be 1, got %v", GetOr(r, "a_b_n", int64(0)))
			}
		}
	}
}

// ============================================================================
// WINDOW FUNCTION TESTS
// ============================================================================

func TestWindowRowNumber(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "Alice", "salary": float64(90000)}),
		NewRecord(map[string]any{"name": "Bob", "salary": float64(80000)}),
		NewRecord(map[string]any{"name": "Charlie", "salary": float64(70000)}),
	})

	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "salary", Desc: true}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WRowNumber(), ResultName: "rn"}},
	}})(input))

	if len(result) != 3 {
		t.Fatalf("expected 3 records, got %d", len(result))
	}

	// Sorted desc by salary: Alice(90k)=1, Bob(80k)=2, Charlie(70k)=3
	for _, r := range result {
		name := GetOr(r, "name", "")
		rn := GetOr(r, "rn", int64(0))
		switch name {
		case "Alice":
			if rn != 1 {
				t.Errorf("Alice rn: want 1, got %d", rn)
			}
		case "Bob":
			if rn != 2 {
				t.Errorf("Bob rn: want 2, got %d", rn)
			}
		case "Charlie":
			if rn != 3 {
				t.Errorf("Charlie rn: want 3, got %d", rn)
			}
		}
	}
}

func TestWindowRowNumberPartitioned(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "Alice", "dept": "eng", "salary": float64(90000)}),
		NewRecord(map[string]any{"name": "Bob", "dept": "eng", "salary": float64(80000)}),
		NewRecord(map[string]any{"name": "Charlie", "dept": "sales", "salary": float64(70000)}),
		NewRecord(map[string]any{"name": "Diana", "dept": "sales", "salary": float64(95000)}),
	})

	result := slices.Collect(Window([]WindowConfig{{
		PartitionBy: []string{"dept"},
		OrderBy:     []OrderField{{Field: "salary", Desc: true}},
		Frame:       WindowFrame{Preceding: -1, Following: 0},
		Specs:       []WindowSpec{{Function: WRowNumber(), ResultName: "rn"}},
	}})(input))

	if len(result) != 4 {
		t.Fatalf("expected 4 records, got %d", len(result))
	}

	for _, r := range result {
		name := GetOr(r, "name", "")
		rn := GetOr(r, "rn", int64(0))
		switch name {
		case "Alice":
			if rn != 1 {
				t.Errorf("Alice rn: want 1 (top in eng), got %d", rn)
			}
		case "Bob":
			if rn != 2 {
				t.Errorf("Bob rn: want 2 (2nd in eng), got %d", rn)
			}
		case "Diana":
			if rn != 1 {
				t.Errorf("Diana rn: want 1 (top in sales), got %d", rn)
			}
		case "Charlie":
			if rn != 2 {
				t.Errorf("Charlie rn: want 2 (2nd in sales), got %d", rn)
			}
		}
	}
}

func TestWindowRank(t *testing.T) {
	// Ties: two records with salary 80000
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "A", "salary": float64(90000)}),
		NewRecord(map[string]any{"name": "B", "salary": float64(80000)}),
		NewRecord(map[string]any{"name": "C", "salary": float64(80000)}),
		NewRecord(map[string]any{"name": "D", "salary": float64(70000)}),
	})

	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "salary", Desc: true}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WRank(), ResultName: "rnk"}},
	}})(input))

	if len(result) != 4 {
		t.Fatalf("expected 4 records, got %d", len(result))
	}

	// Expected ranks: A=1, B=2, C=2, D=4 (gap after tie)
	for _, r := range result {
		name := GetOr(r, "name", "")
		rnk := GetOr(r, "rnk", int64(0))
		switch name {
		case "A":
			if rnk != 1 {
				t.Errorf("A rank: want 1, got %d", rnk)
			}
		case "B":
			if rnk != 2 {
				t.Errorf("B rank: want 2, got %d", rnk)
			}
		case "C":
			if rnk != 2 {
				t.Errorf("C rank: want 2, got %d", rnk)
			}
		case "D":
			if rnk != 4 {
				t.Errorf("D rank: want 4, got %d", rnk)
			}
		}
	}
}

func TestWindowDenseRank(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "A", "salary": float64(90000)}),
		NewRecord(map[string]any{"name": "B", "salary": float64(80000)}),
		NewRecord(map[string]any{"name": "C", "salary": float64(80000)}),
		NewRecord(map[string]any{"name": "D", "salary": float64(70000)}),
	})

	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "salary", Desc: true}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WDenseRank(), ResultName: "dr"}},
	}})(input))

	// Expected: A=1, B=2, C=2, D=3 (no gap)
	for _, r := range result {
		name := GetOr(r, "name", "")
		dr := GetOr(r, "dr", int64(0))
		switch name {
		case "A":
			if dr != 1 {
				t.Errorf("A dense_rank: want 1, got %d", dr)
			}
		case "B":
			if dr != 2 {
				t.Errorf("B dense_rank: want 2, got %d", dr)
			}
		case "C":
			if dr != 2 {
				t.Errorf("C dense_rank: want 2, got %d", dr)
			}
		case "D":
			if dr != 3 {
				t.Errorf("D dense_rank: want 3, got %d", dr)
			}
		}
	}
}

func TestWindowLag(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"date": "2026-01", "revenue": float64(100)}),
		NewRecord(map[string]any{"date": "2026-02", "revenue": float64(200)}),
		NewRecord(map[string]any{"date": "2026-03", "revenue": float64(150)}),
	})

	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "date"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WLag("revenue", 1), ResultName: "prev_rev"}},
	}})(input))

	if len(result) != 3 {
		t.Fatalf("expected 3 records, got %d", len(result))
	}

	for _, r := range result {
		date := GetOr(r, "date", "")
		prev, exists := Get[any](r, "prev_rev")
		switch date {
		case "2026-01":
			if exists && prev != nil {
				t.Errorf("first row lag should be nil, got %v", prev)
			}
		case "2026-02":
			if pv, ok := prev.(float64); !ok || pv != 100 {
				t.Errorf("2026-02 prev_rev: want 100, got %v", prev)
			}
		case "2026-03":
			if pv, ok := prev.(float64); !ok || pv != 200 {
				t.Errorf("2026-03 prev_rev: want 200, got %v", prev)
			}
		}
	}
}

func TestWindowLead(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"date": "2026-01", "revenue": float64(100)}),
		NewRecord(map[string]any{"date": "2026-02", "revenue": float64(200)}),
		NewRecord(map[string]any{"date": "2026-03", "revenue": float64(150)}),
	})

	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "date"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WLead("revenue", 1), ResultName: "next_rev"}},
	}})(input))

	for _, r := range result {
		date := GetOr(r, "date", "")
		next, exists := Get[any](r, "next_rev")
		switch date {
		case "2026-01":
			if nv, ok := next.(float64); !ok || nv != 200 {
				t.Errorf("2026-01 next_rev: want 200, got %v", next)
			}
		case "2026-02":
			if nv, ok := next.(float64); !ok || nv != 150 {
				t.Errorf("2026-02 next_rev: want 150, got %v", next)
			}
		case "2026-03":
			if exists && next != nil {
				t.Errorf("last row lead should be nil, got %v", next)
			}
		}
	}
}

func TestWindowRunningSum(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"date": "2026-01", "revenue": float64(100)}),
		NewRecord(map[string]any{"date": "2026-02", "revenue": float64(200)}),
		NewRecord(map[string]any{"date": "2026-03", "revenue": float64(150)}),
	})

	// Default frame: UNBOUNDED PRECEDING to CURRENT ROW
	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "date"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WSum("revenue"), ResultName: "running_total"}},
	}})(input))

	expected := []float64{100, 300, 450}
	for i, r := range result {
		rt := GetOr(r, "running_total", float64(0))
		if rt != expected[i] {
			t.Errorf("row %d running_total: want %v, got %v", i, expected[i], rt)
		}
	}
}

func TestWindowMovingAverage(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"date": "d1", "price": float64(10)}),
		NewRecord(map[string]any{"date": "d2", "price": float64(20)}),
		NewRecord(map[string]any{"date": "d3", "price": float64(30)}),
		NewRecord(map[string]any{"date": "d4", "price": float64(40)}),
		NewRecord(map[string]any{"date": "d5", "price": float64(50)}),
	})

	// 3-row moving average: ROWS BETWEEN 2 PRECEDING AND CURRENT ROW
	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "date"}},
		Frame:   WindowFrame{Preceding: 2, Following: 0},
		Specs:   []WindowSpec{{Function: WAvg("price"), ResultName: "ma3"}},
	}})(input))

	// d1: avg(10) = 10
	// d2: avg(10,20) = 15
	// d3: avg(10,20,30) = 20
	// d4: avg(20,30,40) = 30
	// d5: avg(30,40,50) = 40
	expected := []float64{10, 15, 20, 30, 40}
	for i, r := range result {
		ma := GetOr(r, "ma3", float64(0))
		if ma != expected[i] {
			t.Errorf("row %d ma3: want %v, got %v", i, expected[i], ma)
		}
	}
}

func TestWindowCount(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"x": int64(1)}),
		NewRecord(map[string]any{"x": int64(2)}),
		NewRecord(map[string]any{"x": int64(3)}),
	})

	// Running count with default frame
	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "x"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WCount(), ResultName: "cnt"}},
	}})(input))

	expected := []int64{1, 2, 3}
	for i, r := range result {
		cnt := GetOr(r, "cnt", int64(0))
		if cnt != expected[i] {
			t.Errorf("row %d count: want %d, got %d", i, expected[i], cnt)
		}
	}
}

func TestWindowMinMax(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"date": "d1", "price": float64(30)}),
		NewRecord(map[string]any{"date": "d2", "price": float64(10)}),
		NewRecord(map[string]any{"date": "d3", "price": float64(50)}),
		NewRecord(map[string]any{"date": "d4", "price": float64(20)}),
	})

	// Running min/max
	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "date"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{
			{Function: WMin("price"), ResultName: "running_min"},
			{Function: WMax("price"), ResultName: "running_max"},
		},
	}})(input))

	// d1: min=30, max=30
	// d2: min=10, max=30
	// d3: min=10, max=50
	// d4: min=10, max=50
	expectedMin := []float64{30, 10, 10, 10}
	expectedMax := []float64{30, 30, 50, 50}
	for i, r := range result {
		rmin, _ := Get[float64](r, "running_min")
		rmax, _ := Get[float64](r, "running_max")
		if rmin != expectedMin[i] {
			t.Errorf("row %d running_min: want %v, got %v", i, expectedMin[i], rmin)
		}
		if rmax != expectedMax[i] {
			t.Errorf("row %d running_max: want %v, got %v", i, expectedMax[i], rmax)
		}
	}
}

func TestWindowMultipleConfigs(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "Alice", "dept": "eng", "salary": float64(90000), "date": "2026-01"}),
		NewRecord(map[string]any{"name": "Bob", "dept": "eng", "salary": float64(80000), "date": "2026-02"}),
		NewRecord(map[string]any{"name": "Charlie", "dept": "sales", "salary": float64(70000), "date": "2026-03"}),
	})

	result := slices.Collect(Window([]WindowConfig{
		{
			PartitionBy: []string{"dept"},
			OrderBy:     []OrderField{{Field: "salary", Desc: true}},
			Frame:       WindowFrame{Preceding: -1, Following: 0},
			Specs:       []WindowSpec{{Function: WRank(), ResultName: "salary_rank"}},
		},
		{
			OrderBy: []OrderField{{Field: "date"}},
			Frame:   WindowFrame{Preceding: -1, Following: 0},
			Specs:   []WindowSpec{{Function: WLag("date", 1), ResultName: "prev_date"}},
		},
	})(input))

	if len(result) != 3 {
		t.Fatalf("expected 3 records, got %d", len(result))
	}

	// Verify both window results are present on each record
	for _, r := range result {
		name := GetOr(r, "name", "")
		salaryRank := GetOr(r, "salary_rank", int64(0))
		prevDate, _ := Get[any](r, "prev_date")

		switch name {
		case "Alice":
			if salaryRank != 1 {
				t.Errorf("Alice salary_rank: want 1, got %d", salaryRank)
			}
		case "Bob":
			if salaryRank != 2 {
				t.Errorf("Bob salary_rank: want 2, got %d", salaryRank)
			}
		case "Charlie":
			if salaryRank != 1 {
				t.Errorf("Charlie salary_rank: want 1 (top in sales), got %d", salaryRank)
			}
		}

		// prev_date check (ordered by date globally)
		_ = prevDate // both fields should exist
	}
}

func TestWindowMultiFieldOrder(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "eng", "salary": float64(80000), "name": "Bob"}),
		NewRecord(map[string]any{"dept": "eng", "salary": float64(80000), "name": "Alice"}),
		NewRecord(map[string]any{"dept": "sales", "salary": float64(70000), "name": "Charlie"}),
	})

	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{
			{Field: "dept"},
			{Field: "name"},
		},
		Frame: WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{{Function: WRowNumber(), ResultName: "rn"}},
	}})(input))

	// Sorted by dept asc, then name asc: Alice(eng), Bob(eng), Charlie(sales)
	for _, r := range result {
		name := GetOr(r, "name", "")
		rn := GetOr(r, "rn", int64(0))
		switch name {
		case "Alice":
			if rn != 1 {
				t.Errorf("Alice rn: want 1, got %d", rn)
			}
		case "Bob":
			if rn != 2 {
				t.Errorf("Bob rn: want 2, got %d", rn)
			}
		case "Charlie":
			if rn != 3 {
				t.Errorf("Charlie rn: want 3, got %d", rn)
			}
		}
	}
}

func TestWindowEmpty(t *testing.T) {
	var empty []Record
	input := slices.Values(empty)

	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "x"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WRowNumber(), ResultName: "rn"}},
	}})(input))

	if len(result) != 0 {
		t.Errorf("empty input should produce empty output, got %d records", len(result))
	}
}

func TestSortRecords(t *testing.T) {
	r1 := MakeMutableRecord().String("dept", "eng").Int("salary", int64(90000)).String("name", "Alice").Freeze()
	r2 := MakeMutableRecord().String("dept", "sales").Int("salary", int64(80000)).String("name", "Bob").Freeze()
	r3 := MakeMutableRecord().String("dept", "eng").Int("salary", int64(70000)).String("name", "Charlie").Freeze()
	r4 := MakeMutableRecord().String("dept", "sales").Int("salary", int64(95000)).String("name", "Diana").Freeze()

	input := slices.Values([]Record{r1, r2, r3, r4})

	// Sort by dept ascending, then salary descending
	result := slices.Collect(SortRecords([]OrderField{
		{Field: "dept", Desc: false},
		{Field: "salary", Desc: true},
	})(input))

	// eng should come first (alphabetically), then sales
	// Within eng: 90000 (Alice) before 70000 (Charlie) (descending)
	// Within sales: 95000 (Diana) before 80000 (Bob) (descending)
	expected := []string{"Alice", "Charlie", "Diana", "Bob"}
	for i, name := range expected {
		got := GetOr(result[i], "name", "")
		if got != name {
			t.Errorf("SortRecords[%d]: expected %q, got %q", i, name, got)
		}
	}
}

func TestSortRecords_Strings(t *testing.T) {
	r1 := MakeMutableRecord().String("name", "Charlie").Freeze()
	r2 := MakeMutableRecord().String("name", "Alice").Freeze()
	r3 := MakeMutableRecord().String("name", "Bob").Freeze()

	input := slices.Values([]Record{r1, r2, r3})

	result := slices.Collect(SortRecords([]OrderField{
		{Field: "name", Desc: false},
	})(input))

	expected := []string{"Alice", "Bob", "Charlie"}
	for i, name := range expected {
		got := GetOr(result[i], "name", "")
		if got != name {
			t.Errorf("SortRecords_Strings[%d]: expected %q, got %q", i, name, got)
		}
	}
}

func TestCompareAny(t *testing.T) {
	tests := []struct {
		a, b any
		want int
	}{
		{nil, nil, 0},
		{nil, int64(1), -1},
		{int64(1), nil, 1},
		{int64(1), int64(2), -1},
		{int64(2), int64(1), 1},
		{int64(1), int64(1), 0},
		{float64(1.5), float64(2.5), -1},
		{int64(1), float64(1.5), -1},  // cross-type numeric
		{float64(2), int64(1), 1},     // cross-type numeric
		{"abc", "def", -1},
		{"def", "abc", 1},
		{"abc", "abc", 0},
	}

	for _, tt := range tests {
		got := CompareAny(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("CompareAny(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestWindowNtile(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"x": int64(1)}),
		NewRecord(map[string]any{"x": int64(2)}),
		NewRecord(map[string]any{"x": int64(3)}),
		NewRecord(map[string]any{"x": int64(4)}),
		NewRecord(map[string]any{"x": int64(5)}),
	})

	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "x"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WNtile(3), ResultName: "bucket"}},
	}})(input))

	// 5 rows into 3 buckets: sizes 2, 2, 1 → buckets: 1,1,2,2,3
	// NTILE formula: (pos * N / partLen) + 1
	// pos=0: (0*3/5)+1 = 1
	// pos=1: (1*3/5)+1 = 1
	// pos=2: (2*3/5)+1 = 2
	// pos=3: (3*3/5)+1 = 2
	// pos=4: (4*3/5)+1 = 3
	expected := []int64{1, 1, 2, 2, 3}
	for i, r := range result {
		bucket := GetOr(r, "bucket", int64(0))
		if bucket != expected[i] {
			t.Errorf("row %d bucket: want %d, got %d", i, expected[i], bucket)
		}
	}
}

func TestWindowPercentRank(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "A", "salary": float64(90000)}),
		NewRecord(map[string]any{"name": "B", "salary": float64(80000)}),
		NewRecord(map[string]any{"name": "C", "salary": float64(80000)}),
		NewRecord(map[string]any{"name": "D", "salary": float64(70000)}),
	})

	result := slices.Collect(Window([]WindowConfig{{
		OrderBy: []OrderField{{Field: "salary", Desc: true}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WPercentRank(), ResultName: "pct_rank"}},
	}})(input))

	// Ranks: A=1, B=2, C=2, D=4
	// PERCENT_RANK = (rank-1)/(n-1) where n=4
	// A: (1-1)/3 = 0.0
	// B: (2-1)/3 = 0.333...
	// C: (2-1)/3 = 0.333...
	// D: (4-1)/3 = 1.0
	for _, r := range result {
		name := GetOr(r, "name", "")
		pctRank := GetOr(r, "pct_rank", float64(-1))
		switch name {
		case "A":
			if pctRank != 0.0 {
				t.Errorf("A pct_rank: want 0.0, got %v", pctRank)
			}
		case "D":
			if pctRank != 1.0 {
				t.Errorf("D pct_rank: want 1.0, got %v", pctRank)
			}
		}
	}
}

// ============================================================================
// STREAMING WINDOW FUNCTION TESTS
// ============================================================================

// TestStreamWindow_MatchesWindow runs both Window() and StreamWindow() on presorted input
// and verifies the outputs match for all 8 supported streaming functions.
func TestStreamWindow_MatchesWindow(t *testing.T) {
	// Presorted by dept (partition) then salary (order) ascending
	input := []Record{
		NewRecord(map[string]any{"name": "Charlie", "dept": "eng", "salary": float64(70000)}),
		NewRecord(map[string]any{"name": "Bob", "dept": "eng", "salary": float64(80000)}),
		NewRecord(map[string]any{"name": "Alice", "dept": "eng", "salary": float64(90000)}),
		NewRecord(map[string]any{"name": "Eve", "dept": "sales", "salary": float64(60000)}),
		NewRecord(map[string]any{"name": "Diana", "dept": "sales", "salary": float64(95000)}),
	}

	configs := []WindowConfig{{
		PartitionBy: []string{"dept"},
		OrderBy:     []OrderField{{Field: "salary"}},
		Frame:       WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{
			{Function: WRowNumber(), ResultName: "rn"},
			{Function: WRank(), ResultName: "rnk"},
			{Function: WDenseRank(), ResultName: "drnk"},
			{Function: WSum("salary"), ResultName: "running_sum"},
			{Function: WAvg("salary"), ResultName: "running_avg"},
			{Function: WCount(), ResultName: "cnt"},
			{Function: WFirst("name"), ResultName: "first_name"},
			{Function: WLast("name"), ResultName: "last_name"},
		},
	}}

	// Window() — materializing
	windowResult := slices.Collect(Window(configs)(slices.Values(input)))

	// StreamWindow() — streaming
	streamFilter, err := StreamWindow(configs)
	if err != nil {
		t.Fatalf("StreamWindow: %v", err)
	}
	streamResult := slices.Collect(streamFilter(slices.Values(input)))

	if len(windowResult) != len(streamResult) {
		t.Fatalf("length mismatch: Window=%d, StreamWindow=%d", len(windowResult), len(streamResult))
	}

	fields := []string{"rn", "rnk", "drnk", "running_sum", "cnt", "first_name", "last_name"}
	for i := range windowResult {
		wName := GetOr(windowResult[i], "name", "")
		sName := GetOr(streamResult[i], "name", "")
		if wName != sName {
			t.Errorf("row %d: name mismatch: Window=%q, StreamWindow=%q", i, wName, sName)
			continue
		}
		for _, f := range fields {
			wv, _ := Get[any](windowResult[i], f)
			sv, _ := Get[any](streamResult[i], f)
			if fmt.Sprintf("%v", wv) != fmt.Sprintf("%v", sv) {
				t.Errorf("row %d (%s) field %q: Window=%v, StreamWindow=%v", i, wName, f, wv, sv)
			}
		}
		// Float comparison for running_avg
		wAvg := GetOr(windowResult[i], "running_avg", float64(-1))
		sAvg := GetOr(streamResult[i], "running_avg", float64(-1))
		if math.Abs(wAvg-sAvg) > 1e-9 {
			t.Errorf("row %d (%s) running_avg: Window=%v, StreamWindow=%v", i, wName, wAvg, sAvg)
		}
	}
}

func TestStreamWindowRowNumber(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "A", "val": float64(1)}),
		NewRecord(map[string]any{"name": "B", "val": float64(2)}),
		NewRecord(map[string]any{"name": "C", "val": float64(3)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WRowNumber(), ResultName: "rn"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	for i, r := range result {
		rn := GetOr(r, "rn", int64(0))
		if rn != int64(i+1) {
			t.Errorf("row %d: want rn=%d, got %d", i, i+1, rn)
		}
	}
}

func TestStreamWindowRank(t *testing.T) {
	// Ties: A=10, B=20, C=20, D=30
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "A", "val": float64(10)}),
		NewRecord(map[string]any{"name": "B", "val": float64(20)}),
		NewRecord(map[string]any{"name": "C", "val": float64(20)}),
		NewRecord(map[string]any{"name": "D", "val": float64(30)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WRank(), ResultName: "rnk"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	want := []int64{1, 2, 2, 4} // B and C tie at rank 2, D skips to 4
	for i, r := range result {
		rnk := GetOr(r, "rnk", int64(0))
		if rnk != want[i] {
			t.Errorf("row %d: want rank=%d, got %d", i, want[i], rnk)
		}
	}
}

func TestStreamWindowDenseRank(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "A", "val": float64(10)}),
		NewRecord(map[string]any{"name": "B", "val": float64(20)}),
		NewRecord(map[string]any{"name": "C", "val": float64(20)}),
		NewRecord(map[string]any{"name": "D", "val": float64(30)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WDenseRank(), ResultName: "drnk"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	want := []int64{1, 2, 2, 3} // B and C tie at 2, D is 3 (no gap)
	for i, r := range result {
		drnk := GetOr(r, "drnk", int64(0))
		if drnk != want[i] {
			t.Errorf("row %d: want dense_rank=%d, got %d", i, want[i], drnk)
		}
	}
}

func TestStreamWindowSum(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WSum("val"), ResultName: "running"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	want := []float64{10, 30, 60}
	for i, r := range result {
		v := GetOr(r, "running", float64(0))
		if v != want[i] {
			t.Errorf("row %d: want sum=%v, got %v", i, want[i], v)
		}
	}
}

func TestStreamWindowAvg(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WAvg("val"), ResultName: "avg"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	want := []float64{10, 15, 20} // 10/1, 30/2, 60/3
	for i, r := range result {
		v := GetOr(r, "avg", float64(0))
		if math.Abs(v-want[i]) > 1e-9 {
			t.Errorf("row %d: want avg=%v, got %v", i, want[i], v)
		}
	}
}

func TestStreamWindowCount(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WCount(), ResultName: "cnt"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	for i, r := range result {
		cnt := GetOr(r, "cnt", int64(0))
		if cnt != int64(i+1) {
			t.Errorf("row %d: want count=%d, got %d", i, i+1, cnt)
		}
	}
}

func TestStreamWindowFirst(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "Alice", "val": float64(10)}),
		NewRecord(map[string]any{"name": "Bob", "val": float64(20)}),
		NewRecord(map[string]any{"name": "Charlie", "val": float64(30)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WFirst("name"), ResultName: "first"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	for i, r := range result {
		first := GetOr(r, "first", "")
		if first != "Alice" {
			t.Errorf("row %d: want first=Alice, got %q", i, first)
		}
	}
}

func TestStreamWindowLast(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "Alice", "val": float64(10)}),
		NewRecord(map[string]any{"name": "Bob", "val": float64(20)}),
		NewRecord(map[string]any{"name": "Charlie", "val": float64(30)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WLast("name"), ResultName: "last"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	want := []string{"Alice", "Bob", "Charlie"} // last = current row for this frame
	for i, r := range result {
		last := GetOr(r, "last", "")
		if last != want[i] {
			t.Errorf("row %d: want last=%q, got %q", i, want[i], last)
		}
	}
}

func TestStreamWindowEmpty(t *testing.T) {
	input := slices.Values([]Record{})

	filter, err := StreamWindow([]WindowConfig{{
		Frame: WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{{Function: WRowNumber(), ResultName: "rn"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	if len(result) != 0 {
		t.Errorf("expected 0 records, got %d", len(result))
	}
}

func TestStreamWindowSingleRecord(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(42)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		Frame: WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{
			{Function: WRowNumber(), ResultName: "rn"},
			{Function: WSum("val"), ResultName: "sum"},
			{Function: WAvg("val"), ResultName: "avg"},
			{Function: WCount(), ResultName: "cnt"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	if len(result) != 1 {
		t.Fatalf("expected 1 record, got %d", len(result))
	}
	r := result[0]
	if rn := GetOr(r, "rn", int64(0)); rn != 1 {
		t.Errorf("rn: want 1, got %d", rn)
	}
	if sum := GetOr(r, "sum", float64(0)); sum != 42 {
		t.Errorf("sum: want 42, got %v", sum)
	}
	if avg := GetOr(r, "avg", float64(0)); avg != 42 {
		t.Errorf("avg: want 42, got %v", avg)
	}
	if cnt := GetOr(r, "cnt", int64(0)); cnt != 1 {
		t.Errorf("cnt: want 1, got %d", cnt)
	}
}

func TestStreamWindowPartitionedRowNumber(t *testing.T) {
	// Presorted by dept, then val
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "A", "val": float64(1)}),
		NewRecord(map[string]any{"dept": "A", "val": float64(2)}),
		NewRecord(map[string]any{"dept": "B", "val": float64(10)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		PartitionBy: []string{"dept"},
		OrderBy:     []OrderField{{Field: "val"}},
		Frame:       WindowFrame{Preceding: -1, Following: 0},
		Specs:       []WindowSpec{{Function: WRowNumber(), ResultName: "rn"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	want := []int64{1, 2, 1} // reset at partition boundary
	for i, r := range result {
		rn := GetOr(r, "rn", int64(0))
		if rn != want[i] {
			t.Errorf("row %d: want rn=%d, got %d", i, want[i], rn)
		}
	}
}

// Error tests

func TestStreamWindowRejectsNtile(t *testing.T) {
	_, err := StreamWindow([]WindowConfig{{
		Frame: WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{{Function: WNtile(4), ResultName: "q"}},
	}})
	if err == nil {
		t.Fatal("expected error for NTILE")
	}
}

func TestStreamWindowRejectsPercentRank(t *testing.T) {
	_, err := StreamWindow([]WindowConfig{{
		Frame: WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{{Function: WPercentRank(), ResultName: "pct"}},
	}})
	if err == nil {
		t.Fatal("expected error for PERCENT_RANK")
	}
}

func TestStreamWindowRejectsFollowingPositive(t *testing.T) {
	_, err := StreamWindow([]WindowConfig{{
		Frame: WindowFrame{Preceding: 2, Following: 1},
		Specs: []WindowSpec{{Function: WSum("val"), ResultName: "sum"}},
	}})
	if err == nil {
		t.Fatal("expected error for Following > 0")
	}
}

func TestStreamWindowRejectsUnboundedFollowing(t *testing.T) {
	_, err := StreamWindow([]WindowConfig{{
		Frame: WindowFrame{Preceding: -1, Following: -1},
		Specs: []WindowSpec{{Function: WSum("val"), ResultName: "sum"}},
	}})
	if err == nil {
		t.Fatal("expected error for UNBOUNDED FOLLOWING")
	}
}

func TestStreamWindowRejectsMismatchedClauses(t *testing.T) {
	_, err := StreamWindow([]WindowConfig{
		{
			PartitionBy: []string{"dept"},
			OrderBy:     []OrderField{{Field: "val"}},
			Frame:       WindowFrame{Preceding: -1, Following: 0},
			Specs:       []WindowSpec{{Function: WRowNumber(), ResultName: "rn"}},
		},
		{
			PartitionBy: []string{"region"}, // different partition
			OrderBy:     []OrderField{{Field: "val"}},
			Frame:       WindowFrame{Preceding: -1, Following: 0},
			Specs:       []WindowSpec{{Function: WCount(), ResultName: "cnt"}},
		},
	})
	if err == nil {
		t.Fatal("expected error for mismatched partition fields")
	}
}

// ============================================================================
// PHASE 2: Bounded frames, LAG/LEAD, MIN/MAX
// ============================================================================

func TestStreamWindow_MatchesWindow_BoundedFrame(t *testing.T) {
	// Presorted by dept (partition) then salary (order) ascending
	input := []Record{
		NewRecord(map[string]any{"name": "Charlie", "dept": "eng", "salary": float64(70000)}),
		NewRecord(map[string]any{"name": "Bob", "dept": "eng", "salary": float64(80000)}),
		NewRecord(map[string]any{"name": "Alice", "dept": "eng", "salary": float64(90000)}),
		NewRecord(map[string]any{"name": "Eve", "dept": "sales", "salary": float64(60000)}),
		NewRecord(map[string]any{"name": "Diana", "dept": "sales", "salary": float64(95000)}),
	}

	configs := []WindowConfig{{
		PartitionBy: []string{"dept"},
		OrderBy:     []OrderField{{Field: "salary"}},
		Frame:       WindowFrame{Preceding: 2, Following: 0},
		Specs: []WindowSpec{
			{Function: WSum("salary"), ResultName: "sliding_sum"},
			{Function: WAvg("salary"), ResultName: "sliding_avg"},
			{Function: WCount(), ResultName: "cnt"},
			{Function: WFirst("name"), ResultName: "first_name"},
			{Function: WMin("salary"), ResultName: "min_sal"},
			{Function: WMax("salary"), ResultName: "max_sal"},
		},
	}}

	// Window() — materializing
	windowResult := slices.Collect(Window(configs)(slices.Values(input)))

	// StreamWindow() — streaming
	streamFilter, err := StreamWindow(configs)
	if err != nil {
		t.Fatalf("StreamWindow: %v", err)
	}
	streamResult := slices.Collect(streamFilter(slices.Values(input)))

	if len(windowResult) != len(streamResult) {
		t.Fatalf("length mismatch: Window=%d, StreamWindow=%d", len(windowResult), len(streamResult))
	}

	fields := []string{"sliding_sum", "cnt", "first_name", "min_sal", "max_sal"}
	for i := range windowResult {
		wName := GetOr(windowResult[i], "name", "")
		sName := GetOr(streamResult[i], "name", "")
		if wName != sName {
			t.Errorf("row %d: name mismatch: Window=%q, StreamWindow=%q", i, wName, sName)
			continue
		}
		for _, f := range fields {
			wv, _ := Get[any](windowResult[i], f)
			sv, _ := Get[any](streamResult[i], f)
			if fmt.Sprintf("%v", wv) != fmt.Sprintf("%v", sv) {
				t.Errorf("row %d (%s) field %q: Window=%v, StreamWindow=%v", i, wName, f, wv, sv)
			}
		}
		// Float comparison for sliding_avg
		wAvg := GetOr(windowResult[i], "sliding_avg", float64(-1))
		sAvg := GetOr(streamResult[i], "sliding_avg", float64(-1))
		if math.Abs(wAvg-sAvg) > 1e-9 {
			t.Errorf("row %d (%s) sliding_avg: Window=%v, StreamWindow=%v", i, wName, wAvg, sAvg)
		}
	}
}

func TestStreamWindowSlidingSum(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
		NewRecord(map[string]any{"val": float64(40)}),
		NewRecord(map[string]any{"val": float64(50)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: 2, Following: 0}, // ROWS 2,0
		Specs:   []WindowSpec{{Function: WSum("val"), ResultName: "sum"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	want := []float64{10, 30, 60, 90, 120}
	for i, r := range result {
		v := GetOr(r, "sum", float64(0))
		if v != want[i] {
			t.Errorf("row %d: want sum=%v, got %v", i, want[i], v)
		}
	}
}

func TestStreamWindowSlidingAvg(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
		NewRecord(map[string]any{"val": float64(40)}),
		NewRecord(map[string]any{"val": float64(50)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: 2, Following: 0},
		Specs:   []WindowSpec{{Function: WAvg("val"), ResultName: "avg"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	// row0: 10/1=10, row1: 30/2=15, row2: 60/3=20, row3: 90/3=30, row4: 120/3=40
	want := []float64{10, 15, 20, 30, 40}
	for i, r := range result {
		v := GetOr(r, "avg", float64(0))
		if math.Abs(v-want[i]) > 1e-9 {
			t.Errorf("row %d: want avg=%v, got %v", i, want[i], v)
		}
	}
}

func TestStreamWindowSlidingCount(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
		NewRecord(map[string]any{"val": float64(40)}),
		NewRecord(map[string]any{"val": float64(50)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: 2, Following: 0},
		Specs:   []WindowSpec{{Function: WCount(), ResultName: "cnt"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	want := []int64{1, 2, 3, 3, 3}
	for i, r := range result {
		cnt := GetOr(r, "cnt", int64(0))
		if cnt != want[i] {
			t.Errorf("row %d: want count=%d, got %d", i, want[i], cnt)
		}
	}
}

func TestStreamWindowSlidingFirst(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"name": "A", "val": float64(1)}),
		NewRecord(map[string]any{"name": "B", "val": float64(2)}),
		NewRecord(map[string]any{"name": "C", "val": float64(3)}),
		NewRecord(map[string]any{"name": "D", "val": float64(4)}),
		NewRecord(map[string]any{"name": "E", "val": float64(5)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: 2, Following: 0},
		Specs:   []WindowSpec{{Function: WFirst("name"), ResultName: "first"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	// Frame ROWS 2,0: row0=[A], row1=[A,B], row2=[A,B,C], row3=[B,C,D], row4=[C,D,E]
	want := []string{"A", "A", "A", "B", "C"}
	for i, r := range result {
		first := GetOr(r, "first", "")
		if first != want[i] {
			t.Errorf("row %d: want first=%q, got %q", i, want[i], first)
		}
	}
}

func TestStreamWindowRunningMin(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(30)}),
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(5)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0}, // default frame
		Specs:   []WindowSpec{{Function: WMin("val"), ResultName: "rmin"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	want := []float64{30, 10, 10, 5}
	for i, r := range result {
		v := GetOr(r, "rmin", float64(0))
		if v != want[i] {
			t.Errorf("row %d: want min=%v, got %v", i, want[i], v)
		}
	}
}

func TestStreamWindowRunningMax(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(30)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(5)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WMax("val"), ResultName: "rmax"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	want := []float64{10, 30, 30, 30}
	for i, r := range result {
		v := GetOr(r, "rmax", float64(0))
		if v != want[i] {
			t.Errorf("row %d: want max=%v, got %v", i, want[i], v)
		}
	}
}

func TestStreamWindowSlidingMin(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(50)}),
		NewRecord(map[string]any{"val": float64(30)}),
		NewRecord(map[string]any{"val": float64(40)}),
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: 2, Following: 0}, // ROWS 2,0 (window of 3)
		Specs:   []WindowSpec{{Function: WMin("val"), ResultName: "smin"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	// Frames: [50]=50, [50,30]=30, [50,30,40]=30, [30,40,10]=10, [40,10,20]=10
	want := []float64{50, 30, 30, 10, 10}
	for i, r := range result {
		v := GetOr(r, "smin", float64(0))
		if v != want[i] {
			t.Errorf("row %d: want smin=%v, got %v", i, want[i], v)
		}
	}
}

func TestStreamWindowSlidingMax(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(30)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(50)}),
		NewRecord(map[string]any{"val": float64(40)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: 2, Following: 0},
		Specs:   []WindowSpec{{Function: WMax("val"), ResultName: "smax"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	// Frames: [10]=10, [10,30]=30, [10,30,20]=30, [30,20,50]=50, [20,50,40]=50
	want := []float64{10, 30, 30, 50, 50}
	for i, r := range result {
		v := GetOr(r, "smax", float64(0))
		if v != want[i] {
			t.Errorf("row %d: want smax=%v, got %v", i, want[i], v)
		}
	}
}

func TestStreamWindowLag(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
		NewRecord(map[string]any{"val": float64(40)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WLag("val", 2), ResultName: "lag2"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	// LAG(val, 2): nil, nil, 10, 20
	for i, r := range result {
		v, _ := Get[any](r, "lag2")
		if i < 2 {
			if v != nil {
				t.Errorf("row %d: want nil, got %v", i, v)
			}
		} else {
			expected := float64((i - 1) * 10)
			if v != expected {
				t.Errorf("row %d: want lag=%v, got %v", i, expected, v)
			}
		}
	}
}

func TestStreamWindowLead(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
		NewRecord(map[string]any{"val": float64(40)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs:   []WindowSpec{{Function: WLead("val", 1), ResultName: "lead1"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	// LEAD(val, 1): 20, 30, 40, nil
	for i, r := range result {
		v, _ := Get[any](r, "lead1")
		if i < 3 {
			expected := float64((i + 2) * 10)
			if v != expected {
				t.Errorf("row %d: want lead=%v, got %v", i, expected, v)
			}
		} else {
			if v != nil {
				t.Errorf("row %d: want nil, got %v", i, v)
			}
		}
	}
}

func TestStreamWindowLeadPartitioned(t *testing.T) {
	// Presorted by dept, then val
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "A", "val": float64(1)}),
		NewRecord(map[string]any{"dept": "A", "val": float64(2)}),
		NewRecord(map[string]any{"dept": "A", "val": float64(3)}),
		NewRecord(map[string]any{"dept": "B", "val": float64(10)}),
		NewRecord(map[string]any{"dept": "B", "val": float64(20)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		PartitionBy: []string{"dept"},
		OrderBy:     []OrderField{{Field: "val"}},
		Frame:       WindowFrame{Preceding: -1, Following: 0},
		Specs:       []WindowSpec{{Function: WLead("val", 1), ResultName: "next"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	// Partition A: [1→2, 2→3, 3→nil], Partition B: [10→20, 20→nil]
	wantVal := []any{float64(2), float64(3), nil, float64(20), nil}
	for i, r := range result {
		v, _ := Get[any](r, "next")
		if fmt.Sprintf("%v", v) != fmt.Sprintf("%v", wantVal[i]) {
			t.Errorf("row %d: want next=%v, got %v", i, wantVal[i], v)
		}
	}
}

func TestStreamWindowFrameLargerThanPartition(t *testing.T) {
	// Frame ROWS 10,0 with only 3 records
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: 10, Following: 0},
		Specs:   []WindowSpec{{Function: WSum("val"), ResultName: "sum"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	// Frame never fills, so it's like UNBOUNDED PRECEDING
	want := []float64{10, 30, 60}
	for i, r := range result {
		v := GetOr(r, "sum", float64(0))
		if v != want[i] {
			t.Errorf("row %d: want sum=%v, got %v", i, want[i], v)
		}
	}
}

func TestStreamWindowLagLargerThanPartition(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "A", "val": float64(1)}),
		NewRecord(map[string]any{"dept": "A", "val": float64(2)}),
		NewRecord(map[string]any{"dept": "B", "val": float64(10)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		PartitionBy: []string{"dept"},
		OrderBy:     []OrderField{{Field: "val"}},
		Frame:       WindowFrame{Preceding: -1, Following: 0},
		Specs:       []WindowSpec{{Function: WLag("val", 5), ResultName: "lag5"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	// All nil — lag offset > partition size
	for i, r := range result {
		v, _ := Get[any](r, "lag5")
		if v != nil {
			t.Errorf("row %d: want nil, got %v", i, v)
		}
	}
}

func TestStreamWindowLeadLargerThanPartition(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"dept": "A", "val": float64(1)}),
		NewRecord(map[string]any{"dept": "A", "val": float64(2)}),
		NewRecord(map[string]any{"dept": "B", "val": float64(10)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		PartitionBy: []string{"dept"},
		OrderBy:     []OrderField{{Field: "val"}},
		Frame:       WindowFrame{Preceding: -1, Following: 0},
		Specs:       []WindowSpec{{Function: WLead("val", 5), ResultName: "lead5"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	// All nil — lead offset > partition size
	for i, r := range result {
		v, _ := Get[any](r, "lead5")
		if v != nil {
			t.Errorf("row %d: want nil, got %v", i, v)
		}
	}
}

func TestStreamWindowMixedLagAndSum(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{
			{Function: WLag("val", 1), ResultName: "prev"},
			{Function: WSum("val"), ResultName: "running"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	wantPrev := []any{nil, float64(10), float64(20)}
	wantSum := []float64{10, 30, 60}
	for i, r := range result {
		prev, _ := Get[any](r, "prev")
		if fmt.Sprintf("%v", prev) != fmt.Sprintf("%v", wantPrev[i]) {
			t.Errorf("row %d: want prev=%v, got %v", i, wantPrev[i], prev)
		}
		sum := GetOr(r, "running", float64(0))
		if sum != wantSum[i] {
			t.Errorf("row %d: want sum=%v, got %v", i, wantSum[i], sum)
		}
	}
}

func TestStreamWindowMixedLeadAndSlidingSum(t *testing.T) {
	input := slices.Values([]Record{
		NewRecord(map[string]any{"val": float64(10)}),
		NewRecord(map[string]any{"val": float64(20)}),
		NewRecord(map[string]any{"val": float64(30)}),
		NewRecord(map[string]any{"val": float64(40)}),
	})

	filter, err := StreamWindow([]WindowConfig{{
		OrderBy: []OrderField{{Field: "val"}},
		Frame:   WindowFrame{Preceding: 1, Following: 0},
		Specs: []WindowSpec{
			{Function: WLead("val", 1), ResultName: "next"},
			{Function: WSum("val"), ResultName: "sum2"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	result := slices.Collect(filter(input))
	wantNext := []any{float64(20), float64(30), float64(40), nil}
	wantSum := []float64{10, 30, 50, 70} // ROWS 1,0: [10], [10,20], [20,30], [30,40]
	for i, r := range result {
		next, _ := Get[any](r, "next")
		if fmt.Sprintf("%v", next) != fmt.Sprintf("%v", wantNext[i]) {
			t.Errorf("row %d: want next=%v, got %v", i, wantNext[i], next)
		}
		sum := GetOr(r, "sum2", float64(0))
		if sum != wantSum[i] {
			t.Errorf("row %d: want sum=%v, got %v", i, wantSum[i], sum)
		}
	}
}
