package ssql

import (
	"iter"
	"slices"
	"testing"
	"time"
)

// ============================================================================
// COMPOSITION FUNCTIONS TESTS
// ============================================================================

func TestPipe(t *testing.T) {
	// Test basic piping: double then add 10
	double := func(seq iter.Seq[int]) iter.Seq[int] {
		return func(yield func(int) bool) {
			for v := range seq {
				if !yield(v * 2) {
					return
				}
			}
		}
	}

	addTen := func(seq iter.Seq[int]) iter.Seq[int] {
		return func(yield func(int) bool) {
			for v := range seq {
				if !yield(v + 10) {
					return
				}
			}
		}
	}

	pipeline := Pipe(double, addTen)
	input := slices.Values([]int{1, 2, 3})
	result := slices.Collect(pipeline(input))

	expected := []int{12, 14, 16} // (1*2)+10, (2*2)+10, (3*2)+10
	if !slices.Equal(result, expected) {
		t.Errorf("Pipe failed: expected %v, got %v", expected, result)
	}
}

func TestPipe3(t *testing.T) {
	// Test three-stage pipeline: double, add 10, then multiply by 3
	double := func(seq iter.Seq[int]) iter.Seq[int] {
		return func(yield func(int) bool) {
			for v := range seq {
				if !yield(v * 2) {
					return
				}
			}
		}
	}

	addTen := func(seq iter.Seq[int]) iter.Seq[int] {
		return func(yield func(int) bool) {
			for v := range seq {
				if !yield(v + 10) {
					return
				}
			}
		}
	}

	triple := func(seq iter.Seq[int]) iter.Seq[int] {
		return func(yield func(int) bool) {
			for v := range seq {
				if !yield(v * 3) {
					return
				}
			}
		}
	}

	pipeline := Pipe3(double, addTen, triple)
	input := slices.Values([]int{1, 2, 3})
	result := slices.Collect(pipeline(input))

	expected := []int{36, 42, 48} // ((1*2)+10)*3, ((2*2)+10)*3, ((3*2)+10)*3
	if !slices.Equal(result, expected) {
		t.Errorf("Pipe3 failed: expected %v, got %v", expected, result)
	}
}

func TestChain(t *testing.T) {
	addOne := func(seq iter.Seq[int]) iter.Seq[int] {
		return func(yield func(int) bool) {
			for v := range seq {
				if !yield(v + 1) {
					return
				}
			}
		}
	}

	multiplyTwo := func(seq iter.Seq[int]) iter.Seq[int] {
		return func(yield func(int) bool) {
			for v := range seq {
				if !yield(v * 2) {
					return
				}
			}
		}
	}

	pipeline := Chain(addOne, multiplyTwo, addOne)
	input := slices.Values([]int{1, 2, 3})
	result := slices.Collect(pipeline(input))

	expected := []int{5, 7, 9} // ((1+1)*2)+1, ((2+1)*2)+1, ((3+1)*2)+1
	if !slices.Equal(result, expected) {
		t.Errorf("Chain failed: expected %v, got %v", expected, result)
	}
}

func TestChainEmpty(t *testing.T) {
	// Test Chain with no filters (identity)
	pipeline := Chain[int]()
	input := slices.Values([]int{1, 2, 3})
	result := slices.Collect(pipeline(input))

	expected := []int{1, 2, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("Chain with no filters failed: expected %v, got %v", expected, result)
	}
}

