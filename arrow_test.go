package ssql

import (
	"bytes"
	"os"
	"slices"
	"testing"
)

func TestArrowRoundTrip(t *testing.T) {
	// Create test records
	records := []Record{
		MakeMutableRecord().
			String("name", "Alice").
			Int("age", int64(30)).
			Float("salary", 75000.50).
			Bool("active", true).
			Freeze(),
		MakeMutableRecord().
			String("name", "Bob").
			Int("age", int64(25)).
			Float("salary", 65000.00).
			Bool("active", false).
			Freeze(),
		MakeMutableRecord().
			String("name", "Carol").
			Int("age", int64(35)).
			Float("salary", 85000.75).
			Bool("active", true).
			Freeze(),
	}

	// Write to Arrow file
	tmpFile := "/tmp/test_arrow_roundtrip.arrow"
	defer os.Remove(tmpFile)

	err := WriteArrow(slices.Values(records), tmpFile)
	if err != nil {
		t.Fatalf("WriteArrow failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
		t.Fatal("Arrow file was not created")
	}

	// Read back from Arrow file
	readRecords, err := ReadArrow(tmpFile)
	if err != nil {
		t.Fatalf("ReadArrow failed: %v", err)
	}

	// Collect results
	var results []Record
	for r := range readRecords {
		results = append(results, r)
	}

	// Verify count
	if len(results) != 3 {
		t.Errorf("Expected 3 records, got %d", len(results))
	}

	// Verify first record values
	if len(results) > 0 {
		name := GetOr(results[0], "name", "")
		if name != "Alice" {
			t.Errorf("Expected name 'Alice', got %q", name)
		}

		age := GetOr(results[0], "age", int64(0))
		if age != int64(30) {
			t.Errorf("Expected age 30, got %d", age)
		}

		salary := GetOr(results[0], "salary", float64(0))
		if salary != 75000.50 {
			t.Errorf("Expected salary 75000.50, got %f", salary)
		}

		active := GetOr(results[0], "active", false)
		if !active {
			t.Error("Expected active true, got false")
		}
	}
}

func TestArrowWriteToWriter(t *testing.T) {
	records := []Record{
		MakeMutableRecord().
			String("city", "NYC").
			Int("population", int64(8000000)).
			Freeze(),
		MakeMutableRecord().
			String("city", "LA").
			Int("population", int64(4000000)).
			Freeze(),
	}

	var buf bytes.Buffer
	err := WriteArrowToWriter(slices.Values(records), &buf)
	if err != nil {
		t.Fatalf("WriteArrowToWriter failed: %v", err)
	}

	// Verify something was written
	if buf.Len() == 0 {
		t.Error("No data written to buffer")
	}

	// Arrow IPC files start with "ARROW1" magic bytes
	data := buf.Bytes()
	if len(data) < 6 {
		t.Fatalf("Buffer too small: %d bytes", len(data))
	}
	magic := string(data[:6])
	if magic != "ARROW1" {
		t.Errorf("Expected ARROW1 magic, got %q", magic)
	}
}

func TestArrowEmptyRecords(t *testing.T) {
	// Test with no records
	var records []Record

	var buf bytes.Buffer
	err := WriteArrowToWriter(slices.Values(records), &buf)
	if err != nil {
		t.Fatalf("WriteArrowToWriter with empty records failed: %v", err)
	}

	// Empty records should produce no output (nothing to write)
	if buf.Len() != 0 {
		t.Errorf("Expected empty output for empty records, got %d bytes", buf.Len())
	}
}

func TestArrowReadNonExistentFile(t *testing.T) {
	_, err := ReadArrow("/tmp/nonexistent_arrow_file_12345.arrow")
	if err == nil {
		t.Error("Expected error for non-existent file, got nil")
	}
}

func TestArrowTypeConversions(t *testing.T) {
	// Test various numeric types are properly converted
	records := []Record{
		MakeMutableRecord().
			Int("int_val", int64(42)).
			Float("float_val", 3.14159).
			String("str_val", "hello").
			Bool("bool_val", true).
			Freeze(),
	}

	tmpFile := "/tmp/test_arrow_types.arrow"
	defer os.Remove(tmpFile)

	err := WriteArrow(slices.Values(records), tmpFile)
	if err != nil {
		t.Fatalf("WriteArrow failed: %v", err)
	}

	readRecords, err := ReadArrow(tmpFile)
	if err != nil {
		t.Fatalf("ReadArrow failed: %v", err)
	}

	var results []Record
	for r := range readRecords {
		results = append(results, r)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(results))
	}

	r := results[0]

	// Check int64 round-trip
	intVal := GetOr(r, "int_val", int64(0))
	if intVal != int64(42) {
		t.Errorf("int_val: expected 42, got %d", intVal)
	}

	// Check float64 round-trip
	floatVal := GetOr(r, "float_val", float64(0))
	if floatVal != 3.14159 {
		t.Errorf("float_val: expected 3.14159, got %f", floatVal)
	}

	// Check string round-trip
	strVal := GetOr(r, "str_val", "")
	if strVal != "hello" {
		t.Errorf("str_val: expected 'hello', got %q", strVal)
	}

	// Check bool round-trip
	boolVal := GetOr(r, "bool_val", false)
	if !boolVal {
		t.Error("bool_val: expected true, got false")
	}
}

