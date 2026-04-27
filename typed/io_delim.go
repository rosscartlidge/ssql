package typed

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"runtime"
	"sync"
	"unsafe"
)

// DelimOption configures a delimited-text reader/writer. Pass to
// [ReadDelim] / [WriteDelim] and their variants.
type DelimOption func(*delimOpts)

type delimOpts struct {
	delim  byte
	strict bool
}

// WithDelim sets the field delimiter. Default is '\t' (tab).
//
// Common choices:
//   - '\t' for TSV (default)
//   - ',' for fast comma-separated reading WITHOUT quote handling.
//     Note: this is **not** RFC-4180 CSV — fields cannot contain
//     embedded commas, quotes, or newlines. Use [ReadCSV] when full
//     CSV semantics are required.
//   - '|', ':' for pipe- or colon-separated formats.
func WithDelim(b byte) DelimOption {
	return func(o *delimOpts) { o.delim = b }
}

// DelimStrict mirrors [Strict] for delimited-text readers: any header
// column without a matching struct field, OR any required (non-pointer)
// struct field without a matching column, is a hard error. Without it
// (the default), unknown columns are silently dropped and missing
// fields stay at their zero value.
func DelimStrict() DelimOption {
	return func(o *delimOpts) { o.strict = true }
}

func resolveDelimOpts(opts []DelimOption) delimOpts {
	o := delimOpts{delim: '\t'}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// splitLine fills row with one string per delim-separated field.
// row's underlying array is reused across calls when the caller passes
// the same slice in. Returns the (possibly grown) row.
//
// Each field is `string(line[start:end])` — one copy. Use
// [splitLineAlias] for the zero-copy variant when the caller can
// guarantee the line buffer lives at least as long as the strings.
func splitLine(line []byte, delim byte, row []string) []string {
	row = row[:0]
	start := 0
	for i := 0; i < len(line); i++ {
		if line[i] == delim {
			row = append(row, string(line[start:i]))
			start = i + 1
		}
	}
	row = append(row, string(line[start:]))
	return row
}

// splitLineAlias is the **zero-copy** variant of [splitLine]. Each
// returned string points directly into the input line via
// [unsafe.String] — no allocation, no copy.
//
// Uses [bytes.IndexByte] (SIMD-accelerated on amd64) to find each
// delimiter in O(line/16) instead of O(line) byte-by-byte; ~5-10×
// faster than a manual scan loop on long lines.
//
// SAFETY: the resulting strings alias `line`. Callers MUST guarantee
// the line buffer is not reused or freed for as long as any of the
// strings (or string-typed struct fields decoded from them) are live.
//
// Used by [ReadDelimParallel] where the entire file is held in memory
// (`os.ReadFile` once, sliced into per-shard chunks); each shard's
// chunk lives for the duration of the iter.Seq, and the strings
// derived from it remain valid for the whole pipeline. NOT safe with
// `bufio.Scanner` which reuses its buffer between calls.
func splitLineAlias(line []byte, delim byte, row []string) []string {
	row = row[:0]
	for {
		idx := bytes.IndexByte(line, delim)
		if idx < 0 {
			row = append(row, bytesAsString(line))
			return row
		}
		row = append(row, bytesAsString(line[:idx]))
		line = line[idx+1:]
	}
}

// bytesAsString reinterprets b as a string without copying. The
// resulting string aliases b's underlying memory.
func bytesAsString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// ReadDelim streams rows of T from a delimited-text file. Each line is
// split on the configured delimiter (default '\t'); fields are NOT
// quote-decoded — embedded delimiters or newlines are not handled.
// Use [ReadCSV] when RFC-4180 semantics are needed.
//
// Reflection happens once at file-open time (to build the read schema
// from the header). The per-row path uses precomputed offset writers
// only — no reflection per row.
func ReadDelim[T any](filename string, opts ...DelimOption) iter.Seq[T] {
	return func(yield func(T) bool) {
		f, err := os.Open(filename)
		if err != nil {
			return
		}
		defer f.Close()
		ReadDelimFromReader[T](f, opts...)(yield)
	}
}

// ReadDelimFromReader is the [io.Reader] variant of [ReadDelim].
func ReadDelimFromReader[T any](r io.Reader, opts ...DelimOption) iter.Seq[T] {
	o := resolveDelimOpts(opts)
	return func(yield func(T) bool) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		if !scanner.Scan() {
			return
		}
		header := splitLine(scanner.Bytes(), o.delim, nil)
		schema, err := buildReadSchema[T](header, o.strict)
		if err != nil {
			return
		}
		var row []string
		for scanner.Scan() {
			row = splitLine(scanner.Bytes(), o.delim, row)
			var rec T
			_ = schema.decode(unsafe.Pointer(&rec), row)
			if !yield(rec) {
				return
			}
		}
	}
}

