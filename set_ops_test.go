package ssql

import (
	"slices"
	"testing"
)

func setOpRows(rows ...[]any) []Record {
	var out []Record
	for _, row := range rows {
		m := MakeMutableRecord()
		for i := 0; i+1 < len(row); i += 2 {
			m = m.String(row[i].(string), row[i+1].(string))
		}
		out = append(out, m.Freeze())
	}
	return out
}

func setOpNames(recs []Record) []string {
	var out []string
	for _, r := range recs {
		out = append(out, GetOr(r, "id", "")+":"+GetOr(r, "v", ""))
	}
	return out
}

func TestExceptIntersectWholeRow(t *testing.T) {
	left := setOpRows(
		[]any{"id", "a", "v", "1"},
		[]any{"id", "b", "v", "2"},
		[]any{"id", "a", "v", "1"},
		[]any{"id", "c", "v", "3"},
		[]any{"id", "a", "v", "1"},
	)
	right := setOpRows(
		[]any{"id", "a", "v", "1"},
		[]any{"id", "d", "v", "4"},
	)
	seq := func(rs []Record) func(func(Record) bool) { return slices.Values(rs) }

	cases := []struct {
		name string
		got  []Record
		want []string
	}{
		{"except distinct", slices.Collect(Except(seq(right), WholeRow, WholeRow, false)(seq(left))), []string{"b:2", "c:3"}},
		// EXCEPT ALL: the one right a:1 cancels ONE of the three left a:1
		{"except all", slices.Collect(Except(seq(right), WholeRow, WholeRow, true)(seq(left))), []string{"b:2", "a:1", "c:3", "a:1"}},
		{"intersect distinct", slices.Collect(Intersect(seq(right), WholeRow, WholeRow, false)(seq(left))), []string{"a:1"}},
		// INTERSECT ALL: min(3, 1) = one a:1
		{"intersect all", slices.Collect(Intersect(seq(right), WholeRow, WholeRow, true)(seq(left))), []string{"a:1"}},
	}
	for _, c := range cases {
		if got := setOpNames(c.got); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestExceptIntersectKeyed(t *testing.T) {
	// Left rows carry a value the right side does not have: the key
	// decides membership, the left row comes out unchanged.
	left := setOpRows(
		[]any{"id", "a", "v", "x"},
		[]any{"id", "b", "v", "y"},
		[]any{"id", "a", "v", "z"},
		[]any{"id", "a", "v", "x"},
		[]any{"v", "no-id"},
	)
	right := setOpRows(
		[]any{"cust", "a", "w", "1"},
		[]any{"cust", "a", "w", "2"},
		[]any{"cust", "d", "w", "3"},
		[]any{"w", "no-cust"},
	)
	seq := func(rs []Record) func(func(Record) bool) { return slices.Values(rs) }
	lk, rk := FieldsKey("id"), FieldsKey("cust")

	cases := []struct {
		name string
		got  []Record
		want []string
	}{
		// anti-join, distinct by whole row; the row with no id matches
		// nothing and is kept
		{"except", slices.Collect(Except(seq(right), lk, rk, false)(seq(left))), []string{"b:y", ":no-id"}},
		// -all keeps duplicate left rows; keys are membership only (two
		// right a rows do not cancel anything)
		{"except all", slices.Collect(Except(seq(right), lk, rk, true)(seq(left))), []string{"b:y", ":no-id"}},
		// semi-join: a:x once (distinct), a:z; the row with no id is dropped
		{"intersect", slices.Collect(Intersect(seq(right), lk, rk, false)(seq(left))), []string{"a:x", "a:z"}},
		{"intersect all", slices.Collect(Intersect(seq(right), lk, rk, true)(seq(left))), []string{"a:x", "a:z", "a:x"}},
	}
	for _, c := range cases {
		if got := setOpNames(c.got); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestFieldsKeyNumbersAndText(t *testing.T) {
	k := FieldsKey("n")
	key := func(r Record) string {
		s, _ := k(r)
		return s
	}
	asInt := key(MakeMutableRecord().Int("n", 3).Freeze())
	asFloat := key(MakeMutableRecord().Float("n", 3).Freeze())
	asText := key(MakeMutableRecord().String("n", "3").Freeze())
	if asInt != asFloat {
		t.Errorf("int 3 and float 3 must share a key, as join keys do")
	}
	if asInt == asText {
		t.Errorf("a number must not share a key with the text that prints like it")
	}
	if _, ok := k(MakeMutableRecord().Int("other", 1).Freeze()); ok {
		t.Errorf("an absent field must give no key")
	}
	two := FieldsKey("a", "b")
	ab, _ := two(MakeMutableRecord().String("a", "x").String("b", "y").Freeze())
	ba, _ := two(MakeMutableRecord().String("a", "y").String("b", "x").Freeze())
	if ab == ba {
		t.Errorf("composite keys must be ordered")
	}
}
