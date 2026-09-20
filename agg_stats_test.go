package ssql

import (
	"math"
	"strings"
	"testing"
)

// TestStatisticsAggregates: median/percentile (continuous quantile),
// sample stddev/variance (Welford, merge-exact on the fixture) and mode
// (first-arrival tie-break) — DFC129 phase 2. The numbers are the
// Engineering group of the corpus fixture, cross-checked against
// DuckDB (median 95000, quantile_cont 0.9 = 103000, stddev_samp
// 8544.003745317532, var_samp 73000000) and Python's statistics module.
func TestStatisticsAggregates(t *testing.T) {
	recs := aggRecords("s", int64(95000), int64(105000), nil, int64(88000))
	if got := Median("s")(recs).GetValue(); got != float64(95000) {
		t.Fatalf("Median = %v", got)
	}
	if got := Percentile("s", 0.9)(recs).GetValue(); got != float64(103000) {
		t.Fatalf("Percentile 0.9 = %v, want 103000", got)
	}
	if got := Variance("s")(recs).GetValue(); got != float64(73000000) {
		t.Fatalf("Variance = %v, want 73000000", got)
	}
	if got := StdDev("s")(recs).GetValue(); got != 8544.003745317532 {
		t.Fatalf("StdDev = %v, want 8544.003745317532", got)
	}
	if got := Variance("s")(aggRecords("s", int64(5))).GetValue(); got != float64(0) {
		t.Fatalf("Variance of one value = %v, want 0", got)
	}
	if got := Median("s")(aggRecords("s", nil)).GetValue(); got != nil {
		t.Fatalf("Median of nothing = %v, want nil (no value)", got)
	}

	t.Run("quantile interpolation", func(t *testing.T) {
		if got := QuantileCont([]float64{1, 2, 3, 4}, 0.5); got != 2.5 {
			t.Fatalf("QuantileCont even n = %v, want 2.5", got)
		}
		if got := QuantileCont([]float64{10}, 0.25); got != 10 {
			t.Fatalf("QuantileCont single = %v", got)
		}
		if got := QuantileCont([]float64{1, 2, 3}, 1); got != 3 {
			t.Fatalf("QuantileCont p=1 = %v", got)
		}
	})

	t.Run("welford merge equals a single pass", func(t *testing.T) {
		var whole, a, b Welford
		xs := []float64{2, 4, 4, 4, 5, 5, 7, 9}
		for _, x := range xs {
			whole.Add(x)
		}
		for _, x := range xs[:3] {
			a.Add(x)
		}
		for _, x := range xs[3:] {
			b.Add(x)
		}
		a.Merge(b)
		if a.N != whole.N || math.Abs(a.Variance()-whole.Variance()) > 1e-12 || math.Abs(a.MeanValue()-whole.MeanValue()) > 1e-12 {
			t.Fatalf("merged %+v vs whole %+v", a, whole)
		}
		if whole.Variance() != 32.0/7 {
			t.Fatalf("sample variance = %v, want 32/7", whole.Variance())
		}
	})

	t.Run("mode keeps the type and breaks ties by first arrival", func(t *testing.T) {
		if got := Mode("c")(aggRecords("c", "SF", "NYC", "SF", nil, "NYC", "SF")).GetValue(); got != "SF" {
			t.Fatalf("Mode = %v, want SF", got)
		}
		if got := Mode("l")(aggRecords("l", int64(7), int64(9), int64(6))).GetValue(); got != float64(7) {
			t.Fatalf("Mode tie = %v, want first-seen 7", got)
		}
	})

	t.Run("non-numeric is loud", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil || !strings.Contains(r.(error).Error(), "not a number") {
				t.Fatalf("expected a not-a-number panic, got %v", r)
			}
		}()
		Median("s")(aggRecords("s", "abc"))
	})
}
