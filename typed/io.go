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
	encoders []fieldEncoder
}

func (rs *rowSchema) decode(p unsafe.Pointer, rec []string) error {
	if len(rec) < len(rs.decoders) {
		return fmt.Errorf("typed: row has %d columns, expected %d", len(rec), len(rs.decoders))
	}
	for i, dec := range rs.decoders {
		if err := dec(p, rec[i]); err != nil {
			return fmt.Errorf("column %q: %w", rs.header[i], err)
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
func buildReadSchema[T any](header []string) (*rowSchema, error) {
	var zero T
	rt := reflect.TypeOf(zero)
	if rt == nil || rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("typed: T must be a struct, got %v", rt)
	}

	byName := make(map[string]reflect.StructField, rt.NumField())
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
	}

	decoders := make([]fieldDecoder, len(header))
	for col, h := range header {
		f, ok := byName[strings.ToLower(h)]
		if !ok {
			decoders[col] = noopDecoder
			continue
		}
		dec, err := decoderFor(f.Type, f.Offset)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		decoders[col] = dec
	}
	return &rowSchema{decoders: decoders, header: append([]string(nil), header...)}, nil
}

// buildWriteSchema produces the header and per-field encoders for type T.
func buildWriteSchema[T any]() (*rowSchema, error) {
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
		enc, err := encoderFor(f.Type, f.Offset)
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
				*(*int64)(unsafe.Add(p, off)) = 0
				return nil
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
				*(*int32)(unsafe.Add(p, off)) = 0
				return nil
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
				*(*int)(unsafe.Add(p, off)) = 0
				return nil
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
				*(*uint64)(unsafe.Add(p, off)) = 0
				return nil
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
				*(*float64)(unsafe.Add(p, off)) = 0
				return nil
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
				*(*float32)(unsafe.Add(p, off)) = 0
				return nil
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
				*(*bool)(unsafe.Add(p, off)) = false
				return nil
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

// decodeTime parses RFC3339 timestamps. Empty value → zero time.
//
// Note: time.Parse with a fixed layout is not the absolute fastest path
// — a hand-rolled RFC3339 parser is ~3x faster — but it handles all
// edge cases (timezones, fractional seconds) correctly and is the
// natural choice for a generic library. See PERFORMANCE-NOTES.md for
// the optimization opportunity.
func decodeTime(off uintptr) fieldDecoder {
	return func(p unsafe.Pointer, s string) error {
		if s == "" {
			*(*time.Time)(unsafe.Add(p, off)) = time.Time{}
			return nil
		}
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return err
		}
		*(*time.Time)(unsafe.Add(p, off)) = v
		return nil
	}
}

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

func encoderFor(t reflect.Type, off uintptr) (fieldEncoder, error) {
	if t == timeType {
		return func(p unsafe.Pointer) string {
			t := *(*time.Time)(unsafe.Add(p, off))
			if t.IsZero() {
				return ""
			}
			return t.Format(time.RFC3339)
		}, nil
	}
	if t.Kind() == reflect.Pointer {
		return encoderForPointer(t, off)
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
func encoderForPointer(t reflect.Type, off uintptr) (fieldEncoder, error) {
	elem := t.Elem()
	inner, err := encoderFor(elem, 0)
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

// ReadCSV streams rows of T from a CSV file. Parse errors on individual
// columns silently zero the field and continue — use [ReadCSVSafe] when
// you need to surface those errors.
//
// Reflection happens once at file-open time (to build the read schema
// from the header). The per-row path uses precomputed offset writers
// only — no reflection per row.
func ReadCSV[T any](filename string) iter.Seq[T] {
	return func(yield func(T) bool) {
		f, err := os.Open(filename)
		if err != nil {
			return
		}
		defer f.Close()
		ReadCSVFromReader[T](f)(yield)
	}
}

// ReadCSVFromReader is the [io.Reader] variant of [ReadCSV].
func ReadCSVFromReader[T any](r io.Reader) iter.Seq[T] {
	return func(yield func(T) bool) {
		cr := csv.NewReader(r)
		cr.ReuseRecord = true
		header, err := cr.Read()
		if err != nil {
			return
		}
		schema, err := buildReadSchema[T](header)
		if err != nil {
			return
		}
		for {
			rec, err := cr.Read()
			if err != nil {
				return
			}
			var row T
			_ = schema.decode(unsafe.Pointer(&row), rec)
			if !yield(row) {
				return
			}
		}
	}
}

// ReadCSVSafe is the error-reporting variant of [ReadCSV]. The returned
// sequence yields a fresh error for any row that fails to parse — file
// open failures, header decode failures, and per-row parse failures.
func ReadCSVSafe[T any](filename string) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		f, err := os.Open(filename)
		if err != nil {
			var zero T
			yield(zero, err)
			return
		}
		defer f.Close()
		ReadCSVSafeFromReader[T](f)(yield)
	}
}

// ReadCSVSafeFromReader is the [io.Reader] variant of [ReadCSVSafe].
func ReadCSVSafeFromReader[T any](r io.Reader) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		cr := csv.NewReader(r)
		cr.ReuseRecord = true
		header, err := cr.Read()
		if err != nil {
			var zero T
			yield(zero, fmt.Errorf("typed.ReadCSV: read header: %w", err))
			return
		}
		schema, err := buildReadSchema[T](header)
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
func WriteCSV[T any](seq iter.Seq[T], filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return WriteCSVToWriter(seq, f)
}

// WriteCSVToWriter is the [io.Writer] variant of [WriteCSV].
func WriteCSVToWriter[T any](seq iter.Seq[T], w io.Writer) error {
	schema, err := buildWriteSchema[T]()
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
