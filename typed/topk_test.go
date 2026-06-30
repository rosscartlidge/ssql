package typed

import (
	"reflect"
	"slices"
	"testing"
)

type tkRow struct {
	Name  string
	Score int64
}

func names(seq func(yield func(tkRow) bool)) []string {
	var out []string
	for r := range seq {
		out = append(out, r.Name)
	}
	return out
}

func tkData() []tkRow {
	return []tkRow{
		{"a", 30}, {"b", 10}, {"c", 50}, {"d", 20}, {"e", 40},
	}
}

func TestTopBySerial(t *testing.T) {
	key := func(r tkRow) int64 { return r.Score }
	got := names(TopBy(3, key)(slices.Values(tkData())))
	want := []string{"c", "e", "a"} // 50, 40, 30 descending
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TopBy(3) = %v, want %v", got, want)
	}
}

func TestBottomBySerial(t *testing.T) {
	key := func(r tkRow) int64 { return r.Score }
	got := names(BottomBy(3, key)(slices.Values(tkData())))
	want := []string{"b", "d", "a"} // 10, 20, 30 ascending
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BottomBy(3) = %v, want %v", got, want)
	}
}

func TestTopByStringKey(t *testing.T) {
	key := func(r tkRow) string { return r.Name }
	got := names(TopBy(2, key)(slices.Values(tkData())))
	want := []string{"e", "d"} // lexicographic top-2
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TopBy(2) by name = %v, want %v", got, want)
	}
}

func TestTopByEdgeCases(t *testing.T) {
	key := func(r tkRow) int64 { return r.Score }
	// n larger than input → all rows, still ordered.
	got := names(TopBy(100, key)(slices.Values(tkData())))
	if want := []string{"c", "e", "a", "d", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TopBy(100) = %v, want %v", got, want)
	}
	// n <= 0 → empty.
	if got := names(TopBy(0, key)(slices.Values(tkData()))); len(got) != 0 {
		t.Errorf("TopBy(0) = %v, want empty", got)
	}
	if got := names(BottomBy(-1, key)(slices.Values(tkData()))); len(got) != 0 {
		t.Errorf("BottomBy(-1) = %v, want empty", got)
	}
}

// TestTopByParallelMatchesSerial is the key correctness gate: the parallel
// per-shard-heap form must select the same set as the serial form. Ordering
// of equal keys may differ across shards, so compare the multiset of keys.
func TestTopByParallelMatchesSerial(t *testing.T) {
	// Larger dataset with duplicate keys so sharding actually splits work.
	var data []tkRow
	for i := 0; i < 1000; i++ {
		data = append(data, tkRow{Name: string(rune('a' + i%26)), Score: int64((i * 7) % 100)})
	}
	key := func(r tkRow) int64 { return r.Score }

	for _, n := range []int{1, 5, 10, 50} {
		serial := scoresOf(TopBy(n, key)(slices.Values(data)))
		par := scoresOf(TopByParallel(Parallel(slices.Values(data), 4), n, key))
		if !slices.Equal(serial, par) {
			t.Errorf("TopByParallel(n=%d) keys %v != serial %v", n, par, serial)
		}

		bserial := scoresOf(BottomBy(n, key)(slices.Values(data)))
		bpar := scoresOf(BottomByParallel(Parallel(slices.Values(data), 4), n, key))
		if !slices.Equal(bserial, bpar) {
			t.Errorf("BottomByParallel(n=%d) keys %v != serial %v", n, bpar, bserial)
		}
	}
}

func TestTopByParallelEdge(t *testing.T) {
	key := func(r tkRow) int64 { return r.Score }
	// n <= 0 → empty.
	if got := scoresOf(TopByParallel(Parallel(slices.Values(tkData()), 4), 0, key)); len(got) != 0 {
		t.Errorf("TopByParallel(0) = %v, want empty", got)
	}
}

func scoresOf(seq func(yield func(tkRow) bool)) []int64 {
	var out []int64
	for r := range seq {
		out = append(out, r.Score)
	}
	// The serial and parallel forms both yield best-first; the parallel
	// merge preserves that, so the score sequences compare directly.
	return out
}
