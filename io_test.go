package ssql

import (
	"bytes"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ============================================================================
// CSV TESTS
// ============================================================================

func TestDefaultCSVConfig(t *testing.T) {
	config := DefaultCSVConfig()

	if config.Delimiter != ',' {
		t.Errorf("Default delimiter should be ',', got %c", config.Delimiter)
	}
	if config.Comment != '#' {
		t.Errorf("Default comment should be '#', got %c", config.Comment)
	}
	if !config.HasHeaders {
		t.Error("Default HasHeaders should be true")
	}
}

func TestReadCSVFromReader(t *testing.T) {
	csvData := `name,age,city
Alice,30,NYC
Bob,25,LA
Charlie,35,SF`

	reader := strings.NewReader(csvData)
	seq := ReadCSVFromReader(reader)
	result := slices.Collect(seq)

	if len(result) != 3 {
		t.Fatalf("Expected 3 records, got %d", len(result))
	}

	if GetOr(result[0], "name", "") != "Alice" {
		t.Errorf("First record name should be Alice, got %v", GetOr(result[0], "name", ""))
	}
	// CSV parsing converts numbers automatically
	if GetOr(result[1], "age", int64(0)) != int64(25) {
		t.Errorf("Second record age should be 25 (int64), got %v (type %T)", GetOr(result[1], "age", int64(0)), GetOr(result[1], "age", int64(0)))
	}
}

func TestReadCSVFromReaderWithCustomDelimiter(t *testing.T) {
	csvData := `name|age|city
Alice|30|NYC
Bob|25|LA`

	config := DefaultCSVConfig()
	config.Delimiter = '|'

	reader := strings.NewReader(csvData)
	seq := ReadCSVFromReader(reader, config)
	result := slices.Collect(seq)

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}

	if GetOr(result[0], "name", "") != "Alice" {
		t.Errorf("Name should be Alice, got %v", GetOr(result[0], "name", ""))
	}
}

func TestReadCSVSafeFromReader(t *testing.T) {
	csvData := `name,age
Alice,30
Bob,25`

	reader := strings.NewReader(csvData)
	seq := ReadCSVSafeFromReader(reader)

	var result []Record
	for record, err := range seq {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, record)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}
}

func TestWriteCSVToWriter(t *testing.T) {
	r1 := MakeMutableRecord()
	r1.fields["name"] = "Alice"
	r1.fields["age"] = "30"

	r2 := MakeMutableRecord()
	r2.fields["name"] = "Bob"
	r2.fields["age"] = "25"

	records := slices.Values([]Record{r1.Freeze(), r2.Freeze()})

	var buf bytes.Buffer
	err := WriteCSVToWriter(records, &buf)
	if err != nil {
		t.Fatalf("WriteCSVToWriter failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Alice") {
		t.Error("Output should contain Alice")
	}
	if !strings.Contains(output, "Bob") {
		t.Error("Output should contain Bob")
	}
}

func TestReadWriteCSV(t *testing.T) {
	// Create temporary file
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.csv")

	// Write CSV
	r1 := MakeMutableRecord()
	r1.fields["name"] = "Alice"
	r1.fields["age"] = int64(30)
	r1.fields["city"] = "NYC"

	r2 := MakeMutableRecord()
	r2.fields["name"] = "Bob"
	r2.fields["age"] = int64(25)
	r2.fields["city"] = "LA"

	records := slices.Values([]Record{r1.Freeze(), r2.Freeze()})

	err := WriteCSV(records, filename)
	if err != nil {
		t.Fatalf("WriteCSV failed: %v", err)
	}

	// Read it back
	seq, err := ReadCSV(filename)
	if err != nil {
		t.Fatalf("ReadCSV failed: %v", err)
	}
	result := slices.Collect(seq)

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}

	if GetOr(result[0], "name", "") != "Alice" {
		t.Errorf("Name should be Alice, got %v", GetOr(result[0], "name", ""))
	}
}

func TestReadCSVSafe(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.csv")

	// Write test data
	data := []byte("name,age\nAlice,30\nBob,25\n")
	err := os.WriteFile(filename, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Read with safe version
	seq := ReadCSVSafe(filename)
	var result []Record
	for record, err := range seq {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, record)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}
}

// ============================================================================
// JSON TESTS
// ============================================================================

func TestReadJSONFromReader(t *testing.T) {
	jsonData := `{"name":"Alice","age":30}
{"name":"Bob","age":25}
{"name":"Charlie","age":35}`

	reader := strings.NewReader(jsonData)
	seq := ReadJSONFromReader(reader)
	result := slices.Collect(seq)

	if len(result) != 3 {
		t.Fatalf("Expected 3 records, got %d", len(result))
	}

	if GetOr(result[0], "name", "") != "Alice" {
		t.Errorf("First record name should be Alice, got %v", GetOr(result[0], "name", ""))
	}
}

