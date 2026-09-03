package ssql

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// jsonBufferPool provides reusable buffers for fast JSON encoding
var jsonBufferPool = sync.Pool{
	New: func() any {
		// Start with 4KB buffer - large enough for most records
		buf := make([]byte, 0, 4096)
		return &buf
	},
}

// ============================================================================
// I/O OPERATIONS WITH DUAL ERROR HANDLING
// ============================================================================

// ============================================================================
// CSV OPERATIONS
// ============================================================================

// CSVConfig configures CSV reading and writing
type CSVConfig struct {
	HasHeaders    bool
	Delimiter     rune
	Comment       rune
	Fields        []string             // Optional: fields to write (nil = auto-detect all fields in alphabetical order)
	TypeOverrides map[string]FieldType // Optional: override auto-detection for specific fields
	DefaultType   FieldType            // Optional: default type for all fields (FieldTypeAuto = auto-detect)
}

// FieldType represents the type to use when parsing CSV fields
type FieldType int

const (
	FieldTypeAuto   FieldType = iota // Auto-detect type (default)
	FieldTypeString                  // Always treat as string
	FieldTypeInt                     // Parse as int64
	FieldTypeFloat                   // Parse as float64
	FieldTypeBool                    // Parse as bool
)

// DefaultCSVConfig provides sensible defaults for CSV processing
func DefaultCSVConfig() CSVConfig {
	return CSVConfig{
		HasHeaders:    true,
		Delimiter:     ',',
		Comment:       '#',
		Fields:        nil,           // Auto-detect fields
		TypeOverrides: nil,           // No overrides
		DefaultType:   FieldTypeAuto, // Auto-detect types
	}
}

// ParseFieldType converts a string type name to FieldType
// Supported values: "auto", "string", "int", "float", "bool"
func ParseFieldType(s string) (FieldType, error) {
	switch strings.ToLower(s) {
	case "auto":
		return FieldTypeAuto, nil
	case "string", "str", "text":
		return FieldTypeString, nil
	case "int", "integer", "int64":
		return FieldTypeInt, nil
	case "float", "float64", "double", "number":
		return FieldTypeFloat, nil
	case "bool", "boolean":
		return FieldTypeBool, nil
	default:
		return FieldTypeAuto, fmt.Errorf("unknown field type: %q (use: auto, string, int, float, bool)", s)
	}
}

// FieldTypeName returns the string name for a FieldType
func (ft FieldType) String() string {
	switch ft {
	case FieldTypeString:
		return "string"
	case FieldTypeInt:
		return "int"
	case FieldTypeFloat:
		return "float"
	case FieldTypeBool:
		return "bool"
	default:
		return "auto"
	}
}

// ============================================================================
// CSV OPERATIONS WITH IO.READER/IO.WRITER
// ============================================================================

// ReadCSVFromReader reads CSV data from an io.Reader and returns an iterator
func ReadCSVFromReader(reader io.Reader, config ...CSVConfig) iter.Seq[Record] {
	cfg := DefaultCSVConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	return func(yield func(Record) bool) {
		// Wrap reader in bufio.Reader for better performance with large files
		bufferedReader := bufio.NewReader(reader)
		csvReader := csv.NewReader(bufferedReader)
		csvReader.Comma = cfg.Delimiter
		csvReader.Comment = cfg.Comment

		var headers []string
		if cfg.HasHeaders {
			headerRow, err := csvReader.Read()
			if err != nil {
				return
			}
			headers = headerRow
		}

		// Read first data row to infer types (when using auto-detection)
		firstRow, err := csvReader.Read()
		if err != nil {
			return // Empty file or error
		}

		// Determine field names
		var fieldNames []string
		if cfg.HasHeaders && len(headers) > 0 {
			fieldNames = make([]string, len(headers))
			copy(fieldNames, headers)
		} else {
			fieldNames = make([]string, len(firstRow))
			for i := range firstRow {
				fieldNames[i] = fmt.Sprintf("col_%d", i)
			}
		}

		// Sort field names and create shared schema ONCE
		slices.Sort(fieldNames)
		schema := NewSchema(fieldNames)

		// Build index map: original field position -> schema position
		var fieldIndices []int // maps CSV column index to schema index
		if cfg.HasHeaders && len(headers) > 0 {
			fieldIndices = make([]int, len(headers))
			for i, h := range headers {
				fieldIndices[i] = schema.Index(h)
			}
		} else {
			fieldIndices = make([]int, len(firstRow))
			for i := range firstRow {
				fieldIndices[i] = schema.Index(fmt.Sprintf("col_%d", i))
			}
		}

		// Determine parsers: either from config or inferred from first row
		var fieldParsers []func(string) any
		if cfg.HasHeaders && len(headers) > 0 {
			fieldParsers = make([]func(string) any, len(headers))
			for i, header := range headers {
				// Check for explicit override first
				if cfg.TypeOverrides != nil {
					if ft, ok := cfg.TypeOverrides[header]; ok {
						fieldParsers[i] = getParserForType(ft)
						continue
					}
				}
				// If default type is specified (not auto), use it
				if cfg.DefaultType != FieldTypeAuto {
					fieldParsers[i] = getParserForType(cfg.DefaultType)
					continue
				}
				// Auto-detect: infer type from first row value, then lock it
				if i < len(firstRow) {
					fieldParsers[i] = getParserForType(inferFieldType(firstRow[i]))
				} else {
					fieldParsers[i] = parseAsString
				}
			}
		} else {
			// No headers - infer from first row
			fieldParsers = make([]func(string) any, len(firstRow))
			for i, value := range firstRow {
				if cfg.DefaultType != FieldTypeAuto {
					fieldParsers[i] = getParserForType(cfg.DefaultType)
				} else {
					fieldParsers[i] = getParserForType(inferFieldType(value))
				}
			}
		}

		// Process first row using shared schema
		values := make([]any, schema.Width())
		for i, value := range firstRow {
			if i < len(fieldIndices) {
				values[fieldIndices[i]] = fieldParsers[i](value)
			}
		}
		if !yield(NewRecordFromSchema(schema, values)) {
			return
		}

		// Process remaining rows with shared schema
		numFields := schema.Width()
		for {
			row, err := csvReader.Read()
			if err != nil {
				return // EOF or error
			}

			// Allocate new values slice per record (schema is shared)
			values := make([]any, numFields)

			// Parse values into correct schema positions
			for i, value := range row {
				if i < len(fieldIndices) {
					values[fieldIndices[i]] = fieldParsers[i](value)
				}
			}

			// Create record with shared schema (no allocation for schema)
			if !yield(NewRecordFromSchema(schema, values)) {
				return
			}
		}
	}
}

// inferFieldType determines the FieldType from a sample value
func inferFieldType(s string) FieldType {
	s = strings.TrimSpace(s)

	// Try integer
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return FieldTypeInt
	}

	// Try float
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return FieldTypeFloat
	}

	// Try boolean (explicit true/false only)
	if s == "true" || s == "false" {
		return FieldTypeBool
	}

	// Default to string
	return FieldTypeString
}

// getParserForType returns the parser function for a specific FieldType
func getParserForType(ft FieldType) func(string) any {
	switch ft {
	case FieldTypeString:
		return parseAsString
	case FieldTypeInt:
		return parseAsInt
	case FieldTypeFloat:
		return parseAsFloat
	case FieldTypeBool:
		return parseAsBool
	default:
		return parseValue // Should not happen, but fallback to auto
	}
}

// Fast type-specific parsers (no type detection overhead)

// parseAsString returns the value as-is (trimmed)
func parseAsString(s string) any {
	return strings.TrimSpace(s)
}

// parseAsInt parses as int64. An EMPTY cell is missing → nil (the
// field is absent; DFC124) — never 0, which would be a value the data
// does not contain. Other unparsable text still falls back to 0 (a
// recorded follow-up in DFC124 §3).
func parseAsInt(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	// If it looks like a float, truncate it
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return int64(0)
}

// parseAsFloat parses as float64; an empty cell is missing → nil (DFC124).
func parseAsFloat(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return float64(0)
}

// parseAsBool parses as bool (true/false, 1/0, yes/no), returns false on error
func parseAsBool(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil // missing (DFC124)
	}
	switch strings.ToLower(s) {
	case "true", "1", "yes", "y", "on":
		return true
	case "false", "0", "no", "n", "off":
		return false
	default:
		return false
	}
}

