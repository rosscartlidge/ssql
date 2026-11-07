package typed

import (
	"fmt"
	"iter"
	"slices"
	"testing"
	"time"
)

type lkLeft struct {
	ID   int64
	A, Z string
}

type lkKind struct{ Kind, Name string }

// TestLookupJoin: a row survives only when every clause matches, the
// combinations come first clause outermost, and the parallel form yields
// the serial rows in shard order.
func TestLookupJoin(t *testing.T) {
	left := []lkLeft{{1, "K1", "K2"}, {2, "K1", "K9"}, {3, "K8", "K2"}, {4, "K2", "K2"}}
	right := []lkKind{{"K1", "one"}, {"K2", "two"}, {"K2", "deux"}}
	leftKeys := []func(lkLeft) string{func(l lkLeft) string { return l.A }, func(l lkLeft) string { return l.Z }}
	rightKeys := []func(lkKind) string{func(r lkKind) string { return r.Kind }, func(r lkKind) string { return r.Kind }}
	merge := func(l lkLeft, rs []lkKind) string { return fmt.Sprintf("%d %s %s", l.ID, rs[0].Name, rs[1].Name) }
	want := []string{"1 one two", "1 one deux", "4 two two", "4 two deux", "4 deux two", "4 deux deux"}

	got := slices.Collect(LookupJoin(slices.Values(left), slices.Values(right), leftKeys, rightKeys, merge))
	if !slices.Equal(got, want) {
		t.Fatalf("LookupJoin = %q, want %q", got, want)
	}
	var par []string
	for _, shard := range LookupJoinParallel(ParallelFromSlice(left, 3), slices.Values(right), leftKeys, rightKeys, merge).shards {
		par = append(par, slices.Collect(shard)...)
	}
	if !slices.Equal(par, want) {
		t.Fatalf("LookupJoinParallel = %q, want %q", par, want)
	}
}

// TestStreamInOrderIsShardOrder: a join's right side comes back in shard order
// however the shards finish. Serial fans in as they come, so a side with a where
// gave one left row its matches in a different order from run to run (2026-11-07).
func TestStreamInOrderIsShardOrder(t *testing.T) {
	shard := func(delay time.Duration, rows ...int) iter.Seq[int] {
		return func(yield func(int) bool) {
			time.Sleep(delay)
			for _, r := range rows {
				if !yield(r) {
					return
				}
			}
		}
	}
	s := Stream[int]{
		shards: []iter.Seq[int]{shard(20*time.Millisecond, 1, 2), shard(10*time.Millisecond, 3), shard(0, 4, 5)},
		n:      3,
	}
	if got := slices.Collect(s.InOrder()); !slices.Equal(got, []int{1, 2, 3, 4, 5}) {
		t.Errorf("InOrder = %v", got)
	}
	// The contrast that makes it a test: Serial gives the last shard first.
	if got := slices.Collect(s.Serial()); slices.Equal(got, []int{1, 2, 3, 4, 5}) {
		t.Errorf("Serial kept shard order, so the delays prove nothing: %v", got)
	}
}
