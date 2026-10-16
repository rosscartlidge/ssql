package typed

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/rosscartlidge/ssql/v4"
)

// timeType is cached so decoderFor doesn't allocate a reflect.Type per call.
var timeType = reflect.TypeOf(time.Time{})

// fieldDecoder writes one CSV column into a struct field at a known offset.
// The closure has zero reflection at call time.
type fieldDecoder func(p unsafe.Pointer, s string) error

// fieldEncoder reads one struct field as a string column.
type fieldEncoder func(p unsafe.Pointer) string

// rowSchema is the precomputed I/O plan for type T against a given header.
type rowSchema struct {
	decoders []fieldDecoder
	header   []string
	types    []string // Go type of the field each column decodes into ("" = no field)
	encoders []fieldEncoder
}

// decode fills the struct at p from one row. The error names the
// column, the offending value and the field type it did not fit —
// what a user needs to fix the data or widen the field.
func (rs *rowSchema) decode(p unsafe.Pointer, rec []string) error {
	if len(rec) < len(rs.decoders) {
		return fmt.Errorf("row has %d columns, expected %d", len(rec), len(rs.decoders))
	}
	for i, dec := range rs.decoders {
		// Every lane trims a cell before typing it (csv_infer.go's
		// parse*Cell; DuckDB for the numeric types it parses), so a
		// padded "  7" is the int 7 and " a " is "a". Until 2026-10-15
		// the typed lane parsed the raw cell: the column inferred as a
		// string and `where -if n gt 7` compared lexically.
		cell := strings.TrimSpace(rec[i])
		if err := dec(p, cell); err != nil {
			return fmt.Errorf("column %q: %q is not %s", rs.header[i], cell, rs.types[i])
		}
	}
	return nil
}

// columnName returns the CSV column name for a struct field.
// `ssql:"name"` is preferred; `csv:"name"` is accepted as fallback.
// Returns ("", true) when the field is excluded (tag value "-").
func columnName(f reflect.StructField) (name string, skip bool) {
	if tag, ok := f.Tag.Lookup("ssql"); ok {
		if tag == "-" {
			return "", true
		}
		return tag, false
	}
	if tag, ok := f.Tag.Lookup("csv"); ok {
		if tag == "-" {
			return "", true
		}
		return tag, false
	}
	return f.Name, false
}

// buildReadSchema analyzes T against the CSV header and returns one
// decoder per column. Columns whose name has no matching struct field
// get a no-op decoder. Reflection happens here, once.
//
// When strict is true, the function fails if any header column has
// no matching struct field, OR if any required (non-pointer) struct
// field has no matching column.
func buildReadSchema[T any](header []string, strict bool, timeFormats map[string]string) (*rowSchema, error) {
	var zero T
	rt := reflect.TypeOf(zero)
	if rt == nil || rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("typed: T must be a struct, got %v", rt)
	}

	byName := make(map[string]reflect.StructField, rt.NumField())
	wantedFields := make([]reflect.StructField, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		name, skip := columnName(f)
		if skip {
			continue
		}
		byName[strings.ToLower(name)] = f
		wantedFields = append(wantedFields, f)
	}

	matched := make(map[string]struct{}, len(byName))
	decoders := make([]fieldDecoder, len(header))
	types := make([]string, len(header))
	for col, h := range header {
		f, ok := byName[strings.ToLower(h)]
		if !ok {
			if strict {
				return nil, fmt.Errorf("typed: strict: header column %q has no matching field in %v", h, rt)
			}
			decoders[col] = noopDecoder
			continue
		}
		dec, err := decoderFor(f.Type, f.Offset)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		if pattern, ok := timeFormats[strings.ToLower(h)]; ok && f.Type == timeType {
			layout, err := ssql.TimeLayout(pattern)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", f.Name, err)
			}
			dec = decodeTimeLayout(f.Offset, layout)
		}
		decoders[col] = dec
		types[col] = f.Type.String()
		matched[strings.ToLower(h)] = struct{}{}
	}

	if strict {
		var missing []string
		for _, f := range wantedFields {
			name, _ := columnName(f)
			if _, ok := matched[strings.ToLower(name)]; ok {
				continue
			}
			// Pointer fields are nullable by definition — absent is OK.
			if f.Type.Kind() == reflect.Pointer {
				continue
			}
			missing = append(missing, name)
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("typed: strict: missing required CSV columns: %v", missing)
		}
	}

	return &rowSchema{
		decoders: decoders,
		header:   append([]string(nil), header...),
		types:    types,
	}, nil
}

