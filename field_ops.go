package ssql

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
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
// as instants, two bools by equality. Mixed kinds cannot be compared: that
// is a *CompareError (panicked, for the same recover that turns every
// generated-program failure into one Error line), never false and never a
// comparison of their renderings (the `top`-by-string lesson). The string
// operators need two strings; the right one is the pattern.
func FieldOp(r Record, left, op, right string) bool {
	a, ok := Get[any](r, left)
	if !ok {
		return false
	}
	b, ok := Get[any](r, right)
	if !ok {
		return false
	}
	res, err := FieldOpValues(a, op, b)
	if err != nil {
		var ce *CompareError
		if errors.As(err, &ce) {
			ce.Field, ce.Value = left, right
		}
		panic(err)
	}
	return res
}

// FieldOpValues is FieldOp on two values already read from a record,
// reporting a mixed-kind pairing as a *CompareError (Field and Value are
// left for the caller to fill with the two names).
func FieldOpValues(a any, op string, b any) (bool, error) {
	if isStringOp(op) {
		_, aText := a.(string)
		_, bText := b.(string)
		if !aText || !bText {
			kind := kindName(a)
			if aText {
				kind = kindName(b)
			}
			return false, &CompareError{Op: op, Kind: kind, Fields: true}
		}
		return ValueOp(a, op, b), nil
	}
	if !sameKind(a, b) {
		return false, &CompareError{Op: op, Kind: kindName(a) + " against " + kindName(b), Fields: true}
	}
	return ValueOp(a, op, b), nil
}

// sameKind: both numeric, or both of one other kind.
func sameKind(a, b any) bool {
	if _, ok := toFloat(a); ok {
		_, ok := toFloat(b)
		return ok
	}
	switch a.(type) {
	case string:
		_, ok := b.(string)
		return ok
	case bool:
		_, ok := b.(bool)
		return ok
	case time.Time:
		_, ok := b.(time.Time)
		return ok
	}
	return false
}

