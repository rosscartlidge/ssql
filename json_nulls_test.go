package ssql

import (
	"bytes"
	"encoding/json"
	"math"
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

// TestNonFiniteFloatsStayValidJSON (DFC133): JSON has no NaN or Infinity.
// The writer used to emit the bare words, the line stopped being JSON, and
// the next stage skipped it — `update -set-expr z '0.0/0.0'` made whole
// rows vanish, exit 0. They are written as null (no value); negative zero
// is written as 0 so that write → read → write is a fixed point.
func TestNonFiniteFloatsStayValidJSON(t *testing.T) {
	nan := math.NaN()
	r := MakeMutableRecord().Int("id", 1).Float("nan", nan).Float("inf", math.Inf(1)).Float("ninf", math.Inf(-1)).Float("nz", math.Copysign(0, -1)).Float("ok", 2.5).Freeze()
	line := r.AppendJSON(nil)
	var generic map[string]any
	if err := json.Unmarshal(line, &generic); err != nil {
		t.Fatalf("the writer produced invalid JSON: %v\n%s", err, line)
	}
	back, err := ParseJSONLineWithNulls(line)
	if err != nil {
		t.Fatalf("ssql cannot read its own line: %v\n%s", err, line)
	}
	rec := back.Freeze()
	if got := GetOr(rec, "id", int64(0)); got != 1 {
		t.Errorf("the row did not survive: %s", line)
	}
	for _, f := range []string{"nan", "inf", "ninf"} {
		if rec.HasValue(f) || !rec.Has(f) {
			t.Errorf("%s must read back as a field without a value: %s", f, line)
		}
	}
	if got := GetOr(rec, "ok", 0.0); got != 2.5 {
		t.Errorf("ok = %v", got)
	}
	if again := rec.AppendJSON(nil); !bytes.Equal(again, r.AppendJSON(nil)) && !bytes.Contains(again, []byte(`"nz":0`)) {
		t.Errorf("not a fixed point:\n  %s\n  %s", line, again)
	}
}
