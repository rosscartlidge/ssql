package ssql

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FirstOf, LastOf, CountDistinct and StringAgg are the group-by
// aggregates behind the CLI's -first / -last / -any / -count-distinct /
// -string-agg flags (DFC129 phase 1). Like MinOf/MaxOf they are
// dynamic — the value keeps the field's own type — and skip records
// where the field is missing.
//
// FirstOf returns the first present value in arrival order; LastOf the
// last. On a file source that is file order, and the typed parallel lane
// merges shards in file order, so every lane agrees; on an unordered
// source the answer is whichever value arrived first, which is what
// SQL's any_value promises and what the CLI's -any documents.
//
// Example:
//
//	ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
//	    "opened":  ssql.FirstOf("ts"),
//	    "closed":  ssql.LastOf("ts"),
//	    "cities":  ssql.CountDistinct("city"),
//	    "names":   ssql.StringAgg("name", ", "),
//	})
func FirstOf(field string) AggregateFunc {
	context := fmt.Sprintf("FirstOf(%q)", field)
	return func(records []Record) AggregateResult {
		for _, r := range records {
			if v, ok := Get[any](r, field); ok && v != nil {
				return aggResult(context, v)
			}
		}
		return AggResult[string]{val: ""}
	}
}

// LastOf is the last present value in arrival order; see FirstOf.
func LastOf(field string) AggregateFunc {
	context := fmt.Sprintf("LastOf(%q)", field)
	return func(records []Record) AggregateResult {
		for i := len(records) - 1; i >= 0; i-- {
			if v, ok := Get[any](records[i], field); ok && v != nil {
				return aggResult(context, v)
			}
		}
		return AggResult[string]{val: ""}
	}
}

// CountDistinct counts the distinct present values of field (SQL's
// COUNT(DISTINCT f)). Scalars are compared by value; nested JSON by its
// text.
func CountDistinct(field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		seen := make(map[any]struct{})
		for _, r := range records {
			v, ok := Get[any](r, field)
			if !ok || v == nil {
				continue
			}
			seen[distinctKey(v)] = struct{}{}
		}
		return AggResult[int64]{val: int64(len(seen))}
	}
}

// distinctKey makes a value usable as a map key: comparable scalars as
// themselves (numbers widened to float64 so 3 and 3.0 are one value),
// everything else by its formatted text.
func distinctKey(v any) any {
	switch x := v.(type) {
	case string, bool:
		return x
	case time.Time:
		return x.UnixNano()
	}
	if isNumeric(v) {
		return toFloat64(v)
	}
	return fmt.Sprint(v)
}

// StringAgg joins the present values of field, in arrival order, with
// sep (SQL's string_agg). Non-string values are formatted the way the
// typed lane formats them — AggValueString — so every lane produces the
// same text.
func StringAgg(field, sep string) AggregateFunc {
	return func(records []Record) AggregateResult {
		var parts []string
		for _, r := range records {
			if v, ok := Get[any](r, field); ok && v != nil {
				parts = append(parts, AggValueString(v))
			}
		}
		return AggResult[string]{val: strings.Join(parts, sep)}
	}
}

// AggValueString is the text form StringAgg uses for a value: integers
// in full, floats in Go's shortest round-trip form, times as RFC 3339.
// Generated typed code emits the same formatting inline (typed_groupby.go).
func AggValueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return fmt.Sprint(v)
}
