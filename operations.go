package ssql

import (
	"cmp"
	"container/heap"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// CORE STREAM OPERATIONS - FUNCTIONAL FILTER API
// ============================================================================

// ============================================================================
// TRANSFORM OPERATIONS
// ============================================================================

// Select transforms each element using the provided function (SQL SELECT).
// This is the fundamental transformation operation in ssql.
//
// Example:
//
//	// Transform integers to strings
//	numbers := slices.Values([]int{1, 2, 3, 4, 5})
//	strings := ssql.Select(func(n int) string {
//	    return fmt.Sprintf("Number: %d", n)
//	})(numbers)
//
//	// Transform records
//	data, _ := ssql.ReadCSV("people.csv")
//	names := ssql.Select(func(r ssql.Record) string {
//	    return ssql.GetOr(r, "name", "")
//	})(data)
func Select[T, U any](fn func(T) U) Filter[T, U] {
	return func(input iter.Seq[T]) iter.Seq[U] {
		return func(yield func(U) bool) {
			for v := range input {
				if !yield(fn(v)) {
					return
				}
			}
		}
	}
}

// Update transforms records by applying mutations via MutableRecord.
// The function receives a mutable copy of each record and should return
// the mutated record (which will be frozen before yielding).
//
// This is a convenience wrapper around Select that handles the
// ToMutable() and Freeze() boilerplate, making field updates more concise.
//
// Example - Update single field:
//
//	updated := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
//	    return mut.String("status", "processed")
//	})(records)
//
// Example - Update multiple fields with chaining:
//
//	updated := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
//	    return mut.
//	        String("status", "processed").
//	        Time("updated_at", time.Now())
//	})(records)
//
// Example - Computed field update:
//
//	updated := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
//	    frozen := mut.Freeze()
//	    price := ssql.GetOr(frozen, "price", float64(0))
//	    qty := ssql.GetOr(frozen, "quantity", int64(0))
//	    return mut.Float("total", price * float64(qty))
//	})(records)
//
// Example - Conditional update:
//
//	updated := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
//	    frozen := mut.Freeze()
//	    if ssql.GetOr(frozen, "age", int64(0)) >= 18 {
//	        return mut.String("category", "adult")
//	    }
//	    return mut.String("category", "minor")
//	})(records)
//
// Equivalent to:
//
//	Select(func(r Record) Record {
//	    return r.ToMutable().String("status", "processed").Freeze()
//	})
func Update(fn func(MutableRecord) MutableRecord) Filter[Record, Record] {
	return Select(func(r Record) Record {
		mut := r.ToMutable()
		return fn(mut).Freeze()
	})
}

// SelectSafe transforms each element with error handling (SQL SELECT with errors)
func SelectSafe[T, U any](fn func(T) (U, error)) FilterWithErrors[T, U] {
	return func(input iter.Seq2[T, error]) iter.Seq2[U, error] {
		return func(yield func(U, error) bool) {
			for v, err := range input {
				if err != nil {
					var zero U
					if !yield(zero, err) {
						return
					}
					continue
				}
				result, mapErr := fn(v)
				if !yield(result, mapErr) {
					return
				}
			}
		}
	}
}

// SelectMany flattens nested sequences into a single stream (SQL SELECT with UNNEST).
// This is ssql's equivalent to FlatMap in functional programming.
// Use this for one-to-many transformations where each input produces multiple outputs.
//
// Example:
//
//	// Split strings into individual words
//	sentences := slices.Values([]string{"hello world", "foo bar"})
//	words := ssql.SelectMany(func(s string) iter.Seq[string] {
//	    return slices.Values(strings.Fields(s))
//	})(sentences)
//	// Result: ["hello", "world", "foo", "bar"]
//
//	// Expand records with iter.Seq fields
//	data := []ssql.Record{
//	    ssql.MakeMutableRecord().
//	        String("user", "Alice").
//	        IntSeq("scores", slices.Values([]int{90, 85, 95})).
//	        Freeze(),
//	}
//	expanded := ssql.SelectMany(func(r ssql.Record) iter.Seq[ssql.Record] {
//	    scores := ssql.Get[iter.Seq[int]](r, "scores")
//	    return ssql.Select(func(score int) ssql.Record {
//	        return ssql.MakeMutableRecord().
//	            String("user", ssql.GetOr(r, "user", "")).
//	            Int("score", int64(score)).
//	            Freeze()
//	    })(scores)
//	})(slices.Values(data))
func SelectMany[T, U any](fn func(T) iter.Seq[U]) Filter[T, U] {
	return func(input iter.Seq[T]) iter.Seq[U] {
		return func(yield func(U) bool) {
			for v := range input {
				for u := range fn(v) {
					if !yield(u) {
						return
					}
				}
			}
		}
	}
}

// ============================================================================
// FILTER OPERATIONS
// ============================================================================

// Where filters elements based on a predicate (equivalent to SQL WHERE).
// This is the fundamental filtering operation in ssql.
//
// Example:
//
//	// Filter positive integers
//	numbers := slices.Values([]int{-2, -1, 0, 1, 2, 3})
//	positive := ssql.Where(func(n int) bool {
//	    return n > 0
//	})(numbers)
//	// Result: [1, 2, 3]
//
//	// Filter CSV data
//	data, _ := ssql.ReadCSV("people.csv")
//	adults := ssql.Where(func(r ssql.Record) bool {
//	    age := ssql.GetOr(r, "age", int64(0))
//	    return age >= 18
//	})(data)
func Where[T any](predicate func(T) bool) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			for v := range input {
				if predicate(v) && !yield(v) {
					return
				}
			}
		}
	}
}

