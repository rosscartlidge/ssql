package ssql

import (
	"slices"
	"testing"
)

func recordsOf(vals ...map[string]any) []Record {
	out := make([]Record, 0, len(vals))
	for _, m := range vals {
		out = append(out, NewRecord(m))
	}
	return out
}

func TestCoerceFieldTypes(t *testing.T) {
	in := recordsOf(
		map[string]any{"v": int64(1), "s": "12", "b": "true", "n": 2.0},
		map[string]any{"v": 1.5, "s": "", "b": "false", "n": int64(3)},
		map[string]any{"s": "7"}, // v absent stays absent
	)
	got := slices.Collect(CoerceFieldTypes(slices.Values(in), map[string]FieldType{
		"v": FieldTypeFloat, "s": FieldTypeInt, "b": FieldTypeBool, "n": FieldTypeString,
	}))
	if len(got) != 3 {
		t.Fatalf("rows = %d", len(got))
	}
	if v, _ := Get[any](got[0], "v"); v != 1.0 {
		t.Errorf("int64 1 → float: %#v", v)
	}
	if v, _ := Get[any](got[1], "v"); v != 1.5 {
		t.Errorf("float stays: %#v", v)
	}
	if v, _ := Get[any](got[0], "s"); v != int64(12) {
		t.Errorf("\"12\" → int: %#v", v)
	}
	if _, ok := Get[any](got[1], "s"); ok {
		t.Errorf("\"\" → int should be absent")
	}
	if v, _ := Get[any](got[0], "b"); v != true {
		t.Errorf("\"true\" → bool: %#v", v)
	}
	if v, _ := Get[any](got[0], "n"); v != "2" {
		t.Errorf("2.0 → string: %#v", v)
	}
	if v, _ := Get[any](got[1], "n"); v != "3" {
		t.Errorf("3 → string: %#v", v)
	}
	if _, ok := Get[any](got[2], "v"); ok {
		t.Errorf("absent field should stay absent")
	}
}

// Strict: a fraction into int, a number into bool, a bool into a
// number, an unparsable string — each is a *CellError naming the record.
func TestCoerceFieldTypesIsStrict(t *testing.T) {
	cases := []struct {
		val  any
		ft   FieldType
		want string
	}{
		{1.5, FieldTypeInt, "1.5"},
		{int64(1), FieldTypeBool, "1"},
		{true, FieldTypeFloat, "true"},
		{"abc", FieldTypeInt, "abc"},
	}
	for _, tc := range cases {
		in := recordsOf(map[string]any{"ok": int64(1)}, map[string]any{"f": tc.val})
		func() {
			defer func() {
				ce, ok := recover().(*CellError)
				if !ok {
					t.Fatalf("%v → %v: expected *CellError panic", tc.val, tc.ft)
				}
				if ce.Row != 2 || ce.Column != "f" || ce.Value != tc.want || ce.Type != tc.ft || ce.Sampled != 0 {
					t.Errorf("%v → %v: CellError = %+v", tc.val, tc.ft, ce)
				}
			}()
			for range CoerceFieldTypes(slices.Values(in), map[string]FieldType{"f": tc.ft}) {
			}
		}()
	}
	// No overrides: the sequence is returned as-is.
	in := recordsOf(map[string]any{"a": int64(1)})
	if n := len(slices.Collect(CoerceFieldTypes(slices.Values(in), nil))); n != 1 {
		t.Errorf("no-op coercion lost rows: %d", n)
	}
}
