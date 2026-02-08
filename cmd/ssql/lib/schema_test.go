package lib

import (
	"bytes"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

func TestNewSchema(t *testing.T) {
	s := NewSchema()
	if s == nil {
		t.Fatal("NewSchema returned nil")
	}
	if len(s.Fields) != 0 {
		t.Errorf("expected empty Fields, got %v", s.Fields)
	}
	if len(s.Types) != 0 {
		t.Errorf("expected empty Types, got %v", s.Types)
	}
}

func TestSchemaAddField(t *testing.T) {
	s := NewSchema()
	s.AddField("name", TypeString)
	s.AddField("age", TypeInt)
	s.AddField("active", TypeBool)

	if len(s.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(s.Fields))
	}

	// Verify order preserved
	expected := []string{"name", "age", "active"}
	for i, f := range expected {
		if s.Fields[i] != f {
			t.Errorf("field %d: expected %q, got %q", i, f, s.Fields[i])
		}
	}

	// Verify types
	if s.TypeOf("name") != TypeString {
		t.Errorf("expected name type %q, got %q", TypeString, s.TypeOf("name"))
	}
	if s.TypeOf("age") != TypeInt {
		t.Errorf("expected age type %q, got %q", TypeInt, s.TypeOf("age"))
	}
}

func TestSchemaAddFieldDuplicate(t *testing.T) {
	s := NewSchema()
	s.AddField("name", TypeString)
	s.AddField("name", TypeInt) // Update type, don't duplicate

	if len(s.Fields) != 1 {
		t.Errorf("expected 1 field, got %d", len(s.Fields))
	}
	if s.TypeOf("name") != TypeInt {
		t.Errorf("expected type to be updated to %q, got %q", TypeInt, s.TypeOf("name"))
	}
}

func TestSchemaRemoveField(t *testing.T) {
	s := NewSchema()
	s.AddField("a", TypeString)
	s.AddField("b", TypeInt)
	s.AddField("c", TypeBool)

	s.RemoveField("b")

	if len(s.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(s.Fields))
	}
	if s.HasField("b") {
		t.Error("field 'b' should have been removed")
	}

	// Verify order preserved
	if s.Fields[0] != "a" || s.Fields[1] != "c" {
		t.Errorf("expected [a, c], got %v", s.Fields)
	}
}

func TestSchemaRenameField(t *testing.T) {
	s := NewSchema()
	s.AddField("a", TypeString)
	s.AddField("b", TypeInt)
	s.AddField("c", TypeBool)

	s.RenameField("b", "renamed")

	if s.HasField("b") {
		t.Error("old field name 'b' should not exist")
	}
	if !s.HasField("renamed") {
		t.Error("new field name 'renamed' should exist")
	}
	if s.TypeOf("renamed") != TypeInt {
		t.Errorf("renamed field should preserve type, expected %q, got %q", TypeInt, s.TypeOf("renamed"))
	}

	// Verify position preserved
	if s.Fields[1] != "renamed" {
		t.Errorf("renamed field should be at position 1, got %v", s.Fields)
	}
}

func TestSchemaRenameNonexistent(t *testing.T) {
	s := NewSchema()
	s.AddField("a", TypeString)

	s.RenameField("nonexistent", "new") // Should be no-op

	if len(s.Fields) != 1 {
		t.Errorf("expected 1 field, got %d", len(s.Fields))
	}
	if s.HasField("new") {
		t.Error("renaming nonexistent field should not create new field")
	}
}

func TestSchemaSetType(t *testing.T) {
	s := NewSchema()
	s.AddField("x", TypeString)

	s.SetType("x", TypeFloat)
	if s.TypeOf("x") != TypeFloat {
		t.Errorf("expected type %q, got %q", TypeFloat, s.TypeOf("x"))
	}

	// SetType on new field should add it
	s.SetType("y", TypeBool)
	if !s.HasField("y") {
		t.Error("SetType on new field should add it")
	}
}

