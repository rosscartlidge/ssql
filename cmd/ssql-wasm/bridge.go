//go:build js && wasm

package main

import (
	"bytes"
	"slices"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
)

// jsonToRecords parses a JSON array string into a slice of Records.
func jsonToRecords(jsonData string) ([]ssql.Record, error) {
	records := ssql.ReadJSONFastFromReader(strings.NewReader(jsonData))
	return slices.Collect(records), nil
}

// recordsToJSON converts a slice of Records to a JSON array string.
func recordsToJSON(records []ssql.Record) (string, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	first := true
	jsonBuf := make([]byte, 0, 4096)
	for _, r := range records {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		jsonBuf = jsonBuf[:0]
		jsonBuf = r.AppendJSON(jsonBuf)
		buf.Write(jsonBuf)
	}
	buf.WriteByte(']')
	return buf.String(), nil
}
