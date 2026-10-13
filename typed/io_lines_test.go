package typed

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// WriteLines is ReadLines' twin: text(row) per row, one line each; a
// file written by one reads back through the other.
func TestWriteLines(t *testing.T) {
	rows := slices.Values([]Line{{1, "a"}, {2, ""}, {3, "c"}})
	text := func(l Line) string { return l.Line }

	var buf bytes.Buffer
	if err := WriteLinesToWriter(rows, text, &buf); err != nil || buf.String() != "a\n\nc\n" {
		t.Errorf("writer: %v %q", err, buf.String())
	}

	p := filepath.Join(t.TempDir(), "out.txt")
	if err := WriteLines(rows, text, p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "a\n\nc\n" {
		t.Errorf("file: %q", b)
	}
	back := slices.Collect(ReadLines(p))
	if len(back) != 3 || back[1] != (Line{2, ""}) || back[2] != (Line{3, "c"}) {
		t.Errorf("round trip: %+v", back)
	}
}