func TestPipeWithErrors(t *testing.T) {
	// Test error-aware pipe composition
	addErr := func(seq iter.Seq2[int, error]) iter.Seq2[int, error] {
		return func(yield func(int, error) bool) {
			for v, err := range seq {
				if err != nil {
					if !yield(0, err) {
						return
					}
					continue
				}
				if !yield(v+1, nil) {
					return
				}
			}
		}
	}

	doubleErr := func(seq iter.Seq2[int, error]) iter.Seq2[int, error] {
		return func(yield func(int, error) bool) {
			for v, err := range seq {
				if err != nil {
					if !yield(0, err) {
						return
					}
					continue
				}
				if !yield(v*2, nil) {
					return
				}
			}
		}
	}

	pipeline := PipeWithErrors(addErr, doubleErr)
	input := Safe(slices.Values([]int{1, 2, 3}))

	var result []int
	for v, err := range pipeline(input) {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, v)
	}

	expected := []int{4, 6, 8} // (1+1)*2, (2+1)*2, (3+1)*2
	if !slices.Equal(result, expected) {
		t.Errorf("PipeWithErrors failed: expected %v, got %v", expected, result)
	}
}

func TestChainWithErrors(t *testing.T) {
	addErr := func(seq iter.Seq2[int, error]) iter.Seq2[int, error] {
		return func(yield func(int, error) bool) {
			for v, err := range seq {
				if err != nil {
					if !yield(0, err) {
						return
					}
					continue
				}
				if !yield(v+1, nil) {
					return
				}
			}
		}
	}

	pipeline := ChainWithErrors(addErr, addErr, addErr)
	input := Safe(slices.Values([]int{1, 2, 3}))

	var result []int
	for v, err := range pipeline(input) {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, v)
	}

	expected := []int{4, 5, 6} // 1+1+1+1, 2+1+1+1, 3+1+1+1
	if !slices.Equal(result, expected) {
		t.Errorf("ChainWithErrors failed: expected %v, got %v", expected, result)
	}
}

// ============================================================================
// JSONSTRING TESTS
// ============================================================================

func TestNewJSONString(t *testing.T) {
	tests := []struct {
		name  string
		input any
		valid bool
	}{
		{"simple string", "hello", true},
		{"number", 42, true},
		{"bool", true, true},
		{"map", map[string]any{"key": "value"}, true},
		{"slice", []int{1, 2, 3}, true},
		{"nil", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			js, err := NewJSONString(tt.input)
			if !tt.valid && err == nil {
				t.Error("Expected error but got none")
			}
			if tt.valid && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if tt.valid && !js.IsValid() {
				t.Errorf("Expected valid JSON, got: %s", js)
			}
		})
	}
}

func TestJSONStringParse(t *testing.T) {
	js := JSONString(`{"name":"Alice","age":30}`)
	result, err := js.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("Expected map[string]any")
	}

	if m["name"] != "Alice" {
		t.Errorf("Expected name=Alice, got %v", m["name"])
	}
}

func TestJSONStringMustParse(t *testing.T) {
	js := JSONString(`[1,2,3]`)
	result := js.MustParse()

	arr, ok := result.([]any)
	if !ok {
		t.Fatal("Expected []any")
	}

	if len(arr) != 3 {
		t.Errorf("Expected length 3, got %d", len(arr))
	}
}

func TestJSONStringMustParsePanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for invalid JSON")
		}
	}()

	js := JSONString(`{invalid}`)
	js.MustParse()
}

func TestJSONStringIsValid(t *testing.T) {
	tests := []struct {
		json  JSONString
		valid bool
	}{
		{`{"valid":true}`, true},
		{`[1,2,3]`, true},
		{`"string"`, true},
		{`42`, true},
		{`{invalid}`, false},
		{``, false},
	}

	for _, tt := range tests {
		if tt.json.IsValid() != tt.valid {
			t.Errorf("IsValid(%s) = %v, want %v", tt.json, !tt.valid, tt.valid)
		}
	}
}

func TestJSONStringPretty(t *testing.T) {
	js := JSONString(`{"a":1,"b":2}`)
	pretty := js.Pretty()

	if pretty == string(js) {
		t.Error("Pretty should format JSON with indentation")
	}

	// Invalid JSON should return original
	invalid := JSONString(`{bad}`)
	if invalid.Pretty() != string(invalid) {
		t.Error("Pretty should return original for invalid JSON")
	}
}

func TestJSONStringString(t *testing.T) {
	js := JSONString(`{"test":true}`)
	if js.String() != `{"test":true}` {
		t.Errorf("String() failed: got %s", js.String())
	}
}