func TestSchemaClone(t *testing.T) {
	s := NewSchema()
	s.AddField("a", TypeString)
	s.AddField("b", TypeInt)

	clone := s.Clone()

	// Modify original
	s.AddField("c", TypeBool)
	s.SetType("a", TypeFloat)

	// Clone should be unchanged
	if len(clone.Fields) != 2 {
		t.Errorf("clone should have 2 fields, got %d", len(clone.Fields))
	}
	if clone.TypeOf("a") != TypeString {
		t.Errorf("clone type should be unchanged, expected %q, got %q", TypeString, clone.TypeOf("a"))
	}
	if clone.HasField("c") {
		t.Error("clone should not have field 'c'")
	}
}

func TestSchemaWriteHeader(t *testing.T) {
	s := NewSchema()
	s.AddField("name", TypeString)
	s.AddField("age", TypeInt)

	var buf bytes.Buffer
	err := s.WriteHeader(&buf)
	if err != nil {
		t.Fatalf("WriteHeader error: %v", err)
	}

	output := buf.String()

	// Should contain _schema key
	if !bytes.Contains(buf.Bytes(), []byte(`"_schema"`)) {
		t.Errorf("output should contain _schema key: %s", output)
	}

	// Should end with newline
	if output[len(output)-1] != '\n' {
		t.Error("output should end with newline")
	}
}

func TestParseSchemaHeader(t *testing.T) {
	data := map[string]any{
		"_schema": map[string]any{
			"fields": []any{"name", "age", "active"},
			"types": map[string]any{
				"name":   "string",
				"age":    "int",
				"active": "bool",
			},
		},
	}

	schema, ok := ParseSchemaHeader(data)
	if !ok {
		t.Fatal("ParseSchemaHeader should return true for schema data")
	}

	if len(schema.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(schema.Fields))
	}

	// Verify field order
	expected := []string{"name", "age", "active"}
	for i, f := range expected {
		if schema.Fields[i] != f {
			t.Errorf("field %d: expected %q, got %q", i, f, schema.Fields[i])
		}
	}

	// Verify types
	if schema.TypeOf("name") != TypeString {
		t.Errorf("expected name type %q, got %q", TypeString, schema.TypeOf("name"))
	}
	if schema.TypeOf("age") != TypeInt {
		t.Errorf("expected age type %q, got %q", TypeInt, schema.TypeOf("age"))
	}
	if schema.TypeOf("active") != TypeBool {
		t.Errorf("expected active type %q, got %q", TypeBool, schema.TypeOf("active"))
	}
}

func TestParseSchemaHeaderNotSchema(t *testing.T) {
	// Regular data record
	data := map[string]any{
		"name": "Alice",
		"age":  30,
	}

	schema, ok := ParseSchemaHeader(data)
	if ok {
		t.Error("ParseSchemaHeader should return false for non-schema data")
	}
	if schema != nil {
		t.Error("schema should be nil for non-schema data")
	}
}

func TestParseSchemaHeaderFromBytes(t *testing.T) {
	jsonData := []byte(`{"_schema":{"fields":["x","y"],"types":{"x":"float","y":"float"}}}`)

	schema, ok := ParseSchemaHeaderFromBytes(jsonData)
	if !ok {
		t.Fatal("ParseSchemaHeaderFromBytes should return true")
	}

	if len(schema.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(schema.Fields))
	}
	if schema.TypeOf("x") != TypeFloat {
		t.Errorf("expected x type %q, got %q", TypeFloat, schema.TypeOf("x"))
	}
}

func TestParseSchemaHeaderFromBytesInvalid(t *testing.T) {
	// Invalid JSON
	_, ok := ParseSchemaHeaderFromBytes([]byte(`not json`))
	if ok {
		t.Error("should return false for invalid JSON")
	}

	// Valid JSON but not schema
	_, ok = ParseSchemaHeaderFromBytes([]byte(`{"name":"Alice"}`))
	if ok {
		t.Error("should return false for non-schema JSON")
	}
}

