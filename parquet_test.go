//go:build !slim

package ssql

import (
	"path/filepath"
	"testing"
)

func TestParquetSchemaFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.parquet")
	recs := func(yield func(Record) bool) {
		r := MakeMutableRecord().Int("n", 42).Float("x", 1.5).String("s", "a").Bool("b", true)
		yield(r.Freeze())
	}
	if err := WriteParquet(recs, p); err != nil {
		t.Fatal(err)
	}
	names, types, err := ParquetSchemaFields(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"n": "int", "x": "float", "s": "string", "b": "bool"}
	if len(names) != 4 {
		t.Fatalf("names = %v", names)
	}
	for f, wt := range want {
		if types[f] != wt {
			t.Errorf("%s: type %q, want %q", f, types[f], wt)
		}
	}
	if _, _, err := ParquetSchemaFields(filepath.Join(t.TempDir(), "nope.parquet")); err == nil {
		t.Error("missing file: want error")
	}
}
