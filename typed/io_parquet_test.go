//go:build !slim

package typed

import (
	"bytes"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

type pqRow struct {
	Name   string
	Age    int64
	Salary float64
	Active bool
	Dept   string `ssql:"dept_id"`
}

func TestParquetRoundTrip(t *testing.T) {
	in := []pqRow{
		{Name: "alice", Age: 30, Salary: 50000.5, Active: true, Dept: "D1"},
		{Name: "bob", Age: 25, Salary: 40000, Active: false, Dept: "D2"},
		{Name: "carol", Age: 28, Salary: 60000.25, Active: true, Dept: "D1"},
	}
	path := filepath.Join(t.TempDir(), "out.parquet")
	if err := WriteParquet(slices.Values(in), path); err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}
	got := slices.Collect(ReadParquet[pqRow](path))
	if !slices.Equal(got, in) {
		t.Errorf("ReadParquet round-trip:\n got %#v\nwant %#v", got, in)
	}
}

func TestParquetWriteToWriterRead(t *testing.T) {
	in := []pqRow{
		{Name: "alice", Age: 30, Salary: 50000, Active: true, Dept: "D1"},
		{Name: "bob", Age: 25, Salary: 40000, Active: false, Dept: "D2"},
	}
	var buf bytes.Buffer
	if err := WriteParquetToWriter(slices.Values(in), &buf); err != nil {
		t.Fatalf("WriteParquetToWriter: %v", err)
	}
	got := slices.Collect(ReadParquetFromReaderAt[pqRow](bytes.NewReader(buf.Bytes())))
	if !slices.Equal(got, in) {
		t.Errorf("ReadParquetFromReaderAt round-trip: got %#v, want %#v", got, in)
	}
}

func TestParquetEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.parquet")
	if err := WriteParquet(slices.Values([]pqRow{}), path); err != nil {
		t.Fatalf("WriteParquet empty: %v", err)
	}
	got := slices.Collect(ReadParquet[pqRow](path))
	if len(got) != 0 {
		t.Errorf("ReadParquet empty: got %d rows, want 0", len(got))
	}
}

func TestParquetColumnsFilter(t *testing.T) {
	in := []pqRow{
		{Name: "alice", Age: 30, Salary: 50000.5, Active: true, Dept: "D1"},
		{Name: "bob", Age: 25, Salary: 40000, Active: false, Dept: "D2"},
	}
	path := filepath.Join(t.TempDir(), "cols.parquet")
	if err := WriteParquet(slices.Values(in), path); err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}

	// Read only Name and Age — the other fields stay zero.
	got := slices.Collect(ReadParquet[pqRow](path, ParquetColumns("Name", "Age")))
	want := []pqRow{
		{Name: "alice", Age: 30},
		{Name: "bob", Age: 25},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ParquetColumns: got %#v, want %#v", got, want)
	}
}

func TestParquetStrictMissingField(t *testing.T) {
	in := []pqRow{{Name: "alice", Age: 30}}
	path := filepath.Join(t.TempDir(), "strict_missing.parquet")
	if err := WriteParquet(slices.Values(in), path); err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}

	// pqRowSubset is missing the Dept field → strict should reject.
	type pqRowSubset struct {
		Name string
	}
	for v, err := range ReadParquetSafe[pqRowSubset](path, ParquetStrict()) {
		_ = v
		if err == nil {
			t.Fatalf("expected strict error, got nil")
		}
		break
	}
}

func TestParquetParallelMatchesSerial(t *testing.T) {
	// Build 1000 rows with deterministic content.
	in := make([]pqRow, 1000)
	for i := 0; i < 1000; i++ {
		in[i] = pqRow{
			Name:   "user",
			Age:    int64(20 + i%50),
			Salary: float64(40000 + i*100),
			Active: i%2 == 0,
			Dept:   "D1",
		}
	}
	path := filepath.Join(t.TempDir(), "parallel.parquet")
	if err := WriteParquet(slices.Values(in), path); err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}

	serial := slices.Collect(ReadParquet[pqRow](path))
	if len(serial) != len(in) {
		t.Fatalf("serial: got %d rows, want %d", len(serial), len(in))
	}

	for _, n := range []int{1, 2, 4} {
		stream := ReadParquetParallel[pqRow](path, n)
		par := slices.Collect(stream.Serial())
		if len(par) != len(serial) {
			t.Errorf("n=%d: row count %d, want %d", n, len(par), len(serial))
		}
	}
}

func TestStreamWriteParquetRoundTrip(t *testing.T) {
	in := []pqRow{
		{Name: "alice", Age: 30, Salary: 50000.5, Active: true, Dept: "D1"},
		{Name: "bob", Age: 25, Salary: 40000, Active: false, Dept: "D2"},
		{Name: "carol", Age: 28, Salary: 60000, Active: true, Dept: "D1"},
		{Name: "dave", Age: 33, Salary: 70000, Active: false, Dept: "D3"},
	}
	stream := ParallelFromSlice(in, 3)
	path := filepath.Join(t.TempDir(), "stream.parquet")
	if err := stream.WriteParquet(path); err != nil {
		t.Fatalf("Stream.WriteParquet: %v", err)
	}
	got := slices.Collect(ReadParquet[pqRow](path))
	if len(got) != len(in) {
		t.Errorf("Stream.WriteParquet: got %d rows, want %d", len(got), len(in))
	}
	// Order may differ across shards — compare as multiset by name.
	slices.SortFunc(got, func(a, b pqRow) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	want := slices.Clone(in)
	slices.SortFunc(want, func(a, b pqRow) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	if !slices.Equal(got, want) {
		t.Errorf("Stream.WriteParquet multiset mismatch:\n got %#v\nwant %#v", got, want)
	}
}

type pqTimeRow struct {
	When  time.Time
	Value int64
}

func TestParquetTimestamps(t *testing.T) {
	t1 := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 29, 11, 30, 0, 0, time.UTC)
	in := []pqTimeRow{{When: t1, Value: 100}, {When: t2, Value: 200}}
	path := filepath.Join(t.TempDir(), "time.parquet")
	if err := WriteParquet(slices.Values(in), path); err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}
	got := slices.Collect(ReadParquet[pqTimeRow](path))
	if len(got) != 2 {
		t.Fatalf("time round-trip: got %d rows", len(got))
	}
	if !got[0].When.Equal(t1) || got[0].Value != 100 {
		t.Errorf("row 0: %v", got[0])
	}
	if !got[1].When.Equal(t2) || got[1].Value != 200 {
		t.Errorf("row 1: %v", got[1])
	}
}
