package ssql

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// Native fuzz targets (DFC133 instrument 4). Each asserts a property that
// is unambiguous without an oracle: the function does not panic, and a
// value it ACCEPTS survives a round trip. Run one at a time:
//
//	go test . -run '^$' -fuzz FuzzParseJSONLine -fuzztime 60s
//
// A crasher is written to testdata/fuzz/<Target>/ and becomes a permanent
// regression input for the ordinary test run.

var fuzzJSONSeeds = []string{
	`{"id":1,"name":"Alice","score":2.5,"ok":true,"note":null}`,
	`{"a":{"b":[1,2,{"c":null}]},"e":""}`,
	`{"big":12345678901234567890,"neg":-0,"exp":1e400,"u":"é😀"}`,
	`{"_schema":{"fields":["a"],"types":{"a":"int"}}}`,
	`{"dup":1,"dup":2}`, `{}`, `[]`, `{"a":`, `{"a":1}trailing`, ` { "a" : 1 } `,
}

// FuzzParseJSONLine: no panic on any input, for both the null-dropping and
// the null-keeping parser; whatever parses re-serialises to a line that
// parses to the same serialisation (a fixed point), and the two parsers
// agree on every non-null field.
func FuzzParseJSONLine(f *testing.F) {
	for _, s := range fuzzJSONSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		m, err := ParseJSONLine(line)
		mn, errN := ParseJSONLineWithNulls(line)
		if (err == nil) != (errN == nil) {
			t.Fatalf("the two parsers disagree on validity: %v vs %v for %q", err, errN, line)
		}
		if err != nil {
			return
		}
		rec, recN := m.Freeze(), mn.Freeze()
		out := rec.AppendJSON(nil)
		if outN := recN.AppendJSON(nil); !bytes.Equal(out, outN) {
			t.Fatalf("keeping nulls changed the written record:\n  %s\n  %s", out, outN)
		}
		again, err := ParseJSONLine(out)
		if err != nil {
			t.Fatalf("ssql cannot read what it wrote: %v\n  in:  %q\n  out: %s", err, line, out)
		}
		if out2 := again.Freeze().AppendJSON(nil); !bytes.Equal(out, out2) {
			t.Fatalf("not a fixed point:\n  1: %s\n  2: %s", out, out2)
		}
	})
}

// FuzzParseTime: no panic; a string it accepts is an instant that survives
// RFC 3339 — the form ssql writes — exactly.
func FuzzParseTime(f *testing.F) {
	for _, s := range []string{"2026-01-02T10:30:00Z", "2026-01-02 10:30:00", "2026-01-02T10:30:00", "2026-01-01 23:30:00+00",
		"2026-01-02", "2026-13-45", "0000-01-01", "9999-12-31T23:59:59.999999999+14:00", "", "banana", "2026-01-02T10:30:00+99:99"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		tm, ok := ParseTime(s)
		if !ok {
			return
		}
		back, ok2 := ParseTime(tm.Format(time.RFC3339Nano))
		if !ok2 || !back.Equal(tm) {
			t.Fatalf("ParseTime(%q) = %v does not survive RFC 3339 (%v, %v)", s, tm, back, ok2)
		}
	})
}

// FuzzReadCSV: the reader never panics with anything but its own
// *CellError (the documented fail-fast for a cell that does not fit its
// column — the CLI turns it into an error), every record has the header's
// width, and reading is deterministic.
func FuzzReadCSV(f *testing.F) {
	for _, s := range []string{"a,b\n1,2\n", "a,b\n1\n", "a,a\n1,2\n", "\n", "a\n\"x\ny\"\n", "a,b\n1,x\n2.5,\n", "\xef\xbb\xbfa,b\n1,2\n",
		"a,b\r\n1,2\r\n", "a;b\n1;2\n", "a,b\n\"unterminated\n", "# comment\na,b\n1,2\n", ",\n,\n"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		read := func() (n int, width int) {
			defer func() {
				if r := recover(); r != nil {
					// The two documented failures: a cell not of its column's
					// type, and a row that is not a row (a malformed row used
					// to END the read quietly; since 2026-09-22 it is loud).
					var ce *CellError
					var re *RowError
					if e, ok := r.(error); !ok || !(errors.As(e, &ce) || errors.As(e, &re)) {
						panic(r) // anything else is a finding
					}
					n, width = -1, -1
				}
			}()
			for r := range ReadCSVFromReader(bytes.NewReader(data)) {
				if n == 0 {
					width = r.Len()
				} else if r.Len() != width {
					t.Fatalf("record %d has %d fields, the first had %d", n+1, r.Len(), width)
				}
				n++
			}
			return n, width
		}
		n1, w1 := read()
		n2, w2 := read()
		if n1 != n2 || w1 != w2 {
			t.Fatalf("reading the same bytes twice gave %d×%d then %d×%d", n1, w1, n2, w2)
		}
	})
}
