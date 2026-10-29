package ssql

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
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

// parseInt128Cell reads an int128 column's cell: any integer literal
// that fits 128 bits, small ones included (the column is one type).
func parseInt128Cell(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if w, err := ParseInt128(s); err == nil {
		return w, nil
	}
	return nil, errCellType
}

// parseDecimalCell reads a cell of a column declared decimal:scale —
// the text at that scale, rounding half away from zero as DuckDB's
// read_csv does with a DECIMAL type; an empty cell is missing.
func parseDecimalCell(scale int) cellParser {
	return func(s string) (any, error) {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, nil
		}
		if d, err := ParseDecimalScale(s, scale); err == nil {
			return d, nil
		}
		return nil, errCellType
	}
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

// parseTimeCell parses a declared `time` column (from csv -type F time).
func parseTimeCell(s string) (any, error) {
	if t, ok := ParseTime(strings.TrimSpace(s)); ok {
		return t, nil
	}
	return nil, errCellType
}

// parseTimeCellPattern parses a time column whose cells follow a pattern
// (strftime or a Go layout — TimeLayout). The layout is converted once; a
// pattern that cannot be parsed makes every cell an error naming it.
func parseTimeCellPattern(pattern string) cellParser {
	layout, lerr := TimeLayout(pattern)
	return func(s string) (any, error) {
		if lerr != nil {
			return nil, lerr
		}
		t, err := time.ParseInLocation(layout, strings.TrimSpace(s), StreamLocation()) // a local clock: the pipeline zone
		if err != nil {
			return nil, errCellType
		}
		return t, nil
	}
}

// zeroForEmpty wraps a column's parser so an empty cell is the type's
// zero value — 0, 0.0, false — instead of missing: `from csv -empty
// zero`, the user's explicit choice where the data means a blank as
// nothing-to-add (a sales sheet with blank cells). The default stays
// DFC124's absent cell. A string column is "" either way; a time or
// json column keeps the empty cell missing (a zero time is not a
// value anyone wrote).
func zeroForEmpty(ft FieldType, p cellParser) cellParser {
	var zero any
	if ft.IsDecimal() {
		zero = MustNewDecimal(Int128{}, ft.Scale())
		return func(s string) (any, error) {
			if strings.TrimSpace(s) == "" {
				return zero, nil
			}
			return p(s)
		}
	}
	switch ft {
	case FieldTypeInt:
		zero = int64(0)
	case FieldTypeInt128:
		zero = Int128{}
	case FieldTypeFloat:
		zero = float64(0)
	case FieldTypeBool:
		zero = false
	default:
		return p
	}
	return func(s string) (any, error) {
		if strings.TrimSpace(s) == "" {
			return zero, nil
		}
		return p(s)
	}
}

// parserForType returns the cell parser for a FieldType.
func parserForType(ft FieldType) cellParser {
	if ft.IsDecimal() {
		return parseDecimalCell(ft.Scale())
	}
	switch ft {
	case FieldTypeInt:
		return parseIntCell
	case FieldTypeInt128:
		return parseInt128Cell
	case FieldTypeFloat:
		return parseFloatCell
	case FieldTypeBool:
		return parseBoolCell
	case FieldTypeTime:
		return parseTimeCell
	case FieldTypeJSON:
		return parseJSONCell
	default:
		return parseStringCell
	}
}

// parseJSONCell reads a cell declared json (`-type COL json`, a sidecar's
// object/array column): an array or object is the nested value, as its
// own text; an empty cell is missing; anything else is a cell error —
// the column was declared nested and this cell is not (DFC146; before
// 2026-10-10 the declaration was accepted and the cell stayed text, so
// no path into it resolved).
func parseJSONCell(s string) (any, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil, nil
	}
	if (t[0] == '{' || t[0] == '[') && json.Valid([]byte(t)) {
		return JSONString(t), nil
	}
	return nil, errCellType
}

