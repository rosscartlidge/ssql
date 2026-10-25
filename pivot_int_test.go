package ssql

import (
	"errors"
	"math"
	"slices"
	"testing"
)

// A pivot cell keeps the kind of its values: an integer column sums
// exactly in an Int128 (DFC155 C, as every integer sum) and its min/max
// stay ints; a float column stays float; an empty cell is 0 in the
// column's kind.
func TestPivotIntegerCells(t *testing.T) {
	rows := []Record{
		NewRecord(map[string]any{"d": "a", "q": "x", "v": int64(9007199254740993)}),
		NewRecord(map[string]any{"d": "a", "q": "x", "v": int64(9007199254740994)}),
		NewRecord(map[string]any{"d": "a", "q": "y", "v": int64(5)}),
		NewRecord(map[string]any{"d": "b", "q": "x", "v": int64(7)}),
	}
	cell := func(fn, d, q string) any {
		for r := range Pivot("d", "q", "v", fn)(slices.Values(rows)) {
			if GetOr(r, "d", "") == d {
				v, _ := Get[any](r, q)
				return v
			}
		}
		return nil
	}
	if got := cell("sum", "a", "x"); got != Int128From(18014398509481987) {
		t.Errorf("sum: %v (%T)", got, got)
	}
	if got := cell("min", "a", "x"); got != int64(9007199254740993) {
		t.Errorf("min: %v (%T)", got, got)
	}
	if got := cell("max", "a", "x"); got != int64(9007199254740994) {
		t.Errorf("max: %v (%T)", got, got)
	}
	if got := cell("avg", "a", "x"); got != float64(9007199254740993.5) {
		t.Errorf("avg: %v (%T)", got, got)
	}
	if got := cell("sum", "b", "y"); got != (Int128{}) { // empty cell in an int column: the int128 zero
		t.Errorf("empty int cell: %v (%T)", got, got)
	}
	if got := cell("min", "b", "y"); got != int64(0) { // min/max keep the values' own kind
		t.Errorf("empty int min cell: %v (%T)", got, got)
	}
	mixed := append(slices.Clone(rows), NewRecord(map[string]any{"d": "b", "q": "y", "v": 0.5}))
	for r := range Pivot("d", "q", "v", "sum")(slices.Values(mixed)) {
		if GetOr(r, "d", "") == "a" {
			if v, _ := Get[any](r, "y"); v != Int128From(5) { // the int cell of a column that also holds a float stays an integer
				t.Errorf("mixed column int cell: %v (%T)", v, v)
			}
		}
	}
	wide := []Record{
		NewRecord(map[string]any{"d": "a", "q": "x", "v": int64(math.MaxInt64)}),
		NewRecord(map[string]any{"d": "a", "q": "x", "v": int64(1)}),
	}
	for r := range Pivot("d", "q", "v", "sum")(slices.Values(wide)) {
		if v, _ := Get[any](r, "x"); v != MustParseInt128("9223372036854775808") { // widens past 2^63 (DFC155 C)
			t.Errorf("wide cell: %v (%T)", v, v)
		}
	}
	over := []Record{
		NewRecord(map[string]any{"d": "a", "q": "x", "v": MaxInt128}),
		NewRecord(map[string]any{"d": "a", "q": "x", "v": int64(1)}),
	}
	err := Run(func() error { _ = slices.Collect(Pivot("d", "q", "v", "sum")(slices.Values(over))); return nil })
	var oe *Int128OverflowError
	if !errors.As(err, &oe) {
		t.Errorf("overflowing cell: want *Int128OverflowError, got %v", err)
	}
}
