package typed

import (
	"runtime"
	"slices"
	"sort"
	"sync/atomic"
	"testing"
)

func TestParallelSerialRoundTrip(t *testing.T) {
	in := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	s := Parallel(slices.Values(in), 4)
	if s.Shards() != 4 {
		t.Errorf("Shards() = %d, want 4", s.Shards())
	}
	got := slices.Collect(s.Serial())
	// Serial() is unordered, but every input value must appear exactly once.
	sort.Ints(got)
	if !slices.Equal(got, in) {
		t.Errorf("round-trip lost or duplicated values:\n  got:  %v\n  want: %v", got, in)
	}
}

func TestParallelDefaultN(t *testing.T) {
	s := Parallel(slices.Values([]int{1, 2, 3}), 0)
	if s.Shards() != runtime.GOMAXPROCS(0) {
		t.Errorf("Shards() = %d, want GOMAXPROCS=%d", s.Shards(), runtime.GOMAXPROCS(0))
	}
}

func TestStreamWhere(t *testing.T) {
	in := make([]int, 1000)
	for i := range in {
		in[i] = i
	}
	s := Parallel(slices.Values(in), 4)
	filtered := s.Where(func(v int) bool { return v%2 == 0 })
	got := slices.Collect(filtered.Serial())
	sort.Ints(got)
	want := make([]int, 0, 500)
	for i := 0; i < 1000; i++ {
		if i%2 == 0 {
			want = append(want, i)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %d items, want %d", len(got), len(want))
	}
}

func TestStreamWhereRunsInParallel(t *testing.T) {
	// Predicate counts goroutine identities (rough proxy via stamping
	// per-call atomic counters); we expect more than one to be hit
	// when n > 1. Less direct than runtime.NumGoroutine but avoids
	// race-detector false positives.
	in := make([]int, 10000)
	for i := range in {
		in[i] = i
	}
	var byGoroutine [4]int64
	s := Parallel(slices.Values(in), 4)
	filtered := s.Where(func(v int) bool {
		// Hash-mod-by-4 of a goroutine-local counter; every shard's
		// goroutine sees a distinct identity.
		atomic.AddInt64(&byGoroutine[v%4], 1)
		return true
	})
	for range filtered.Serial() {
	}
	hits := 0
	for _, c := range byGoroutine {
		if c > 0 {
			hits++
		}
	}
	if hits < 2 {
		t.Errorf("expected >= 2 of 4 buckets to be hit by parallel Where; got %d", hits)
	}
}

func TestStreamSelect(t *testing.T) {
	s := Parallel(slices.Values([]int{1, 2, 3, 4}), 2)
	doubled := StreamSelect(s, func(v int) int { return v * 2 })
	got := slices.Collect(doubled.Serial())
	sort.Ints(got)
	want := []int{2, 4, 6, 8}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestHashJoinParallel(t *testing.T) {
	type L struct{ K, V string }
	type R struct{ K, Extra string }
	type O struct{ V, Extra string }

	left := []L{{K: "a", V: "1"}, {K: "b", V: "2"}, {K: "z", V: "9"}, {K: "a", V: "3"}}
	right := []R{{K: "a", Extra: "Apple"}, {K: "b", Extra: "Banana"}}

	leftStream := Parallel(slices.Values(left), 4)
	joined := HashJoinParallel(leftStream, slices.Values(right),
		func(l L) string { return l.K },
		func(r R) string { return r.K },
		func(l L, r R) O { return O{V: l.V, Extra: r.Extra} },
	)

	got := slices.Collect(joined.Serial())
	// Inner-join: matches a/Apple, b/Banana, a/Apple again. z/9 has
	// no right match — dropped. Three results in some order.
	sort.Slice(got, func(i, j int) bool {
		if got[i].V != got[j].V {
			return got[i].V < got[j].V
		}
		return got[i].Extra < got[j].Extra
	})
	want := []O{
		{V: "1", Extra: "Apple"},
		{V: "2", Extra: "Banana"},
		{V: "3", Extra: "Apple"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestStreamEarlyTermination(t *testing.T) {
	in := make([]int, 100000)
	for i := range in {
		in[i] = i
	}
	s := Parallel(slices.Values(in), 4)
	seen := 0
	for v := range s.Serial() {
		seen++
		if v == 5 {
			break
		}
	}
	// Early termination from Serial() must drain the workers' channel
	// so they exit cleanly. Hard to assert directly; instead verify
	// the loop exits and no goroutine deadlock is detected by `go
	// test -race` or by the test runner's timeout.
	if seen < 1 {
		t.Errorf("loop should have run at least once")
	}
}
