//go:build !slim

package typed

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// A catalog's shards read typed (DFC149 §4.6): each file's constants —
// the hive key, the shard field — are string fields of the row, set on
// every row of that file; every mismatch between the constants and the
// struct is a *ReadError naming the file.
func TestParquetSources(t *testing.T) {
	dir := t.TempDir()
	type catRow struct {
		Name   string
		Age    int64
		Region string
		Shard  string
	}
	write := func(region string, rows ...pqRow) string {
		p := filepath.Join(dir, "region="+region, "data.parquet")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WriteParquet(slices.Values(rows), p, WithRowGroupSize(2)); err != nil {
			t.Fatal(err)
		}
		return p
	}
	north := write("north", pqRow{Name: "hal", Age: 6}, pqRow{Name: "gus", Age: 3}, pqRow{Name: "amy", Age: 5})
	south := write("south", pqRow{Name: "ivy", Age: 8}, pqRow{Name: "jon", Age: 1})
	sources := []ParquetSource{
		{Path: north, Constants: []ParquetConstant{{Name: "shard", Value: "local:" + north}, {Name: "region", Value: "north", FromPath: true}}},
		{Path: south, Constants: []ParquetConstant{{Name: "shard", Value: "local:" + south}, {Name: "region", Value: "south", FromPath: true}}},
	}
	want := []catRow{
		{"hal", 6, "north", "local:" + north}, {"gus", 3, "north", "local:" + north}, {"amy", 5, "north", "local:" + north},
		{"ivy", 8, "south", "local:" + south}, {"jon", 1, "south", "local:" + south},
	}
	if got := slices.Collect(ReadParquetSources[catRow](sources)); !slices.Equal(got, want) {
		t.Errorf("serial:\n got %v\nwant %v", got, want)
	}
	byName := func(rows []catRow) []catRow {
		rows = slices.Clone(rows)
		slices.SortFunc(rows, func(a, b catRow) int { return strings.Compare(a.Name, b.Name) })
		return rows
	}
	for _, n := range []int{1, 2, 8} {
		got := slices.Collect(ReadParquetSourcesParallel[catRow](sources, n).Serial())
		if !slices.Equal(byName(got), byName(want)) {
			t.Errorf("parallel n=%d:\n got %v\nwant %v", n, byName(got), byName(want))
		}
	}
	if got := slices.Collect(ReadParquetSources[catRow](nil)); len(got) != 0 {
		t.Errorf("no sources: %v", got)
	}
	if s := ReadParquetSourcesParallel[catRow](nil, 4); s.n != 0 {
		t.Errorf("no sources, parallel: %d shards", s.n)
	}

	// A file column of the same name: a catalog column takes its place,
	// a key from the path is a clash.
	inPlace := []ParquetSource{{Path: north, Constants: []ParquetConstant{{Name: "name", Value: "x"}}}}
	for _, r := range slices.Collect(ReadParquetSources[catRow](inPlace)) {
		if r.Name != "x" {
			t.Errorf("catalog column in place of the file's: %v", r)
		}
	}
	mustReadError := func(what, want string, fn func()) {
		t.Helper()
		defer func() {
			r := recover()
			re, ok := r.(*ReadError)
			if !ok {
				t.Errorf("%s: recovered %v (%T), want a *ReadError", what, r, r)
				return
			}
			if !strings.Contains(re.Error(), want) || !strings.Contains(re.Error(), north) {
				t.Errorf("%s: %v, want %q naming the file", what, re, want)
			}
		}()
		fn()
	}
	mustReadError("path key clash", `partition key "name" is also a column of the file`, func() {
		_ = slices.Collect(ReadParquetSources[catRow]([]ParquetSource{{Path: north, Constants: []ParquetConstant{{Name: "name", Value: "x", FromPath: true}}}}))
	})
	mustReadError("no field", `adds column "year", which catRow has no field for`, func() {
		_ = slices.Collect(ReadParquetSources[catRow]([]ParquetSource{{Path: north, Constants: []ParquetConstant{{Name: "year", Value: "2023"}}}}))
	})
	mustReadError("not a string", `column "age" is field Age of type int64`, func() {
		ReadParquetSourcesParallel[catRow]([]ParquetSource{{Path: north, Constants: []ParquetConstant{{Name: "age", Value: "7"}}}}, 2)
	})

	// The sources a loaded catalog gives: the record lane's metadata,
	// the hive key marked as from the path, the shard field first.
	entries, err := ssql.LoadCatalog(filepath.Join(dir, "region=*", "data.parquet"), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CatalogParquetSources(entries, "shard")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(sources) {
		t.Errorf("CatalogParquetSources:\n got %v\nwant %v", got, sources)
	}
	remote := append(slices.Clone(entries), ssql.CatalogEntry{Host: "db1", Path: "/data/x.parquet"})
	if _, err := CatalogParquetSources(remote, ""); err == nil || !strings.Contains(err.Error(), "db1:/data/x.parquet is not a Parquet file on this host") {
		t.Errorf("remote shard: %v", err)
	}
	csv := []ssql.CatalogEntry{{Host: "local", Path: "/data/x.csv"}}
	if _, err := CatalogParquetSources(csv, ""); err == nil || !strings.Contains(err.Error(), `(format "csv")`) {
		t.Errorf("csv shard: %v", err)
	}
}
