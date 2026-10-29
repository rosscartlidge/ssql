package typed

import (
	"math/rand"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

// TestSortParallelMatchesStable: the parallel sorts over 1, 3 and 24
// shards, past the chunk threshold, equal the stable serial sort by the
// same key — ties in input order (ssql.SortRecords is stable; the
// parallel index sort keeps the row index as its tiebreak) — ascending,
// descending, by a string key (the prefix-keyed path) and by a
// comparator; the output shards concatenate to the order and Serial
// walks it.
func TestSortParallelMatchesStable(t *testing.T) {
	type row struct {
		K   int64
		S   string
		Seq int
	}
	n := 3*sortChunkMin + 1234
	rng := rand.New(rand.NewSource(7))
	rows := make([]row, n)
	for i := range rows {
		rows[i] = row{K: int64(rng.Intn(500)), S: "k" + strconv.Itoa(rng.Intn(300)), Seq: i} // many ties
	}
	collect := func(s Stream[row]) []row {
		var out []row
		for _, shard := range s.shards {
			for r := range shard {
				out = append(out, r)
			}
		}
		return out
	}
	serial := func(s Stream[row]) []row { return slices.Collect(s.Serial()) }
	stable := func(less func(a, b row) int) []row {
		out := slices.Clone(rows)
		slices.SortStableFunc(out, less)
		return out
	}
	cases := []struct {
		name string
		par  func(Stream[row]) Stream[row]
		want []row
	}{
		{"int asc", SortParallelBy(func(r row) int64 { return r.K }, false), stable(func(a, b row) int { return int(a.K - b.K) })},
		{"int desc", SortParallelBy(func(r row) int64 { return r.K }, true), stable(func(a, b row) int { return int(b.K - a.K) })},
		{"string asc", SortParallelBy(func(r row) string { return r.S }, false), stable(func(a, b row) int { return cmpStr(a.S, b.S) })},
		{"string desc", SortParallelBy(func(r row) string { return r.S }, true), stable(func(a, b row) int { return cmpStr(b.S, a.S) })},
		{"func K then S desc", SortParallelFunc(func(a, b row) int {
			if a.K != b.K {
				return int(a.K - b.K)
			}
			return cmpStr(b.S, a.S)
		}), stable(func(a, b row) int {
			if a.K != b.K {
				return int(a.K - b.K)
			}
			return cmpStr(b.S, a.S)
		})},
	}
	for _, c := range cases {
		for _, shards := range []int{1, 3, 24} {
			out := c.par(ParallelFromSlice(rows, shards))
			if got := collect(out); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("%s, %d shards: the output shards concatenated differ from the stable sort (first diff at %d)", c.name, shards, firstDiff(got, c.want))
			}
			if got := serial(c.par(ParallelFromSlice(rows, shards))); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("%s, %d shards: Serial() differs from the stable sort (first diff at %d)", c.name, shards, firstDiff(got, c.want))
			}
		}
	}
	// Empty input.
	if got := serial(SortParallelBy(func(r row) int64 { return r.K }, false)(ParallelFromSlice([]row{}, 4))); len(got) != 0 {
		t.Fatalf("empty: %d rows", len(got))
	}
}

func cmpStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func firstDiff[T comparable](a, b []T) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}