func TestReadJSONSafeFromReader(t *testing.T) {
	jsonData := `{"name":"Alice","age":30}
{"name":"Bob","age":25}`

	reader := strings.NewReader(jsonData)
	seq := ReadJSONSafeFromReader(reader)

	var result []Record
	for record, err := range seq {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, record)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}
}

func TestWriteJSONToWriter(t *testing.T) {
	r1 := MakeMutableRecord()
	r1.fields["name"] = "Alice"
	r1.fields["age"] = float64(30)

	r2 := MakeMutableRecord()
	r2.fields["name"] = "Bob"
	r2.fields["age"] = float64(25)

	records := slices.Values([]Record{r1.Freeze(), r2.Freeze()})

	var buf bytes.Buffer
	err := WriteJSONToWriter(records, &buf)
	if err != nil {
		t.Fatalf("WriteJSONToWriter failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Alice") {
		t.Error("Output should contain Alice")
	}
	if !strings.Contains(output, "Bob") {
		t.Error("Output should contain Bob")
	}
}

func TestReadWriteJSON(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.json")

	// Write JSON
	r1 := MakeMutableRecord()
	r1.fields["name"] = "Alice"
	r1.fields["age"] = float64(30)

	r2 := MakeMutableRecord()
	r2.fields["name"] = "Bob"
	r2.fields["age"] = float64(25)

	records := slices.Values([]Record{r1.Freeze(), r2.Freeze()})

	err := WriteJSON(records, filename)
	if err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	// Read it back
	seq, err := ReadJSON(filename)
	if err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}
	result := slices.Collect(seq)

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}

	if GetOr(result[0], "name", "") != "Alice" {
		t.Errorf("Name should be Alice, got %v", GetOr(result[0], "name", ""))
	}
}

func TestReadJSONSafe(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.json")

	// Write test data
	data := []byte(`{"name":"Alice","age":30}
{"name":"Bob","age":25}
`)
	err := os.WriteFile(filename, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Read with safe version
	seq := ReadJSONSafe(filename)
	var result []Record
	for record, err := range seq {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, record)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}
}

// ============================================================================
// LINES TESTS
// ============================================================================

func TestReadLines(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.txt")

	// Write test data
	data := []byte("line1\nline2\nline3\n")
	err := os.WriteFile(filename, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Read lines
	seq, err := ReadLines(filename)
	if err != nil {
		t.Fatalf("ReadLines failed: %v", err)
	}
	result := slices.Collect(seq)

	if len(result) != 3 {
		t.Fatalf("Expected 3 records, got %d", len(result))
	}

	if GetOr(result[0], "line", "") != "line1" {
		t.Errorf("First line should be 'line1', got %v", GetOr(result[0], "line", ""))
	}
}

func TestReadLinesSafe(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.txt")

	// Write test data
	data := []byte("line1\nline2\n")
	err := os.WriteFile(filename, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Read with safe version
	seq := ReadLinesSafe(filename)
	var result []Record
	for record, err := range seq {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, record)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}
}

func TestWriteLines(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.txt")

	// Write lines
	r1 := MakeMutableRecord()
	r1.fields["line"] = "first line"

	r2 := MakeMutableRecord()
	r2.fields["line"] = "second line"

	records := slices.Values([]Record{r1.Freeze(), r2.Freeze()})

	err := WriteLines(records, filename)
	if err != nil {
		t.Fatalf("WriteLines failed: %v", err)
	}

	// Read it back
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "first line") {
		t.Error("Content should contain 'first line'")
	}
	if !strings.Contains(content, "second line") {
		t.Error("Content should contain 'second line'")
	}
}

// ============================================================================
// COMMAND TESTS
// ============================================================================

func TestDefaultCommandConfig(t *testing.T) {
	config := DefaultCommandConfig()

	if !config.HasHeaders {
		t.Error("Default HasHeaders should be true")
	}
	if !config.TrimSpaces {
		t.Error("Default TrimSpaces should be true")
	}
}

func TestExecCommand(t *testing.T) {
	// Test with echo command - need to disable headers since echo output isn't column-aligned
	config := DefaultCommandConfig()
	config.HasHeaders = false

	seq, err := ExecCommand("echo", []string{"hello world"}, config)
	if err != nil {
		t.Fatalf("ExecCommand failed: %v", err)
	}
	result := slices.Collect(seq)

	// Should have at least one record
	if len(result) == 0 {
		t.Error("ExecCommand should return at least one record")
	}

	// Verify the output contains our text
	if len(result) > 0 {
		rawLine := GetOr(result[0], "_raw_line", "")
		if !strings.Contains(rawLine, "hello world") {
			t.Errorf("Expected output to contain 'hello world', got: %s", rawLine)
		}
	}
}

func TestExecCommandSafe(t *testing.T) {
	// Test with printf to create column-aligned output
	// printf "NAME   AGE\nAlice  30\nBob    25"
	config := DefaultCommandConfig()
	config.HasHeaders = true

	seq := ExecCommandSafe("printf", []string{"NAME   AGE\\nAlice  30\\nBob    25"}, config)

	var result []Record
	var hasError bool
	for record, err := range seq {
		if err != nil {
			hasError = true
			t.Logf("Got error: %v", err)
			continue
		}
		result = append(result, record)
	}

	// We should get 2 data records (Alice and Bob)
	if !hasError && len(result) < 2 {
		t.Errorf("Expected at least 2 records, got %d", len(result))
	}
}

