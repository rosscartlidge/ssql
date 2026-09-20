package lib

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"math"
	"os"
	"time"

	"github.com/rosscartlidge/ssql/v4"
)

// Stdout is a convenience variable for writing to stdout
var Stdout io.WriteCloser = os.Stdout

// ReadJSONL reads headerless JSONL (JSON Lines) from a reader — another
// tool's NDJSON export, or the output of a stage that wrote no `_schema`
// line. It goes through ssql.ReadJSONLFromReader, which caches the schema
// across records and does NOT inject the library's synthetic
// `_line_number` field: nothing in the CLI ever consumed it, and it showed
// up as a leading column on every DuckDB/Postgres export (DFC128 F2/D2).
// The library's ReadJSON* helpers keep their behaviour.
func ReadJSONL(r io.Reader) iter.Seq[ssql.Record] {
	return ssql.ReadJSONLFromReader(r)
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
func setValueFromJSON(record ssql.MutableRecord, key string, v any) ssql.MutableRecord {
	switch val := v.(type) {
	case nil:
		// A JSON null: the field exists, without a value (DFC128 §6g).
		return record.Null(key)
	case []any:
		// Convert array to []any for storage (preserves as proper slice, not JSONString)
		// This allows the array to be serialized back as a JSON array
		result := make([]any, len(val))
		for i, elem := range val {
			result[i] = elem
		}
		return ssql.Set(record, key, result)
	case map[string]any:
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

// convertRecordValue converts ssql Record values to JSON-friendly types
func convertRecordValue(v any) any {
	switch val := v.(type) {
	case ssql.Record:
		// Convert nested Record to map
		result := make(map[string]any)
		for k, subv := range val.All() {
			result[k] = convertRecordValue(subv)
		}
		return result
	case float64:
		// encoding/json refuses NaN and Infinity outright; JSON has
		// neither. No value, like the JSONL writer (DFC133).
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return nil
		}
		return val
	case int64, bool, string, nil:
		// Canonical types pass through
		return val
	case time.Time:
		// RFC 3339, as `to jsonl` writes it (DFC128 D1)
		return val.Format(time.RFC3339Nano)
	case []any:
		// Convert slice elements recursively (for Collect aggregation results)
		result := make([]any, len(val))
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
	return readJSONLSchemaAndRecords(r, nil)
}

// ReadJSONLWithSchemaSkipInvalid is ReadJSONLWithSchema for a source the
// user has declared dirty (`from jsonl FILE -skip-invalid`): lines that
// are not JSON are skipped and counted in *skipped.
func ReadJSONLWithSchemaSkipInvalid(r io.Reader, skipped *int64) *SchemaAndRecords {
	if skipped == nil {
		skipped = new(int64)
	}
	return readJSONLSchemaAndRecords(r, skipped)
}

// readJSONLSchemaAndRecords is every stage's stdin. A line that is not
// JSON panics with *ssql.LineError (main reports it as one Error line)
// unless skipped is non-nil. It used to SKIP such lines — and if the
// FIRST line was not JSON it returned nothing at all, so `cat data.json |
// ssql where …` on a JSON array produced an empty result and exit 0.
func readJSONLSchemaAndRecords(r io.Reader, skipped *int64) *SchemaAndRecords {
	br := bufio.NewReader(r)
	empty := &SchemaAndRecords{Records: func(yield func(ssql.Record) bool) {}}

	// Read first line to check for schema
	firstLine, err := br.ReadBytes('\n')
	if err != nil && len(bytes.TrimSpace(firstLine)) == 0 {
		return empty // no input at all
	}

	// A schema header is a JSON object; anything else is data for the
	// line reader, which decides what an invalid line means.
	var firstRecord map[string]any
	if json.Unmarshal(bytes.TrimSpace(firstLine), &firstRecord) == nil {
		if schema, ok := ParseSchemaHeader(firstRecord); ok {
			return &SchemaAndRecords{Schema: schema, Records: readJSONLWithSchema(br, schema, skipped)}
		}
	}

	// First line is data (or not JSON) - prepend it back and read everything
	combined := io.MultiReader(bytes.NewReader(firstLine), br)
	if skipped != nil {
		return &SchemaAndRecords{Records: ssql.ReadJSONLFromReaderSkipInvalid(combined, skipped)}
	}
	return &SchemaAndRecords{Records: ReadJSONL(combined)}
}

// readJSONLWithSchema reads the records AFTER a `_schema` header line,
// coercing by it. Shares a single ssql.Schema across all records.
func readJSONLWithSchema(r io.Reader, schema *Schema, skipped *int64) iter.Seq[ssql.Record] {
	return func(yield func(ssql.Record) bool) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), ssql.MaxJSONLineBytes)

		// Create shared ssql.Schema from lib.Schema fields, plus the
		// header's declared types so whole-number floats (JSON `2` under
		// a float column) come back as float64, not int64.
		var ssqlSchema *ssql.Schema
		var wireTypes []ssql.FieldType
		if schema != nil && len(schema.Fields) > 0 {
			ssqlSchema = ssql.NewSchema(schema.Fields)
			wireTypes = make([]ssql.FieldType, ssqlSchema.Width())
			for _, f := range schema.Fields {
				if ft, err := ssql.ParseFieldType(schema.Types[f]); err == nil {
					wireTypes[ssqlSchema.Index(f)] = ft
				}
			}
		}

		lineNo := int64(1) // the header was line 1
		for scanner.Scan() {
			lineNo++
			line := scanner.Bytes()
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var record ssql.Record
			var err error
			if ssqlSchema != nil {
				record, err = ssql.ParseJSONLineWithSchemaTypes(line, ssqlSchema, wireTypes)
			} else {
				var parsed ssql.MutableRecord
				if parsed, err = ssql.ParseJSONLineWithNulls(line); err == nil {
					record = parsed.Freeze()
				}
			}
			if err != nil {
				if skipped == nil {
					panic(ssql.NewLineError(lineNo, line, err))
				}
				*skipped++
				continue
			}
			if !yield(record) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			panic(ssql.NewLineError(lineNo+1, nil, err))
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