func TestSchemaRoundTrip(t *testing.T) {
	// Create schema
	original := NewSchema()
	original.AddField("name", TypeString)
	original.AddField("age", TypeInt)
	original.AddField("score", TypeFloat)
	original.AddField("active", TypeBool)

	// Write to buffer
	var buf bytes.Buffer
	err := original.WriteHeader(&buf)
	if err != nil {
		t.Fatalf("WriteHeader error: %v", err)
	}

	// Parse back
	parsed, ok := ParseSchemaHeaderFromBytes(buf.Bytes())
	if !ok {
		t.Fatal("failed to parse written schema")
	}

	// Compare
	if len(parsed.Fields) != len(original.Fields) {
		t.Errorf("field count mismatch: %d vs %d", len(parsed.Fields), len(original.Fields))
	}

	for i, f := range original.Fields {
		if parsed.Fields[i] != f {
			t.Errorf("field %d mismatch: %q vs %q", i, f, parsed.Fields[i])
		}
		if parsed.TypeOf(f) != original.TypeOf(f) {
			t.Errorf("type mismatch for %q: %q vs %q", f, original.TypeOf(f), parsed.TypeOf(f))
		}
	}
}

func TestInferFromRecord(t *testing.T) {
	record := ssql.MakeMutableRecord().
		String("name", "Alice").
		Int("age", int64(30)).
		Float("score", 95.5).
		Bool("active", true).
		Freeze()

	schema := InferFromRecord(record)

	// Check all fields present with correct types
	if schema.TypeOf("name") != TypeString {
		t.Errorf("expected name type %q, got %q", TypeString, schema.TypeOf("name"))
	}
	if schema.TypeOf("age") != TypeInt {
		t.Errorf("expected age type %q, got %q", TypeInt, schema.TypeOf("age"))
	}
	if schema.TypeOf("score") != TypeFloat {
		t.Errorf("expected score type %q, got %q", TypeFloat, schema.TypeOf("score"))
	}
	if schema.TypeOf("active") != TypeBool {
		t.Errorf("expected active type %q, got %q", TypeBool, schema.TypeOf("active"))
	}
}

func TestInferFromRecordOrdered(t *testing.T) {
	record := ssql.MakeMutableRecord().
		String("c", "third").
		String("a", "first").
		String("b", "second").
		Freeze()

	// Specify desired order
	schema := InferFromRecordOrdered(record, []string{"a", "b", "c"})

	// Fields should be in specified order
	if schema.Fields[0] != "a" || schema.Fields[1] != "b" || schema.Fields[2] != "c" {
		t.Errorf("expected [a, b, c], got %v", schema.Fields)
	}
}

func TestInferTypeString(t *testing.T) {
	tests := []struct {
		value    any
		expected string
	}{
		{int64(42), TypeInt},
		{float64(3.14), TypeFloat},
		{true, TypeBool},
		{"hello", TypeString},
		{[]any{1, 2, 3}, TypeJSON},
		{map[string]any{"x": 1}, TypeJSON},
		{nil, TypeString}, // nil defaults to string
	}

	for _, tt := range tests {
		got := InferTypeString(tt.value)
		if got != tt.expected {
			t.Errorf("InferTypeString(%T) = %q, expected %q", tt.value, got, tt.expected)
		}
	}
}

func TestFieldTypeConversions(t *testing.T) {
	// FieldType -> Schema type
	tests := []struct {
		fieldType  ssql.FieldType
		schemaType string
	}{
		{ssql.FieldTypeInt, TypeInt},
		{ssql.FieldTypeFloat, TypeFloat},
		{ssql.FieldTypeBool, TypeBool},
		{ssql.FieldTypeString, TypeString},
		{ssql.FieldTypeAuto, TypeString}, // Auto maps to string
	}

	for _, tt := range tests {
		got := FieldTypeToSchemaType(tt.fieldType)
		if got != tt.schemaType {
			t.Errorf("FieldTypeToSchemaType(%v) = %q, expected %q", tt.fieldType, got, tt.schemaType)
		}
	}

	// Schema type -> FieldType
	reverseTests := []struct {
		schemaType string
		fieldType  ssql.FieldType
	}{
		{TypeInt, ssql.FieldTypeInt},
		{TypeFloat, ssql.FieldTypeFloat},
		{TypeBool, ssql.FieldTypeBool},
		{TypeString, ssql.FieldTypeString},
		{TypeJSON, ssql.FieldTypeAuto},
		{"unknown", ssql.FieldTypeString}, // Unknown maps to string
	}

	for _, tt := range reverseTests {
		got := SchemaTypeToFieldType(tt.schemaType)
		if got != tt.fieldType {
			t.Errorf("SchemaTypeToFieldType(%q) = %v, expected %v", tt.schemaType, got, tt.fieldType)
		}
	}
}

