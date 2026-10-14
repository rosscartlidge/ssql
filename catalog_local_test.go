package ssql

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The local fast path (DFC149 §4.4): a local Parquet shard is read in
// this process. The subprocess path cannot run here — remoteBin names a
// binary that does not exist — so a record that comes back came from
// the fast path. Every record of a shard shares one output schema (the
// #1 performance rule), the metadata columns come in catalog order on
// every record, a metadata column that is also a file column takes the
// metadata value, a hive clash and a missing file are the shard's error.
func TestCatalogLocalParquetFastPath(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, rows ...Record) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WriteParquet(slices.Values(rows), p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	row := func(id int64, name string) Record {
		return MakeMutableRecord().Int("id", id).String("name", name).Freeze()
	}
	a := write("pq/day=2023-05-16/a.parquet", row(1, "x"), row(2, "y"))
	b := write("pq/day=2023-05-17/b.parquet", row(3, "z"))
	csvShard := filepath.Join(dir, "c.csv")
	os.WriteFile(csvShard, []byte("id,name\n9,w\n"), 0o644)

	// A catalog with columns in a deliberate order (zone before region),
	// a range pair that is not a field, and a glob under -hive.
	catalog := filepath.Join(dir, "cat.csv")
	os.WriteFile(catalog, []byte("host,path,zone,region,t_from,t_to\nlocal,"+filepath.Join(dir, "pq/day=*/*.parquet")+",eu,north,1,2\n"), 0o644)
	entries, err := LoadCatalog(catalog, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !slices.Equal(entries[0].Columns, []string{"zone", "region", "t_from", "t_to", "day"}) {
		t.Fatalf("entries %+v", entries)
	}
	records := slices.Collect(ProcessCatalogShards(entries, "/nonexistent/ssql", "shard", nil))
	if len(records) != 3 {
		t.Fatalf("got %d records", len(records))
	}
	wantFields := []string{"id", "name", "shard", "zone", "region", "day"}
	for i, r := range records {
		if !slices.Equal(r.Schema().Fields(), wantFields) {
			t.Errorf("record %d fields %v, want %v", i, r.Schema().Fields(), wantFields)
		}
	}
	// The glob form (no catalog CSV, so no Columns from a header): the
	// derived key is the only metadata column, listed ONCE — it was
	// appended to the sorted map keys after joining the map, so every
	// record carried day twice (2026-10-14).
	globEntries, err := LoadCatalog(filepath.Join(dir, "pq/day=*/*.parquet"), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(globEntries[0].Columns, []string{"day"}) {
		t.Errorf("glob form columns %v, want [day]", globEntries[0].Columns)
	}
	for i, r := range slices.Collect(ProcessCatalogShards(globEntries, "/nonexistent/ssql", "", nil)) {
		if want := []string{"id", "name", "day"}; !slices.Equal(r.Schema().Fields(), want) {
			t.Errorf("glob record %d fields %v, want %v", i, r.Schema().Fields(), want)
		}
	}
	if records[0].Schema() != records[1].Schema() {
		t.Error("records of one shard must share one output schema")
	}
	if GetOr(records[0], "day", "") != "2023-05-16" || GetOr(records[2], "day", "") != "2023-05-17" || GetOr(records[2], "shard", "") != "local:"+b || GetOr(records[1], "id", int64(0)) != 2 {
		t.Errorf("values: %v %v %v", records[0], records[1], records[2])
	}

	// A catalog column that is also a file column: the metadata wins, in
	// place, with no duplicate field.
	in := []CatalogEntry{{Host: "local", Path: a, Metadata: map[string]string{"name": "meta"}}}
	got := slices.Collect(ProcessCatalogShards(in, "/nonexistent/ssql", "", nil))
	if len(got) != 2 || !slices.Equal(got[0].Schema().Fields(), []string{"id", "name"}) || GetOr(got[0], "name", "") != "meta" {
		t.Errorf("in-place metadata: %v", got)
	}

	// Errors are the shard's, as panics the caller's ssql.Run reports.
	expectPanic := func(entries []CatalogEntry, want string) {
		t.Helper()
		defer func() {
			r := recover()
			if r == nil || !strings.Contains(r.(error).Error(), want) {
				t.Errorf("want a panic containing %q, got %v", want, r)
			}
		}()
		_ = slices.Collect(ProcessCatalogShards(entries, "/nonexistent/ssql", "", nil))
	}
	expectPanic([]CatalogEntry{{Host: "local", Path: a, Metadata: map[string]string{"name": "n"}, HiveKeys: []string{"name"}}}, `partition key "name" is also a column of the file`)
	expectPanic([]CatalogEntry{{Host: "local", Path: filepath.Join(dir, "nope.parquet")}}, "shard local:")

	// A CSV shard, a remote host or a pushdown stage are not the fast
	// path (the subprocess path, which the child ssql owns).
	for _, c := range []struct {
		e    CatalogEntry
		push [][]string
	}{
		{CatalogEntry{Host: "local", Path: csvShard}, nil},
		{CatalogEntry{Host: "node1", Path: a}, nil},
		{CatalogEntry{Host: "local", Path: a}, [][]string{{"where", "-if", "id", "gt", "1"}}},
	} {
		if catalogLocalParquet(c.e, c.push) {
			t.Errorf("%+v push=%v must not take the fast path", c.e, c.push)
		}
	}
	if !catalogLocalParquet(CatalogEntry{Host: "local", Path: a, Format: "parquet"}, nil) || !catalogLocalParquet(CatalogEntry{Host: "localhost", Path: b}, nil) {
		t.Error("a local parquet shard, by format column or extension, takes the fast path")
	}

	// WriteCatalog keeps the catalog's column order (it was map order).
	var buf bytes.Buffer
	if err := WriteCatalog(&buf, entries); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "host,path,zone,region,t_from,t_to,day\n") {
		t.Errorf("WriteCatalog header: %q", strings.SplitN(buf.String(), "\n", 2)[0])
	}
}
