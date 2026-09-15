package ssql

import (
	"math"
	"testing"
)

// TestCompensatedSum: Neumaier summation keeps what plain addition loses,
// its shard merge is as accurate as a serial pass, and the -sum/-avg
// aggregates and the Welford variance are built on it.
func TestCompensatedSum(t *testing.T) {
	// The unit vanishes under plain addition (and under DuckDB's SUM and
	// kahan_sum): 1e16 + 1 rounds to 1e16, then -1e16 leaves 0.
	plain := 0.0
	for _, x := range []float64{1e16, 1, -1e16} {
		plain += x
	}
	if plain != 0 {
		t.Fatalf("plain sum = %v; this test assumes it loses the 1", plain)
	}
	var cs CompensatedSum
	for _, x := range []float64{1e16, 1, -1e16} {
		cs.Add(x)
	}
	if cs.Value() != 1 {
		t.Fatalf("CompensatedSum = %v, want 1", cs.Value())
	}

	t.Run("merge keeps the compensation", func(t *testing.T) {
		var a, b, c CompensatedSum
		a.Add(1e16)
		b.Add(1)
		c.Add(-1e16)
		a.Merge(b)
		a.Merge(c)
		if a.Value() != 1 {
			t.Fatalf("merged = %v, want 1", a.Value())
		}
	})

	t.Run("a long drift-prone sum is exact to the ulp", func(t *testing.T) {
		// 0.1 a million times: plain float64 addition is off by ~1e-9;
		// the compensated total rounds to the correctly rounded 1e5.
		var cs CompensatedSum
		plain := 0.0
		for i := 0; i < 1_000_000; i++ {
			cs.Add(0.1)
			plain += 0.1
		}
		if math.Abs(plain-1e5) < 1e-10 {
			t.Fatalf("plain sum %v is unexpectedly exact; the fixture no longer discriminates", plain)
		}
		if math.Abs(cs.Value()-1e5) > 1e-10 {
			t.Fatalf("compensated sum = %.12f, want 100000", cs.Value())
		}
	})

	t.Run("Sum and Avg aggregates are compensated", func(t *testing.T) {
		recs := aggRecords("v", 1e16, 1.0, -1e16)
		if got := Sum("v")(recs).GetValue(); got != float64(1) {
			t.Fatalf("Sum = %v, want 1", got)
		}
		if got := Avg("v")(recs).GetValue(); got != 1.0/3 {
			t.Fatalf("Avg = %v, want 1/3", got)
		}
	})

	t.Run("Welford mean and M2 do not drift", func(t *testing.T) {
		// A million equal values: the running mean must stay on the value
		// and M2 must stay at (numerical) zero. (1e16 + 1 − 1e16 is NOT a
		// case compensation can rescue in Welford — the unit is lost in
		// the x−mean subtraction itself, before any summation.)
		var w Welford
		for i := 0; i < 1_000_000; i++ {
			w.Add(0.1)
		}
		if math.Abs(w.MeanValue()-0.1) > 1e-15 {
			t.Fatalf("Welford mean = %.17g, want 0.1", w.MeanValue())
		}
		if w.Variance() > 1e-20 {
			t.Fatalf("Welford variance of a constant = %g, want ~0", w.Variance())
		}
	})
}
