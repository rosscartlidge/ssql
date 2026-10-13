package ssql

import (
	"bufio"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"
)

// FormatValue renders a value as the text sinks write it: a string as
// itself, a JSONString as its JSON text, numbers in Go's shortest form,
// a bool as true/false, a time in RFC 3339, nil as "". One rule for
// `to csv`, `to tsv` and `to lines`, in every lane.
func FormatValue(value any) string { return formatValue(value) }

// WriteLines writes one field's value per record as a line of text —
// the twin of ReadLines, behind `ssql to lines FIELD`. Values render by
// FormatValue, so a `json` field holding a pipeline document (a row of a
// views table) comes out as its JSON text, ready for `ssql run -stdin`.
// The field is validated against the first record, naming the record's
// fields when it is missing: a misspelt field would otherwise print
// blank lines. A later record without the field prints an empty line
// (the value is absent).
func WriteLines(records iter.Seq[Record], field, filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	if err := WriteLinesToWriter(records, field, f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// WriteLinesToWriter is WriteLines to a writer.
func WriteLinesToWriter(records iter.Seq[Record], field string, w io.Writer) error {
	bw := bufio.NewWriter(w)
	first := true
	for r := range records {
		if first {
			first = false
			if !r.Has(field) {
				return fmt.Errorf("to lines: unknown field %q (available: %s)", field, strings.Join(r.Schema().Fields(), ", "))
			}
		}
		if _, err := bw.WriteString(formatValue(GetOr[any](r, field, nil))); err != nil {
			return err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return err
		}
	}
	return bw.Flush()
}
