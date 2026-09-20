package ssql

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestCastValue: a value that is not of the target type FAILS — it never
// becomes 0 or false. Defined conversions still convert.
func TestCastValue(t *testing.T) {
	tm := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	ok := []struct {
		in     any
		target FieldType
		want   any
	}{
		{"42", FieldTypeInt, int64(42)}, {" 42 ", FieldTypeInt, int64(42)}, {"2.9", FieldTypeInt, int64(2)}, {2.9, FieldTypeInt, int64(2)}, {true, FieldTypeInt, int64(1)},
		{"2.5", FieldTypeFloat, 2.5}, {int64(3), FieldTypeFloat, float64(3)}, {false, FieldTypeFloat, float64(0)},
		{"yes", FieldTypeBool, true}, {"OFF", FieldTypeBool, false}, {int64(0), FieldTypeBool, false}, {0.5, FieldTypeBool, true},
		{int64(7), FieldTypeString, "7"}, {2.5, FieldTypeString, "2.5"}, {tm, FieldTypeString, "2026-01-02T00:00:00Z"},
		{"2026-01-02", FieldTypeTime, tm},
	}
	for _, c := range ok {
		got, good := CastValue(c.in, c.target)
		if gt, isTime := got.(time.Time); isTime {
			if !good || !gt.Equal(c.want.(time.Time)) {
				t.Errorf("CastValue(%v, %s) = %v, %v", c.in, c.target, got, good)
			}
			continue
		}
		if !good || got != c.want {
			t.Errorf("CastValue(%v, %s) = %v (%T), %v; want %v", c.in, c.target, got, got, good, c.want)
		}
	}
	for _, c := range []struct {
		in     any
		target FieldType
	}{{"abc", FieldTypeInt}, {"N/A", FieldTypeFloat}, {"maybe", FieldTypeBool}, {"banana", FieldTypeTime}, {"12abc", FieldTypeInt}, {"", FieldTypeInt}} {
		if got, good := CastValue(c.in, c.target); good {
			t.Errorf("CastValue(%q, %s) = %v — must fail, not become a zero", c.in, c.target, got)
		}
	}
}

// TestCastField: missing stays missing, an empty text cell becomes a field
// without a value, a bad value panics with a *CastError that IS an error
// (so generated programs print one line, not a trace) — or, under
// invalidMissing, becomes a field without a value and is counted.
func TestCastField(t *testing.T) {
	src := MakeMutableRecord().String("n", "abc").String("e", "").String("good", "5").Freeze()
	var invalid int64
	mut := src.ToMutable()
	mut = CastField(mut, src, "good", FieldTypeInt, false, &invalid)
	mut = CastField(mut, src, "e", FieldTypeInt, false, &invalid)
	mut = CastField(mut, src, "absent", FieldTypeInt, false, &invalid)
	mut = CastField(mut, src, "n", FieldTypeInt, true, &invalid)
	r := mut.Freeze()
	if got := GetOr(r, "good", int64(0)); got != 5 {
		t.Errorf("good = %v", got)
	}
	if r.HasValue("e") || !r.Has("e") || r.HasValue("n") || !r.Has("n") || r.Has("absent") {
		t.Errorf("empty and invalid must be fields without a value, absent stays absent: %v", r)
	}
	if invalid != 1 {
		t.Errorf("invalid count = %d, want 1 (the empty cell is missing, not invalid)", invalid)
	}
	func() {
		defer func() {
			err, isErr := recover().(error)
			var ce *CastError
			if !isErr || !errors.As(err, &ce) || ce.Field != "n" || !strings.Contains(err.Error(), `"abc" is not an int`) || !strings.Contains(err.Error(), "-invalid missing") {
				t.Errorf("want a *CastError naming the field, the value and the way out, got %v", err)
			}
		}()
		CastField(src.ToMutable(), src, "n", FieldTypeInt, false, nil)
	}()
	func() {
		defer func() {
			if _, isErr := recover().(error); !isErr {
				t.Error("MustParseTime must panic with an error, not a string")
			}
		}()
		MustParseTime("junk", "ts")
	}()
	if got := MustCast[int64]("", FieldTypeInt, "x"); got != 0 {
		t.Errorf("typed lane: an empty string is its missing value, the zero: %v", got)
	}
	if got := MustCast[float64]("2.5", FieldTypeFloat, "x"); got != 2.5 {
		t.Errorf("MustCast = %v", got)
	}
}
