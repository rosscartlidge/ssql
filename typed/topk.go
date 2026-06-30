package typed

import (
	"container/heap"
	"iter"
	"sync"
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
	var wg sync.WaitGroup
	wg.Add(nShards)
	for i, shard := range in.shards {
		i, shard := i, shard
		go func() {
			defer wg.Done()
			partials[i] = selectN(n, func(emit func(topkEntry[T, K])) {
				for v := range shard {
					emit(topkEntry[T, K]{item: v, key: keyFn(v)})
				}
			})
		}()
	}
	wg.Wait()
	return selectN(n, func(emit func(topkEntry[T, K])) {
		for _, p := range partials {
			for _, e := range p {
				emit(e)
			}
		}
	})
}
