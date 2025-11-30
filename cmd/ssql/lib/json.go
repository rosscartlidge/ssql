package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"iter"

	"github.com/rosscartlidge/ssql/v3"
)

// ReadJSON reads JSON from a reader and returns an iterator of Records.
// Auto-detects JSON array format ([{...}, {...}]) vs JSONL ({...}\n{...}\n)
func ReadJSON(r io.Reader) iter.Seq[ssql.Record] {
	return func(yield func(ssql.Record) bool) {
		// Read all input to detect format
		data, err := io.ReadAll(r)
		if err != nil {
			return // Fail silently in streaming context
		}

		// Trim whitespace
		data = bytes.TrimSpace(data)
		if len(data) == 0 {
			return
		}

		// Check if it starts with '[' (JSON array)
		if data[0] == '[' {
			// Parse as JSON array
			var records []map[string]interface{}
			if err := json.Unmarshal(data, &records); err != nil {
				return // Fail silently
			}

			for _, rec := range records {
				record := ssql.MakeMutableRecord()
				for k, v := range rec {
					record = setValueFromJSON(record, k, v)
				}

				if !yield(record.Freeze()) {
					return
				}
			}
		} else {
			// Parse as JSONL (line by line)
			lines := bytes.Split(data, []byte("\n"))
			for _, line := range lines {
				line = bytes.TrimSpace(line)
				if len(line) == 0 {
					continue
				}

				var rec map[string]interface{}
				if err := json.Unmarshal(line, &rec); err != nil {
					continue // Skip malformed lines
				}

				record := ssql.MakeMutableRecord()
				for k, v := range rec {
					record = setValueFromJSON(record, k, v)
				}

				if !yield(record.Freeze()) {
					return
				}
			}
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
