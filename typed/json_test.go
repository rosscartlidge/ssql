package typed

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

type arrayRow struct {
	ID   int64   `json:"id"`
	Name string  `json:"name"`
	Addr string  `json:"addr"` // a nested value: raw text, as the CLI's json type
	Tags string  `json:"tags"`
	Pct  float64 `json:"pct"`
}

func writeTempJSON(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rows.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadJSONArrayBasic(t *testing.T) {
	p := writeTempJSON(t, `
	[ {"id": 1, "name": "Alice", "addr": {"city": "NYC"}, "tags": ["go","rust"], "pct": 0.5},
	  {"id": 2, "name": "Bob", "tags": []},
	  {"id": 3, "name": "Carol", "addr": null, "pct": 1} ]
	`)
	got := slices.Collect(ReadJSON[arrayRow](p))
	want := []arrayRow{
		{1, "Alice", `{"city": "NYC"}`, `["go","rust"]`, 0.5},
		{2, "Bob", "", "[]", 0},
		{3, "Carol", "", "", 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ReadJSON:\n got %+v\nwant %+v", got, want)
	}
}

func TestReadJSONArrayEmptyAndEmptyArray(t *testing.T) {
	for name, body := range map[string]string{"empty file": "", "empty array": "[]", "spaced": "  [ \n ]\n"} {
		p := writeTempJSON(t, body)
		if n := len(slices.Collect(ReadJSON[arrayRow](p))); n != 0 {
			t.Errorf("%s: got %d rows, want 0", name, n)
		}
	}
}

func TestReadJSONArrayRefusesJSONL(t *testing.T) {
	p := writeTempJSON(t, "{\"id\":1}\n{\"id\":2}\n")
	err := recoverReadError(t, func() {
		for range ReadJSON[arrayRow](p) {
		}
	})
	if err == nil || !strings.Contains(err.Error(), "not a JSON array") {
		t.Fatalf("want 'not a JSON array' ReadError, got %v", err)
	}
	if err.Op != "typed.ReadJSON" || err.Source != p || err.Row != 0 {
		t.Errorf("ReadError fields: %+v", err)
	}
}

func TestReadJSONArrayBadElementNamesIt(t *testing.T) {
	p := writeTempJSON(t, `[{"id":1},{"id":2},{"id":"two"}]`)
	err := recoverReadError(t, func() {
		for range ReadJSON[arrayRow](p) {
		}
	})
	if err == nil || err.Row != 3 {
		t.Fatalf("want ReadError at element 3, got %v", err)
	}
	if !strings.Contains(err.Error(), "row 3") {
		t.Errorf("message should name the element: %v", err)
	}
}

func TestReadJSONArrayNonObjectElement(t *testing.T) {
	p := writeTempJSON(t, `[{"id":1}, 42]`)
	err := recoverReadError(t, func() {
		for range ReadJSON[arrayRow](p) {
		}
	})
	if err == nil || err.Row != 2 {
		t.Fatalf("want ReadError at element 2, got %v", err)
	}
}

func TestReadJSONParallelMatchesSerial(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("[\n")
	const n = 5*parallelBatchSize + 13
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",\n")
		}
		fmt.Fprintf(&sb, `{"id":%d,"name":"n%d","addr":{"k":%d},"tags":["a","b"],"pct":%d.5}`, i, i, i, i)
	}
	sb.WriteString("\n]\n")
	p := writeTempJSON(t, sb.String())
	serial := slices.Collect(ReadJSON[arrayRow](p))
	for _, shards := range []int{1, 3, 8} {
		s := ReadJSONParallel[arrayRow](p, shards)
		if s.Shards() != shards {
			t.Errorf("Shards() = %d, want %d", s.Shards(), shards)
		}
		par := slices.Collect(s.Serial())
		sort.Slice(par, func(i, j int) bool { return par[i].ID < par[j].ID })
		if !slices.Equal(par, serial) {
			t.Errorf("shards=%d: parallel differs from serial (%d vs %d rows)", shards, len(par), len(serial))
		}
	}
}

func TestReadJSONParallelErrorReachesConsumer(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < 3*parallelBatchSize; i++ {
		fmt.Fprintf(&sb, `{"id":%d},`, i)
	}
	sb.WriteString(`{"id":"bad"}]`)
	p := writeTempJSON(t, sb.String())
	err := recoverReadError(t, func() {
		for range ReadJSONParallel[arrayRow](p, 4).Serial() {
		}
	})
	if err == nil || err.Row != int64(3*parallelBatchSize+1) {
		t.Fatalf("want ReadError at element %d, got %v", 3*parallelBatchSize+1, err)
	}
	// A bad document is raised by the scanner and reaches every shard.
	bad := writeTempJSON(t, `{"id":1}`)
	err = recoverReadError(t, func() {
		for range ReadJSONParallel[arrayRow](bad, 2).Serial() {
		}
	})
	if err == nil || !strings.Contains(err.Error(), "not a JSON array") {
		t.Fatalf("want 'not a JSON array' from the parallel reader, got %v", err)
	}
}

func TestReadJSONParallelEarlyStop(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < 10*parallelBatchSize; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":%d}`, i)
	}
	sb.WriteString("]")
	p := writeTempJSON(t, sb.String())
	n := 0
	for range ReadJSONParallel[arrayRow](p, 4).Serial() {
		n++
		if n == 5 {
			break
		}
	}
	if n != 5 {
		t.Errorf("stopped after %d rows, want 5", n)
	}
	// The test ends — i.e. the scanner goroutine was released — or the
	// race/leak detectors in the package suite will say so.
}

func recoverReadError(t *testing.T, fn func()) (re *ReadError) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if e, ok := r.(*ReadError); ok {
			re = e
			return
		}
		if e, ok := r.(error); ok && errors.As(e, &re) {
			return
		}
		t.Fatalf("unexpected panic value %T: %v", r, r)
	}()
	fn()
	return nil
}
