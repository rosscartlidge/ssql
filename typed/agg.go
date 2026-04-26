package typed

import "iter"

// Aggregator accumulates a value across a stream of input rows and
// returns a final result on close. It's the building block for GroupBy
// and the standalone aggregate helpers (Sum, Count, Min, Max, Avg).
//
// Implementations should be cheap to Add(); New() is called once per
// group, so allocation in New() multiplies by the number of distinct
// keys but allocation in Add() multiplies by the number of input rows.
type Aggregator[T, R any] interface {
	Add(T)
	Result() R
}

// AggFunc constructs a fresh aggregator. GroupBy calls it once per
// distinct key to build that group's accumulator.
type AggFunc[T, R any] func() Aggregator[T, R]

// ---- standalone aggregations ----

// Count returns the number of items in seq.
func Count[T any](seq iter.Seq[T]) int64 {
	var n int64
	for range seq {
		n++
	}
	return n
}

// Sum returns the sum of fn(x) over seq.
func Sum[T any, N Number](seq iter.Seq[T], fn func(T) N) N {
	var s N
	for v := range seq {
		s += fn(v)
	}
	return s
}

// Min returns the minimum of fn(x) over seq, plus a flag indicating
// whether the sequence was non-empty.
func Min[T any, N Ordered](seq iter.Seq[T], fn func(T) N) (N, bool) {
	var min N
	first := true
	for v := range seq {
		x := fn(v)
		if first || x < min {
			min = x
			first = false
		}
	}
	return min, !first
}

// Max returns the maximum of fn(x) over seq, plus a flag indicating
// whether the sequence was non-empty.
func Max[T any, N Ordered](seq iter.Seq[T], fn func(T) N) (N, bool) {
	var max N
	first := true
	for v := range seq {
		x := fn(v)
		if first || x > max {
			max = x
			first = false
		}
	}
	return max, !first
}

// Avg returns the mean of fn(x) over seq, plus the count. Returns
// (0, 0) on an empty sequence.
func Avg[T any, N Number](seq iter.Seq[T], fn func(T) N) (float64, int64) {
	var sum N
	var n int64
	for v := range seq {
		sum += fn(v)
		n++
	}
	if n == 0 {
		return 0, 0
	}
	return float64(sum) / float64(n), n
}

// ---- numeric type constraints ----

// Number is the constraint for fields that can be summed or averaged.
type Number interface {
	~int | ~int32 | ~int64 | ~uint64 | ~float32 | ~float64
}

// Ordered is the constraint for fields that can be min/max'd.
type Ordered interface {
	Number | ~string
}

// ---- group-by ----

// GroupBy collapses an input stream into one output row per distinct
// key. The build function constructs the output row from a key and the
// (already-finalized) per-group state.
//
// All groups are buffered in memory until the input is fully consumed,
// because GroupBy makes no assumptions about input ordering. For
// pre-sorted input, [GroupByOrdered] is O(1) memory.
//
// Example: count rows per dept_id.
//
//	type Result struct{ DeptID string; N int64 }
//	out := typed.GroupBy(rows,
//	    func(r Order) string { return r.DeptID },
//	    func() typed.Aggregator[Order, int64] { return &typed.Counter[Order]{} },
//	    func(k string, n int64) Result { return Result{DeptID: k, N: n} },
//	)
func GroupBy[T, S, O any, K comparable](
	seq iter.Seq[T],
	keyFn func(T) K,
	newAgg AggFunc[T, S],
	build func(K, S) O,
) iter.Seq[O] {
	return func(yield func(O) bool) {
		groups := make(map[K]Aggregator[T, S])
		// Preserve insertion order so output is deterministic for a
		// deterministic input — critical for testing and for users who
		// expect "first key seen, first key emitted".
		var keys []K
		for v := range seq {
			k := keyFn(v)
			agg, ok := groups[k]
			if !ok {
				agg = newAgg()
				groups[k] = agg
				keys = append(keys, k)
			}
			agg.Add(v)
		}
		for _, k := range keys {
			if !yield(build(k, groups[k].Result())) {
				return
			}
		}
	}
}

// GroupByOrdered streams a pre-sorted input into one output per group
// in O(1) memory. The input MUST be ordered such that all rows with
// the same key are contiguous; otherwise the result is undefined.
//
// Use this when the input is the output of a sort, or comes from a
// naturally-grouped source (e.g. partitioned files).
func GroupByOrdered[T, S, O any, K comparable](
	seq iter.Seq[T],
	keyFn func(T) K,
	newAgg AggFunc[T, S],
	build func(K, S) O,
) iter.Seq[O] {
	return func(yield func(O) bool) {
		var (
			haveKey  bool
			lastKey  K
			agg      Aggregator[T, S]
		)
		for v := range seq {
			k := keyFn(v)
			if !haveKey {
				lastKey = k
				agg = newAgg()
				haveKey = true
			} else if k != lastKey {
				if !yield(build(lastKey, agg.Result())) {
					return
				}
				lastKey = k
				agg = newAgg()
			}
			agg.Add(v)
		}
		if haveKey {
			yield(build(lastKey, agg.Result()))
		}
	}
}

// ---- prebuilt aggregators ----

// Counter counts inputs.
type Counter[T any] struct{ N int64 }

func (c *Counter[T]) Add(T)          { c.N++ }
func (c *Counter[T]) Result() int64  { return c.N }

// Summer accumulates fn(x) over inputs.
type Summer[T any, N Number] struct {
	fn  func(T) N
	sum N
}

func NewSummer[T any, N Number](fn func(T) N) AggFunc[T, N] {
	return func() Aggregator[T, N] { return &Summer[T, N]{fn: fn} }
}

func (s *Summer[T, N]) Add(v T)  { s.sum += s.fn(v) }
func (s *Summer[T, N]) Result() N { return s.sum }

// Averager accumulates a running mean.
type Averager[T any, N Number] struct {
	fn  func(T) N
	sum N
	n   int64
}

func NewAverager[T any, N Number](fn func(T) N) AggFunc[T, float64] {
	return func() Aggregator[T, float64] { return &Averager[T, N]{fn: fn} }
}

func (a *Averager[T, N]) Add(v T) {
	a.sum += a.fn(v)
	a.n++
}

func (a *Averager[T, N]) Result() float64 {
	if a.n == 0 {
		return 0
	}
	return float64(a.sum) / float64(a.n)
}