func TestSchemaLen(t *testing.T) {
	s := NewSchema()
	if s.Len() != 0 {
		t.Errorf("expected Len() = 0, got %d", s.Len())
	}

	s.AddField("a", TypeString)
	s.AddField("b", TypeInt)
	if s.Len() != 2 {
		t.Errorf("expected Len() = 2, got %d", s.Len())
	}

	s.RemoveField("a")
	if s.Len() != 1 {
		t.Errorf("expected Len() = 1, got %d", s.Len())
	}
}

func TestSchemaString(t *testing.T) {
	s := NewSchema()
	s.AddField("x", TypeInt)

	str := s.String()
	if str == "" {
		t.Error("String() should not return empty")
	}
	if !bytes.Contains([]byte(str), []byte("_schema")) {
		t.Errorf("String() should contain _schema: %s", str)
	}
}

// Integration tests for ReadJSONLWithSchema and WriteJSONLWithSchema

func TestWriteReadJSONLWithSchemaRoundTrip(t *testing.T) {
	// Create schema
	schema := NewSchema()
	schema.AddField("name", TypeString)
	schema.AddField("age", TypeInt)
	schema.AddField("score", TypeFloat)

	// Create records
	records := []ssql.Record{
		ssql.MakeMutableRecord().
			String("name", "Alice").
			Int("age", int64(30)).
			Float("score", 95.5).
			Freeze(),
		ssql.MakeMutableRecord().
			String("name", "Bob").
			Int("age", int64(25)).
			Float("score", 88.0).
			Freeze(),
	}

	// Write to buffer
	var buf bytes.Buffer
	recordIter := func(yield func(ssql.Record) bool) {
		for _, r := range records {
			if !yield(r) {
				return
			}
		}
	}

	err := WriteJSONLWithSchema(&buf, schema, recordIter)
	if err != nil {
		t.Fatalf("WriteJSONLWithSchema error: %v", err)
	}

	// Read back
	result := ReadJSONLWithSchema(&buf)

	// Verify schema
	if result.Schema == nil {
		t.Fatal("expected schema, got nil")
	}
	if len(result.Schema.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(result.Schema.Fields))
	}
	if result.Schema.TypeOf("name") != TypeString {
		t.Errorf("expected name type %q, got %q", TypeString, result.Schema.TypeOf("name"))
	}

	// Verify records
	var readRecords []ssql.Record
	for r := range result.Records {
		readRecords = append(readRecords, r)
	}

	if len(readRecords) != 2 {
		t.Fatalf("expected 2 records, got %d", len(readRecords))
	}

	// Check first record
	name := ssql.GetOr(readRecords[0], "name", "")
	if name != "Alice" {
		t.Errorf("expected name 'Alice', got %q", name)
	}
	age := ssql.GetOr(readRecords[0], "age", int64(0))
	if age != int64(30) {
		t.Errorf("expected age 30, got %d", age)
	}
}

