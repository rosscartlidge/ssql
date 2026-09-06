package typed

import (
	"bytes"
	"encoding/json"
	"iter"
	"runtime"
	"unsafe"

	"github.com/rosscartlidge/ssql/v4/internal/mmap"
)

// ReadJSONLParallel reads a JSON-Lines file as a Stream[T] with n shards
// (n <= 0 → GOMAXPROCS), each decoding a contiguous run of lines with
// the positional decoder — typed-codegen roadmap §9, step 3, the JSONL
// twin of [ReadCSVParallel].
//
// Mechanics: mmap the file, index every newline with bytes.IndexByte
// (SIMD on amd64), skip a leading `_schema` header line, split the data
// lines into n equal runs, and give each shard its byte range. Blank
// lines are skipped and, as in [ReadJSONL], a line that fails to decode
// is dropped. The positional decoder copies every string it stores and
// parses numbers immediately, so yielded structs never alias the
// mapping; each shard pins it with runtime.KeepAlive until it is done.
// A type the positional plan cannot cover decodes with encoding/json in
// each shard instead.
//
// Order: rows within a shard keep file order; the Stream's consumers
// (GroupByParallel, per-shard sinks) define the cross-shard order, as
// for the CSV twin. Memory: ~file size (the mapping) plus 8 bytes per
// line for the index.
func ReadJSONLParallel[T any](filename string, n int) Stream[T] {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	m, err := mmap.Map(filename)
	if err != nil {
		return Stream[T]{}
	}
	data := m.Data
	if len(data) == 0 {
		return Stream[T]{}
	}

	// Newline index; an unterminated last line counts too.
	newlines := make([]int, 0, len(data)/64)
	for off := 0; off < len(data); {
		idx := bytes.IndexByte(data[off:], '\n')
		if idx < 0 {
			break
		}
		newlines = append(newlines, off+idx)
		off += idx + 1
	}
	// lineStart[k] .. lineEnd[k] bounds data line k (exclusive of '\n').
	numLines := len(newlines)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		numLines++
	}
	lineStart := func(k int) int {
		if k == 0 {
			return 0
		}
		return newlines[k-1] + 1
	}
	lineEnd := func(k int) int {
		if k < len(newlines) {
			return newlines[k]
		}
		return len(data)
	}

	// A leading `_schema` header describes the rows; it is not one.
	first := 0
	if numLines > 0 && isSchemaHeaderLine(bytes.TrimLeft(data[lineStart(0):lineEnd(0)], " \t\r")) {
		first = 1
	}
	dataLines := numLines - first
	if dataLines <= 0 {
		runtime.KeepAlive(m)
		return Stream[T]{}
	}
	if n > dataLines {
		n = dataLines
	}
	perShard := (dataLines + n - 1) / n

	plan, perr := buildJSONLPlan[T]()

	shards := make([]iter.Seq[T], n)
	for i := 0; i < n; i++ {
		lo := first + i*perShard
		hi := lo + perShard
		if hi > numLines {
			hi = numLines
		}
		if lo >= hi {
			shards[i] = func(yield func(T) bool) {}
			continue
		}
		chunk := data[lineStart(lo):lineEnd(hi-1)]
		shards[i] = func(yield func(T) bool) {
			defer runtime.KeepAlive(m)
			rest := chunk
			for len(rest) > 0 {
				var line []byte
				if nl := bytes.IndexByte(rest, '\n'); nl >= 0 {
					line, rest = rest[:nl], rest[nl+1:]
				} else {
					line, rest = rest, nil
				}
				line = bytes.TrimSpace(line)
				if len(line) == 0 {
					continue
				}
				var row T
				var derr error
				if perr == nil {
					derr = plan.decode(line, unsafe.Pointer(&row))
				} else {
					derr = json.Unmarshal(line, &row)
				}
				if derr != nil {
					continue
				}
				if !yield(row) {
					return
				}
			}
		}
	}
	runtime.KeepAlive(m)
	return Stream[T]{shards: shards, n: n}
}
