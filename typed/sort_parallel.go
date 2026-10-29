package typed

import (
	"cmp"
	"iter"
	"runtime"
	"sync"
)

// The parallel sort (2026-10-29, DFC158 §3.13). The sort stage was the
// one barrier the typed lane still ran the old way: SerialOnly, so the
// planner fanned the source's shards into one iter.Seq through a channel
// (10 M rows at ~100 ns each) and slices.SortFunc sorted the slice on
// one core with a closure comparator — 3.5 s of a 5.5 s program whose
// DuckDB twin ran in 0.7. SortParallelBy and SortParallelFunc take the
// Stream: the shards are materialised concurrently with no channel, an
// index is sorted by a typed key as the window's partitions are
// (sortIdxByKey / sortIdxByString: (key, index) pairs, chunks sorted on
// every core and merged pairwise, the index the tiebreak, so the sort
// is STABLE — as ssql.SortRecords is, which slices.SortFunc is not),
// the rows are gathered into order on every core, and the output is an
// ordered Stream whose shards are ranges of the sorted rows, so the
// consumer after the sort runs parallel too and Serial walks it in
// order. The sort runs once, on the first shard consumed.

// SortParallelBy sorts a Stream by a key, ascending or descending, ties
// in input order (shard order, which is the input's).
func SortParallelBy[T any, K Ordered](key func(T) K, desc bool) func(Stream[T]) Stream[T] {
	return func(in Stream[T]) Stream[T] {
		return orderedStream(func() []T {
			all := materialise(in)
			idx := identity(len(all))
			if ks, ok := any(key).(func(T) string); ok {
				sortIdxByString(all, idx, ks, desc)
			} else {
				sortIdxByKey(all, idx, key, desc, cmp.Compare[K])
			}
			return gather(all, idx)
		})
	}
}

// SortParallelFunc sorts a Stream by a comparator (a multi-field order,
// an Int128 or decimal key), ties in input order.
func SortParallelFunc[T any](compare func(a, b T) int) func(Stream[T]) Stream[T] {
	return func(in Stream[T]) Stream[T] {
		return orderedStream(func() []T {
			all := materialise(in)
			idx := identity(len(all))
			sortIdx(idx, func(a, b int) int {
				if c := compare(all[a], all[b]); c != 0 {
					return c
				}
				return cmp.Compare(a, b)
			})
			return gather(all, idx)
		})
	}
}

func identity(n int) []int {
	idx := make([]int, n)
	parallelRanges(n, func(lo, hi int) {
		for i := lo; i < hi; i++ {
			idx[i] = i
		}
	})
	return idx
}

// gather is the rows in idx order, copied as ranges on every core (a
// random read per row, the memory system's parallelism; DFC158 §3.10).
func gather[T any](all []T, idx []int) []T {
	out := make([]T, len(idx))
	parallelRanges(len(idx), func(lo, hi int) {
		for p := lo; p < hi; p++ {
			out[p] = all[idx[p]]
		}
	})
	return out
}

// orderedStream is an ordered Stream over the rows prepare returns —
// one shard per core, each a contiguous range — computed once, on the
// first shard consumed; a panic in prepare is captured and re-raised by
// every shard (sync.Once would otherwise hand the later shards nothing).
func orderedStream[T any](prepare func() []T) Stream[T] {
	var (
		once    sync.Once
		rows    []T
		failure any
	)
	ready := func() {
		once.Do(func() {
			defer func() {
				if r := recover(); r != nil {
					failure = r
				}
			}()
			rows = prepare()
		})
		if failure != nil {
			panic(failure)
		}
	}
	r := runtime.GOMAXPROCS(0)
	shards := make([]iter.Seq[T], r)
	for i := range r {
		shards[i] = func(yield func(T) bool) {
			ready()
			n := len(rows)
			for p := i * n / r; p < (i+1)*n/r; p++ {
				if !yield(rows[p]) {
					return
				}
			}
		}
	}
	return Stream[T]{shards: shards, n: r, ordered: true, backing: func() []T { ready(); return rows }}
}