func TestExecCommandWithConfig(t *testing.T) {
	config := DefaultCommandConfig()
	config.TrimSpaces = true
	config.HasHeaders = false // Disable headers for echo output

	seq, err := ExecCommand("echo", []string{"hello"}, config)
	if err != nil {
		t.Fatalf("ExecCommand failed: %v", err)
	}
	result := slices.Collect(seq)

	if len(result) == 0 {
		t.Fatal("Expected at least one record")
	}
}

// ============================================================================
// CHANNEL CONVERSION TESTS
// ============================================================================

func TestToChannel(t *testing.T) {
	input := slices.Values([]int{1, 2, 3, 4, 5})
	ch := ToChannel(input)

	var result []int
	for v := range ch {
		result = append(result, v)
	}

	expected := []int{1, 2, 3, 4, 5}
	if !slices.Equal(result, expected) {
		t.Errorf("ToChannel failed: expected %v, got %v", expected, result)
	}
}

func TestToChannelWithErrors(t *testing.T) {
	input := Safe(slices.Values([]int{1, 2, 3}))
	itemCh, errCh := ToChannelWithErrors(input)

	var result []int
	var errors []error

	done := make(chan bool)
	go func() {
		for err := range errCh {
			errors = append(errors, err)
		}
		done <- true
	}()

	for v := range itemCh {
		result = append(result, v)
	}

	<-done

	expected := []int{1, 2, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("ToChannelWithErrors failed: expected %v, got %v", expected, result)
	}

	if len(errors) != 0 {
		t.Errorf("Expected no errors, got %d", len(errors))
	}
}

func TestFromChannelSafe(t *testing.T) {
	itemCh := make(chan int, 3)
	errCh := make(chan error, 1) // Buffer the error channel

	// Send some values
	go func() {
		itemCh <- 1
		itemCh <- 2
		itemCh <- 3
		close(itemCh)
		close(errCh)
	}()

	seq := FromChannelSafe(itemCh, errCh)
	var result []int
	for v, err := range seq {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
		}
		result = append(result, v)
	}

	expected := []int{1, 2, 3}
	if !slices.Equal(result, expected) {
		t.Errorf("FromChannelSafe failed: expected %v, got %v", expected, result)
	}
}

func TestChannelRoundTrip(t *testing.T) {
	// Test converting to channel and back
	input := slices.Values([]int{1, 2, 3, 4, 5})

	// Convert to channels
	itemCh, errCh := ToChannelWithErrors(Safe(input))

	// Convert back to iter.Seq2
	seq := FromChannelSafe(itemCh, errCh)

	// Collect results
	var result []int
	for v, err := range seq {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		result = append(result, v)
	}

	expected := []int{1, 2, 3, 4, 5}
	if !slices.Equal(result, expected) {
		t.Errorf("Channel round-trip failed: expected %v, got %v", expected, result)
	}
}

// ============================================================================
// INTEGRATION TESTS
// ============================================================================

func TestCSVPipeline(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.csv")
	outputFile := filepath.Join(tmpDir, "output.csv")

	// Write input CSV
	inputData := []byte("name,age,score\nAlice,30,85\nBob,25,90\nCharlie,35,75\n")
	err := os.WriteFile(inputFile, inputData, 0644)
	if err != nil {
		t.Fatalf("Failed to write input file: %v", err)
	}

	// Read, filter, and write
	input, err := ReadCSV(inputFile)
	if err != nil {
		t.Fatalf("ReadCSV failed: %v", err)
	}

	filtered := Where(func(r Record) bool {
		// CSV parses numbers automatically, so age is int64, not string
		ageInt := GetOr(r, "age", int64(0))
		return ageInt != int64(25)
	})(input)

	err = WriteCSV(filtered, outputFile)
	if err != nil {
		t.Fatalf("Failed to write output: %v", err)
	}

	// Read output and verify
	output, err := ReadCSV(outputFile)
	if err != nil {
		t.Fatalf("ReadCSV failed: %v", err)
	}
	result := slices.Collect(output)

	// Should have Alice and Charlie (filtered out Bob who has age 25)
	// CSV parsing converts numbers, so age is int64
	if len(result) != 2 {
		t.Fatalf("Expected 2 records after filtering, got %d", len(result))
	}

	// Verify Bob (age 25) was filtered out
	for _, r := range result {
		age := GetOr(r, "age", int64(0))
		if age == int64(25) {
			name := GetOr(r, "name", "")
			t.Errorf("Record with age 25 (%s) should have been filtered out", name)
		}
	}

	// Verify Alice and Charlie are present
	names := make(map[string]bool)
	for _, r := range result {
		name := GetOr(r, "name", "")
		names[name] = true
	}

	if !names["Alice"] {
		t.Error("Alice should be in the results")
	}
	if !names["Charlie"] {
		t.Error("Charlie should be in the results")
	}
}