// WriteOption configures the CSV, delimited and table writers.
type WriteOption func(*writeOpts)

type writeOpts struct {
	timeFormat string // a time.Time column's output pattern ("" = RFC 3339)
}

// OutputTimeFormat renders every time.Time column with a pattern —
// strftime ("%Y-%m-%d %H:%M") or a Go layout — on the pipeline zone's
// wall clock (ssql.FormatTimePattern): the sinks' -time-format.
func OutputTimeFormat(pattern string) WriteOption {
	return func(o *writeOpts) { o.timeFormat = pattern }
}

func resolveWriteOpts(opts []WriteOption) writeOpts {
	var o writeOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// buildWriteSchema produces the header and per-field encoders for type T.
func buildWriteSchema[T any](opts ...WriteOption) (*rowSchema, error) {
	o := resolveWriteOpts(opts)
	var zero T
	rt := reflect.TypeOf(zero)
	if rt == nil || rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("typed: T must be a struct, got %v", rt)
	}
	header := make([]string, 0, rt.NumField())
	encoders := make([]fieldEncoder, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		name, skip := columnName(f)
		if skip {
			continue
		}
		enc, err := encoderFor(f.Type, f.Offset, o.timeFormat)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		header = append(header, name)
		encoders = append(encoders, enc)
	}
	return &rowSchema{header: header, encoders: encoders}, nil
}

func noopDecoder(unsafe.Pointer, string) error { return nil }

func decoderFor(t reflect.Type, off uintptr) (fieldDecoder, error) {
	if t == timeType {
		return decodeTime(off), nil
	}
	if t.Kind() == reflect.Pointer {
		return decoderForPointer(t, off)
	}
	switch t.Kind() {
	case reflect.String:
		return func(p unsafe.Pointer, s string) error {
			*(*string)(unsafe.Add(p, off)) = s
			return nil
		}, nil
	case reflect.Int64:
		return func(p unsafe.Pointer, s string) error {
			if s == "" {
				return errEmptyCell
			}
			v, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return err
			}
			*(*int64)(unsafe.Add(p, off)) = v
			return nil
		}, nil
	case reflect.Int32:
		return func(p unsafe.Pointer, s string) error {
			if s == "" {
				return errEmptyCell
			}
			v, err := strconv.ParseInt(s, 10, 32)
			if err != nil {
				return err
			}
			*(*int32)(unsafe.Add(p, off)) = int32(v)
			return nil
		}, nil
	case reflect.Int:
		return func(p unsafe.Pointer, s string) error {
			if s == "" {
				return errEmptyCell
			}
			v, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return err
			}
			*(*int)(unsafe.Add(p, off)) = int(v)
			return nil
		}, nil
	case reflect.Uint64:
		return func(p unsafe.Pointer, s string) error {
			if s == "" {
				return errEmptyCell
			}
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return err
			}
			*(*uint64)(unsafe.Add(p, off)) = v
			return nil
		}, nil
	case reflect.Float64:
		return func(p unsafe.Pointer, s string) error {
			if s == "" {
				return errEmptyCell
			}
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return err
			}
			*(*float64)(unsafe.Add(p, off)) = v
			return nil
		}, nil
	case reflect.Float32:
		return func(p unsafe.Pointer, s string) error {
			if s == "" {
				return errEmptyCell
			}
			v, err := strconv.ParseFloat(s, 32)
			if err != nil {
				return err
			}
			*(*float32)(unsafe.Add(p, off)) = float32(v)
			return nil
		}, nil
	case reflect.Bool:
		return func(p unsafe.Pointer, s string) error {
			if s == "" {
				return errEmptyCell
			}
			v, err := strconv.ParseBool(s)
			if err != nil {
				return err
			}
			*(*bool)(unsafe.Add(p, off)) = v
			return nil
		}, nil
	default:
		return nil, fmt.Errorf("unsupported field kind: %v", t.Kind())
	}
}