func TestReadJSONLWithSchemaNoSchema(t *testing.T) {
	// JSONL without schema header
	input := `{"name":"Alice","age":30}
{"name":"Bob","age":25}
`
	result := ReadJSONLWithSchema(bytes.NewBufferString(input))

	// Should have nil schema
	if result.Schema != nil {
		t.Error("expected nil schema for data without schema header")
	}

	// Should still read records
	var records []ssql.Record
	for r := range result.Records {
		records = append(records, r)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	name := ssql.GetOr(records[0], "name", "")
	if name != "Alice" {
		t.Errorf("expected name 'Alice', got %q", name)
	}
}

func TestReadJSONLWithSchemaTypeCoercion(t *testing.T) {
	// Schema says age is int, but JSON has it as float (30.0)
	input := `{"_schema":{"fields":["name","age"],"types":{"name":"string","age":"int"}}}
{"name":"Alice","age":30.5}
{"name":"Bob","age":25.9}
`
	result := ReadJSONLWithSchema(bytes.NewBufferString(input))

	if result.Schema == nil {
		t.Fatal("expected schema")
	}

	var records []ssql.Record
	for r := range result.Records {
		records = append(records, r)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Age should be coerced to int64 (truncated)
	age := ssql.GetOr(records[0], "age", int64(0))
	if age != int64(30) {
		t.Errorf("expected age 30 (coerced from 30.5), got %d", age)
	}

	age2 := ssql.GetOr(records[1], "age", int64(0))
	if age2 != int64(25) {
		t.Errorf("expected age 25 (coerced from 25.9), got %d", age2)
	}
}

func TestWriteJSONLWithSchemaOrdered(t *testing.T) {
	// Create schema with specific field order
	schema := NewSchema()
	schema.AddField("z", TypeString)
	schema.AddField("a", TypeString)
	schema.AddField("m", TypeString)

	// Create record (fields might be in different order internally)
	record := ssql.MakeMutableRecord().
		String("a", "first").
		String("m", "middle").
		String("z", "last").
		Freeze()

	var buf bytes.Buffer
	recordIter := func(yield func(ssql.Record) bool) {
		yield(record)
	}

	err := WriteJSONLWithSchemaOrdered(&buf, schema, recordIter)
	if err != nil {
		t.Fatalf("WriteJSONLWithSchemaOrdered error: %v", err)
	}

	lines := bytes.Split(buf.Bytes(), []byte("\n"))

	// First line should be schema
	if !bytes.Contains(lines[0], []byte("_schema")) {
		t.Errorf("first line should be schema: %s", lines[0])
	}

	// Second line should have fields in z, a, m order
	// The JSON should start with "z" key
	dataLine := string(lines[1])
	zIdx := bytes.Index(lines[1], []byte(`"z"`))
	aIdx := bytes.Index(lines[1], []byte(`"a"`))
	mIdx := bytes.Index(lines[1], []byte(`"m"`))

	if zIdx == -1 || aIdx == -1 || mIdx == -1 {
		t.Fatalf("missing fields in output: %s", dataLine)
	}

	if !(zIdx < aIdx && aIdx < mIdx) {
		t.Errorf("fields not in schema order (z, a, m). Got indices z=%d, a=%d, m=%d in: %s",
			zIdx, aIdx, mIdx, dataLine)
	}
}

func TestReadJSONLWithSchemaEmpty(t *testing.T) {
	// Empty input
	result := ReadJSONLWithSchema(bytes.NewBufferString(""))

	if result.Schema != nil {
		t.Error("expected nil schema for empty input")
	}

	var count int
	for range result.Records {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 records, got %d", count)
	}
}

func TestWriteJSONLWithSchemaNilSchema(t *testing.T) {
	// Writing with nil schema should not write schema header
	record := ssql.MakeMutableRecord().
		String("name", "Alice").
		Freeze()

	var buf bytes.Buffer
	recordIter := func(yield func(ssql.Record) bool) {
		yield(record)
	}

	err := WriteJSONLWithSchema(&buf, nil, recordIter)
	if err != nil {
		t.Fatalf("WriteJSONLWithSchema error: %v", err)
	}

	output := buf.String()
	if bytes.Contains(buf.Bytes(), []byte("_schema")) {
		t.Errorf("nil schema should not write schema header: %s", output)
	}

	// Should still have the data record
	if !bytes.Contains(buf.Bytes(), []byte("Alice")) {
		t.Errorf("should contain data: %s", output)
	}
}
