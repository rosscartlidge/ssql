package ssql

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// DefaultInferRows is how many leading data rows the CSV reader examines
// to infer each column's type when CSVConfig.InferRows is zero. The
// typed lane's schema sampler uses the same figure, and DuckDB's CSV
// sniffer samples a comparable prefix.
//
// Why a sample and not the first row: a column that opens with `0`
// used to be typed int and every later `.001` was silently truncated
// to 0 — a whole signal became zeros without an error (DFC124 §3,
// found by the signal-processing codelab runner, 2026-09-05). The
// sample makes that rare; the loud CellError below makes it impossible
// to be silent.
const DefaultInferRows = 1000

// CellError reports a CSV cell that does not parse as its column's
// type. After the leading sample has fixed a column's type, a later
// value that does not fit is an ERROR — never a coerced zero (the
// schema header is already on the wire, so the type cannot widen
// mid-stream; the honest options are to fail or to be told the type).
//
// The unsafe readers (ReadCSV, ReadCSVFromReader, ReadXLSX) panic with
// a *CellError, per their documented fail-fast contract; the *Safe
// readers yield it. The CLI recovers it into a normal error that names
// the override flag.
type CellError struct {
	Row     int64     // 1-based data row (the header is not counted)
	Column  string    // column name (or col_N without headers)
	Value   string    // the offending cell, trimmed
	Type    FieldType // the type the column was fixed to
	Sampled int       // rows the type was inferred from; 0 = explicit override
}

func (e *CellError) Error() string {
	how := "explicit column type"
	if e.Sampled > 0 {
		how = fmt.Sprintf("type inferred from the first %d rows", e.Sampled)
	}
	return fmt.Sprintf("row %d, column %q: %q is not %s (%s)", e.Row, e.Column, e.Value, e.Type, how)
}

// errCellType is the parsers' sentinel; the reader wraps it into a
// CellError with row/column context.
var errCellType = errors.New("cell does not parse as the column type")

// cellParser converts one trimmed cell to its typed value. An empty
// cell is missing → nil for every type but string (DFC124).
type cellParser func(s string) (any, error)

func parseStringCell(s string) (any, error) { return strings.TrimSpace(s), nil }

func parseIntCell(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, nil
	}
	// No float truncation: "1.5" in an int column is the caller's
	// problem to hear about, not a 1 to compute with.
	return nil, errCellType
}

func parseFloatCell(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, nil
	}
	return nil, errCellType
}

// parseBoolCell accepts the lenient spellings (true/false, 1/0,
// yes/no, y/n, on/off) so an explicitly bool-typed column of yes/no
// data works; inference itself only recognises true/false.
func parseBoolCell(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	switch strings.ToLower(s) {
	case "true", "1", "yes", "y", "on":
		return true, nil
	case "false", "0", "no", "n", "off":
		return false, nil
	}
	return nil, errCellType
}

// parserForType returns the cell parser for a FieldType.
func parserForType(ft FieldType) cellParser {
	switch ft {
	case FieldTypeInt:
		return parseIntCell
	case FieldTypeFloat:
		return parseFloatCell
	case FieldTypeBool:
		return parseBoolCell
	default:
		return parseStringCell
	}
}

// inferColumnType picks the narrowest type every non-empty sampled
// value fits: int → float → bool → string. Empty cells carry no type
// information (they are missing); a column with no non-empty sample is
// a string column. Bool is recognised from true/false only — `1`/`0`
// are ints, and a column mixing `true` with numbers is text.
func inferColumnType(values []string) FieldType {
	seen := false
	allInt, allFloat, allBool := true, true, true
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		seen = true
		if allInt {
			_, err := strconv.ParseInt(v, 10, 64)
			allInt = err == nil
		}
		if allFloat {
			_, err := strconv.ParseFloat(v, 64)
			allFloat = err == nil
		}
		if allBool {
			l := strings.ToLower(v)
			allBool = l == "true" || l == "false"
		}
		if !allInt && !allFloat && !allBool {
			break
		}
	}
	switch {
	case !seen:
		return FieldTypeString
	case allInt:
		return FieldTypeInt
	case allFloat:
		return FieldTypeFloat
	case allBool:
		return FieldTypeBool
	default:
		return FieldTypeString
	}
}

// inferFieldType is the single-value case of inferColumnType.
func inferFieldType(s string) FieldType { return inferColumnType([]string{s}) }

