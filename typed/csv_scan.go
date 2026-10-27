package typed

import (
	"bytes"
	"unsafe"
)

// The zero-copy CSV line scanner for ReadCSVParallel (2026-10-27).
//
// encoding/csv builds one record buffer per row and slices every field's
// string out of it: on a 1.23 GB, 14.6 M-row file that is 14.6 M
// allocations and ~1.2 GB of garbage, and the collector's write barriers
// and span scans were a third of the read's CPU, encoding/csv itself
// another half (DFC157 §4 measured the typed CSV group-by at 1.63 s
// against DataFusion's 0.24 s). The parallel reader already holds the
// whole file as one mapped []byte and knows every newline offset, so a
// line can be split by bytes.IndexByte (SIMD on amd64) into fields that
// VIEW the mapping — quoted fields included, a doubled quote unescaped
// into a per-shard scratch buffer — so there is no allocation at all for
// the columns parsed into numbers, times and bools, one strings.Clone
// per kept string column, nothing for columns the struct does not have.
// Only a malformed line goes through encoding/csv, for its error. (The
// first cut sent every line WITH a quote to encoding/csv and was 2×
// slower than before: half the lines of the benchmark file quote a name
// that holds a comma, and each fallback built a 4 KB reader.) DFC084 §1 records the first byte-level
// reader (2026-04) that lost to encoding/csv: it read byte by byte
// through bufio on the serial path; this one is the "SIMD delimiter
// scan" that note named as the remaining real win.

// splitCSVLine splits one line into fields that view the line's bytes (a
// quoted field with no doubled quote views the bytes inside the quotes;
// one with doubled quotes is unescaped into scratch and views that). ok
// is false when the line is not well-formed — an unterminated quote, a
// bare quote in an unquoted field, text after a closing quote — and the
// caller hands the line to encoding/csv, whose error names the fault.
// Embedded newlines cannot occur: the line was cut at a newline, as the
// parallel reader has always done. The caller must copy any field it
// keeps beyond the line's life: the views alias the file mapping or the
// scratch buffer.
func splitCSVLine(line []byte, fields []string, scratch *[]byte) ([]string, bool) {
	fields = fields[:0]
	if cap(*scratch) < len(line) {
		*scratch = make([]byte, 0, 2*len(line))
	}
	*scratch = (*scratch)[:0]
	for {
		if len(line) > 0 && line[0] == '"' {
			rest := line[1:]
			start := len(*scratch)
			escaped := false
			for {
				i := bytes.IndexByte(rest, '"')
				if i < 0 {
					return fields, false // unterminated quote
				}
				if i+1 < len(rest) && rest[i+1] == '"' {
					*scratch = append(*scratch, rest[:i+1]...) // the doubled quote is one quote
					escaped = true
					rest = rest[i+2:]
					continue
				}
				if escaped {
					*scratch = append(*scratch, rest[:i]...)
					fields = append(fields, byteView((*scratch)[start:]))
				} else {
					fields = append(fields, byteView(rest[:i]))
				}
				rest = rest[i+1:]
				break
			}
			if len(rest) == 0 {
				return fields, true
			}
			if rest[0] != ',' {
				return fields, false // text after the closing quote
			}
			line = rest[1:]
			continue
		}
		i := bytes.IndexByte(line, ',')
		if i < 0 {
			if bytes.IndexByte(line, '"') >= 0 {
				return fields, false // a bare quote in an unquoted field
			}
			return append(fields, byteView(line)), true
		}
		if bytes.IndexByte(line[:i], '"') >= 0 {
			return fields, false
		}
		fields = append(fields, byteView(line[:i]))
		line = line[i+1:]
	}
}

// byteView is the string that views b, allocation-free.
func byteView(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}
