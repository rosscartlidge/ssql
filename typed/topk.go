package typed

import (
	"container/heap"
	"iter"
)

// Top-k selection for the typed pipeline. The serial forms (TopBy /
// BottomBy) mirror the Record-package ssql.TopBy / ssql.BottomBy: a
// bounded heap of size n gives O(N·log n) time and O(n) memory, versus
// the O(N·log N) time + O(N) memory of a full SortBy + Limit.
//
// The parallel forms (TopByParallel / BottomByParallel) exploit the
// fact that top-k is an associative reduction: each Stream[T] shard
// keeps its own size-n heap with no cross-shard coordination, then the
// per-shard survivors (≤ shards·n entries) are merged through one final
// size-n selection. Output is a plain iter.Seq[T] of the ≤ n winners,
// already ordered (descending key for Top, ascending for Bottom).

// topkEntry pairs an item with its precomputed key so the key function
// is evaluated exactly once per item, even across the merge step.
type topkEntry[T any, K Ordered] struct {
	item T
	key  K
}

// topkMinHeap keeps the SMALLEST key at the root, so TopBy can cheaply
// discard the current minimum whenever a larger key arrives.
type topkMinHeap[T any, K Ordered] []topkEntry[T, K]

func (h topkMinHeap[T, K]) Len() int           { return len(h) }
func (h topkMinHeap[T, K]) Less(i, j int) bool { return h[i].key < h[j].key }
func (h topkMinHeap[T, K]) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *topkMinHeap[T, K]) Push(x any)        { *h = append(*h, x.(topkEntry[T, K])) }
func (h *topkMinHeap[T, K]) Pop() any {
	old := *h
	m := len(old)
	it := old[m-1]
	*h = old[:m-1]
	return it
}

// topkMaxHeap keeps the LARGEST key at the root, so BottomBy can cheaply
// discard the current maximum whenever a smaller key arrives.
type topkMaxHeap[T any, K Ordered] []topkEntry[T, K]

func (h topkMaxHeap[T, K]) Len() int           { return len(h) }
func (h topkMaxHeap[T, K]) Less(i, j int) bool { return h[i].key > h[j].key }
func (h topkMaxHeap[T, K]) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *topkMaxHeap[T, K]) Push(x any)        { *h = append(*h, x.(topkEntry[T, K])) }
func (h *topkMaxHeap[T, K]) Pop() any {
	old := *h
	m := len(old)
	it := old[m-1]
	*h = old[:m-1]
	return it
}

// topNLargest retains the n largest entries pushed through emit and
// returns them in descending key order (largest first).
func topNLargest[T any, K Ordered](n int, push func(emit func(topkEntry[T, K]))) []topkEntry[T, K] {
	h := &topkMinHeap[T, K]{}
	push(func(e topkEntry[T, K]) {
		if h.Len() < n {
			heap.Push(h, e)
		} else if e.key > (*h)[0].key {
			(*h)[0] = e
			heap.Fix(h, 0)
		}
	})
	res := make([]topkEntry[T, K], h.Len())
	for i := len(res) - 1; i >= 0; i-- {
		res[i] = heap.Pop(h).(topkEntry[T, K])
	}
	return res
}

// topNSmallest retains the n smallest entries pushed through emit and
// returns them in ascending key order (smallest first).
func topNSmallest[T any, K Ordered](n int, push func(emit func(topkEntry[T, K]))) []topkEntry[T, K] {
	h := &topkMaxHeap[T, K]{}
	push(func(e topkEntry[T, K]) {
		if h.Len() < n {
			heap.Push(h, e)
		} else if e.key < (*h)[0].key {
			(*h)[0] = e
			heap.Fix(h, 0)
		}
	})
	res := make([]topkEntry[T, K], h.Len())
	for i := len(res) - 1; i >= 0; i-- {
		res[i] = heap.Pop(h).(topkEntry[T, K])
	}
	return res
}

// TopBy returns the top n items by key (highest first). Heap-based:
// O(N·log n) time, O(n) memory — the typed analogue of ssql.TopBy.
func TopBy[T any, K Ordered](n int, keyFn func(T) K) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if n <= 0 {
				return
			}
			res := topNLargest(n, func(emit func(topkEntry[T, K])) {
				for v := range in {
					emit(topkEntry[T, K]{item: v, key: keyFn(v)})
				}
			})
			for _, e := range res {
				if !yield(e.item) {
					return
				}
			}
		}
	}
}

// BottomBy returns the bottom n items by key (lowest first). Heap-based:
// O(N·log n) time, O(n) memory — the typed analogue of ssql.BottomBy.
func BottomBy[T any, K Ordered](n int, keyFn func(T) K) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if n <= 0 {
				return
			}
			res := topNSmallest(n, func(emit func(topkEntry[T, K])) {
				for v := range in {
					emit(topkEntry[T, K]{item: v, key: keyFn(v)})
				}
			})
			for _, e := range res {
				if !yield(e.item) {
					return
				}
			}
		}
	}
}

// TopByParallel selects the top n items by key from a Stream[T]. Each
// shard keeps its own size-n heap concurrently; the survivors are merged
// through one final size-n selection. Returns the ≤ n winners as an
// iter.Seq[T] in descending key order. Use this on a Stream source; for
// a plain iter.Seq use [TopBy].
func TopByParallel[T any, K Ordered](in Stream[T], n int, keyFn func(T) K) iter.Seq[T] {
	return func(yield func(T) bool) {
		final := topkStreamReduce(in, n, keyFn, topNLargest[T, K])
		for _, e := range final {
			if !yield(e.item) {
				return
			}
		}
	}
}