// readCSVRows is the one CSV reading loop behind ReadCSVFromReader and
// ReadCSVSafeFromReader. It reads the header, buffers up to
// cfg.InferRows data rows to infer column types (explicit
// TypeOverrides/DefaultType win), emits the buffered rows, then streams
// the rest with the fixed parsers. Each row is yielded as (record, nil);
// a malformed row or a cell that does not fit its column's type is
// yielded as (Record{}, err) and skipped — the caller decides whether
// to stop (unsafe: panic) or carry on (safe: forward). Returns when the
// input ends or yield returns false.
func readCSVRows(reader io.Reader, cfg CSVConfig, yield func(Record, error) bool) {
	csvReader := csv.NewReader(bufio.NewReader(reader))
	csvReader.Comma = cfg.Delimiter
	csvReader.Comment = cfg.Comment

	var headers []string
	if cfg.HasHeaders {
		headerRow, err := csvReader.Read()
		if err != nil {
			if err != io.EOF {
				yield(Record{}, fmt.Errorf("failed to read CSV headers: %w", err))
			}
			return
		}
		headers = headerRow
	}
	hasHeaders := cfg.HasHeaders && len(headers) > 0

	inferRows := cfg.InferRows
	if inferRows <= 0 {
		inferRows = DefaultInferRows
	}

	// The inference sample. rowIndex counts data rows (1-based) for
	// error messages, including malformed rows that were skipped.
	var sample [][]string
	rowIndex := int64(0)
	for len(sample) < inferRows {
		row, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		rowIndex++
		if err != nil {
			if !yield(Record{}, fmt.Errorf("failed to read CSV row %d: %w", rowIndex, err)) {
				return
			}
			continue
		}
		sample = append(sample, row)
	}
	if len(sample) == 0 {
		return
	}

	// Column names: the header, or col_N from the first row's width.
	ncols := len(headers)
	if !hasHeaders {
		ncols = len(sample[0])
	}
	colNames := make([]string, ncols)
	for i := range colNames {
		if hasHeaders {
			colNames[i] = headers[i]
		} else {
			colNames[i] = fmt.Sprintf("col_%d", i)
		}
	}

	// Shared schema, created ONCE (the #1 performance rule); records
	// are positional against it, so map CSV column → schema slot.
	fieldNames := slices.Clone(colNames)
	slices.Sort(fieldNames)
	schema := NewSchema(fieldNames)
	fieldIndices := make([]int, ncols)
	for i, name := range colNames {
		fieldIndices[i] = schema.Index(name)
	}

	// One parser per column: explicit override, else the configured
	// default, else inferred from the sample.
	parsers := make([]cellParser, ncols)
	types := make([]FieldType, ncols)
	sampled := make([]int, ncols)
	colVals := make([]string, 0, len(sample))
	for i, name := range colNames {
		if ft, ok := cfg.TypeOverrides[name]; ok {
			types[i] = ft
		} else if cfg.DefaultType != FieldTypeAuto {
			types[i] = cfg.DefaultType
		} else {
			colVals = colVals[:0]
			for _, row := range sample {
				if i < len(row) {
					colVals = append(colVals, row[i])
				}
			}
			types[i] = inferColumnType(colVals)
			sampled[i] = len(sample)
		}
		parsers[i] = parserForType(types[i])
	}

	width := schema.Width()
	emit := func(row []string, at int64) bool {
		values := make([]any, width)
		for i, cell := range row {
			if i >= ncols {
				continue // ragged row: extra cells have no column
			}
			v, err := parsers[i](cell)
			if err != nil {
				return yield(Record{}, &CellError{
					Row: at, Column: colNames[i], Value: strings.TrimSpace(cell),
					Type: types[i], Sampled: sampled[i],
				})
			}
			values[fieldIndices[i]] = v
		}
		return yield(NewRecordFromSchema(schema, values), nil)
	}

	// The sample was read before any parser existed; replay it.
	sampleStart := rowIndex - int64(len(sample)) + 1
	for k, row := range sample {
		if !emit(row, sampleStart+int64(k)) {
			return
		}
	}
	// Stream the rest.
	for {
		row, err := csvReader.Read()
		if err == io.EOF {
			return
		}
		rowIndex++
		if err != nil {
			if !yield(Record{}, fmt.Errorf("failed to read CSV row %d: %w", rowIndex, err)) {
				return
			}
			continue
		}
		if !emit(row, rowIndex) {
			return
		}
	}
}