// ReadDelimSafe is the error-reporting variant of [ReadDelim].
func ReadDelimSafe[T any](filename string, opts ...DelimOption) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		f, err := os.Open(filename)
		if err != nil {
			var zero T
			yield(zero, err)
			return
		}
		defer f.Close()
		ReadDelimSafeFromReader[T](f, opts...)(yield)
	}
}

// ReadDelimSafeFromReader is the [io.Reader] variant of [ReadDelimSafe].
func ReadDelimSafeFromReader[T any](r io.Reader, opts ...DelimOption) iter.Seq2[T, error] {
	o := resolveDelimOpts(opts)
	return func(yield func(T, error) bool) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				var zero T
				yield(zero, fmt.Errorf("typed.ReadDelim: read header: %w", err))
			}
			return
		}
		header := splitLine(scanner.Bytes(), o.delim, nil)
		schema, err := buildReadSchema[T](header, o.strict)
		if err != nil {
			var zero T
			yield(zero, err)
			return
		}
		var row []string
		for scanner.Scan() {
			row = splitLine(scanner.Bytes(), o.delim, row)
			var rec T
			if err := schema.decode(unsafe.Pointer(&rec), row); err != nil {
				if !yield(rec, err) {
					return
				}
				continue
			}
			if !yield(rec, nil) {
				return
			}
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			var zero T
			yield(zero, err)
		}
	}
}

// WriteDelim writes a sequence of T as delimited text. The header row
// is taken from struct field names (or `ssql:"name"` / `csv:"name"`
// tags). Fields are written with no quoting — values containing the
// delimiter or a newline will produce an unparseable file.
func WriteDelim[T any](seq iter.Seq[T], filename string, opts ...DelimOption) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return WriteDelimToWriter(seq, f, opts...)
}

