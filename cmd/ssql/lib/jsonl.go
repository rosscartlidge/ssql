package lib

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"strconv"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
)

// Stdout is a convenience variable for writing to stdout
var Stdout io.WriteCloser = os.Stdout

// ReadJSONL reads JSONL (JSON Lines) from a reader and returns an iterator of Records
func ReadJSONL(r io.Reader) iter.Seq[ssql.Record] {
	return func(yield func(ssql.Record) bool) {
		scanner := bufio.NewScanner(r)

		// Increase buffer size for large lines
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024) // 1MB max token size

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue // Skip empty lines
			}

			// Parse JSON object
			var data map[string]interface{}
			if err := json.Unmarshal(line, &data); err != nil {
				// Skip malformed lines silently in streaming context
				continue
			}

			// Convert to Record directly (not using TypedRecord builder)
			record := ssql.MakeMutableRecord()
			for k, v := range data {
				record = setValueFromJSON(record, k, v)
			}

			if !yield(record.Freeze()) {
				return
			}
		}
	}
}

// WriteJSONL writes Records to a writer as JSONL (JSON Lines)
func WriteJSONL(w io.Writer, records iter.Seq[ssql.Record]) error {
	writer := bufio.NewWriter(w)
	defer writer.Flush()

	for record := range records {
		// Convert Record to map for JSON encoding
		data := make(map[string]interface{})

		// Extract all fields from record
		for k, v := range record.All() {
			data[k] = convertRecordValue(v)
		}

		// Encode as JSON
		jsonBytes, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("encoding record as JSON: %w", err)
		}

		// Write line
		if _, err := writer.Write(jsonBytes); err != nil {
			return fmt.Errorf("writing JSON line: %w", err)
		}
		if _, err := writer.Write([]byte("\n")); err != nil {
			return fmt.Errorf("writing newline: %w", err)
		}
	}

	return writer.Flush()
}

// OpenInputFile opens a file for input (not stdin - use os.Stdin directly for that)
func OpenInputFile(filename string) (io.ReadCloser, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening file %s: %w", filename, err)
	}
	return file, nil
}

// OpenOutput opens an output destination (file or stdout)
func OpenOutput(filename string) (io.WriteCloser, error) {
	if filename == "" || filename == "-" {
		return os.Stdout, nil
	}

	file, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("creating file %s: %w", filename, err)
	}
	return file, nil
}

// setValueFromJSON sets a field on a MutableRecord from a JSON value
// Handles JSON-specific type conversions (nil, arrays, nested objects, numbers, bools, strings)
func setValueFromJSON(record ssql.MutableRecord, key string, v interface{}) ssql.MutableRecord {
	switch val := v.(type) {
	case nil:
		// Skip nil values - don't set the field
		return record
	case []interface{}:
		// Convert array to []any for storage (preserves as proper slice, not JSONString)
		// This allows the array to be serialized back as a JSON array
		result := make([]any, len(val))
		for i, elem := range val {
			result[i] = elem
		}
		return ssql.Set(record, key, result)
	case map[string]interface{}:
		// Nested object - convert to Record recursively
		nested := ssql.MakeMutableRecord()
		for k, subv := range val {
			nested = setValueFromJSON(nested, k, subv)
		}
		return ssql.Set(record, key, nested.Freeze())
	case float64:
		// JSON numbers are always float64 - check if it's actually an integer
		if val == float64(int64(val)) {
			return record.Int(key, int64(val))
		}
		return record.Float(key, val)
	case bool:
		return record.Bool(key, val)
	case string:
		return record.String(key, val)
	default:
		// Unknown type (shouldn't happen with valid JSON) - convert to string
		return record.String(key, fmt.Sprintf("%v", v))
	}
}

// inferJSONFieldType determines the FieldType from a JSON-parsed value
func inferJSONFieldType(value interface{}) ssql.FieldType {
	switch value.(type) {
	case float64:
		return ssql.FieldTypeFloat // JSON numbers are always float64
	case bool:
		return ssql.FieldTypeBool
	case string:
		return ssql.FieldTypeString
	case []interface{}, map[string]interface{}, ssql.Record:
		return ssql.FieldTypeAuto // Preserve complex types as-is
	default:
		return ssql.FieldTypeAuto // Preserve unknown types as-is
	}
}

// setValueWithType sets a field on a MutableRecord, coercing to the target type
func setValueWithType(record ssql.MutableRecord, key string, v interface{}, targetType ssql.FieldType) ssql.MutableRecord {
	switch targetType {
	case ssql.FieldTypeFloat:
		switch val := v.(type) {
		case float64:
			return record.Float(key, val)
		case bool:
			if val {
				return record.Float(key, 1)
			}
			return record.Float(key, 0)
		case string:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				return record.Float(key, f)
			}
			return record.Float(key, 0)
		default:
			return record.Float(key, 0)
		}
	case ssql.FieldTypeInt:
		switch val := v.(type) {
		case float64:
			return record.Int(key, int64(val))
		case bool:
			if val {
				return record.Int(key, 1)
			}
			return record.Int(key, 0)
		case string:
			if i, err := strconv.ParseInt(val, 10, 64); err == nil {
				return record.Int(key, i)
			}
			return record.Int(key, 0)
		default:
			return record.Int(key, 0)
		}
	case ssql.FieldTypeBool:
		switch val := v.(type) {
		case bool:
			return record.Bool(key, val)
		case float64:
			return record.Bool(key, val != 0)
		case string:
			switch strings.ToLower(val) {
			case "true", "1", "yes", "y", "on":
				return record.Bool(key, true)
			default:
				return record.Bool(key, false)
			}
		default:
			return record.Bool(key, false)
		}
	case ssql.FieldTypeString:
		switch val := v.(type) {
		case string:
			return record.String(key, val)
		case float64:
			return record.String(key, strconv.FormatFloat(val, 'g', -1, 64))
		case bool:
			return record.String(key, strconv.FormatBool(val))
		default:
			return record.String(key, fmt.Sprintf("%v", v))
		}
	default:
		return setValueFromJSON(record, key, v)
	}
}

// convertRecordValue converts ssql Record values to JSON-friendly types
func convertRecordValue(v interface{}) interface{} {
	switch val := v.(type) {
	case ssql.Record:
		// Convert nested Record to map
		result := make(map[string]interface{})
		for k, subv := range val.All() {
			result[k] = convertRecordValue(subv)
		}
		return result
	case int64, float64, bool, string, nil:
		// Canonical types pass through
		return val
	case []any:
		// Convert slice elements recursively (for Collect aggregation results)
		result := make([]interface{}, len(val))
		for i, elem := range val {
			result[i] = convertRecordValue(elem)
		}
		return result
	default:
		// For sequences and other types, try to convert to simple representation
		return fmt.Sprintf("%v", v)
	}
}