func TestArrowNullValues(t *testing.T) {
	// Test that records with missing fields are handled correctly
	records := []Record{
		MakeMutableRecord().
			String("name", "Alice").
			Int("age", int64(30)).
			Freeze(),
		MakeMutableRecord().
			String("name", "Bob").
			// age is missing
			Freeze(),
	}

	tmpFile := "/tmp/test_arrow_nulls.arrow"
	defer os.Remove(tmpFile)

	err := WriteArrow(slices.Values(records), tmpFile)
	if err != nil {
		t.Fatalf("WriteArrow failed: %v", err)
	}

	readRecords, err := ReadArrow(tmpFile)
	if err != nil {
		t.Fatalf("ReadArrow failed: %v", err)
	}

	var results []Record
	for r := range readRecords {
		results = append(results, r)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(results))
	}

	// First record should have age
	age1 := GetOr(results[0], "age", int64(-1))
	if age1 != int64(30) {
		t.Errorf("First record age: expected 30, got %d", age1)
	}

	// Second record should have default (null) for age
	age2 := GetOr(results[1], "age", int64(-1))
	if age2 != int64(-1) {
		t.Errorf("Second record age: expected -1 (default), got %d", age2)
	}
}

func TestArrowInternalFieldsSkipped(t *testing.T) {
	// Internal fields (starting with _) should not be written to Arrow
	records := []Record{
		MakeMutableRecord().
			String("name", "Alice").
			Int("_internal", int64(1)). // Internal field (underscore prefix)
			Freeze(),
	}

	tmpFile := "/tmp/test_arrow_internal.arrow"
	defer os.Remove(tmpFile)

	err := WriteArrow(slices.Values(records), tmpFile)
	if err != nil {
		t.Fatalf("WriteArrow failed: %v", err)
	}

	readRecords, err := ReadArrow(tmpFile)
	if err != nil {
		t.Fatalf("ReadArrow failed: %v", err)
	}

	var results []Record
	for r := range readRecords {
		results = append(results, r)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(results))
	}

	// Should have name but not the internal field
	name := GetOr(results[0], "name", "")
	if name != "Alice" {
		t.Errorf("Expected name 'Alice', got %q", name)
	}

	// Internal field should not be present
	if _, exists := Get[int64](results[0], "_internal"); exists {
		t.Error("Internal field _internal should not be written to Arrow")
	}
}

func TestExtractSignalFromArrow(t *testing.T) {
	// Create test records with signal data
	records := []Record{
		MakeMutableRecord().Float("amplitude", 1.0).Int("index", int64(0)).Freeze(),
		MakeMutableRecord().Float("amplitude", 2.5).Int("index", int64(1)).Freeze(),
		MakeMutableRecord().Float("amplitude", -0.5).Int("index", int64(2)).Freeze(),
		MakeMutableRecord().Float("amplitude", 3.0).Int("index", int64(3)).Freeze(),
		MakeMutableRecord().Float("amplitude", 0.0).Int("index", int64(4)).Freeze(),
	}

	// Write to Arrow format in memory
	var buf bytes.Buffer
	err := WriteArrowToWriter(slices.Values(records), &buf)
	if err != nil {
		t.Fatalf("WriteArrowToWriter failed: %v", err)
	}

	// Write to temp file for testing file-based extraction
	tmpFile, err := os.CreateTemp("", "signal_test_*.arrow")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(buf.Bytes()); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	tmpFile.Close()

	// Test ExtractSignalFromArrow
	signal, err := ExtractSignalFromArrow(tmpFile.Name(), "amplitude")
	if err != nil {
		t.Fatalf("ExtractSignalFromArrow failed: %v", err)
	}

	expected := Signal{1.0, 2.5, -0.5, 3.0, 0.0}
	if len(signal) != len(expected) {
		t.Fatalf("Expected %d samples, got %d", len(expected), len(signal))
	}

	for i, v := range expected {
		if signal[i] != v {
			t.Errorf("Sample %d: expected %v, got %v", i, v, signal[i])
		}
	}

	// Test extracting integer field (should convert to float64)
	indexSignal, err := ExtractSignalFromArrow(tmpFile.Name(), "index")
	if err != nil {
		t.Fatalf("ExtractSignalFromArrow for index failed: %v", err)
	}

	expectedIndex := Signal{0.0, 1.0, 2.0, 3.0, 4.0}
	for i, v := range expectedIndex {
		if indexSignal[i] != v {
			t.Errorf("Index sample %d: expected %v, got %v", i, v, indexSignal[i])
		}
	}

	// Test non-existent field
	_, err = ExtractSignalFromArrow(tmpFile.Name(), "nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent field")
	}
}
