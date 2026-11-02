package typed

import (
	"fmt"
	"slices"
	"testing"
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
