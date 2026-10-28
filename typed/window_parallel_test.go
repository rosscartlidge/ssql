package typed

import (
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
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

// TestWindowKeySortMatchesCompare: a clause sorted through its typed key
// (string, int64, float64; ascending and descending) yields exactly what
// the same clause sorted through Compare yields — ties in input order,
// a NaN compared equal to everything as the generated comparator does,
// partitions above and below the chunked-sort threshold.
func TestWindowKeySortMatchesCompare(t *testing.T) {
	type row struct {
		Part int64
		S    string
		I    int64
		F    float64
		V    float64
	}
	rng := rand.New(rand.NewSource(11))
	n := 3*sortChunkMin + 777
	rows := make([]row, n)
	for i := range rows {
		rows[i] = row{Part: int64(rng.Intn(2)), S: fmt.Sprintf("s%04d", rng.Intn(3000)), I: int64(rng.Intn(500)) - 250, F: float64(rng.Intn(400)) / 8, V: float64(i)}
		if i%97 == 0 {
			rows[i].F = math.NaN()
		}
	}
	specs := []WindowSpec[row]{
		{Kind: WRowNumber},
		{Kind: WRank},
		{Kind: WSum, Num: func(r row) (float64, bool) { return r.V, true }},
	}
	build := func(r row, res []any) [4]any { return [4]any{r.V, res[0], res[1], res[2]} }
	run := func(c WindowClause[row]) [][4]any {
		var out [][4]any
		for o := range Window([]WindowClause[row]{c}, build)(slices.Values(rows)) {
			out = append(out, o)
		}
		return out
	}
	for _, desc := range []bool{false, true} {
		cases := []struct {
			name string
			cmp  func(a, b row) int
			key  func(c *WindowClause[row])
		}{
			{"string", func(a, b row) int {
				if a.S < b.S {
					return -1
				}
				if a.S > b.S {
					return 1
				}
				return 0
			}, func(c *WindowClause[row]) { c.KeyString = func(r row) string { return r.S } }},
			{"int", func(a, b row) int {
				if a.I < b.I {
					return -1
				}
				if a.I > b.I {
					return 1
				}
				return 0
			}, func(c *WindowClause[row]) { c.KeyInt = func(r row) int64 { return r.I } }},
			{"float with NaN", func(a, b row) int {
				if a.F < b.F {
					return -1
				}
				if a.F > b.F {
					return 1
				}
				return 0
			}, func(c *WindowClause[row]) { c.KeyFloat = func(r row) float64 { return r.F } }},
		}
		for _, c := range cases {
			cmpFn := c.cmp
			if desc {
				cmpFn = func(a, b row) int { return c.cmp(b, a) }
			}
			base := WindowClause[row]{
				Partition: func(r row) string { return strconv.FormatInt(r.Part, 10) },
				HasOrder:  true, Compare: cmpFn, Desc: desc,
				Frame: WindowFrame{Preceding: -1, Following: 0}, Specs: specs,
			}
			want := run(base)
			keyed := base
			c.key(&keyed)
			got := run(keyed)
			if !reflect.DeepEqual(got, want) {
				for i := range want {
					if i < len(got) && !reflect.DeepEqual(got[i], want[i]) {
						t.Fatalf("%s desc=%v: row %d differs: key %v, compare %v", c.name, desc, i, got[i], want[i])
					}
				}
				t.Fatalf("%s desc=%v: %d rows, want %d", c.name, desc, len(got), len(want))
			}
		}
	}
}
