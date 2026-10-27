package typed

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"iter"
	"os"
	"runtime"
	"strings"
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
	// The feeder runs the source; a fail-fast reader panic there must
	// reach the consumer, so it is captured and re-raised by every
	// shard once the channel drains.
	var feeder shardGroup
	feeder.Go(func() {
		defer close(work)
		for v := range in {
			work <- v
		}
	})

	shards := make([]iter.Seq[T], n)
	for i := 0; i < n; i++ {
		shards[i] = func(yield func(T) bool) {
			for v := range work {
				if !yield(v) {
					return
				}
			}
			feeder.rethrow()
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
		var g shardGroup
		for _, shard := range s.shards {
			shard := shard
			g.Go(func() {
				for v := range shard {
					out <- v
				}
			})
		}
		go func() {
			g.waitQuiet()
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
		// Every shard has finished (out is closed): surface a reader's
		// fail-fast panic here, in the consumer's goroutine.
		g.rethrow()
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
	var g shardGroup
	for _, shard := range s.shards {
		shard := shard
		g.Go(func() {
			var local int64
			for range shard {
				local++
			}
			atomic.AddInt64(&total, local)
		})
	}
	g.Wait()
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
func (s Stream[T]) WriteCSV(filename string, opts ...WriteOption) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return s.WriteCSVToWriter(f, opts...)
}

// WriteCSVToWriter is the [io.Writer] variant of [Stream.WriteCSV].
func (s Stream[T]) WriteCSVToWriter(w io.Writer, opts ...WriteOption) error {
	schema, err := buildWriteSchema[T](opts...)
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
	var g shardGroup
	for i, shard := range s.shards {
		i, shard := i, shard
		g.Go(func() {
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
		})
	}
	g.Wait()

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
	var g shardGroup
	for i, shard := range in.shards {
		i, shard := i, shard
		g.Go(func() {
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
		})
	}
	g.Wait()

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

// HashJoinMultiParallel is [HashJoinParallel] with [HashJoinMulti]'s
// many-to-many semantics: every left row produces one output row per
// matching right row. The CLI's `join` emits this form — a right side
// with a repeated key (orders per customer) is the ordinary case, and
// the single-match form silently kept only the last order per customer
// (found by the join_left_type equivalence case, 2026-09-27).
func HashJoinMultiParallel[L, R, O any, K comparable](
	left Stream[L],
	right iter.Seq[R],
	leftKey func(L) K,
	rightKey func(R) K,
	merge func(L, R) O,
) Stream[O] {
	idx := make(map[K][]R)
	for r := range right {
		k := rightKey(r)
		idx[k] = append(idx[k], r)
	}
	out := make([]iter.Seq[O], len(left.shards))
	for i, shard := range left.shards {
		shard := shard
		out[i] = func(yield func(O) bool) {
			for l := range shard {
				for _, r := range idx[leftKey(l)] {
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
func ReadCSVParallel[T any](filename string, n int, opts ...CSVOption) Stream[T] {
	o := resolveOpts(opts)
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
	const op = "typed.ReadCSVParallel"
	m, err := mapInput(filename)
	if err != nil {
		failRead(op, filename, err)
	}
	data := m.Data
	if len(data) == 0 {
		return Stream[T]{shards: nil, n: 0}
	}

	// The header ends at the first newline; the shards are byte ranges
	// of the rest, each cut at a newline, and each shard finds its own
	// lines with bytes.IndexByte (SIMD on amd64). Until 2026-10-27 the
	// reader indexed every newline of the file first — one serial pass
	// over the whole file into a 117 MB slice for 14.6 M rows, half the
	// wall time of a one-column read once the scan itself was parallel
	// and allocation-free (csv_scan.go). The boundary search is n
	// IndexByte calls; a shard's row number, needed only for an error,
	// is counted when the error happens (rowAt).
	headerEnd := bytes.IndexByte(data, '\n')
	if headerEnd < 0 {
		headerEnd = len(data) // a header with no rows
	}
	hr := csv.NewReader(bytes.NewReader(data[:headerEnd]))
	header, err := hr.Read()
	if err != nil {
		failRead(op, filename, fmt.Errorf("read header: %w", err))
	}
	schema, err := buildReadSchema[T](header, o.strict, o.timeFormats, o.emptyZero)
	if err != nil {
		failRead(op, filename, err)
	}
	body := headerEnd + 1
	if body >= len(data) {
		return Stream[T]{shards: nil, n: 0}
	}
	// Split points: every n-th of the body, moved forward to just past
	// the next newline so no line straddles two shards. A tiny file
	// leaves later shards empty.
	bounds := make([]int, n+1)
	bounds[0] = body
	for i := 1; i < n; i++ {
		p := body + (len(data)-body)*i/n
		if p <= bounds[i-1] {
			bounds[i] = bounds[i-1]
			continue
		}
		if nl := bytes.IndexByte(data[p:], '\n'); nl >= 0 {
			bounds[i] = p + nl + 1
		} else {
			bounds[i] = len(data)
		}
	}
	bounds[n] = len(data)
	// rowAt is the 1-based data row that starts at byte lineStart: the
	// newlines before it, less the header's.
	rowAt := func(lineStart int) int64 {
		return int64(bytes.Count(data[:lineStart], []byte{'\n'}))
	}

	shards := make([]iter.Seq[T], n)
	for i := 0; i < n; i++ {
		chunk := data[bounds[i]:bounds[i+1]]
		chunkStart := bounds[i]
		if len(chunk) == 0 {
			shards[i] = func(yield func(T) bool) {}
			continue
		}
		shards[i] = func(yield func(T) bool) {
			// The lines point into mmap'd memory the GC cannot trace — the
			// KeepAlive pins the mapping until this shard finishes.
			defer runtime.KeepAlive(m)
			// The zero-copy scanner (csv_scan.go): the line is split into
			// views of the mapping (quoted fields included), the kept
			// string columns copied; a malformed line goes through
			// encoding/csv for its error. The field count is fixed by the
			// shard's first line, as encoding/csv fixes it by the first
			// record it reads.
			fields := make([]string, 0, len(schema.decoders)+4)
			var scratch []byte
			want := -1
			for pos := 0; pos < len(chunk); {
				lineStart := pos
				var line []byte
				if nl := bytes.IndexByte(chunk[pos:], '\n'); nl >= 0 {
					line = chunk[pos : pos+nl]
					pos += nl + 1
				} else {
					line = chunk[pos:]
					pos = len(chunk)
				}
				if n := len(line); n > 0 && line[n-1] == '\r' {
					line = line[:n-1]
				}
				if len(line) == 0 {
					continue // encoding/csv skips a blank line
				}
				rec, ok := splitCSVLine(line, fields, &scratch)
				if ok {
					for _, ci := range schema.copyCols {
						if ci < len(rec) {
							rec[ci] = strings.Clone(rec[ci])
						}
					}
				} else {
					cr := csv.NewReader(bytes.NewReader(line))
					r, err := cr.Read()
					if err != nil {
						failRow(op, filename, rowAt(chunkStart+lineStart), err)
					}
					rec = r
				}
				if want < 0 {
					want = len(rec)
				} else if len(rec) != want {
					failRow(op, filename, rowAt(chunkStart+lineStart), csv.ErrFieldCount)
				}
				var row T
				if err := schema.decode(unsafe.Pointer(&row), rec); err != nil {
					failRow(op, filename, rowAt(chunkStart+lineStart), err)
				}
				if !yield(row) {
					return
				}
			}
		}
	}
	runtime.KeepAlive(m) // the setup above also read the mapping
	return Stream[T]{shards: shards, n: n}
}

// parallelBatchSize is the number of rows ParallelBatched hands a shard
// per channel transit. At 1024 the ~100 ns send+receive pair that made
// per-row Parallel 3× slower than serial (claude/concurrency.md §1)
// costs about 0.1 ns a row.
const parallelBatchSize = 1024

// ParallelBatched converts an iter.Seq[T] the caller owns into a
// Stream[T] without a channel transit per row: the feeder pulls rows
// into batches of parallelBatchSize and sends each batch once; every
// shard takes whole batches (cooperative, like [Parallel]) and iterates
// them in pure stack code. It is the entry point for a generated
// library function (`generate go -package`), whose input is a sequence
// rather than a file [ReadCSVParallel] could shard by byte range, and
// it streams — memory is bounded by n×2 batches, unlike
// [FromRecordsParallel], which materialises the whole input. n=0 means
// runtime.GOMAXPROCS(0).
//
// A fail-fast panic in the source is captured in the feeder and
// re-raised by every shard once the batches drain, so it reaches the
// consumer's recover. A consumer that stops early releases the feeder.
func ParallelBatched[T any](in iter.Seq[T], n int) Stream[T] {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	work := make(chan []T, n*2)
	stop := make(chan struct{})
	var stopOnce sync.Once
	// Batches are recycled: a shard hands its emptied batch back for
	// the feeder to refill. Without this every batch is a fresh
	// ~100 KB large-object allocation (zeroed on allocation, collected
	// soon after) — 10 M rows cost 560 MB of allocation and most of
	// the distributor's time went to the allocator and the GC.
	pool := sync.Pool{New: func() any {
		b := make([]T, 0, parallelBatchSize)
		return &b
	}}
	var feeder shardGroup
	feeder.Go(func() {
		defer close(work)
		batch := (*pool.Get().(*[]T))[:0]
		flush := func() bool {
			if len(batch) == 0 {
				return true
			}
			select {
			case work <- batch:
				batch = (*pool.Get().(*[]T))[:0]
				return true
			case <-stop:
				return false
			}
		}
		for v := range in {
			batch = append(batch, v)
			if len(batch) == parallelBatchSize && !flush() {
				return
			}
		}
		flush()
	})
	shards := make([]iter.Seq[T], n)
	for i := 0; i < n; i++ {
		shards[i] = func(yield func(T) bool) {
			for batch := range work {
				for _, v := range batch {
					if !yield(v) {
						stopOnce.Do(func() { close(stop) })
						return
					}
				}
				clear(batch) // drop references held by the rows
				b := batch[:0]
				pool.Put(&b)
			}
			feeder.rethrow()
		}
	}
	return Stream[T]{shards: shards, n: n}
}
