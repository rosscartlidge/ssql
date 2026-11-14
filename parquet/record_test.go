package parquet

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	ssqlarrow "github.com/rosscartlidge/ssql/v4/arrow"

	ssql "github.com/rosscartlidge/ssql/v4"
)

func TestParquetSchemaFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.parquet")
	recs := func(yield func(ssql.Record) bool) {
		r := ssql.MakeMutableRecord().Int("n", 42).Float("x", 1.5).String("s", "a").Bool("b", true)
		yield(r.Freeze())
	}
	if err := Write(recs, p); err != nil {
		t.Fatal(err)
	}
	names, types, err := SchemaFields(p)
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
	if _, _, err := SchemaFields(filepath.Join(t.TempDir(), "nope.parquet")); err == nil {
		t.Error("missing file: want error")
	}
}

// TestWriteColumnTypes: the record writers type a column from every
// value, not the first record's: a time is a timestamp (it was a
// VARCHAR of Go's Time.String()), a column null in row 1 takes its
// later values' type, int then float is float (the int column used to
// truncate the float), int then string is string, and a field first
// seen in a later record is written (it used to be dropped). Declared
// columns keep an all-null column's type and let an empty input write
// a zero-row file; no records and no schema is an error with no file.
func TestWriteColumnTypes(t *testing.T) {
	ts := time.Date(2023, 5, 15, 23, 30, 0, 0, time.UTC)
	rows := []ssql.Record{
		ssql.MakeMutableRecord().Int("id", 1).Time("ts", ts).Null("late").Int("x", 1).Int("mix", 7).Freeze(),
		ssql.MakeMutableRecord().Int("id", 2).Time("ts", ts.Add(time.Hour)).Time("late", ts).Float("x", 2.5).String("mix", "q").String("extra", "e").Freeze(),
	}
	seq := func(rs []ssql.Record) func(func(ssql.Record) bool) {
		return func(yield func(ssql.Record) bool) {
			for _, r := range rs {
				if !yield(r) {
					return
				}
			}
		}
	}
	dir := t.TempDir()
	check := func(name string, write func(string) error, read func(string) ([]ssql.Record, error)) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := write(p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := read(p)
		if err != nil {
			t.Fatalf("%s read: %v", name, err)
		}
		if len(got) != 2 {
			t.Fatalf("%s: %d rows", name, len(got))
		}
		if v, _ := ssql.Get[time.Time](got[0], "ts"); !v.Equal(ts) {
			t.Errorf("%s: ts = %v, want %v", name, v, ts)
		}
		if _, ok := ssql.Get[any](got[0], "late"); ok {
			t.Errorf("%s: late in row 1 should be null", name)
		}
		if v, _ := ssql.Get[time.Time](got[1], "late"); !v.Equal(ts) {
			t.Errorf("%s: late in row 2 = %v", name, v)
		}
		if v, _ := ssql.Get[float64](got[1], "x"); v != 2.5 {
			t.Errorf("%s: x = %v, want 2.5 (int then float widens to float)", name, v)
		}
		if v, _ := ssql.Get[string](got[0], "mix"); v != "7" {
			t.Errorf("%s: mix = %q, want \"7\" (int then string is string)", name, v)
		}
		if v, _ := ssql.Get[string](got[1], "extra"); v != "e" {
			t.Errorf("%s: extra = %q (a field first seen in row 2)", name, v)
		}
	}
	collect := func(seq func(func(ssql.Record) bool), err error) ([]ssql.Record, error) {
		if err != nil {
			return nil, err
		}
		var out []ssql.Record
		for r := range seq {
			out = append(out, r)
		}
		return out, nil
	}
	check("w.parquet",
		func(p string) error { return Write(seq(rows), p) },
		func(p string) ([]ssql.Record, error) { s, err := Read(p); return collect(s, err) })
	check("w.arrow",
		func(p string) error { return ssqlarrow.Write(seq(rows), p) },
		func(p string) ([]ssql.Record, error) { s, err := ssqlarrow.Read(p); return collect(s, err) })

	names, types, err := SchemaFields(filepath.Join(dir, "w.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"id", "ts", "late", "x", "mix", "extra"}) {
		t.Errorf("columns = %v", names)
	}
	for f, want := range map[string]string{"id": "int", "ts": "time", "late": "time", "x": "float", "mix": "string"} {
		if types[f] != want {
			t.Errorf("%s: %q, want %q", f, types[f], want)
		}
	}

	// Declared columns: an empty input writes a zero-row file.
	empty := filepath.Join(dir, "empty.parquet")
	if err := Write(seq(nil), empty, WithColumnTypes([]string{"a", "when"}, map[string]string{"a": "int", "when": "time"})); err != nil {
		t.Fatal(err)
	}
	names, types, err = SchemaFields(empty)
	if err != nil || !slices.Equal(names, []string{"a", "when"}) || types["when"] != "time" {
		t.Errorf("empty with schema: %v %v %v", names, types, err)
	}

	// No records, no schema: an error, and no file.
	for _, write := range []func(string) error{
		func(p string) error { return Write(seq(nil), p) },
		func(p string) error { return ssqlarrow.Write(seq(nil), p) },
	} {
		p := filepath.Join(dir, "none")
		if err := write(p); err == nil {
			t.Error("no records and no schema: want error")
		}
		if _, err := os.Stat(p); err == nil {
			t.Error("no records and no schema: left a file")
			os.Remove(p)
		}
	}
}
