package ssql

import (
	"errors"
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
		{"s", "contains", "p", true}, {"s", "startswith", "p", false}, {"s", "endswith", "p", false}, {"s", "regex", "q", true},
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
	// Mixed kinds, and a string operator on a number, cannot be compared:
	// loud, never false and never a comparison of renderings.
	for _, c := range [][3]string{{"i", "eq", "num"}, {"i", "gt", "s"}, {"t", "lt", "i"}, {"i", "contains", "p"}, {"d1", "regex", "q"}} {
		func() {
			defer func() {
				r := recover()
				ce, ok := r.(*CompareError)
				if !ok || ce.Field != c[0] {
					t.Errorf("%v: want a *CompareError naming %s, got %v", c, c[0], r)
				}
			}()
			FieldOp(r, c[0], c[1], c[2])
			t.Errorf("%v: no panic", c)
		}()
	}
}

// LiteralOp: the operand is read in the FIELD's kind; a literal not of
// that kind is a *CompareError; "" is false; a string operator needs text.
func TestLiteralOp(t *testing.T) {
	ok := []struct {
		v       any
		op, lit string
		want    bool
	}{
		{int64(30), "gt", "29.5", true}, {int64(30), "eq", "30", true}, {int64(30), "eq", "30.0", true},
		{2.5, "lt", "3", true}, {true, "eq", "true", true}, {true, "ne", "false", true},
		{"bob", "gt", "5", true}, {"007", "eq", "007", true}, {"12", "gt", "9", false}, // text compares as text
		{"hello", "contains", "ell", true}, {"hello", "regex", "^h", true},
		{int64(1), "eq", "", false}, {2.0, "ne", "", false}, {true, "eq", "", false},
		{time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), "ge", "2026-03-01", true},
	}
	for _, c := range ok {
		got, err := LiteralOp(c.v, c.op, c.lit)
		if err != nil || got != c.want {
			t.Errorf("%v %s %q: got %v, %v; want %v", c.v, c.op, c.lit, got, err, c.want)
		}
	}
	bad := []struct {
		v       any
		op, lit string
	}{
		{int64(30), "gt", "abc"}, {int64(30), "gt", "3O"}, {2.5, "eq", "x"}, {true, "eq", "maybe"},
		{int64(30), "contains", "3"}, {2.5, "startswith", "2"}, {true, "regex", "t"},
		{time.Now(), "gt", "yesterday"},
	}
	for _, c := range bad {
		_, err := LiteralOp(c.v, c.op, c.lit)
		var ce *CompareError
		if !errors.As(err, &ce) {
			t.Errorf("%v %s %q: want *CompareError, got %v", c.v, c.op, c.lit, err)
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
