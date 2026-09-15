package ssql

import (
	"fmt"
	"math"
	"sort"
)

// Statistics aggregates behind the CLI's -median / -percentile / -stddev /
// -variance / -mode flags (DFC129 phase 2). All skip records where the
// field is missing.
//
// Median and Percentile are the continuous (interpolating) quantile —
// DuckDB's quantile_cont / median, Postgres's percentile_cont: sort the
// group's values, take position p·(n−1) and interpolate linearly between
// its neighbours. StdDev and Variance are the SAMPLE statistics (n−1),
// like every SQL engine's stddev/variance, computed with Welford's
// single-pass update so the exec, record and serial typed lanes produce
// the same bits; the parallel typed lane merges shard states with the
// Chan et al. formula. Mode is the most frequent value, ties broken by
// FIRST arrival (what DuckDB's mode does on a single-threaded scan), so
// every lane agrees and the answer does not depend on how shards split.
//
// Example:
//
//	ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
//	    "median_salary": ssql.Median("salary"),
//	    "p90":           ssql.Percentile("salary", 0.9),
//	    "sd":            ssql.StdDev("salary"),
//	    "top_city":      ssql.Mode("city"),
//	})
func Median(field string) AggregateFunc { return Percentile(field, 0.5) }

// Percentile is the continuous p-quantile (0 ≤ p ≤ 1) of a numeric field;
// see Median. A non-numeric value in the group is an error.
func Percentile(field string, p float64) AggregateFunc {
	context := fmt.Sprintf("Percentile(%q, %v)", field, p)
	if p < 0 || p > 1 || math.IsNaN(p) {
		panic(fmt.Errorf("%s: p must be between 0 and 1", context))
	}
	return func(records []Record) AggregateResult {
		vals := numericValues(context, records, field)
		if len(vals) == 0 {
			return AggResult[string]{val: ""}
		}
		sort.Float64s(vals)
		return AggResult[float64]{val: QuantileCont(vals, p)}
	}
}

// QuantileCont interpolates the p-quantile of an already SORTED slice —
// the one formula the exec and typed lanes share. n must be ≥ 1.
func QuantileCont(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 1 {
		return sorted[0]
	}
	idx := p * float64(n-1)
	lo := int(math.Floor(idx))
	hi := lo + 1
	if hi >= n {
		return sorted[n-1]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(idx-float64(lo))
}

// Variance is the sample variance (n−1) of a numeric field; one value →
// 0, none → the empty string. See Median for the algorithm.
func Variance(field string) AggregateFunc {
	context := fmt.Sprintf("Variance(%q)", field)
	return func(records []Record) AggregateResult {
		var w Welford
		for _, v := range numericValues(context, records, field) {
			w.Add(v)
		}
		if w.N == 0 {
			return AggResult[string]{val: ""}
		}
		return AggResult[float64]{val: w.Variance()}
	}
}

// StdDev is the sample standard deviation; see Variance.
func StdDev(field string) AggregateFunc {
	context := fmt.Sprintf("StdDev(%q)", field)
	return func(records []Record) AggregateResult {
		var w Welford
		for _, v := range numericValues(context, records, field) {
			w.Add(v)
		}
		if w.N == 0 {
			return AggResult[string]{val: ""}
		}
		return AggResult[float64]{val: math.Sqrt(w.Variance())}
	}
}

// Welford is the running mean / M2 state for a single-pass variance.
// Both running quantities are CompensatedSum accumulators (Neumaier), so
// the mean update and the M2 update do not drift over long groups.
// Generated typed code embeds this type and calls Add/Merge/Variance, so
// the lanes share one arithmetic.
type Welford struct {
	N    int64
	mean CompensatedSum
	m2   CompensatedSum
}

// Add folds one value into the state.
func (w *Welford) Add(x float64) {
	w.N++
	delta := x - w.mean.Value()
	w.mean.Add(delta / float64(w.N))
	w.m2.Add(delta * (x - w.mean.Value()))
}

// Merge folds another state in (Chan, Golub & LeVeque), for shard merges.
func (w *Welford) Merge(o Welford) {
	if o.N == 0 {
		return
	}
	if w.N == 0 {
		*w = o
		return
	}
	n := float64(w.N + o.N)
	delta := o.mean.Value() - w.mean.Value()
	w.mean.Add(delta * float64(o.N) / n)
	w.m2.Merge(o.m2)
	w.m2.Add(delta * delta * float64(w.N) * float64(o.N) / n)
	w.N += o.N
}

// MeanValue is the running mean.
func (w Welford) MeanValue() float64 { return w.mean.Value() }

// Variance is the sample variance of the state (0 for a single value).
func (w Welford) Variance() float64 {
	if w.N < 2 {
		return 0
	}
	return w.m2.Value() / float64(w.N-1)
}

// Mode is the most frequent present value of field, keeping its type;
// ties go to the value that arrived first.
func Mode(field string) AggregateFunc {
	context := fmt.Sprintf("Mode(%q)", field)
	return func(records []Record) AggregateResult {
		type entry struct {
			value any
			n     int64
			first int64
		}
		counts := make(map[any]*entry)
		var order int64
		for _, r := range records {
			v, ok := Get[any](r, field)
			if !ok || v == nil {
				continue
			}
			k := distinctKey(v)
			if e, seen := counts[k]; seen {
				e.n++
			} else {
				counts[k] = &entry{value: v, n: 1, first: order}
			}
			order++
		}
		var best *entry
		for _, e := range counts {
			if best == nil || e.n > best.n || (e.n == best.n && e.first < best.first) {
				best = e
			}
		}
		if best == nil {
			return AggResult[string]{val: ""}
		}
		return aggResult(context, best.value)
	}
}

// numericValues extracts the present numeric values of field as float64,
// in arrival order; a present non-numeric value is loud.
func numericValues(context string, records []Record, field string) []float64 {
	var vals []float64
	for _, r := range records {
		v, ok := Get[any](r, field)
		if !ok || v == nil {
			continue
		}
		if !isNumeric(v) {
			panic(fmt.Errorf("%s: value %v (%T) is not a number", context, v, v))
		}
		vals = append(vals, toFloat64(v))
	}
	return vals
}
