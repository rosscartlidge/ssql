package typed

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"os"
	"reflect"
	"unsafe"

	"github.com/rosscartlidge/ssql/v4"
)

// ReadJSONL streams rows of T from a JSON-Lines file (one JSON object per
// line). Lines that fail to decode are silently skipped — use
// [ReadJSONLSafe] when you need to surface errors.
//
// Field mapping uses standard `json:"name"` struct tags. Unlike CSV
// where every column gets a value, JSON fields are optional — missing
// fields leave the corresponding struct field at its zero value.
//
// Decoding is positional: the type is reflected over ONCE to build a
// key → field plan (jsonl_fast.go), then each line is walked once and
// values are written straight into the struct — the same fieldDecoder
// closures the CSV reader uses. A type with a field kind the plan
// cannot handle (slices, maps, nested structs) falls back to
// encoding/json for every row.
//
// Like [ReadCSV] it fails fast: an unreadable file, a line that is not
// a JSON object, a value that does not fit its field's type, or a line
// over the 1 MB limit panics with a *[ReadError] naming the line;
// [ReadJSONLSafe] yields those as errors instead. Blank lines and a
// leading `_schema` header are skipped.
func ReadJSONL[T any](filename string, opts ...JSONOption) iter.Seq[T] {
	return func(yield func(T) bool) {
		f, err := ssql.OpenInput(filename)
		if err != nil {
			failRead("typed.ReadJSONL", filename, err)
		}
		defer f.Close()
		readJSONLLoud[T]("typed.ReadJSONL", filename, f, yield, opts)
	}
}

// ReadJSONLFromReader is the [io.Reader] variant of [ReadJSONL].
func ReadJSONLFromReader[T any](rd io.Reader, opts ...JSONOption) iter.Seq[T] {
	return func(yield func(T) bool) {
		readJSONLLoud[T]("typed.ReadJSONLFromReader", "", rd, yield, opts)
	}
}

// readJSONLLoud is the shared body of the lossy JSONL readers. Types
// the positional plan cannot cover decode with encoding/json.
func readJSONLLoud[T any](op, source string, rd io.Reader, yield func(T) bool, opts []JSONOption) {
	pl, perr := buildJSONLPlan[T](opts...)
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64*1024), 1024*1024) // 1 MB max line
	var lineN int64
	for sc.Scan() {
		lineN++
		line := sc.Bytes()
		if len(line) == 0 || isSchemaHeaderLine(line) {
			continue
		}
		var row T
		var err error
		if perr == nil {
			err = pl.decode(line, unsafe.Pointer(&row))
		} else {
			err = json.Unmarshal(line, &row)
		}
		if err != nil {
			failLine(op, source, lineN, err)
		}
		if !yield(row) {
			return
		}
	}
	if err := sc.Err(); err != nil {
		failLine(op, source, lineN+1, err)
	}
}

// readJSONLReflect is the encoding/json path on its own — the reference
// the differential test and benchmark compare the positional decoder
// against. Behaves like [ReadJSONLFromReader] with no positional plan.
func readJSONLReflect[T any](rd io.Reader) iter.Seq[T] {
	return func(yield func(T) bool) {
		sc := bufio.NewScanner(rd)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		var lineN int64
		for sc.Scan() {
			lineN++
			line := sc.Bytes()
			if len(line) == 0 || isSchemaHeaderLine(line) {
				continue
			}
			var row T
			if err := json.Unmarshal(line, &row); err != nil {
				failLine("typed.ReadJSONLFromReader", "", lineN, err)
			}
			if !yield(row) {
				return
			}
		}
	}
}

// isSchemaHeaderLine reports whether a JSONL line is ssql's `_schema`
// header (written by tee and every ssql stage). It describes the rows;
// decoding it as a row would yield a zero-valued phantom record.
func isSchemaHeaderLine(line []byte) bool {
	return bytes.HasPrefix(line, []byte(`{"_schema"`))
}

// ReadJSONLSafe is the error-reporting variant of [ReadJSONL].
func ReadJSONLSafe[T any](filename string, opts ...JSONOption) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		f, err := ssql.OpenInput(filename)
		if err != nil {
			var zero T
			yield(zero, fmt.Errorf("typed.ReadJSONL: %w", err))
			return
		}
		defer f.Close()
		ReadJSONLSafeFromReader[T](f, opts...)(yield)
	}
}

// ReadJSONLSafeFromReader is the [io.Reader] variant of [ReadJSONLSafe].
func ReadJSONLSafeFromReader[T any](r io.Reader, opts ...JSONOption) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		pl, perr := buildJSONLPlan[T](opts...)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || isSchemaHeaderLine(line) {
				continue
			}
			var row T
			var err error
			if perr == nil {
				err = pl.decode(line, unsafe.Pointer(&row))
			} else {
				err = json.Unmarshal(line, &row)
			}
			if err != nil {
				if !yield(row, err) {
					return
				}
				continue
			}
			if !yield(row, nil) {
				return
			}
		}
		if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
			var zero T
			yield(zero, err)
		}
	}
}

// WriteJSONL writes a sequence of T as JSON-Lines (one object per line).
// Encoding uses standard encoding/json with the type's `json:"name"` tags.
func WriteJSONL[T any](seq iter.Seq[T], filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return WriteJSONLToWriter(seq, f)
}

// WriteJSONLToWriter is the [io.Writer] variant of [WriteJSONL]. A row
// holding a float that is not a number (NaN, ±Inf — a square root of a
// negative, a division by zero) is written with that field as null, the
// rule every ssql writer follows (DFC133: JSON has no NaN; null is no
// value), where encoding/json would refuse the whole row.
func WriteJSONLToWriter[T any](seq iter.Seq[T], w io.Writer) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	for v := range seq {
		if err := enc.Encode(v); err != nil {
			var unsupported *json.UnsupportedValueError
			if !errors.As(err, &unsupported) {
				return err
			}
			if err := writeRowNonFinite(bw, v); err != nil {
				return err
			}
		}
	}
	return bw.Flush()
}

// writeRowNonFinite writes one struct row field by field, in declaration
// order under the json tag names, with a non-finite float (plain or
// pointer) as null and every other field through encoding/json. Only the
// slow path of a row encoding/json refused.
func writeRowNonFinite(bw *bufio.Writer, v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			_, err := bw.WriteString("null\n")
			return err
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("WriteJSONL: cannot write a %s holding a value that is not a number", rv.Type())
	}
	bw.WriteByte('{')
	first := true
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Type().Field(i)
		name, skip := jsonFieldName(f)
		if skip || !f.IsExported() {
			continue
		}
		fv := rv.Field(i)
		var text []byte
		switch fv.Kind() {
		case reflect.Float32, reflect.Float64:
			if x := fv.Float(); math.IsNaN(x) || math.IsInf(x, 0) {
				text = []byte("null")
			}
		case reflect.Pointer:
			if k := fv.Type().Elem().Kind(); !fv.IsNil() && (k == reflect.Float32 || k == reflect.Float64) {
				if x := fv.Elem().Float(); math.IsNaN(x) || math.IsInf(x, 0) {
					text = []byte("null")
				}
			}
		}
		if text == nil {
			var err error
			if text, err = json.Marshal(fv.Interface()); err != nil {
				return fmt.Errorf("WriteJSONL: field %s: %w", name, err)
			}
		}
		if !first {
			bw.WriteByte(',')
		}
		first = false
		nameJSON, _ := json.Marshal(name)
		bw.Write(nameJSON)
		bw.WriteByte(':')
		bw.Write(text)
	}
	bw.WriteByte('}')
	return bw.WriteByte('\n')
}
