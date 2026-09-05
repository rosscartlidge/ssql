package ssql

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// The record CSV reader infers each column's type from a SAMPLE of
// leading rows (DefaultInferRows), and a later cell that does not fit
// the fixed type is a loud *CellError — never a coerced zero. Both
// halves are pinned here; the first-row-only inference they replace
// turned a bc-generated sine wave (`0,0` first) into all zeros
// (DFC124 §3, signal-processing codelab runner, 2026-09-05).

func TestCSVInferenceSamplesLeadingRows(t *testing.T) {
	// First row says int; the sample says float. Every value survives.
	in := "time,amplitude\n0,0\n.001,.062789\n.002,.125332\n"
	rows := slices.Collect(ReadCSVFromReader(strings.NewReader(in)))
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	want := []float64{0, 0.062789, 0.125332}
	for i, r := range rows {
		raw, _ := Get[any](r, "amplitude")
		v, ok := raw.(float64) // a type assertion, not Get[float64]: that converts int64 and would hide the bug
		if !ok {
			t.Fatalf("row %d: amplitude is %T, want float64", i, raw)
		}
		if v != want[i] {
			t.Errorf("row %d: amplitude = %v, want %v", i, v, want[i])
		}
	}
}

func TestCSVInferenceLattice(t *testing.T) {
	cases := []struct {
		name   string
		values []string
		want   FieldType
	}{
		{"ints", []string{"1", "0", "42"}, FieldTypeInt},
		{"int_then_float", []string{"0", ".5", "2"}, FieldTypeFloat},
		{"floats", []string{"1.5", "2.0", "3"}, FieldTypeFloat},
		{"bools", []string{"true", "FALSE", "True"}, FieldTypeBool},
		{"bool_with_yes_is_text", []string{"true", "yes"}, FieldTypeString},
		{"one_zero_are_ints_not_bools", []string{"1", "0"}, FieldTypeInt},
		{"number_then_text", []string{"1", "N/A", "3"}, FieldTypeString},
		{"empties_carry_no_type", []string{"", "7", ""}, FieldTypeInt},
		{"all_empty_is_text", []string{"", "", ""}, FieldTypeString},
		{"text", []string{"Alice", "123"}, FieldTypeString},
	}
	for _, c := range cases {
		if got := inferColumnType(c.values); got != c.want {
			t.Errorf("%s: inferColumnType(%q) = %s, want %s", c.name, c.values, got, c.want)
		}
	}
}

// A cell beyond the sample that does not fit is an error carrying row,
// column, value, and how the type was decided — the unsafe reader
// panics with it (its fail-fast contract), the safe reader yields it
// and continues with the next row.
func TestCSVUnparsableCellIsLoud(t *testing.T) {
	in := "id,v\n1,10\n2,20\n3,x\n4,40\n"
	cfg := DefaultCSVConfig()
	cfg.InferRows = 2 // the sample sees only ints; row 3 breaks the rule

	t.Run("safe_yields_CellError_and_continues", func(t *testing.T) {
		var got []int64
		var errs []error
		for r, err := range ReadCSVSafeFromReader(strings.NewReader(in), cfg) {
			if err != nil {
				errs = append(errs, err)
				continue
			}
			got = append(got, GetOr(r, "v", int64(-1)))
		}
		if len(errs) != 1 {
			t.Fatalf("want exactly one error, got %d: %v", len(errs), errs)
		}
		var ce *CellError
		if !errors.As(errs[0], &ce) {
			t.Fatalf("error is %T, want *CellError: %v", errs[0], errs[0])
		}
		if ce.Row != 3 || ce.Column != "v" || ce.Value != "x" || ce.Type != FieldTypeInt || ce.Sampled != 2 {
			t.Errorf("CellError = %+v", *ce)
		}
		for _, want := range []string{`row 3`, `column "v"`, `"x" is not int`, "first 2 rows"} {
			if !strings.Contains(ce.Error(), want) {
				t.Errorf("message %q lacks %q", ce.Error(), want)
			}
		}
		if !slices.Equal(got, []int64{10, 20, 40}) {
			t.Errorf("rows after the bad one still flow in the safe reader: got %v", got)
		}
	})

	t.Run("unsafe_panics_with_CellError", func(t *testing.T) {
		defer func() {
			r := recover()
			ce, ok := r.(*CellError)
			if !ok {
				t.Fatalf("panic value is %T (%v), want *CellError", r, r)
			}
			if ce.Row != 3 || ce.Column != "v" {
				t.Errorf("CellError = %+v", *ce)
			}
		}()
		for range ReadCSVFromReader(strings.NewReader(in), cfg) {
		}
		t.Fatal("unsafe reader did not panic on the unparsable cell")
	})

	t.Run("explicit_override_reports_no_sample", func(t *testing.T) {
		c2 := cfg
		c2.TypeOverrides = map[string]FieldType{"v": FieldTypeInt}
		for _, err := range ReadCSVSafeFromReader(strings.NewReader(in), c2) {
			if err == nil {
				continue
			}
			var ce *CellError
			if !errors.As(err, &ce) {
				t.Fatalf("got %T", err)
			}
			if ce.Sampled != 0 || !strings.Contains(ce.Error(), "explicit column type") {
				t.Errorf("override error should say explicit, got %q", ce.Error())
			}
			return
		}
		t.Fatal("no error for the unparsable cell under an explicit type")
	})
}

// No float truncation: an int column never quietly rounds "1.5" to 1.
func TestCSVIntColumnRefusesFloat(t *testing.T) {
	cfg := DefaultCSVConfig()
	cfg.InferRows = 1
	var ce *CellError
	for _, err := range ReadCSVSafeFromReader(strings.NewReader("v\n1\n1.5\n"), cfg) {
		if err != nil && errors.As(err, &ce) {
			break
		}
	}
	if ce == nil || ce.Value != "1.5" {
		t.Fatalf("want a CellError for 1.5 in an int column, got %v", ce)
	}
}

// InferRows is a real bound: with a sample of 1 the old first-row
// behaviour returns — but loudly, never as a silent zero.
func TestCSVInferRowsBound(t *testing.T) {
	cfg := DefaultCSVConfig()
	cfg.InferRows = 1
	in := "t,v\n0,0\n1,.5\n"
	var gotErr error
	var rows []Record
	for r, err := range ReadCSVSafeFromReader(strings.NewReader(in), cfg) {
		if err != nil {
			gotErr = err
			continue
		}
		rows = append(rows, r)
	}
	if gotErr == nil {
		t.Fatal("InferRows=1 with a later float must error, not coerce")
	}
	if len(rows) != 1 {
		t.Errorf("want the 1 good row, got %d", len(rows))
	}
}

// Empty cells stay absent (DFC124) under sampling, and a column that is
// empty throughout the sample is a string column.
func TestCSVEmptyCellsUnderSampling(t *testing.T) {
	in := "id,n,e\n1,,\n2,7,\n3,,\n"
	rows := slices.Collect(ReadCSVFromReader(strings.NewReader(in)))
	if _, ok := Get[any](rows[0], "n"); ok {
		t.Errorf("row 1: empty int cell should be absent")
	}
	if raw, _ := Get[any](rows[1], "n"); raw != int64(7) {
		t.Errorf("row 2: n = %T %v, want int64 7", raw, raw)
	}
	if raw, _ := Get[any](rows[0], "e"); raw != "" {
		t.Errorf("all-empty column should be a string column holding \"\", got %T %v", raw, raw)
	}
}
