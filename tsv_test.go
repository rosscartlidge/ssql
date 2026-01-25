package ssql

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestDetectTSVSeparator(t *testing.T) {
	tests := []struct {
		header string
		want   rune
	}{
		{"name\tage\tsalary", '\t'},
		{"name|age|salary", '|'},
		{"name;age;salary", ';'},
		{"name,age,salary", ','},
		{"name age salary", ' '},
		{"field1\tfield2", '\t'},
		{"_private\t_other", '\t'},
		{"a1\tb2\tc3", '\t'},
		// No separator found - default to tab
		{"singlefield", '\t'},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			got := DetectTSVSeparator(tt.header)
			if got != tt.want {
				t.Errorf("DetectTSVSeparator(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestIsIdentifierChar(t *testing.T) {
	tests := []struct {
		r       rune
		isFirst bool
		want    bool
	}{
		{'a', true, true},
		{'z', true, true},
		{'A', true, true},
		{'Z', true, true},
		{'_', true, true},
		{'0', true, false},  // digit not allowed first
		{'9', true, false},  // digit not allowed first
		{'0', false, true},  // digit allowed after first
		{'9', false, true},  // digit allowed after first
		{'\t', true, false},
		{' ', true, false},
		{'|', true, false},
		{';', true, false},
		{'-', true, false},
		{'.', true, false},
	}

	for _, tt := range tests {
		got := isIdentifierChar(tt.r, tt.isFirst)
		if got != tt.want {
			t.Errorf("isIdentifierChar(%q, %v) = %v, want %v", tt.r, tt.isFirst, got, tt.want)
		}
	}
}

func TestReadTSVFromReader(t *testing.T) {
	input := "name\tage\tsalary\nAlice\t30\t50000.50\nBob\t25\t45000\n"
	reader := strings.NewReader(input)

	records := slices.Collect(ReadTSVFromReader(reader))

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Check first record
	name := GetOr(records[0], "name", "")
	if name != "Alice" {
		t.Errorf("expected name=Alice, got %v", name)
	}
	age := GetOr(records[0], "age", int64(0))
	if age != int64(30) {
		t.Errorf("expected age=30, got %v", age)
	}
	salary := GetOr(records[0], "salary", float64(0))
	if salary != 50000.50 {
		t.Errorf("expected salary=50000.50, got %v", salary)
	}

	// Check second record
	name2 := GetOr(records[1], "name", "")
	if name2 != "Bob" {
		t.Errorf("expected name=Bob, got %v", name2)
	}
}

func TestReadTSVWithPipeSeparator(t *testing.T) {
	input := "name|age|active\nAlice|30|true\nBob|25|false\n"
	reader := strings.NewReader(input)

	records := slices.Collect(ReadTSVFromReader(reader))

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	active := GetOr(records[0], "active", false)
	if active != true {
		t.Errorf("expected active=true, got %v", active)
	}

	active2 := GetOr(records[1], "active", true)
	if active2 != false {
		t.Errorf("expected active=false, got %v", active2)
	}
}

func TestWriteTSVToWriter(t *testing.T) {
	records := []Record{
		MakeMutableRecord().
			String("name", "Alice").
			Int("age", 30).
			Float("salary", 50000.5).
			Freeze(),
		MakeMutableRecord().
			String("name", "Bob").
			Int("age", 25).
			Float("salary", 45000).
			Freeze(),
	}

	var buf bytes.Buffer
	err := WriteTSVToWriter(slices.Values(records), &buf)
	if err != nil {
		t.Fatalf("WriteTSVToWriter failed: %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 data), got %d", len(lines))
	}

	// Header should contain all field names separated by tabs
	header := lines[0]
	if !strings.Contains(header, "\t") {
		t.Errorf("header should use tab separator: %q", header)
	}
}

func TestWriteTSVWithCustomSeparator(t *testing.T) {
	records := []Record{
		MakeMutableRecord().
			String("name", "Alice").
			Int("age", 30).
			Freeze(),
	}

	var buf bytes.Buffer
	err := WriteTSVToWriterWithSeparator(slices.Values(records), &buf, '|')
	if err != nil {
		t.Fatalf("WriteTSVToWriterWithSeparator failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "|") {
		t.Errorf("output should use pipe separator: %q", output)
	}
	if strings.Contains(output, "\t") {
		t.Errorf("output should not contain tabs: %q", output)
	}
}

func TestTSVRoundTrip(t *testing.T) {
	original := []Record{
		MakeMutableRecord().
			String("name", "Alice").
			Int("count", 42).
			Float("rate", 3.14).
			Bool("active", true).
			Freeze(),
		MakeMutableRecord().
			String("name", "Bob").
			Int("count", 17).
			Float("rate", 2.71).
			Bool("active", false).
			Freeze(),
	}

	// Write to buffer
	var buf bytes.Buffer
	err := WriteTSVToWriter(slices.Values(original), &buf)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Read back
	reader := strings.NewReader(buf.String())
	roundTrip := slices.Collect(ReadTSVFromReader(reader))

	if len(roundTrip) != len(original) {
		t.Fatalf("expected %d records, got %d", len(original), len(roundTrip))
	}

	// Verify values
	for i := range original {
		origName := GetOr(original[i], "name", "")
		rtName := GetOr(roundTrip[i], "name", "")
		if origName != rtName {
			t.Errorf("record %d: name mismatch: %v != %v", i, origName, rtName)
		}

		origCount := GetOr(original[i], "count", int64(0))
		rtCount := GetOr(roundTrip[i], "count", int64(0))
		if origCount != rtCount {
			t.Errorf("record %d: count mismatch: %v != %v", i, origCount, rtCount)
		}

		origActive := GetOr(original[i], "active", false)
		rtActive := GetOr(roundTrip[i], "active", false)
		if origActive != rtActive {
			t.Errorf("record %d: active mismatch: %v != %v", i, origActive, rtActive)
		}
	}
}
