package ssql

// Tests for WriteJSONLWithInferredSchemaToWriter — public helper
// that emits JSONL prefixed with a {"_schema":...} header inferred
// from the first record. Used by `ssql generate go`'s no-sink
// JSONL fallback so output matches the wire format the rest of
// the CLI produces.

import (
	"bytes"
	"iter"
	"strings"
	"testing"
)

func TestWriteJSONLWithInferredSchema_BasicShape(t *testing.T) {
	records := iter.Seq[Record](func(yield func(Record) bool) {
		for _, r := range []struct {
			name string
			age  int64
		}{
			{"Alice", 30},
			{"Bob", 25},
		} {
			rec := MakeMutableRecord().
				String("name", r.name).
				Int("age", r.age).
				Freeze()
			if !yield(rec) {
				return
			}
		}
	})
	var buf bytes.Buffer
	if err := WriteJSONLWithInferredSchemaToWriter(records, &buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (schema + 2 records), got %d:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], `{"_schema":`) {
		t.Errorf("first line should be schema header; got: %s", lines[0])
	}
	// Fields in the order the records were built (name, then age), not
	// sorted: MutableRecord keeps insertion order since 2026-09-23.
	for _, want := range []string{`"fields":["name","age"]`, `"age":"int"`, `"name":"string"`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("schema line missing %q; got: %s", want, lines[0])
		}
	}
	if !strings.Contains(lines[1], `"name":"Alice"`) {
		t.Errorf("first record line wrong: %s", lines[1])
	}
}

func TestWriteJSONLWithInferredSchema_EmptyInput(t *testing.T) {
	// Empty input: no header, no records, no error.
	empty := iter.Seq[Record](func(yield func(Record) bool) {})
	var buf bytes.Buffer
	if err := WriteJSONLWithInferredSchemaToWriter(empty, &buf); err != nil {
		t.Fatalf("empty input write: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output for empty input, got %q", buf.String())
	}
}

func TestReadJSONLFromReader_RoundTrip(t *testing.T) {
	// Round-trip: write a record stream with WriteJSONLWithInferredSchemaToWriter,
	// read it back with ReadJSONLFromReader. The reader strips the
	// `_schema` header and uses it for type coercion. Records should
	// match (modulo field iteration order).
	original := []map[string]any{
		{"name": "Alice", "age": int64(30)},
		{"name": "Bob", "age": int64(25)},
	}
	source := iter.Seq[Record](func(yield func(Record) bool) {
		for _, m := range original {
			rec := MakeMutableRecord().
				String("name", m["name"].(string)).
				Int("age", m["age"].(int64)).
				Freeze()
			if !yield(rec) {
				return
			}
		}
	})
	var buf bytes.Buffer
	if err := WriteJSONLWithInferredSchemaToWriter(source, &buf); err != nil {
		t.Fatalf("write: %v", err)
	}

	var got []map[string]any
	for r := range ReadJSONLFromReader(&buf) {
		row := make(map[string]any)
		for k, v := range r.All() {
			row[k] = v
		}
		got = append(got, row)
	}
	if len(got) != len(original) {
		t.Fatalf("round-trip lost records: got %d, want %d\n%v", len(got), len(original), got)
	}
	// No `_line_number` should have been added by the reader — that's
	// the whole point of ReadJSONLFromReader vs ReadJSONFromReader.
	for i, row := range got {
		if _, has := row["_line_number"]; has {
			t.Errorf("row %d unexpectedly has _line_number: %v", i, row)
		}
		if name, _ := row["name"].(string); name != original[i]["name"].(string) {
			t.Errorf("row %d name mismatch: got %v, want %v", i, name, original[i]["name"])
		}
	}
}

func TestReadJSONLFromReader_NoSchemaHeader(t *testing.T) {
	// When the input has no schema header (just raw JSONL), the
	// reader still parses records correctly. Type-inference is
	// per-record (less efficient but correct).
	input := `{"name":"Alice","age":30}` + "\n" + `{"name":"Bob","age":25}` + "\n"
	var got []string
	for r := range ReadJSONLFromReader(strings.NewReader(input)) {
		name, _ := Get[string](r, "name")
		got = append(got, name)
	}
	want := []string{"Alice", "Bob"}
	if len(got) != len(want) {
		t.Fatalf("expected %d records, got %d", len(want), len(got))
	}
	for i, name := range got {
		if name != want[i] {
			t.Errorf("record %d: got %q, want %q", i, name, want[i])
		}
	}
}

func TestWriteJSONLWithInferredSchema_TypeInference(t *testing.T) {
	// Cover all the types in inferJSONType.
	rec := MakeMutableRecord().
		String("s", "hi").
		Int("i", 42).
		Float("f", 3.14).
		Bool("b", true).
		Freeze()
	records := iter.Seq[Record](func(yield func(Record) bool) {
		yield(rec)
	})
	var buf bytes.Buffer
	if err := WriteJSONLWithInferredSchemaToWriter(records, &buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	header := strings.SplitN(buf.String(), "\n", 2)[0]
	for _, want := range []string{`"s":"string"`, `"i":"int"`, `"f":"float"`, `"b":"bool"`} {
		if !strings.Contains(header, want) {
			t.Errorf("header missing type %q; got: %s", want, header)
		}
	}
}

// TestTableMaxWidthZeroAndSmall: -max-width 0 shows every value in full;
// a cap ≥ 3 truncates with "..."; 1–3 hard-truncate; nothing panics.
func TestTableMaxWidthZeroAndSmall(t *testing.T) {
	recs := []Record{NewRecord(map[string]any{"name": "Alexandria the Great", "n": int64(1)})}
	render := func(w int) string {
		var b strings.Builder
		DisplayTableWithFieldsTo(&b, func(yield func(Record) bool) {
			for _, r := range recs {
				if !yield(r) {
					return
				}
			}
		}, w, []string{"name", "n"}, true)
		return b.String()
	}
	if out := render(0); !strings.Contains(out, "Alexandria the Great") {
		t.Fatalf("maxWidth 0 must not truncate:\n%s", out)
	}
	if out := render(8); !strings.Contains(out, "Alexa...") || strings.Contains(out, "Alexandria") {
		t.Fatalf("maxWidth 8 must truncate to 5 chars + ...:\n%s", out)
	}
	for _, w := range []int{1, 2, 3, -1} {
		out := render(w) // must not panic
		if strings.Contains(out, "Alexandria the Great") && w > 0 {
			t.Fatalf("maxWidth %d should hard-truncate:\n%s", w, out)
		}
	}
	if truncateCell("abcdef", 3) != "abc" || truncateCell("abcdef", 6) != "abcdef" || truncateCell("abcdefg", 6) != "abc..." {
		t.Fatal("truncateCell rules")
	}
}