// ============================================================================
// RECORD BUILDER TESTS
// ============================================================================

func TestMakeRecord(t *testing.T) {
	r := MakeMutableRecord().
		String("name", "Alice").
		Int("age", 30).
		Float("score", 95.5).
		Bool("active", true)

	if r.fields["name"] != "Alice" {
		t.Errorf("Expected name=Alice, got %v", r.fields["name"])
	}
	if r.fields["age"] != int64(30) {
		t.Errorf("Expected age=30, got %v", r.fields["age"])
	}
	if r.fields["score"] != 95.5 {
		t.Errorf("Expected score=95.5, got %v", r.fields["score"])
	}
	if r.fields["active"] != true {
		t.Errorf("Expected active=true, got %v", r.fields["active"])
	}
}

func TestRecordWithTime(t *testing.T) {
	now := time.Now()
	r := MakeMutableRecord().Time("timestamp", now)

	if r.fields["timestamp"] != now {
		t.Errorf("Expected timestamp=%v, got %v", now, r.fields["timestamp"])
	}
}

func TestRecordWithNestedRecord(t *testing.T) {
	nested := MakeMutableRecord().
		String("city", "NYC").
		Freeze()
	r := MakeMutableRecord().
		String("name", "Alice").
		Nested("address", nested).
		Freeze()

	addr, ok := Get[Record](r, "address")
	if !ok {
		t.Fatal("Expected nested Record")
	}
	if GetOr(addr, "city", "") != "NYC" {
		t.Errorf("Expected city=NYC, got %v", GetOr(addr, "city", ""))
	}
}

func TestRecordWithJSONString(t *testing.T) {
	js := JSONString(`{"data":"value"}`)
	r := MakeMutableRecord().JSONString("json", js)

	result, ok := r.fields["json"].(JSONString)
	if !ok {
		t.Fatal("Expected JSONString")
	}
	if result != js {
		t.Errorf("Expected %s, got %s", js, result)
	}
}

// ============================================================================
// RECORD ACCESS TESTS
// ============================================================================

func TestGet(t *testing.T) {
	r := MakeMutableRecord().
		String("name", "Alice").
		Int("age", 30).
		Float("score", 95.5).
		Bool("active", true).
		Freeze()

	// Direct type match
	name, ok := Get[string](r, "name")
	if !ok || name != "Alice" {
		t.Errorf("Get[string] failed: got %v, %v", name, ok)
	}

	// Type conversion int64 -> int
	age, ok := Get[int](r, "age")
	if !ok || age != 30 {
		t.Errorf("Get[int] with conversion failed: got %v, %v", age, ok)
	}

	// Non-existent field
	_, ok = Get[string](r, "missing")
	if ok {
		t.Error("Get should return false for missing field")
	}
}

func TestGetOr(t *testing.T) {
	r := func() Record {
		r := MakeMutableRecord()
		r.fields["name"] = "Alice"
		return r.Freeze()
	}()

	// Existing field
	name := GetOr(r, "name", "default")
	if name != "Alice" {
		t.Errorf("GetOr failed: expected Alice, got %v", name)
	}

	// Missing field - use default
	age := GetOr(r, "age", 25)
	if age != 25 {
		t.Errorf("GetOr default failed: expected 25, got %v", age)
	}
}

func TestMutableRecordSet(t *testing.T) {
	m := MakeMutableRecord().String("name", "Alice")
	m2 := Set(m, "age", int64(30))

	// Both m and m2 should point to same underlying map (mutation)
	if m2.fields["age"] != int64(30) {
		t.Errorf("Set failed: expected 30, got %v", m2.fields["age"])
	}
	if m2.fields["name"] != "Alice" {
		t.Error("Set should preserve existing fields")
	}
}

