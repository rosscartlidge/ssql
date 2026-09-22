package ssql

import (
	"regexp"
	"strings"
	"time"
)

// Field-to-field operations (DFC135): the structured flags `-if-field
// FIELD OP FIELD` and `-set-field FIELD SOURCE`. One implementation for the
// interpreter and for generated record-mode code, so the two cannot drift.

// FieldOp evaluates `left OP right` where both operands are fields of r.
// The operators are those of `where -if`: eq ne gt ge lt le contains
// startswith endswith regex. Either side absent makes the condition false
// (DFC124: a condition on a missing value is false; its negation true).
//
// Operand types follow the runtime values: two numbers compare as numbers
// (int against float through float64), two strings lexically, two times
// as instants, two bools by equality. Mixed kinds are false, never a
// string comparison of their renderings (the `top`-by-string lesson).
// The string operators need two strings; the right one is the pattern.
func FieldOp(r Record, left, op, right string) bool {
	a, ok := Get[any](r, left)
	if !ok {
		return false
	}
	b, ok := Get[any](r, right)
	if !ok {
		return false
	}
	return ValueOp(a, op, b)
}

// ValueOp is FieldOp on two values already read from a record.
func ValueOp(a any, op string, b any) bool {
	switch op {
	case "eq":
		c, ok := compareTyped(a, b)
		return ok && c == 0
	case "ne":
		c, ok := compareTyped(a, b)
		return ok && c != 0
	case "gt":
		c, ok := compareTyped(a, b)
		return ok && c > 0
	case "ge":
		c, ok := compareTyped(a, b)
		return ok && c >= 0
	case "lt":
		c, ok := compareTyped(a, b)
		return ok && c < 0
	case "le":
		c, ok := compareTyped(a, b)
		return ok && c <= 0
	case "contains", "startswith", "endswith", "regex":
		s, ok1 := a.(string)
		p, ok2 := b.(string)
		if !ok1 || !ok2 {
			return false
		}
		switch op {
		case "contains":
			return strings.Contains(s, p)
		case "startswith":
			return strings.HasPrefix(s, p)
		case "endswith":
			return strings.HasSuffix(s, p)
		}
		re, err := regexp.Compile(p)
		return err == nil && re.MatchString(s)
	}
	return false
}

// compareTyped orders two values of the same kind; ok=false for mixed
// kinds (or kinds with no order).
func compareTyped(a, b any) (int, bool) {
	if af, ok := toFloat(a); ok {
		bf, ok := toFloat(b)
		if !ok {
			return 0, false
		}
		// Two ints compare exactly; a float on either side compares as
		// float64, as every other lane does.
		if ai, aIsInt := a.(int64); aIsInt {
			if bi, bIsInt := b.(int64); bIsInt {
				return cmpOrdered(ai, bi), true
			}
		}
		return cmpOrdered(af, bf), true
	}
	switch av := a.(type) {
	case string:
		if bv, ok := b.(string); ok {
			return cmpOrdered(av, bv), true
		}
	case time.Time:
		if bv, ok := b.(time.Time); ok {
			switch {
			case av.Before(bv):
				return -1, true
			case av.After(bv):
				return 1, true
			}
			return 0, true
		}
	case bool:
		if bv, ok := b.(bool); ok {
			if av == bv {
				return 0, true
			}
			if !av {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, false
}

func cmpOrdered[T int64 | float64 | string](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// CopyField sets target to source's value and type on m, read from src (the
// record before this update's changes). An absent source leaves the target
// absent: it is removed if it existed, so that after `-set-field t s` the
// target has exactly the source's presence (DFC124 keeps absence absent).
func CopyField(m MutableRecord, src Record, target, source string) MutableRecord {
	v, ok := Get[any](src, source)
	if !ok {
		return m.Delete(target)
	}
	m.fields[target] = v
	return m
}
