package ssql

import (
	"bufio"
	"errors"
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

// ReadTSV reads records from a TSV file. The separator is auto-detected
// from the header line; column types are sampled as for [ReadCSV].
func ReadTSV(filename string) (iter.Seq[Record], error) {
	return ReadTSVWithConfig(filename, DefaultTSVConfig())
}

// ReadTSVWithConfig is [ReadTSV] with explicit column types
// (CSVConfig.TypeOverrides / DefaultType / InferRows). A Delimiter of 0
// means auto-detect from the header, the TSV default.
func ReadTSVWithConfig(filename string, cfg CSVConfig) (iter.Seq[Record], error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	return ReadTSVFromReaderWithConfig(file, cfg), nil
}

// DefaultTSVConfig is [DefaultCSVConfig] with the delimiter left to
// header auto-detection (Delimiter 0).
func DefaultTSVConfig() CSVConfig {
	cfg := DefaultCSVConfig()
	cfg.Delimiter = 0
	return cfg
}

// ReadTSVFromReader reads TSV records from an io.Reader.
// The separator is auto-detected from the header line.
func ReadTSVFromReader(r io.Reader) iter.Seq[Record] {
	return ReadTSVFromReaderWithConfig(r, DefaultTSVConfig())
}

// ReadTSVFromReaderWithSeparator reads TSV records with a specific separator.
// If sep is 0, the separator is auto-detected from the header line.
func ReadTSVFromReaderWithSeparator(r io.Reader, sep rune) iter.Seq[Record] {
	cfg := DefaultTSVConfig()
	cfg.Delimiter = sep
	return ReadTSVFromReaderWithConfig(r, cfg)
}

// ReadTSVFromReaderWithConfig reads delimited text without quoting
// rules (a field is everything between separators) and types columns
// exactly as [ReadCSVFromReader] does: TypeOverrides / DefaultType when
// set, else inferred from the first InferRows data rows; a later cell
// that does not fit is a *CellError and this reader PANICS with it (the
// same fail-fast contract as the CSV reader; there is no coerced zero
// and no silently mixed column). Empty cells are absent for non-string
// columns (DFC124). cfg.Delimiter 0 = auto-detect from the header (the
// first non-identifier rune, default tab); blank lines are skipped. If
// r is an io.Closer it is closed when the sequence ends.
func ReadTSVFromReaderWithConfig(r io.Reader, cfg CSVConfig) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		if closer, ok := r.(io.Closer); ok {
			defer closer.Close()
		}
		tr := &tsvRowReader{sc: bufio.NewScanner(r), sep: cfg.Delimiter}
		tr.sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		readRows(tr, cfg, func(rec Record, err error) bool {
			if err != nil {
				var ce *CellError
				if errors.As(err, &ce) {
					panic(ce)
				}
				return false
			}
			return yield(rec)
		})
	}
}

// tsvRowReader is the [rowReader] for delimited text: the header line
// fixes the separator (auto-detected when sep is 0), data lines are
// split on it with no quote handling, blank lines are skipped. The row
// slice is reused between calls.
type tsvRowReader struct {
	sc         *bufio.Scanner
	sep        rune
	headerDone bool
	row        []string
}

func (t *tsvRowReader) Read() ([]string, error) {
	for t.sc.Scan() {
		line := t.sc.Text()
		if !t.headerDone {
			t.headerDone = true
			if t.sep == 0 {
				t.sep = DetectTSVSeparator(line)
			}
			return strings.Split(line, string(t.sep)), nil
		}
		if line == "" {
			continue
		}
		t.row = t.row[:0]
		sep := string(t.sep)
		for {
			i := strings.Index(line, sep)
			if i < 0 {
				t.row = append(t.row, line)
				break
			}
			t.row = append(t.row, line[:i])
			line = line[i+len(sep):]
		}
		return t.row, nil
	}
	if err := t.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
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
