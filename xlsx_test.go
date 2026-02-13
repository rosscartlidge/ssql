package ssql

import (
	"os"
	"testing"

	"github.com/xuri/excelize/v2"
)

// createTestXLSX creates a temporary XLSX file with the given data for testing.
// Returns the path to the created file.
func createTestXLSX(t *testing.T, sheetName string, headers []string, rows [][]any) string {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()

	// Rename default sheet
	defaultSheet := f.GetSheetName(0)
	if defaultSheet != sheetName {
		f.NewSheet(sheetName)
		f.DeleteSheet(defaultSheet)
	}

	// Write headers
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheetName, cell, h)
	}

	// Write data rows
	for rowIdx, row := range rows {
		for colIdx, val := range row {
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, rowIdx+2)
			f.SetCellValue(sheetName, cell, val)
		}
	}

	path := t.TempDir() + "/test.xlsx"
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("Failed to create test XLSX: %v", err)
	}
	return path
}

func TestReadXLSX(t *testing.T) {
	path := createTestXLSX(t, "Sheet1",
		[]string{"name", "age", "city"},
		[][]any{
			{"Alice", 30, "NYC"},
			{"Bob", 25, "LA"},
			{"Charlie", 35, "Chicago"},
		},
	)

	records, err := ReadXLSX(path)
	if err != nil {
		t.Fatalf("ReadXLSX error: %v", err)
	}

	var count int
	for r := range records {
		name := GetOr(r, "name", "")
		age := GetOr(r, "age", int64(0))
		city := GetOr(r, "city", "")

		switch count {
		case 0:
			if name != "Alice" || age != 30 || city != "NYC" {
				t.Errorf("Row 0: got name=%q age=%d city=%q", name, age, city)
			}
		case 1:
			if name != "Bob" || age != 25 || city != "LA" {
				t.Errorf("Row 1: got name=%q age=%d city=%q", name, age, city)
			}
		case 2:
			if name != "Charlie" || age != 35 || city != "Chicago" {
				t.Errorf("Row 2: got name=%q age=%d city=%q", name, age, city)
			}
		}
		count++
	}

	if count != 3 {
		t.Errorf("Expected 3 records, got %d", count)
	}
}

func TestReadXLSXSpecificSheet(t *testing.T) {
	// Create workbook with two sheets
	f := excelize.NewFile()
	defer f.Close()

	// Sheet1 has different data
	f.SetCellValue("Sheet1", "A1", "x")
	f.SetCellValue("Sheet1", "A2", "1")

	// Create Sales sheet
	f.NewSheet("Sales")
	f.SetCellValue("Sales", "A1", "product")
	f.SetCellValue("Sales", "B1", "revenue")
	f.SetCellValue("Sales", "A2", "Widget")
	f.SetCellValue("Sales", "B2", 100.50)

	path := t.TempDir() + "/multi.xlsx"
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("Failed to create test XLSX: %v", err)
	}

	// Read the Sales sheet
	records, err := ReadXLSX(path, XLSXConfig{SheetName: "Sales"})
	if err != nil {
		t.Fatalf("ReadXLSX error: %v", err)
	}

	var count int
	for r := range records {
		product := GetOr(r, "product", "")
		revenue := GetOr(r, "revenue", float64(0))

		if product != "Widget" || revenue != 100.50 {
			t.Errorf("Got product=%q revenue=%v", product, revenue)
		}
		count++
	}

	if count != 1 {
		t.Errorf("Expected 1 record, got %d", count)
	}
}

func TestReadXLSXTypeInference(t *testing.T) {
	path := createTestXLSX(t, "Sheet1",
		[]string{"name", "count", "price", "active"},
		[][]any{
			{"Alice", 42, 99.99, true},
			{"Bob", 7, 0.50, false},
		},
	)

	records, err := ReadXLSX(path)
	if err != nil {
		t.Fatalf("ReadXLSX error: %v", err)
	}

	var count int
	for r := range records {
		switch count {
		case 0:
			name := GetOr(r, "name", "")
			cnt := GetOr(r, "count", int64(0))
			price := GetOr(r, "price", float64(0))
			active := GetOr(r, "active", "")

			if name != "Alice" {
				t.Errorf("Expected name=Alice, got %q", name)
			}
			if cnt != int64(42) {
				t.Errorf("Expected count=42, got %v (type %T)", cnt, cnt)
			}
			if price != 99.99 {
				t.Errorf("Expected price=99.99, got %v", price)
			}
			// excelize renders bools as TRUE/FALSE strings in GetRows
			if active != "TRUE" && active != "true" && active != "1" {
				// Check if it parsed as bool
				if b, ok := Get[bool](r, "active"); ok {
					if !b {
						t.Errorf("Expected active=true, got false")
					}
				} else {
					t.Logf("active field value: %v (type %T)", active, active)
				}
			}
		}
		count++
	}

	if count != 2 {
		t.Errorf("Expected 2 records, got %d", count)
	}
}

