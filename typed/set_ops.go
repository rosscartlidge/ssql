package typed

import "iter"

// Except keeps the left rows whose key is absent from the right (SQL
// EXCEPT with identity keys; an anti-join with field keys). The right
// side is read in full when the first left row arrives; the left
// streams in input order. all=false yields each distinct left row once
// (L is compared as a value, so the row type must be comparable, which
// every generated row type is); all=true keeps duplicate left rows.
//
// For EXCEPT ALL, the multiset form where each right row cancels one
// matching left row, use [ExceptAll].
func Except[L comparable, R any, K comparable](right iter.Seq[R], leftKey func(L) K, rightKey func(R) K, all bool) func(iter.Seq[L]) iter.Seq[L] {
	return setOp(right, leftKey, rightKey, all, false)
}

// Intersect keeps the left rows whose key is present on the right (SQL
// INTERSECT with identity keys; a semi-join with field keys: the left
// row comes out unchanged). all=false yields each distinct left row
// once; all=true keeps duplicate left rows.
//
// For INTERSECT ALL, the multiset form where a left row comes out once
// per unclaimed matching right row, use [IntersectAll].
func Intersect[L comparable, R any, K comparable](right iter.Seq[R], leftKey func(L) K, rightKey func(R) K, all bool) func(iter.Seq[L]) iter.Seq[L] {
	return setOp(right, leftKey, rightKey, all, true)
}

func setOp[L comparable, R any, K comparable](right iter.Seq[R], leftKey func(L) K, rightKey func(R) K, all, keep bool) func(iter.Seq[L]) iter.Seq[L] {
	return func(left iter.Seq[L]) iter.Seq[L] {
		return func(yield func(L) bool) {
			set := make(map[K]struct{})
			for r := range right {
				set[rightKey(r)] = struct{}{}
			}
			var seen map[L]struct{}
			if !all {
				seen = make(map[L]struct{})
			}
			for l := range left {
				_, present := set[leftKey(l)]
				if present != keep {
					continue
				}
				if !all {
					if _, dup := seen[l]; dup {
						continue
					}
					seen[l] = struct{}{}
				}
				if !yield(l) {
					return
				}
			}
		}
	}
}

// ExceptAll is SQL EXCEPT ALL: each right row cancels one equal left
// row; the remaining left rows come out in input order.
func ExceptAll[T comparable](right iter.Seq[T]) func(iter.Seq[T]) iter.Seq[T] {
	return multisetOp(right, false)
}

// IntersectAll is SQL INTERSECT ALL: a left row comes out once for each
// equal right row not yet claimed, in left input order.
func IntersectAll[T comparable](right iter.Seq[T]) func(iter.Seq[T]) iter.Seq[T] {
	return multisetOp(right, true)
}

func multisetOp[T comparable](right iter.Seq[T], keep bool) func(iter.Seq[T]) iter.Seq[T] {
	return func(left iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			counts := make(map[T]int)
			for r := range right {
				counts[r]++
			}
			for l := range left {
				present := counts[l] > 0
				if present {
					counts[l]--
				}
				if present != keep {
					continue
				}
				if !yield(l) {
					return
				}
			}
		}
	}
}

// ExceptParallel is the sharded form of [Except] with all=true: a pure
// per-row membership filter, so each shard probes the shared read-only
// set independently, as [HashJoinParallel] does. The distinct form is
// this followed by [DistinctParallel] with the identity key.
func ExceptParallel[L any, R any, K comparable](left Stream[L], right iter.Seq[R], leftKey func(L) K, rightKey func(R) K) Stream[L] {
	return setOpParallel(left, right, leftKey, rightKey, false)
}

// IntersectParallel is the sharded form of [Intersect] with all=true.
func IntersectParallel[L any, R any, K comparable](left Stream[L], right iter.Seq[R], leftKey func(L) K, rightKey func(R) K) Stream[L] {
	return setOpParallel(left, right, leftKey, rightKey, true)
}

func setOpParallel[L any, R any, K comparable](left Stream[L], right iter.Seq[R], leftKey func(L) K, rightKey func(R) K, keep bool) Stream[L] {
	set := make(map[K]struct{})
	for r := range right {
		set[rightKey(r)] = struct{}{}
	}
	return left.Where(func(l L) bool {
		_, present := set[leftKey(l)]
		return present == keep
	})
}
