package typed

import (
	"bytes"
	"encoding/csv"
	"io"
	"iter"
	"os"
	"runtime"

	"github.com/rosscartlidge/ssql/v4/internal/mmap"
	"sync"
	"sync/atomic"
	"unsafe"
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
		if lo > len(data) {
			// Round-up chunking can push later shards past the end
			// entirely (e.g. 50 rows / 24 shards → chunkSize 3 →
			// shard 17 starts at 51). Those shards are empty.
			lo = len(data)
		}
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

// WriteCSV writes a Stream[T] to a CSV file, parallel-friendly: each
// shard formats its rows into its own bytes.Buffer concurrently, then
// the buffers are concatenated to the output in shard order.
//
// This is the parallel-aware sink that avoids the per-row fan-in
// channel cost that [Stream.Serial] + [WriteCSV] would pay. On the
// 10M-row, 7M-output workload the channel-based path measured
// ~2.5x slower than serial typed; this method restores the parallel
// win.
//
// **Trade:** peak memory is roughly 2x output-size (each shard buffers
// its slice in memory before dump). For huge outputs that don't fit
// in RAM, fall back to [Stream.Serial] + [WriteCSV] (slower but
// streaming) or use a smaller dataset.
//
// **Order:** rows from shard 0 come before shard 1 etc. Within a
// shard, input order is preserved. Between shards, original input
// order is NOT preserved (because Parallel/ParallelFromSlice
// partition by chunk).
func (s Stream[T]) WriteCSV(filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return s.WriteCSVToWriter(f)
}

// WriteCSVToWriter is the [io.Writer] variant of [Stream.WriteCSV].
func (s Stream[T]) WriteCSVToWriter(w io.Writer) error {
	schema, err := buildWriteSchema[T]()
	if err != nil {
		return err
	}

	// Write the header once, before the shards run.
	cw := csv.NewWriter(w)
	if err := cw.Write(schema.header); err != nil {
		return err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}

	// Each shard formats into its own buffer in parallel.
	buffers := make([]*bytes.Buffer, len(s.shards))
	errs := make([]error, len(s.shards))
	var wg sync.WaitGroup
	wg.Add(len(s.shards))
	for i, shard := range s.shards {
		i, shard := i, shard
		go func() {
			defer wg.Done()
			buf := &bytes.Buffer{}
			buffers[i] = buf
			scw := csv.NewWriter(buf)
			row := make([]string, len(schema.encoders))
			for v := range shard {
				p := unsafe.Pointer(&v)
				for j, enc := range schema.encoders {
					row[j] = enc(p)
				}
				if err := scw.Write(row); err != nil {
					errs[i] = err
					return
				}
			}
			scw.Flush()
			if err := scw.Error(); err != nil {
				errs[i] = err
			}
		}()
	}
	wg.Wait()

	// Sequential dump in shard order.
	for i, buf := range buffers {
		if errs[i] != nil {
			return errs[i]
		}
		if _, err := w.Write(buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// GroupByParallel implements the Sink/Combine/Finalize three-phase
// contract for parallel group-by-with-aggregation.
//
//  1. **Sink (per-shard, parallel):** each shard maintains its own
//     map[K]ParallelAggregator[T, S]; rows are folded in via Add()
//     with no cross-shard coordination.
//  2. **Combine (sequential):** the orchestrator walks the partial
//     maps in shard order, transferring new keys as-is and calling
//     Merge for keys already present. Cost is O(#shards × #groups),
//     not O(#rows) — negligible when #groups ≪ #rows.
//  3. **Finalize (lazy):** returns an iter.Seq[O] that yields one row
//     per distinct key in **shard-then-insertion** order, calling
//     build(k, agg.Result()) for each.
//
// Ordering: within a shard, first-seen-key order is preserved.
// Across shards, shard-0's first-seen keys come before shard-1's new
// first-seen keys, etc. Deterministic for a deterministic input.
// Not the same as serial typed.GroupBy (which sees keys in true
// input order); fall back to typed.GroupBy if you need that.
//
// **When this wins.** Workloads where #rows ≫ #groups
// (e.g. 10M rows × 1k dept_ids). The Add() phase dominates and
// parallelises cleanly; the Merge phase is small. When #groups is
// close to #rows, the Combine phase grows and the speedup shrinks
// — consider hash-partitioning the input first (future variant).
//
// See doc/research/typed-groupby-parallel-proposal.md for the full
// design rationale.
func GroupByParallel[T, S, O any, K comparable](
	in Stream[T],
	keyFn func(T) K,
	newAgg ParallelAggFunc[T, S],
	build func(K, S) O,
) iter.Seq[O] {
	nShards := len(in.shards)
	if nShards == 0 {
		return func(yield func(O) bool) {}
	}

	partials := make([]map[K]ParallelAggregator[T, S], nShards)
	keysPerShard := make([][]K, nShards)
	var wg sync.WaitGroup
	wg.Add(nShards)
	for i, shard := range in.shards {
		i, shard := i, shard
		go func() {
			defer wg.Done()
			m := make(map[K]ParallelAggregator[T, S])
			var keys []K
			for v := range shard {
				k := keyFn(v)
				agg, ok := m[k]
				if !ok {
					agg = newAgg()
					m[k] = agg
					keys = append(keys, k)
				}
				agg.Add(v)
			}
			partials[i] = m
			keysPerShard[i] = keys
		}()
	}
	wg.Wait()

	final := make(map[K]ParallelAggregator[T, S])
	var orderedKeys []K
	for i := 0; i < nShards; i++ {
		for _, k := range keysPerShard[i] {
			if existing, ok := final[k]; ok {
				existing.Merge(partials[i][k])
			} else {
				final[k] = partials[i][k]
				orderedKeys = append(orderedKeys, k)
			}
		}
	}

	return func(yield func(O) bool) {
		for _, k := range orderedKeys {
			if !yield(build(k, final[k].Result())) {
				return
			}
		}
	}
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

// ReadCSVParallel reads a CSV file using n worker goroutines. The
// file is partitioned by line count: each shard parses a contiguous
// range of data lines independently. Header is parsed once before
// shards start; the resulting schema is shared read-only.
//
// Implementation: reads the entire file into memory, scans for
// newline byte offsets (~200ms for a 600 MB file), partitions lines
// across n shards. Each shard wraps its byte range in its own
// bytes.NewReader / csv.NewReader and parses rows in parallel.
//
// LIMITATION: this PoC assumes no quoted fields with embedded
// newlines. Files produced by typed.WriteCSV satisfy this. Files
// from other producers may not — fall back to serial ReadCSV if
// you need RFC-4180-correct parsing of quoted multi-line fields.
//
// Memory cost: ~filesize bytes (the whole file is held in memory)
// plus ~8 bytes per line for the newline-offset index. For our
// 600 MB / 10M-row workload that's ~680 MB. Negligible compared to
// the saved CSV-read time.
func ReadCSVParallel[T any](filename string, n int) Stream[T] {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	// mmap the file instead of os.ReadFile: no kernel→user copy, no
	// file-sized heap allocation (1.7–1.9× faster slurp on a 1.23 GB CSV
	// — doc/research/mmap-readers-proposal.md). SAFE here because
	// encoding/csv COPIES field strings (ReuseRecord reuses only the
	// record slice), so yielded structs never alias the mapping; each
	// shard closure holds the *Mapped reachable while it reads. NB
	// ReadDelimParallel must NOT do this — its splitLineAlias strings
	// alias the buffer, which the GC cannot see into a mapping.
	m, err := mmap.Map(filename)
	if err != nil {
		return Stream[T]{shards: nil, n: 0}
	}
	data := m.Data
	if len(data) == 0 {
		return Stream[T]{shards: nil, n: 0}
	}

	// Find every newline byte offset using bytes.IndexByte (SIMD on
	// amd64). The first newline ends the header; subsequent newlines
	// end data rows.
	newlines := make([]int, 0, len(data)/64) // rough size hint
	for off := 0; off < len(data); {
		idx := bytes.IndexByte(data[off:], '\n')
		if idx < 0 {
			break
		}
		newlines = append(newlines, off+idx)
		off += idx + 1
	}
	if len(newlines) == 0 {
		return Stream[T]{shards: nil, n: 0}
	}

	// Parse the header once and build the shared decode schema.
	headerEnd := newlines[0]
	hr := csv.NewReader(bytes.NewReader(data[:headerEnd]))
	header, err := hr.Read()
	if err != nil {
		return Stream[T]{shards: nil, n: 0}
	}
	schema, err := buildReadSchema[T](header, false)
	if err != nil {
		return Stream[T]{shards: nil, n: 0}
	}

	// Determine number of data lines. Lines are bounded by newlines;
	// an unterminated last line counts as a data line too.
	numDataLines := len(newlines) - 1 // each non-last newline ends a data line
	if data[len(data)-1] != '\n' {
		numDataLines++ // unterminated last line is also data
	}
	if numDataLines == 0 {
		return Stream[T]{shards: nil, n: 0}
	}
	if n > numDataLines {
		n = numDataLines
	}

	// Partition data lines across n shards. Each shard owns a
	// contiguous byte range starting just after one newline and
	// ending just after another (or at EOF).
	linesPerShard := (numDataLines + n - 1) / n // round up

	shards := make([]iter.Seq[T], n)
	for i := 0; i < n; i++ {
		// Lines [startLine, endLine) belong to this shard.
		// Line indices are 0-based against data lines (0 = first
		// row after header); newlines[0] ends the header, so line k
		// starts at newlines[k]+1 and ends at newlines[k+1] (inclusive
		// of the trailing \n) or at len(data) for an unterminated last.
		startLine := i * linesPerShard
		endLine := startLine + linesPerShard
		if endLine > numDataLines {
			endLine = numDataLines
		}
		if startLine >= endLine {
			shards[i] = func(yield func(T) bool) {}
			continue
		}

		startByte := newlines[startLine] + 1
		var endByte int
		if endLine <= len(newlines)-1 {
			endByte = newlines[endLine] + 1
		} else {
			endByte = len(data)
		}
		chunk := data[startByte:endByte]

		shards[i] = func(yield func(T) bool) {
			// chunk points into mmap'd memory the GC cannot trace — the
			// KeepAlive pins the mapping until this shard finishes.
			defer runtime.KeepAlive(m)
			cr := csv.NewReader(bytes.NewReader(chunk))
			cr.ReuseRecord = true
			for {
				rec, err := cr.Read()
				if err != nil {
					return
				}
				var row T
				_ = schema.decode(unsafe.Pointer(&row), rec)
				if !yield(row) {
					return
				}
			}
		}
	}
	runtime.KeepAlive(m) // the setup above also read the mapping
	return Stream[T]{shards: shards, n: n}
}