func TestJSONPipeline(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.json")

	// Create and write records
	r1 := MakeMutableRecord()
	r1.fields["name"] = "Alice"
	r1.fields["value"] = float64(100)

	r2 := MakeMutableRecord()
	r2.fields["name"] = "Bob"
	r2.fields["value"] = float64(200)

	r3 := MakeMutableRecord()
	r3.fields["name"] = "Charlie"
	r3.fields["value"] = float64(150)

	records := slices.Values([]Record{r1.Freeze(), r2.Freeze(), r3.Freeze()})

	err := WriteJSON(records, filename)
	if err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	// Read and process
	input, err := ReadJSON(filename)
	if err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}
	filtered := Where(func(r Record) bool {
		value, ok := Get[float64](r, "value")
		return ok && value >= 150
	})(input)

	result := slices.Collect(filtered)

	if len(result) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(result))
	}
}

// ============================================================================
// ERROR HANDLING TESTS
// ============================================================================

func TestReadCSVNonExistentFile(t *testing.T) {
	seq := ReadCSVSafe("/nonexistent/file.csv")

	var hasError bool
	for _, err := range seq {
		if err != nil {
			hasError = true
			break
		}
	}

	if !hasError {
		t.Error("ReadCSVSafe should produce error for nonexistent file")
	}
}

func TestReadJSONNonExistentFile(t *testing.T) {
	seq := ReadJSONSafe("/nonexistent/file.json")

	var hasError bool
	for _, err := range seq {
		if err != nil {
			hasError = true
			break
		}
	}

	if !hasError {
		t.Error("ReadJSONSafe should produce error for nonexistent file")
	}
}

func TestReadLinesNonExistentFile(t *testing.T) {
	seq := ReadLinesSafe("/nonexistent/file.txt")

	var hasError bool
	for _, err := range seq {
		if err != nil {
			hasError = true
			break
		}
	}

	if !hasError {
		t.Error("ReadLinesSafe should produce error for nonexistent file")
	}
}

func TestWriteCSVInvalidPath(t *testing.T) {
	r := MakeMutableRecord()
	r.fields["name"] = "Alice"

	records := slices.Values([]Record{r.Freeze()})

	err := WriteCSV(records, "/invalid/path/file.csv")
	if err == nil {
		t.Error("WriteCSV should return error for invalid path")
	}
}

func TestWriteJSONInvalidPath(t *testing.T) {
	r := MakeMutableRecord()
	r.fields["name"] = "Alice"

	records := slices.Values([]Record{r.Freeze()})

	err := WriteJSON(records, "/invalid/path/file.json")
	if err == nil {
		t.Error("WriteJSON should return error for invalid path")
	}
}

// ============================================================================
// EDGE CASE TESTS
// ============================================================================

func TestReadEmptyCSV(t *testing.T) {
	csvData := ``
	reader := strings.NewReader(csvData)
	seq := ReadCSVFromReader(reader)
	result := slices.Collect(seq)

	if len(result) != 0 {
		t.Errorf("Empty CSV should return 0 records, got %d", len(result))
	}
}

func TestReadCSVHeaderOnly(t *testing.T) {
	csvData := `name,age,city`
	reader := strings.NewReader(csvData)
	seq := ReadCSVFromReader(reader)
	result := slices.Collect(seq)

	if len(result) != 0 {
		t.Errorf("CSV with only header should return 0 records, got %d", len(result))
	}
}

