package parquet

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// TestReadParquetFiles: files are read in argument order as one stream,
// with -columns in the order given; a schema mismatch is refused naming
// both files and the column.
func TestReadParquetFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, ids ...int64) string {
		p := filepath.Join(dir, name)
		seq := func(yield func(ssql.Record) bool) {
			for _, id := range ids {
				if !yield(ssql.MakeMutableRecord().Int("id", id).String("name", fmt.Sprint("n", id)).Freeze()) {
					return
				}
			}
		}
		if err := Write(seq, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b := write("a.parquet", 3, 1), write("b.parquet", 2)
	seq, err := ReadFiles([]string{b, a}, []string{"name", "id"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for r := range seq {
		var fields []string
		for k := range r.All() {
			fields = append(fields, k)
		}
		got = append(got, fmt.Sprint(fields, ssql.GetOr(r, "id", int64(0))))
	}
	if want := []string{"[name id] 2", "[name id] 3", "[name id] 1"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	other := filepath.Join(dir, "other.parquet")
	if err := Write(func(yield func(ssql.Record) bool) {
		yield(ssql.MakeMutableRecord().String("id", "x").Freeze())
	}, other); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles([]string{a, other}, nil); err == nil || !strings.Contains(err.Error(), "other.parquet does not match") {
		t.Errorf("mismatch: %v", err)
	}
}