// WhereSafe filters elements with error handling
func WhereSafe[T any](predicate func(T) (bool, error)) FilterWithErrors[T, T] {
	return func(input iter.Seq2[T, error]) iter.Seq2[T, error] {
		return func(yield func(T, error) bool) {
			for v, err := range input {
				if err != nil {
					if !yield(v, err) {
						return
					}
					continue
				}
				include, predErr := predicate(v)
				if predErr != nil {
					if !yield(v, predErr) {
						return
					}
					continue
				}
				if include && !yield(v, nil) {
					return
				}
			}
		}
	}
}

// ============================================================================
// LIMITING OPERATIONS - SQL-STYLE
// ============================================================================

// TakeLast keeps only the last n elements, in arrival order (the tail
// of the sequence; `ssql limit -last N`). A ring buffer of n elements
// — O(n) memory regardless of input length — emitted once the input
// is exhausted, so TakeLast is a barrier: nothing flows until the source
// ends. n <= 0 yields nothing.
//
// Example:
//
//	ssql.TakeLast[ssql.Record](10)(records) // the 10 most recently read records
func TakeLast[T any](n int) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if n <= 0 {
				return
			}
			// Capacity is a hint: a huge N must not fail in make() (DFC133).
			ring := make([]T, 0, min(n, 1<<16))
			head := 0 // index of the oldest element once the ring is full
			for v := range input {
				if len(ring) < n {
					ring = append(ring, v)
				} else {
					ring[head] = v
					head = (head + 1) % n
				}
			}
			for i := 0; i < len(ring); i++ {
				if !yield(ring[(head+i)%len(ring)]) {
					return
				}
			}
		}
	}
}

// Limit restricts iterator to first N elements (equivalent to SQL LIMIT).
// Essential for converting infinite streams to finite ones.
//
// Example:
//
//	// Get first 10 records
//	data, _ := ssql.ReadCSV("large_file.csv")
//	first10 := ssql.Limit[ssql.Record](10)(data)
//
//	// Combined with other operations
//	topCustomers := ssql.Limit[ssql.Record](5)(
//	    ssql.SortBy(func(r ssql.Record) float64 {
//	        return -ssql.GetOr(r, "revenue", float64(0))
//	    })(data))
func Limit[T any](n int) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if n <= 0 {
				return
			}

			count := 0
			for v := range input {
				if count >= n {
					return
				}
				if !yield(v) {
					return
				}
				count++
			}
		}
	}
}

// LimitSafe restricts iterator with error handling
func LimitSafe[T any](n int) FilterWithErrors[T, T] {
	return func(input iter.Seq2[T, error]) iter.Seq2[T, error] {
		return func(yield func(T, error) bool) {
			count := 0
			for v, err := range input {
				if count >= n {
					return
				}
				if !yield(v, err) {
					return
				}
				if err == nil {
					count++
				}
			}
		}
	}
}

// Offset skips first N elements (equivalent to SQL OFFSET)
func Offset[T any](n int) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			skipped := 0
			for v := range input {
				if skipped < n {
					skipped++
					continue
				}
				if !yield(v) {
					return
				}
			}
		}
	}
}

// OffsetSafe skips first N elements with error handling
func OffsetSafe[T any](n int) FilterWithErrors[T, T] {
	return func(input iter.Seq2[T, error]) iter.Seq2[T, error] {
		return func(yield func(T, error) bool) {
			skipped := 0
			for v, err := range input {
				if err != nil {
					if !yield(v, err) {
						return
					}
					continue
				}
				if skipped < n {
					skipped++
					continue
				}
				if !yield(v, nil) {
					return
				}
			}
		}
	}
}

// ============================================================================
// ORDERING OPERATIONS
// ============================================================================

// Sort sorts elements in ascending order using standard library
func Sort[T cmp.Ordered]() Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return slices.Values(slices.Sorted(input))
	}
}

// SortBy sorts elements using a key extraction function
func SortBy[T any, K cmp.Ordered](keyFn func(T) K) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return slices.Values(slices.SortedFunc(input, func(a, b T) int {
			return cmp.Compare(keyFn(a), keyFn(b))
		}))
	}
}

// SortFunc sorts elements using a comparator function.
// The comparator should return a negative number when a < b, zero when a == b,
// and a positive number when a > b. The sort is STABLE: equal elements
// keep their input order, so `sort` and `sort -spill` (whose runs are
// stably sorted and stably merged) agree on ties, and every lane's
// output for a sort on a non-unique key is the same.
func SortFunc[T any](cmpFn func(T, T) int) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return slices.Values(slices.SortedStableFunc(input, cmpFn))
	}
}

// SortDesc sorts elements in descending order
func SortDesc[T cmp.Ordered]() Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return slices.Values(slices.SortedFunc(input, func(a, b T) int {
			return cmp.Compare(b, a) // Reverse comparison
		}))
	}
}