// TestCSVTypeParsing tests that CSV parsing correctly identifies types
// Types are inferred from the first row and applied consistently to all rows
func TestCSVTypeParsing(t *testing.T) {
	// Test 1: Integer column stays integer (regression for bug where "1" became bool(true))
	t.Run("integers_not_bools", func(t *testing.T) {
		csvData := `value
1
0
42`
		reader := strings.NewReader(csvData)
		seq := ReadCSVFromReader(reader)
		result := slices.Collect(seq)

		for i, r := range result {
			val, ok := Get[any](r, "value")
			if !ok {
				t.Errorf("Row %d: value field not found", i)
				continue
			}
			if _, ok := val.(int64); !ok {
				t.Errorf("Row %d: value should be int64, got %T(%v)", i, val, val)
			}
		}
		// Verify specific values
		if GetOr(result[0], "value", int64(0)) != int64(1) {
			t.Errorf("Expected 1, got %v", GetOr(result[0], "value", int64(0)))
		}
		if GetOr(result[1], "value", int64(0)) != int64(0) {
			t.Errorf("Expected 0, got %v", GetOr(result[1], "value", int64(0)))
		}
	})

	// Test 2: Boolean column inferred from first row
	t.Run("booleans_consistent", func(t *testing.T) {
		csvData := `active
true
false
yes
no
1
0`
		reader := strings.NewReader(csvData)
		seq := ReadCSVFromReader(reader)
		result := slices.Collect(seq)

		// All values should be parsed as bool since first row is "true"
		expected := []bool{true, false, true, false, true, false}
		for i, r := range result {
			val, ok := Get[any](r, "active")
			if !ok {
				t.Errorf("Row %d: active field not found", i)
				continue
			}
			if _, ok := val.(bool); !ok {
				t.Errorf("Row %d: value should be bool, got %T(%v)", i, val, val)
			}
			if val != expected[i] {
				t.Errorf("Row %d: expected %v, got %v", i, expected[i], val)
			}
		}
	})

	// Test 3: Float column stays float
	t.Run("floats_consistent", func(t *testing.T) {
		csvData := `price
1.5
2.0
3`
		reader := strings.NewReader(csvData)
		seq := ReadCSVFromReader(reader)
		result := slices.Collect(seq)

		for i, r := range result {
			val, ok := Get[any](r, "price")
			if !ok {
				t.Errorf("Row %d: price field not found", i)
				continue
			}
			if _, ok := val.(float64); !ok {
				t.Errorf("Row %d: value should be float64, got %T(%v)", i, val, val)
			}
		}
	})

	// Test 4: String column stays string
	t.Run("strings_consistent", func(t *testing.T) {
		csvData := `name
Alice
Bob
123`
		reader := strings.NewReader(csvData)
		seq := ReadCSVFromReader(reader)
		result := slices.Collect(seq)

		for i, r := range result {
			val, ok := Get[any](r, "name")
			if !ok {
				t.Errorf("Row %d: name field not found", i)
				continue
			}
			if _, ok := val.(string); !ok {
				t.Errorf("Row %d: value should be string, got %T(%v)", i, val, val)
			}
		}
		// Even "123" stays string since first row was string
		if GetOr(result[2], "name", "") != "123" {
			t.Errorf("Expected '123' as string, got %v", GetOr(result[2], "name", ""))
		}
	})

	// Test 5: Type overrides work
	t.Run("type_overrides", func(t *testing.T) {
		csvData := `zipcode,age
01234,30
00123,25`
		reader := strings.NewReader(csvData)
		config := CSVConfig{
			HasHeaders:    true,
			Delimiter:     ',',
			TypeOverrides: map[string]FieldType{"zipcode": FieldTypeString},
		}
		seq := ReadCSVFromReader(reader, config)
		result := slices.Collect(seq)

		// zipcode should be string (preserving leading zeros)
		if GetOr(result[0], "zipcode", "") != "01234" {
			t.Errorf("Expected '01234', got %v", GetOr(result[0], "zipcode", ""))
		}
		if GetOr(result[1], "zipcode", "") != "00123" {
			t.Errorf("Expected '00123', got %v", GetOr(result[1], "zipcode", ""))
		}
		// age should still be auto-detected as int
		if GetOr(result[0], "age", int64(0)) != int64(30) {
			t.Errorf("Expected int64(30), got %T(%v)", GetOr(result[0], "age", int64(0)), GetOr(result[0], "age", int64(0)))
		}
	})

	// Test 6: Default type works
	t.Run("default_type", func(t *testing.T) {
		csvData := `zipcode,age
01234,30`
		reader := strings.NewReader(csvData)
		config := CSVConfig{
			HasHeaders:  true,
			Delimiter:   ',',
			DefaultType: FieldTypeString,
		}
		seq := ReadCSVFromReader(reader, config)
		result := slices.Collect(seq)

		// Both should be strings
		if _, ok := Get[string](result[0], "zipcode"); !ok {
			val, _ := Get[any](result[0], "zipcode")
			t.Errorf("zipcode should be string, got %T", val)
		}
		if _, ok := Get[string](result[0], "age"); !ok {
			val, _ := Get[any](result[0], "age")
			t.Errorf("age should be string, got %T", val)
		}
	})
}

func TestReadEmptyJSON(t *testing.T) {
	jsonData := ``
	reader := strings.NewReader(jsonData)
	seq := ReadJSONFromReader(reader)
	result := slices.Collect(seq)

	if len(result) != 0 {
		t.Errorf("Empty JSON should return 0 records, got %d", len(result))
	}
}

func TestWriteEmptySequence(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "empty.csv")

	empty := func(yield func(Record) bool) {
		// Yield nothing
	}

	err := WriteCSV(iter.Seq[Record](empty), filename)
	if err != nil {
		t.Errorf("WriteCSV should handle empty sequence: %v", err)
	}
}

// ============================================================================
// ADVANCED INTEGRATION TESTS
// ============================================================================

