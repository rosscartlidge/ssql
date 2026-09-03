package ssql

// Seek-based tail at the source (`ssql from csv FILE -last N`): read
// backwards from EOF until N+1 newlines, parse only those lines under
// the header's schema. Cost is O(N lines) regardless of file size —
// the mirror image of byte-offset sampling (sample_file.go). Results
// are IDENTICAL to `from FILE | limit -last N` (both are "the last N
// data lines in file order"); only the cost differs. Shared caveat with
// -sample: backward scanning assumes newline-terminated records, so a
// quoted field containing a newline can split wrong.

import (
	"bytes"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"
)

// tailLines returns the last n lines of [dataStart, size) as raw byte
// slices (no trailing newline), in file order. Fewer than n lines →
// all of them. Reads backwards in chunks; never touches the middle of
// the file.
func tailLines(f io.ReaderAt, size, dataStart int64, n int) ([][]byte, error) {
	if n <= 0 || size <= dataStart {
		return nil, nil
	}
	const chunk = 64 * 1024
	var acc []byte // tail bytes accumulated so far (file order)
	end := size
	newlines := 0
	for end > dataStart {
		start := end - chunk
		if start < dataStart {
			start = dataStart
		}
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return nil, err
		}
		acc = append(buf, acc...)
		end = start
		// Count newlines excluding a single trailing one at EOF.
		newlines = bytes.Count(acc, []byte{'\n'})
		if len(acc) > 0 && acc[len(acc)-1] == '\n' {
			newlines--
		}
		if newlines >= n {
			break
		}
	}
	acc = bytes.TrimRight(acc, "\r\n")
	if len(acc) == 0 {
		return nil, nil
	}
	parts := bytes.Split(acc, []byte{'\n'})
	if len(parts) > n {
		parts = parts[len(parts)-n:]
	}
	for i := range parts {
		parts[i] = bytes.TrimRight(parts[i], "\r")
	}
	return parts, nil
}

// openForTail opens the file and finds where data starts (after the
// header line when hasHeader).
func openForTail(filename string, hasHeader bool) (*os.File, int64, string, int64, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, 0, "", 0, fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, "", 0, err
	}
	size := st.Size()
	if !hasHeader {
		return f, size, "", 0, nil
	}
	head := make([]byte, 64*1024)
	m, _ := io.ReadFull(f, head)
	head = head[:m]
	nl := bytes.IndexByte(head, '\n')
	if nl < 0 {
		return f, size, strings.TrimRight(string(head), "\r"), size, nil // header only
	}
	return f, size, strings.TrimRight(string(head[:nl]), "\r"), int64(nl + 1), nil
}

// TailCSVFile returns the last n data lines of a header-bearing CSV,
// parsed under the header's schema (types inferred from these lines).
// URLs and other non-seekable sources fall back to a full streaming
// read — same result, no speed-up.
func TailCSVFile(filename string, n int, config ...CSVConfig) (iter.Seq[Record], error) {
	cfg := DefaultCSVConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	if IsHTTPURL(filename) {
		body, err := OpenHTTPStream(filename)
		if err != nil {
			return nil, err
		}
		return tailStream(body, n, func(r io.Reader) iter.Seq[Record] { return ReadCSVFromReader(r, cfg) }), nil
	}
	f, size, header, dataStart, err := openForTail(filename, true)
	if err != nil {
		return nil, err
	}
	lines, err := tailLines(f, size, dataStart, n)
	f.Close()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(header)
	buf.WriteByte('\n')
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	return ReadCSVFromReader(&buf, cfg), nil
}

// TailTSVFile is TailCSVFile for TSV (delimiter auto-detected from the
// header, as every TSV path does).
func TailTSVFile(filename string, n int) (iter.Seq[Record], error) {
	if IsHTTPURL(filename) {
		body, err := OpenHTTPStream(filename)
		if err != nil {
			return nil, err
		}
		return tailStream(body, n, ReadTSVFromReader), nil
	}
	f, size, header, dataStart, err := openForTail(filename, true)
	if err != nil {
		return nil, err
	}
	lines, err := tailLines(f, size, dataStart, n)
	f.Close()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(header)
	buf.WriteByte('\n')
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	return ReadTSVFromReader(&buf), nil
}

// TailJSONLFile returns the last n records of a JSONL file (each line
// is self-describing, so no header replay is needed; a leading
// `_schema` line is never in the tail unless the file is tiny, and the
// JSONL reader skips it anyway).
func TailJSONLFile(filename string, n int) (iter.Seq[Record], error) {
	if IsHTTPURL(filename) {
		body, err := OpenHTTPStream(filename)
		if err != nil {
			return nil, err
		}
		return tailStream(body, n, ReadJSONLFromReader), nil
	}
	f, size, _, _, err := openForTail(filename, false)
	if err != nil {
		return nil, err
	}
	// Ask for one extra line so a `_schema` header landing in the tail
	// of a tiny file doesn't cost a data record.
	lines, err := tailLines(f, size, 0, n+1)
	f.Close()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	return TakeLast[Record](n)(ReadJSONLFromReader(&buf)), nil
}

// tailStream is the non-seekable fallback: a full streaming read kept
// to the last n records (same result as the seek path, no speed-up).
func tailStream(body io.ReadCloser, n int, read func(io.Reader) iter.Seq[Record]) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		defer body.Close()
		for r := range TakeLast[Record](n)(read(body)) {
			if !yield(r) {
				return
			}
		}
	}
}
