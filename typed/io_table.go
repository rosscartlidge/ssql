package typed

import (
	"bufio"
	"fmt"
	"io"
	"iter"
	"reflect"
	"strings"
	"unsafe"
)

// WriteTableToWriter formats a sequence of T as a width-aligned table.
// Numeric and boolean columns are right-justified; string and time
// columns are left-justified. The header is taken from struct field
// names (or `ssql:"name"` / `csv:"name"` tags), same as the CSV
// writer.
//
// All rows are buffered before printing so column widths can be
// computed exactly. This matches the behaviour of [ssql.DisplayTable]
// in the main package and is appropriate for terminal output (which
// is the only sensible use of `to table`). For very large outputs
// you'd typically pipe to `to csv` instead.
//
// Output shape (3-space column separator, dash underline):
//
//	relationship   number
//	----------------------
//	           1   4849187
//	          17    392383
func WriteTableToWriter[T any](seq iter.Seq[T], w io.Writer) error {
	schema, err := buildWriteSchema[T]()
	if err != nil {
		return err
	}
	return writeTableWithSchema(seq, w, schema, structFieldAlignments[T]())
}

// alignmentRight is true when the field should be right-justified
// (numeric/bool); false for left-justified (string/time/anything else).
func structFieldAlignments[T any]() []bool {
	var zero T
	rt := reflect.TypeOf(zero)
	if rt == nil || rt.Kind() != reflect.Struct {
		return nil
	}
	out := make([]bool, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		if _, skip := columnName(f); skip {
			continue
		}
		out = append(out, alignRightForType(f.Type))
	}
	return out
}

func alignRightForType(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.Bool:
		return true
	}
	return false
}

// WriteTableSelectedToWriter is the variant of [WriteTableToWriter]
// that accepts an explicit list of (header, extractor, rightAlign)
// triples. Used by the CLI codegen when the user has selected a
// subset / reordering of columns via `ssql to table FIELDS…`.
//
// Each extractor takes a *T and returns the field's string form. The
// header strings appear above each column and govern the minimum
// column width.
type TableColumn[T any] struct {
	Header     string
	Format     func(*T) string
	RightAlign bool
}

// WriteTableSelectedToWriter formats columns in the order given.
func WriteTableSelectedToWriter[T any](seq iter.Seq[T], w io.Writer, cols []TableColumn[T]) error {
	rows := make([][]string, 0, 64)
	for v := range seq {
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = c.Format(&v)
		}
		rows = append(rows, row)
	}
	headers := make([]string, len(cols))
	rightAlign := make([]bool, len(cols))
	for i, c := range cols {
		headers[i] = c.Header
		rightAlign[i] = c.RightAlign
	}
	return writeTableLines(w, headers, rows, rightAlign)
}

func writeTableWithSchema[T any](seq iter.Seq[T], w io.Writer, schema *rowSchema, rightAlign []bool) error {
	rows := make([][]string, 0, 64)
	for v := range seq {
		row := make([]string, len(schema.encoders))
		p := unsafe.Pointer(&v)
		for i, enc := range schema.encoders {
			row[i] = enc(p)
		}
		rows = append(rows, row)
	}
	return writeTableLines(w, schema.header, rows, rightAlign)
}

// writeTableLines is the shared formatter used by both
// WriteTableToWriter (struct-schema variant) and
// WriteTableSelectedToWriter (column-list variant).
//
// Column widths are max(header, each row's value); column separator
// is 3 spaces; underline is one dash per column-plus-separator
// character. Header is always left-justified; data alignment per
// column controlled by rightAlign.
func writeTableLines(w io.Writer, headers []string, rows [][]string, rightAlign []bool) error {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, v := range r {
			if i < len(widths) && len(v) > widths[i] {
				widths[i] = len(v)
			}
		}
	}

	bw := bufio.NewWriter(w)
	const sep = "   "

	for i, h := range headers {
		if i > 0 {
			bw.WriteString(sep)
		}
		fmt.Fprintf(bw, "%-*s", widths[i], h)
	}
	bw.WriteByte('\n')

	total := 0
	for i, cw := range widths {
		if i > 0 {
			total += len(sep)
		}
		total += cw
	}
	bw.WriteString(strings.Repeat("-", total))
	bw.WriteByte('\n')

	for _, r := range rows {
		for i, v := range r {
			if i > 0 {
				bw.WriteString(sep)
			}
			if i < len(rightAlign) && rightAlign[i] {
				fmt.Fprintf(bw, "%*s", widths[i], v)
			} else {
				fmt.Fprintf(bw, "%-*s", widths[i], v)
			}
		}
		bw.WriteByte('\n')
	}
	return bw.Flush()
}
