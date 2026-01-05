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
)

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

		// Process first row
		record := MakeMutableRecord()
		if cfg.HasHeaders && len(headers) > 0 {
			for i, value := range firstRow {
				if i < len(headers) {
					record.fields[headers[i]] = fieldParsers[i](value)
				}
			}
		} else {
			for i, value := range firstRow {
				record.fields[fmt.Sprintf("col_%d", i)] = fieldParsers[i](value)
			}
		}
		record.fields["_row_number"] = int64(0)
		if !yield(record.Freeze()) {
			return
		}

		// Process remaining rows with locked-in parsers
		rowIndex := int64(1)
		for {
			row, err := csvReader.Read()
			if err != nil {
				return // EOF or error
			}

			record := MakeMutableRecord()
			if cfg.HasHeaders && len(headers) > 0 {
				for i, value := range row {
					if i < len(headers) {
						record.fields[headers[i]] = fieldParsers[i](value)
					}
				}
			} else {
				for i, value := range row {
					if i < len(fieldParsers) {
						record.fields[fmt.Sprintf("col_%d", i)] = fieldParsers[i](value)
					}
				}
			}

			record.fields["_row_number"] = rowIndex
			rowIndex++

			if !yield(record.Freeze()) {
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

// parseAsInt parses as int64, returns 0 on error
func parseAsInt(s string) any {
	s = strings.TrimSpace(s)
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	// If it looks like a float, truncate it
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return int64(0)
}

// parseAsFloat parses as float64, returns 0.0 on error
func parseAsFloat(s string) any {
	s = strings.TrimSpace(s)
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return float64(0)
}

// parseAsBool parses as bool (true/false, 1/0, yes/no), returns false on error
func parseAsBool(s string) any {
	s = strings.TrimSpace(s)
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

		// Process first row
		record := MakeMutableRecord()
		if cfg.HasHeaders && len(headers) > 0 {
			for i, value := range firstRow {
				if i < len(headers) {
					record.fields[headers[i]] = fieldParsers[i](value)
				}
			}
		} else {
			for i, value := range firstRow {
				record.fields[fmt.Sprintf("col_%d", i)] = fieldParsers[i](value)
			}
		}
		record.fields["_row_number"] = int64(0)
		if !yield(record.Freeze(), nil) {
			return
		}

		// Process remaining rows with locked-in parsers
		rowIndex := int64(1)
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

			record := MakeMutableRecord()
			if cfg.HasHeaders && len(headers) > 0 {
				for i, value := range row {
					if i < len(headers) {
						record.fields[headers[i]] = fieldParsers[i](value)
					}
				}
			} else {
				for i, value := range row {
					if i < len(fieldParsers) {
						record.fields[fmt.Sprintf("col_%d", i)] = fieldParsers[i](value)
					}
				}
			}

			record.fields["_row_number"] = rowIndex
			rowIndex++

			if !yield(record.Freeze(), nil) {
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
			for field := range record.All() {
				// Skip complex fields (iter.Seq, Record) and internal metadata fields
				if val, exists := record.fields[field]; exists && !isIterSeq(val) {
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
			if value, exists := record.fields[field]; exists {
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

			var rawRecord Record
			if err := json.Unmarshal([]byte(line), &rawRecord); err != nil {
				// For simple API, skip invalid JSON lines
				lineNumber++
				continue
			}

			// First valid record - infer field types
			if fieldTypes == nil {
				fieldTypes = make(map[string]FieldType)
				for key, value := range rawRecord.fields {
					fieldTypes[key] = inferJSONFieldType(value)
				}
			}

			// Coerce fields to consistent types
			record := MakeMutableRecord()
			for key, value := range rawRecord.fields {
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
				for key, value := range rawRecord.fields {
					fieldTypes[key] = inferJSONFieldType(value)
				}
			}

			// Coerce fields to consistent types
			record := MakeMutableRecord()
			for key, value := range rawRecord.fields {
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

		// Use the io.Reader version
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
			record.fields["_line_number"] = int64(lineNum)
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

		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			record := Record{fields: map[string]any{
				"line":        scanner.Text(),
				"line_number": int64(lineNum),
			}}
			lineNum++

			if !yield(record) {
				return
			}
		}
	}

	return seq, nil
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
			record := Record{fields: map[string]any{
				"line":        scanner.Text(),
				"line_number": int64(lineNum),
			}}
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
		if value, exists := record.fields["line"]; exists {
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

// formatValue converts a value to string for output
func formatValue(value any) string {
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
			record.fields["_line_number"] = int64(lineNum)
			record.fields["_raw_line"] = line

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

			record.fields["_line_number"] = int64(lineNum)
			record.fields["_raw_line"] = line

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
	DisplayTableWithFields(records, maxWidth, nil, false)
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

	// Build column list based on field ordering options
	var columns []string
	if len(fieldOrder) > 0 {
		// Add specified fields first (in order), only if they exist in data
		specifiedSet := make(map[string]bool)
		for _, field := range fieldOrder {
			if columnSet[field] {
				columns = append(columns, field)
				specifiedSet[field] = true
			}
		}

		// Add remaining fields alphabetically (unless onlySpecified)
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
		// No field order specified - sort alphabetically (original behavior)
		columns = make([]string, 0, len(columnSet))
		for col := range columnSet {
			columns = append(columns, col)
		}
		slices.Sort(columns)
	}

	// Calculate max width for each column
	colWidths := make(map[string]int)
	for _, col := range columns {
		colWidths[col] = len(col) // Start with header width
	}

	for _, record := range allRecords {
		for field, value := range record.All() {
			strValue := fmt.Sprintf("%v", value)
			if len(strValue) > colWidths[field] {
				if len(strValue) > maxWidth {
					colWidths[field] = maxWidth
				} else {
					colWidths[field] = len(strValue)
				}
			}
		}
	}

	// Print header
	for i, col := range columns {
		if i > 0 {
			fmt.Print("   ")
		}
		fmt.Printf("%-*s", colWidths[col], col)
	}
	fmt.Println()

	// Print separator line
	totalWidth := 0
	for i, col := range columns {
		if i > 0 {
			totalWidth += 3 // separator spacing
		}
		totalWidth += colWidths[col]
	}
	fmt.Println(strings.Repeat("-", totalWidth))

	// Print data rows
	for _, record := range allRecords {
		for i, col := range columns {
			if i > 0 {
				fmt.Print("   ")
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

			fmt.Printf("%-*s", colWidths[col], strValue)
		}
		fmt.Println()
	}
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
			record.fields["_line_number"] = int64(lineNum)
			record.fields["_raw_line"] = line
			record.fields["_command"] = command

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

			record.fields["_line_number"] = int64(lineNum)
			record.fields["_raw_line"] = line
			record.fields["_command"] = command

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
		var records []map[string]interface{}
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
	var recordMaps []map[string]interface{}
	for record := range sb {
		data := make(map[string]interface{})
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
func addJSONField(record MutableRecord, key string, value interface{}) MutableRecord {
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
	case []interface{}, map[string]interface{}:
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
func convertRecordValueForJSON(v interface{}) interface{} {
	switch val := v.(type) {
	case Record:
		// Convert nested Record to map
		result := make(map[string]interface{})
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
