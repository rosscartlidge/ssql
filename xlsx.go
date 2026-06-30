//go:build !slim

package ssql

import (
	"fmt"
	"iter"
	"slices"

	"github.com/xuri/excelize/v2"
)

// XLSXConfig configures XLSX reading and writing
type XLSXConfig struct {
	SheetName string // Sheet to read/write (default: first sheet for read, "Sheet1" for write)
}

// DefaultXLSXConfig provides sensible defaults for XLSX processing
func DefaultXLSXConfig() XLSXConfig {
	return XLSXConfig{
		SheetName: "",
	}
}

// ReadXLSX reads an XLSX file and returns an iterator of Records.
// Row 1 is treated as headers (field names), remaining rows are data.
// Types are inferred from cell values: numbers become int64 or float64,
// booleans become bool, everything else becomes string.
//
// Returns error if file cannot be opened or parsed.
//
// Example:
//
//	data, err := ssql.ReadXLSX("sales.xlsx")
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
//	data, err := ssql.ReadXLSX("workbook.xlsx", ssql.XLSXConfig{SheetName: "Sales"})
func ReadXLSX(filename string, config ...XLSXConfig) (iter.Seq[Record], error) {
	cfg := DefaultXLSXConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	f, err := excelize.OpenFile(filename)
	if err != nil {
		return nil, fmt.Errorf("opening XLSX file %s: %w", filename, err)
	}

	// Determine which sheet to read
	sheetName := cfg.SheetName
	if sheetName == "" {
		sheetName = f.GetSheetName(0)
		if sheetName == "" {
			f.Close()
			return nil, fmt.Errorf("XLSX file %s has no sheets", filename)
		}
	}

	// Get all rows
	rows, err := f.GetRows(sheetName)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("reading sheet %q from %s: %w", sheetName, filename, err)
	}

	if len(rows) < 1 {
		f.Close()
		return func(yield func(Record) bool) {}, nil
	}

	// First row is headers
	headers := rows[0]
	if len(headers) == 0 {
		f.Close()
		return func(yield func(Record) bool) {}, nil
	}

	// Build schema from headers
	fieldNames := make([]string, len(headers))
	copy(fieldNames, headers)
	slices.Sort(fieldNames)
	schema := NewSchema(fieldNames)

	// Map CSV column index to schema index
	fieldIndices := make([]int, len(headers))
	for i, h := range headers {
		fieldIndices[i] = schema.Index(h)
	}

	// Infer types from second row (first data row) if available
	var fieldParsers []func(string) any
	if len(rows) > 1 {
		firstDataRow := rows[1]
		fieldParsers = make([]func(string) any, len(headers))
		for i := range headers {
			if i < len(firstDataRow) {
				fieldParsers[i] = getParserForType(inferFieldType(firstDataRow[i]))
			} else {
				fieldParsers[i] = parseAsString
			}
		}
	}

	dataRows := rows[1:]

	seq := func(yield func(Record) bool) {
		defer f.Close()

		numFields := schema.Width()
		for _, row := range dataRows {
			values := make([]any, numFields)

			for i, cell := range row {
				if i < len(fieldIndices) {
					if fieldParsers != nil && i < len(fieldParsers) {
						values[fieldIndices[i]] = fieldParsers[i](cell)
					} else {
						values[fieldIndices[i]] = parseValue(cell)
					}
				}
			}

			if !yield(NewRecordFromSchema(schema, values)) {
				return
			}
		}
	}

	return seq, nil
}

// WriteXLSX writes records to an XLSX file.
// Field names are used as headers in row 1, data follows in subsequent rows.
// Field order is preserved from the record schema.
//
// Example:
//
//	err := ssql.WriteXLSX(records, "output.xlsx")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Write to a specific sheet
//	err := ssql.WriteXLSX(records, "output.xlsx", ssql.XLSXConfig{SheetName: "Results"})
func WriteXLSX(records iter.Seq[Record], filename string, config ...XLSXConfig) error {
	cfg := DefaultXLSXConfig()
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
	var allRecords []Record
	var fields []string
	fieldSet := make(map[string]bool)

	for record := range records {
		allRecords = append(allRecords, record)
		if fields == nil {
			// Use first record's schema field order if available
			if record.schema != nil {
				for _, field := range record.schema.fields {
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
			val, exists := Get[any](record, field)
			if !exists {
				continue
			}
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, rowIdx+2)
			if err := f.SetCellValue(sheetName, cell, xlsxCellValue(val)); err != nil {
				return fmt.Errorf("writing cell: %w", err)
			}
		}
	}

	return f.SaveAs(filename)
}

// xlsxCellValue converts a Go value to an appropriate excelize cell value.
func xlsxCellValue(val any) any {
	switch v := val.(type) {
	case int64:
		return v
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

// ReadXLSXSheetNames returns the list of sheet names in an XLSX file.
// Useful for exploring multi-sheet workbooks.
func ReadXLSXSheetNames(filename string) ([]string, error) {
	f, err := excelize.OpenFile(filename)
	if err != nil {
		return nil, fmt.Errorf("opening XLSX file %s: %w", filename, err)
	}
	defer f.Close()

	return f.GetSheetList(), nil
}