// TopBy returns the top N elements by key value (highest first).
// Uses a min-heap of size N for O(N*log(K)) time and O(K) memory.
// Results are returned in descending order by key.
func TopBy[T any, K cmp.Ordered](n int, keyFn func(T) K) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if n <= 0 {
				return
			}

			h := &topMinHeap[T, K]{}
			for item := range input {
				key := keyFn(item)
				if h.Len() < n {
					heap.Push(h, topHeapEntry[T, K]{item: item, key: key})
				} else if key > (*h)[0].key {
					(*h)[0] = topHeapEntry[T, K]{item: item, key: key}
					heap.Fix(h, 0)
				}
			}

			// Extract sorted descending
			result := make([]topHeapEntry[T, K], h.Len())
			for i := len(result) - 1; i >= 0; i-- {
				result[i] = heap.Pop(h).(topHeapEntry[T, K])
			}

			for _, entry := range result {
				if !yield(entry.item) {
					return
				}
			}
		}
	}
}

// BottomBy returns the bottom N elements by key value (lowest first).
// Uses a max-heap of size N for O(N*log(K)) time and O(K) memory.
// Results are returned in ascending order by key.
func BottomBy[T any, K cmp.Ordered](n int, keyFn func(T) K) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if n <= 0 {
				return
			}

			h := &topMaxHeap[T, K]{}
			for item := range input {
				key := keyFn(item)
				if h.Len() < n {
					heap.Push(h, topHeapEntry[T, K]{item: item, key: key})
				} else if key < (*h)[0].key {
					(*h)[0] = topHeapEntry[T, K]{item: item, key: key}
					heap.Fix(h, 0)
				}
			}

			// Extract sorted ascending
			result := make([]topHeapEntry[T, K], h.Len())
			for i := len(result) - 1; i >= 0; i-- {
				result[i] = heap.Pop(h).(topHeapEntry[T, K])
			}

			for _, entry := range result {
				if !yield(entry.item) {
					return
				}
			}
		}
	}
}

// topHeapEntry holds an item and its pre-computed key for heap operations
type topHeapEntry[T any, K cmp.Ordered] struct {
	item T
	key  K
}

// topMinHeap is a min-heap used by TopBy (keeps smallest at root to efficiently discard)
type topMinHeap[T any, K cmp.Ordered] []topHeapEntry[T, K]

func (h topMinHeap[T, K]) Len() int           { return len(h) }
func (h topMinHeap[T, K]) Less(i, j int) bool { return h[i].key < h[j].key }
func (h topMinHeap[T, K]) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *topMinHeap[T, K]) Push(x any)        { *h = append(*h, x.(topHeapEntry[T, K])) }
func (h *topMinHeap[T, K]) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// topMaxHeap is a max-heap used by BottomBy (keeps largest at root to efficiently discard)
type topMaxHeap[T any, K cmp.Ordered] []topHeapEntry[T, K]

func (h topMaxHeap[T, K]) Len() int           { return len(h) }
func (h topMaxHeap[T, K]) Less(i, j int) bool { return h[i].key > h[j].key }
func (h topMaxHeap[T, K]) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *topMaxHeap[T, K]) Push(x any)        { *h = append(*h, x.(topHeapEntry[T, K])) }
func (h *topMaxHeap[T, K]) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// TopByFunc returns the top N elements ranked by a comparator: cmp(a, b) > 0
// means a ranks higher than b (and so sorts earlier in the result). Like
// [TopBy] it uses a bounded heap of size N — O(N*log(K)) time, O(K) memory —
// but ranks by an arbitrary comparator instead of a single Ordered key. Use
// this when the ordering is type-aware (e.g. [CompareAny], which orders
// numbers numerically and everything else lexicographically) so a single field
// can hold mixed or non-numeric values. Results are highest-first.
func TopByFunc[T any](n int, cmp func(a, b T) int) Filter[T, T] {
	return topByComparator(n, cmp, false)
}

// BottomByFunc returns the bottom N elements ranked by a comparator (the N for
// which cmp ranks them lowest), lowest-first. The comparator counterpart of
// [BottomBy]; see [TopByFunc].
func BottomByFunc[T any](n int, cmp func(a, b T) int) Filter[T, T] {
	return topByComparator(n, cmp, true)
}

// topByComparator backs TopByFunc/BottomByFunc. asc=false keeps the N highest
// (heap root = current lowest survivor, replaced when a higher item arrives);
// asc=true keeps the N lowest (root = current highest survivor). Either way the
// result is emitted best-first.
func topByComparator[T any](n int, cmp func(a, b T) int, asc bool) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if n <= 0 {
				return
			}
			// The heap's worst survivor sits at the root so it can be
			// discarded in O(log K). For top-N that's the smallest; for
			// bottom-N the largest — invert the comparator for the latter.
			less := func(a, b T) bool { return cmp(a, b) < 0 }
			worse := func(item, root T) bool { return cmp(item, root) > 0 }
			if asc {
				less = func(a, b T) bool { return cmp(a, b) > 0 }
				worse = func(item, root T) bool { return cmp(item, root) < 0 }
			}
			h := &cmpHeap[T]{less: less}
			for item := range input {
				if len(h.items) < n {
					heap.Push(h, item)
				} else if worse(item, h.items[0]) {
					h.items[0] = item
					heap.Fix(h, 0)
				}
			}
			result := make([]T, len(h.items))
			for i := len(result) - 1; i >= 0; i-- {
				result[i] = heap.Pop(h).(T)
			}
			for _, item := range result {
				if !yield(item) {
					return
				}
			}
		}
	}
}

