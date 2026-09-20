package ssql

import (
	"slices"
	"testing"
)

// TestJoinIntKeyToFloatKey (DFC133 row-order sweep): the hash key prints
// both sides ("3"), the confirming Match must agree with it. int64(3) and
// float64(3) are the same key; 3 and "3" are not; a fractional key matches
// nothing on an int side.
func TestJoinIntKeyToFloatKey(t *testing.T) {
	left := []Record{
		MakeMutableRecord().Int("o", 1).Float("k", 3).Freeze(),
		MakeMutableRecord().Int("o", 2).Float("k", 1).Freeze(),
		MakeMutableRecord().Int("o", 3).Float("k", 2.5).Freeze(),
	}
	right := []Record{
		MakeMutableRecord().Int("k", 1).String("name", "one").Freeze(),
		MakeMutableRecord().Int("k", 3).String("name", "three").Freeze(),
		MakeMutableRecord().String("k", "2.5").String("name", "text").Freeze(),
	}
	var got []string
	for r := range InnerJoin(slices.Values(right), OnFields("k"))(slices.Values(left)) {
		got = append(got, GetOr(r, "name", ""))
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"one", "three"}) {
		t.Errorf("int/float keys must match as numbers, and a number must not match text: %v", got)
	}
	for _, c := range []struct {
		a, b any
		want bool
	}{{int64(3), float64(3), true}, {float64(2.5), int64(2), false}, {int64(3), "3", false}, {"a", "a", true}, {true, "true", true}} {
		if joinValuesEqual(c.a, c.b) != c.want {
			t.Errorf("joinValuesEqual(%v, %v) != %v", c.a, c.b, c.want)
		}
	}
}
