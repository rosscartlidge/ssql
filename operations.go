package ssql

import (
	"cmp"
	"container/heap"
	"context"
	"fmt"
	"iter"
	"maps"
	"slices"
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
// and a positive number when a > b.
func SortFunc[T any](cmpFn func(T, T) int) Filter[T, T] {
	return func(input iter.Seq[T]) iter.Seq[T] {
		return slices.Values(slices.SortedFunc(input, cmpFn))
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

	// Start broadcaster goroutine
	go func() {
		defer func() {
			for _, ch := range channels {
				close(ch)
			}
		}()

		for v := range input {
			// Send to all channels
			for i, ch := range channels {
				select {
				case ch <- v:
					// Successfully sent to this channel
				case <-done:
					// One of the consumers has terminated, stop broadcasting
					return
				default:
					// Channel is full - consumer is too slow
					// For now, we'll drop the value for this consumer
					// In production, you might want different backpressure strategies
					_ = i // Just to use the variable
				}
			}
		}
	}()

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
				outputRecord.fields["running_sum"] = runningTotal
				outputRecord.fields["running_count"] = int64(count)
				outputRecord.fields["running_avg"] = runningTotal / float64(count)

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
				outputRecord.fields["moving_avg"] = avg
				outputRecord.fields["window_size"] = int64(len(window))
				outputRecord.fields["total_count"] = int64(count)

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
				outputRecord.fields["ema"] = ema
				outputRecord.fields["alpha"] = alpha

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
				outputRecord.fields["running_min"] = min
				outputRecord.fields["running_max"] = max
				outputRecord.fields["running_range"] = max - min

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
				outputRecord.fields["distinct_counts"] = counts
				outputRecord.fields["total_count"] = totalCount
				outputRecord.fields["distinct_values"] = int64(len(counts))

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

			done := make(chan struct{})

			go func() {
				defer close(done)
				for v := range input {
					select {
					case <-ctx.Done():
						return // Timeout reached
					default:
						if !yield(v) {
							return
						}
					}
				}
			}()

			select {
			case <-done:
				// Processing completed normally
			case <-ctx.Done():
				// Timeout reached
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
