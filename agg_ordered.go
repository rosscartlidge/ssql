package ssql

import "fmt"

// MinOf and MaxOf are the ordered-extreme aggregations the CLI's
// `group-by -min` / `-max` use: they keep the FIELD'S OWN TYPE. Numbers
// (any width) compare as float64 and come back as float64; strings
// compare lexically; times chronologically. Records where the field is
// missing are skipped. A group whose values cannot be ordered (bools,
// nested JSON) or mix kinds (a string and a number) is an error, surfaced
// as a panic the CLI turns into an `Error:` line — never a silent zero.
//
// Min[T] / Max[T] remain for callers who know the type at compile time;
// MinOf/MaxOf are for the dynamic case. Until v4.99.0 the CLI used
// Min[float64], which reported 0 for every string group while the typed
// lane refused and `generate sql` answered correctly (DFC129 §2).
//
// Example:
//
//	ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
//	    "first_alpha": ssql.MinOf("name"),   // "Alice"
//	    "latest":      ssql.MaxOf("hired"),  // a time.Time, if the field is one
//	})
func MinOf(field string) AggregateFunc { return orderedExtreme("MinOf", field, -1) }

// MaxOf is the type-preserving maximum; see MinOf.
func MaxOf(field string) AggregateFunc { return orderedExtreme("MaxOf", field, 1) }

// aggMissing is the aggregates' definition of a missing value — DFC124's,
// the one `describe` and `unpivot` already used: absent, null, or the
// empty string. CSV cannot tell an empty string from a missing one, the
// reader keeps "" so that `where -if s eq ""` stays expressible, and
// commands treat it as missing. The aggregates skipped only nil, so MIN
// over {"Oslo", ""} answered "" where SQL answers Oslo, and COUNT(DISTINCT)
// counted the empty string as a value (DFC133 random differential).
func aggMissing(v any) bool {
	if v == nil {
		return true
	}
	s, isString := v.(string)
	return isString && s == ""
}

func orderedExtreme(name, field string, sign int) AggregateFunc {
	context := fmt.Sprintf("%s(%q)", name, field)
	return func(records []Record) AggregateResult {
		var best any
		found := false
		for _, r := range records {
			v, ok := Get[any](r, field)
			if !ok || aggMissing(v) {
				continue
			}
			if !found {
				// aggCompare rejects unorderable kinds only when it sees a
				// pair; check the first value against itself so a group of
				// one bool is refused too.
				if _, err := aggCompare(name, v, v); err != nil {
					panic(fmt.Errorf("%s: %w", context, err))
				}
				best, found = v, true
				continue
			}
			c, err := aggCompare(name, v, best)
			if err != nil {
				panic(fmt.Errorf("%s: %w", context, err))
			}
			if c*sign > 0 {
				best = v
			}
		}
		if !found {
			// No value in the group: the string empty value, the one
			// zero that is visibly "nothing" in every sink.
			return aggNoValue{}
		}
		return aggResult(context, best)
	}
}
