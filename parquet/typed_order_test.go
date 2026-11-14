package parquet

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

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
	if err := WriteTyped(slices.Values(rows), path, WithRowGroupSize(3)); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 3, 5, 8, 20} {
		s := ReadTypedParallel[row](path, n)
		var got []row
		for _, shard := range s.ShardSeqs() {
			for r := range shard {
				got = append(got, r)
			}
		}
		if !reflect.DeepEqual(got, rows) {
			t.Errorf("n=%d: shard order is not file order: %v", n, got)
		}
	}
}
