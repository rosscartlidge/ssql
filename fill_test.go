package ssql

import "testing"

func TestFillGoldenHandComputed(t *testing.T) {
	in := recsOf(
		[]kv{{"id", int64(1)}, {"region", ""}, {"score", int64(5)}},   // leading gap → default
		[]kv{{"id", int64(2)}, {"region", "west"}, {"score", int64(7)}},
		[]kv{{"id", int64(3)}, {"region", ""}},                        // region carried, score absent → default
		[]kv{{"id", int64(4)}, {"region", "east"}, {"score", int64(9)}},
		[]kv{{"id", int64(5)}},                                        // both carried/defaulted
	)
	cfg := FillConfig{
		Down:     []string{"region"},
		Defaults: []FillDefault{{"region", "unknown"}, {"score", int64(0)}},
	}
	var got [][3]any
	for r := range FillRecords(in, cfg) {
		got = append(got, [3]any{GetOr(r, "id", int64(0)), GetOr(r, "region", "?"), GetOr(r, "score", int64(-1))})
	}
	want := [][3]any{
		{int64(1), "unknown", int64(5)},
		{int64(2), "west", int64(7)},
		{int64(3), "west", int64(0)},
		{int64(4), "east", int64(9)},
		{int64(5), "east", int64(0)},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestFillDownOnlyLeavesLeadingGap(t *testing.T) {
	in := recsOf([]kv{{"id", int64(1)}}, []kv{{"id", int64(2)}, {"x", int64(3)}}, []kv{{"id", int64(3)}})
	var xs []int64
	for r := range FillRecords(in, FillConfig{Down: []string{"x"}}) {
		xs = append(xs, GetOr(r, "x", int64(-1)))
	}
	if len(xs) != 3 || xs[0] != -1 || xs[1] != 3 || xs[2] != 3 {
		t.Errorf("down without default: %v, want [-1(absent) 3 3]", xs)
	}
}

func TestFillDoesNotTouchPresentValues(t *testing.T) {
	in := recsOf([]kv{{"a", int64(0)}, {"b", "keep"}})
	for r := range FillRecords(in, FillConfig{Defaults: []FillDefault{{"a", int64(9)}, {"b", "x"}}}) {
		if GetOr(r, "a", int64(-1)) != 0 || GetOr(r, "b", "") != "keep" {
			t.Errorf("present values (incl. a real 0) must be untouched: %v", r)
		}
	}
}