func TestRecordSetImmutable(t *testing.T) {
	r := MakeMutableRecord().
		String("name", "Alice").
		Freeze()
	r2 := SetImmutable(r, "age", int64(30))

	// Original should be unchanged
	if r.Has("age") {
		t.Error("SetImmutable should not modify original record")
	}

	// New record should have field
	if GetOr(r2, "age", int64(0)) != int64(30) {
		t.Errorf("SetImmutable failed: expected 30, got %v", GetOr(r2, "age", int64(0)))
	}
	if GetOr(r2, "name", "") != "Alice" {
		t.Error("SetImmutable should preserve existing fields")
	}
}

func TestRecordToMutable(t *testing.T) {
	// Create an immutable record
	original := MakeMutableRecord().
		String("name", "Alice").
		Int("age", 30).
		Float("score", 95.5).
		Freeze()

	// Convert to mutable for modification
	mutable := original.ToMutable().
		Int("age", 31).
		String("city", "NYC")

	updated := mutable.Freeze()

	// Original should be unchanged (shallow copy preserves immutability)
	if GetOr(original, "age", int64(0)) != int64(30) {
		t.Error("ToMutable should not modify original record")
	}
	if original.Has("city") {
		t.Error("ToMutable should create a copy, not mutate original")
	}

	// Updated record should have changes
	if GetOr(updated, "age", int64(0)) != int64(31) {
		t.Errorf("ToMutable update failed: expected age=31, got %v", GetOr(updated, "age", int64(0)))
	}
	if GetOr(updated, "city", "") != "NYC" {
		t.Errorf("ToMutable addition failed: expected city=NYC, got %v", GetOr(updated, "city", ""))
	}
	if GetOr(updated, "name", "") != "Alice" {
		t.Error("ToMutable should preserve existing fields")
	}
}

func TestRecordHas(t *testing.T) {
	r := MakeMutableRecord().
		String("name", "Alice").
		Freeze()

	if !r.Has("name") {
		t.Error("Has should return true for existing field")
	}
	if r.Has("age") {
		t.Error("Has should return false for missing field")
	}
}

func TestRecordKeys(t *testing.T) {
	r := MakeMutableRecord().
		String("name", "Alice").
		Int("age", 30).
		Freeze()
	keys := r.Keys()

	if len(keys) != 2 {
		t.Errorf("Expected 2 keys, got %d", len(keys))
	}

	hasName := false
	hasAge := false
	for _, k := range keys {
		if k == "name" {
			hasName = true
		}
		if k == "age" {
			hasAge = true
		}
	}

	if !hasName || !hasAge {
		t.Errorf("Keys missing expected fields: %v", keys)
	}
}

// ============================================================================
// TYPE CONVERSION TESTS
// ============================================================================