func kindName(v any) string {
	switch v.(type) {
	case int64, int, float64:
		return "number"
	case string:
		return "text"
	case bool:
		return "bool"
	case time.Time:
		return "time"
	}
	return fmt.Sprintf("%T", v)
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

// CompareError: a `-if FIELD OP VALUE` literal that is not of the field's
// kind (`age gt abc` on a numeric age, `active eq maybe` on a bool, a
// string operator on a number). The comparison cannot be made; it is an
// error, not false (exec until v4.106.0) and not a comparison of the
// number's text (record codegen guessed, `top` once did). The one literal
// every kind accepts is "" (empty), which is false: no value equals "no
// value". A time field's operand goes through ParseTime.
type CompareError struct {
	Field  string // the field, when known; "" from ValueOp-style callers
	Op     string
	Value  string // the literal, or for Fields the right-hand field
	Kind   string // the field's kind: int, float, bool, time; for Fields "KIND against KIND"
	Fields bool   // -if-field: two fields of different kinds
}

func (e *CompareError) Error() string {
	where := "the field"
	if e.Field != "" {
		where = "field " + strconv.Quote(e.Field)
	}
	if e.Fields {
		if isStringOp(e.Op) {
			return fmt.Sprintf("where: operator %s needs two text fields, and one of %s and %s is a %s", e.Op, strconv.Quote(e.Field), strconv.Quote(e.Value), e.Kind)
		}
		return fmt.Sprintf("where: fields %s and %s cannot be compared (%s)", strconv.Quote(e.Field), strconv.Quote(e.Value), e.Kind)
	}
	if isStringOp(e.Op) {
		kind := e.Kind
		if kind == "int" || kind == "float" {
			kind = "number"
		}
		return fmt.Sprintf("where: operator %s needs a text field, and %s is a %s", e.Op, where, kind)
	}
	switch e.Kind {
	case "int", "float":
		return fmt.Sprintf("where: %s is a number but %q is not (operator %s)", where, e.Value, e.Op)
	case "bool":
		return fmt.Sprintf("where: %s is a bool but %q is not true or false (operator %s)", where, e.Value, e.Op)
	case "time":
		return fmt.Sprintf("where: %s is a time but %q is not (use a form like 2026-01-31 or 2026-01-31T10:30:00Z)", where, e.Value)
	}
	return fmt.Sprintf("where: %s is a %s but %q is not", where, e.Kind, e.Value)
}

// LiteralOp evaluates `v OP literal` for a field's value and a literal
// from the command line, with the operand read in the FIELD's kind: a
// number (int against a fractional literal compares as float64), a bool,
// a time, or text. A literal not of that kind is a *CompareError. The
// string operators need a text field. An empty literal against a
// non-text field is false.
func LiteralOp(v any, op, literal string) (bool, error) {
	switch x := v.(type) {
	case string:
		return ValueOp(x, op, literal), nil
	case int64:
		if literal == "" {
			return false, nil
		}
		if isStringOp(op) {
			return false, &CompareError{Op: op, Value: literal, Kind: "int"}
		}
		if n, err := strconv.ParseInt(literal, 10, 64); err == nil {
			return ValueOp(x, op, n), nil
		}
		f, err := strconv.ParseFloat(literal, 64)
		if err != nil {
			return false, &CompareError{Op: op, Value: literal, Kind: "int"}
		}
		return ValueOp(float64(x), op, f), nil
	case float64:
		if literal == "" {
			return false, nil
		}
		if isStringOp(op) {
			return false, &CompareError{Op: op, Value: literal, Kind: "float"}
		}
		f, err := strconv.ParseFloat(literal, 64)
		if err != nil {
			return false, &CompareError{Op: op, Value: literal, Kind: "float"}
		}
		return ValueOp(x, op, f), nil
	case bool:
		if literal == "" {
			return false, nil
		}
		if isStringOp(op) {
			return false, &CompareError{Op: op, Value: literal, Kind: "bool"}
		}
		b, err := strconv.ParseBool(literal)
		if err != nil {
			return false, &CompareError{Op: op, Value: literal, Kind: "bool"}
		}
		return ValueOp(x, op, b), nil
	case time.Time:
		if literal == "" {
			return false, nil
		}
		if isStringOp(op) {
			return false, &CompareError{Op: op, Value: literal, Kind: "time"}
		}
		t, ok := ParseTime(literal)
		if !ok {
			return false, &CompareError{Op: op, Value: literal, Kind: "time"}
		}
		return ValueOp(x, op, t), nil
	}
	// Other kinds (nested records, sequences) compare by their rendering,
	// as they always have.
	return ValueOp(fmt.Sprint(v), op, literal), nil
}

func isStringOp(op string) bool {
	switch op {
	case "contains", "startswith", "endswith", "regex":
		return true
	}
	return false
}

// CompareLiteral is LiteralOp on a field of r, for generated record code
// whose column types were not known at generation time: it panics with the
// *CompareError (the program's recover turns it into one Error line), and
// an absent field is false.
func CompareLiteral(r Record, field, op, literal string) bool {
	v, ok := Get[any](r, field)
	if !ok {
		return false
	}
	res, err := LiteralOp(v, op, literal)
	if err != nil {
		var ce *CompareError
		if errors.As(err, &ce) {
			ce.Field = field
		}
		panic(err)
	}
	return res
}

// MustNumber parses a runtime flag's value for a numeric comparison in
// generated code; a non-number panics with a *CompareError. desc is
// "FIELD OP" for the message.
func MustNumber(literal, field, op string) float64 {
	f, err := strconv.ParseFloat(literal, 64)
	if err != nil {
		panic(&CompareError{Field: field, Op: op, Value: literal, Kind: "float"})
	}
	return f
}

// MustBool is MustNumber for a bool comparison.
func MustBool(literal, field, op string) bool {
	b, err := strconv.ParseBool(literal)
	if err != nil {
		panic(&CompareError{Field: field, Op: op, Value: literal, Kind: "bool"})
	}
	return b
}
