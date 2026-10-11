package ssql

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestExpandFilePatterns: a pattern becomes its sorted matches in place,
// literals and URLs pass through, a pattern matching nothing and ** are
// errors (never an empty read, never a silently shallow one).
func TestExpandFilePatterns(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b.parquet", "a.parquet", "sub/c.parquet"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, n)), 0o755)
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	got, err := ExpandFilePatterns([]string{"https://h/x*.parquet", dir + "/*.parquet", "lit.parquet"})
	want := []string{"https://h/x*.parquet", dir + "/a.parquet", dir + "/b.parquet", "lit.parquet"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("got %v (%v), want %v", got, err, want)
	}
	if _, err := ExpandFilePatterns([]string{dir + "/*.csv"}); err == nil || !strings.Contains(err.Error(), "matches no files") {
		t.Errorf("no match: %v", err)
	}
	if _, err := ExpandFilePatterns([]string{dir + "/**/*.parquet"}); err == nil || !strings.Contains(err.Error(), "**") {
		t.Errorf("**: %v", err)
	}
}

// TestReadParquetFiles: files are read in argument order as one stream,
// with -columns in the order given; a schema mismatch is refused naming
// both files and the column.
func TestReadParquetFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, ids ...int64) string {
		p := filepath.Join(dir, name)
		seq := func(yield func(Record) bool) {
			for _, id := range ids {
				if !yield(MakeMutableRecord().Int("id", id).String("name", fmt.Sprint("n", id)).Freeze()) {
					return
				}
			}
		}
		if err := WriteParquet(seq, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b := write("a.parquet", 3, 1), write("b.parquet", 2)
	seq, err := ReadParquetFiles([]string{b, a}, []string{"name", "id"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for r := range seq {
		var fields []string
		for k := range r.All() {
			fields = append(fields, k)
		}
		got = append(got, fmt.Sprint(fields, GetOr(r, "id", int64(0))))
	}
	if want := []string{"[name id] 2", "[name id] 3", "[name id] 1"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	other := filepath.Join(dir, "other.parquet")
	if err := WriteParquet(func(yield func(Record) bool) {
		yield(MakeMutableRecord().String("id", "x").Freeze())
	}, other); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadParquetFiles([]string{a, other}, nil); err == nil || !strings.Contains(err.Error(), "other.parquet does not match") {
		t.Errorf("mismatch: %v", err)
	}
}
