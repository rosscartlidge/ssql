//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"
)

func TestParseEmptyArray(t *testing.T) {
	ds, err := parseJSONArray("[]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(ds.rows))
	}
}

func TestParseSingleObject(t *testing.T) {
	ds, err := parseJSONArray(`[{"name":"Alice","age":30}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(ds.rows))
	}
	if len(ds.s.fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(ds.s.fields))
	}
	name := ds.rows[0].mustGet("name")
	if name != "Alice" {
		t.Errorf("expected Alice, got %v", name)
	}
	age := ds.rows[0].mustGet("age")
	if age != int64(30) {
		t.Errorf("expected 30 (int64), got %v (%T)", age, age)
	}
}

func TestParseMultipleObjects(t *testing.T) {
	ds, err := parseJSONArray(`[{"name":"Alice","age":30},{"name":"Bob","age":25}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(ds.rows))
	}
	// All rows share the same schema
	if ds.rows[0].s != ds.rows[1].s {
		t.Error("rows should share the same schema pointer")
	}
	name := ds.rows[1].mustGet("name")
	if name != "Bob" {
		t.Errorf("expected Bob, got %v", name)
	}
}

func TestParseMixedTypes(t *testing.T) {
	ds, err := parseJSONArray(`[{"s":"hello","i":42,"f":3.14,"b":true,"n":null}]`)
	if err != nil {
		t.Fatal(err)
	}
	r := ds.rows[0]
	if v := r.mustGet("s"); v != "hello" {
		t.Errorf("string: expected hello, got %v", v)
	}
	if v := r.mustGet("i"); v != int64(42) {
		t.Errorf("int: expected 42, got %v (%T)", v, v)
	}
	if v := r.mustGet("f"); v != 3.14 {
		t.Errorf("float: expected 3.14, got %v", v)
	}
	if v := r.mustGet("b"); v != true {
		t.Errorf("bool: expected true, got %v", v)
	}
	if v := r.mustGet("n"); v != nil {
		t.Errorf("null: expected nil, got %v", v)
	}
}

func TestParseNestedObjects(t *testing.T) {
	ds, err := parseJSONArray(`[{"name":"Alice","addr":{"city":"NYC","zip":"10001"}}]`)
	if err != nil {
		t.Fatal(err)
	}
	addr := ds.rows[0].mustGet("addr")
	raw, ok := addr.(rawJSON)
	if !ok {
		t.Fatalf("expected rawJSON, got %T", addr)
	}
	if !strings.Contains(string(raw), "NYC") {
		t.Errorf("expected nested object with NYC, got %s", raw)
	}
}

func TestParseNestedArrays(t *testing.T) {
	ds, err := parseJSONArray(`[{"name":"Alice","scores":[1,2,3]}]`)
	if err != nil {
		t.Fatal(err)
	}
	scores := ds.rows[0].mustGet("scores")
	raw, ok := scores.(rawJSON)
	if !ok {
		t.Fatalf("expected rawJSON, got %T", scores)
	}
	if string(raw) != "[1,2,3]" {
		t.Errorf("expected [1,2,3], got %s", raw)
	}
}

func TestParseEscapedStrings(t *testing.T) {
	ds, err := parseJSONArray(`[{"msg":"hello\nworld","path":"C:\\dir\\file"}]`)
	if err != nil {
		t.Fatal(err)
	}
	msg := ds.rows[0].mustGet("msg")
	if msg != "hello\nworld" {
		t.Errorf("expected hello\\nworld, got %q", msg)
	}
	path := ds.rows[0].mustGet("path")
	if path != `C:\dir\file` {
		t.Errorf("expected C:\\dir\\file, got %q", path)
	}
}

func TestParseUnicodeEscapes(t *testing.T) {
	ds, err := parseJSONArray(`[{"emoji":"\u0041\u0042"}]`)
	if err != nil {
		t.Fatal(err)
	}
	v := ds.rows[0].mustGet("emoji")
	if v != "AB" {
		t.Errorf("expected AB, got %q", v)
	}
}

func TestParseNumberTypes(t *testing.T) {
	ds, err := parseJSONArray(`[{"int":42,"neg":-7,"float":3.14,"exp":1e3,"negexp":2.5E-1}]`)
	if err != nil {
		t.Fatal(err)
	}
	r := ds.rows[0]
	if v := r.mustGet("int"); v != int64(42) {
		t.Errorf("int: expected int64(42), got %v (%T)", v, v)
	}
	if v := r.mustGet("neg"); v != int64(-7) {
		t.Errorf("neg: expected int64(-7), got %v (%T)", v, v)
	}
	if v := r.mustGet("float"); v != 3.14 {
		t.Errorf("float: expected 3.14, got %v", v)
	}
	if v := r.mustGet("exp"); v != float64(1000) {
		t.Errorf("exp: expected 1000.0, got %v (%T)", v, v)
	}
	if v := r.mustGet("negexp"); v != 0.25 {
		t.Errorf("negexp: expected 0.25, got %v", v)
	}
}