func TestConvertToInt64(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected int64
		ok       bool
	}{
		{"int64", int64(42), 42, true},
		{"float64", 42.0, 42, true},
		{"string", "42", 42, true},
		{"bool true", true, 1, true},
		{"bool false", false, 0, true},
		{"invalid string", "abc", 0, false},
		// Non-canonical types should NOT convert
		{"int (non-canonical)", 42, 0, false},
		{"int32 (non-canonical)", int32(42), 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := convertToInt64(tt.input)
			if ok != tt.ok {
				t.Errorf("convertToInt64 ok = %v, want %v", ok, tt.ok)
			}
			if ok && result != tt.expected {
				t.Errorf("convertToInt64 = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestConvertToFloat64(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected float64
		ok       bool
	}{
		{"float64", 42.5, 42.5, true},
		{"int64", int64(42), 42.0, true},
		{"string", "42.5", 42.5, true},
		{"invalid string", "abc", 0, false},
		// Non-canonical types should NOT convert
		{"int (non-canonical)", 42, 0, false},
		{"float32 (non-canonical)", float32(42.5), 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := convertToFloat64(tt.input)
			if ok != tt.ok {
				t.Errorf("convertToFloat64 ok = %v, want %v", ok, tt.ok)
			}
			if ok && result != tt.expected {
				t.Errorf("convertToFloat64 = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestConvertToString(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected string
		ok       bool
	}{
		{"string", "hello", "hello", true},
		{"bytes", []byte("hello"), "hello", true},
		{"int", 42, "42", true},
		{"bool", true, "true", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := convertToString(tt.input)
			if ok != tt.ok {
				t.Errorf("convertToString ok = %v, want %v", ok, tt.ok)
			}
			if result != tt.expected {
				t.Errorf("convertToString = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestConvertToBool(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected bool
		ok       bool
	}{
		{"bool true", true, true, true},
		{"bool false", false, false, true},
		{"int64 zero", int64(0), false, true},
		{"int64 nonzero", int64(42), true, true},
		{"float64 zero", 0.0, false, true},
		{"float64 nonzero", 42.5, true, true},
		{"string empty", "", false, true},
		{"string nonempty", "hello", true, true},
		// Non-canonical types should NOT convert
		{"int (non-canonical)", 0, false, false},
		{"int (non-canonical)", 42, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := convertToBool(tt.input)
			if ok != tt.ok {
				t.Errorf("convertToBool ok = %v, want %v", ok, tt.ok)
			}
			if ok && result != tt.expected {
				t.Errorf("convertToBool = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestConvertToTime(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name  string
		input any
		ok    bool
	}{
		{"time.Time", now, true},
		{"RFC3339", "2024-01-02T15:04:05Z", true},
		{"SQL datetime", "2024-01-02 15:04:05", true},
		{"Unix timestamp", int64(1704207845), true},
		{"invalid", "not a time", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := convertToTime(tt.input)
			if ok != tt.ok {
				t.Errorf("convertToTime ok = %v, want %v", ok, tt.ok)
			}
		})
	}
}

// ============================================================================
// VALIDATION TESTS
// ============================================================================

func TestValidateRecord(t *testing.T) {
	// Valid record
	valid := func() Record {
		r := MakeMutableRecord()
		r.fields["name"] = "Alice"
		r.fields["age"] = int64(30)
		r.fields["active"] = true
		return r.Freeze()
	}()
	if err := ValidateRecord(valid); err != nil {
		t.Errorf("ValidateRecord failed on valid record: %v", err)
	}

	// Invalid record with unsupported type
	invalid := func() Record {
		r := MakeMutableRecord()
		r.fields["name"] = "Alice"
		r.fields["data"] = struct{}{}
		return r.Freeze()
	}()
	if err := ValidateRecord(invalid); err == nil {
		t.Error("ValidateRecord should fail on invalid type")
	}
}

func TestField(t *testing.T) {
	r := Field("name", "Alice")

	if r.Len() != 1 {
		t.Errorf("Field should create single-field record, got %d fields", r.Len())
	}
	if GetOr(r, "name", "") != "Alice" {
		t.Errorf("Field value incorrect: got %v", GetOr(r, "name", ""))
	}
}

// ============================================================================
// CONVERSION UTILITIES TESTS
// ============================================================================

func TestFrom(t *testing.T) {
	// Test creating iterator from slice
	data := []int{1, 2, 3, 4, 5}
	seq := From(data)

	result := slices.Collect(seq)
	expected := []int{1, 2, 3, 4, 5}

	if !slices.Equal(result, expected) {
		t.Errorf("From failed: expected %v, got %v", expected, result)
	}
}

func TestFromEmptySlice(t *testing.T) {
	// Test with empty slice
	data := []string{}
	seq := From(data)

	result := slices.Collect(seq)

	if len(result) != 0 {
		t.Errorf("From with empty slice should return empty iterator, got %d items", len(result))
	}
}

func TestFromRecords(t *testing.T) {
	// Test with Records
	r1 := MakeMutableRecord()
	r1.fields["name"] = "Alice"
	r1.fields["age"] = int64(30)

	r2 := MakeMutableRecord()
	r2.fields["name"] = "Bob"
	r2.fields["age"] = int64(25)

	records := []Record{r1.Freeze(), r2.Freeze()}

	seq := From(records)
	result := slices.Collect(seq)

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}

	if GetOr(result[0], "name", "") != "Alice" {
		t.Errorf("First record name should be Alice, got %v", GetOr(result[0], "name", ""))
	}
}

func TestSafe(t *testing.T) {
	input := slices.Values([]int{1, 2, 3})
	safe := Safe(input)

	var result []int
	for v, err := range safe {
		if err != nil {
			t.Errorf("Safe should never produce errors, got: %v", err)
		}
		result = append(result, v)
	}

	expected := []int{1, 2, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("Safe failed: expected %v, got %v", expected, result)
	}
}

func TestUnsafe(t *testing.T) {
	// Create error-free sequence
	safe := Safe(slices.Values([]int{1, 2, 3}))
	unsafe := Unsafe(safe)

	result := slices.Collect(unsafe)
	expected := []int{1, 2, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("Unsafe failed: expected %v, got %v", expected, result)
	}
}

func TestUnsafePanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Unsafe should panic on error")
		}
	}()

	// Create sequence with error
	errSeq := func(yield func(int, error) bool) {
		yield(1, nil)
		yield(0, &testError{"test error"})
	}

	unsafe := Unsafe(errSeq)
	for range unsafe {
		// Should panic when error is encountered
	}
}

func TestIgnoreErrors(t *testing.T) {
	// Create sequence with some errors
	errSeq := func(yield func(int, error) bool) {
		yield(1, nil)
		yield(0, &testError{"error"})
		yield(2, nil)
		yield(0, &testError{"error"})
		yield(3, nil)
	}

	result := slices.Collect(IgnoreErrors(errSeq))
	expected := []int{1, 2, 3} // Errors are skipped

	if !slices.Equal(result, expected) {
		t.Errorf("IgnoreErrors failed: expected %v, got %v", expected, result)
	}
}

// ============================================================================
// MATERIALIZE TESTS
// ============================================================================

func TestMaterialize(t *testing.T) {
	tagSeq := func(yield func(string) bool) {
		tags := []string{"urgent", "work", "important"}
		for _, tag := range tags {
			if !yield(tag) {
				return
			}
		}
	}

	r := MakeMutableRecord()
	r.fields["id"] = int64(1)
	r.fields["tags"] = iter.Seq[string](tagSeq)
	input := slices.Values([]Record{r.Freeze()})

	filter := Materialize("tags", "tags_key", ",")
	result := slices.Collect(filter(input))

	if len(result) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(result))
	}

	tagsKey, ok := Get[string](result[0], "tags_key")
	if !ok {
		t.Fatal("tags_key should be a string")
	}

	expected := "urgent,work,important"
	if tagsKey != expected {
		t.Errorf("Materialize failed: expected %s, got %s", expected, tagsKey)
	}

	// Original field should still exist
	if !result[0].Has("tags") {
		t.Error("Materialize should preserve original field")
	}
}

func TestMaterializeJSON(t *testing.T) {
	nestedRecord := MakeMutableRecord().
		String("city", "NYC").
		String("zip", "10001").
		Freeze()

	r := MakeMutableRecord().
		Int("id", 1).
		Nested("address", nestedRecord).
		Freeze()
	input := slices.Values([]Record{r})

	filter := MaterializeJSON("address", "address_json")
	result := slices.Collect(filter(input))

	if len(result) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(result))
	}

	addressJSON, ok := Get[JSONString](result[0], "address_json")
	if !ok {
		t.Fatal("address_json should be JSONString")
	}

	if !addressJSON.IsValid() {
		t.Error("MaterializeJSON should produce valid JSON")
	}
}

// ============================================================================
// FLATTEN TESTS
// ============================================================================

func TestDotFlatten(t *testing.T) {
	nestedR := MakeMutableRecord()
	nestedR.fields["name"] = "Alice"
	nestedR.fields["city"] = "NYC"
	nested := nestedR.Freeze()

	r := MakeMutableRecord()
	r.fields["id"] = int64(1)
	r.fields["user"] = nested
	input := slices.Values([]Record{r.Freeze()})

	filter := DotFlatten(".", "user")
	result := slices.Collect(filter(input))

	if len(result) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(result))
	}

	if GetOr(result[0], "user.name", "") != "Alice" {
		t.Errorf("DotFlatten failed: expected user.name=Alice, got %v", GetOr(result[0], "user.name", ""))
	}
	if GetOr(result[0], "user.city", "") != "NYC" {
		t.Errorf("DotFlatten failed: expected user.city=NYC, got %v", GetOr(result[0], "user.city", ""))
	}
}