// BottomByParallel is [TopByParallel] for the n smallest items
// (ascending key order).
func BottomByParallel[T any, K Ordered](in Stream[T], n int, keyFn func(T) K) iter.Seq[T] {
	return func(yield func(T) bool) {
		final := topkStreamReduce(in, n, keyFn, topNSmallest[T, K])
		for _, e := range final {
			if !yield(e.item) {
				return
			}
		}
	}
}

// topkStreamReduce runs select (topNLargest or topNSmallest) over every
// shard in parallel, then once more over the concatenated survivors.
func topkStreamReduce[T any, K Ordered](
	in Stream[T],
	n int,
	keyFn func(T) K,
	selectN func(int, func(func(topkEntry[T, K]))) []topkEntry[T, K],
) []topkEntry[T, K] {
	nShards := len(in.shards)
	if n <= 0 || nShards == 0 {
		return nil
	}
	partials := make([][]topkEntry[T, K], nShards)
	var g shardGroup
	for i, shard := range in.shards {
		i, shard := i, shard
		g.Go(func() {
			partials[i] = selectN(n, func(emit func(topkEntry[T, K])) {
				for v := range shard {
					emit(topkEntry[T, K]{item: v, key: keyFn(v)})
				}
			})
		})
	}
	g.Wait()
	return selectN(n, func(emit func(topkEntry[T, K])) {
		for _, p := range partials {
			for _, e := range p {
				emit(e)
			}
		}
	})
}

// topNFunc is topNLargest/topNSmallest for a comparator instead of an
// Ordered key: the n items that rank highest (largest) or lowest under
// cmp, in rank order. For a key type that is not cmp.Ordered — an
// ssql.Int128 column (DFC155 C) — the generated code uses the *Func
// forms below, as sort uses SortByFunc.
func topNFunc[T any](n int, cmp func(a, b T) int, largest bool, source func(emit func(T))) []T {
	// A bounded heap whose root is the WORST survivor: for the n largest
	// the smallest, for the n smallest the largest.
	worse := func(a, b T) bool { // a ranks worse than b
		if largest {
			return cmp(a, b) < 0
		}
		return cmp(a, b) > 0
	}
	h := make([]T, 0, n)
	up := func(i int) {
		for i > 0 {
			p := (i - 1) / 2
			if !worse(h[i], h[p]) {
				return
			}
			h[i], h[p] = h[p], h[i]
			i = p
		}
	}
	down := func(i int) {
		for {
			l, r, m := 2*i+1, 2*i+2, i
			if l < len(h) && worse(h[l], h[m]) {
				m = l
			}
			if r < len(h) && worse(h[r], h[m]) {
				m = r
			}
			if m == i {
				return
			}
			h[i], h[m] = h[m], h[i]
			i = m
		}
	}
	source(func(v T) {
		if len(h) < n {
			h = append(h, v)
			up(len(h) - 1)
		} else if worse(h[0], v) {
			h[0] = v
			down(0)
		}
	})
	res := make([]T, len(h))
	for i := len(res) - 1; i >= 0; i-- {
		res[i] = h[0]
		last := len(h) - 1
		h[0] = h[last]
		h = h[:last]
		down(0)
	}
	return res
}

// TopByFunc returns the top n items under cmp (the one that compares
// greatest first) — [TopBy] for a key type that is not cmp.Ordered.
func TopByFunc[T any](n int, cmp func(a, b T) int) func(iter.Seq[T]) iter.Seq[T] {
	return topByFunc(n, cmp, true)
}

// BottomByFunc returns the bottom n items under cmp (the one that
// compares least first) — [BottomBy] for a comparator.
func BottomByFunc[T any](n int, cmp func(a, b T) int) func(iter.Seq[T]) iter.Seq[T] {
	return topByFunc(n, cmp, false)
}

func topByFunc[T any](n int, cmp func(a, b T) int, largest bool) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if n <= 0 {
				return
			}
			for _, v := range topNFunc(n, cmp, largest, func(emit func(T)) {
				for v := range in {
					emit(v)
				}
			}) {
				if !yield(v) {
					return
				}
			}
		}
	}
}

// TopByFuncParallel is [TopByParallel] for a comparator: a size-n heap
// per shard, the survivors merged through one final selection.
func TopByFuncParallel[T any](in Stream[T], n int, cmp func(a, b T) int) iter.Seq[T] {
	return topByFuncParallel(in, n, cmp, true)
}

// BottomByFuncParallel is [BottomByParallel] for a comparator.
func BottomByFuncParallel[T any](in Stream[T], n int, cmp func(a, b T) int) iter.Seq[T] {
	return topByFuncParallel(in, n, cmp, false)
}

func topByFuncParallel[T any](in Stream[T], n int, cmp func(a, b T) int, largest bool) iter.Seq[T] {
	return func(yield func(T) bool) {
		nShards := len(in.shards)
		if n <= 0 || nShards == 0 {
			return
		}
		partials := make([][]T, nShards)
		var g shardGroup
		for i, shard := range in.shards {
			i, shard := i, shard
			g.Go(func() {
				partials[i] = topNFunc(n, cmp, largest, func(emit func(T)) {
					for v := range shard {
						emit(v)
					}
				})
			})
		}
		g.Wait()
		for _, v := range topNFunc(n, cmp, largest, func(emit func(T)) {
			for _, p := range partials {
				for _, v := range p {
					emit(v)
				}
			}
		}) {
			if !yield(v) {
				return
			}
		}
	}
}
