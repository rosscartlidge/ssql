package ssql

import (
	"strings"
	"testing"
)

// TestArgMaxArgMin: FIELD at the extreme BY, ties to first arrival,
// records missing either field skipped, BY ordered like MinOf (DFC129
// phase 3).
func TestArgMaxArgMin(t *testing.T) {
	recs := []Record{
		NewRecord(map[string]any{"name": "Alice", "salary": int64(95000)}),
		NewRecord(map[string]any{"name": "Carol", "salary": int64(105000)}),
		NewRecord(map[string]any{"name": "NoPay"}), // no BY: skipped
		NewRecord(map[string]any{"salary": int64(200000)}), // no FIELD: skipped
		NewRecord(map[string]any{"name": "Eve", "salary": int64(88000)}),
	}
	if got := ArgMax("name", "salary")(recs).GetValue(); got != "Carol" {
		t.Fatalf("ArgMax = %v, want Carol", got)
	}
	if got := ArgMin("name", "salary")(recs).GetValue(); got != "Eve" {
		t.Fatalf("ArgMin = %v, want Eve", got)
	}
	// Ties keep the first arrival.
	tied := aggRecords("by", int64(1), int64(1))
	tied[0] = NewRecord(map[string]any{"by": int64(1), "v": "first"})
	tied[1] = NewRecord(map[string]any{"by": int64(1), "v": "second"})
	if got := ArgMax("v", "by")(tied).GetValue(); got != "first" {
		t.Fatalf("ArgMax tie = %v, want first", got)
	}
	// BY may be a string; the carried value keeps its type (a number).
	byStr := []Record{
		NewRecord(map[string]any{"code": "b", "n": int64(2)}),
		NewRecord(map[string]any{"code": "c", "n": int64(3)}),
		NewRecord(map[string]any{"code": "a", "n": int64(1)}),
	}
	if got := ArgMax("n", "code")(byStr).GetValue(); got != float64(3) {
		t.Fatalf("ArgMax by string = %v (%T), want 3", got, got)
	}
	if got := ArgMax("name", "salary")(aggRecords("x", nil)).GetValue(); got != "" {
		t.Fatalf("ArgMax of nothing = %v, want \"\"", got)
	}
	t.Run("unorderable BY is loud", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil || !strings.Contains(r.(error).Error(), "cannot order") {
				t.Fatalf("expected a cannot-order panic, got %v", r)
			}
		}()
		ArgMax("v", "b")([]Record{NewRecord(map[string]any{"b": true, "v": 1})})
	})
}
