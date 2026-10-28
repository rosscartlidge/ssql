package typed

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// TestWindowParallelMatchesWindow: over a stream whose shards are
// contiguous runs of the input, WindowParallel yields exactly Window's
// output — the same rows in the same order, partitions in first-seen
// order with the partitions interleaved across shards, ties kept in
// input order.
func TestWindowParallelMatchesWindow(t *testing.T) {
	rows := []winRow{
		{"sales", "Bob", 65000, nil}, {"eng", "Alice", 95000, nil}, {"eng", "Carol", 105000, nil},
		{"ops", "Zed", 1, nil}, {"eng", "Eve", 88000, nil}, {"sales", "Frank", 82000, nil},
		{"eng", "Dan", 95000, nil}, {"ops", "Yan", 1, nil}, {"sales", "Gus", 65000, nil},
	}
	for _, ordered := range []bool{true, false} {
		clause := WindowClause[winRow]{
			Partition: func(r winRow) string { return r.Dept },
			HasOrder:  ordered,
			Frame:     WindowFrame{Preceding: -1, Following: 0},
			Specs: []WindowSpec[winRow]{
				{Kind: WRowNumber},
				{Kind: WRank},
				{Kind: WSum, Num: func(r winRow) (float64, bool) { return float64(r.Salary), true }},
				{Kind: WLag, N: 1, Val: func(r winRow) (any, bool) { return r.Salary, true }},
			},
		}
		if ordered {
			clause.Compare = func(a, b winRow) int {
				if a.Salary < b.Salary {
					return -1
				}
				if a.Salary > b.Salary {
					return 1
				}
				return 0
			}
		}
		build := func(r winRow, res []any) []any { return append([]any{r.Name}, res...) }
		var serial, parallel [][]any
		for o := range Window([]WindowClause[winRow]{clause}, build)(slices.Values(rows)) {
			serial = append(serial, o)
		}
		for _, n := range []int{1, 2, 4, 9} {
			parallel = nil
			for o := range WindowParallel([]WindowClause[winRow]{clause}, build)(ParallelFromSlice(rows, n)) {
				parallel = append(parallel, o)
			}
			if !reflect.DeepEqual(serial, parallel) {
				t.Errorf("ordered=%v shards=%d:\n serial   %v\n parallel %v", ordered, n, serial, parallel)
			}
		}
	}
}

// TestReadParquetParallelShardsAreInFileOrder: the shards are contiguous
// runs of row groups, so concatenated in shard order they are the file —
// what WindowParallel and the parallel sinks rely on.
func TestReadParquetParallelShardsAreInFileOrder(t *testing.T) {
	type row struct {
		ID int64 `ssql:"id"`
	}
	var rows []row
	for i := int64(0); i < 23; i++ {
		rows = append(rows, row{ID: i})
	}
	path := filepath.Join(t.TempDir(), "o.parquet")
	if err := WriteParquet(slices.Values(rows), path, WithRowGroupSize(3)); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 3, 5, 8, 20} {
		s := ReadParquetParallel[row](path, n)
		var got []row
		for _, shard := range s.shards {
			for r := range shard {
				got = append(got, r)
			}
		}
		if !reflect.DeepEqual(got, rows) {
			t.Errorf("n=%d: shard order is not file order: %v", n, got)
		}
	}
}