// TestJSONComplexTypesRoundTrip tests JSON round-trip with complex types
// including iter.Seq fields, nested Records, and JSONString fields
func TestJSONComplexTypesRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "complex_roundtrip.json")

	// Create complex records with various types
	tags := slices.Values([]string{"urgent", "security"})
	scores := slices.Values([]int{95, 88, 92})
	weights := slices.Values([]float64{1.5, 2.3, 0.8})

	metadata := MakeMutableRecord().
		String("priority", "high").
		Int("version", 2)

	// Create JSONString field
	configJSON, err := NewJSONString(map[string]any{
		"timeout": 30,
		"retries": 3,
	})
	if err != nil {
		t.Fatalf("Failed to create JSONString: %v", err)
	}

	originalRecords := []Record{
		MakeMutableRecord().
			String("id", "TASK-001").
			String("title", "Security Update").
			Int("priority_num", 1).
			Float("score", 95.5).
			Bool("completed", false).
			StringSeq("tags", tags).
			IntSeq("scores", scores).
			Float64Seq("weights", weights).
			Nested("metadata", metadata.Freeze()).
			JSONString("config", configJSON).
			Freeze(),
		MakeMutableRecord().
			String("id", "TASK-002").
			String("title", "Feature Request").
			Int("priority_num", 2).
			Float("score", 87.2).
			Bool("completed", true).
			Freeze(),
	}

	// Write to JSON
	originalStream := slices.Values(originalRecords)
	err = WriteJSON(originalStream, filename)
	if err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	// Read back from JSON
	reconstructedStream, err := ReadJSON(filename)
	if err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}
	reconstructedRecords := slices.Collect(reconstructedStream)

	if len(reconstructedRecords) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(reconstructedRecords))
	}

	// Verify basic fields are preserved
	if GetOr(reconstructedRecords[0], "id", "") != "TASK-001" {
		t.Errorf("ID should be TASK-001, got %v", GetOr(reconstructedRecords[0], "id", ""))
	}

	if GetOr(reconstructedRecords[0], "title", "") != "Security Update" {
		t.Errorf("Title should be 'Security Update', got %v", GetOr(reconstructedRecords[0], "title", ""))
	}

	// Verify numeric fields (JSON converts to float64)
	scoreValue, ok := Get[float64](reconstructedRecords[0], "score")
	if !ok || scoreValue != 95.5 {
		val, _ := Get[any](reconstructedRecords[0], "score")
		t.Errorf("Score should be 95.5 (float64), got %v (%T)", val, val)
	}

	// Verify boolean field
	completed, ok := Get[bool](reconstructedRecords[0], "completed")
	if !ok || completed != false {
		val, _ := Get[any](reconstructedRecords[0], "completed")
		t.Errorf("Completed should be false (bool), got %v (%T)", val, val)
	}

	// Verify iter.Seq fields become arrays
	tagsValue, ok := Get[[]any](reconstructedRecords[0], "tags")
	if !ok {
		val, _ := Get[any](reconstructedRecords[0], "tags")
		t.Errorf("Tags should be array after round-trip, got %T", val)
	} else {
		if len(tagsValue) != 2 {
			t.Errorf("Tags should have 2 elements, got %d", len(tagsValue))
		}
		if tagsValue[0] != "urgent" || tagsValue[1] != "security" {
			t.Errorf("Tags data not preserved correctly: %v", tagsValue)
		}
	}

	// Verify Record fields become map[string]any
	metadataValue, ok := Get[map[string]any](reconstructedRecords[0], "metadata")
	if !ok {
		val, _ := Get[any](reconstructedRecords[0], "metadata")
		t.Errorf("Metadata should be map after round-trip, got %T", val)
	} else {
		if metadataValue["priority"] != "high" {
			t.Errorf("Metadata priority should be 'high', got %v", metadataValue["priority"])
		}
	}

	// Verify JSONString is parsed (not double-encoded)
	configValue, ok := Get[map[string]any](reconstructedRecords[0], "config")
	if !ok {
		val, _ := Get[any](reconstructedRecords[0], "config")
		t.Errorf("Config should be parsed map, got %T", val)
	} else {
		// JSON converts all numbers to float64
		if timeout, ok := configValue["timeout"].(float64); !ok || timeout != 30 {
			t.Errorf("Config timeout should be 30, got %v (%T)", configValue["timeout"], configValue["timeout"])
		}
	}
}

