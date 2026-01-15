package lib

import (
	"bufio"
	"bytes"
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
// Uses fast JSON parsing (avoids reflection) for better performance.
func ReadJSONL(r io.Reader) iter.Seq[ssql.Record] {
	return ssql.ReadJSONFastFromReader(r)
}

// WriteJSONL writes Records to a writer as JSONL (JSON Lines)
// Uses fast JSON encoding (avoids reflection) for better performance.
func WriteJSONL(w io.Writer, records iter.Seq[ssql.Record]) error {
	return ssql.WriteJSONFastToWriter(records, w)
}

// WriteJSONLRecord writes a single Record to a writer as JSONL (JSON Lines)
// Uses fast JSON encoding (avoids reflection) for better performance.
func WriteJSONLRecord(w io.Writer, record ssql.Record) error {
	// Use fast encoding
	buf := record.AppendJSON(nil)
	buf = append(buf, '\n')

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("writing JSON line: %w", err)
	}

	return nil
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
// Handles both json.Unmarshal types (float64 for all numbers) and fast parser types (int64/float64)
func inferJSONFieldType(value interface{}) ssql.FieldType {
	switch value.(type) {
	case int64:
		return ssql.FieldTypeInt // Fast parser returns integers as int64
	case float64:
		return ssql.FieldTypeFloat // JSON numbers or decimals
	case bool:
		return ssql.FieldTypeBool
	case string:
		return ssql.FieldTypeString
	case []interface{}, map[string]interface{}, ssql.Record, ssql.JSONString:
		return ssql.FieldTypeAuto // Preserve complex types as-is
	default:
		return ssql.FieldTypeAuto // Preserve unknown types as-is
	}
}

// setValueWithType sets a field on a MutableRecord, coercing to the target type
// Handles both json.Unmarshal types (float64 for all numbers) and fast parser types (int64/float64)
func setValueWithType(record ssql.MutableRecord, key string, v interface{}, targetType ssql.FieldType) ssql.MutableRecord {
	switch targetType {
	case ssql.FieldTypeFloat:
		switch val := v.(type) {
		case float64:
			return record.Float(key, val)
		case int64:
			return record.Float(key, float64(val))
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
		case int64:
			return record.Int(key, val)
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
		case int64:
			return record.Bool(key, val != 0)
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
		case int64:
			return record.String(key, strconv.FormatInt(val, 10))
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

// SchemaAndRecords holds the result of reading JSONL with an optional schema header.
type SchemaAndRecords struct {
	Schema  *Schema
	Records iter.Seq[ssql.Record]
}

// ReadJSONLWithSchema reads JSONL from a reader, detecting and extracting a schema header if present.
// If the first line is a schema header (contains "_schema" key), it is parsed and returned.
// Otherwise, returns nil schema and all records including the first line.
func ReadJSONLWithSchema(r io.Reader) *SchemaAndRecords {
	br := bufio.NewReader(r)

	// Read first line to check for schema
	firstLine, err := br.ReadBytes('\n')
	if err != nil && len(firstLine) == 0 {
		// Empty or error - return empty iterator
		return &SchemaAndRecords{
			Schema:  nil,
			Records: func(yield func(ssql.Record) bool) {},
		}
	}

	// Try to parse first line as JSON
	var firstRecord map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(firstLine), &firstRecord); err != nil {
		// Invalid JSON - return empty iterator
		return &SchemaAndRecords{
			Schema:  nil,
			Records: func(yield func(ssql.Record) bool) {},
		}
	}

	// Check if first line is a schema header
	if schema, ok := ParseSchemaHeader(firstRecord); ok {
		// Schema found - read remaining records with type coercion
		return &SchemaAndRecords{
			Schema:  schema,
			Records: readJSONLWithSchema(br, schema),
		}
	}

	// First line is data - prepend it back and read all records
	combined := io.MultiReader(bytes.NewReader(firstLine), br)
	return &SchemaAndRecords{
		Schema:  nil,
		Records: ReadJSONL(combined),
	}
}

// readJSONLWithSchema reads JSONL using schema for type coercion.
// Uses fast JSON parsing with schema-based type coercion.
func readJSONLWithSchema(r io.Reader, schema *Schema) iter.Seq[ssql.Record] {
	return func(yield func(ssql.Record) bool) {
		scanner := bufio.NewScanner(r)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			// Use fast parser
			parsed, err := ssql.ParseJSONLine(line)
			if err != nil {
				continue
			}

			// Apply schema type coercion if needed
			if schema != nil {
				record := ssql.MakeMutableRecord()
				for k, v := range parsed.Freeze().All() {
					if typ := schema.TypeOf(k); typ != "" {
						record = setValueWithType(record, k, v, SchemaTypeToFieldType(typ))
					} else {
						record = setValueFromJSON(record, k, v)
					}
				}
				if !yield(record.Freeze()) {
					return
				}
			} else {
				if !yield(parsed.Freeze()) {
					return
				}
			}
		}
	}
}

// WriteJSONLWithSchema writes a schema header (if provided) followed by records.
// If schema is nil, behaves like WriteJSONL (no schema header).
// Uses fast JSON encoding (avoids reflection) for better performance.
func WriteJSONLWithSchema(w io.Writer, schema *Schema, records iter.Seq[ssql.Record]) error {
	writer := bufio.NewWriter(w)
	defer writer.Flush()

	// Write schema header if provided
	if schema != nil {
		if err := schema.WriteHeader(writer); err != nil {
			return fmt.Errorf("writing schema header: %w", err)
		}
	}

	// Pre-allocate buffer for reuse across records
	buf := make([]byte, 0, 4096)

	// Write records using fast encoding
	for record := range records {
		buf = buf[:0] // Reset buffer, keep capacity
		buf = record.AppendJSON(buf)
		buf = append(buf, '\n')

		if _, err := writer.Write(buf); err != nil {
			return err
		}
	}

	return writer.Flush()
}

// WriteJSONLWithSchemaOrdered writes records with fields in schema order.
// If schema is nil, fields are written in iteration order (non-deterministic).
// Uses fast JSON encoding when no ordering is required.
func WriteJSONLWithSchemaOrdered(w io.Writer, schema *Schema, records iter.Seq[ssql.Record]) error {
	writer := bufio.NewWriter(w)
	defer writer.Flush()

	// Write schema header if provided
	if schema != nil {
		if err := schema.WriteHeader(writer); err != nil {
			return fmt.Errorf("writing schema header: %w", err)
		}
	}

	// Pre-allocate buffer for reuse across records
	buf := make([]byte, 0, 4096)

	// Write records with ordered fields
	for record := range records {
		buf = buf[:0] // Reset buffer, keep capacity

		if schema != nil && len(schema.Fields) > 0 {
			// Build ordered map using schema field order
			buf = record.AppendJSONOrdered(buf, schema.Fields)
		} else {
			// No schema - use fast encoding
			buf = record.AppendJSON(buf)
		}

		buf = append(buf, '\n')
		if _, err := writer.Write(buf); err != nil {
			return err
		}
	}

	return writer.Flush()
}

// marshalOrderedRecord marshals a record with fields in the specified order.
// Uses fast encoding via Record.AppendJSONOrdered for performance.
func marshalOrderedRecord(record ssql.Record, fieldOrder []string) ([]byte, error) {
	return record.AppendJSONOrdered(nil, fieldOrder), nil
}
