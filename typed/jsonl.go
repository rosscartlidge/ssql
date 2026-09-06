package typed

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"unsafe"
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
func ReadJSONL[T any](filename string) iter.Seq[T] {
	return func(yield func(T) bool) {
		f, err := os.Open(filename)
		if err != nil {
			return
		}
		defer f.Close()
		ReadJSONLFromReader[T](f)(yield)
	}
}

// ReadJSONLFromReader is the [io.Reader] variant of [ReadJSONL].
func ReadJSONLFromReader[T any](r io.Reader) iter.Seq[T] {
	pl, perr := buildJSONLPlan[T]()
	if perr != nil {
		return readJSONLReflect[T](r)
	}
	return func(yield func(T) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024) // 1 MB max line
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || isSchemaHeaderLine(line) {
				continue
			}
			var row T
			if err := pl.decode(line, unsafe.Pointer(&row)); err != nil {
				continue
			}
			if !yield(row) {
				return
			}
		}
	}
}

// readJSONLReflect is the encoding/json path: the fallback for types
// the positional plan cannot cover, and the reference the differential
// test compares against.
func readJSONLReflect[T any](r io.Reader) iter.Seq[T] {
	return func(yield func(T) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || isSchemaHeaderLine(line) {
				continue
			}
			var row T
			if err := json.Unmarshal(line, &row); err != nil {
				continue
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
func ReadJSONLSafe[T any](filename string) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		f, err := os.Open(filename)
		if err != nil {
			var zero T
			yield(zero, fmt.Errorf("typed.ReadJSONL: %w", err))
			return
		}
		defer f.Close()
		ReadJSONLSafeFromReader[T](f)(yield)
	}
}

// ReadJSONLSafeFromReader is the [io.Reader] variant of [ReadJSONLSafe].
func ReadJSONLSafeFromReader[T any](r io.Reader) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		pl, perr := buildJSONLPlan[T]()
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

// WriteJSONLToWriter is the [io.Writer] variant of [WriteJSONL].
func WriteJSONLToWriter[T any](seq iter.Seq[T], w io.Writer) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	for v := range seq {
		if err := enc.Encode(v); err != nil {
			return err
		}
	}
	return bw.Flush()
}
