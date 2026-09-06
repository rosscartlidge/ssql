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
)

// ReadJSONL streams rows of T from a JSON-Lines file (one JSON object per
// line). Lines that fail to decode are silently skipped — use
// [ReadJSONLSafe] when you need to surface errors.
//
// Field mapping uses standard `json:"name"` struct tags. Unlike CSV
// where every column gets a value, JSON fields are optional — missing
// fields leave the corresponding struct field at its zero value.
//
// Note: encoding/json uses reflection on every row. For pipelines
// where JSONL throughput matters more than convenience, consider
// custom unmarshalling per type or a faster JSON library
// (e.g. goccy/go-json or bytedance/sonic). See PERFORMANCE-NOTES.md.
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
	return func(yield func(T) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024) // 1 MB max line
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
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || isSchemaHeaderLine(line) {
				continue
			}
			var row T
			if err := json.Unmarshal(line, &row); err != nil {
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
