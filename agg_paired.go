package ssql

import "fmt"

// ArgMax and ArgMin are the paired-extreme aggregates behind the CLI's
// `group-by -arg-max FIELD BY RESULT` / `-arg-min` (DFC129 phase 3): the
// value of FIELD from the record where BY is largest (smallest). BY
// orders like MinOf/MaxOf — numbers, strings, times — and the carried
// FIELD keeps its own type. Records missing either field are skipped;
// ties go to the record that arrived first (DuckDB's arg_max on a
// single-threaded scan does the same). An unorderable or mixed BY is an
// error, surfaced as a panic the CLI turns into an `Error:` line.
//
// Example:
//
//	ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
//	    "top_earner": ssql.ArgMax("name", "salary"), // "Carol"
//	    "peak_at":    ssql.ArgMax("ts", "value"),
//	})
func ArgMax(field, by string) AggregateFunc { return pairedExtreme("ArgMax", field, by, 1) }

// ArgMin is FIELD at the smallest BY; see ArgMax.
func ArgMin(field, by string) AggregateFunc { return pairedExtreme("ArgMin", field, by, -1) }

func pairedExtreme(name, field, by string, sign int) AggregateFunc {
	context := fmt.Sprintf("%s(%q, %q)", name, field, by)
	return func(records []Record) AggregateResult {
		var bestBy, carried any
		found := false
		for _, r := range records {
			b, ok := Get[any](r, by)
			if !ok || aggMissing(b) {
				continue
			}
			v, ok := Get[any](r, field)
			if !ok || aggMissing(v) {
				continue
			}
			if !found {
				if _, err := aggCompare(name, b, b); err != nil {
					panic(fmt.Errorf("%s: %w", context, err))
				}
				bestBy, carried, found = b, v, true
				continue
			}
			c, err := aggCompare(name, b, bestBy)
			if err != nil {
				panic(fmt.Errorf("%s: %w", context, err))
			}
			if c*sign > 0 { // strictly better: ties keep the first arrival
				bestBy, carried = b, v
			}
		}
		if !found {
			return aggNoValue{}
		}
		return aggResult(context, carried)
	}
}