func TestWriteXLSX(t *testing.T) {
	r1 := MakeMutableRecord().
		String("name", "Alice").
		Int("age", 30).
		Float("salary", 95000.50).
		Freeze()

	r2 := MakeMutableRecord().
		String("name", "Bob").
		Int("age", 25).
		Float("salary", 75000.00).
		Freeze()

	records := func(yield func(Record) bool) {
		if !yield(r1) {
			return
		}
		yield(r2)
	}

	path := t.TempDir() + "/output.xlsx"
	if err := WriteXLSX(records, path); err != nil {
		t.Fatalf("WriteXLSX error: %v", err)
	}

	// Verify the file exists and can be read
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Output file not created: %v", err)
	}

	// Read it back
	readBack, err := ReadXLSX(path)
	if err != nil {
		t.Fatalf("ReadXLSX error reading back: %v", err)
	}

	var count int
	for r := range readBack {
		name := GetOr(r, "name", "")
		age := GetOr(r, "age", int64(0))

		switch count {
		case 0:
			if name != "Alice" || age != 30 {
				t.Errorf("Row 0: got name=%q age=%d", name, age)
			}
		case 1:
			if name != "Bob" || age != 25 {
				t.Errorf("Row 1: got name=%q age=%d", name, age)
			}
		}
		count++
	}

	if count != 2 {
		t.Errorf("Expected 2 records, got %d", count)
	}
}

func TestWriteXLSXCustomSheet(t *testing.T) {
	r1 := MakeMutableRecord().String("item", "Widget").Int("qty", 10).Freeze()

	records := func(yield func(Record) bool) {
		yield(r1)
	}

	path := t.TempDir() + "/custom.xlsx"
	if err := WriteXLSX(records, path, XLSXConfig{SheetName: "Inventory"}); err != nil {
		t.Fatalf("WriteXLSX error: %v", err)
	}

	// Read back from the specific sheet
	readBack, err := ReadXLSX(path, XLSXConfig{SheetName: "Inventory"})
	if err != nil {
		t.Fatalf("ReadXLSX error: %v", err)
	}

	var count int
	for r := range readBack {
		item := GetOr(r, "item", "")
		qty := GetOr(r, "qty", int64(0))
		if item != "Widget" || qty != 10 {
			t.Errorf("Got item=%q qty=%d", item, qty)
		}
		count++
	}

	if count != 1 {
		t.Errorf("Expected 1 record, got %d", count)
	}
}

func TestReadXLSXSheetNames(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()

	f.NewSheet("Sales")
	f.NewSheet("Inventory")

	path := t.TempDir() + "/multi.xlsx"
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("Failed to create test XLSX: %v", err)
	}

	names, err := ReadXLSXSheetNames(path)
	if err != nil {
		t.Fatalf("ReadXLSXSheetNames error: %v", err)
	}

	if len(names) < 2 {
		t.Fatalf("Expected at least 2 sheets, got %d: %v", len(names), names)
	}

	// Should contain our custom sheets
	hasSheets := make(map[string]bool)
	for _, n := range names {
		hasSheets[n] = true
	}
	if !hasSheets["Sales"] || !hasSheets["Inventory"] {
		t.Errorf("Expected Sales and Inventory sheets, got: %v", names)
	}
}

func TestWriteXLSXEmpty(t *testing.T) {
	records := func(yield func(Record) bool) {}

	path := t.TempDir() + "/empty.xlsx"
	if err := WriteXLSX(records, path); err != nil {
		t.Fatalf("WriteXLSX error on empty records: %v", err)
	}

	// File should exist
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Output file not created: %v", err)
	}
}

func TestReadXLSXNonExistentFile(t *testing.T) {
	_, err := ReadXLSX("/tmp/does_not_exist.xlsx")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestReadXLSXNonExistentSheet(t *testing.T) {
	path := createTestXLSX(t, "Sheet1",
		[]string{"x"},
		[][]any{{"1"}},
	)

	_, err := ReadXLSX(path, XLSXConfig{SheetName: "DoesNotExist"})
	if err == nil {
		t.Error("Expected error for non-existent sheet")
	}
}
