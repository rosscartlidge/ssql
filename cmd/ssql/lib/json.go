package lib

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"iter"

	"github.com/rosscartlidge/ssql/v4"
)

// ReadJSON reads JSON from a reader and returns an iterator of Records.
// Auto-detects JSON array format ([{...}, {...}]) vs JSONL ({...}\n{...}\n)
// Streams data to support early termination.
func ReadJSON(r io.Reader) iter.Seq[ssql.Record] {
	return func(yield func(ssql.Record) bool) {
		// Use buffered reader to peek at first non-whitespace byte
		br := bufio.NewReader(r)

		// Skip leading whitespace and peek at first character to detect format
		for {
			b, err := br.Peek(1)
			if err != nil {
				return // EOF or error
			}
			if b[0] == ' ' || b[0] == '\t' || b[0] == '\n' || b[0] == '\r' {
				br.ReadByte() // consume whitespace
				continue
			}
			break
		}

		firstByte, err := br.Peek(1)
		if err != nil {
			return
		}

		if firstByte[0] == '[' {
			// JSON array - use streaming decoder
			readJSONArray(br, yield)
		} else {
			// JSONL - use line-by-line streaming
			readJSONLines(br, yield)
		}
	}
}

// readJSONArray streams a JSON array using json.Decoder
// Field types are inferred from the first record and applied consistently.
func readJSONArray(r io.Reader, yield func(ssql.Record) bool) {
	decoder := json.NewDecoder(r)

	// Read opening bracket
	token, err := decoder.Token()
	if err != nil {
		return
	}
	if delim, ok := token.(json.Delim); !ok || delim != '[' {
		return // Not a JSON array
	}

	// Track field types from first record for consistency
	var fieldTypes map[string]ssql.FieldType

	// Read array elements
	for decoder.More() {
		var rec map[string]any
		if err := decoder.Decode(&rec); err != nil {
			continue // Skip malformed elements
		}

		if fieldTypes == nil {
			fieldTypes = make(map[string]ssql.FieldType)
		}

		// Build record with consistent types: a field's type is locked by
		// its first VALUE. A null says nothing about the type, so it never
		// locks one (a column NULL in the first element used to be locked
		// as a string) and never takes one (DFC128 §6g).
		record := ssql.MakeMutableRecord()
		for k, v := range rec {
			if v == nil {
				record = record.Null(k)
			} else if ft, ok := fieldTypes[k]; ok {
				record = setValueWithType(record, k, v, ft)
			} else {
				fieldTypes[k] = inferJSONFieldType(v)
				record = setValueFromJSON(record, k, v)
			}
		}

		if !yield(record.Freeze()) {
			return // Early termination
		}
	}
}

// readJSONLines streams JSONL through the wire-format reader — the one
// implementation (ssql.ReadJSONLFromReader): `_schema` headers honoured,
// one Schema shared across same-shaped records, nulls kept as nil slots.
// This was a third copy of the line loop and the schema cache, with a
// first-record type lock that coerced later values to the first row's
// type; header inference now widens instead (InferFromSample).
func readJSONLines(r io.Reader, yield func(ssql.Record) bool) {
	for record := range ssql.ReadJSONLFromReader(r) {
		if !yield(record) {
			return
		}
	}
}

// coerceValueToType converts a value to the target field type
func coerceValueToType(v any, ft ssql.FieldType) any {
	switch ft {
	case ssql.FieldTypeInt:
		switch val := v.(type) {
		case int64:
			return val
		case float64:
			return int64(val)
		}
	case ssql.FieldTypeFloat:
		switch val := v.(type) {
		case float64:
			return val
		case int64:
			return float64(val)
		}
	}
	return v
}

// WriteJSON writes Records as JSON.
// If pretty is true, writes as a pretty-printed JSON array.
// If pretty is false, writes as JSONL (one record per line).
func WriteJSON(w io.Writer, records iter.Seq[ssql.Record], pretty bool) error {
	if !pretty {
		// Write as JSONL
		return WriteJSONL(w, records)
	}

	// Collect all records into a slice
	var recordMaps []map[string]any
	for record := range records {
		data := make(map[string]any)
		for k, v := range record.All() {
			data[k] = convertRecordValue(v)
		}
		recordMaps = append(recordMaps, data)
	}

	// Marshal as pretty JSON array
	jsonBytes, err := json.MarshalIndent(recordMaps, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding records as JSON: %w", err)
	}

	if _, err := w.Write(jsonBytes); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}

	// Add final newline
	if _, err := w.Write([]byte("\n")); err != nil {
		return fmt.Errorf("writing newline: %w", err)
	}

	return nil
}

