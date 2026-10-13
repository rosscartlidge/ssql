package ssql

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// WriteLines renders one field per record by FormatValue: strings as
// they are, a JSONString as its JSON (a views table's pipeline column
// feeds `run -stdin`), times RFC 3339, numbers shortest, nil an empty
// line. An unknown field is an error naming the fields; a field absent
// from a later record is an empty line.
func TestWriteLinesToWriter(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	schema := NewSchema([]string{"name", "doc", "when", "n", "f", "b"})
	records := slices.Values([]Record{
		NewRecordFromSchema(schema, []any{"a", JSONString(`[["from","csv","x.csv"]]`), ts, int64(1), 2.5, true}),
		NewRecordFromSchema(schema, []any{"b", nil, nil, int64(2), 3.0, false}),
	})
	for field, want := range map[string]string{
		"name": "a\nb\n",
		"doc":  `[["from","csv","x.csv"]]` + "\n\n",
		"when": "2026-01-02T03:04:05Z\n\n",
		"n":    "1\n2\n",
		"f":    "2.5\n3\n",
		"b":    "true\nfalse\n",
	} {
		var buf bytes.Buffer
		if err := WriteLinesToWriter(records, field, &buf); err != nil {
			t.Errorf("%s: %v", field, err)
			continue
		}
		if buf.String() != want {
			t.Errorf("%s: got %q, want %q", field, buf.String(), want)
		}
	}

	var buf bytes.Buffer
	err := WriteLinesToWriter(records, "nope", &buf)
	if err == nil || !strings.Contains(err.Error(), `unknown field "nope"`) || !strings.Contains(err.Error(), "available: name, doc, when") {
		t.Errorf("unknown field: got %v", err)
	}

	// A field the second record lacks (headerless JSONL with a gap).
	gappy := slices.Values([]Record{
		NewRecordFromSchema(NewSchema([]string{"id", "v"}), []any{int64(1), "x"}),
		NewRecordFromSchema(NewSchema([]string{"id"}), []any{int64(2)}),
	})
	buf.Reset()
	if err := WriteLinesToWriter(gappy, "v", &buf); err != nil || buf.String() != "x\n\n" {
		t.Errorf("absent in a later record: %v %q", err, buf.String())
	}

	// To a file; empty input writes an empty file.
	p := filepath.Join(t.TempDir(), "out.txt")
	if err := WriteLines(records, "name", p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "a\nb\n" {
		t.Errorf("file: %q", b)
	}
	if err := WriteLines(slices.Values([]Record{}), "name", p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); len(b) != 0 {
		t.Errorf("empty input: %q", b)
	}
}