// cmpHeap is a comparator-driven heap used by TopByFunc/BottomByFunc.
type cmpHeap[T any] struct {
	items []T
	less  func(a, b T) bool
}

func (h *cmpHeap[T]) Len() int           { return len(h.items) }
func (h *cmpHeap[T]) Less(i, j int) bool { return h.less(h.items[i], h.items[j]) }
func (h *cmpHeap[T]) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *cmpHeap[T]) Push(x any)         { h.items = append(h.items, x.(T)) }
func (h *cmpHeap[T]) Pop() any {
	old := h.items
	n := len(old)
	item := old[n-1]
	h.items = old[:n-1]
	return item
}

// MergeSorted performs a k-way merge of pre-sorted Record iterators.
// Each source must already be sorted by the given orderBy fields.
// Output is a single sorted iterator with O(K) memory where K is the number of sources.
// When records compare equal, sources with lower index are emitted first (stable).
func MergeSorted(orderBy []OrderField, sources ...iter.Seq[Record]) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		if len(sources) == 0 {
			return
		}

		// Convert push iterators to pull iterators
		h := &mergeHeap{orderBy: orderBy}
		stops := make([]func(), 0, len(sources))
		defer func() {
			for _, stop := range stops {
				stop()
			}
		}()

		for i, src := range sources {
			next, stop := iter.Pull(src)
			stops = append(stops, stop)
			if rec, ok := next(); ok {
				heap.Push(h, mergeHeapEntry{record: rec, next: next, index: i})
			}
		}

		for h.Len() > 0 {
			entry := heap.Pop(h).(mergeHeapEntry)
			if !yield(entry.record) {
				return
			}
			if rec, ok := entry.next(); ok {
				heap.Push(h, mergeHeapEntry{record: rec, next: entry.next, index: entry.index})
			}
		}
	}
}

// mergeHeapEntry holds the current record from a source and its pull function
type mergeHeapEntry struct {
	record Record
	next   func() (Record, bool)
	index  int // source index for stable ordering
}

// mergeHeap is a min-heap for k-way merge sort
type mergeHeap struct {
	entries []mergeHeapEntry
	orderBy []OrderField
}

func (h mergeHeap) Len() int { return len(h.entries) }
func (h mergeHeap) Less(i, j int) bool {
	cmp := CompareRecordFields(h.entries[i].record, h.entries[j].record, h.orderBy)
	if cmp == 0 {
		return h.entries[i].index < h.entries[j].index // stable
	}
	return cmp < 0
}
func (h mergeHeap) Swap(i, j int) { h.entries[i], h.entries[j] = h.entries[j], h.entries[i] }
func (h *mergeHeap) Push(x any)   { h.entries = append(h.entries, x.(mergeHeapEntry)) }
func (h *mergeHeap) Pop() any {
	old := h.entries
	n := len(old)
	entry := old[n-1]
	h.entries = old[:n-1]
	return entry
}

// ============================================================================
// UTILITY OPERATIONS
// ============================================================================

// Distinct removes duplicate elements (requires comparable type)
func Distinct[T comparable]() Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			seen := make(map[T]bool)
			for v := range input {
				if !seen[v] {
					seen[v] = true
					if !yield(v) {
						return
					}
				}
			}
		}
	}
}

// DistinctBy removes duplicates based on a key extraction function
func DistinctBy[T any, K comparable](keyFn func(T) K) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			seen := make(map[K]bool)
			for v := range input {
				key := keyFn(v)
				if !seen[key] {
					seen[key] = true
					if !yield(v) {
						return
					}
				}
			}
		}
	}
}

// Reverse reverses the order of elements
func Reverse[T any]() Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			collected := slices.Collect(input)
			// Yield in reverse order
			for i := len(collected) - 1; i >= 0; i-- {
				if !yield(collected[i]) {
					return
				}
			}
		}
	}
}

// ============================================================================
// WINDOW OPERATIONS
// ============================================================================

// ============================================================================
// STREAM UTILITIES
// ============================================================================

// Tee splits a stream into multiple identical streams for parallel consumption.
// Returns a slice of iterators that will each yield the same sequence of values.
// The source stream is fully consumed and buffered to enable multiple iterations.
func Tee[T any](input iter.Seq[T], n int) []iter.Seq[T] {
	if n <= 0 {
		return nil
	}

	// Collect all values from the source stream
	var values []T
	for v := range input {
		values = append(values, v)
	}

	// Create n identical iterators over the collected values
	streams := make([]iter.Seq[T], n)
	for i := range n {
		streams[i] = slices.Values(values)
	}

	return streams
}

