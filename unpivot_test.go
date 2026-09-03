package ssql

import "testing"

func TestUnpivotGoldenHandComputed(t *testing.T) {
	in := recsOf(
		[]kv{{"name", "Alice"}, {"jan", int64(10)}, {"feb", int64(20)}},
		[]kv{{"name", "Bob"}, {"jan", int64(5)}}, // feb absent → no row
	)
	var got [][3]any
	for r := range UnpivotRecords(in, UnpivotConfig{IDs: []string{"name"}, Values: []string{"jan", "feb"}, NameField: "month", ValueField: "amount"}) {
		got = append(got, [3]any{GetOr(r, "name", ""), GetOr(r, "month", ""), GetOr(r, "amount", int64(-1))})
		if r.Has("jan") || r.Has("feb") {
			t.Errorf("value fields must not leak into output: %v", r)
		}
	}
	want := [][3]any{{"Alice", "jan", int64(10)}, {"Alice", "feb", int64(20)}, {"Bob", "jan", int64(5)}}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestUnpivotDefaultsAllNonIDSorted(t *testing.T) {
	in := recsOf([]kv{{"id", int64(1)}, {"z", "last"}, {"a", "first"}, {"m", 2.5}})
	var names []string
	for r := range UnpivotRecords(in, UnpivotConfig{IDs: []string{"id"}}) {
		names = append(names, GetOr(r, "name", ""))
		if !r.Has("value") || GetOr(r, "id", int64(0)) != 1 {
			t.Errorf("default field names / id copy wrong: %v", r)
		}
	}
	if len(names) != 3 || names[0] != "a" || names[1] != "m" || names[2] != "z" {
		t.Errorf("default values must be all non-id fields sorted by name, got %v", names)
	}
}

func TestUnpivotKeepsValueTypes(t *testing.T) {
	in := recsOf([]kv{{"k", "x"}, {"i", int64(3)}, {"f", 1.5}, {"s", "str"}, {"b", true}})
	types := map[string]string{}
	for r := range UnpivotRecords(in, UnpivotConfig{IDs: []string{"k"}}) {
		n := GetOr(r, "name", "")
		switch {
		case r.Has("value") && GetOr(r, "value", int64(-9)) != int64(-9) && n == "i":
			types[n] = "int"
		case n == "f" && GetOr(r, "value", -9.0) == 1.5:
			types[n] = "float"
		case n == "s" && GetOr(r, "value", "") == "str":
			types[n] = "string"
		case n == "b" && GetOr(r, "value", false):
			types[n] = "bool"
		}
	}
	for _, n := range []string{"i", "f", "s", "b"} {
		if types[n] == "" {
			t.Errorf("value of %q lost its type", n)
		}
	}
}