// TestJSONStreamProcessing tests process chaining via readers/writers
// simulating stdin/stdout processing
func TestJSONStreamProcessing(t *testing.T) {
	// Step 1: Create sample data
	salesData := []Record{
		MakeMutableRecord().
			String("product", "Laptop").
			Float("price", 1999.99).
			Int("quantity", 1).
			String("region", "North").
			Freeze(),
		MakeMutableRecord().
			String("product", "Phone").
			Float("price", 899.99).
			Int("quantity", 2).
			String("region", "South").
			Freeze(),
		MakeMutableRecord().
			String("product", "Tablet").
			Float("price", 399.99).
			Int("quantity", 1).
			String("region", "North").
			Freeze(),
	}

	// Step 2: Write to buffer (simulating first process output)
	var step1Output bytes.Buffer
	err := WriteJSONToWriter(slices.Values(salesData), &step1Output)
	if err != nil {
		t.Fatalf("Step 1 WriteJSONToWriter failed: %v", err)
	}

	// Step 3: Read from buffer and filter (simulating second process)
	step2Input := bytes.NewReader(step1Output.Bytes())
	inputStream := ReadJSONFromReader(step2Input)

	var filteredRecords []Record
	for record := range inputStream {
		price := GetOr(record, "price", float64(0))
		if price >= 500.0 {
			// Add calculated field - copy to MutableRecord, add field, freeze
			quantity, _ := Get[float64](record, "quantity")
			mut := MakeMutableRecord()
			maps.Insert(mut.fields, record.All())
			mut.fields["total_value"] = price * quantity
			filteredRecords = append(filteredRecords, mut.Freeze())
		}
	}

	// Step 4: Write filtered output (simulating third process input)
	var step2Output bytes.Buffer
	err = WriteJSONToWriter(slices.Values(filteredRecords), &step2Output)
	if err != nil {
		t.Fatalf("Step 2 WriteJSONToWriter failed: %v", err)
	}

	// Step 5: Read and verify final output
	step3Input := bytes.NewReader(step2Output.Bytes())
	finalStream := ReadJSONFromReader(step3Input)
	finalRecords := slices.Collect(finalStream)

	// Should have Laptop and Phone (filtered out Tablet with price < 500)
	if len(finalRecords) != 2 {
		t.Fatalf("Expected 2 filtered records, got %d", len(finalRecords))
	}

	// Verify calculated field exists
	if !finalRecords[0].Has("total_value") {
		t.Error("total_value field should be added during filtering")
	}

	// Verify data integrity through pipeline
	foundLaptop := false
	foundPhone := false
	for _, record := range finalRecords {
		product := GetOr(record, "product", "")
		if product == "Laptop" {
			foundLaptop = true
			totalValue := GetOr(record, "total_value", float64(0))
			if totalValue < 1999 { // Should be 1999.99 * 1
				t.Errorf("Laptop total_value incorrect: %v", totalValue)
			}
		}
		if product == "Phone" {
			foundPhone = true
		}
		if product == "Tablet" {
			t.Error("Tablet should have been filtered out")
		}
	}

	if !foundLaptop || !foundPhone {
		t.Error("Expected both Laptop and Phone in results")
	}
}

// TestFunctionalPipelineComposition tests complex functional composition
// with Chain, GroupBy, and Aggregate
func TestFunctionalPipelineComposition(t *testing.T) {
	// Create test data
	sales := []Record{
		MakeMutableRecord().String("region", "North").String("product", "Laptop").Float("amount", 1200).Freeze(),
		MakeMutableRecord().String("region", "South").String("product", "Phone").Float("amount", 800).Freeze(),
		MakeMutableRecord().String("region", "North").String("product", "Phone").Float("amount", 900).Freeze(),
		MakeMutableRecord().String("region", "East").String("product", "Laptop").Float("amount", 1100).Freeze(),
		MakeMutableRecord().String("region", "South").String("product", "Laptop").Float("amount", 1300).Freeze(),
		MakeMutableRecord().String("region", "North").String("product", "Tablet").Float("amount", 400).Freeze(),
	}

	// Test 1: Chain multiple Where filters
	chained := Chain(
		Where(func(r Record) bool {
			amount := GetOr(r, "amount", 0.0)
			return amount >= 800 // Filter high-value sales
		}),
		Where(func(r Record) bool {
			product := GetOr(r, "product", "")
			return product != "Tablet" // Exclude tablets
		}),
	)(slices.Values(sales))

	filteredCount := 0
	var filtered []Record
	for record := range chained {
		filteredCount++
		filtered = append(filtered, record)
	}

	// Should have 5 records (all except Tablet with 400, which is both < 800 and is Tablet)
	if filteredCount != 5 {
		t.Errorf("Expected 5 filtered records, got %d", filteredCount)
	}

	// Test 2: GroupBy and Aggregate composition
	grouped := GroupByFields("sales_data", "region")(slices.Values(filtered))

	aggregated := Aggregate("sales_data", map[string]AggregateFunc{
		"total_revenue": Sum("amount"),
		"avg_amount":    Avg("amount"),
		"count":         Count(),
	})(grouped)

	results := slices.Collect(aggregated)

	// Should have 3 regions (North, South, East)
	if len(results) != 3 {
		t.Errorf("Expected 3 regional summaries, got %d", len(results))
	}

	// Verify aggregation worked correctly
	regionTotals := make(map[string]float64)
	for _, result := range results {
		region := GetOr(result, "region", "")
		total := GetOr(result, "total_revenue", 0.0)
		count := GetOr(result, "count", int64(0))

		regionTotals[region] = total

		// Verify count is reasonable
		if count < 1 {
			t.Errorf("Region %s should have at least 1 sale, got %d", region, count)
		}

		// Verify average was calculated
		if !result.Has("avg_amount") {
			t.Errorf("avg_amount should be present for region %s", region)
		}
	}

	// Test 3: Step-by-step functional composition (same result)
	filtered2 := Where(func(r Record) bool {
		amount := GetOr(r, "amount", 0.0)
		product := GetOr(r, "product", "")
		return amount >= 800 && product != "Tablet"
	})(slices.Values(sales))

	grouped2 := GroupByFields("sales_data", "region")(filtered2)

	aggregated2 := Aggregate("sales_data", map[string]AggregateFunc{
		"total_revenue": Sum("amount"),
	})(grouped2)

	results2 := slices.Collect(aggregated2)

	// Should produce same results as chained version
	if len(results2) != len(results) {
		t.Errorf("Step-by-step composition should produce same number of results: expected %d, got %d", len(results), len(results2))
	}

	// Verify totals match between both approaches
	regionTotals2 := make(map[string]float64)
	for _, result := range results2 {
		region := GetOr(result, "region", "")
		total := GetOr(result, "total_revenue", 0.0)
		regionTotals2[region] = total
	}

	for region, total := range regionTotals {
		if regionTotals2[region] != total {
			t.Errorf("Region %s totals don't match: chained=%v, step-by-step=%v", region, total, regionTotals2[region])
		}
	}
}

