package ssql

import (
	"fmt"
	"iter"
	"strings"
)

// SetKeyFunc maps a record to its membership key for [Except] and
// [Intersect]. ok=false means the record has no key (a named field is
// absent): a left row without a key matches nothing, so except keeps it
// and intersect drops it (the DFC124 rule that a condition on a missing
// value is false).
type SetKeyFunc func(r Record) (key string, ok bool)

// WholeRow is the key for the whole-row forms of Except and Intersect:
// two records match when every field matches (SQL EXCEPT / INTERSECT),
// by [RecordKey]. It is the nil SetKeyFunc; a nil key on both sides
// selects the whole-row form.
var WholeRow SetKeyFunc

// setKey applies a SetKeyFunc, with nil meaning the whole row.
func setKey(f SetKeyFunc, r Record) (string, bool) {
	if f == nil {
		return RecordKey(r), true
	}
	return f(r)
}

// FieldsKey returns the SetKeyFunc that keys a record by the values of
// the named fields, in order. Numbers compare as numbers (an int 3 and a
// float 3 are the same key, as they are for join); everything else by
// its printed value; a number is never the same key as the text that
// prints like it. Any absent field means no key.
func FieldsKey(fields ...string) SetKeyFunc {
	return func(r Record) (string, bool) {
		var b strings.Builder
		for i, f := range fields {
			v, ok := Get[any](r, f)
			if !ok {
				return "", false
			}
			if i > 0 {
				b.WriteByte(0)
			}
			if n, isNum := joinNumber(v); isNum {
				fmt.Fprintf(&b, "n:%v", n)
			} else {
				fmt.Fprintf(&b, "v:%v", v)
			}
		}
		return b.String(), true
	}
}

// Except keeps the left rows whose key does not appear on the right (SQL
// EXCEPT; with a field key, an anti-join). The right side is read in
// full when the first left row arrives; the left streams in input order.
//
// all=false: distinct output, by whole row. all=true keeps duplicate
// left rows; with [WholeRow] keys that is EXCEPT ALL, where each right
// row cancels one matching left row.
func Except(right iter.Seq[Record], leftKey, rightKey SetKeyFunc, all bool) Filter[Record, Record] {
	return setOp(right, leftKey, rightKey, all, false)
}

// Intersect keeps the left rows whose key appears on the right (SQL
// INTERSECT; with a field key, a semi-join: the left row comes out
// unchanged, nothing from the right is added). The right side is read
// in full when the first left row arrives; the left streams in input
// order.
//
// all=false: distinct output, by whole row. all=true keeps duplicate
// left rows; with [WholeRow] keys that is INTERSECT ALL, where a left
// row comes out once per matching right row still unclaimed.
func Intersect(right iter.Seq[Record], leftKey, rightKey SetKeyFunc, all bool) Filter[Record, Record] {
	return setOp(right, leftKey, rightKey, all, true)
}

// setOp is the one implementation behind Except and Intersect: a count
// per right key, then a streaming pass over the left. keep says which
// side of the membership test comes out. The multiset forms (all with a
// whole-row key) consume one right count per matching left row; the
// keyed all forms only test membership, so a right key is never used up.
func setOp(right iter.Seq[Record], leftKey, rightKey SetKeyFunc, all, keep bool) Filter[Record, Record] {
	return func(left iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			counts := make(map[string]int)
			for r := range right {
				if k, ok := setKey(rightKey, r); ok {
					counts[k]++
				}
			}
			multiset := all && leftKey == nil && rightKey == nil
			var seen map[string]struct{}
			if !all {
				seen = make(map[string]struct{})
			}
			for r := range left {
				k, ok := setKey(leftKey, r)
				present := ok && counts[k] > 0
				if present && multiset {
					counts[k]--
				}
				if present != keep {
					continue
				}
				if !all {
					rk := RecordKey(r)
					if _, dup := seen[rk]; dup {
						continue
					}
					seen[rk] = struct{}{}
				}
				if !yield(r) {
					return
				}
			}
		}
	}
}