// LazyTee splits a stream into multiple identical streams using channels for infinite streams.
// Unlike Tee, this doesn't buffer the entire stream in memory, making it suitable for infinite streams.
// Uses channels with backpressure to handle slow consumers gracefully.
func LazyTee[T any](input iter.Seq[T], n int) []iter.Seq[T] {
	if n <= 0 {
		return nil
	}

	// Create channels for each output stream
	channels := make([]chan T, n)
	done := make(chan struct{})

	for i := range n {
		channels[i] = make(chan T, 100) // Buffered to handle temporary speed differences
	}

	// The broadcaster pulls the input in its own goroutine; a stage panic
	// there is captured and re-raised in whichever consumer drains next
	// (DFC142 step 1 — it used to tear the process down from here).
	var g panicGroup
	g.Go(func() {
		defer func() {
			for _, ch := range channels {
				close(ch)
			}
		}()

		for v := range input {
			// Send to every consumer, blocking on a full buffer: that is the
			// backpressure the doc promises. Until 2026-10-05 a `default:`
			// branch dropped the value for any consumer whose buffer was full
			// — silent data loss for both consumers once the producer
			// outpaced them (a 1,000-element source delivered 201 and 150).
			for _, ch := range channels {
				select {
				case ch <- v:
				case <-done:
					// One of the consumers has terminated, stop broadcasting
					return
				}
			}
		}
	})

	// Create output iterators
	streams := make([]iter.Seq[T], n)
	for i := range n {
		ch := channels[i]
		streams[i] = func(yield func(T) bool) {
			defer func() {
				// Signal termination to broadcaster
				select {
				case <-done:
				default:
					close(done)
				}
			}()

			for v := range ch {
				if !yield(v) {
					return
				}
			}
			// The channel closed: the broadcaster finished or panicked.
			// Wait for it (its close runs before the capture) and re-raise.
			g.Wait()
		}
	}

	return streams
}

// ============================================================================
// STREAMING AGGREGATIONS FOR INFINITE STREAMS
// ============================================================================

// RunningSum maintains a running total, emitting updated results for each input element
// Perfect for real-time dashboards and continuous monitoring
func RunningSum(fieldName string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			var runningTotal float64
			count := 0

			for record := range input {
				// Extract value and add to running total
				value := GetOr(record, fieldName, 0.0)
				runningTotal += value
				count++

				// Create output record with running sum
				outputRecord := MakeMutableRecord()
				// Copy original record
				maps.Insert(outputRecord.fields, record.All())
				// Add running sum fields
				outputRecord.put("running_sum", runningTotal)
				outputRecord.put("running_count", int64(count))
				outputRecord.put("running_avg", runningTotal/float64(count))

				if !yield(outputRecord.Freeze()) {
					return
				}
			}
		}
	}
}

// RunningAverage computes a moving average over a specified window size
// Maintains bounded memory usage even for infinite streams
func RunningAverage(fieldName string, windowSize int) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			if windowSize <= 0 {
				return
			}

			window := make([]float64, 0, windowSize)
			var sum float64
			count := 0

			for record := range input {
				value := GetOr(record, fieldName, 0.0)
				count++

				// Add to window
				if len(window) < windowSize {
					window = append(window, value)
					sum += value
				} else {
					// Remove oldest value and add new one
					sum = sum - window[0] + value
					copy(window, window[1:])
					window[windowSize-1] = value
				}

				// Calculate moving average
				avg := sum / float64(len(window))

				// Create output record
				outputRecord := MakeMutableRecord()
				maps.Insert(outputRecord.fields, record.All())
				outputRecord.put("moving_avg", avg)
				outputRecord.put("window_size", int64(len(window)))
				outputRecord.put("total_count", int64(count))

				if !yield(outputRecord.Freeze()) {
					return
				}
			}
		}
	}
}

// ExponentialMovingAverage computes EMA with configurable smoothing factor
// Memory efficient and responsive to recent changes
func ExponentialMovingAverage(fieldName string, alpha float64) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			var ema float64
			initialized := false

			for record := range input {
				value := GetOr(record, fieldName, 0.0)

				if !initialized {
					ema = value
					initialized = true
				} else {
					// EMA formula: EMA = alpha * current + (1 - alpha) * previous_EMA
					ema = alpha*value + (1-alpha)*ema
				}

				// Create output record
				outputRecord := MakeMutableRecord()
				maps.Insert(outputRecord.fields, record.All())
				outputRecord.put("ema", ema)
				outputRecord.put("alpha", alpha)

				if !yield(outputRecord.Freeze()) {
					return
				}
			}
		}
	}
}

// RunningMinMax tracks minimum and maximum values continuously
func RunningMinMax(fieldName string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			var min, max float64
			initialized := false

			for record := range input {
				value := GetOr(record, fieldName, 0.0)

				if !initialized {
					min = value
					max = value
					initialized = true
				} else {
					if value < min {
						min = value
					}
					if value > max {
						max = value
					}
				}

				// Create output record
				outputRecord := MakeMutableRecord()
				maps.Insert(outputRecord.fields, record.All())
				outputRecord.put("running_min", min)
				outputRecord.put("running_max", max)
				outputRecord.put("running_range", max-min)

				if !yield(outputRecord.Freeze()) {
					return
				}
			}
		}
	}
}

