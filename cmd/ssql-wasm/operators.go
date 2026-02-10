//go:build js && wasm

package main

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
)

// applyWhere filters records using the same comparison logic as the CLI.
func applyWhere(records []ssql.Record, field, op, value string) []ssql.Record {
	predicate := func(r ssql.Record) bool {
		fieldValue, ok := ssql.Get[any](r, field)
		if !ok {
			return false
		}
		return applyOperator(fieldValue, op, value)
	}
	filtered := ssql.Where(predicate)(slices.Values(records))
	return slices.Collect(filtered)
}

// applySort sorts records by a field in ascending or descending order.
func applySort(records []ssql.Record, field string, descending bool) []ssql.Record {
	sorted := slices.SortedFunc(slices.Values(records), func(a, b ssql.Record) int {
		av := ssql.GetOr[any](a, field, nil)
		bv := ssql.GetOr[any](b, field, nil)
		c := compareValues(av, bv)
		if descending {
			return -c
		}
		return c
	})
	return sorted
}

// applyGroupBy groups records by a field and applies an aggregation function.
func applyGroupBy(records []ssql.Record, groupField, aggField, aggFunc string) []ssql.Record {
	grouped := ssql.GroupByFields("_group", groupField)(slices.Values(records))

	aggFn, err := buildAggregator(aggFunc, aggField)
	if err != nil {
		return nil
	}

	aggregated := ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
		aggFunc: aggFn,
	})(grouped)

	return slices.Collect(aggregated)
}

// applyDistinct deduplicates records by a field's value.
func applyDistinct(records []ssql.Record, field string) []ssql.Record {
	keyFn := func(r ssql.Record) string {
		return fmt.Sprintf("%v", ssql.GetOr[any](r, field, nil))
	}
	result := ssql.DistinctBy(keyFn)(slices.Values(records))
	return slices.Collect(result)
}

// applyLimit returns up to n records, optionally skipping offset records first.
func applyLimit(records []ssql.Record, n, offset int) []ssql.Record {
	seq := slices.Values(records)
	if offset > 0 {
		seq = ssql.Offset[ssql.Record](offset)(seq)
	}
	if n > 0 {
		seq = ssql.Limit[ssql.Record](n)(seq)
	}
	return slices.Collect(seq)
}

// buildAggregator creates an AggregateFunc from a function name and field.
func buildAggregator(function, field string) (ssql.AggregateFunc, error) {
	switch strings.ToLower(function) {
	case "count":
		return ssql.Count(), nil
	case "sum":
		return ssql.Sum(field), nil
	case "avg":
		return ssql.Avg(field), nil
	case "min":
		return ssql.Min[float64](field), nil
	case "max":
		return ssql.Max[float64](field), nil
	default:
		return nil, fmt.Errorf("unknown aggregation function: %s", function)
	}
}

// compareValues compares two record field values, handling mixed types.
func compareValues(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	// Try numeric comparison
	af, aOk := toFloat64(a)
	bf, bOk := toFloat64(b)
	if aOk && bOk {
		return cmp.Compare(af, bf)
	}

	// Fall back to string comparison
	return cmp.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b))
}

func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int64:
		return float64(val), true
	case int:
		return float64(val), true
	default:
		return 0, false
	}
}

// applyOperator applies a comparison operator (same logic as CLI helpers.go).
func applyOperator(fieldValue any, op string, compareValue string) bool {
	switch op {
	case "eq":
		return compareEqual(fieldValue, compareValue)
	case "ne":
		return !compareEqual(fieldValue, compareValue)
	case "gt":
		return compareGreater(fieldValue, compareValue)
	case "ge":
		return compareGreater(fieldValue, compareValue) || compareEqual(fieldValue, compareValue)
	case "lt":
		return compareLess(fieldValue, compareValue)
	case "le":
		return compareLess(fieldValue, compareValue) || compareEqual(fieldValue, compareValue)
	case "contains":
		return strings.Contains(fmt.Sprintf("%v", fieldValue), compareValue)
	case "startswith":
		return strings.HasPrefix(fmt.Sprintf("%v", fieldValue), compareValue)
	case "endswith":
		return strings.HasSuffix(fmt.Sprintf("%v", fieldValue), compareValue)
	case "regex":
		re, err := regexp.Compile(compareValue)
		if err != nil {
			return false
		}
		return re.MatchString(fmt.Sprintf("%v", fieldValue))
	default:
		return false
	}
}

func compareEqual(fieldValue any, compareValue string) bool {
	switch v := fieldValue.(type) {
	case string:
		return v == compareValue
	case int64:
		if num, err := strconv.ParseInt(compareValue, 10, 64); err == nil {
			return v == num
		}
	case float64:
		if num, err := strconv.ParseFloat(compareValue, 64); err == nil {
			return v == num
		}
	case bool:
		if b, err := strconv.ParseBool(compareValue); err == nil {
			return v == b
		}
	}
	return fmt.Sprintf("%v", fieldValue) == compareValue
}

func compareGreater(fieldValue any, compareValue string) bool {
	switch v := fieldValue.(type) {
	case int64:
		if num, err := strconv.ParseInt(compareValue, 10, 64); err == nil {
			return v > num
		}
	case float64:
		if num, err := strconv.ParseFloat(compareValue, 64); err == nil {
			return v > num
		}
	case string:
		return v > compareValue
	}
	return false
}

func compareLess(fieldValue any, compareValue string) bool {
	switch v := fieldValue.(type) {
	case int64:
		if num, err := strconv.ParseInt(compareValue, 10, 64); err == nil {
			return v < num
		}
	case float64:
		if num, err := strconv.ParseFloat(compareValue, 64); err == nil {
			return v < num
		}
	case string:
		return v < compareValue
	}
	return false
}