// decodeTime parses a time cell in the forms every other lane accepts
// (ssql.ParseTime: RFC 3339, "2006-01-02 15:04:05", "2006-01-02", …). It
// accepted RFC 3339 alone until 2026-09-22, so `from csv -type d time`
// over a date-only column read in exec and record mode and failed in
// typed mode. Empty cell → errEmptyCell, as for every non-string type.
// decodeTimeLayout is decodeTime for a column read through a pattern.
func decodeTimeLayout(off uintptr, layout string) fieldDecoder {
	return func(p unsafe.Pointer, s string) error {
		if s == "" {
			return errEmptyCell
		}
		v, err := time.ParseInLocation(layout, s, ssql.StreamLocation()) // a local clock: the pipeline zone
		if err != nil {
			return fmt.Errorf("%q does not match the column's time format", s)
		}
		*(*time.Time)(unsafe.Add(p, off)) = v
		return nil
	}
}

func decodeTime(off uintptr) fieldDecoder {
	return func(p unsafe.Pointer, s string) error {
		if s == "" {
			return errEmptyCell
		}
		v, ok := ssql.ParseTime(s)
		if !ok {
			return fmt.Errorf("%q is not a time", s)
		}
		*(*time.Time)(unsafe.Add(p, off)) = v
		return nil
	}
}

// errEmptyCell is what a non-pointer, non-string decoder returns for an
// empty cell. A typed struct cannot hold absence, and writing the zero
// value silently (the behaviour until v4.92.0) made the typed lane
// disagree with the record lane, where an empty numeric/bool cell is an
// absent field (DFC124). The row-level wrapper renders it as
// `column "v": "" is not int64`; pointer fields (*int64) take nil and
// string fields take "" as before.
var errEmptyCell = errors.New("empty cell")

// decoderForPointer builds a decoder for *T fields. Empty CSV value
// becomes a nil pointer; non-empty allocates a fresh T, parses, and
// points at it.
//
// Allocation cost: one heap allocation per non-empty value. Users with
// hot paths and many nullable fields should prefer separate "Valid bool"
// fields (sql.NullInt64-style) instead of pointer types.
func decoderForPointer(t reflect.Type, off uintptr) (fieldDecoder, error) {
	elem := t.Elem()
	// Build an inner decoder that writes at offset 0 (relative to the
	// freshly-allocated element).
	inner, err := decoderFor(elem, 0)
	if err != nil {
		return nil, fmt.Errorf("pointer to %v: %w", elem, err)
	}
	// reflect.New produces a *T as reflect.Value. We need its address
	// stored at the field's offset. Use reflect for the allocation
	// (control-path), then copy the pointer bits.
	return func(p unsafe.Pointer, s string) error {
		if s == "" {
			// Zero out the pointer field (set to nil).
			*(*unsafe.Pointer)(unsafe.Add(p, off)) = nil
			return nil
		}
		v := reflect.New(elem)
		if err := inner(v.UnsafePointer(), s); err != nil {
			return err
		}
		*(*unsafe.Pointer)(unsafe.Add(p, off)) = v.UnsafePointer()
		return nil
	}, nil
}

