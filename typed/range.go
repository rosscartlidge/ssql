package typed

import (
	"iter"
	"runtime"
)

// Range yields count values start, start+step, … built by mk — the
// serial form of the row generator behind `ssql from range N` (DFC154).
// mk turns the number into the row type (one int64 field named by the
// command's -field), so one runtime serves every column name.
// count <= 0 yields nothing; step may be negative.
func Range[T any](start, step, count int64, mk func(int64) T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for i := int64(0); i < count; i++ {
			if !yield(mk(start + i*step)) {
				return
			}
		}
	}
}

// RangeParallel is Range as a Stream[T]: the count is split across n
// shards (n <= 0 means GOMAXPROCS) and each shard counts its own
// interval — no slice is built and nothing is shared between shards. It
// is the first synthetic source in typed and the pattern for any other:
// a generator whose shards derive their rows from their own offsets, the
// way ReadCSVParallel's shards know their first row number.
func RangeParallel[T any](start, step, count int64, n int, mk func(int64) T) Stream[T] {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	if count <= 0 {
		return Stream[T]{shards: nil, n: 0}
	}
	if int64(n) > count {
		n = int(count)
	}
	shards := make([]iter.Seq[T], n)
	chunk := (count + int64(n) - 1) / int64(n) // round up
	for i := 0; i < n; i++ {
		lo := int64(i) * chunk
		if lo > count {
			// Round-up chunking can push later shards past the end
			// entirely (7 rows / 4 shards → chunk 2 → shard 3 starts at
			// 6, shard 4 at 8). Those shards are empty.
			lo = count
		}
		hi := lo + chunk
		if hi > count {
			hi = count
		}
		shards[i] = func(yield func(T) bool) {
			for j := lo; j < hi; j++ {
				if !yield(mk(start + j*step)) {
					return
				}
			}
		}
	}
	return Stream[T]{shards: shards, n: n}
}