func TestParseExtraFields(t *testing.T) {
	// Second object has a field not in the first object
	ds, err := parseJSONArray(`[{"a":1},{"a":2,"b":3}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(ds.rows))
	}
	// Schema should have expanded
	if len(ds.s.fields) != 2 {
		t.Errorf("expected 2 fields, got %d: %v", len(ds.s.fields), ds.s.fields)
	}
	// First row should have nil for field b
	if v := ds.rows[0].mustGet("b"); v != nil {
		t.Errorf("expected nil for missing field b, got %v", v)
	}
	if v := ds.rows[1].mustGet("b"); v != int64(3) {
		t.Errorf("expected 3 for field b, got %v", v)
	}
}

func TestParseMissingFields(t *testing.T) {
	// Second object is missing a field from the first
	ds, err := parseJSONArray(`[{"a":1,"b":2},{"a":3}]`)
	if err != nil {
		t.Fatal(err)
	}
	if v := ds.rows[1].mustGet("b"); v != nil {
		t.Errorf("expected nil for missing field b, got %v", v)
	}
}

func TestParseWhitespace(t *testing.T) {
	ds, err := parseJSONArray(`  [  { "a" : 1 } , { "a" : 2 }  ]  `)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(ds.rows))
	}
}

func TestParseErrorCases(t *testing.T) {
	tests := []string{
		`{`,         // not an array
		`[{`,        // unterminated object
		`[{"a":}]`,  // missing value
		`[{"a" 1}]`, // missing colon
	}
	for _, input := range tests {
		_, err := parseJSONArray(input)
		if err == nil {
			t.Errorf("expected error for input %q", input)
		}
	}
}

func TestParseEmptyString(t *testing.T) {
	ds, err := parseJSONArray("")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(ds.rows))
	}
}

// ============================================================================
// Serializer tests
// ============================================================================

func TestToJSONEmpty(t *testing.T) {
	ds := dataset{s: newSchema(nil)}
	got := ds.toJSON()
	if got != "[]" {
		t.Errorf("expected [], got %s", got)
	}
}

func TestToJSONRoundTrip(t *testing.T) {
	input := `[{"name":"Alice","age":30},{"name":"Bob","age":25}]`
	ds, err := parseJSONArray(input)
	if err != nil {
		t.Fatal(err)
	}
	output := ds.toJSON()
	// Parse again to verify
	ds2, err := parseJSONArray(output)
	if err != nil {
		t.Fatalf("failed to reparse: %v", err)
	}
	if len(ds2.rows) != 2 {
		t.Errorf("expected 2 rows after round trip, got %d", len(ds2.rows))
	}
	if ds2.rows[0].mustGet("name") != "Alice" {
		t.Error("name mismatch after round trip")
	}
	if ds2.rows[0].mustGet("age") != int64(30) {
		t.Error("age mismatch after round trip")
	}
}

func TestToJSONSkipsNil(t *testing.T) {
	ds := makeTestDataset([]string{"a", "b"}, [][]any{
		{int64(1), nil},
	})
	got := ds.toJSON()
	if strings.Contains(got, "null") {
		t.Errorf("nil values should be skipped, got %s", got)
	}
	if !strings.Contains(got, `"a":1`) {
		t.Errorf("expected a:1 in output, got %s", got)
	}
}

func TestToJSONEscaping(t *testing.T) {
	ds := makeTestDataset([]string{"msg"}, [][]any{
		{"hello\nworld"},
	})
	got := ds.toJSON()
	if !strings.Contains(got, `\n`) {
		t.Errorf("expected escaped newline, got %s", got)
	}
}

func TestToJSONRawJSON(t *testing.T) {
	ds := makeTestDataset([]string{"name", "nested"}, [][]any{
		{"Alice", rawJSON(`{"x":1}`)},
	})
	got := ds.toJSON()
	if !strings.Contains(got, `{"x":1}`) {
		t.Errorf("expected raw nested JSON, got %s", got)
	}
}

func TestToJSONBooleans(t *testing.T) {
	ds := makeTestDataset([]string{"a", "b"}, [][]any{
		{true, false},
	})
	got := ds.toJSON()
	if !strings.Contains(got, `"a":true`) {
		t.Errorf("expected true, got %s", got)
	}
	if !strings.Contains(got, `"b":false`) {
		t.Errorf("expected false, got %s", got)
	}
}

// ============================================================================
// errorJSON test
// ============================================================================

func TestErrorJSON(t *testing.T) {
	got := errorJSON("something failed")
	if !strings.Contains(got, `"error"`) {
		t.Errorf("expected error key, got %s", got)
	}
	if !strings.Contains(got, "something failed") {
		t.Errorf("expected message, got %s", got)
	}
}

// ============================================================================
// Pipeline ops parsing
// ============================================================================

func TestParsePipelineOps(t *testing.T) {
	input := `[
		{"op":"where","field":"age","operator":"gt","value":"25"},
		{"op":"sort","field":"name","desc":true},
		{"op":"group_by","groupFields":["dept"],"aggs":[{"field":"salary","func":"sum","alias":"total"}]},
		{"op":"distinct","field":"name"},
		{"op":"limit","n":10,"offset":5}
	]`
	ops, err := parsePipelineOps(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 5 {
		t.Fatalf("expected 5 ops, got %d", len(ops))
	}

	// where
	if ops[0].Op != "where" || ops[0].Field != "age" || ops[0].Operator != "gt" || ops[0].Value != "25" {
		t.Errorf("where op wrong: %+v", ops[0])
	}
	// sort
	if ops[1].Op != "sort" || ops[1].Field != "name" || !ops[1].Desc {
		t.Errorf("sort op wrong: %+v", ops[1])
	}
	// group_by
	if ops[2].Op != "group_by" || len(ops[2].GroupFields) != 1 || ops[2].GroupFields[0] != "dept" {
		t.Errorf("group_by op wrong: %+v", ops[2])
	}
	if len(ops[2].Aggs) != 1 || ops[2].Aggs[0].Func != "sum" || ops[2].Aggs[0].Alias != "total" {
		t.Errorf("group_by aggs wrong: %+v", ops[2].Aggs)
	}
	// distinct
	if ops[3].Op != "distinct" || ops[3].Field != "name" {
		t.Errorf("distinct op wrong: %+v", ops[3])
	}
	// limit
	if ops[4].Op != "limit" || ops[4].N != 10 || ops[4].Offset != 5 {
		t.Errorf("limit op wrong: %+v", ops[4])
	}
}

func TestParsePipelineOpsNewTypes(t *testing.T) {
	input := `[
		{"op": "group_by", "groupFields": ["dept", "region"], "aggs": [{"field": "salary", "func": "sum", "alias": "total"}, {"field": "", "func": "count", "alias": "n"}]},
		{"op": "compute", "name": "annual", "expr": "salary * 12"},
		{"op": "pivot", "rowField": "dept", "colField": "quarter", "valField": "revenue", "aggFunc": "sum"}
	]`

	ops, err := parsePipelineOps(input)
	if err != nil {
		t.Fatal(err)
	}

	if len(ops) != 3 {
		t.Fatalf("expected 3 ops, got %d", len(ops))
	}

	// group_by
	if ops[0].Op != "group_by" {
		t.Errorf("expected group_by, got %q", ops[0].Op)
	}
	if len(ops[0].GroupFields) != 2 || ops[0].GroupFields[0] != "dept" || ops[0].GroupFields[1] != "region" {
		t.Errorf("groupFields wrong: %v", ops[0].GroupFields)
	}
	if len(ops[0].Aggs) != 2 || ops[0].Aggs[0].Field != "salary" || ops[0].Aggs[0].Func != "sum" || ops[0].Aggs[0].Alias != "total" {
		t.Errorf("aggs wrong: %+v", ops[0].Aggs)
	}
	if ops[0].Aggs[1].Func != "count" || ops[0].Aggs[1].Alias != "n" {
		t.Errorf("agg count wrong: %+v", ops[0].Aggs[1])
	}

	// compute
	if ops[1].Op != "compute" || ops[1].Name != "annual" || ops[1].Expr != "salary * 12" {
		t.Errorf("compute op wrong: %+v", ops[1])
	}

	// pivot
	if ops[2].Op != "pivot" || ops[2].RowField != "dept" || ops[2].ColField != "quarter" || ops[2].ValField != "revenue" || ops[2].AggFunc != "sum" {
		t.Errorf("pivot op wrong: %+v", ops[2])
	}
}

// ============================================================================
// Helpers
// ============================================================================

func makeTestDataset(fields []string, rows [][]any) dataset {
	s := newSchema(fields)
	var records []record
	for _, row := range rows {
		values := make([]any, len(fields))
		copy(values, row)
		records = append(records, record{s: s, values: values})
	}
	return dataset{s: s, rows: records}
}
