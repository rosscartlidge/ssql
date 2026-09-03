package typed

import (
	"iter"
	"slices"
)

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

// TakeLast keeps the last n items in arrival order (ring buffer, O(n)
// memory). A barrier: nothing is yielded until the input ends. T -> T.
func TakeLast[T any](n int) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			if n <= 0 {
				return
			}
			ring := make([]T, 0, n)
			head := 0
			for v := range in {
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
// Inner-join semantics: a left row with no matching right row is dropped.
// If multiple right rows share a key, only the last is kept — use
// [HashJoinMulti] when you need many-to-many.
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

// HashJoinSized is [HashJoin] with a size hint for the build-side map.
// When the right side's row count is known (or even roughly known) at
// call time, passing it here avoids the rehash allocations that happen
// as the map grows. Pass 0 to fall back to default growth.
//
// Useful when joining a streaming left side against a slice on the
// right: pass len(rightSlice) and the map is sized exactly once.
func HashJoinSized[L, R, O any, K comparable](
	left iter.Seq[L],
	right iter.Seq[R],
	rightSizeHint int,
	leftKey func(L) K,
	rightKey func(R) K,
	merge func(L, R) O,
) iter.Seq[O] {
	idx := make(map[K]R, rightSizeHint)
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

// HashJoinMulti is the many-to-many variant of [HashJoin]: every left
// row with N matches in the right side produces N output rows. Right
// values are stored in a map[K][]R, so memory cost scales with the
// total right-side size rather than the number of unique keys.
func HashJoinMulti[L, R, O any, K comparable](
	left iter.Seq[L],
	right iter.Seq[R],
	leftKey func(L) K,
	rightKey func(R) K,
	merge func(L, R) O,
) iter.Seq[O] {
	idx := make(map[K][]R)
	for r := range right {
		k := rightKey(r)
		idx[k] = append(idx[k], r)
	}
	return func(yield func(O) bool) {
		for l := range left {
			for _, r := range idx[leftKey(l)] {
				if !yield(merge(l, r)) {
					return
				}
			}
		}
	}
}

// LeftJoin keeps every left row. Left rows with no matching right row
// are emitted with the right side absent — the caller's merge function
// receives a zero R and a found=false flag so it can populate the
// output with nulls / defaults / pointer fields as needed.
//
// Example:
//
//	merge := func(l Order, r Customer, found bool) Joined {
//	    var name string
//	    if found {
//	        name = r.Name
//	    }
//	    return Joined{OrderID: l.ID, CustomerName: name}
//	}
func LeftJoin[L, R, O any, K comparable](
	left iter.Seq[L],
	right iter.Seq[R],
	leftKey func(L) K,
	rightKey func(R) K,
	merge func(l L, r R, found bool) O,
) iter.Seq[O] {
	idx := make(map[K]R)
	for r := range right {
		idx[rightKey(r)] = r
	}
	return func(yield func(O) bool) {
		for l := range left {
			r, ok := idx[leftKey(l)]
			if !yield(merge(l, r, ok)) {
				return
			}
		}
	}
}

// RightJoin is the symmetric mirror of [LeftJoin]: every right row is
// emitted, with left rows attached when their keys match.
//
// Implementation note: the left side is fully materialized into a map
// keyed by leftKey, then the right side streams. If you actually want
// one row per left input, with right when matched, use [LeftJoin] —
// it's cheaper because the typically-larger left side stays streaming.
func RightJoin[L, R, O any, K comparable](
	left iter.Seq[L],
	right iter.Seq[R],
	leftKey func(L) K,
	rightKey func(R) K,
	merge func(l L, r R, found bool) O,
) iter.Seq[O] {
	idx := make(map[K]L)
	for l := range left {
		idx[leftKey(l)] = l
	}
	return func(yield func(O) bool) {
		for r := range right {
			l, ok := idx[rightKey(r)]
			if !yield(merge(l, r, ok)) {
				return
			}
		}
	}
}

// FullJoin emits every left row (matched or not) followed by every
// unmatched right row. Both sides are materialized.
//
// merge receives (l, r, leftFound, rightFound). For left-only rows:
// leftFound=true, rightFound=false (r is zero). For right-only rows:
// leftFound=false, rightFound=true (l is zero). For matched rows: both
// true. (false/false never occurs.)
func FullJoin[L, R, O any, K comparable](
	left iter.Seq[L],
	right iter.Seq[R],
	leftKey func(L) K,
	rightKey func(R) K,
	merge func(l L, r R, leftFound, rightFound bool) O,
) iter.Seq[O] {
	rightIdx := make(map[K]R)
	for r := range right {
		rightIdx[rightKey(r)] = r
	}
	return func(yield func(O) bool) {
		seen := make(map[K]struct{}, len(rightIdx))
		var zeroL L
		var zeroR R
		for l := range left {
			k := leftKey(l)
			if r, ok := rightIdx[k]; ok {
				seen[k] = struct{}{}
				if !yield(merge(l, r, true, true)) {
					return
				}
			} else {
				if !yield(merge(l, zeroR, true, false)) {
					return
				}
			}
		}
		for k, r := range rightIdx {
			if _, ok := seen[k]; ok {
				continue
			}
			if !yield(merge(zeroL, r, false, true)) {
				return
			}
		}
	}
}

// SortBy collects the input into a slice, sorts it ascending by the
// extracted key using slices.SortFunc, then yields. Materializes the
// full input — O(N) memory.
//
// For descending order use [SortByDesc]. Stable sort isn't guaranteed
// (slices.SortFunc is not stable); use SortByStable when you need
// determinism for ties.
func SortBy[T any, K Ordered](key func(T) K) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			buf := slices.Collect(in)
			slices.SortFunc(buf, func(a, b T) int {
				ka, kb := key(a), key(b)
				switch {
				case ka < kb:
					return -1
				case ka > kb:
					return 1
				default:
					return 0
				}
			})
			for _, v := range buf {
				if !yield(v) {
					return
				}
			}
		}
	}
}