// WriteDelimToWriter is the [io.Writer] variant of [WriteDelim].
func WriteDelimToWriter[T any](seq iter.Seq[T], w io.Writer, opts ...DelimOption) error {
	o := resolveDelimOpts(opts)
	schema, err := buildWriteSchema[T]()
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	if err := writeDelimHeader(bw, schema.header, o.delim); err != nil {
		return err
	}
	row := make([]string, len(schema.encoders))
	for v := range seq {
		p := unsafe.Pointer(&v)
		for i, enc := range schema.encoders {
			row[i] = enc(p)
		}
		if err := writeDelimRow(bw, row, o.delim); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func writeDelimHeader(w *bufio.Writer, header []string, delim byte) error {
	for i, h := range header {
		if i > 0 {
			if err := w.WriteByte(delim); err != nil {
				return err
			}
		}
		if _, err := w.WriteString(h); err != nil {
			return err
		}
	}
	return w.WriteByte('\n')
}

func writeDelimRow(w *bufio.Writer, row []string, delim byte) error {
	for i, f := range row {
		if i > 0 {
			if err := w.WriteByte(delim); err != nil {
				return err
			}
		}
		if _, err := w.WriteString(f); err != nil {
			return err
		}
	}
	return w.WriteByte('\n')
}

// ReadDelimParallel reads a delimited-text file using n worker
// goroutines. Same shape and partitioning strategy as
// [ReadCSVParallel] — read whole file, scan newline byte offsets,
// partition lines across n shards — but each shard parses with the
// fast delim splitter instead of an `encoding/csv` reader. Header is
// parsed once before shards start.
//
// LIMITATION: assumes no fields with embedded delimiters or newlines.
// Use [ReadCSV] / [ReadCSVParallel] for RFC-4180-correct parsing.
//
// Memory cost: ~filesize bytes (whole file held in memory) plus
// ~8 bytes per line for the newline-offset index.
func ReadDelimParallel[T any](filename string, n int, opts ...DelimOption) Stream[T] {
	o := resolveDelimOpts(opts)
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	data, err := os.ReadFile(filename)
	if err != nil || len(data) == 0 {
		return Stream[T]{shards: nil, n: 0}
	}

	newlines := make([]int, 0, len(data)/64)
	for off := 0; off < len(data); {
		idx := bytes.IndexByte(data[off:], '\n')
		if idx < 0 {
			break
		}
		newlines = append(newlines, off+idx)
		off += idx + 1
	}
	if len(newlines) == 0 {
		return Stream[T]{shards: nil, n: 0}
	}

	headerEnd := newlines[0]
	header := splitLine(data[:headerEnd], o.delim, nil)
	schema, err := buildReadSchema[T](header, o.strict)
	if err != nil {
		return Stream[T]{shards: nil, n: 0}
	}

	numDataLines := len(newlines) - 1
	if data[len(data)-1] != '\n' {
		numDataLines++
	}
	if numDataLines == 0 {
		return Stream[T]{shards: nil, n: 0}
	}
	if n > numDataLines {
		n = numDataLines
	}

	linesPerShard := (numDataLines + n - 1) / n
	shards := make([]iter.Seq[T], n)
	delim := o.delim
	for i := 0; i < n; i++ {
		startLine := i * linesPerShard
		endLine := startLine + linesPerShard
		if endLine > numDataLines {
			endLine = numDataLines
		}
		if startLine >= endLine {
			shards[i] = func(yield func(T) bool) {}
			continue
		}
		startByte := newlines[startLine] + 1
		var endByte int
		if endLine <= len(newlines)-1 {
			endByte = newlines[endLine] + 1
		} else {
			endByte = len(data)
		}
		chunk := data[startByte:endByte]

		shards[i] = func(yield func(T) bool) {
			var row []string
			off := 0
			for off < len(chunk) {
				idx := bytes.IndexByte(chunk[off:], '\n')
				var line []byte
				if idx < 0 {
					line = chunk[off:]
					off = len(chunk)
				} else {
					line = chunk[off : off+idx]
					off += idx + 1
				}
				if len(line) == 0 {
					continue
				}
				// Zero-copy: each string aliases into `chunk`,
				// which aliases into `data`, which lives for the
				// duration of this closure. Safe because the
				// decoder either stores the string into a struct
				// field (still aliased into stable memory) or
				// passes it to strconv (which doesn't retain).
				row = splitLineAlias(line, delim, row)
				var rec T
				_ = schema.decode(unsafe.Pointer(&rec), row)
				if !yield(rec) {
					return
				}
			}
		}
	}
	return Stream[T]{shards: shards, n: n}
}

// WriteDelim writes a Stream[T] to a delimited-text file using the
// per-shard buffer-dump pattern: each shard formats its rows into its
// own bytes.Buffer concurrently, then the buffers are concatenated to
// the output in shard order. Same trade-offs as [Stream.WriteCSV] —
// peak memory ~2× output size; output is shard-concatenation order
// (within-shard order preserved).
func (s Stream[T]) WriteDelim(filename string, opts ...DelimOption) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return s.WriteDelimToWriter(f, opts...)
}

// WriteDelimToWriter is the [io.Writer] variant of [Stream.WriteDelim].
func (s Stream[T]) WriteDelimToWriter(w io.Writer, opts ...DelimOption) error {
	o := resolveDelimOpts(opts)
	schema, err := buildWriteSchema[T]()
	if err != nil {
		return err
	}

	bw := bufio.NewWriter(w)
	if err := writeDelimHeader(bw, schema.header, o.delim); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return err
	}

	buffers := make([]*bytes.Buffer, len(s.shards))
	errs := make([]error, len(s.shards))
	var wg sync.WaitGroup
	wg.Add(len(s.shards))
	delim := o.delim
	for i, shard := range s.shards {
		i, shard := i, shard
		go func() {
			defer wg.Done()
			buf := &bytes.Buffer{}
			buffers[i] = buf
			sbw := bufio.NewWriter(buf)
			row := make([]string, len(schema.encoders))
			for v := range shard {
				p := unsafe.Pointer(&v)
				for j, enc := range schema.encoders {
					row[j] = enc(p)
				}
				if err := writeDelimRow(sbw, row, delim); err != nil {
					errs[i] = err
					return
				}
			}
			if err := sbw.Flush(); err != nil {
				errs[i] = err
			}
		}()
	}
	wg.Wait()

	for i, buf := range buffers {
		if errs[i] != nil {
			return errs[i]
		}
		if _, err := w.Write(buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}
