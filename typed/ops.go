package typed

import "iter"

// Where filters in place. T -> T.
//
// Pure generic — no reflection ever; the compiler inlines the predicate
// when it can.
func Where[T any](pred func(T) bool) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			for v := range in {
				if pred(v) && !yield(v) {
					return
				}
			}
		}
	}
}

// Limit takes the first n items. T -> T.
func Limit[T any](n int) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if n <= 0 {
				return
			}
			i := 0
			for v := range in {
				if !yield(v) {
					return
				}
				i++
				if i >= n {
					return
				}
			}
		}
	}
}

// Skip drops the first n items. T -> T.
func Skip[T any](n int) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			i := 0
			for v := range in {
				if i < n {
					i++
					continue
				}
				if !yield(v) {
					return
				}
			}
		}
	}
}

// Select projects each input row to a different output type. Use this
// for include/exclude/rename equivalents — generated code emits the
// correct constructor.
func Select[T, U any](fn func(T) U) func(iter.Seq[T]) iter.Seq[U] {
	return func(in iter.Seq[T]) iter.Seq[U] {
		return func(yield func(U) bool) {
			for v := range in {
				if !yield(fn(v)) {
					return
				}
			}
		}
	}
}

// HashJoin builds a hash index over right and emits merged rows for each
// matching left. Right is materialized in memory (build phase); left
// streams (probe phase). Same shape as ssql.InnerJoin but type-safe.
//
// For multi-column joins, use a tuple type (e.g. struct or array) as K
// and have the key functions return the composite key.
func HashJoin[L, R, O any, K comparable](
	left iter.Seq[L],
	right iter.Seq[R],
	leftKey func(L) K,
	rightKey func(R) K,
	merge func(L, R) O,
) iter.Seq[O] {
	idx := make(map[K]R)
	for r := range right {
		idx[rightKey(r)] = r
	}
	return func(yield func(O) bool) {
		for l := range left {
			if r, ok := idx[leftKey(l)]; ok {
				if !yield(merge(l, r)) {
					return
				}
			}
		}
	}
}
