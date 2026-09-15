package ssql

import (
	"strings"
	"testing"
	"time"
)

func aggRecords(field string, vals ...any) []Record {
	out := make([]Record, 0, len(vals))
	for _, v := range vals {
		if v == nil {
			out = append(out, NewRecord(map[string]any{"other": int64(1)})) // field absent
			continue
		}
		out = append(out, NewRecord(map[string]any{field: v}))
	}
	return out
}

// TestMinOfMaxOf: the CLI's -min/-max keep the field's type — strings
// order lexically, times chronologically, numbers as float64 — and skip
// missing values. Until v4.99.0 `-min name` returned 0 for every group.
func TestMinOfMaxOf(t *testing.T) {
	t.Run("strings", func(t *testing.T) {
		recs := aggRecords("name", "Carol", "Alice", nil, "Bob")
		if got := MinOf("name")(recs).GetValue(); got != "Alice" {
			t.Fatalf("MinOf strings = %v, want Alice", got)
		}
		if got := MaxOf("name")(recs).GetValue(); got != "Carol" {
			t.Fatalf("MaxOf strings = %v, want Carol", got)
		}
	})
	t.Run("numbers mixed width come back as float64", func(t *testing.T) {
		recs := aggRecords("v", int64(7), 2.5, int64(-1))
		if got := MinOf("v")(recs).GetValue(); got != float64(-1) {
			t.Fatalf("MinOf numbers = %v (%T), want -1 float64", got, got)
		}
		if got := MaxOf("v")(recs).GetValue(); got != float64(7) {
			t.Fatalf("MaxOf numbers = %v (%T), want 7 float64", got, got)
		}
	})
	t.Run("times", func(t *testing.T) {
		t1 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		t2 := t1.Add(48 * time.Hour)
		recs := aggRecords("ts", t2, t1)
		if got := MinOf("ts")(recs).GetValue(); !got.(time.Time).Equal(t1) {
			t.Fatalf("MinOf times = %v, want %v", got, t1)
		}
		if got := MaxOf("ts")(recs).GetValue(); !got.(time.Time).Equal(t2) {
			t.Fatalf("MaxOf times = %v, want %v", got, t2)
		}
	})
	t.Run("all missing is the empty string, not a zero number", func(t *testing.T) {
		recs := aggRecords("x", nil, nil)
		if got := MinOf("x")(recs).GetValue(); got != "" {
			t.Fatalf("MinOf on absent field = %v (%T), want \"\"", got, got)
		}
	})
	t.Run("mixed kinds are loud", func(t *testing.T) {
		defer func() {
			r := recover()
			msg := ""
			if e, ok := r.(error); ok {
				msg = e.Error()
			}
			if !strings.Contains(msg, "mixed types") && !strings.Contains(msg, "cannot order") {
				t.Fatalf("expected a mixed-types panic, got %v", r)
			}
		}()
		MinOf("v")(aggRecords("v", "a", int64(1)))
	})
	t.Run("unorderable kinds are loud", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil || !strings.Contains(r.(error).Error(), "cannot order") {
				t.Fatalf("expected a cannot-order panic, got %v", r)
			}
		}()
		MaxOf("b")(aggRecords("b", true))
	})
}
