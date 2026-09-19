package lib

import (
	"slices"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

// TestInferFromSample (DFC128 D3): the header of a headerless source is
// the union of the sample's fields in first-seen order, so a column that
// is NULL (absent) in the first record survives; int+float widens to
// float, any other disagreement is a string.
func TestInferFromSample(t *testing.T) {
	recs := []ssql.Record{
		ssql.MakeMutableRecord().Int("id", 1).Freeze(),
		ssql.MakeMutableRecord().Int("id", 2).String("note", "b").Int("score", 4).Int("mixed", 1).Freeze(),
		ssql.MakeMutableRecord().Int("id", 3).Float("score", 2.5).String("mixed", "x").Bool("late", true).Freeze(),
	}
	s := InferFromSample(recs)
	for _, f := range []string{"id", "note", "score", "mixed", "late"} {
		if !s.HasField(f) {
			t.Errorf("field %q missing from %v", f, s.Fields)
		}
	}
	if s.Fields[0] != "id" || slices.Index(s.Fields, "late") != len(s.Fields)-1 {
		t.Errorf("first-seen order not kept: %v", s.Fields)
	}
	want := map[string]string{"id": TypeInt, "note": TypeString, "score": TypeFloat, "mixed": TypeString, "late": TypeBool}
	for f, ty := range want {
		if s.Types[f] != ty {
			t.Errorf("type of %q = %q, want %q", f, s.Types[f], ty)
		}
	}
	if got := InferFromSample(nil); len(got.Fields) != 0 {
		t.Errorf("empty sample: %v", got.Fields)
	}
}