// RunningCount counts occurrences of distinct values for a field
// Useful for real-time frequency analysis
func RunningCount(fieldName string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			counts := make(map[string]int64)
			totalCount := int64(0)

			for record := range input {
				// Convert field value to string for counting
				fieldValue, _ := Get[any](record, fieldName)
				value := fmt.Sprintf("%v", fieldValue)
				counts[value]++
				totalCount++

				// Create output record
				outputRecord := MakeMutableRecord()
				maps.Insert(outputRecord.fields, record.All())
				outputRecord.put("distinct_counts", counts)
				outputRecord.put("total_count", totalCount)
				outputRecord.put("distinct_values", int64(len(counts)))

				if !yield(outputRecord.Freeze()) {
					return
				}
			}
		}
	}
}

// ============================================================================
// WINDOWING OPERATIONS FOR INFINITE STREAMS
// ============================================================================

// CountWindow groups elements into fixed-size windows
// Essential for processing infinite streams in manageable chunks
func CountWindow[T any](size int) Filter[T, []T] {
	return func(input iter.Seq[T]) iter.Seq[[]T] {
		return func(yield func([]T) bool) {
			if size <= 0 {
				return
			}

			window := make([]T, 0, size)
			for v := range input {
				window = append(window, v)

				// Emit window when full
				if len(window) == size {
					if !yield(slices.Clone(window)) {
						return
					}
					window = window[:0] // Reset window
				}
			}

			// Emit partial window if any elements remain
			if len(window) > 0 {
				yield(window)
			}
		}
	}
}

// SlidingCountWindow creates overlapping windows with specified step size
// Useful for moving averages and trend analysis on infinite streams
func SlidingCountWindow[T any](windowSize, stepSize int) Filter[T, []T] {
	return func(input iter.Seq[T]) iter.Seq[[]T] {
		return func(yield func([]T) bool) {
			if windowSize <= 0 || stepSize <= 0 {
				return
			}

			buffer := make([]T, 0, windowSize)
			count := 0

			for v := range input {
				buffer = append(buffer, v)
				count++

				// Emit window when we have enough elements
				if len(buffer) == windowSize {
					if !yield(slices.Clone(buffer)) {
						return
					}

					// Slide the window by stepSize
					if stepSize >= windowSize {
						buffer = buffer[:0]
					} else {
						// Move window forward by stepSize
						copy(buffer, buffer[stepSize:])
						buffer = buffer[:len(buffer)-stepSize]
					}
				}
			}
		}
	}
}

// TimeWindow groups elements by time duration (requires timestamp field)
// Critical for time-series analysis of infinite streams
func TimeWindow[T any](duration time.Duration, timeField string) Filter[T, []T] {
	return func(input iter.Seq[T]) iter.Seq[[]T] {
		return func(yield func([]T) bool) {
			if duration <= 0 {
				return
			}

			var window []T
			var windowStart time.Time
			var initialized bool

			for v := range input {
				// Extract timestamp from record
				timestamp := extractTimestamp(v, timeField)
				if timestamp.IsZero() {
					continue // Skip records without valid timestamps
				}

				// Initialize window start time
				if !initialized {
					windowStart = timestamp.Truncate(duration)
					initialized = true
				}

				// Check if we need to emit current window
				windowEnd := windowStart.Add(duration)
				if timestamp.After(windowEnd) || timestamp.Equal(windowEnd) {
					// Emit current window
					if len(window) > 0 {
						if !yield(slices.Clone(window)) {
							return
						}
					}

					// Start new window
					windowStart = timestamp.Truncate(duration)
					window = window[:0]
				}

				window = append(window, v)
			}

			// Emit final window if any elements remain
			if len(window) > 0 {
				yield(window)
			}
		}
	}
}

// SlidingTimeWindow creates overlapping time-based windows
// Perfect for real-time analytics with overlapping time periods
func SlidingTimeWindow[T any](windowDuration, slideDuration time.Duration, timeField string) Filter[T, []T] {
	return func(input iter.Seq[T]) iter.Seq[[]T] {
		return func(yield func([]T) bool) {
			if windowDuration <= 0 || slideDuration <= 0 {
				return
			}

			var buffer []T
			var nextEmitTime time.Time
			var initialized bool

			for v := range input {
				timestamp := extractTimestamp(v, timeField)
				if timestamp.IsZero() {
					continue
				}

				if !initialized {
					nextEmitTime = timestamp.Add(slideDuration)
					initialized = true
				}

				// Add to buffer
				buffer = append(buffer, v)

				// Check if it's time to emit a window
				if timestamp.After(nextEmitTime) || timestamp.Equal(nextEmitTime) {
					// Collect elements within the window duration
					cutoffTime := timestamp.Add(-windowDuration)
					var window []T

					for _, item := range buffer {
						itemTime := extractTimestamp(item, timeField)
						if itemTime.After(cutoffTime) {
							window = append(window, item)
						}
					}

					if len(window) > 0 {
						if !yield(slices.Clone(window)) {
							return
						}
					}

					// Remove old elements from buffer
					var newBuffer []T
					for _, item := range buffer {
						itemTime := extractTimestamp(item, timeField)
						if itemTime.After(cutoffTime) {
							newBuffer = append(newBuffer, item)
						}
					}
					buffer = newBuffer

					// Set next emit time
					nextEmitTime = timestamp.Add(slideDuration)
				}
			}
		}
	}
}

// ============================================================================
// WINDOWING HELPER FUNCTIONS
// ============================================================================