// ReadCSVSafeFromReader reads CSV data from an io.Reader with error handling
func ReadCSVSafeFromReader(reader io.Reader, config ...CSVConfig) iter.Seq2[Record, error] {
	cfg := DefaultCSVConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	return func(yield func(Record, error) bool) {
		// Wrap reader in bufio.Reader for better performance with large files
		bufferedReader := bufio.NewReader(reader)
		csvReader := csv.NewReader(bufferedReader)
		csvReader.Comma = cfg.Delimiter
		csvReader.Comment = cfg.Comment

		var headers []string
		if cfg.HasHeaders {
			headerRow, err := csvReader.Read()
			if err != nil {
				yield(Record{}, fmt.Errorf("failed to read CSV headers: %w", err))
				return
			}
			headers = headerRow
		}

		// Read first data row to infer types (when using auto-detection)
		firstRow, err := csvReader.Read()
		if err == io.EOF {
			return // Empty file
		}
		if err != nil {
			yield(Record{}, fmt.Errorf("failed to read first CSV row: %w", err))
			return
		}

		// Determine field names
		var fieldNames []string
		if cfg.HasHeaders && len(headers) > 0 {
			fieldNames = make([]string, len(headers))
			copy(fieldNames, headers)
		} else {
			fieldNames = make([]string, len(firstRow))
			for i := range firstRow {
				fieldNames[i] = fmt.Sprintf("col_%d", i)
			}
		}

		// Sort field names and create shared schema ONCE
		slices.Sort(fieldNames)
		schema := NewSchema(fieldNames)

		// Build index map: original field position -> schema position
		var fieldIndices []int
		if cfg.HasHeaders && len(headers) > 0 {
			fieldIndices = make([]int, len(headers))
			for i, h := range headers {
				fieldIndices[i] = schema.Index(h)
			}
		} else {
			fieldIndices = make([]int, len(firstRow))
			for i := range firstRow {
				fieldIndices[i] = schema.Index(fmt.Sprintf("col_%d", i))
			}
		}

		// Determine parsers: either from config or inferred from first row
		var fieldParsers []func(string) any
		if cfg.HasHeaders && len(headers) > 0 {
			fieldParsers = make([]func(string) any, len(headers))
			for i, header := range headers {
				// Check for explicit override first
				if cfg.TypeOverrides != nil {
					if ft, ok := cfg.TypeOverrides[header]; ok {
						fieldParsers[i] = getParserForType(ft)
						continue
					}
				}
				// If default type is specified (not auto), use it
				if cfg.DefaultType != FieldTypeAuto {
					fieldParsers[i] = getParserForType(cfg.DefaultType)
					continue
				}
				// Auto-detect: infer type from first row value, then lock it
				if i < len(firstRow) {
					fieldParsers[i] = getParserForType(inferFieldType(firstRow[i]))
				} else {
					fieldParsers[i] = parseAsString
				}
			}
		} else {
			// No headers - infer from first row
			fieldParsers = make([]func(string) any, len(firstRow))
			for i, value := range firstRow {
				if cfg.DefaultType != FieldTypeAuto {
					fieldParsers[i] = getParserForType(cfg.DefaultType)
				} else {
					fieldParsers[i] = getParserForType(inferFieldType(value))
				}
			}
		}

		// Process first row using shared schema
		values := make([]any, schema.Width())
		for i, value := range firstRow {
			if i < len(fieldIndices) {
				values[fieldIndices[i]] = fieldParsers[i](value)
			}
		}
		if !yield(NewRecordFromSchema(schema, values), nil) {
			return
		}

		// Process remaining rows with shared schema. rowIndex is the
		// 1-based data-row counter used only for error messages.
		rowIndex := int64(1)
		numFields := schema.Width()
		for {
			row, err := csvReader.Read()
			if err == io.EOF {
				return
			}
			if err != nil {
				if !yield(Record{}, fmt.Errorf("failed to read CSV row %d: %w", rowIndex, err)) {
					return
				}
				continue
			}

			// Allocate new values slice per record (schema is shared)
			values := make([]any, numFields)

			// Parse values into correct schema positions
			for i, value := range row {
				if i < len(fieldIndices) {
					values[fieldIndices[i]] = fieldParsers[i](value)
				}
			}
			rowIndex++

			// Create record with shared schema
			if !yield(NewRecordFromSchema(schema, values), nil) {
				return
			}
		}
	}
}

// WriteCSVToWriter writes records as CSV to an io.Writer
func WriteCSVToWriter(sb iter.Seq[Record], writer io.Writer, config ...CSVConfig) error {
	cfg := DefaultCSVConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	csvWriter := csv.NewWriter(writer)
	csvWriter.Comma = cfg.Delimiter

	var fields []string
	var recordsBuffer []Record

	// Determine fields to write
	if cfg.Fields != nil {
		// Use explicitly provided fields
		fields = cfg.Fields
	} else {
		// Auto-detect: materialize all records to collect unique field names
		fieldSet := make(map[string]bool)
		for record := range sb {
			recordsBuffer = append(recordsBuffer, record)
			for field, val := range record.All() {
				// Skip complex fields (iter.Seq, Record) and internal metadata fields
				if !isIterSeq(val) {
					if _, isRecord := val.(Record); !isRecord {
						// Skip internal metadata fields starting with underscore
						if !strings.HasPrefix(field, "_") {
							fieldSet[field] = true
						}
					}
				}
			}
		}

		// Sort field names alphabetically for consistent ordering
		for field := range fieldSet {
			fields = append(fields, field)
		}

		// Sort using standard library
		slices.Sort(fields)
	}

	// Write headers if enabled
	if cfg.HasHeaders {
		if err := csvWriter.Write(fields); err != nil {
			return fmt.Errorf("failed to write CSV headers: %w", err)
		}
	}

	// Write data rows
	dataSource := sb
	if len(recordsBuffer) > 0 {
		// Use buffered records if we materialized for field detection
		dataSource = func(yield func(Record) bool) {
			for _, record := range recordsBuffer {
				if !yield(record) {
					return
				}
			}
		}
	}

	for record := range dataSource {
		row := make([]string, len(fields))
		for i, field := range fields {
			if value, exists := Get[any](record, field); exists {
				row[i] = formatValue(value)
			}
		}

		if err := csvWriter.Write(row); err != nil {
			return fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	csvWriter.Flush()
	return csvWriter.Error()
}

// ============================================================================
// CSV FILE CONVENIENCE FUNCTIONS
// ============================================================================

// ReadCSV reads CSV from a file and returns an iterator of Records.
// This is the primary way to load CSV data in ssql.
//
// CSV values are automatically parsed to appropriate types:
//   - Integers (like "1", "42", "-100") become int64
//   - Decimals (like "1.5", "3.14") become float64
//   - Explicit "true"/"false" strings become bool (NOT "1"/"0")
//   - Everything else stays as string
//
// Returns error if file cannot be opened.
//
// Example:
//
//	// Read CSV with default settings (has headers, comma delimiter)
//	data, err := ssql.ReadCSV("sales.csv")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Process the data
//	for record := range data {
//	    region := ssql.GetOr(record, "region", "")
//	    amount := ssql.GetOr(record, "amount", float64(0))
//	    fmt.Printf("%s: %.2f\n", region, amount)
//	}
//
//	// Custom configuration
//	config := ssql.CSVConfig{
//	    HasHeaders: false,
//	    Delimiter:  '\t',
//	}
//	data, err := ssql.ReadCSV("data.tsv", config)
func ReadCSV(filename string, config ...CSVConfig) (iter.Seq[Record], error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filename, err)
	}

	seq := func(yield func(Record) bool) {
		defer file.Close()

		// Use the io.Reader version
		for record := range ReadCSVFromReader(file, config...) {
			if !yield(record) {
				return
			}
		}
	}

	return seq, nil
}

// ReadCSVSafe reads CSV with error handling
func ReadCSVSafe(filename string, config ...CSVConfig) iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		file, err := os.Open(filename)
		if err != nil {
			if !yield(Record{}, fmt.Errorf("failed to open file %s: %w", filename, err)) {
				return
			}
			return
		}
		defer file.Close()

		// Use the io.Reader version
		for record, err := range ReadCSVSafeFromReader(file, config...) {
			if !yield(record, err) {
				return
			}
		}
	}
}

// WriteCSV writes records to a CSV file.
// Field names are auto-detected and sorted alphabetically unless specified in config.
//
// Example:
//
//	// Write processed data to CSV
//	result := ssql.Where(func(r ssql.Record) bool {
//	    age := ssql.GetOr(r, "age", int64(0))
//	    return age > 25
//	})(data)
//
//	err := ssql.WriteCSV(result, "filtered.csv")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Custom field order
//	config := ssql.CSVConfig{
//	    Fields: []string{"name", "age", "salary"},
//	}
//	err := ssql.WriteCSV(result, "output.csv", config)
func WriteCSV(sb iter.Seq[Record], filename string, config ...CSVConfig) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer file.Close()

	// Wrap in buffered writer for performance
	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Use the io.Writer version
	if err := WriteCSVToWriter(sb, writer, config...); err != nil {
		return err
	}

	return writer.Flush()
}

// ============================================================================
// JSON OPERATIONS WITH IO.READER/IO.WRITER
// ============================================================================

// ReadJSONFromReader reads JSON records from an io.Reader (one JSON object per line)
// Field types are inferred from the first record and applied consistently to all records.
func ReadJSONFromReader(reader io.Reader) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		scanner := bufio.NewScanner(reader)
		lineNumber := int64(0)

		// Track field types from first record
		var fieldTypes map[string]FieldType

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				lineNumber++
				continue
			}

			// Skip schema header lines
			if len(line) > 10 && strings.Contains(line[:min(20, len(line))], `"_schema"`) {
				lineNumber++
				continue
			}

			var rawRecord Record
			if err := json.Unmarshal([]byte(line), &rawRecord); err != nil {
				// For simple API, skip invalid JSON lines
				lineNumber++
				continue
			}

			// First valid record - infer field types
			if fieldTypes == nil {
				fieldTypes = make(map[string]FieldType)
				for key, value := range rawRecord.All() {
					fieldTypes[key] = inferJSONFieldType(value)
				}
			}

			// Coerce fields to consistent types
			record := MakeMutableRecord()
			for key, value := range rawRecord.All() {
				if ft, ok := fieldTypes[key]; ok {
					record.fields[key] = coerceToType(value, ft)
				} else {
					// New field not seen in first record - infer and lock its type
					fieldTypes[key] = inferJSONFieldType(value)
					record.fields[key] = value
				}
			}

			// Add line number metadata
			record.fields["_line_number"] = lineNumber
			lineNumber++

			if !yield(record.Freeze()) {
				return
			}
		}
	}
}

