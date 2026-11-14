// Package xlsx reads and writes Excel workbooks as ssql records.
//
// It is its own package so that a program links excelize only when it
// imports it (DFC168): a CSV pipeline's binary carries no spreadsheet
// code. Cell typing is the CSV reader's, through
// ssql.RecordsFromStringRows.
package xlsx

import (
	"fmt"
	"iter"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/xuri/excelize/v2"
)

// Config configures XLSX reading and writing.
type Config struct {
	SheetName string // Sheet to read/write (default: first sheet for read, "Sheet1" for write)
}

// DefaultConfig provides sensible defaults for XLSX processing.
func DefaultConfig() Config {
	return Config{}
}

// Read reads an XLSX file and returns an iterator of Records.
// Row 1 is treated as headers (field names), remaining rows are data.
// Each column's type is inferred from its leading cells, as the CSV
// reader does; a later cell that does not fit panics with an
// *ssql.CellError.
//
// Example:
//
//	data, err := xlsx.Read("sales.xlsx")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for record := range data {
//	    name := ssql.GetOr(record, "name", "")
//	    amount := ssql.GetOr(record, "amount", float64(0))
//	    fmt.Printf("%s: %.2f\n", name, amount)
//	}
//
//	// Read a specific sheet
//	data, err := xlsx.Read("workbook.xlsx", xlsx.Config{SheetName: "Sales"})
func Read(filename string, config ...Config) (iter.Seq[ssql.Record], error) {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	f, err := excelize.OpenFile(filename)
	if err != nil {
		return nil, fmt.Errorf("opening XLSX file %s: %w", filename, err)
	}
	// GetRows holds the whole sheet in memory, so the file is done with
	// once it returns.
	defer f.Close()

	sheetName := cfg.SheetName
	if sheetName == "" {
		sheetName = f.GetSheetName(0)
		if sheetName == "" {
			return nil, fmt.Errorf("XLSX file %s has no sheets", filename)
		}
	}

	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("reading sheet %q from %s: %w", sheetName, filename, err)
	}
	if len(rows) < 1 || len(rows[0]) == 0 {
		return func(yield func(ssql.Record) bool) {}, nil
	}
	return ssql.RecordsFromStringRows(rows[0], rows[1:]), nil
}

// Write writes records to an XLSX file.
// Field names are used as headers in row 1, data follows in subsequent rows.
// Field order is preserved from the record schema.
//
// Example:
//
//	err := xlsx.Write(records, "output.xlsx")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Write to a specific sheet
//	err := xlsx.Write(records, "output.xlsx", xlsx.Config{SheetName: "Results"})
func Write(records iter.Seq[ssql.Record], filename string, config ...Config) error {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	sheetName := cfg.SheetName
	if sheetName == "" {
		sheetName = "Sheet1"
	}

	f := excelize.NewFile()
	defer f.Close()

	// Rename the default sheet
	defaultSheet := f.GetSheetName(0)
	if defaultSheet != sheetName {
		if _, err := f.NewSheet(sheetName); err != nil {
			return fmt.Errorf("creating sheet %q: %w", sheetName, err)
		}
		if err := f.DeleteSheet(defaultSheet); err != nil {
			return fmt.Errorf("deleting default sheet: %w", err)
		}
	}

	// Collect records to determine fields
	var allRecords []ssql.Record
	var fields []string
	fieldSet := make(map[string]bool)

	for record := range records {
		allRecords = append(allRecords, record)
		if fields == nil {
			// Use first record's schema field order if available
			if schema := record.Schema(); schema != nil {
				for _, field := range schema.Fields() {
					if field[0] != '_' && !fieldSet[field] {
						fields = append(fields, field)
						fieldSet[field] = true
					}
				}
			}
		}
		// Pick up any new fields from later records
		for k := range record.All() {
			if k[0] != '_' && !fieldSet[k] {
				fields = append(fields, k)
				fieldSet[k] = true
			}
		}
	}

	if len(allRecords) == 0 || len(fields) == 0 {
		return f.SaveAs(filename)
	}

	// Write header row
	for i, field := range fields {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(sheetName, cell, field); err != nil {
			return fmt.Errorf("writing header cell: %w", err)
		}
	}

	// Write data rows
	for rowIdx, record := range allRecords {
		for colIdx, field := range fields {
			val, exists := ssql.Get[any](record, field)
			if !exists {
				continue
			}
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, rowIdx+2)
			if err := f.SetCellValue(sheetName, cell, cellValue(val)); err != nil {
				return fmt.Errorf("writing cell: %w", err)
			}
		}
	}

	return f.SaveAs(filename)
}

// cellValue converts a Go value to an appropriate excelize cell value.
func cellValue(val any) any {
	// ssql:domain fixed: a sheet cell is a number, bool or text; every other scalar is its text
	switch v := val.(type) {
	case int64:
		return v
	case ssql.Int128:
		return v.String() // a sheet's numbers are doubles: the digits stay exact as text
	case ssql.Decimal:
		return v.Float64() // a sheet's numbers are doubles (exact to 2^53 units)
	case float64:
		return v
	case bool:
		return v
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

// SheetNames returns the list of sheet names in an XLSX file.
// Useful for exploring multi-sheet workbooks.
func SheetNames(filename string) ([]string, error) {
	f, err := excelize.OpenFile(filename)
	if err != nil {
		return nil, fmt.Errorf("opening XLSX file %s: %w", filename, err)
	}
	defer f.Close()

	return f.GetSheetList(), nil
}
