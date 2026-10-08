package ssql

import (
	"strings"
	"testing"
)

// DFC144 Level 0: one representation of a nested value (JSONString) and
// the latent panics around in-memory slices.

func TestEqualDoesNotPanicOnSliceValues(t *testing.T) {
	a := NewRecord(map[string]any{"tags": []any{"x", "y"}})
	b := NewRecord(map[string]any{"tags": []any{"x", "y"}})
	c := NewRecord(map[string]any{"tags": []any{"x"}})
	if !a.Equal(b) {
		t.Error("equal slices should compare equal")
	}
	if a.Equal(c) {
		t.Error("different slices should not compare equal")
	}
	d := NewRecord(map[string]any{"tags": JSONString(`["x","y"]`)})
	if a.Equal(d) {
		t.Error("a slice and a JSONString are different values")
	}
}

func TestSimpleValueRefusesSlicesAndMaps(t *testing.T) {
	if isSimpleValue([]any{1}) || isSimpleValue(map[string]any{"a": 1}) {
		t.Error("a slice or map is not a grouping key")
	}
	if !isSimpleValue(JSONString(`[1]`)) {
		t.Error("a JSONString is a grouping key (its text)")
	}
	if !isValueType([]any{1}) {
		t.Error("[]any is in the Value constraint and must pass isValueType")
	}
}

func TestUnmarshalJSONNestedIsJSONString(t *testing.T) {
	var r Record
	if err := r.UnmarshalJSON([]byte(`{"n": 1, "tags": [ "a" , "b" ], "addr": {"zip": "1", "city": "x"}}`)); err != nil {
		t.Fatal(err)
	}
	tags, ok := Get[JSONString](r, "tags")
	if !ok || string(tags) != `["a","b"]` {
		t.Errorf("tags = %v (%T), want compacted JSON text", tags, tags)
	}
	addr, ok := Get[JSONString](r, "addr")
	if !ok || string(addr) != `{"zip":"1","city":"x"}` {
		t.Errorf("addr = %v, want the object's text in source order", addr)
	}
}

func TestCollectEmitsJSONString(t *testing.T) {
	recs := []Record{
		NewRecord(map[string]any{"g": "a", "v": int64(1)}),
		NewRecord(map[string]any{"g": "a", "v": int64(2)}),
	}
	res := Collect("v")(recs)
	js, ok := res.GetValue().(JSONString)
	if !ok {
		t.Fatalf("Collect returned %T, want JSONString", res.GetValue())
	}
	if string(js) != "[1,2]" {
		t.Errorf("Collect = %s", js)
	}
	if empty := Collect("missing")(recs).GetValue().(JSONString); string(empty) != "[]" {
		t.Errorf("empty Collect = %s", empty)
	}
}

func TestCastValueJSON(t *testing.T) {
	if v, ok := CastValue(`[1, 2]`, FieldTypeJSON); !ok || v.(JSONString) != JSONString("[1, 2]") {
		t.Errorf("CastValue to json = %v %v", v, ok)
	}
	if _, ok := CastValue("notjson", FieldTypeJSON); ok {
		t.Error("plain text must not cast to json")
	}
	if _, ok := CastValue(JSONString(`{"a":`), FieldTypeJSON); ok {
		t.Error("invalid JSON text must not cast to json")
	}
	ft, err := ParseFieldType("json")
	if err != nil || ft != FieldTypeJSON || !strings.EqualFold(ft.String(), "json") {
		t.Errorf("ParseFieldType(json) = %v %v", ft, err)
	}
}
