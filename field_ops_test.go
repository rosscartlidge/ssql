package ssql

import (
	"testing"
	"time"
)

func TestFieldOp(t *testing.T) {
	r := MakeMutableRecord().
		Int("i", 5).Int("j", 5).Int("k", 7).Float("f", 5.0).Float("g", 5.5).
		String("s", "abc").String("p", "b").String("q", "^a").String("num", "5").
		Bool("t", true).Bool("u", false).
		Time("d1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)).Time("d2", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)).
		Freeze()
	cases := []struct {
		left, op, right string
		want            bool
	}{
		{"i", "eq", "j", true}, {"i", "ne", "k", true}, {"i", "lt", "k", true}, {"k", "ge", "i", true}, {"i", "gt", "k", false},
		{"i", "eq", "f", true}, {"i", "lt", "g", true}, {"g", "gt", "i", true}, // int against float, as numbers
		{"i", "eq", "num", false}, {"i", "gt", "num", false},                  // mixed kinds: never a string comparison
		{"s", "contains", "p", true}, {"s", "startswith", "p", false}, {"s", "endswith", "p", false}, {"s", "regex", "q", true},
		{"i", "contains", "p", false},                                    // string operators need two strings
		{"s", "gt", "p", false}, {"p", "gt", "s", true}, {"s", "lt", "p", true}, // lexical: "b" > "abc"
		{"t", "eq", "u", false}, {"t", "ne", "u", true}, {"t", "gt", "u", true},
		{"d1", "lt", "d2", true}, {"d2", "ge", "d1", true}, {"d1", "eq", "d2", false},
		{"i", "eq", "absent", false}, {"absent", "eq", "i", false}, {"i", "ne", "absent", false}, // absent: false, never true
		{"i", "bogus", "j", false},
	}
	for _, c := range cases {
		if got := FieldOp(r, c.left, c.op, c.right); got != c.want {
			t.Errorf("%s %s %s: got %v, want %v", c.left, c.op, c.right, got, c.want)
		}
	}
}

func TestCopyField(t *testing.T) {
	src := MakeMutableRecord().Int("a", 1).String("b", "x").Freeze()
	m := CopyField(src.ToMutable(), src, "c", "a")
	if v, ok := Get[int64](m.Freeze(), "c"); !ok || v != 1 {
		t.Errorf("copy: %v %v", v, ok)
	}
	m = CopyField(src.ToMutable(), src, "b", "nope")
	if _, ok := Get[any](m.Freeze(), "b"); ok {
		t.Error("an absent source must leave the target absent")
	}
}