// ZeroPaddedNumber reports whether s is digits with a leading zero that a
// number would not have: 007, 02134, -05, 00.5 — but not 0, 0.5 or -0.25.
// Such a value is an identifier (a postcode, an account or part number);
// type inference must leave its column as text, because parsing it as a
// number discards the zeros and nothing downstream can put them back.
// DuckDB's CSV sniffer draws the same line. One rule for every inference
// site: the CSV/TSV reader, the typed sampler, the SQL prologue sampler.
func ZeroPaddedNumber(s string) bool {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	return len(s) > 1 && s[0] == '0' && s[1] >= '0' && s[1] <= '9'
}

// inferColumnType picks the narrowest type every non-empty sampled
// value fits: int → int128 → float → bool → string. Empty cells carry no
// type information (they are missing); a column with no non-empty sample
// is a string column. Bool is recognised from true/false only — `1`/`0`
// are ints, and a column mixing `true` with numbers is text. An integer
// literal past int64 makes the column int128, exact (DFC155 C; it was a
// float, rounded past 2^53); past int128 it is a float as before.
func inferColumnType(values []string) FieldType {
	seen := false
	allInt, allInt128, allFloat, allBool := true, true, true, true
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		seen = true
		if ZeroPaddedNumber(v) {
			// 007, 02134: an identifier that happens to be digits. Read as
			// a number it loses its zeros for good (DFC133).
			allInt, allInt128, allFloat = false, false, false
		}
		if allInt {
			_, err := strconv.ParseInt(v, 10, 64)
			allInt = err == nil
		}
		if allInt128 && !allInt {
			_, err := ParseInt128(v)
			allInt128 = err == nil
		}
		if allFloat {
			_, err := strconv.ParseFloat(v, 64)
			allFloat = err == nil
		}
		if allBool {
			l := strings.ToLower(v)
			allBool = l == "true" || l == "false"
		}
		if !allInt128 && !allFloat && !allBool {
			break
		}
	}
	switch {
	case !seen:
		return FieldTypeString
	case allInt:
		return FieldTypeInt
	case allInt128:
		return FieldTypeInt128
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
	readRows(csvReader, cfg, yield)
}

// RowError is a row of delimited text that could not be read as a row: a
// bare quote in an unquoted field, a wrong number of fields, an
// unterminated quote. Row is the 1-based data row. It is the malformed-row
// counterpart of *CellError (a cell not of its column's type) and of the
// JSON Lines reader's *LineError: the unsafe readers panic with it, the
// safe readers yield it.
type RowError struct {
	Row int64
	Err error
}

func (e *RowError) Error() string { return fmt.Sprintf("failed to read CSV row %d: %v", e.Row, e.Err) }
func (e *RowError) Unwrap() error { return e.Err }

// rowReader is the row source readRows types and parses: encoding/csv's
// Reader, or the delimited-text splitter behind ReadTSV. Read returns
// io.EOF at the end; a returned slice may be reused by the next Read
// (readRows clones what it retains for the inference sample).
type rowReader interface {
	Read() ([]string, error)
}

// readRows is the ONE implementation of column typing for row-shaped
// text input — CSV and TSV share it, so the two formats cannot drift
// (TSV typed each value on its own until v4.91.0: a column of ints with
// one "1.5" came out mixed, and an empty cell was "" rather than absent).
func readRows(csvReader rowReader, cfg CSVConfig, yield func(Record, error) bool) {
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
			if !yield(Record{}, &RowError{Row: rowIndex, Err: err}) {
				return
			}
			continue
		}
		sample = append(sample, slices.Clone(row))
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

	// Shared schema, created ONCE (the #1 performance rule), in HEADER
	// order: a record's field order is its schema's, and the header is
	// the order the file's author chose. (Sorted until 2026-09-23, which
	// made every record-mode output alphabetical.) Records are positional
	// against it, so map CSV column → schema slot.
	fieldNames := slices.Clone(colNames)
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
		if pattern := cfg.TimeFormats[name]; pattern != "" {
			// A declared pattern makes the column a time read with it
			// (-format COL PATTERN, a sidecar's date format).
			types[i] = FieldTypeTime
			parsers[i] = parseTimeCellPattern(pattern)
		}
		if cfg.EmptyAsZero {
			parsers[i] = zeroForEmpty(types[i], parsers[i])
		}
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
			if !yield(Record{}, &RowError{Row: rowIndex, Err: err}) {
				return
			}
			continue
		}
		if !emit(row, rowIndex) {
			return
		}
	}
}
