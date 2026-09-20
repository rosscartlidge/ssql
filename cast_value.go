package ssql

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CastError is a value that cannot be converted to the type a cast asked
// for. It is an error — and is what CastField panics with — so that both
// the CLI and a generated program report it as one `Error:` line rather
// than a Go stack trace.
type CastError struct {
	Field  string
	Value  any
	Target FieldType
}

func (e *CastError) Error() string {
	hint := ""
	switch e.Target {
	case FieldTypeTime:
		hint = ` (accepted: RFC 3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02", Unix seconds; use date(value, layout) in -set-expr for another layout)`
	case FieldTypeBool:
		hint = " (accepted: true/false, 1/0, yes/no, y/n, on/off)"
	}
	return fmt.Sprintf("cast: field %q value %v is not %s%s — fix the data, or use `-invalid missing` to leave such values without a value",
		e.Field, formatCastValue(e.Value), castTypeNoun(e.Target), hint)
}

func formatCastValue(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	return fmt.Sprint(v)
}

func castTypeNoun(t FieldType) string {
	switch t {
	case FieldTypeInt:
		return "an int"
	case FieldTypeFloat:
		return "a float"
	case FieldTypeBool:
		return "a bool"
	case FieldTypeTime:
		return "a time"
	}
	return "a " + t.String()
}

// CastValue converts v to target, reporting whether it could. It is the
// ONE conversion behind `cast` in the interpreter and in generated code.
// A value that is not of the target type is a failure, never a zero:
// "abc" → int used to be 0, the behaviour DFC124 removed from the CSV
// reader because it destroys the information before anyone can see it.
//
// Conversions that are defined, not failures: a float to int truncates
// (as a Go conversion does); a bool is 1 or 0; a number is true when
// non-zero; a numeric string may be written as a float ("2.5" → int 2).
func CastValue(v any, target FieldType) (any, bool) {
	switch target {
	case FieldTypeString:
		switch x := v.(type) {
		case string:
			return x, true
		case int64:
			return strconv.FormatInt(x, 10), true
		case float64:
			return strconv.FormatFloat(x, 'g', -1, 64), true
		case bool:
			return strconv.FormatBool(x), true
		case time.Time:
			return x.Format(time.RFC3339Nano), true
		}
		return fmt.Sprintf("%v", v), true
	case FieldTypeInt:
		switch x := v.(type) {
		case int64:
			return x, true
		case float64:
			return int64(x), true
		case bool:
			if x {
				return int64(1), true
			}
			return int64(0), true
		case string:
			s := strings.TrimSpace(x)
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return i, true
			}
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return int64(f), true
			}
		}
	case FieldTypeFloat:
		switch x := v.(type) {
		case float64:
			return x, true
		case int64:
			return float64(x), true
		case bool:
			if x {
				return float64(1), true
			}
			return float64(0), true
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
				return f, true
			}
		}
	case FieldTypeBool:
		switch x := v.(type) {
		case bool:
			return x, true
		case int64:
			return x != 0, true
		case float64:
			return x != 0, true
		case string:
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "true", "1", "yes", "y", "on":
				return true, true
			case "false", "0", "no", "n", "off":
				return false, true
			}
		}
	case FieldTypeTime:
		if t, ok := ParseTime(v); ok {
			return t, true
		}
	}
	return nil, false
}

// CastField casts one field of a record being rebuilt: src is the record
// as read, mut the record under construction. A field with no value stays
// as it is; an empty text cell is MISSING (DFC124) and becomes a field
// without a value for any target but string. A value that cannot be
// converted panics with a *CastError — unless invalidMissing, in which
// case it becomes a field without a value and *invalid (when non-nil) is
// incremented, so the caller can say how many there were.
func CastField(mut MutableRecord, src Record, field string, target FieldType, invalidMissing bool, invalid *int64) MutableRecord {
	v, ok := Get[any](src, field)
	if !ok {
		return mut
	}
	if s, isString := v.(string); isString && s == "" && target != FieldTypeString {
		return mut.Null(field)
	}
	out, ok := CastValue(v, target)
	if !ok {
		if !invalidMissing {
			panic(&CastError{Field: field, Value: v, Target: target})
		}
		if invalid != nil {
			*invalid++
		}
		return mut.Null(field)
	}
	switch x := out.(type) {
	case string:
		return mut.String(field, x)
	case int64:
		return mut.Int(field, x)
	case float64:
		return mut.Float(field, x)
	case bool:
		return mut.Bool(field, x)
	case time.Time:
		return mut.Time(field, x)
	}
	return mut
}

// MustCast is CastValue for typed generated code: the converted value, or
// a *CastError panic. T is the Go type of target (int64, float64, bool,
// string, time.Time). An empty string is the typed lane's missing value
// and yields T's zero, as its readers do (DFC124 §3).
func MustCast[T any](v any, target FieldType, field string) T {
	var zero T
	if s, isString := v.(string); isString && s == "" && target != FieldTypeString {
		return zero
	}
	out, ok := CastValue(v, target)
	if !ok {
		panic(&CastError{Field: field, Value: v, Target: target})
	}
	t, ok := out.(T)
	if !ok {
		panic(fmt.Errorf("cast: field %q: %s does not produce a %T", field, target, zero))
	}
	return t
}
