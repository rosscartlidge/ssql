package lib

import (
	"encoding/json"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

// DFC144 Level 0, bug (a): `to json` wrote a JSONString as a quoted
// string ("[\"x\"]"); it is JSON already and must be emitted as such.
func TestConvertRecordValueJSONStringIsRaw(t *testing.T) {
	v := convertRecordValue(ssql.JSONString(`["x",{"k":1}]`))
	raw, ok := v.(json.RawMessage)
	if !ok || string(raw) != `["x",{"k":1}]` {
		t.Fatalf("convertRecordValue(JSONString) = %v (%T), want json.RawMessage of the text", v, v)
	}
}

// A JSON array file's nested values are JSONString text with the
// source's key order (until 2026-10-08 an object became a nested Record
// built by a map walk, so its key order was random).
func TestSetValueFromJSONNestedIsText(t *testing.T) {
	rec := setValueFromJSON(ssql.MakeMutableRecord(), "o", json.RawMessage(`{ "zip": "1", "city": "x" }`))
	rec = setValueFromJSON(rec, "l", []any{"a", float64(2)})
	r := rec.Freeze()
	if o, ok := ssql.Get[ssql.JSONString](r, "o"); !ok || string(o) != `{"zip":"1","city":"x"}` {
		t.Errorf("object = %v, want compacted text in source order", o)
	}
	if l, ok := ssql.Get[ssql.JSONString](r, "l"); !ok || string(l) != `["a",2]` {
		t.Errorf("list = %v, want JSON text", l)
	}
	if InferTypeString(ssql.JSONString(`[1]`)) != TypeJSON {
		t.Error("a JSONString infers as the json wire type")
	}
}
