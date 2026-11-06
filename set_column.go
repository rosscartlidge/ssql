package ssql

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// IntoColumn writes value into field, typed like the column's current
// value existingValue (exists says the row has the field): update -set's
// rule for a literal, one implementation the interpreter and generated
// record code both call (record codegen ignored the column's type and
// wrote a json column's [1] as text until 2026-11-06; DFC163 §7). A field
// the row does not have takes the value's own type. It returns whether
// a coercion changed the value's type, for the interpreter's warning.
func IntoColumn(mut MutableRecord, field string, value any, existingValue any, exists bool) (MutableRecord, bool) {
	if !exists {
		// Field doesn't exist, apply with natural type
		return SetValue(mut, field, value), false
	}

	if v, isInet := value.(Inet); isInet {
		// inet(x) into a text column declares it (DFC162): the column
		// retypes, as a decimal result does — never coerced back to text.
		return mut.Inet(field, v), false
	}
	if d, isDec := value.(Decimal); isDec {
		if _, colIsDec := existingValue.(Decimal); !colIsDec {
			// A decimal result into a column of another type: the column
			// retypes (the interpreter retypes per row; DFC155 E).
			return mut.Decimal(field, d), false
		}
	}

	// Determine the target type from existing value
	var coerced bool
	// ssql:domain fixed: an existing time column takes the value's own type (the default)
	switch existingValue.(type) {
	case int64:
		if w, ok := value.(Int128); ok {
			// An int128 result into an int column: the column widens (the
			// interpreter retypes per row; nothing is wrapped). DFC155 C.
			if i, fits := w.Int64(); fits {
				return mut.Int(field, i), false
			}
			return mut.Int128(field, w), false
		}
		coercedVal, didCoerce := coerceToInt64(value)
		if didCoerce {
			coerced = true
		}
		return mut.Int(field, coercedVal), coerced

	case Int128:
		coercedVal, didCoerce := coerceToInt128(value)
		if didCoerce {
			coerced = true
		}
		return mut.Int128(field, coercedVal), coerced

	case Decimal:
		// A decimal column: a decimal of another scale retypes the column
		// (as an expression result does); anything else is brought to the
		// column's scale by the cast's rules, else kept by its own type.
		col := existingValue.(Decimal)
		if d, isDec := value.(Decimal); isDec {
			return mut.Decimal(field, d), false
		}
		if out, ok := CastValue(value, DecimalType(col.Scale())); ok {
			return mut.Decimal(field, out.(Decimal)), true
		}
		return SetValue(mut, field, value), false

	case float64:
		coercedVal, didCoerce := coerceToFloat64(value)
		if didCoerce {
			coerced = true
		}
		return mut.Float(field, coercedVal), coerced

	case bool:
		coercedVal, didCoerce := coerceToBool(value)
		if didCoerce {
			coerced = true
		}
		return mut.Bool(field, coercedVal), coerced

	case string:
		coercedVal, didCoerce := coerceToString(value)
		if didCoerce {
			coerced = true
		}
		return mut.String(field, coercedVal), coerced

	case JSONString:
		// A nested field takes a nested result; text is read as JSON, as a
		// declared json cell is, and text that is not a JSON array or object
		// is the cast's error (it was stored as text, so `-set j [1]` wrote
		// the string "[1]" where the typed lane wrote the array; DFC163 §7).
		if text, ok := value.(string); ok {
			out, ok := CastValue(text, FieldTypeJSON)
			if !ok {
				panic(&CastError{Field: field, Value: value, Target: FieldTypeJSON})
			}
			return mut.JSONString(field, out.(JSONString)), true
		}
		return SetValue(mut, field, value), false

	case Inet:
		// An inet column takes an address: an inet, or text that parses
		// as one. Anything else is the cast's error, never text silently
		// written into an address column (DFC162).
		if v, ok := value.(Inet); ok {
			return mut.Inet(field, v), false
		}
		out, ok := CastValue(value, FieldTypeInet)
		if !ok {
			panic(&CastError{Field: field, Value: value, Target: FieldTypeInet})
		}
		return mut.Inet(field, out.(Inet)), true

	default:
		// Unknown existing type, apply with natural type
		return SetValue(mut, field, value), false
	}
}

// coerceToInt128 converts a value to an Int128, returning whether coercion
// occurred: integers widen without one; a whole float, a digit string and
// a bool are coercions.
func coerceToInt128(value any) (Int128, bool) {
	// ssql:domain fixed: an int128 column's sources; the default is a type error
	switch v := value.(type) {
	case Int128:
		return v, false
	case int64:
		return Int128From(v), false
	case int:
		return Int128From(int64(v)), false
	case float64:
		if w, ok := Int128FromFloat(math.Trunc(v)); ok {
			return w, true
		}
		return Int128{}, true
	case string:
		if w, err := ParseInt128(v); err == nil {
			return w, true
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			if w, ok := Int128FromFloat(math.Trunc(f)); ok {
				return w, true
			}
		}
		return Int128{}, true
	case bool:
		if v {
			return Int128From(1), true
		}
		return Int128{}, true
	default:
		return Int128{}, true
	}
}

// coerceToInt64 converts a value to int64, returning whether coercion occurred
func coerceToInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, false
	case int:
		return int64(v), false // Same logical type
	case float64:
		return int64(v), true
	case string:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return int64(f), true
		}
		return 0, true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	default:
		return 0, true
	}
}

// coerceToFloat64 converts a value to float64, returning whether coercion occurred
func coerceToFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, false
	case float32:
		return float64(v), false // Same logical type
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
		return 0, true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	default:
		return 0, true
	}
}

// coerceToBool converts a value to bool, returning whether coercion occurred
func coerceToBool(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, false
	case int64:
		return v != 0, true
	case int:
		return v != 0, true
	case float64:
		return v != 0, true
	case string:
		lower := strings.ToLower(v)
		switch lower {
		case "true", "1", "yes", "y", "on":
			return true, true
		default:
			return false, true
		}
	default:
		return false, true
	}
}

// coerceToString converts a value to string, returning whether coercion occurred
func coerceToString(value any) (string, bool) {
	// ssql:domain fixed: every other scalar's text is its String (or %v)
	switch v := value.(type) {
	case string:
		return v, false
	case int64:
		return strconv.FormatInt(v, 10), true
	case int:
		return strconv.Itoa(v), true
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	case time.Time:
		// RFC 3339, the form the JSON writer uses for a time in a NEW
		// field and the one convertToTime, DuckDB and Postgres read back.
		// The %v default gave Go's Time.String() ("2026-01-02 10:30:00
		// +0000 UTC"), which nothing parses (DFC128 F3).
		return v.Format(time.RFC3339Nano), true
	default:
		return fmt.Sprintf("%v", v), true
	}
}

// SetLiteral is update -set's literal for generated record code, by the
// interpreter's rule: into a text column the token as typed (02134 stays
// 02134), into any other column value — the token as the CLI parses it —
// typed like the row's value by IntoColumn, and into a field the row does
// not have, value by its own type.
func SetLiteral(mut MutableRecord, frozen Record, field, token string, value any) MutableRecord {
	existing, exists := Get[any](frozen, field)
	if _, isText := existing.(string); isText {
		return mut.String(field, token)
	}
	out, _ := IntoColumn(mut, field, value, existing, exists)
	return out
}