// ============================================================================
// STREAMING TABLE TESTS
// ============================================================================

func TestDisplayTableStreamingTo_SmallInput(t *testing.T) {
	// With input smaller than sample size, output should match DisplayTableWithFields
	records := []Record{
		NewRecord(map[string]any{"name": "Alice", "age": int64(30)}),
		NewRecord(map[string]any{"name": "Bob", "age": int64(25)}),
	}

	// Capture streaming output
	var streamBuf bytes.Buffer
	DisplayTableStreamingTo(&streamBuf, slices.Values(records), 50, 100, nil, false)

	// Capture non-streaming output
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	DisplayTableWithFields(slices.Values(records), 50, nil, false)
	w.Close()
	var normalBuf bytes.Buffer
	normalBuf.ReadFrom(r)
	os.Stdout = oldStdout

	if streamBuf.String() != normalBuf.String() {
		t.Errorf("Streaming output differs from normal:\nStreaming:\n%s\nNormal:\n%s", streamBuf.String(), normalBuf.String())
	}
}

func TestDisplayTableStreamingTo_LargerThanSample(t *testing.T) {
	// Create 10 records, sample 3
	var records []Record
	for i := range 10 {
		records = append(records, NewRecord(map[string]any{
			"id":   int64(i),
			"name": "item",
		}))
	}

	var buf bytes.Buffer
	DisplayTableStreamingTo(&buf, slices.Values(records), 50, 3, nil, false)

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// header + separator + 10 data rows = 12 lines
	if len(lines) != 12 {
		t.Errorf("Expected 12 lines, got %d:\n%s", len(lines), output)
	}
}

func TestDisplayTableStreamingTo_Truncation(t *testing.T) {
	// Sample has short values, later record has longer value
	sample := []Record{
		NewRecord(map[string]any{"val": "short"}),
	}
	long := []Record{
		NewRecord(map[string]any{"val": "this-is-a-very-long-value-that-exceeds-width"}),
	}
	all := append(sample, long...)

	var buf bytes.Buffer
	DisplayTableStreamingTo(&buf, slices.Values(all), 50, 1, nil, false)

	output := buf.String()
	// The long value should appear (possibly truncated by maxWidth=50, but column width was inferred from "short")
	if !strings.Contains(output, "this-is-a-very-long") {
		t.Errorf("Long value should appear in output:\n%s", output)
	}
}

func TestDisplayTableStreamingTo_EmptyInput(t *testing.T) {
	var buf bytes.Buffer
	DisplayTableStreamingTo(&buf, slices.Values([]Record{}), 50, 100, nil, false)

	if buf.String() != "" {
		t.Errorf("Empty input should produce no output, got: %q", buf.String())
	}
}

func TestWriteMarkdownTo(t *testing.T) {
	records := []Record{
		NewRecord(map[string]any{"name": "Ann|e", "note": "line1\nline2", "n": int64(3)}),
		NewRecord(map[string]any{"name": "Bob", "note": "plain", "n": int64(12)}),
	}
	var buf bytes.Buffer
	if err := WriteMarkdownTo(&buf, slices.Values(records), []string{"name", "n", "note"}, false); err != nil {
		t.Fatal(err)
	}
	want := "| name | n | note |\n" +
		"|---|---:|---|\n" +
		"| Ann\\|e | 3 | line1<br>line2 |\n" +
		"| Bob | 12 | plain |\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}

	// -only hides unspecified columns.
	buf.Reset()
	if err := WriteMarkdownTo(&buf, slices.Values(records), []string{"name"}, true); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "| name |\n|---|\n| Ann\\|e |\n| Bob |\n" {
		t.Errorf("-only output wrong:\n%s", got)
	}

	// Empty input: no output, no error.
	buf.Reset()
	if err := WriteMarkdownTo(&buf, slices.Values([]Record{}), nil, false); err != nil || buf.Len() != 0 {
		t.Errorf("empty input: err=%v out=%q", err, buf.String())
	}
}
