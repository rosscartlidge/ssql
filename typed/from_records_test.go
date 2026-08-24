package typed

import (
	"iter"
	"slices"
	"testing"
)

func seqOf[T any](vals []T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, v := range vals {
			if !yield(v) {
				return
			}
		}
	}
}

type fakeRec struct{ k string }
type row struct{ K string }

func TestFromRecords(t *testing.T) {
	src := []fakeRec{{"a"}, {"b"}, {"c"}}
	got := slices.Collect(FromRecords(seqOf(src), func(r fakeRec) row { return row{K: r.k} }))
	want := []row{{"a"}, {"b"}, {"c"}}
	if !slices.Equal(got, want) {
		t.Fatalf("FromRecords = %v, want %v", got, want)
	}
}

func TestFromRecordsEarlyStop(t *testing.T) {
	src := []fakeRec{{"a"}, {"b"}, {"c"}}
	var got []row
	for r := range FromRecords(seqOf(src), func(r fakeRec) row { return row{K: r.k} }) {
		got = append(got, r)
		break
	}
	if len(got) != 1 || got[0].K != "a" {
		t.Fatalf("early stop got %v", got)
	}
}

func TestFromRecordsParallel(t *testing.T) {
	for _, n := range []int{1, 3, 24, 100} {
		src := make([]fakeRec, 50)
		for i := range src {
			src[i] = fakeRec{k: string(rune('a' + i%26))}
		}
		s := FromRecordsParallel(seqOf(src), func(r fakeRec) row { return row{K: r.k} }, n)
		var got []row
		for r := range s.Serial() {
			got = append(got, r)
		}
		if len(got) != 50 {
			t.Fatalf("n=%d: got %d rows, want 50", n, len(got))
		}
	}
}

// TestParallelFromSliceManyShards pins the lo-clamp fix: round-up
// chunking with more shards than fit (50 rows / 24 shards → shard 17
// would start at 51) used to panic with slice bounds out of range.
func TestParallelFromSliceManyShards(t *testing.T) {
	for _, tc := range []struct{ rows, shards int }{
		{50, 24}, {1, 16}, {7, 3}, {0, 8}, {100, 7},
	} {
		data := make([]int, tc.rows)
		for i := range data {
			data[i] = i
		}
		s := ParallelFromSlice(data, tc.shards)
		total := 0
		for range s.Serial() {
			total++
		}
		if total != tc.rows {
			t.Fatalf("rows=%d shards=%d: iterated %d", tc.rows, tc.shards, total)
		}
	}
}

func TestDistinctParallel(t *testing.T) {
	for _, shards := range []int{1, 3, 16} {
		data := make([]int, 5000)
		for i := range data {
			data[i] = i % 37
		}
		got := slices.Collect(DistinctParallel(ParallelFromSlice(data, shards), func(v int) int { return v }))
		if len(got) != 37 {
			t.Fatalf("shards=%d: %d distinct, want 37", shards, len(got))
		}
		seen := map[int]bool{}
		for _, v := range got {
			if seen[v] {
				t.Fatalf("shards=%d: duplicate %d", shards, v)
			}
			seen[v] = true
		}
	}
	if got := slices.Collect(DistinctParallel(ParallelFromSlice([]int{}, 4), func(v int) int { return v })); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
}
