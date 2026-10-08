package ssql

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// DFC144 Level 1: the boundary between a nested value's text and the Go
// values an expression works on.

func TestExprValueOpensJSONStrings(t *testing.T) {
	got := ExprValue(JSONString(`{"a":[1,2.5,"x"],"b":{"c":30}}`))
	want := map[string]any{"a": []any{int64(1), 2.5, "x"}, "b": map[string]any{"c": int64(30)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExprValue = %#v, want %#v", got, want)
	}
	if v := ExprValue(JSONString(`not json`)); v != JSONString(`not json`) {
		t.Errorf("unparsable text must stay as it is, got %#v", v)
	}
	if v := ExprValue(int64(3)); v != int64(3) {
		t.Errorf("a scalar passes through, got %#v", v)
	}
}

func TestNestedValueRecognisesSlicesAndMaps(t *testing.T) {
	cases := []struct {
		in   any
		want string
		ok   bool
	}{
		{[]string{"a", "b"}, `["a","b"]`, true},
		{[]any{int64(1), "x"}, `[1,"x"]`, true},
		{map[string]any{"k": 1}, `{"k":1}`, true},
		{JSONString(`[1]`), `[1]`, true},
		{"text", "", false},
		{int64(1), "", false},
		{nil, "", false},
	}
	for _, c := range cases {
		js, ok := NestedValue(c.in)
		if ok != c.ok || string(js) != c.want {
			t.Errorf("NestedValue(%#v) = %q, %v; want %q, %v", c.in, js, ok, c.want, c.ok)
		}
	}
}

// DFC144 Level 2: dotted paths resolve into nested values through Get;
// a literal dotted field wins; explode and flatten are the structural moves.

func TestGetResolvesDottedPaths(t *testing.T) {
	r := NewRecord(map[string]any{
		"addr": JSONString(`{"city":"NYC","geo":{"lat":40.7},"zip":"10001"}`),
		"tags": JSONString(`["go","rust"]`),
		"a.b":  "literal",
		"a":    JSONString(`{"b":"path"}`),
	})
	if got := GetOr(r, "addr.city", ""); got != "NYC" {
		t.Errorf("addr.city = %q", got)
	}
	if got := GetOr(r, "addr.geo.lat", 0.0); got != 40.7 {
		t.Errorf("addr.geo.lat = %v", got)
	}
	if got := GetOr(r, "addr.geo", JSONString("")); string(got) != `{"lat":40.7}` {
		t.Errorf("a nested leaf is JSON text, got %q", got)
	}
	if got := GetOr(r, "tags.0", ""); got != "go" {
		t.Errorf("tags.0 = %q", got)
	}
	if got := GetOr(r, "tags.-1", ""); got != "rust" {
		t.Errorf("tags.-1 = %q", got)
	}
	if got := GetOr(r, "a.b", ""); got != "literal" {
		t.Errorf("a literal dotted field must win over a path: %q", got)
	}
	if _, ok := Get[any](r, "addr.nope"); ok {
		t.Error("a missing key is no field")
	}
	if _, ok := Get[any](r, "tags.9"); ok {
		t.Error("an index past the end is no field")
	}
	if !r.Has("addr.city") || r.Has("addr.nope") || !r.HasValue("tags.1") {
		t.Error("Has/HasValue must follow paths")
	}
}

func TestExplodeListField(t *testing.T) {
	rows := []Record{
		NewRecord(map[string]any{"id": int64(1), "tags": JSONString(`["go","rust"]`)}),
		NewRecord(map[string]any{"id": int64(2), "tags": JSONString(`[]`)}),
		NewRecord(map[string]any{"id": int64(3)}),
		NewRecord(map[string]any{"id": int64(4), "tags": JSONString(`[7,{"k":1}]`)}),
	}
	var got []string
	for r := range Explode("tags", false)(From(rows)) {
		v, _ := Get[any](r, "tags")
		got = append(got, fmt.Sprintf("%v:%v", GetOr(r, "id", int64(0)), v))
	}
	want := []string{"1:go", "1:rust", "4:7", `4:{"k":1}`}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("explode = %v, want %v", got, want)
	}
	var kept []int64
	for r := range Explode("tags", true)(From(rows)) {
		if !r.HasValue("tags") {
			kept = append(kept, GetOr(r, "id", int64(0)))
		}
	}
	if fmt.Sprint(kept) != "[2 3]" {
		t.Errorf("-keep-empty rows = %v, want the empty and the missing list", kept)
	}
	scalar := []Record{NewRecord(map[string]any{"tags": "x"})}
	func() {
		defer func() {
			if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "not a list") {
				t.Errorf("exploding a scalar must fail loudly, got %v", r)
			}
		}()
		for range Explode("tags", false)(From(scalar)) {
		}
	}()
}

func TestFlattenObjectField(t *testing.T) {
	rows := []Record{
		NewRecord(map[string]any{"id": int64(1), "addr": JSONString(`{"zip":"10001","city":"NYC","geo":{"lat":40.7,"lng":-74}}`)}),
		NewRecord(map[string]any{"id": int64(2), "addr": JSONString(`{"zip":"94105","city":"SF"}`)}),
		NewRecord(map[string]any{"id": int64(3)}),
	}
	var out []Record
	for r := range FlattenField("addr", 1, false)(From(rows)) {
		out = append(out, r)
	}
	if len(out) != 3 {
		t.Fatalf("%d rows", len(out))
	}
	if out[0].Has("addr") || GetOr(out[0], "addr.city", "") != "NYC" || GetOr(out[0], "addr.zip", "") != "10001" {
		t.Errorf("row 1 = %v", out[0])
	}
	if got := GetOr(out[0], "addr.geo", JSONString("")); string(got) != `{"lat":40.7,"lng":-74}` {
		t.Errorf("a nested object below -depth stays JSON, got %q", got)
	}
	if fields := slices.Collect(out[0].KeysIter()); fmt.Sprint(fields) != "[id addr.zip addr.city addr.geo]" {
		t.Errorf("key order must be the object's: %v", fields)
	}
	if !out[1].Has("addr.geo") || out[1].HasValue("addr.geo") {
		t.Error("a key the first row had and this row lacks is a field with no value")
	}
	if !out[2].Has("id") || out[2].Has("addr.city") {
		t.Error("a row without the object passes through unchanged")
	}
	var deep []Record
	for r := range FlattenField("addr", 2, true)(From(rows[:1])) {
		deep = append(deep, r)
	}
	if GetOr(deep[0], "addr.geo.lat", 0.0) != 40.7 || !deep[0].Has("addr") {
		t.Errorf("-depth 2 -keep: %v", deep[0])
	}
	extra := []Record{rows[1], NewRecord(map[string]any{"id": int64(9), "addr": JSONString(`{"zip":"1","city":"x","country":"AU"}`)})}
	func() {
		defer func() {
			if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), `"country"`) {
				t.Errorf("a new key in a later row must fail naming it, got %v", r)
			}
		}()
		for range FlattenField("addr", 1, false)(From(extra)) {
		}
	}()
}
