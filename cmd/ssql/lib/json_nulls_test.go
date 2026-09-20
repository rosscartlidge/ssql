package lib

import (
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

// TestInferFromSampleNulls (DFC128 §6g): a nil slot names a field without
// typing it; the type comes from the values; all-null is a string column
// (the CSV reader's call for a column with no non-empty sample).
func TestInferFromSampleNulls(t *testing.T) {
	recs := []ssql.Record{
		ssql.MakeMutableRecord().Int("id", 1).Null("score").Null("gone").Freeze(),
		ssql.MakeMutableRecord().Int("id", 2).Int("score", 4).Null("gone").Freeze(),
		ssql.MakeMutableRecord().Int("id", 3).Float("score", 2.5).Null("gone").Freeze(),
	}
	s := InferFromSample(recs)
	want := map[string]string{"id": TypeInt, "score": TypeFloat, "gone": TypeString}
	for f, ty := range want {
		if !s.HasField(f) || s.Types[f] != ty {
			t.Errorf("%q: has=%v type=%q, want %q", f, s.HasField(f), s.Types[f], ty)
		}
	}
	// the null placeholder must not widen a real type to string
	if s.Types["score"] == TypeString {
		t.Error("a leading null turned a numeric column into a string")
	}
}

// TestJSONArrayNullIsNotZero: NULL is NULL in a column of any type, in
// the first element or a later one, and never locks the column's type.
func TestJSONArrayNullIsNotZero(t *testing.T) {
	in := `[{"id":1,"n":5,"score":null},{"id":2,"n":null,"score":2.5},{"id":3,"n":7,"score":4}]`
	var recs []ssql.Record
	for r := range ReadJSON(strings.NewReader(in)) {
		recs = append(recs, r)
	}
	if len(recs) != 3 {
		t.Fatalf("%d records", len(recs))
	}
	if v, ok := ssql.Get[any](recs[1], "n"); ok {
		t.Errorf("a null in an int column became %v", v)
	}
	if !recs[1].Has("n") || !recs[0].Has("score") {
		t.Error("null-valued keys must stay in the schema")
	}
	if got := ssql.GetOr(recs[1], "score", -1.0); got != 2.5 {
		t.Errorf("score after a leading null = %v (type locked by the null?)", got)
	}
	if got := ssql.GetOr(recs[2], "n", int64(-1)); got != 7 {
		t.Errorf("n = %v", got)
	}
}