func encoderFor(t reflect.Type, off uintptr, timeFormat string) (fieldEncoder, error) {
	if t == timeType {
		return func(p unsafe.Pointer) string {
			t := *(*time.Time)(unsafe.Add(p, off))
			if t.IsZero() {
				return ""
			}
			if timeFormat != "" {
				return ssql.FormatTimePattern(t, timeFormat)
			}
			return t.Format(time.RFC3339)
		}, nil
	}
	if t.Kind() == reflect.Pointer {
		return encoderForPointer(t, off, timeFormat)
	}
	switch t.Kind() {
	case reflect.String:
		return func(p unsafe.Pointer) string {
			return *(*string)(unsafe.Add(p, off))
		}, nil
	case reflect.Int64:
		return func(p unsafe.Pointer) string {
			return strconv.FormatInt(*(*int64)(unsafe.Add(p, off)), 10)
		}, nil
	case reflect.Int32:
		return func(p unsafe.Pointer) string {
			return strconv.FormatInt(int64(*(*int32)(unsafe.Add(p, off))), 10)
		}, nil
	case reflect.Int:
		return func(p unsafe.Pointer) string {
			return strconv.Itoa(*(*int)(unsafe.Add(p, off)))
		}, nil
	case reflect.Uint64:
		return func(p unsafe.Pointer) string {
			return strconv.FormatUint(*(*uint64)(unsafe.Add(p, off)), 10)
		}, nil
	case reflect.Float64:
		return func(p unsafe.Pointer) string {
			return strconv.FormatFloat(*(*float64)(unsafe.Add(p, off)), 'f', -1, 64)
		}, nil
	case reflect.Float32:
		return func(p unsafe.Pointer) string {
			return strconv.FormatFloat(float64(*(*float32)(unsafe.Add(p, off))), 'f', -1, 32)
		}, nil
	case reflect.Bool:
		return func(p unsafe.Pointer) string {
			return strconv.FormatBool(*(*bool)(unsafe.Add(p, off)))
		}, nil
	default:
		return nil, fmt.Errorf("unsupported field kind: %v", t.Kind())
	}
}

// encoderForPointer builds an encoder for *T fields. nil pointer → empty string.
func encoderForPointer(t reflect.Type, off uintptr, timeFormat string) (fieldEncoder, error) {
	elem := t.Elem()
	inner, err := encoderFor(elem, 0, timeFormat)
	if err != nil {
		return nil, fmt.Errorf("pointer to %v: %w", elem, err)
	}
	return func(p unsafe.Pointer) string {
		ptr := *(*unsafe.Pointer)(unsafe.Add(p, off))
		if ptr == nil {
			return ""
		}
		return inner(ptr)
	}, nil
}

// CSVOption configures a CSV reader. Pass to ReadCSV / ReadCSVSafe and
// their *FromReader variants.
type CSVOption func(*csvOpts)

type csvOpts struct {
	strict      bool
	timeFormats map[string]string // lower-cased column → pattern (TimeFormat)
}

// TimeFormat reads a time.Time column whose cells follow a pattern
// (strftime "%d/%m/%Y" or a Go layout) rather than the RFC 3339 and SQL
// forms ssql.ParseTime accepts — `from csv -format COL PATTERN`.
func TimeFormat(column, pattern string) CSVOption {
	return func(o *csvOpts) {
		if o.timeFormats == nil {
			o.timeFormats = map[string]string{}
		}
		o.timeFormats[strings.ToLower(column)] = pattern
	}
}

// Strict makes ReadCSV* fail on schema mismatch: any CSV column without
// a matching struct field, OR any required (non-pointer) struct field
// without a matching column. Pointer fields are always optional.
//
// Without Strict (the default), unknown columns are silently dropped
// and missing fields stay at their zero value.
func Strict() CSVOption { return func(o *csvOpts) { o.strict = true } }