// extractTimestamp extracts a timestamp from a value based on the field name
// Supports Record types and tries to parse various timestamp formats
func extractTimestamp(value any, timeField string) time.Time {
	// Handle Record type specifically
	if record, ok := value.(Record); ok {
		if timeValue, exists := Get[any](record, timeField); exists {
			return parseTimeValue(timeValue)
		}
	}

	// For other types, we'd need reflection or type assertions
	// For now, return zero time for unsupported types
	return time.Time{}
}

// parseTimeValue attempts to parse various time representations
func parseTimeValue(value any) time.Time {
	switch v := value.(type) {
	case time.Time:
		return v
	case string:
		// Try common timestamp formats
		formats := []string{
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05.000000",
		}

		for _, format := range formats {
			if t, err := time.Parse(format, v); err == nil {
				return t
			}
		}
	case int64:
		// Assume Unix timestamp
		return time.Unix(v, 0)
	case float64:
		// Assume Unix timestamp with potential fractional seconds
		return time.Unix(int64(v), int64((v-float64(int64(v)))*1e9))
	}

	return time.Time{} // Zero time if parsing fails
}

// ============================================================================
// EARLY TERMINATION PATTERNS FOR INFINITE STREAMS
// ============================================================================

// TakeWhile continues emitting elements while a predicate is true
// Stops processing as soon as the condition becomes false
func TakeWhile[T any](predicate func(T) bool) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			for v := range input {
				if !predicate(v) {
					return // Stop when predicate becomes false
				}
				if !yield(v) {
					return
				}
			}
		}
	}
}

// TakeUntil continues emitting elements until a predicate becomes true
// Stops processing as soon as the condition becomes true
func TakeUntil[T any](predicate func(T) bool) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			for v := range input {
				if predicate(v) {
					return // Stop when predicate becomes true
				}
				if !yield(v) {
					return
				}
			}
		}
	}
}

// Timeout limits stream processing to a maximum duration
// Automatically terminates infinite streams after the specified time
func Timeout[T any](duration time.Duration) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()

			// The producer pulls the input in its own goroutine so the clock
			// can cut it off, and forwards each value over ch; yield is
			// called HERE, in the consumer's goroutine, as the range-over-func
			// contract requires (it was called from the producer until
			// 2026-10-05). A stage panic in the producer is captured and
			// re-raised here after the channel closes (DFC142 step 1).
			ch := make(chan T)
			var g panicGroup
			g.Go(func() {
				defer close(ch)
				for v := range input {
					select {
					case ch <- v:
					case <-ctx.Done():
						return // Timeout reached, or the consumer stopped
					}
				}
			})

			for {
				select {
				case v, ok := <-ch:
					if !ok {
						g.Wait() // producer finished: re-raise its panic, if any
						return
					}
					if !yield(v) {
						cancel()
						g.Wait()
						return
					}
				case <-ctx.Done():
					// Timeout reached. The producer sees ctx.Done on its next
					// send; a value already pulled is dropped, as before.
					g.Wait()
					return
				}
			}
		}
	}
}

// TimeBasedTimeout stops processing after a specified time from the first element
// Uses a time field from records to determine when to stop
func TimeBasedTimeout(timeField string, duration time.Duration) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			var startTime time.Time
			started := false

			for record := range input {
				currentTime := extractTimestamp(record, timeField)

				if !started {
					startTime = currentTime
					started = true
				} else if !currentTime.IsZero() && currentTime.Sub(startTime) > duration {
					return // Time window exceeded
				}

				if !yield(record) {
					return
				}
			}
		}
	}
}

// SkipWhile skips elements while a predicate is true, then emits all remaining elements
// Useful for skipping headers or initial conditions in infinite streams
func SkipWhile[T any](predicate func(T) bool) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			skipping := true
			for v := range input {
				if skipping && predicate(v) {
					continue // Skip this element
				}
				skipping = false // Start emitting from here
				if !yield(v) {
					return
				}
			}
		}
	}
}

// SkipUntil skips elements until a predicate becomes true, then emits all remaining elements
func SkipUntil[T any](predicate func(T) bool) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			skipping := true
			for v := range input {
				if skipping && !predicate(v) {
					continue // Keep skipping
				}
				skipping = false // Start emitting from here
				if !yield(v) {
					return
				}
			}
		}
	}
}

// ParseFloat64 parses a string as float64, returning 0 on failure.
// Used by generated code to convert parameterized flag values for numeric comparisons.
func ParseFloat64(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// Explode is UNNEST for a nested list (DFC144 Level 2): one output row per
// element of the list in field, with the field holding the element (a
// scalar as the scalar it is, a nested element as JSON text) and every
// other field repeated. An empty, missing or null list yields no row —
// DuckDB's UNNEST semantics — or, with keepEmpty, one row in which the
// field has no value. A value that is neither a list nor absent is an
// error: exploding a scalar or an object is almost always a mistake, and
// flatten is the verb for an object.
func Explode(field string, keepEmpty bool) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			row := 0
			for record := range input {
				row++
				v, ok := Get[any](record, field)
				var list []any
				switch x := ExprValue(v).(type) {
				case nil:
				case []any:
					list = x
				default:
					if !ok {
						break
					}
					panic(fmt.Errorf("explode: row %d: field %q holds %s, not a list (use flatten for an object)", row, field, describeValue(x)))
				}
				if len(list) == 0 {
					if keepEmpty {
						if !yield(record.ToMutable().Null(field).Freeze()) {
							return
						}
					}
					continue
				}
				for _, elem := range list {
					mut := record.ToMutable()
					if js, nested := NestedValue(elem); nested {
						mut = mut.JSONString(field, js)
					} else if elem == nil {
						mut = mut.Null(field)
					} else {
						mut = setScalar(mut, field, elem)
					}
					if !yield(mut.Freeze()) {
						return
					}
				}
			}
		}
	}
}