// inferJSONFieldType determines FieldType from a JSON-parsed value
func inferJSONFieldType(value any) FieldType {
	switch value.(type) {
	case float64:
		return FieldTypeFloat // JSON numbers are always float64
	case bool:
		return FieldTypeBool
	case string:
		return FieldTypeString
	case []any, map[string]any, Record:
		return FieldTypeAuto // Preserve complex types as-is
	default:
		return FieldTypeAuto // Preserve unknown types as-is
	}
}

// coerceToType converts a value to the target FieldType
func coerceToType(value any, targetType FieldType) any {
	switch targetType {
	case FieldTypeFloat:
		switch v := value.(type) {
		case float64:
			return v
		case bool:
			if v {
				return float64(1)
			}
			return float64(0)
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
			return float64(0)
		default:
			return float64(0)
		}
	case FieldTypeInt:
		switch v := value.(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case bool:
			if v {
				return int64(1)
			}
			return int64(0)
		case string:
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				return i
			}
			return int64(0)
		default:
			return int64(0)
		}
	case FieldTypeBool:
		switch v := value.(type) {
		case bool:
			return v
		case float64:
			return v != 0
		case string:
			switch strings.ToLower(v) {
			case "true", "1", "yes", "y", "on":
				return true
			default:
				return false
			}
		default:
			return false
		}
	case FieldTypeString:
		switch v := value.(type) {
		case string:
			return v
		case float64:
			return strconv.FormatFloat(v, 'g', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		default:
			return fmt.Sprintf("%v", v)
		}
	default:
		return value
	}
}

// ReadJSONSafeFromReader reads JSON records from an io.Reader with error handling
// Field types are inferred from the first record and applied consistently to all records.
func ReadJSONSafeFromReader(reader io.Reader) iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		scanner := bufio.NewScanner(reader)
		lineNumber := int64(0)

		// Track field types from first record
		var fieldTypes map[string]FieldType

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				lineNumber++
				continue
			}

			var rawRecord Record
			if err := json.Unmarshal([]byte(line), &rawRecord); err != nil {
				if !yield(Record{}, fmt.Errorf("failed to parse JSON on line %d: %w", lineNumber, err)) {
					return
				}
				lineNumber++
				continue
			}

			// First valid record - infer field types
			if fieldTypes == nil {
				fieldTypes = make(map[string]FieldType)
				for key, value := range rawRecord.All() {
					fieldTypes[key] = inferJSONFieldType(value)
				}
			}

			// Coerce fields to consistent types
			record := MakeMutableRecord()
			for key, value := range rawRecord.All() {
				if ft, ok := fieldTypes[key]; ok {
					record.fields[key] = coerceToType(value, ft)
				} else {
					// New field not seen in first record - infer and lock its type
					fieldTypes[key] = inferJSONFieldType(value)
					record.fields[key] = value
				}
			}

			record.fields["_line_number"] = lineNumber
			lineNumber++

			if !yield(record.Freeze(), nil) {
				return
			}
		}

		// Check for scanner errors
		if err := scanner.Err(); err != nil {
			yield(Record{}, fmt.Errorf("error reading input: %w", err))
		}
	}
}

// WriteJSONToWriter writes records as JSON to an io.Writer (one object per line)
func WriteJSONToWriter(sb iter.Seq[Record], writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	for record := range sb {
		// Convert complex fields for JSON compatibility
		jsonRecord := MakeMutableRecord()
		for key, value := range record.All() {
			switch v := value.(type) {
			case JSONString:
				// Parse JSONString back to structured data to avoid double-encoding
				if parsed, err := v.Parse(); err == nil {
					jsonRecord.fields[key] = parsed
				} else {
					// Fallback to string if parsing fails
					jsonRecord.fields[key] = string(v)
				}
			default:
				if isIterSeq(value) {
					// Convert iter.Seq to array for JSON
					jsonRecord.fields[key] = materializeSequence(value)
				} else {
					jsonRecord.fields[key] = value
				}
			}
		}

		if err := encoder.Encode(jsonRecord.Freeze()); err != nil {
			return fmt.Errorf("failed to write JSON record: %w", err)
		}
	}

	return nil
}

// ============================================================================
// JSON FILE CONVENIENCE FUNCTIONS
// ============================================================================

// ReadJSON reads JSON records from a file (one JSON object per line - JSONL format).
// This is useful for working with log files, data exports, and streaming JSON data.
//
// Returns error if file cannot be opened.
//
// Example:
//
//	// Read JSONL file
//	data, err := ssql.ReadJSON("events.jsonl")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Process events
//	for record := range data {
//	    eventType := ssql.GetOr(record, "type", "")
//	    timestamp := ssql.GetOr(record, "timestamp", "")
//	    fmt.Printf("%s: %s\n", timestamp, eventType)
//	}
func ReadJSON(filename string) (iter.Seq[Record], error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filename, err)
	}

	seq := func(yield func(Record) bool) {
		defer file.Close()

		// Use the io.Reader version. NOTE: this legacy reader parses
		// nested arrays/objects into structured values ([]any / map),
		// unlike ParseJSONLine's JSONString representation — swapping
		// in ReadJSONLFromReader here breaks that public contract
		// (TestJSONComplexTypesRoundTrip). The faster reader can only
		// land together with a nested-type decision; see TODO.
		for record := range ReadJSONFromReader(file) {
			if !yield(record) {
				return
			}
		}
	}

	return seq, nil
}

// ReadJSONSafe reads JSON with error handling
func ReadJSONSafe(filename string) iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		file, err := os.Open(filename)
		if err != nil {
			if !yield(Record{}, fmt.Errorf("failed to open file %s: %w", filename, err)) {
				return
			}
			return
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			var record Record
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				if !yield(Record{}, fmt.Errorf("invalid JSON on line %d: %w", lineNum, err)) {
					return
				}
				continue
			}

			// Add line metadata
			record = SetImmutable(record, "_line_number", int64(lineNum))
			lineNum++

			if !yield(record, nil) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(Record{}, fmt.Errorf("error reading file: %w", err))
		}
	}
}

// WriteJSON writes records as JSON (one object per line)
func WriteJSON(sb iter.Seq[Record], filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer file.Close()

	// Wrap in buffered writer for performance
	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Use the io.Writer version
	if err := WriteJSONToWriter(sb, writer); err != nil {
		return err
	}

	return writer.Flush()
}

// ============================================================================
// FAST JSON ENCODING (AVOIDS REFLECTION)
// ============================================================================

// ReadJSONLFromReader reads JSONL records from r. Counterpart to
// WriteJSONLWithInferredSchemaToWriter — reads the standard ssql
// wire format. If the first line is a `{"_schema":…}` header, it's
// parsed and used for per-field type coercion; otherwise records
// are read with per-record type inference.
//
// Unlike ReadJSONFromReader, this helper does NOT inject a synthetic
// `_line_number` field. Use it when consuming streams that have
// already been processed (SSH push-down output, generated-Go remote
// pipelines, JSONL written by `ssql ... | ssql to jsonl`).
func ReadJSONLFromReader(r io.Reader) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		br := bufio.NewReader(r)

		// Read first line; might be a {"_schema":…} header.
		firstLine, _ := br.ReadBytes('\n')
		var schema *Schema
		var firstIsHeader bool
		if len(bytes.TrimSpace(firstLine)) > 0 {
			schema, firstIsHeader = parseSchemaHeaderLine(firstLine)
		}

		// Build the iterator source: if first line was the schema
		// header, read the rest. Otherwise treat the first line as
		// a data record (re-prepend).
		var src io.Reader = br
		if !firstIsHeader && len(firstLine) > 0 {
			src = io.MultiReader(bytes.NewReader(firstLine), br)
		}

		scanner := bufio.NewScanner(src)
		// Records can be large (group-by results with -collect, etc.) —
		// match the bufio buffer the schema-aware lib reader uses.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var record Record
			var err error
			if schema != nil {
				record, err = ParseJSONLineWithSchema(line, schema)
			} else {
				var mut MutableRecord
				mut, err = ParseJSONLine(line)
				if err == nil {
					record = mut.Freeze()
				}
			}
			if err != nil {
				continue
			}
			if !yield(record) {
				return
			}
		}
	}
}

