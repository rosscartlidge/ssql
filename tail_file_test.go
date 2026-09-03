package ssql

import (
	"os"
	"strconv"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func idsOf(seq func(func(Record) bool)) []int64 {
	var out []int64
	for r := range seq {
		out = append(out, GetOr(r, "id", int64(-1)))
	}
	return out
}

func TestTailCSVFileMatchesTakeLast(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("v,id\n")
	for i := 1; i <= 5000; i++ { // well over one 64KB backward chunk
		sb.WriteString(strings.Repeat("x", 20))
		sb.WriteString(",")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString("\n")
	}
	p := writeTemp(t, "big.csv", sb.String())
	for _, n := range []int{1, 3, 100, 4999, 5000, 6000} {
		got, err := TailCSVFile(p, n)
		if err != nil {
			t.Fatal(err)
		}
		full, _ := ReadCSV(p)
		want := idsOf(TakeLast[Record](n)(full))
		g := idsOf(got)
		if len(g) != len(want) {
			t.Fatalf("n=%d: len %d vs %d", n, len(g), len(want))
		}
		for i := range want {
			if g[i] != want[i] {
				t.Fatalf("n=%d: row %d = %d, want %d", n, i, g[i], want[i])
			}
		}
	}
}

func TestTailCSVFileEdges(t *testing.T) {
	// No trailing newline; CRLF; header-only; n larger than file.
	p := writeTemp(t, "a.csv", "id,v\r\n1,a\r\n2,b\r\n3,c")
	got, _ := TailCSVFile(p, 2)
	if ids := idsOf(got); len(ids) != 2 || ids[0] != 2 || ids[1] != 3 {
		t.Errorf("CRLF/no-trailing-newline: %v", ids)
	}
	p = writeTemp(t, "h.csv", "id,v\n")
	got, _ = TailCSVFile(p, 3)
	if ids := idsOf(got); len(ids) != 0 {
		t.Errorf("header-only: %v", ids)
	}
	// Types come from the tail rows: v is numeric in the tail even if
	// the first data row (never read) was text.
	p = writeTemp(t, "t.csv", "id,v\n1,text\n2,10\n3,20\n")
	got, _ = TailCSVFile(p, 2)
	for r := range got {
		if _, ok := Get[int64](r, "v"); !ok {
			t.Errorf("tail-row type inference: v should be int64, got %v", r)
		}
	}
}

func TestTailJSONLFile(t *testing.T) {
	p := writeTemp(t, "a.jsonl", `{"_schema":{"fields":["id"],"types":{"id":"int"}}}`+"\n"+`{"id":1}`+"\n"+`{"id":2}`+"\n"+`{"id":3}`+"\n")
	got, err := TailJSONLFile(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ids := idsOf(got); len(ids) != 2 || ids[0] != 2 || ids[1] != 3 {
		t.Errorf("jsonl tail: %v", ids)
	}
	got, _ = TailJSONLFile(p, 10)
	if ids := idsOf(got); len(ids) != 3 {
		t.Errorf("jsonl tail beyond size (schema line must not count): %v", ids)
	}
}
