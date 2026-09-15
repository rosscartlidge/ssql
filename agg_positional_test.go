package ssql

import (
	"testing"
	"time"
)

// TestPositionalAggregates: -first/-last/-any, -count-distinct and
// -string-agg in the library (DFC129 phase 1) — arrival order, missing
// values skipped, types kept, text formatting shared with the typed lane.
func TestPositionalAggregates(t *testing.T) {
	recs := aggRecords("v", nil, "b", int64(3), nil, "a")
	if got := FirstOf("v")(recs).GetValue(); got != "b" {
		t.Fatalf("FirstOf skips missing, got %v", got)
	}
	if got := LastOf("v")(recs).GetValue(); got != "a" {
		t.Fatalf("LastOf = %v, want a", got)
	}
	if got := FirstOf("v")(aggRecords("v", nil, nil)).GetValue(); got != "" {
		t.Fatalf("FirstOf on an all-missing group = %v (%T), want \"\"", got, got)
	}

	t.Run("count distinct", func(t *testing.T) {
		recs := aggRecords("c", "SF", "NYC", nil, "SF", "Chicago")
		if got := CountDistinct("c")(recs).GetValue(); got != int64(3) {
			t.Fatalf("CountDistinct strings = %v, want 3", got)
		}
		// 3 and 3.0 are one value; 4 another.
		nums := aggRecords("n", int64(3), 3.0, int64(4))
		if got := CountDistinct("n")(nums).GetValue(); got != int64(2) {
			t.Fatalf("CountDistinct numbers = %v, want 2", got)
		}
	})

	t.Run("string agg", func(t *testing.T) {
		recs := aggRecords("s", "x", nil, "y", "z")
		if got := StringAgg("s", ", ")(recs).GetValue(); got != "x, y, z" {
			t.Fatalf("StringAgg = %q", got)
		}
		ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		mixed := aggRecords("m", int64(95000), 2.5, true, ts)
		want := "95000;2.5;true;2026-01-02T03:04:05Z"
		if got := StringAgg("m", ";")(mixed).GetValue(); got != want {
			t.Fatalf("StringAgg formatting = %q, want %q", got, want)
		}
		if got := StringAgg("s", ",")(aggRecords("s", nil)).GetValue(); got != "" {
			t.Fatalf("StringAgg of nothing = %q, want empty", got)
		}
	})
}