// parseSchemaHeaderLine returns (schema, true) if line is a
// `{"_schema":…}` header, or (nil, false) otherwise. The schema's
// fields/types are inferred from the header's `fields` and `types`
// maps. Errors return (nil, false) — callers treat the line as a
// data record in that case.
func parseSchemaHeaderLine(line []byte) (*Schema, bool) {
	var hdr struct {
		Schema struct {
			Fields []string          `json:"fields"`
			Types  map[string]string `json:"types"`
		} `json:"_schema"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(line), &hdr); err != nil {
		return nil, false
	}
	if len(hdr.Schema.Fields) == 0 {
		// Not a schema header (or an empty one — treat as data).
		return nil, false
	}
	return NewSchema(hdr.Schema.Fields), true
}

// ParseSchemaHeaderFields extracts the field list from a `_schema`
// header line (`{"_schema":{"fields":[...],...}}`). Second return is
// false when the line isn't a schema header. Exported for consumers of
// the schema-mode protocol (e.g. completion asking `from` for a
// file's fields).
func ParseSchemaHeaderFields(line []byte) ([]string, bool) {
	sch, ok := parseSchemaHeaderLine(line)
	if !ok {
		return nil, false
	}
	return sch.Fields(), true
}

// WriteJSONLWithInferredSchemaToWriter writes records as JSONL prefixed
// with a `{"_schema":{"fields":[...],"types":{...}}}` header inferred
// from the first record. Field order is alphabetical (deterministic).
//
// Wire format matches the standard ssql CLI output (the same header
// the multi-process pipeline produces between stages), so consumers
// reading the result can use the schema to preserve types.
//
// Used by `ssql generate go` as the JSONL fallback sink when a typed-
// mode pipeline has no explicit `to ...` stage. Particularly important
// for `ssql from ssh -remote`, where the local side reads the remote
// program's stdout and benefits from the schema header arriving from
// the remote rather than being inferred locally (which adds a spurious
// `_line_number` column).
//
// Empty input → no header is written (consistent with the rest of the
// JSONL writers). Errors mid-stream are returned immediately.
func WriteJSONLWithInferredSchemaToWriter(sb iter.Seq[Record], writer io.Writer) error {
	next, stop := iter.Pull(sb)
	defer stop()

	first, ok := next()
	if !ok {
		return nil
	}

	// Field list + types map from the first record, in the record's
	// own (schema) order — the header must carry field order across
	// wire hops, not scramble it (alphabetizing here cost column
	// order on every tee/from round-trip; found by the DFC108
	// cut-point equivalence gate).
	fields := make([]string, 0, 16)
	for k := range first.All() {
		fields = append(fields, k)
	}
	types := make(map[string]string, len(fields))
	for _, f := range fields {
		v, _ := Get[any](first, f)
		types[f] = inferJSONType(v)
	}

	// Emit the schema header. Using encoding/json directly keeps the
	// ssql package free of cross-package dependencies on cmd/ssql/lib.
	header := struct {
		Schema struct {
			Fields []string          `json:"fields"`
			Types  map[string]string `json:"types"`
		} `json:"_schema"`
	}{}
	header.Schema.Fields = fields
	header.Schema.Types = types
	hdr, err := json.Marshal(&header)
	if err != nil {
		return fmt.Errorf("marshaling _schema header: %w", err)
	}
	hdr = append(hdr, '\n')
	if _, err := writer.Write(hdr); err != nil {
		return err
	}

	// Write the first record, then the rest, using the same fast
	// encoding path WriteJSONFastToWriter uses.
	bufPtr := jsonBufferPool.Get().(*[]byte)
	buf := *bufPtr
	defer func() {
		*bufPtr = buf[:0]
		jsonBufferPool.Put(bufPtr)
	}()
	writeOne := func(r Record) error {
		buf = buf[:0]
		buf = r.AppendJSON(buf)
		buf = append(buf, '\n')
		_, err := writer.Write(buf)
		return err
	}
	if err := writeOne(first); err != nil {
		return err
	}
	for {
		r, ok := next()
		if !ok {
			return nil
		}
		if err := writeOne(r); err != nil {
			return err
		}
	}
}

// inferJSONType returns the schema type string for an interface value.
// Types match the existing CLI _schema header format ("string", "int",
// "float", "bool", "json"). Unknown types fall back to "string".
func inferJSONType(v any) string {
	switch v.(type) {
	case int64, int, int32:
		return "int"
	case float64, float32:
		return "float"
	case bool:
		return "bool"
	case string:
		return "string"
	case []any, Record, map[string]any:
		return "json"
	default:
		return "string"
	}
}

// WriteJSONFastToWriter writes records as JSONL using fast encoding (no reflection).
// This is 2-5x faster than WriteJSONToWriter for typical workloads.
// Uses pre-computed JSON field prefixes and direct buffer manipulation.
func WriteJSONFastToWriter(sb iter.Seq[Record], writer io.Writer) error {
	// Get a buffer from the pool
	bufPtr := jsonBufferPool.Get().(*[]byte)
	buf := *bufPtr
	defer func() {
		// Return buffer to pool (reset length, keep capacity)
		*bufPtr = buf[:0]
		jsonBufferPool.Put(bufPtr)
	}()

	for record := range sb {
		// Reset buffer for each record
		buf = buf[:0]

		// Encode record to buffer
		buf = record.AppendJSON(buf)
		buf = append(buf, '\n')

		// Write buffer to output
		if _, err := writer.Write(buf); err != nil {
			return fmt.Errorf("failed to write JSON record: %w", err)
		}
	}

	return nil
}

// WriteJSONFast writes records as JSONL to a file using fast encoding.
// This is 2-5x faster than WriteJSON for typical workloads.
func WriteJSONFast(sb iter.Seq[Record], filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer file.Close()

	// Wrap in buffered writer for performance
	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Use the fast writer
	if err := WriteJSONFastToWriter(sb, writer); err != nil {
		return err
	}

	return writer.Flush()
}

// ReadJSONFastFromReader reads JSONL from an io.Reader using fast parsing (no reflection).
// This is 3-5x faster than ReadJSONFromReader for typical workloads.
// Uses manual JSON parsing instead of encoding/json reflection-based unmarshaling.
// Caches and reuses schemas when consecutive records have the same fields.
func ReadJSONFastFromReader(reader io.Reader) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		scanner := bufio.NewScanner(reader)
		// Increase buffer size for long lines
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		lineNumber := int64(0)

		// Schema caching: reuse schema when fields match
		var cachedSchema *Schema
		var cachedFields []string // sorted field names of cached schema

		for scanner.Scan() {
			line := scanner.Bytes()

			// Skip empty lines
			isEmpty := true
			for _, c := range line {
				if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
					isEmpty = false
					break
				}
			}
			if isEmpty {
				lineNumber++
				continue
			}

			// Parse using fast parser
			mutableRecord, err := ParseJSONLine(line)
			if err != nil {
				// Skip invalid JSON lines (matching behavior of ReadJSONFromReader)
				lineNumber++
				continue
			}

			// Add line number metadata
			mutableRecord.fields["_line_number"] = lineNumber
			lineNumber++

			// Check if we can reuse the cached schema
			var record Record
			if cachedSchema != nil && len(mutableRecord.fields) == len(cachedFields) {
				// Check if fields match (quick check: same count, then verify all fields exist)
				allMatch := true
				for _, f := range cachedFields {
					if _, ok := mutableRecord.fields[f]; !ok {
						allMatch = false
						break
					}
				}
				if allMatch {
					// Reuse cached schema
					values := make([]any, cachedSchema.Width())
					for i, f := range cachedSchema.fields {
						values[i] = mutableRecord.fields[f]
					}
					record = Record{schema: cachedSchema, values: values}
				}
			}

			// If we couldn't reuse, create new schema and cache it
			if record.schema == nil {
				record = mutableRecord.Freeze()
				// Cache this schema for potential reuse
				cachedSchema = record.schema
				if cachedSchema != nil {
					cachedFields = cachedSchema.fields
				}
			}

			if !yield(record) {
				return
			}
		}
	}
}

// ReadJSONFastSafeFromReader reads JSONL with error handling using fast parsing.
// Caches and reuses schemas when consecutive records have the same fields.
func ReadJSONFastSafeFromReader(reader io.Reader) iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		scanner := bufio.NewScanner(reader)
		// Increase buffer size for long lines
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		lineNumber := int64(0)

		// Schema caching: reuse schema when fields match
		var cachedSchema *Schema
		var cachedFields []string

		for scanner.Scan() {
			line := scanner.Bytes()

			// Skip empty lines
			isEmpty := true
			for _, c := range line {
				if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
					isEmpty = false
					break
				}
			}
			if isEmpty {
				lineNumber++
				continue
			}

			// Parse using fast parser
			mutableRecord, err := ParseJSONLine(line)
			if err != nil {
				if !yield(Record{}, fmt.Errorf("failed to parse JSON on line %d: %w", lineNumber, err)) {
					return
				}
				lineNumber++
				continue
			}

			// Add line number metadata
			mutableRecord.fields["_line_number"] = lineNumber
			lineNumber++

			// Check if we can reuse the cached schema
			var record Record
			if cachedSchema != nil && len(mutableRecord.fields) == len(cachedFields) {
				allMatch := true
				for _, f := range cachedFields {
					if _, ok := mutableRecord.fields[f]; !ok {
						allMatch = false
						break
					}
				}
				if allMatch {
					values := make([]any, cachedSchema.Width())
					for i, f := range cachedSchema.fields {
						values[i] = mutableRecord.fields[f]
					}
					record = Record{schema: cachedSchema, values: values}
				}
			}

			if record.schema == nil {
				record = mutableRecord.Freeze()
				cachedSchema = record.schema
				if cachedSchema != nil {
					cachedFields = cachedSchema.fields
				}
			}

			if !yield(record, nil) {
				return
			}
		}

		// Check for scanner errors
		if err := scanner.Err(); err != nil {
			yield(Record{}, fmt.Errorf("error reading input: %w", err))
		}
	}
}

// ReadJSONFast reads JSONL from a file using fast parsing.
// This is 3-5x faster than ReadJSON for typical workloads.
func ReadJSONFast(filename string) (iter.Seq[Record], error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filename, err)
	}

	seq := func(yield func(Record) bool) {
		defer file.Close()

		// Use the fast reader
		for record := range ReadJSONFastFromReader(file) {
			if !yield(record) {
				return
			}
		}
	}

	return seq, nil
}

// ReadJSONFastSafe reads JSONL with error handling using fast parsing.
func ReadJSONFastSafe(filename string) iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		file, err := os.Open(filename)
		if err != nil {
			if !yield(Record{}, fmt.Errorf("failed to open file %s: %w", filename, err)) {
				return
			}
			return
		}
		defer file.Close()

		for record, err := range ReadJSONFastSafeFromReader(file) {
			if !yield(record, err) {
				return
			}
		}
	}
}

// ============================================================================
// TEXT LINE OPERATIONS
// ============================================================================

// ReadLines reads text lines from a file
// Returns error if file cannot be opened (Sources always return errors)
func ReadLines(filename string) (iter.Seq[Record], error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filename, err)
	}

	seq := func(yield func(Record) bool) {
		defer file.Close()

		for record := range ReadLinesFromReader(file) {
			if !yield(record) {
				return
			}
		}
	}

	return seq, nil
}

// ReadLinesFromReader yields one record per text line: line_number
// (1-based, like sed/awk/grep -n) and line. Records share one schema.
// Backs `ssql from lines`; pair with ExtractRecords to turn text into
// fields.
func ReadLinesFromReader(r io.Reader) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		schema := NewSchema([]string{"line_number", "line"})
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // long lines happen in logs
		var n int64
		for scanner.Scan() {
			n++
			if !yield(NewRecordFromSchema(schema, []any{n, scanner.Text()})) {
				return
			}
		}
	}
}

// ReadLinesSafe reads text lines with error handling
func ReadLinesSafe(filename string) iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		file, err := os.Open(filename)
		if err != nil {
			if !yield(Record{}, fmt.Errorf("failed to open file %s: %w", filename, err)) {
				return
			}
			return
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++ // 1-based, like sed/awk/grep -n
			record := NewRecord(map[string]any{
				"line":        scanner.Text(),
				"line_number": int64(lineNum),
			})

			if !yield(record, nil) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(Record{}, fmt.Errorf("error reading file: %w", err))
		}
	}
}

// WriteLines writes records as text lines (using "line" field)
func WriteLines(sb iter.Seq[Record], filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	for record := range sb {
		line := ""
		if value, exists := Get[any](record, "line"); exists {
			line = formatValue(value)
		}
		if _, err := writer.WriteString(line + "\n"); err != nil {
			return fmt.Errorf("failed to write line: %w", err)
		}
	}

	return nil
}

// ============================================================================
// UTILITY FUNCTIONS
// ============================================================================

// parseValue attempts to parse a string value into appropriate Go types
func parseValue(s string) any {
	s = strings.TrimSpace(s)

	// Try integer first (before boolean to avoid "1"/"0" being parsed as bool)
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}

	// Try float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}

	// Try boolean (only for explicit true/false, not 1/0)
	if s == "true" || s == "false" {
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
	}

	// Default to string
	return s
}

// formatValue converts a value to string for output. A missing value
// (nil slot — DFC124) renders as an empty cell, never "<nil>".
func formatValue(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case JSONString:
		return string(v) // For CSV, output the raw JSON string
	case int:
		return strconv.Itoa(v)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case []any:
		// Format slice as comma-separated values (for Collect aggregation results)
		var stringValues []string
		for _, val := range v {
			stringValues = append(stringValues, fmt.Sprintf("%v", val))
		}
		return strings.Join(stringValues, ",")
	default:
		// Check if it's an iter.Seq type and materialize it
		if isIterSeq(value) {
			return formatIterSeq(value)
		}
		return fmt.Sprintf("%v", v)
	}
}

// formatIterSeq materializes an iter.Seq and formats it as a comma-separated string
func formatIterSeq(value any) string {
	// Materialize the sequence using the existing function
	values := materializeSequence(value)

	// Convert to strings and join
	var stringValues []string
	for _, val := range values {
		stringValues = append(stringValues, fmt.Sprintf("%v", val))
	}

	return strings.Join(stringValues, ",")
}

// ============================================================================
// COMMAND OUTPUT OPERATIONS
// ============================================================================

// CommandConfig configures command output parsing
type CommandConfig struct {
	HasHeaders    bool   // Whether first line contains column headers
	TrimSpaces    bool   // Whether to trim leading/trailing spaces from fields
	SkipEmpty     bool   // Whether to skip empty lines
	HeaderPattern string // Optional regex pattern to identify header line
}

// DefaultCommandConfig provides sensible defaults for command output parsing
func DefaultCommandConfig() CommandConfig {
	return CommandConfig{
		HasHeaders: true,
		TrimSpaces: true,
		SkipEmpty:  true,
	}
}

// ReadCommandOutput reads command output with column-aligned data
// Returns error if file cannot be opened (Sources always return errors)
func ReadCommandOutput(filename string, config ...CommandConfig) (iter.Seq[Record], error) {
	cfg := DefaultCommandConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filename, err)
	}

	seq := func(yield func(Record) bool) {
		defer file.Close()

		scanner := bufio.NewScanner(file)

		var columnPositions []ColumnInfo
		lineNum := 0
		headerProcessed := false

		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines if configured
			if cfg.SkipEmpty && strings.TrimSpace(line) == "" {
				continue
			}

			// Process header line
			if cfg.HasHeaders && !headerProcessed {
				columnPositions = parseHeaderLine(line)
				headerProcessed = true
				lineNum++
				continue
			}

			// Parse data line
			record := parseDataLine(line, columnPositions, cfg.TrimSpaces)
			record = SetImmutable(record, "_line_number", int64(lineNum))
			record = SetImmutable(record, "_raw_line", line)

			lineNum++

			if !yield(record) {
				return
			}
		}
	}

	return seq, nil
}

// ReadCommandOutputSafe reads command output with error handling
func ReadCommandOutputSafe(filename string, config ...CommandConfig) iter.Seq2[Record, error] {
	cfg := DefaultCommandConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	return func(yield func(Record, error) bool) {
		file, err := os.Open(filename)
		if err != nil {
			if !yield(Record{}, fmt.Errorf("failed to open file %s: %w", filename, err)) {
				return
			}
			return
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)

		var columnPositions []ColumnInfo
		lineNum := 0
		headerProcessed := false

		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines if configured
			if cfg.SkipEmpty && strings.TrimSpace(line) == "" {
				continue
			}

			// Process header line
			if cfg.HasHeaders && !headerProcessed {
				columnPositions = parseHeaderLine(line)
				if len(columnPositions) == 0 {
					if !yield(Record{}, fmt.Errorf("failed to parse header line: %s", line)) {
						return
					}
					continue
				}
				headerProcessed = true
				lineNum++
				continue
			}

			// Parse data line
			record, parseErr := parseDataLineSafe(line, columnPositions, cfg.TrimSpaces)
			if parseErr != nil {
				if !yield(Record{}, fmt.Errorf("error parsing line %d: %w", lineNum, parseErr)) {
					return
				}
				continue
			}

			record = SetImmutable(record, "_line_number", int64(lineNum))
			record = SetImmutable(record, "_raw_line", line)

			lineNum++

			if !yield(record, nil) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(Record{}, fmt.Errorf("error reading file: %w", err))
		}
	}
}

// ============================================================================
// TABLE DISPLAY
// ============================================================================

// DisplayTable formats and prints records as a table to stdout.
// Columns are sorted alphabetically and sized to fit content.
// Long values are truncated with "..." if they exceed maxWidth.
//
// Example:
//
//	records := ssql.ReadCSV("data.csv")
//	ssql.DisplayTable(records, 50)
func DisplayTable(records iter.Seq[Record], maxWidth int) {
	DisplayTableWithFieldsTo(os.Stdout, records, maxWidth, nil, false)
}

// DisplayTableTo is like [DisplayTable] but writes to the given writer.
func DisplayTableTo(w io.Writer, records iter.Seq[Record], maxWidth int) {
	DisplayTableWithFieldsTo(w, records, maxWidth, nil, false)
}

// DisplayTableWithFields formats and prints records as a table with field ordering control.
// If fieldOrder is non-empty, those fields appear first in the specified order.
// If onlySpecified is true, only the fields in fieldOrder are displayed.
// Otherwise, remaining fields are appended in alphabetical order.
// Long values are truncated with "..." if they exceed maxWidth.
//
// Example:
//
//	records := ssql.ReadCSV("data.csv")
//	// Show name and age first, then other fields alphabetically
//	ssql.DisplayTableWithFields(records, 50, []string{"name", "age"}, false)
//	// Show only name and age
//	ssql.DisplayTableWithFields(records, 50, []string{"name", "age"}, true)
func DisplayTableWithFields(records iter.Seq[Record], maxWidth int, fieldOrder []string, onlySpecified bool) {
	DisplayTableWithFieldsTo(os.Stdout, records, maxWidth, fieldOrder, onlySpecified)
}

// resolveDisplayColumns builds the column list shared by the table and
// markdown writers: explicit fieldOrder first (existing fields only),
// then — unless onlySpecified — the remaining fields alphabetically;
// with no fieldOrder, the schema-declared natural order (alphabetical
// fallback).
func resolveDisplayColumns(columnSet map[string]bool, allRecords []Record, fieldOrder []string, onlySpecified bool) []string {
	var columns []string
	if len(fieldOrder) > 0 {
		specifiedSet := make(map[string]bool)
		for _, field := range fieldOrder {
			if columnSet[field] {
				columns = append(columns, field)
				specifiedSet[field] = true
			}
		}
		if !onlySpecified {
			var remaining []string
			for col := range columnSet {
				if !specifiedSet[col] {
					remaining = append(remaining, col)
				}
			}
			slices.Sort(remaining)
			columns = append(columns, remaining...)
		}
		return columns
	}
	return naturalColumnOrder(columnSet, allRecords)
}

// WriteMarkdownTo writes records as a GitHub-flavored Markdown table:
// a header row, an alignment row (numeric/bool columns right-aligned via
// `---:`), and one row per record. Pipes are escaped and newlines become
// <br> so cells can't break the table. Column selection/ordering follows
// the same rules as [DisplayTableWithFields].
func WriteMarkdownTo(w io.Writer, records iter.Seq[Record], fieldOrder []string, onlySpecified bool) error {
	var allRecords []Record
	columnSet := make(map[string]bool)
	for record := range records {
		allRecords = append(allRecords, record)
		for field := range record.All() {
			columnSet[field] = true
		}
	}
	if len(allRecords) == 0 {
		return nil
	}
	columns := resolveDisplayColumns(columnSet, allRecords, fieldOrder, onlySpecified)

	rightAlign := make(map[string]bool, len(columns))
	for _, col := range columns {
		rightAlign[col] = true
	}
	for _, record := range allRecords {
		for field, value := range record.All() {
			if !isNumericOrBoolValue(value) {
				rightAlign[field] = false
			}
		}
	}

	esc := func(s string) string {
		s = strings.ReplaceAll(s, "|", `\|`)
		s = strings.ReplaceAll(s, "\r\n", "<br>")
		s = strings.ReplaceAll(s, "\n", "<br>")
		return s
	}

	var b strings.Builder
	b.WriteString("|")
	for _, col := range columns {
		b.WriteString(" " + esc(col) + " |")
	}
	b.WriteString("\n|")
	for _, col := range columns {
		if rightAlign[col] {
			b.WriteString("---:|")
		} else {
			b.WriteString("---|")
		}
	}
	b.WriteString("\n")
	for _, record := range allRecords {
		values := make(map[string]any, len(columns))
		for field, value := range record.All() {
			values[field] = value
		}
		b.WriteString("|")
		for _, col := range columns {
			cell := ""
			if v, ok := values[col]; ok {
				cell = esc(fmt.Sprintf("%v", v))
			}
			b.WriteString(" " + cell + " |")
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// DisplayTableWithFieldsTo is like [DisplayTableWithFields] but writes to the given writer.
func DisplayTableWithFieldsTo(w io.Writer, records iter.Seq[Record], maxWidth int, fieldOrder []string, onlySpecified bool) {
	// Collect records and determine columns
	var allRecords []Record
	columnSet := make(map[string]bool)

	for record := range records {
		allRecords = append(allRecords, record)
		for field := range record.All() {
			columnSet[field] = true
		}
	}

	if len(allRecords) == 0 {
		return // No records to display
	}

	columns := resolveDisplayColumns(columnSet, allRecords, fieldOrder, onlySpecified)

	// Calculate max width for each column, and decide alignment:
	// columns whose values are all numeric/bool are right-justified;
	// any non-numeric value forces the column to left-justified.
	colWidths := make(map[string]int)
	colRightAlign := make(map[string]bool, len(columns))
	for _, col := range columns {
		colWidths[col] = len(col) // Start with header width
		colRightAlign[col] = true // assume right-align until proven otherwise
	}

	for _, record := range allRecords {
		for field, value := range record.All() {
			strValue := fmt.Sprintf("%v", value)
			if len(strValue) > colWidths[field] {
				colWidths[field] = min(len(strValue), maxWidth)
			}
			if !isNumericOrBoolValue(value) {
				colRightAlign[field] = false
			}
		}
	}

	// Print header
	for i, col := range columns {
		if i > 0 {
			fmt.Fprint(w, "   ")
		}
		fmt.Fprintf(w, "%-*s", colWidths[col], col)
	}
	fmt.Fprintln(w)

	// Print separator line
	totalWidth := 0
	for i, col := range columns {
		if i > 0 {
			totalWidth += 3 // separator spacing
		}
		totalWidth += colWidths[col]
	}
	fmt.Fprintln(w, strings.Repeat("-", totalWidth))

	// Print data rows
	for _, record := range allRecords {
		for i, col := range columns {
			if i > 0 {
				fmt.Fprint(w, "   ")
			}

			// Get value as any type
			var strValue string
			if value, exists := Get[any](record, col); exists {
				strValue = fmt.Sprintf("%v", value)
			} else {
				strValue = ""
			}

			// Truncate if too long
			if len(strValue) > maxWidth {
				strValue = strValue[:maxWidth-3] + "..."
			}

			if colRightAlign[col] {
				fmt.Fprintf(w, "%*s", colWidths[col], strValue)
			} else {
				fmt.Fprintf(w, "%-*s", colWidths[col], strValue)
			}
		}
		fmt.Fprintln(w)
	}
}

// isNumericOrBoolValue reports whether v is a numeric or boolean
// type — anything that should be right-justified in a table.
func isNumericOrBoolValue(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64,
		bool:
		return true
	}
	return false
}

// DisplayTableStreaming prints records as a formatted table in streaming mode.
// Samples the first sampleSize records to determine column widths, then streams
// remaining records without buffering. This uses O(sampleSize) memory instead of
// O(all records). Prints to stdout.
func DisplayTableStreaming(records iter.Seq[Record], maxWidth int, sampleSize int, fieldOrder []string, onlySpecified bool) {
	DisplayTableStreamingTo(os.Stdout, records, maxWidth, sampleSize, fieldOrder, onlySpecified)
}

// DisplayTableStreamingTo prints records as a formatted table in streaming mode,
// writing to the specified writer. Samples the first sampleSize records to determine
// column widths, then streams remaining records without buffering.
func DisplayTableStreamingTo(w io.Writer, records iter.Seq[Record], maxWidth int, sampleSize int, fieldOrder []string, onlySpecified bool) {
	if sampleSize <= 0 {
		sampleSize = 100
	}

	// Use iter.Pull for two-phase iteration
	next, stop := iter.Pull(records)
	defer stop()

	// Phase 1: sample records to determine columns and widths
	var sample []Record
	columnSet := make(map[string]bool)
	for range sampleSize {
		record, ok := next()
		if !ok {
			break
		}
		sample = append(sample, record)
		for field := range record.All() {
			columnSet[field] = true
		}
	}

	if len(sample) == 0 {
		return
	}

	// Build column list
	columns := buildColumnOrderWithSample(columnSet, fieldOrder, onlySpecified, sample)

	// Calculate column widths AND alignment from the sample. Note:
	// streaming mode commits to alignment after the sample — if a
	// later non-numeric value lands in a "right" column we just
	// keep right-aligning it (truncating widths is the same trade).
	colWidths, colRightAlign := calculateColumnWidthsAndAlignment(columns, sample, maxWidth)

	// Print header and separator
	printTableHeader(w, columns, colWidths)
	printTableSeparator(w, columns, colWidths)

	// Print sampled records
	for _, record := range sample {
		printTableRow(w, columns, colWidths, colRightAlign, record, maxWidth)
	}

	// Phase 2: stream remaining records
	for {
		record, ok := next()
		if !ok {
			break
		}
		printTableRow(w, columns, colWidths, colRightAlign, record, maxWidth)
	}
}

// buildColumnOrder builds an ordered column list from a set of discovered columns
func buildColumnOrder(columnSet map[string]bool, fieldOrder []string, onlySpecified bool) []string {
	return buildColumnOrderWithSample(columnSet, fieldOrder, onlySpecified, nil)
}

// buildColumnOrderWithSample is like [buildColumnOrder] but uses
// `sample` records' schema as the natural-order hint when no
// explicit field order is given. Each record's schema field list
// (the order in which the JSONL/CSV producer declared columns) is
// preferred over alphabetical sort.
func buildColumnOrderWithSample(columnSet map[string]bool, fieldOrder []string, onlySpecified bool, sample []Record) []string {
	var columns []string
	if len(fieldOrder) > 0 {
		specifiedSet := make(map[string]bool)
		for _, field := range fieldOrder {
			if columnSet[field] {
				columns = append(columns, field)
				specifiedSet[field] = true
			}
		}
		if !onlySpecified {
			var remaining []string
			for col := range columnSet {
				if !specifiedSet[col] {
					remaining = append(remaining, col)
				}
			}
			slices.Sort(remaining)
			columns = append(columns, remaining...)
		}
	} else {
		columns = naturalColumnOrder(columnSet, sample)
	}
	return columns
}

// naturalColumnOrder returns the columns in `columnSet` ordered by:
// (1) the first record's schema field order (if any), then
// (2) any remaining columns alphabetically.
//
// Records without a schema fall back to fully alphabetical, matching
// the historical behaviour of `DisplayTable`. Used by `to table`
// codegen so that Record-mode generated programs produce the same
// column order as the interactive CLI pipeline.
func naturalColumnOrder(columnSet map[string]bool, records []Record) []string {
	var columns []string
	seen := make(map[string]bool, len(columnSet))

	// Phase 1: schema-driven order from the first record that has one.
	for _, r := range records {
		sch := r.Schema()
		if sch == nil {
			continue
		}
		for _, f := range sch.Fields() {
			if columnSet[f] && !seen[f] {
				columns = append(columns, f)
				seen[f] = true
			}
		}
		break // first record's schema is enough; downstream commands share it
	}

	// Phase 2: any unseen columns alphabetically (typically empty).
	var extras []string
	for c := range columnSet {
		if !seen[c] {
			extras = append(extras, c)
		}
	}
	slices.Sort(extras)
	columns = append(columns, extras...)
	return columns
}

// calculateColumnWidths calculates the maximum display width for each column
func calculateColumnWidths(columns []string, records []Record, maxWidth int) map[string]int {
	colWidths := make(map[string]int)
	for _, col := range columns {
		colWidths[col] = len(col) // Start with header width
	}
	for _, record := range records {
		for field, value := range record.All() {
			strValue := fmt.Sprintf("%v", value)
			if len(strValue) > colWidths[field] {
				colWidths[field] = min(len(strValue), maxWidth)
			}
		}
	}
	return colWidths
}

// calculateColumnWidthsAndAlignment is like calculateColumnWidths but
// also decides per-column alignment. A column is right-aligned when
// every sampled value is numeric/bool; any non-numeric value in the
// sample forces it to left-aligned.
func calculateColumnWidthsAndAlignment(columns []string, records []Record, maxWidth int) (map[string]int, map[string]bool) {
	colWidths := make(map[string]int)
	colRight := make(map[string]bool, len(columns))
	for _, col := range columns {
		colWidths[col] = len(col)
		colRight[col] = true
	}
	for _, record := range records {
		for field, value := range record.All() {
			strValue := fmt.Sprintf("%v", value)
			if len(strValue) > colWidths[field] {
				colWidths[field] = min(len(strValue), maxWidth)
			}
			if !isNumericOrBoolValue(value) {
				colRight[field] = false
			}
		}
	}
	return colWidths, colRight
}

// printTableHeader prints the column headers
func printTableHeader(w io.Writer, columns []string, colWidths map[string]int) {
	for i, col := range columns {
		if i > 0 {
			fmt.Fprint(w, "   ")
		}
		fmt.Fprintf(w, "%-*s", colWidths[col], col)
	}
	fmt.Fprintln(w)
}

// printTableSeparator prints the separator line below the header
func printTableSeparator(w io.Writer, columns []string, colWidths map[string]int) {
	totalWidth := 0
	for i, col := range columns {
		if i > 0 {
			totalWidth += 3
		}
		totalWidth += colWidths[col]
	}
	fmt.Fprintln(w, strings.Repeat("-", totalWidth))
}

// printTableRow prints a single data row. colRight selects per-column
// alignment: right-justified when true (numeric/bool), left-justified
// when false.
func printTableRow(w io.Writer, columns []string, colWidths map[string]int, colRight map[string]bool, record Record, maxWidth int) {
	for i, col := range columns {
		if i > 0 {
			fmt.Fprint(w, "   ")
		}
		var strValue string
		if value, exists := Get[any](record, col); exists {
			strValue = fmt.Sprintf("%v", value)
		}
		if len(strValue) > maxWidth {
			strValue = strValue[:maxWidth-3] + "..."
		}
		if colRight[col] {
			fmt.Fprintf(w, "%*s", colWidths[col], strValue)
		} else {
			fmt.Fprintf(w, "%-*s", colWidths[col], strValue)
		}
	}
	fmt.Fprintln(w)
}

// ============================================================================
// COMMAND EXECUTION
// ============================================================================

// ExecCommand executes a command and returns its output as a stream of Records.
// Parses column-aligned output (like ps, df, ls -l) into structured data.
//
// Returns error if command cannot be started.
//
// Example:
//
//	// Get process information
//	data, err := ssql.ExecCommand("ps", []string{"-efl"})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Find processes using lots of memory
//	highMem := ssql.Where(func(r ssql.Record) bool {
//	    rss := ssql.GetOr(r, "RSS", int64(0))
//	    return rss > 100000  // More than 100MB
//	})(data)
//
//	// Count by user
//	byUser := ssql.Aggregate("procs", map[string]ssql.AggregateFunc{
//	    "count": ssql.Count(),
//	})(ssql.GroupByFields("procs", "UID")(data))
func ExecCommand(command string, args []string, config ...CommandConfig) (iter.Seq[Record], error) {
	cfg := DefaultCommandConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	cmd := exec.Command(command, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	seq := func(yield func(Record) bool) {
		defer func() { _ = cmd.Wait() }()

		scanner := bufio.NewScanner(stdout)

		var columnPositions []ColumnInfo
		lineNum := 0
		headerProcessed := false

		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines if configured
			if cfg.SkipEmpty && strings.TrimSpace(line) == "" {
				continue
			}

			// Process header line
			if cfg.HasHeaders && !headerProcessed {
				columnPositions = parseHeaderLine(line)
				headerProcessed = true
				lineNum++
				continue
			}

			// Parse data line
			record := parseDataLine(line, columnPositions, cfg.TrimSpaces)
			record = SetImmutable(record, "_line_number", int64(lineNum))
			record = SetImmutable(record, "_raw_line", line)
			record = SetImmutable(record, "_command", command)

			lineNum++

			if !yield(record) {
				return
			}
		}
	}

	return seq, nil
}

// ExecCommandSafe executes a command with error handling
func ExecCommandSafe(command string, args []string, config ...CommandConfig) iter.Seq2[Record, error] {
	cfg := DefaultCommandConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	return func(yield func(Record, error) bool) {
		cmd := exec.Command(command, args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			if !yield(Record{}, fmt.Errorf("failed to create stdout pipe: %w", err)) {
				return
			}
			return
		}

		if err := cmd.Start(); err != nil {
			if !yield(Record{}, fmt.Errorf("failed to start command: %w", err)) {
				return
			}
			return
		}
		defer func() {
			if waitErr := cmd.Wait(); waitErr != nil {
				yield(Record{}, fmt.Errorf("command failed: %w", waitErr))
			}
		}()

		scanner := bufio.NewScanner(stdout)

		var columnPositions []ColumnInfo
		lineNum := 0
		headerProcessed := false

		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines if configured
			if cfg.SkipEmpty && strings.TrimSpace(line) == "" {
				continue
			}

			// Process header line
			if cfg.HasHeaders && !headerProcessed {
				columnPositions = parseHeaderLine(line)
				if len(columnPositions) == 0 {
					if !yield(Record{}, fmt.Errorf("failed to parse header line: %s", line)) {
						return
					}
					continue
				}
				headerProcessed = true
				lineNum++
				continue
			}

			// Parse data line
			record, parseErr := parseDataLineSafe(line, columnPositions, cfg.TrimSpaces)
			if parseErr != nil {
				if !yield(Record{}, fmt.Errorf("error parsing line %d: %w", lineNum, parseErr)) {
					return
				}
				continue
			}

			record = SetImmutable(record, "_line_number", int64(lineNum))
			record = SetImmutable(record, "_raw_line", line)
			record = SetImmutable(record, "_command", command)

			lineNum++

			if !yield(record, nil) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(Record{}, fmt.Errorf("error reading command output: %w", err))
		}
	}
}

// ============================================================================
// COMMAND OUTPUT PARSING HELPERS
// ============================================================================

// ColumnInfo holds information about a column's position and name
type ColumnInfo struct {
	Name  string
	Start int
	End   int // -1 for last column (extends to end of line)
}

// parseHeaderLine extracts field names by splitting on whitespace
func parseHeaderLine(line string) []ColumnInfo {
	if strings.TrimSpace(line) == "" {
		return nil
	}

	// Split header on whitespace to get field names
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}

	// Create ColumnInfo entries (positions not used in new whitespace-based parsing)
	var columns []ColumnInfo
	for _, field := range fields {
		columns = append(columns, ColumnInfo{
			Name:  field,
			Start: -1, // Not used in whitespace-based parsing
			End:   -1, // Not used in whitespace-based parsing
		})
	}

	return columns
}

// parseDataLine extracts field values by splitting on whitespace
// Assigns 1:1 with header fields, with remaining tokens going to last field
func parseDataLine(line string, columns []ColumnInfo, _ bool) Record {
	record := MakeMutableRecord()

	if len(columns) == 0 {
		return record.Freeze()
	}

	// Split data line on whitespace
	tokens := strings.Fields(line)

	// Assign tokens to header fields
	for i, col := range columns {
		if i < len(tokens) {
			if i == len(columns)-1 {
				// Last field gets all remaining tokens joined with spaces
				remainingTokens := tokens[i:]
				value := strings.Join(remainingTokens, " ")
				record.fields[col.Name] = parseCommandValue(value)
			} else {
				// Regular field gets single token
				record.fields[col.Name] = parseCommandValue(tokens[i])
			}
		} else {
			// No more tokens, use empty string
			record.fields[col.Name] = ""
		}
	}

	return record.Freeze()
}

// parseDataLineSafe is like parseDataLine but returns errors
func parseDataLineSafe(line string, columns []ColumnInfo, _ bool) (Record, error) {
	if len(columns) == 0 {
		return Record{}, fmt.Errorf("no column information available")
	}

	record := MakeMutableRecord()

	// Split data line on whitespace
	tokens := strings.Fields(line)

	// Assign tokens to header fields
	for i, col := range columns {
		if i < len(tokens) {
			if i == len(columns)-1 {
				// Last field gets all remaining tokens joined with spaces
				remainingTokens := tokens[i:]
				value := strings.Join(remainingTokens, " ")
				record.fields[col.Name] = parseCommandValue(value)
			} else {
				// Regular field gets single token
				record.fields[col.Name] = parseCommandValue(tokens[i])
			}
		} else {
			// No more tokens, use empty string
			record.fields[col.Name] = ""
		}
	}

	return record.Freeze(), nil
}

// parseCommandValue parses command output values with integer priority over boolean
func parseCommandValue(s string) any {
	s = strings.TrimSpace(s)

	// Empty string
	if s == "" {
		return ""
	}

	// Try integer first (before boolean to avoid "1"/"0" being parsed as bool)
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}

	// Try float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}

	// Try boolean (only for explicit true/false, not 1/0)
	if s == "true" || s == "false" {
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
	}

	// Default to string
	return s
}

// ============================================================================
// CHANNEL OPERATIONS
// ============================================================================

// ToChannel converts an iterator to a channel
func ToChannel[T any](sb iter.Seq[T]) <-chan T {
	ch := make(chan T)
	go func() {
		defer close(ch)
		for item := range sb {
			ch <- item
		}
	}()
	return ch
}

// ToChannelWithErrors converts an error-aware iterator to channels
func ToChannelWithErrors[T any](sb iter.Seq2[T, error]) (<-chan T, <-chan error) {
	itemCh := make(chan T)
	errCh := make(chan error, 1)

	go func() {
		defer close(itemCh)
		defer close(errCh)

		for item, err := range sb {
			if err != nil {
				errCh <- err
				return
			}
			itemCh <- item
		}
	}()

	return itemCh, errCh
}

// FromChannelSafe creates an error-aware iterator from channels
func FromChannelSafe[T any](itemCh <-chan T, errCh <-chan error) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		itemChClosed := false
		errChClosed := false

		for {
			// If both channels are closed, we're done
			if itemChClosed && errChClosed {
				return
			}

			select {
			case item, ok := <-itemCh:
				if !ok {
					itemChClosed = true
					// Continue to drain error channel if it's still open
					continue
				}
				if !yield(item, nil) {
					return
				}
			case err, ok := <-errCh:
				if !ok {
					errChClosed = true
					// Continue to drain item channel if it's still open
					continue
				}
				var zero T
				if !yield(zero, err) {
					return
				}
			}
		}
	}
}

// ReadJSONAuto reads JSON from a file, auto-detecting JSON array vs JSONL format
func ReadJSONAuto(filename string) (iter.Seq[Record], error) {
	// Read all file content to detect format
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filename, err)
	}

	// Trim whitespace
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		// Return empty sequence
		return func(yield func(Record) bool) {}, nil
	}

	// Check if it starts with '[' (JSON array)
	if data[0] == '[' {
		// Parse as JSON array
		var records []map[string]any
		if err := json.Unmarshal(data, &records); err != nil {
			return nil, fmt.Errorf("failed to parse JSON array: %w", err)
		}

		return func(yield func(Record) bool) {
			for _, rec := range records {
				record := MakeMutableRecord()
				for k, v := range rec {
					record = addJSONField(record, k, v)
				}
				if !yield(record.Freeze()) {
					return
				}
			}
		}, nil
	}

	// Otherwise parse as JSONL
	return ReadJSON(filename)
}

// WriteJSONPretty writes records as a pretty-printed JSON array
func WriteJSONPretty(sb iter.Seq[Record], filename string) error {
	// Collect all records
	var recordMaps []map[string]any
	for record := range sb {
		data := make(map[string]any)
		for k, v := range record.All() {
			data[k] = convertRecordValueForJSON(v)
		}
		recordMaps = append(recordMaps, data)
	}

	// Marshal as pretty JSON
	jsonBytes, err := json.MarshalIndent(recordMaps, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	// Write to file
	if err := os.WriteFile(filename, append(jsonBytes, '\n'), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", filename, err)
	}

	return nil
}

// addJSONField adds a JSON field to a MutableRecord using type-safe methods
// Skips nil values. Converts arrays/objects to JSONString for type safety.
func addJSONField(record MutableRecord, key string, value any) MutableRecord {
	switch val := value.(type) {
	case float64:
		// JSON numbers are always float64
		// Check if it's actually an integer
		if val == float64(int64(val)) {
			return record.Int(key, int64(val))
		}
		return record.Float(key, val)
	case bool:
		return record.Bool(key, val)
	case string:
		return record.String(key, val)
	case nil:
		// Skip nil values - don't add field
		return record
	case []any, map[string]any:
		// Convert arrays/objects to JSONString for type safety
		jsonBytes, err := json.Marshal(val)
		if err != nil {
			// On error, store as string representation
			return record.String(key, fmt.Sprintf("%v", val))
		}
		return record.JSONString(key, JSONString(jsonBytes))
	default:
		// Convert unknown types to string
		return record.String(key, fmt.Sprintf("%v", val))
	}
}

// convertRecordValueForJSON converts ssql Record values to JSON-friendly types
func convertRecordValueForJSON(v any) any {
	switch val := v.(type) {
	case Record:
		// Convert nested Record to map
		result := make(map[string]any)
		for k, subv := range val.All() {
			result[k] = convertRecordValueForJSON(subv)
		}
		return result
	case int64, float64, bool, string, nil:
		return val
	default:
		return fmt.Sprintf("%v", v)
	}
}

// TeeFile writes every record to filename as schema-headed JSONL (the
// pipeline wire format) while passing it through unchanged — Unix tee
// for record streams: snapshot an intermediate result mid-pipeline.
// (Distinct from [Tee]/[LazyTee], which split a stream into copies.)
//
//	teed := ssql.TeeFile("checkpoint.jsonl")(records)
//
// fieldOrder, if given, sets the header's field order (otherwise
// alphabetical from the first record); value types are inferred from
// the first record. The file is created when iteration starts (empty
// input yields an empty file) and closed when the stream ends. Backs
// `ssql tee FILE`. Write errors terminate the process — silently losing
// a requested snapshot would be worse.
func TeeFile(filename string, fieldOrder ...string) Filter[Record, Record] {
	return func(records iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			die := func(err error) {
				fmt.Fprintf(os.Stderr, "ssql tee: %v\n", err)
				os.Exit(1)
			}
			f, err := os.Create(filename)
			if err != nil {
				die(err)
			}
			defer f.Close()
			w := bufio.NewWriter(f)
			defer func() {
				if err := w.Flush(); err != nil {
					die(err)
				}
			}()

			wroteHeader := false
			var buf []byte
			for r := range records {
				if !wroteHeader {
					wroteHeader = true
					fields := fieldOrder
					if len(fields) == 0 {
						// Record order, not alphabetical — the header
						// carries field order across wire hops.
						for k := range r.All() {
							fields = append(fields, k)
						}
					}
					types := make(map[string]string, len(fields))
					for _, fld := range fields {
						v, _ := Get[any](r, fld)
						types[fld] = inferJSONType(v)
					}
					header := struct {
						Schema struct {
							Fields []string          `json:"fields"`
							Types  map[string]string `json:"types"`
						} `json:"_schema"`
					}{}
					header.Schema.Fields = fields
					header.Schema.Types = types
					hdr, err := json.Marshal(&header)
					if err != nil {
						die(err)
					}
					hdr = append(hdr, '\n')
					if _, err := w.Write(hdr); err != nil {
						die(err)
					}
				}
				buf = buf[:0]
				buf = r.AppendJSON(buf)
				buf = append(buf, '\n')
				if _, err := w.Write(buf); err != nil {
					die(err)
				}
				if !yield(r) {
					return
				}
			}
		}
	}
}