func resolveOpts(opts []CSVOption) csvOpts {
	var o csvOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// ReadCSV streams rows of T from a CSV file. It FAILS FAST: a file that
// cannot be opened, a header that does not fit T (under [Strict]), a
// malformed row, or a cell that does not parse as its field's type
// panics with a *[ReadError] naming the row and column — never a
// silently zeroed field or a dropped row. Use [ReadCSVSafe] to receive
// those conditions as error values instead.
//
// Reflection happens once at file-open time (to build the read schema
// from the header). The per-row path uses precomputed offset writers
// only — no reflection per row.
//
// Pass [Strict] to reject CSVs whose header doesn't exactly match T.
func ReadCSV[T any](filename string, opts ...CSVOption) iter.Seq[T] {
	return func(yield func(T) bool) {
		f, err := ssql.OpenInput(filename)
		if err != nil {
			failRead("typed.ReadCSV", filename, err)
		}
		defer f.Close()
		readCSVLoud[T]("typed.ReadCSV", filename, f, resolveOpts(opts), yield)
	}
}

// ReadCSVFromReader is the [io.Reader] variant of [ReadCSV].
func ReadCSVFromReader[T any](r io.Reader, opts ...CSVOption) iter.Seq[T] {
	o := resolveOpts(opts)
	return func(yield func(T) bool) {
		readCSVLoud[T]("typed.ReadCSVFromReader", "", r, o, yield)
	}
}

// readCSVLoud is the shared body of the lossy CSV readers. An empty
// input (no header line) is zero rows, not an error — the same as the
// root package's reader.
func readCSVLoud[T any](op, source string, r io.Reader, o csvOpts, yield func(T) bool) {
	cr := csv.NewReader(r)
	cr.ReuseRecord = true
	header, err := cr.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return
		}
		failRead(op, source, fmt.Errorf("read header: %w", err))
	}
	schema, err := buildReadSchema[T](header, o.strict, o.timeFormats)
	if err != nil {
		failRead(op, source, err)
	}
	var rowN int64
	for {
		rec, err := cr.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			failRow(op, source, rowN+1, err)
		}
		rowN++
		var row T
		if err := schema.decode(unsafe.Pointer(&row), rec); err != nil {
			failRow(op, source, rowN, err)
		}
		if !yield(row) {
			return
		}
	}
}

// ReadCSVSafe is the error-reporting variant of [ReadCSV]. The returned
// sequence yields a fresh error for any row that fails to parse — file
// open failures, header decode failures, and per-row parse failures.
func ReadCSVSafe[T any](filename string, opts ...CSVOption) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		f, err := ssql.OpenInput(filename)
		if err != nil {
			var zero T
			yield(zero, err)
			return
		}
		defer f.Close()
		ReadCSVSafeFromReader[T](f, opts...)(yield)
	}
}

// ReadCSVSafeFromReader is the [io.Reader] variant of [ReadCSVSafe].
func ReadCSVSafeFromReader[T any](r io.Reader, opts ...CSVOption) iter.Seq2[T, error] {
	o := resolveOpts(opts)
	return func(yield func(T, error) bool) {
		cr := csv.NewReader(r)
		cr.ReuseRecord = true
		header, err := cr.Read()
		if err != nil {
			var zero T
			yield(zero, fmt.Errorf("typed.ReadCSV: read header: %w", err))
			return
		}
		schema, err := buildReadSchema[T](header, o.strict, o.timeFormats)
		if err != nil {
			var zero T
			yield(zero, err)
			return
		}
		for {
			rec, err := cr.Read()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				var zero T
				if !yield(zero, err) {
					return
				}
				continue
			}
			var row T
			if err := schema.decode(unsafe.Pointer(&row), rec); err != nil {
				if !yield(row, err) {
					return
				}
				continue
			}
			if !yield(row, nil) {
				return
			}
		}
	}
}

// WriteCSV writes a sequence of T as CSV. The header row is taken from
// struct field names (or `ssql:"name"`/`csv:"name"` tags). Encoder
// closures are built once before the loop.
func WriteCSV[T any](seq iter.Seq[T], filename string, opts ...WriteOption) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return WriteCSVToWriter(seq, f, opts...)
}

// WriteCSVToWriter is the [io.Writer] variant of [WriteCSV].
func WriteCSVToWriter[T any](seq iter.Seq[T], w io.Writer, opts ...WriteOption) error {
	schema, err := buildWriteSchema[T](opts...)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(schema.header); err != nil {
		return err
	}
	row := make([]string, len(schema.encoders))
	for v := range seq {
		p := unsafe.Pointer(&v)
		for i, enc := range schema.encoders {
			row[i] = enc(p)
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
