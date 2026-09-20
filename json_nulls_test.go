package ssql

import (
	"bytes"
	"testing"
)

// TestJSONNullIsANilSlot (DFC128 §6g): on the wire-format path a JSON null
// keeps its key as a nil slot — the field exists, it has no value — so
// the schema names the column, Get reports it absent, the writer skips
// it, and same-shaped rows share one Schema whether or not a value is
// null (they used to change shape with every null).
func TestJSONNullIsANilSlot(t *testing.T) {
	in := "{\"id\":1,\"note\":null}\n{\"id\":2,\"note\":\"x\"}\n{\"id\":3,\"note\":null}\n"
	var recs []Record
	for r := range ReadJSONLFromReader(bytes.NewReader([]byte(in))) {
		recs = append(recs, r)
	}
	if len(recs) != 3 {
		t.Fatalf("%d records", len(recs))
	}
	for i, r := range recs {
		if !r.Has("note") {
			t.Errorf("record %d: schema lost the null-valued key", i)
		}
	}
	if _, ok := Get[any](recs[0], "note"); ok {
		t.Error("a nil slot must read as absent")
	}
	if got := GetOr(recs[0], "note", "dflt"); got != "dflt" {
		t.Errorf("GetOr on a nil slot = %q", got)
	}
	if got := GetOr(recs[1], "note", ""); got != "x" {
		t.Errorf("value row = %q", got)
	}
	if recs[0].Schema() != recs[1].Schema() || recs[1].Schema() != recs[2].Schema() {
		t.Error("rows that differ only in which values are null must share one Schema")
	}
	if got := string(recs[0].AppendJSON(nil)); got != `{"id":1}` {
		t.Errorf("the writer must skip a nil slot, got %s", got)
	}

	// The legacy parser keeps its contract: the null key is dropped.
	m, err := ParseJSONLine([]byte(`{"id":1,"note":null}`))
	if err != nil || m.Freeze().Has("note") {
		t.Errorf("ParseJSONLine must still drop nulls (%v)", err)
	}
	if r := MakeMutableRecord().Int("id", 1).Null("gone").Freeze(); !r.Has("gone") || r.Len() != 2 {
		t.Errorf("Null() must add a valueless field: %v", r)
	}
}
