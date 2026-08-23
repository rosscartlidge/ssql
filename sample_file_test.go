package ssql

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeSampleFixture(t *testing.T, rows int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("id,val\n")
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&b, "%d,v%d\n", i, i*3)
	}
	p := filepath.Join(t.TempDir(), "fx.csv")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func collectIDs(t *testing.T, seq func(yield func(Record) bool)) []int64 {
	t.Helper()
	var ids []int64
	for r := range seq {
		ids = append(ids, GetOr(r, "id", int64(-1)))
	}
	return ids
}

func TestSampleCSVFileDeterministicAndOrdered(t *testing.T) {
	p := writeSampleFixture(t, 5000)
	s1, err := SampleCSVFile(p, 50, 42)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := SampleCSVFile(p, 50, 42)
	a, b := collectIDs(t, s1), collectIDs(t, s2)
	if len(a) != 50 || !slices.Equal(a, b) {
		t.Fatalf("determinism: %d rows, equal=%v", len(a), slices.Equal(a, b))
	}
	if !slices.IsSorted(a) {
		t.Errorf("file order not preserved: %v", a[:10])
	}
	// Distinct lines.
	seen := map[int64]bool{}
	for _, id := range a {
		if seen[id] {
			t.Fatalf("duplicate line sampled: id %d", id)
		}
		seen[id] = true
	}
	// Whole-file coverage: with 50 draws over 5000 rows, both halves
	// of the file must be represented (a prefix-window bug would
	// concentrate low ids). P(all in one half) = 2^-49 — never flaky.
	var lo, hi int
	for _, id := range a {
		if id < 2500 {
			lo++
		} else {
			hi++
		}
	}
	if lo == 0 || hi == 0 {
		t.Errorf("coverage: lo=%d hi=%d — sampling is not spanning the file", lo, hi)
	}
	// Different seed differs.
	s3, _ := SampleCSVFile(p, 50, 43)
	if slices.Equal(a, collectIDs(t, s3)) {
		t.Error("seeds 42/43 selected identically")
	}
}

func TestSampleCSVFileSmallFileFallback(t *testing.T) {
	// Fewer data lines than n: every row comes back, values intact.
	p := writeSampleFixture(t, 7)
	seq, err := SampleCSVFile(p, 100, 42)
	if err != nil {
		t.Fatal(err)
	}
	ids := collectIDs(t, seq)
	if len(ids) != 7 || !slices.IsSorted(ids) {
		t.Fatalf("small-file fallback: %v", ids)
	}
}

func TestSampleCSVFileParsesTypes(t *testing.T) {
	p := writeSampleFixture(t, 3000)
	seq, err := SampleCSVFile(p, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	for r := range seq {
		if _, ok := Get[int64](r, "id"); !ok {
			t.Fatalf("id not parsed as int64: %v", r)
		}
		if v := GetOr(r, "val", ""); !strings.HasPrefix(v, "v") {
			t.Fatalf("val mangled: %q", v)
		}
	}
}

func TestSampleTSVFile(t *testing.T) {
	var b strings.Builder
	b.WriteString("id|name\n") // pipe-delimited "TSV" — detection rule applies
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "%d|n%d\n", i, i)
	}
	p := filepath.Join(t.TempDir(), "fx.tsv")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	s1, err := SampleTSVFile(p, 40, 42)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := SampleTSVFile(p, 40, 42)
	a, bIDs := collectIDs(t, s1), collectIDs(t, s2)
	if len(a) != 40 || !slices.Equal(a, bIDs) || !slices.IsSorted(a) {
		t.Fatalf("tsv: len=%d equal=%v sorted=%v", len(a), slices.Equal(a, bIDs), slices.IsSorted(a))
	}
	var lo, hi int
	for _, id := range a {
		if id < 1500 {
			lo++
		} else {
			hi++
		}
	}
	if lo == 0 || hi == 0 {
		t.Errorf("tsv coverage: lo=%d hi=%d", lo, hi)
	}
}

func TestSampleJSONLFile(t *testing.T) {
	mk := func(withHeader bool) string {
		var b strings.Builder
		if withHeader {
			b.WriteString(`{"_schema":{"fields":["id","name"],"types":{"id":"int","name":"string"}}}` + "\n")
		}
		for i := 0; i < 3000; i++ {
			fmt.Fprintf(&b, `{"id":%d,"name":"n%d"}`+"\n", i, i)
		}
		p := filepath.Join(t.TempDir(), "fx.jsonl")
		if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, withHeader := range []bool{true, false} {
		p := mk(withHeader)
		s1, err := SampleJSONLFile(p, 40, 42)
		if err != nil {
			t.Fatal(err)
		}
		ids := collectIDs(t, s1)
		if len(ids) != 40 || !slices.IsSorted(ids) {
			t.Fatalf("header=%v: len=%d sorted=%v", withHeader, len(ids), slices.IsSorted(ids))
		}
		for _, id := range ids {
			if id < 0 { // schema header sampled as data would parse id=-1 default
				t.Fatalf("header=%v: schema header leaked into sample", withHeader)
			}
		}
		var lo, hi int
		for _, id := range ids {
			if id < 1500 {
				lo++
			} else {
				hi++
			}
		}
		if lo == 0 || hi == 0 {
			t.Errorf("header=%v coverage: lo=%d hi=%d", withHeader, lo, hi)
		}
	}
	// JSON arrays refuse loudly.
	pa := filepath.Join(t.TempDir(), "arr.json")
	os.WriteFile(pa, []byte(`[{"id":1},{"id":2}]`), 0o644)
	if _, err := SampleJSONLFile(pa, 5, 1); err == nil || !strings.Contains(err.Error(), "JSON array") {
		t.Errorf("array: want loud refusal, got %v", err)
	}
}
