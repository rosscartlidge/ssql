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
	for _, want := range []string{`"fields":["age","name"]`, `"age":"int"`, `"name":"string"`} {
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
