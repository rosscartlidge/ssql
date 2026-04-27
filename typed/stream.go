package typed

import (
	"iter"
	"runtime"
	"sync"
	"sync/atomic"
)

// Stream is the PoC type for the typed concurrency proposal — a
// parallel pipeline of T partitioned across n worker shards. Each
// shard is an `iter.Seq[T]` that, when consumed, pulls values from a
// shared work channel populated by the upstream source.
//
// Stream is deliberately a separate type from `iter.Seq[T]` because
// its execution model is different: there is no guaranteed
// input-order = output-order, and concurrent operations require a
// goroutine pool that the shape of `iter.Seq[T]` doesn't naturally
// express.
//
// **Status:** PoC. The Phase-1 parallel surface is intentionally
// minimal — Parallel, Serial (unordered), Stream.Where, HashJoinParallel.
// More operators will be added if the PoC's measured speedup justifies
// expanding the package.
type Stream[T any] struct {
	shards []iter.Seq[T]
	n      int
}

// Shards returns the number of parallel shards in the stream.
func (s Stream[T]) Shards() int { return s.n }

// Parallel converts a single iter.Seq[T] into a Stream[T] partitioned
// across n shards. n=0 means runtime.GOMAXPROCS(0).
//
// Implementation: a single distributor goroutine pulls from `in` and
// pushes to a shared buffered channel; each shard's iterator pops from
// that same channel. This is cooperative work-stealing rather than
// strict round-robin — workers that finish their current row faster
// pick up the next one immediately, so a slow worker doesn't stall
// the others.
//
// Buffer size is `n*64` rows per channel — enough to keep workers fed
// without bloating memory on small types or starving them on large
// types.
func Parallel[T any](in iter.Seq[T], n int) Stream[T] {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}

	work := make(chan T, n*64)
	go func() {
		for v := range in {
			work <- v
		}
		close(work)
	}()

	shards := make([]iter.Seq[T], n)
	for i := 0; i < n; i++ {
		shards[i] = func(yield func(T) bool) {
			for v := range work {
				if !yield(v) {
					return
				}
			}
		}
	}
	return Stream[T]{shards: shards, n: n}
}

// Serial collects a Stream back into a single iter.Seq[T]. By default
// the output is unordered: each shard runs in its own goroutine and
// values arrive on a fan-in channel in whatever order the workers
// produce them.
//
// SerialOrdered (not yet in the PoC) would preserve input-order at the
// cost of round-robin merging across shards, which forces head-of-line
// blocking on the slowest shard.
func (s Stream[T]) Serial() iter.Seq[T] {
	return func(yield func(T) bool) {
		out := make(chan T, s.n*64)
		var wg sync.WaitGroup
		wg.Add(len(s.shards))
		for _, shard := range s.shards {
			shard := shard
			go func() {
				defer wg.Done()
				for v := range shard {
					out <- v
				}
			}()
		}
		go func() {
			wg.Wait()
			close(out)
		}()
		for v := range out {
			if !yield(v) {
				// Drain remaining values from goroutines so they can
				// exit; otherwise the workers block on `out <- v`.
				go func() {
					for range out {
					}
				}()
				return
			}
		}
	}
}

// Where filters every shard independently — embarrassingly parallel.
// The predicate must be safe to call from multiple goroutines (i.e.
// pure or thread-safe).
func (s Stream[T]) Where(pred func(T) bool) Stream[T] {
	out := make([]iter.Seq[T], len(s.shards))
	for i, shard := range s.shards {
		shard := shard
		out[i] = Where(pred)(shard)
	}
	return Stream[T]{shards: out, n: s.n}
}

// StreamSelect projects each shard's items to a different output type.
// Free function rather than a method because Go generics don't allow
// methods to introduce new type parameters.
func StreamSelect[T, U any](s Stream[T], fn func(T) U) Stream[U] {
	out := make([]iter.Seq[U], len(s.shards))
	for i, shard := range s.shards {
		shard := shard
		out[i] = Select[T, U](fn)(shard)
	}
	return Stream[U]{shards: out, n: s.n}
}

// ParallelFromSlice partitions a materialized slice into n shards
// without any channel transit. Each shard iterates its own contiguous
// chunk of the slice, so workers never contend on a shared channel.
//
// This is the preferred entry point when the source is already in
// memory — channel-based [Parallel] adds ~100ns/row of overhead per
// stage, which dominates wall time on multi-stage pipelines (the
// PoC's first measurement was 3x SLOWER than serial because of this).
//
// For large CSV/JSONL sources, prefer to materialize into a slice
// first (or a future ReadCSVParallel that does its own byte-range
// partitioning).
func ParallelFromSlice[T any](data []T, n int) Stream[T] {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	if n > len(data) {
		n = len(data)
	}
	if n == 0 {
		return Stream[T]{shards: nil, n: 0}
	}
	shards := make([]iter.Seq[T], n)
	chunkSize := (len(data) + n - 1) / n // round up
	for i := 0; i < n; i++ {
		lo := i * chunkSize
		hi := lo + chunkSize
		if hi > len(data) {
			hi = len(data)
		}
		chunk := data[lo:hi]
		shards[i] = func(yield func(T) bool) {
			for _, v := range chunk {
				if !yield(v) {
					return
				}
			}
		}
	}
	return Stream[T]{shards: shards, n: n}
}

// SerialCount drains every shard concurrently and returns the total
// number of values produced. No channel transit on the output side —
// avoids the per-row fan-in cost of [Stream.Serial] when the caller
// only cares about an aggregate (sum, count, etc.). Useful for
// benchmarks and aggregations.
func (s Stream[T]) SerialCount() int64 {
	var total int64
	var wg sync.WaitGroup
	wg.Add(len(s.shards))
	for _, shard := range s.shards {
		shard := shard
		go func() {
			defer wg.Done()
			var local int64
			for range shard {
				local++
			}
			atomic.AddInt64(&total, local)
		}()
	}
	wg.Wait()
	return total
}

// HashJoinParallel is the morsel-driven hash join.
//
// Build phase: the right side is fully consumed by a single goroutine
// into a map[K]R. For tiny right-sides (lookup tables) this is fine.
// Larger right-sides would benefit from a partitioned variant
// (HashJoinParallelPartitioned in the proposal §4) — not in the PoC.
//
// Probe phase: every left shard runs against the shared read-only
// map in parallel. The map is safe to read from concurrent goroutines
// once the build phase has completed and the map is never written
// to again.
func HashJoinParallel[L, R, O any, K comparable](
	left Stream[L],
	right iter.Seq[R],
	leftKey func(L) K,
	rightKey func(R) K,
	merge func(L, R) O,
) Stream[O] {
	idx := make(map[K]R)
	for r := range right {
		idx[rightKey(r)] = r
	}
	out := make([]iter.Seq[O], len(left.shards))
	for i, shard := range left.shards {
		shard := shard
		out[i] = func(yield func(O) bool) {
			for l := range shard {
				if r, ok := idx[leftKey(l)]; ok {
					if !yield(merge(l, r)) {
						return
					}
				}
			}
		}
	}
	return Stream[O]{shards: out, n: left.n}
}
