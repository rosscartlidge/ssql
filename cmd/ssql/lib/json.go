package lib

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"iter"

	"github.com/rosscartlidge/ssql/v3"
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
		var rec map[string]interface{}
		if err := decoder.Decode(&rec); err != nil {
			continue // Skip malformed elements
		}

		// First valid record - infer field types
		if fieldTypes == nil {
			fieldTypes = make(map[string]ssql.FieldType)
			for k, v := range rec {
				fieldTypes[k] = inferJSONFieldType(v)
			}
		}

		// Build record with consistent types
		record := ssql.MakeMutableRecord()
		for k, v := range rec {
			if ft, ok := fieldTypes[k]; ok {
				record = setValueWithType(record, k, v, ft)
			} else {
				// New field - infer and lock its type
				fieldTypes[k] = inferJSONFieldType(v)
				record = setValueFromJSON(record, k, v)
			}
		}

		if !yield(record.Freeze()) {
			return // Early termination
		}
	}
}

// readJSONLines streams JSONL format line by line
// Field types are inferred from the first record and applied consistently.
func readJSONLines(r io.Reader, yield func(ssql.Record) bool) {
	scanner := bufio.NewScanner(r)

	// Increase buffer size for large lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024) // 1MB max token size

	// Track field types from first record for consistency
	var fieldTypes map[string]ssql.FieldType

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue // Skip empty lines
		}

		// Parse JSON object
		var rec map[string]interface{}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // Skip malformed lines
		}

		// First valid record - infer field types
		if fieldTypes == nil {
			fieldTypes = make(map[string]ssql.FieldType)
			for k, v := range rec {
				fieldTypes[k] = inferJSONFieldType(v)
			}
		}

		// Build record with consistent types
		record := ssql.MakeMutableRecord()
		for k, v := range rec {
			if ft, ok := fieldTypes[k]; ok {
				record = setValueWithType(record, k, v, ft)
			} else {
				// New field - infer and lock its type
				fieldTypes[k] = inferJSONFieldType(v)
				record = setValueFromJSON(record, k, v)
			}
		}

		if !yield(record.Freeze()) {
			return // Early termination
		}
	}
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
	var recordMaps []map[string]interface{}
	for record := range records {
		data := make(map[string]interface{})
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