// SortByDesc is [SortBy] in descending order.
func SortByDesc[T any, K Ordered](key func(T) K) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			buf := slices.Collect(in)
			slices.SortFunc(buf, func(a, b T) int {
				ka, kb := key(a), key(b)
				switch {
				case ka > kb:
					return -1
				case ka < kb:
					return 1
				default:
					return 0
				}
			})
			for _, v := range buf {
				if !yield(v) {
					return
				}
			}
		}
	}
}

// SortByFunc collects the input into a slice and sorts it using the
// caller-supplied comparator. Use this for multi-key sorts where the
// comparator combines multiple fields (e.g. "by region asc, then
// revenue desc"). Materializes the full input — O(N) memory.
func SortByFunc[T any](cmp func(a, b T) int) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			buf := slices.Collect(in)
			slices.SortFunc(buf, cmp)
			for _, v := range buf {
				if !yield(v) {
					return
				}
			}
		}
	}
}

// SortByStable is the stable variant of [SortBy] — preserves original
// order for elements with equal keys. Slightly slower; use only when
// stability matters.
func SortByStable[T any, K Ordered](key func(T) K) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			buf := slices.Collect(in)
			slices.SortStableFunc(buf, func(a, b T) int {
				ka, kb := key(a), key(b)
				switch {
				case ka < kb:
					return -1
				case ka > kb:
					return 1
				default:
					return 0
				}
			})
			for _, v := range buf {
				if !yield(v) {
					return
				}
			}
		}
	}
}

// Distinct yields only the first occurrence of each unique key. Streams
// in O(distinct-keys) memory — does not materialize the whole input.
//
// For "distinct entire row" use Distinct with key = the value itself
// (only valid when T is comparable):
//
//	distinct := typed.Distinct(func(r Row) Row { return r })(rows)
//
// For multi-key uniqueness, return a tuple struct from key.
func Distinct[T any, K comparable](key func(T) K) func(iter.Seq[T]) iter.Seq[T] {
	return func(in iter.Seq[T]) iter.Seq[T] {
		return func(yield func(T) bool) {
			seen := make(map[K]struct{})
			for v := range in {
				k := key(v)
				if _, ok := seen[k]; ok {
					continue
				}
				seen[k] = struct{}{}
				if !yield(v) {
					return
				}
			}
		}
	}
}

// Concat yields every element from each input sequence in turn. No
// materialization — pure streaming. Inputs are consumed in order.
//
// Useful for unioning streams of the same type without deduplication.
// For dedup-on-concat use [Union].
func Concat[T any](seqs ...iter.Seq[T]) iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, s := range seqs {
			for v := range s {
				if !yield(v) {
					return
				}
			}
		}
	}
}

// Union concatenates the input sequences and yields only the first
// occurrence of each key — equivalent to Concat followed by Distinct,
// but in a single pass.
func Union[T any, K comparable](key func(T) K, seqs ...iter.Seq[T]) iter.Seq[T] {
	return func(yield func(T) bool) {
		seen := make(map[K]struct{})
		for _, s := range seqs {
			for v := range s {
				k := key(v)
				if _, ok := seen[k]; ok {
					continue
				}
				seen[k] = struct{}{}
				if !yield(v) {
					return
				}
			}
		}
	}
}