// FlattenField is `s.*` for a nested object (DFC144 Level 2): the keys of
// the object in field become sibling fields named field.key, in the
// object's own key order; a nested object recurses to depth levels (1 =
// the object's keys only), and a value below that depth, or a list, stays
// JSON text. The field itself is removed unless keep. The key set is
// fixed by the FIRST row that has the object (a DuckDB STRUCT has the same
// keys in every row); a later row with a key the first did not have is an
// error naming it, rather than a column that silently exists for some
// rows. Rows without the field, or with a null, pass through unchanged. A
// value that is not an object is an error (explode is the verb for a list).
func FlattenField(field string, depth int, keep bool) Filter[Record, Record] {
	if depth < 1 {
		depth = 1
	}
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			var keys []string // the fixed key set (full dotted names), from the first object
			known := map[string]bool{}
			row := 0
			for record := range input {
				row++
				v, ok := Get[any](record, field)
				js, isJSON := v.(JSONString)
				if !ok || v == nil || (isJSON && js == "") {
					if !yield(record) {
						return
					}
					continue
				}
				if !isJSON || !strings.HasPrefix(strings.TrimSpace(string(js)), "{") {
					panic(fmt.Errorf("flatten: row %d: field %q holds %s, not an object (use explode for a list)", row, field, describeValue(v)))
				}
				pairs, err := flattenObject(js, field, depth)
				if err != nil {
					panic(fmt.Errorf("flatten: row %d: field %q: %w", row, field, err))
				}
				if keys == nil {
					for _, p := range pairs {
						keys = append(keys, p.key)
						known[p.key] = true
					}
				}
				mut := record.ToMutable()
				if !keep {
					mut = mut.Delete(field)
				}
				seen := map[string]bool{}
				for _, p := range pairs {
					if !known[p.key] {
						panic(fmt.Errorf("flatten: row %d: field %q has key %q, which the first row's object does not have (keys must be the same in every row, as a DuckDB STRUCT's are; normalise upstream)", row, field, strings.TrimPrefix(p.key, field+".")))
					}
					seen[p.key] = true
					if js, nested := NestedValue(p.val); nested {
						mut = mut.JSONString(p.key, js)
					} else if p.val == nil {
						mut = mut.Null(p.key)
					} else {
						mut = setScalar(mut, p.key, p.val)
					}
				}
				for _, k := range keys {
					if !seen[k] {
						mut = mut.Null(k) // a key the first row had and this row lacks: no value
					}
				}
				if !yield(mut.Freeze()) {
					return
				}
			}
		}
	}
}

type flatPair struct {
	key string
	val any
}

// flattenObject lists an object's keys in document order (encoding/json's
// map loses it, so the text is scanned) with their values, recursing into
// nested objects to depth.
func flattenObject(js JSONString, prefix string, depth int) ([]flatPair, error) {
	order, err := jsonObjectKeyOrder(string(js))
	if err != nil {
		return nil, err
	}
	parsed, ok := ExprValue(js).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("not an object")
	}
	var out []flatPair
	for _, k := range order {
		name := prefix + "." + k
		v := parsed[k]
		if sub, isObj := v.(map[string]any); isObj && depth > 1 {
			subJS := JSONValue(sub)
			subPairs, err := flattenObject(subJS, name, depth-1)
			if err != nil {
				return nil, err
			}
			out = append(out, subPairs...)
			continue
		}
		out = append(out, flatPair{name, v})
	}
	return out, nil
}

// jsonObjectKeyOrder returns a JSON object's top-level keys in document
// order.
func jsonObjectKeyOrder(text string) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not an object")
	}
	var keys []string
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("malformed object")
		}
		keys = append(keys, k)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// setScalar puts a parsed JSON scalar into a record with its own type.
func setScalar(mut MutableRecord, field string, v any) MutableRecord {
	switch x := v.(type) {
	case int64:
		return mut.Int(field, x)
	case float64:
		return mut.Float(field, x)
	case bool:
		return mut.Bool(field, x)
	case string:
		return mut.String(field, x)
	case time.Time:
		return mut.Time(field, x)
	}
	return mut.String(field, fmt.Sprintf("%v", v))
}

// describeValue names a value's kind for an error message.
func describeValue(v any) string {
	switch x := v.(type) {
	case JSONString:
		t := strings.TrimSpace(string(x))
		if strings.HasPrefix(t, "{") {
			return "an object"
		}
		if strings.HasPrefix(t, "[") {
			return "a list"
		}
		return "JSON text"
	case map[string]any:
		return "an object"
	case []any:
		return "a list"
	case string:
		return fmt.Sprintf("the string %q", x)
	}
	return fmt.Sprintf("a %T (%v)", v, v)
}
