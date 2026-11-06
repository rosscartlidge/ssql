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
			return aggNoValue{}
		}
		sort.Float64s(vals)
		return AggResult[float64]{val: QuantileCont(vals, p)}
	}
}

// QuantileCont interpolates the p-quantile of an already SORTED slice —
// the one formula the exec and typed lanes share, and DuckDB's to the
// bit (Ross, DFC163 §7): position RN = (n−1)·p, and between its
// neighbours lo·(1−d) + hi·d. Postgres computes lo + (hi−lo)·d, which can
// differ in the last digit (3.1999999999999997 for 3.2). n must be ≥ 1.
func QuantileCont(sorted []float64, p float64) float64 {
	n := len(sorted)
	rn := float64(n-1) * p
	lo, hi := math.Floor(rn), math.Ceil(rn)
	if lo == hi {
		return sorted[int(lo)]
	}
	d := rn - lo
	return sorted[int(lo)]*(1-d) + sorted[int(hi)]*d
}

// Variance is the sample variance (n−1) of a numeric field; fewer than
// two values have none, as SQL's var_samp is NULL. See Median for the
// algorithm.
func Variance(field string) AggregateFunc {
	context := fmt.Sprintf("Variance(%q)", field)
	return func(records []Record) AggregateResult {
		var w Welford
		for _, v := range numericValues(context, records, field) {
			w.Add(v)
		}
		if v, ok := w.SampleVariance(); ok {
			return AggResult[float64]{val: v}
		}
		return aggNoValue{}
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
		if v, ok := w.SampleVariance(); ok {
			return AggResult[float64]{val: math.Sqrt(v)}
		}
		return aggNoValue{}
	}
}

// Welford is the running mean / M2 state for a single-pass variance, in
// DuckDB's arithmetic to the bit (and DataFusion's, which is the same):
// the update and the merge below are theirs, so a variance agrees with
// the engines' in the last digit (Ross, DFC163 §7). It carried Neumaier
// compensation until 2026-11-06, which no engine does. Postgres uses
// another update (Youngs–Cramer) and can differ in the last digit.
// Generated typed code embeds this type and calls Add/Merge/
// SampleVariance, so the lanes share one arithmetic.
type Welford struct {
	N    int64
	mean float64
	m2   float64
}

// Add folds one value into the state.
func (w *Welford) Add(x float64) {
	w.N++
	newMean := w.mean + (x-w.mean)/float64(w.N)
	w.m2 += (x - newMean) * (x - w.mean)
	w.mean = newMean
}

// Merge folds another state in (Chan, Golub & LeVeque, as DuckDB
// combines), for shard merges.
func (w *Welford) Merge(o Welford) {
	if o.N == 0 {
		return
	}
	if w.N == 0 {
		*w = o
		return
	}
	n := float64(w.N + o.N)
	mean := (float64(o.N)*o.mean + float64(w.N)*w.mean) / n
	delta := o.mean - w.mean
	w.m2 = o.m2 + w.m2 + delta*delta*float64(o.N)*float64(w.N)/n
	w.mean = mean
	w.N += o.N
}

// MeanValue is the running mean.
func (w Welford) MeanValue() float64 { return w.mean }

// Variance is the sample variance of the state (0 below two values; see
// SampleVariance for SQL's NULL).
func (w Welford) Variance() float64 {
	v, _ := w.SampleVariance()
	return v
}

// SampleVariance is the sample variance and true, or false below two
// values, where every SQL engine's var_samp is NULL.
func (w Welford) SampleVariance() (float64, bool) {
	if w.N < 2 {
		return 0, false
	}
	return w.m2 / float64(w.N-1), true
}

// WelfordOf folds values into a state in their order: the typed lane
// keeps a group's values and folds them once, so its answer is exec's
// one pass to the bit, however the shards split.
func WelfordOf(vals []float64) Welford {
	var w Welford
	for _, v := range vals {
		w.Add(v)
	}
	return w
}

// VariancePtr is the sample variance, or nil below two values: the typed
// lane's nullable result (SQL's NULL).
func (w Welford) VariancePtr() *float64 {
	if v, ok := w.SampleVariance(); ok {
		return &v
	}
	return nil
}

// StdDevPtr is the sample standard deviation, or nil below two values.
func (w Welford) StdDevPtr() *float64 {
	if v, ok := w.SampleVariance(); ok {
		sd := math.Sqrt(v)
		return &sd
	}
	return nil
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
			if !ok || aggMissing(v) {
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
			return aggNoValue{}
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
		if !ok || aggMissing(v) {
			continue
		}
		if !isNumeric(v) {
			panic(fmt.Errorf("%s: value %v (%T) is not a number", context, v, v))
		}
		vals = append(vals, toFloat64(v))
	}
	return vals
}
