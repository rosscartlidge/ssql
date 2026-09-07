package ssql

import (
	"fmt"
	"iter"
	"math"
	"strconv"
)

// CoerceFieldTypes converts the named fields of every record to the
// given types — the `-type FIELD TYPE` override for inputs that carry
// no column typing of their own (JSONL, where each line types itself
// and a column may arrive as int on one line and float on the next).
//
// Conversions are STRICT, as the CSV reader's are: int64 → float64 and
// a whole float64 → int64 are exact; a string parses with the CSV cell
// rules (an empty string is a missing value: the field becomes
// absent); anything → string formats the value; a fraction into int, a
// number into bool, a bool into a number, or an unparsable string is a
// *CellError, and this unsafe form PANICS with it (Row = 1-based record
// index, Sampled = 0 for "explicit type"). A field absent from a
// record stays absent; FieldTypeAuto leaves a field untouched.
func CoerceFieldTypes(records iter.Seq[Record], types map[string]FieldType) iter.Seq[Record] {
	if len(types) == 0 {
		return records
	}
	return func(yield func(Record) bool) {
		var row int64
		for rec := range records {
			row++
			mut := rec.ToMutable()
			for field, ft := range types {
				if ft == FieldTypeAuto {
					continue
				}
				v, ok := Get[any](rec, field)
				if !ok {
					continue
				}
				cv, err := coerceValue(v, ft)
				if err != nil {
					panic(&CellError{Row: row, Column: field, Value: fmt.Sprint(v), Type: ft})
				}
				mut = setAny(mut, field, cv)
			}
			if !yield(mut.Freeze()) {
				return
			}
		}
	}
}

// coerceValue converts one value; nil means "absent".
func coerceValue(v any, ft FieldType) (any, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		return parserForType(ft)(s)
	}
	switch ft {
	case FieldTypeString:
		switch x := v.(type) {
		case int64:
			return strconv.FormatInt(x, 10), nil
		case float64:
			return strconv.FormatFloat(x, 'g', -1, 64), nil
		case bool:
			return strconv.FormatBool(x), nil
		}
		return fmt.Sprint(v), nil
	case FieldTypeInt:
		switch x := v.(type) {
		case int64:
			return x, nil
		case float64:
			if x == math.Trunc(x) && !math.IsInf(x, 0) {
				return int64(x), nil
			}
		}
	case FieldTypeFloat:
		switch x := v.(type) {
		case float64:
			return x, nil
		case int64:
			return float64(x), nil
		}
	case FieldTypeBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	}
	return nil, errCellType
}

// setAny stores a parsed cell value (string/int64/float64/bool, or nil
// for absent) into a field.
func setAny(m MutableRecord, field string, v any) MutableRecord {
	switch x := v.(type) {
	case nil:
		return m.Delete(field)
	case string:
		return m.String(field, x)
	case int64:
		return m.Int(field, x)
	case float64:
		return m.Float(field, x)
	case bool:
		return m.Bool(field, x)
	}
	return m
}
