package typed

import (
	"cmp"
	"math/rand"
	"runtime"
	"slices"
	"sort"
	"testing"
)

// TestSortIdxMatchesStable: the chunked parallel sort of a large
// partition gives exactly the order sort.SliceStable gave — keys with
// many ties (the index tiebreak makes the comparator strict), sizes on
// both sides of the chunk threshold, an odd number of chunks, and one
// core (the inline path).
func TestSortIdxMatchesStable(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, n := range []int{10, sortChunkMin - 1, 2 * sortChunkMin, 5*sortChunkMin + 17, 23 * sortChunkMin} {
		keys := make([]int, n)
		for i := range keys {
			keys[i] = rng.Intn(50) // heavy ties
		}
		want := make([]int, n)
		for i := range want {
			want[i] = i
		}
		sort.SliceStable(want, func(a, b int) bool { return keys[want[a]] < keys[want[b]] })
		strict := func(a, b int) int {
			if c := cmp.Compare(keys[a], keys[b]); c != 0 {
				return c
			}
			return cmp.Compare(a, b)
		}
		for _, procs := range []int{1, 3, runtime.GOMAXPROCS(0)} {
			prev := runtime.GOMAXPROCS(procs)
			got := make([]int, n)
			for i := range got {
				got[i] = i
			}
			rng.Shuffle(n, func(i, j int) { got[i], got[j] = got[j], got[i] })
			sortIdx(got, strict)
			runtime.GOMAXPROCS(prev)
			if !slices.Equal(got, want) {
				t.Fatalf("n=%d procs=%d: order differs from sort.SliceStable", n, procs)
			}
		}
	}
}
