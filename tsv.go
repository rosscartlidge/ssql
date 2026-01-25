package ssql

import (
	"bufio"
	"fmt"
	"io"
	"iter"
	"os"
	"strconv"
	"strings"
)

// DetectTSVSeparator detects the field separator from a TSV header line.
// It returns the first character that cannot be part of a valid identifier
// (letters, digits, underscore, not starting with digit).
// If no separator is found, returns tab as default.
func DetectTSVSeparator(header string) rune {
	for i, r := range header {
		if !isIdentifierChar(r, i == 0) {
			return r
		}
	}
	return '\t' // default to tab if no separator found
}

// isIdentifierChar returns true if r is valid in an identifier at the given position.
// First character cannot be a digit.
func isIdentifierChar(r rune, isFirst bool) bool {
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	if r == '_' {
		return true
	}
	if !isFirst && r >= '0' && r <= '9' {
		return true
	}
	return false
}

// ReadTSV reads records from a TSV file.
// The separator is auto-detected from the header line.
func ReadTSV(filename string) (iter.Seq[Record], error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	return ReadTSVFromReader(file), nil
}

// ReadTSVFromReader reads TSV records from an io.Reader.
// The separator is auto-detected from the header line.
func ReadTSVFromReader(r io.Reader) iter.Seq[Record] {
	return ReadTSVFromReaderWithSeparator(r, 0) // 0 means auto-detect
}

// ReadTSVFromReaderWithSeparator reads TSV records with a specific separator.
// If sep is 0, the separator is auto-detected from the header line.
func ReadTSVFromReaderWithSeparator(r io.Reader, sep rune) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		// Handle close if reader is a closer
		if closer, ok := r.(io.Closer); ok {
			defer closer.Close()
		}

		scanner := bufio.NewScanner(r)

		// Read header line
		if !scanner.Scan() {
			return
		}
		header := scanner.Text()

		// Auto-detect separator if not specified
		if sep == 0 {
			sep = DetectTSVSeparator(header)
		}
		sepStr := string(sep)

		// Parse field names
		fields := strings.Split(header, sepStr)

		// Create shared schema for all records
		schema := NewSchema(fields)
		fieldIndices := make([]int, len(fields))
		for i, f := range fields {
			fieldIndices[i] = schema.Index(f)
		}

		// Read data lines
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			parts := strings.Split(line, sepStr)
			values := make([]any, schema.Width())

			for i, part := range parts {
				if i >= len(fieldIndices) {
					break
				}
				values[fieldIndices[i]] = parseTSVValue(part)
			}

			if !yield(NewRecordFromSchema(schema, values)) {
				return
			}
		}
	}
}

// parseTSVValue parses a TSV field value, converting to appropriate types.
func parseTSVValue(s string) any {
	// Try integer first
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	// Try float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	// Try boolean
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	// Return as string
	return s
}

// WriteTSV writes records to a TSV file with tab separator.
func WriteTSV(records iter.Seq[Record], filename string) error {
	return WriteTSVWithSeparator(records, filename, '\t')
}

// WriteTSVWithSeparator writes records to a TSV file with a custom separator.
func WriteTSVWithSeparator(records iter.Seq[Record], filename string, sep rune) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	return WriteTSVToWriterWithSeparator(records, file, sep)
}

// WriteTSVToWriter writes records as TSV to an io.Writer with tab separator.
func WriteTSVToWriter(records iter.Seq[Record], w io.Writer) error {
	return WriteTSVToWriterWithSeparator(records, w, '\t')
}

// WriteTSVToWriterWithSeparator writes records as TSV to an io.Writer with a custom separator.
func WriteTSVToWriterWithSeparator(records iter.Seq[Record], w io.Writer, sep rune) error {
	buf := bufio.NewWriter(w)
	defer buf.Flush()

	sepStr := string(sep)
	var fields []string
	headerWritten := false

	for record := range records {
		if !headerWritten {
			// Get fields from first record and write header
			fields = record.Keys()
			if _, err := buf.WriteString(strings.Join(fields, sepStr)); err != nil {
				return err
			}
			if err := buf.WriteByte('\n'); err != nil {
				return err
			}
			headerWritten = true
		}

		// Write values in field order
		for i, field := range fields {
			if i > 0 {
				if _, err := buf.WriteString(sepStr); err != nil {
					return err
				}
			}
			val, _ := Get[any](record, field)
			if _, err := buf.WriteString(formatTSVValue(val)); err != nil {
				return err
			}
		}
		if err := buf.WriteByte('\n'); err != nil {
			return err
		}
	}

	return nil
}

// formatTSVValue formats a value for TSV output.
func formatTSVValue(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		// Use minimal precision that preserves the value
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ExtractFieldsFromTSV reads the header of a TSV file and returns the field names.
func ExtractFieldsFromTSV(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty TSV file")
	}

	header := scanner.Text()
	sep := DetectTSVSeparator(header)
	return strings.Split(header, string(sep)), nil
}