func TestDotFlattenWithSequences(t *testing.T) {
	tagSeq := func(yield func(string) bool) {
		for _, tag := range []string{"a", "b"} {
			if !yield(tag) {
				return
			}
		}
	}

	scoreSeq := func(yield func(int) bool) {
		for _, score := range []int{10, 20} {
			if !yield(score) {
				return
			}
		}
	}

	r := MakeMutableRecord()
	r.fields["id"] = int64(1)
	r.fields["tags"] = iter.Seq[string](tagSeq)
	r.fields["scores"] = iter.Seq[int](scoreSeq)
	input := slices.Values([]Record{r.Freeze()})

	filter := DotFlatten(".", "tags", "scores")
	result := slices.Collect(filter(input))

	// Should create 2 records (dot product of sequences)
	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}

	// First record: tags=a, scores=10
	if GetOr(result[0], "tags", "") != "a" || GetOr(result[0], "scores", 0) != 10 {
		t.Errorf("DotFlatten first record failed: %v", result[0])
	}

	// Second record: tags=b, scores=20
	if GetOr(result[1], "tags", "") != "b" || GetOr(result[1], "scores", 0) != 20 {
		t.Errorf("DotFlatten second record failed: %v", result[1])
	}
}

func TestCrossFlatten(t *testing.T) {
	tagSeq := func(yield func(string) bool) {
		for _, tag := range []string{"a", "b"} {
			if !yield(tag) {
				return
			}
		}
	}

	r := MakeMutableRecord()
	r.fields["id"] = int64(1)
	r.fields["tags"] = iter.Seq[string](tagSeq)
	input := slices.Values([]Record{r.Freeze()})

	filter := CrossFlatten(".", "tags")
	result := slices.Collect(filter(input))

	// Should create 2 records (one per tag)
	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}

	if GetOr(result[0], "tags", "") != "a" {
		t.Errorf("CrossFlatten first record failed: expected tags=a, got %v", GetOr(result[0], "tags", ""))
	}
	if GetOr(result[1], "tags", "") != "b" {
		t.Errorf("CrossFlatten second record failed: expected tags=b, got %v", GetOr(result[1], "tags", ""))
	}

	// Both should have id=1
	if GetOr(result[0], "id", int64(0)) != int64(1) || GetOr(result[1], "id", int64(0)) != int64(1) {
		t.Error("CrossFlatten should preserve non-sequence fields")
	}
}

func TestCrossFlattenCartesianProduct(t *testing.T) {
	tagSeq := func(yield func(string) bool) {
		for _, tag := range []string{"a", "b"} {
			if !yield(tag) {
				return
			}
		}
	}

	colorSeq := func(yield func(string) bool) {
		for _, color := range []string{"red", "blue"} {
			if !yield(color) {
				return
			}
		}
	}

	r := MakeMutableRecord()
	r.fields["tags"] = iter.Seq[string](tagSeq)
	r.fields["colors"] = iter.Seq[string](colorSeq)
	input := slices.Values([]Record{r.Freeze()})

	filter := CrossFlatten(".", "tags", "colors")
	result := slices.Collect(filter(input))

	// Should create 4 records (2x2 cartesian product)
	if len(result) != 4 {
		t.Fatalf("Expected 4 records (2x2 cartesian), got %d", len(result))
	}
}

// ============================================================================
// HELPER TYPES
// ============================================================================

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
