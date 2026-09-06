package typed

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type jsonRow struct {
	Name   string  `json:"name"`
	Age    int64   `json:"age"`
	Salary float64 `json:"salary"`
}

func TestReadJSONLBasic(t *testing.T) {
	src := `{"name":"Alice","age":30,"salary":95000.5}
{"name":"Bob","age":25,"salary":65000}
`
	got := slices.Collect(ReadJSONLFromReader[jsonRow](strings.NewReader(src)))
	want := []jsonRow{
		{Name: "Alice", Age: 30, Salary: 95000.5},
		{Name: "Bob", Age: 25, Salary: 65000},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestReadJSONLSkipsBlankLines(t *testing.T) {
	src := "\n{\"name\":\"Alice\"}\n\n{\"name\":\"Bob\"}\n\n"
	got := slices.Collect(ReadJSONLFromReader[jsonRow](strings.NewReader(src)))
	if len(got) != 2 {
		t.Errorf("expected 2 rows ignoring blanks, got %d (%#v)", len(got), got)
	}
}

func TestReadJSONLLossy(t *testing.T) {
	src := `{"name":"Alice","age":30}
{not json}
{"name":"Bob","age":25}
`
	got := slices.Collect(ReadJSONLFromReader[jsonRow](strings.NewReader(src)))
	if len(got) != 2 {
		t.Errorf("lossy reader should skip bad lines, got %d (%#v)", len(got), got)
	}
}

func TestReadJSONLSafeReportsErrors(t *testing.T) {
	src := `{"name":"Alice","age":30}
{not json}
{"name":"Bob","age":25}
`
	var rows []jsonRow
	var errs []error
	for r, err := range ReadJSONLSafeFromReader[jsonRow](strings.NewReader(src)) {
		rows = append(rows, r)
		errs = append(errs, err)
	}
	if len(errs) != 3 {
		t.Fatalf("expected 3 yielded items, got %d", len(errs))
	}
	good := 0
	for _, e := range errs {
		if e == nil {
			good++
		}
	}
	if good != 2 {
		t.Errorf("expected 2 successful rows, got %d", good)
	}
}

func TestWriteJSONL(t *testing.T) {
	rows := []jsonRow{
		{Name: "Alice", Age: 30, Salary: 95000.5},
		{Name: "Bob", Age: 25, Salary: 65000},
	}
	var buf bytes.Buffer
	if err := WriteJSONLToWriter(slices.Values(rows), &buf); err != nil {
		t.Fatal(err)
	}
	want := `{"name":"Alice","age":30,"salary":95000.5}
{"name":"Bob","age":25,"salary":65000}
`
	if buf.String() != want {
		t.Errorf("write mismatch:\n  got:  %q\n  want: %q", buf.String(), want)
	}
}

func TestJSONLRoundTripFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rows.jsonl")
	in := []jsonRow{
		{Name: "Alice", Age: 30, Salary: 95000.5},
		{Name: "Bob", Age: 25, Salary: 65000},
		{Name: "Carol", Age: 42, Salary: 105000},
	}
	if err := WriteJSONL(slices.Values(in), path); err != nil {
		t.Fatal(err)
	}
	out := slices.Collect(ReadJSONL[jsonRow](path))
	if !slices.Equal(in, out) {
		t.Errorf("round-trip mismatch:\n  in:  %#v\n  out: %#v", in, out)
	}
}

// A leading `_schema` header line (tee, every ssql stage) is metadata:
// both readers skip it instead of yielding a zero-valued phantom row.
func TestReadJSONLSkipsSchemaHeader(t *testing.T) {
	type row struct {
		Name string `json:"name"`
		Age  int64  `json:"age"`
	}
	in := "{\"_schema\":{\"fields\":[\"name\",\"age\"],\"types\":{\"name\":\"string\",\"age\":\"int\"}}}\n{\"name\":\"Alice\",\"age\":35}\n{\"name\":\"Bob\",\"age\":28}\n"
	got := slices.Collect(ReadJSONLFromReader[row](strings.NewReader(in)))
	if len(got) != 2 || got[0].Name != "Alice" || got[1].Age != 28 {
		t.Errorf("ReadJSONL with header: got %+v", got)
	}
	n := 0
	for r, err := range ReadJSONLSafeFromReader[row](strings.NewReader(in)) {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if r.Name == "" {
			t.Errorf("phantom zero row from the header line")
		}
		n++
	}
	if n != 2 {
		t.Errorf("safe reader yielded %d rows, want 2", n)
	}
}
