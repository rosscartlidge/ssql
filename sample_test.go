package ssql

import (
	"iter"
	"slices"
	"testing"
)

func intSeq(n int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := 0; i < n; i++ {
			if !yield(i) {
				return
			}
		}
	}
}

// TestSampleHashSpecVectors pins the RNG spec: these values may NEVER
// change — every seeded sample ever taken depends on them (and the
// cross-lane equivalence gate depends on all backends agreeing).
func TestSampleHashSpecVectors(t *testing.T) {
	// Pinned to the composition of reference splitmix64 (verified
	// against the published vector below); if this test fails the
	// SPEC changed:
	const golden = uint64(0x9E3779B97F4A7C15)
	bigIdx := uint64(999999)
	pinned := map[[2]int64]uint64{
		{0, 0}:       splitmix64(splitmix64(0)),
		{42, 0}:      splitmix64(splitmix64(42)),
		{42, 1}:      splitmix64(splitmix64(42) ^ golden),
		{-1, 999999}: splitmix64(splitmix64(^uint64(0)) ^ bigIdx*golden),
	}
	for k, want := range pinned {
		if got := sampleHash(k[0], k[1]); got != want {
			t.Errorf("sampleHash(%d,%d) = %#x, want %#x — RNG SPEC CHANGED", k[0], k[1], got, want)
		}
	}
	// splitmix64 reference vector from the published algorithm:
	// state 0 first output.
	if got := splitmix64(0); got != 0xE220A8397B1DCDAF {
		t.Errorf("splitmix64(0) = %#x, want 0xE220A8397B1DCDAF — not the reference SplitMix64", got)
	}
}

func TestSampleNProperties(t *testing.T) {
	// Fewer rows than n: keep all, in order.
	got := slices.Collect(SampleN[int](10, 42)(intSeq(4)))
	if !slices.Equal(got, []int{0, 1, 2, 3}) {
		t.Errorf("n>len: %v", got)
	}
	// Exact count, input order, deterministic under seed.
	a := slices.Collect(SampleN[int](7, 42)(intSeq(1000)))
	b := slices.Collect(SampleN[int](7, 42)(intSeq(1000)))
	if len(a) != 7 || !slices.Equal(a, b) {
		t.Errorf("determinism: %v vs %v", a, b)
	}
	if !slices.IsSorted(a) {
		t.Errorf("input order not preserved: %v", a)
	}
	// Different seed, different selection (overwhelmingly).
	c := slices.Collect(SampleN[int](7, 43)(intSeq(1000)))
	if slices.Equal(a, c) {
		t.Errorf("seeds 42 and 43 selected identically: %v", a)
	}
}

func TestSamplePercentProperties(t *testing.T) {
	if got := slices.Collect(SamplePercent[int](100, 42)(intSeq(50))); len(got) != 50 {
		t.Errorf("percent 100: %d rows", len(got))
	}
	a := slices.Collect(SamplePercent[int](10, 42)(intSeq(10000)))
	b := slices.Collect(SamplePercent[int](10, 42)(intSeq(10000)))
	if !slices.Equal(a, b) {
		t.Error("percent determinism")
	}
	if !slices.IsSorted(a) {
		t.Error("percent order")
	}
	// Statistical smoke with wide, never-flaky bounds: 10% of 10k.
	if len(a) < 700 || len(a) > 1300 {
		t.Errorf("10%% of 10000 gave %d rows (expected ~1000; bound [700,1300])", len(a))
	}
}