// WriteJSONWithFieldOrder writes Records as JSON with fields in specified order.
// If pretty is true, writes as a pretty-printed JSON array.
// If pretty is false, writes as JSONL (one record per line) with fields in order.
// If fieldOrder is nil or empty, fields are written in default (iteration) order.
func WriteJSONWithFieldOrder(w io.Writer, records iter.Seq[ssql.Record], pretty bool, fieldOrder []string) error {
	if !pretty {
		// Write as JSONL with field order
		return writeJSONLOrdered(w, records, fieldOrder)
	}

	// Collect all records into ordered format
	var recordMaps []map[string]any
	for record := range records {
		data := make(map[string]any)
		for k, v := range record.All() {
			data[k] = convertRecordValue(v)
		}
		recordMaps = append(recordMaps, data)
	}

	if len(recordMaps) == 0 {
		// Empty array
		if _, err := w.Write([]byte("[]\n")); err != nil {
			return fmt.Errorf("writing JSON: %w", err)
		}
		return nil
	}

	// For pretty output with field order, build ordered JSON manually
	if len(fieldOrder) > 0 {
		return writePrettyJSONOrdered(w, recordMaps, fieldOrder)
	}

	// No field order - use standard marshaling
	jsonBytes, err := json.MarshalIndent(recordMaps, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding records as JSON: %w", err)
	}

	if _, err := w.Write(jsonBytes); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}

	if _, err := w.Write([]byte("\n")); err != nil {
		return fmt.Errorf("writing newline: %w", err)
	}

	return nil
}

// writeJSONLOrdered writes JSONL with fields in specified order.
// Uses fast JSON encoding when no ordering is required.
func writeJSONLOrdered(w io.Writer, records iter.Seq[ssql.Record], fieldOrder []string) error {
	writer := bufio.NewWriter(w)
	defer writer.Flush()

	// Pre-allocate buffer for reuse across records
	buf := make([]byte, 0, 4096)

	for record := range records {
		buf = buf[:0] // Reset buffer, keep capacity

		if len(fieldOrder) > 0 {
			// Need custom ordering - use fast ordered encoding
			buf = record.AppendJSONOrdered(buf, fieldOrder)
		} else {
			// No ordering - use fast encoding
			buf = record.AppendJSON(buf)
		}

		buf = append(buf, '\n')
		if _, err := writer.Write(buf); err != nil {
			return err
		}
	}

	return writer.Flush()
}

// writePrettyJSONOrdered writes pretty JSON array with fields in specified order.
func writePrettyJSONOrdered(w io.Writer, recordMaps []map[string]any, fieldOrder []string) error {
	writer := bufio.NewWriter(w)
	defer writer.Flush()

	writer.WriteString("[\n")

	for i, data := range recordMaps {
		writer.WriteString("  {\n")

		first := true
		written := make(map[string]bool)

		// Write fields in order
		for _, field := range fieldOrder {
			if v, ok := data[field]; ok {
				if !first {
					writer.WriteString(",\n")
				}
				first = false
				written[field] = true

				keyBytes, _ := json.Marshal(field)
				valBytes, _ := json.Marshal(v)
				writer.WriteString("    ")
				writer.Write(keyBytes)
				writer.WriteString(": ")
				writer.Write(valBytes)
			}
		}

		// Write remaining fields not in order
		for k, v := range data {
			if written[k] {
				continue
			}
			if !first {
				writer.WriteString(",\n")
			}
			first = false

			keyBytes, _ := json.Marshal(k)
			valBytes, _ := json.Marshal(v)
			writer.WriteString("    ")
			writer.Write(keyBytes)
			writer.WriteString(": ")
			writer.Write(valBytes)
		}

		writer.WriteString("\n  }")
		if i < len(recordMaps)-1 {
			writer.WriteString(",")
		}
		writer.WriteString("\n")
	}

	writer.WriteString("]\n")
	return writer.Flush()
}
