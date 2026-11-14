package ssql

import (
	"iter"
	"slices"
	"strings"
)

// RecordsFromStringRows turns a table of text cells into records, typed
// the way the CSV reader types a file: each column's type is inferred
// from its first DefaultInferRows non-header rows (int → int128 → float
// → bool → string), and a later cell that does not parse as its column's
// type panics with a *CellError, the CSV reader's fail-fast contract.
// A row shorter than the header leaves its missing fields absent.
//
// It is the shared core of readers whose format hands over a whole sheet
// of strings, such as package xlsx (DFC168): the format package owns the
// file, the root package owns the typing.
func RecordsFromStringRows(headers []string, rows [][]string) iter.Seq[Record] {
	fieldNames := slices.Clone(headers)
	slices.Sort(fieldNames)
	schema := NewSchema(fieldNames)

	// Map column index to schema index.
	fieldIndices := make([]int, len(headers))
	for i, h := range headers {
		fieldIndices[i] = schema.Index(h)
	}

	var fieldParsers []cellParser
	var fieldTypes []FieldType
	if len(rows) > 0 {
		sample := rows
		if len(sample) > DefaultInferRows {
			sample = sample[:DefaultInferRows]
		}
		fieldParsers = make([]cellParser, len(headers))
		fieldTypes = make([]FieldType, len(headers))
		colVals := make([]string, 0, len(sample))
		for i := range headers {
			colVals = colVals[:0]
			for _, row := range sample {
				if i < len(row) {
					colVals = append(colVals, row[i])
				}
			}
			fieldTypes[i] = inferColumnType(colVals)
			fieldParsers[i] = parserForType(fieldTypes[i])
		}
	}

	return func(yield func(Record) bool) {
		numFields := schema.Width()
		for rowNum, row := range rows {
			values := make([]any, numFields)
			for i, cell := range row {
				if i >= len(fieldIndices) {
					continue
				}
				v, err := fieldParsers[i](cell)
				if err != nil {
					panic(&CellError{Row: int64(rowNum + 1), Column: headers[i], Value: strings.TrimSpace(cell),
						Type: fieldTypes[i], Sampled: min(len(rows), DefaultInferRows)})
				}
				values[fieldIndices[i]] = v
			}
			if !yield(NewRecordFromSchema(schema, values)) {
				return
			}
		}
	}
}
