package ssql

import (
	"iter"
	"slices"
	"testing"
)

// ============================================================================
// JOIN OPERATIONS TESTS
// ============================================================================

func TestInnerJoin(t *testing.T) {
	left := slices.Values([]Record{
		{fields: map[string]any{"id": int64(1), "name": "Alice"}},
		{fields: map[string]any{"id": int64(2), "name": "Bob"}},
		{fields: map[string]any{"id": int64(3), "name": "Charlie"}},
	})

	right := slices.Values([]Record{
		{fields: map[string]any{"id": int64(1), "dept": "Engineering"}},
		{fields: map[string]any{"id": int64(2), "dept": "Sales"}},
	})

	filter := InnerJoin(right, OnFields("id"))
	result := slices.Collect(filter(left))

	// Should match only records with id 1 and 2
	if len(result) != 2 {
		t.Fatalf("InnerJoin should return 2 records, got %d", len(result))
	}

	// Check first joined record
	if result[0].fields["name"] != "Alice" || result[0].fields["dept"] != "Engineering" {
		t.Errorf("First join failed: %v", result[0])
	}
}

func TestLeftJoin(t *testing.T) {
	left := slices.Values([]Record{
		{fields: map[string]any{"id": int64(1), "name": "Alice"}},
		{fields: map[string]any{"id": int64(2), "name": "Bob"}},
		{fields: map[string]any{"id": int64(3), "name": "Charlie"}},
	})

	right := slices.Values([]Record{
		{fields: map[string]any{"id": int64(1), "dept": "Engineering"}},
		{fields: map[string]any{"id": int64(2), "dept": "Sales"}},
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
		if r.fields["name"] == "Charlie" {
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
		{fields: map[string]any{"id": int64(1), "name": "Alice"}},
		{fields: map[string]any{"id": int64(2), "name": "Bob"}},
	})

	right := slices.Values([]Record{
		{fields: map[string]any{"id": int64(1), "dept": "Engineering"}},
		{fields: map[string]any{"id": int64(2), "dept": "Sales"}},
		{fields: map[string]any{"id": int64(3), "dept": "Marketing"}},
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
		if r.fields["dept"] == "Marketing" {
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
		{fields: map[string]any{"id": int64(1), "name": "Alice"}},
		{fields: map[string]any{"id": int64(2), "name": "Bob"}},
		{fields: map[string]any{"id": int64(4), "name": "David"}},
	})

	right := slices.Values([]Record{
		{fields: map[string]any{"id": int64(1), "dept": "Engineering"}},
		{fields: map[string]any{"id": int64(2), "dept": "Sales"}},
		{fields: map[string]any{"id": int64(3), "dept": "Marketing"}},
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
		if r.fields["name"] == "David" {
			hasDavid = true
		}
		if r.fields["dept"] == "Marketing" {
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

	left := Record{fields: map[string]any{"id": int64(1), "type": "A", "name": "Alice"}}
	right := Record{fields: map[string]any{"id": int64(1), "type": "A", "dept": "Eng"}}

	if !predicate.Match(left, right) {
		t.Error("OnFields should match records with same id and type")
	}

	right2 := Record{fields: map[string]any{"id": int64(1), "type": "B", "dept": "Sales"}}
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

	left := Record{fields: map[string]any{"name": "Alice", "age": int64(30)}}
	right := Record{fields: map[string]any{"name": "Bob", "age": int64(25)}}

	if !predicate.Match(left, right) {
		t.Error("OnCondition should match when left age > right age")
	}

	right2 := Record{fields: map[string]any{"name": "Charlie", "age": int64(35)}}
	if predicate.Match(left, right2) {
		t.Error("OnCondition should not match when left age < right age")
	}
}

func TestOnFieldPair(t *testing.T) {
	predicate := OnFieldPair("user_id", "id")

	left := Record{fields: map[string]any{"user_id": int64(1), "name": "Alice"}}
	right := Record{fields: map[string]any{"id": int64(1), "dept": "Eng"}}

	if !predicate.Match(left, right) {
		t.Error("OnFieldPair should match records with user_id=id")
	}

	right2 := Record{fields: map[string]any{"id": int64(2), "dept": "Sales"}}
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
		{fields: map[string]any{"name": "Alice", "a_kind": int64(1), "z_kind": int64(2)}},
		{fields: map[string]any{"name": "Bob", "a_kind": int64(2), "z_kind": int64(1)}},
	})

	// Right side: kind lookup table
	right := slices.Values([]Record{
		{fields: map[string]any{"kind": int64(1), "kind_name": "Premium"}},
		{fields: map[string]any{"kind": int64(2), "kind_name": "Standard"}},
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
	if alice.fields["name"] != "Alice" {
		t.Errorf("First record should be Alice, got %v", alice.fields["name"])
	}
	if alice.fields["a_kind_name"] != "Premium" {
		t.Errorf("Alice a_kind_name should be Premium, got %v", alice.fields["a_kind_name"])
	}
	if alice.fields["z_kind_name"] != "Standard" {
		t.Errorf("Alice z_kind_name should be Standard, got %v", alice.fields["z_kind_name"])
	}

	// Check Bob: a_kind=2 -> Standard, z_kind=1 -> Premium
	bob := result[1]
	if bob.fields["a_kind_name"] != "Standard" {
		t.Errorf("Bob a_kind_name should be Standard, got %v", bob.fields["a_kind_name"])
	}
	if bob.fields["z_kind_name"] != "Premium" {
		t.Errorf("Bob z_kind_name should be Premium, got %v", bob.fields["z_kind_name"])
	}
}

func TestLookupJoinNoMatch(t *testing.T) {
	left := slices.Values([]Record{
		{fields: map[string]any{"name": "Alice", "kind_id": int64(99)}}, // No match
	})

	right := slices.Values([]Record{
		{fields: map[string]any{"kind": int64(1), "kind_name": "Premium"}},
	})

	clauses := []LookupClause{
		Lookup("kind_id", "kind", "kind_name", "kind_name"),
	}

	result := slices.Collect(LookupJoin(right, clauses)(left))

	// Record should pass through unchanged when no match
	if len(result) != 1 {
		t.Fatalf("LookupJoin should return 1 record, got %d", len(result))
	}

	if result[0].fields["name"] != "Alice" {
		t.Error("Record should preserve original fields")
	}
	if _, exists := result[0].fields["kind_name"]; exists {
		t.Error("Record should not have kind_name when no match")
	}
}

func TestLookupJoinMultipleMatches(t *testing.T) {
	left := slices.Values([]Record{
		{fields: map[string]any{"name": "Alice", "dept": "Eng"}},
	})

	// Multiple employees in Eng department
	right := slices.Values([]Record{
		{fields: map[string]any{"dept": "Eng", "employee": "Bob"}},
		{fields: map[string]any{"dept": "Eng", "employee": "Charlie"}},
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
		if c, ok := r.fields["colleague"].(string); ok {
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
		{fields: map[string]any{"name": "Alice", "dept": "Engineering", "salary": int64(100000)}},
		{fields: map[string]any{"name": "Bob", "dept": "Engineering", "salary": int64(110000)}},
		{fields: map[string]any{"name": "Charlie", "dept": "Sales", "salary": int64(90000)}},
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
		if seq, ok := group.fields["employees"].(iter.Seq[Record]); ok {
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
		{fields: map[string]any{"dept": "Eng", "level": "Senior", "count": int64(5)}},
		{fields: map[string]any{"dept": "Eng", "level": "Senior", "count": int64(3)}},
		{fields: map[string]any{"dept": "Eng", "level": "Junior", "count": int64(10)}},
		{fields: map[string]any{"dept": "Sales", "level": "Senior", "count": int64(2)}},
	})

	filter := GroupByFields("members", "dept", "level")
	result := slices.Collect(filter(input))

	// Should create 3 groups: (Eng,Senior), (Eng,Junior), (Sales,Senior)
	if len(result) != 3 {
		t.Fatalf("GroupByFields should return 3 groups, got %d", len(result))
	}

	// Find the (Eng, Senior) group
	for _, group := range result {
		if group.fields["dept"] == "Eng" && group.fields["level"] == "Senior" {
			// This group should have 2 members
			if seq, ok := group.fields["members"].(iter.Seq[Record]); ok {
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
	nestedRecord := Record{fields: map[string]any{"city": "NYC"}}

	input := slices.Values([]Record{
		{fields: map[string]any{"dept": nestedRecord, "name": "Alice"}}, // Complex dept field - should be skipped
		{fields: map[string]any{"dept": "Sales", "location": "Boston"}}, // Simple dept - should work
	})

	filter := GroupByFields("members", "dept")
	result := slices.Collect(filter(input))

	// Should only group the Sales record (Eng has complex dept value)
	if len(result) != 1 {
		t.Fatalf("GroupByFields should skip records with complex grouping field values, expected 1 group, got %d", len(result))
	}

	if result[0].fields["dept"] != "Sales" {
		t.Error("Should only have Sales group")
	}
}

// ============================================================================
// AGGREGATION OPERATIONS TESTS
// ============================================================================

func TestAggregate(t *testing.T) {
	// Create a grouped result first
	input := slices.Values([]Record{
		{fields: map[string]any{"dept": "Eng", "salary": int64(100000)}},
		{fields: map[string]any{"dept": "Eng", "salary": int64(110000)}},
		{fields: map[string]any{"dept": "Sales", "salary": int64(90000)}},
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
		if r.fields["dept"] == "Eng" {
			if r.fields["count"] != int64(2) {
				t.Errorf("Eng count should be 2, got %v", r.fields["count"])
			}
			if r.fields["total"] != float64(210000) {
				t.Errorf("Eng total should be 210000, got %v", r.fields["total"])
			}
			if r.fields["avg_salary"] != float64(105000) {
				t.Errorf("Eng avg should be 105000, got %v", r.fields["avg_salary"])
			}
		}
	}
}

func TestCount(t *testing.T) {
	records := []Record{
		{fields: map[string]any{"name": "Alice"}},
		{fields: map[string]any{"name": "Bob"}},
		{fields: map[string]any{"name": "Charlie"}},
	}

	countFn := Count()
	result := countFn(records).getValue()

	if result != int64(3) {
		t.Errorf("Count should return 3, got %v", result)
	}
}

func TestSum(t *testing.T) {
	records := []Record{
		{fields: map[string]any{"value": int64(10)}},
		{fields: map[string]any{"value": int64(20)}},
		{fields: map[string]any{"value": int64(30)}},
	}

	sumFn := Sum("value")
	result := sumFn(records).getValue()

	if result != float64(60) {
		t.Errorf("Sum should return 60, got %v", result)
	}
}

func TestSumMixedTypes(t *testing.T) {
	records := []Record{
		{fields: map[string]any{"value": int64(10)}},
		{fields: map[string]any{"value": 20.5}},
		{fields: map[string]any{"value": "30"}}, // Should convert
	}

	sumFn := Sum("value")
	result := sumFn(records).getValue()

	if result != float64(60.5) {
		t.Errorf("Sum should handle mixed types and return 60.5, got %v", result)
	}
}

func TestAvg(t *testing.T) {
	records := []Record{
		{fields: map[string]any{"score": int64(80)}},
		{fields: map[string]any{"score": int64(90)}},
		{fields: map[string]any{"score": int64(100)}},
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
		{fields: map[string]any{"value": int64(50)}},
		{fields: map[string]any{"value": int64(10)}},
		{fields: map[string]any{"value": int64(30)}},
	}

	minFn := Min[int64]("value")
	result := minFn(records).getValue()

	if result != int64(10) {
		t.Errorf("Min should return 10, got %v", result)
	}
}

func TestMinFloat(t *testing.T) {
	records := []Record{
		{fields: map[string]any{"value": 5.5}},
		{fields: map[string]any{"value": 2.3}},
		{fields: map[string]any{"value": 7.8}},
	}

	minFn := Min[float64]("value")
	result := minFn(records).getValue()

	if result != 2.3 {
		t.Errorf("Min should return 2.3, got %v", result)
	}
}

func TestMax(t *testing.T) {
	records := []Record{
		{fields: map[string]any{"value": int64(50)}},
		{fields: map[string]any{"value": int64(10)}},
		{fields: map[string]any{"value": int64(100)}},
	}

	maxFn := Max[int64]("value")
	result := maxFn(records).getValue()

	if result != int64(100) {
		t.Errorf("Max should return 100, got %v", result)
	}
}

func TestMaxString(t *testing.T) {
	records := []Record{
		{fields: map[string]any{"name": "Alice"}},
		{fields: map[string]any{"name": "Charlie"}},
		{fields: map[string]any{"name": "Bob"}},
	}

	maxFn := Max[string]("name")
	result := maxFn(records).getValue()

	if result != "Charlie" {
		t.Errorf("Max should return Charlie, got %v", result)
	}
}

func TestFirst(t *testing.T) {
	records := []Record{
		{fields: map[string]any{"value": "first"}},
		{fields: map[string]any{"value": "second"}},
		{fields: map[string]any{"value": "third"}},
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
		{fields: map[string]any{"value": "first"}},
		{fields: map[string]any{"value": "second"}},
		{fields: map[string]any{"value": "third"}},
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
// COMPLEX SQL-STYLE PIPELINE TESTS
// ============================================================================

func TestComplexGroupByAggregate(t *testing.T) {
	// Sales data
	input := slices.Values([]Record{
		{fields: map[string]any{"product": "Widget", "region": "East", "sales": int64(1000)}},
		{fields: map[string]any{"product": "Widget", "region": "East", "sales": int64(1500)}},
		{fields: map[string]any{"product": "Widget", "region": "West", "sales": int64(2000)}},
		{fields: map[string]any{"product": "Gadget", "region": "East", "sales": int64(800)}},
		{fields: map[string]any{"product": "Gadget", "region": "West", "sales": int64(1200)}},
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
		if r.fields["product"] == "Widget" && r.fields["region"] == "East" {
			if r.fields["count"] != int64(2) {
				t.Errorf("Widget/East count should be 2, got %v", r.fields["count"])
			}
			if r.fields["total_sales"] != float64(2500) {
				t.Errorf("Widget/East total should be 2500, got %v", r.fields["total_sales"])
			}
			if r.fields["max_sale"] != int64(1500) {
				t.Errorf("Widget/East max should be 1500, got %v", r.fields["max_sale"])
			}
		}
	}
}

func TestJoinThenGroup(t *testing.T) {
	// Employee data
	employees := slices.Values([]Record{
		{fields: map[string]any{"id": int64(1), "name": "Alice", "dept_id": int64(10)}},
		{fields: map[string]any{"id": int64(2), "name": "Bob", "dept_id": int64(10)}},
		{fields: map[string]any{"id": int64(3), "name": "Charlie", "dept_id": int64(20)}},
	})

	// Department data
	departments := slices.Values([]Record{
		{fields: map[string]any{"dept_id": int64(10), "dept_name": "Engineering"}},
		{fields: map[string]any{"dept_id": int64(20), "dept_name": "Sales"}},
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
		if r.fields["dept_name"] == "Engineering" {
			if r.fields["count"] != int64(2) {
				t.Errorf("Engineering should have 2 employees, got %v", r.fields["count"])
			}
		}
	}
}

func TestMultiFieldGrouping(t *testing.T) {
	// Test grouping by multiple fields
	input := slices.Values([]Record{
		{fields: map[string]any{"year": int64(2024), "month": "Jan", "sales": int64(1000)}},
		{fields: map[string]any{"year": int64(2024), "month": "Jan", "sales": int64(1100)}},
		{fields: map[string]any{"year": int64(2024), "month": "Feb", "sales": int64(1200)}},
		{fields: map[string]any{"year": int64(2023), "month": "Jan", "sales": int64(900)}},
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
		if r.fields["year"] == int64(2024) && r.fields["month"] == "Jan" {
			if r.fields["count"] != int64(2) {
				t.Errorf("2024/Jan should have count 2, got %v", r.fields["count"])
			}
			if r.fields["total"] != float64(2100) {
				t.Errorf("2024/Jan should have total 2100, got %v", r.fields["total"])
			}
		}
	}
}

// ============================================================================
// EDGE CASE TESTS
// ============================================================================

func TestJoinEmptyRight(t *testing.T) {
	left := slices.Values([]Record{
		{fields: map[string]any{"id": int64(1), "name": "Alice"}},
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

func TestAggregateNoSequenceField(t *testing.T) {
	input := slices.Values([]Record{
		{fields: map[string]any{"dept": "Eng", "count": int64(5)}}, // No sequence field
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
