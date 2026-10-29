package typed

import (
	"cmp"
	"iter"
	"runtime"
	"slices"
	"strings"
	"sync"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// Window is the typed runtime for `ssql window` (DFC130 unit 4). It
// mirrors ssql.Window's semantics exactly — partitions in first-seen
// order, each clause sorted by its own comparator, ROWS and RANGE frames,
// the ranking / offset / aggregate functions — over concrete row structs:
// the generated code supplies closures that read fields directly, and a
// build function that assembles the output struct from the row and its
// results. Materialised: every row is held until the partition is known,
// as ssql.Window does.
//
// Output order is ssql.Window's: the first clause that has an ORDER BY
// decides (its partitions in first-seen order, each sorted); with no
// ordered clause, input order.

// WindowFrame mirrors ssql.WindowFrame.
type WindowFrame struct {
	Preceding      int // rows (−1 = unbounded)
	Following      int
	Range          bool
	RangePreceding float64 // −1 = unbounded
	RangeFollowing float64
}

// WindowKind names a window function.
type WindowKind int

const (
	WRowNumber WindowKind = iota
	WRank
	WDenseRank
	WNtile
	WPercentRank
	WCumeDist
	WLag
	WLead
	WFirst
	WLast
	WNth
	WSum
	WAvg
	WCount
	WCountField
	WMin
	WMax
	WFold
)

// WindowSpec is one function over a clause's frame. Num reads a numeric
// field (sum/avg); Val reads any field (lag, lead, first, last, nth,
// count-field, min, max — min/max compare with ssql.CompareAny, as exec
// does). A missing value is ok=false / nil.
//
// WFold is `window -stream-expr`: Init makes a fresh state, Step folds one
// row into it and returns the state, Final reads the row's value from it
// (nil = absent). Over the running frame (UNBOUNDED PRECEDING → CURRENT
// ROW) the state carries across the partition in one pass; over any other
// frame the fold restarts at the frame's first row — ssql.WStreamExpr's
// arithmetic exactly. The generated Step mutates a pointer state and
// returns it, so a fold allocates once per frame.
type WindowSpec[T any] struct {
	Kind    WindowKind
	N       int // NTILE's n, LAG/LEAD's offset, NTH_VALUE's n
	Default any // LAG/LEAD default (nil = absent)
	Num     func(T) (float64, bool)
	Int     func(T) (int64, bool)        // set for an int64 field: the sum is exact, an Int128 (DFC155 C); avg divides the exact sum
	Wide    func(T) (ssql.Int128, bool)  // set for an int128 field, the same way
	Dec     func(T) (ssql.Decimal, bool) // set for a decimal field: the sum is a decimal at its scale (DFC155 E)
	Val     func(T) (any, bool)

	Init  func() any
	Step  func(state any, r T) any
	Final func(state any) any
}

// wide reads an integer field of either width as an Int128, for the
// exact sum (DFC155 C: every integer sum is an int128).
func (spec WindowSpec[T]) wide(r T) (ssql.Int128, bool) {
	if spec.Wide != nil {
		return spec.Wide(r)
	}
	v, ok := spec.Int(r)
	return ssql.Int128From(v), ok
}

// WindowClause mirrors ssql.WindowConfig for one clause.
//
// KeyString, KeyInt and KeyFloat are the single order field read as its
// own type, when the clause orders by one string, int64 or float64
// column (the generated code sets one of them beside Compare). A
// partition is then sorted as (key, index) pairs that move together —
// the sort's reads are sequential, the row is touched once to take its
// key — instead of comparing rows through Compare, which read two rows
// and two string bodies at random per comparison: 15 s of CPU in the
// 14.6 M-row benchmark window (DFC158 §3.9). Compare still decides
// peers, and sorts a clause with several order fields or another type.
type WindowClause[T any] struct {
	Partition func(T) string // partition key ("" for the whole input)
	HasOrder  bool
	Compare   func(a, b T) int // order comparator (0 = peers); nil when !HasOrder
	Desc      bool             // the single order field is descending (RANGE direction)
	RangeKey  func(T) float64  // the single order field as a number / Unix seconds, for RANGE frames
	KeyString func(T) string   // the single order field, when it is a string
	KeyInt    func(T) int64    // … an int64
	KeyFloat  func(T) float64  // … a float64
	Frame     WindowFrame
	Specs     []WindowSpec[T]
}

// Window evaluates clauses over the input and builds one output per
// row; results are passed to build in clause order, then spec order.
func Window[T, O any](clauses []WindowClause[T], build func(T, WindowRow) O) func(iter.Seq[T]) iter.Seq[O] {
	return func(in iter.Seq[T]) iter.Seq[O] {
		return func(yield func(O) bool) {
			var all []T
			for r := range in {
				all = append(all, r)
			}
			rows, cols := windowRows(all, clauses)
			for p := range rows {
				if !yield(build(rows[p], WindowRow{cols, p})) {
					return
				}
			}
		}
	}
}

// WindowParallel is Window over a Stream: the shards are materialised
// concurrently, each into its own slice, and concatenated in shard order
// — which is the input order, since every parallel source's shards are
// contiguous runs of it (ReadCSVParallel's byte ranges, the Parquet
// reader's row groups) — so the partitions' first-seen order and the
// output order are exactly Window's over the same rows. The window itself
// is a barrier (every row is held until its partition is known), and
// before 2026-10-29 its output was an iter.Seq: one core built every
// output row and fed a serial consumer — 1.6 s of the benchmark window,
// and the group-by after it ran its serial form. The output is now an
// ordered Stream whose shards are contiguous ranges of the window's
// output order, one per core: each shard builds its own rows, a
// group-by after the window runs its parallel form (its first-seen key
// order is unchanged, since the ranges concatenate to the output), a
// per-shard sink dumps the ranges in order, and Serial walks them in
// sequence for a serial consumer. The window runs once, on the first
// shard consumed. Before this form the window accepted only an iter.Seq
// and the planner read the source serially: 2.4 s of the benchmark
// window plus the growth of one slice by appends (DFC158 §4.3).
func WindowParallel[T, O any](clauses []WindowClause[T], build func(T, WindowRow) O) func(Stream[T]) Stream[O] {
	return func(in Stream[T]) Stream[O] {
		var (
			once    sync.Once
			rows    []T
			cols    []windowColumn
			failure any
		)
		prepare := func() {
			once.Do(func() {
				// A panic here (a row closure's) is captured so that every
				// shard re-raises it; sync.Once would otherwise hand the
				// later shards empty output.
				defer func() {
					if r := recover(); r != nil {
						failure = r
					}
				}()
				rows, cols = windowRows(materialise(in), clauses)
			})
			if failure != nil {
				panic(failure)
			}
		}
		r := runtime.GOMAXPROCS(0)
		shards := make([]iter.Seq[O], r)
		for i := range r {
			shards[i] = func(yield func(O) bool) {
				prepare()
				n := len(rows)
				for p := i * n / r; p < (i+1)*n/r; p++ {
					if !yield(build(rows[p], WindowRow{cols, p})) {
						return
					}
				}
			}
		}
		return Stream[O]{shards: shards, n: r, ordered: true}
	}
}

// materialise is the Stream's rows in shard order: each shard into its
// own slice concurrently, then copied into one.
func materialise[T any](in Stream[T]) []T {
	parts := make([][]T, len(in.shards))
	var g shardGroup
	for i, shard := range in.shards {
		g.Go(func() {
			var rows []T
			for r := range shard {
				rows = append(rows, r)
			}
			parts[i] = rows
		})
	}
	g.Wait()
	// The shards copy into their own ranges of one slice
	// concurrently: appending them one after another ran
	// 0.76 s of the benchmark window on one core (DFC158 §3.10).
	offs := make([]int, len(parts)+1)
	for i, p := range parts {
		offs[i+1] = offs[i] + len(p)
	}
	all := make([]T, offs[len(parts)])
	var c shardGroup
	for i, p := range parts {
		c.Go(func() {
			copy(all[offs[i]:], p)
			parts[i] = nil
		})
	}
	c.Wait()
	return all
}

// windowRows is the window over the materialised rows: the rows in the
// window's output order and, beside them, one column of results per
// spec in clause order then spec order, in that same order.
func windowRows[T any](all []T, clauses []WindowClause[T]) ([]T, []windowColumn) {
	n := len(all)
	if n == 0 {
		return nil, nil
	}
	var cols []windowColumn
	for _, c := range clauses {
		for _, spec := range c.Specs {
			cols = append(cols, newWindowColumn(spec, n))
		}
	}
	// The first clause with an ORDER BY decides the output order: its
	// partitions in first-seen order, each sorted. Its results are
	// written straight at their output positions (the partition's
	// offset plus the sorted position); any other clause's go through
	// outPos, the row's output position, built once the order is known.
	// Without an ordered clause the output order is the input order and
	// outPos is the identity (nil).
	deciding := -1
	for i, c := range clauses {
		if c.HasOrder {
			deciding = i
			break
		}
	}
	// The deciding clause runs first, so outPos exists for the others;
	// clauses are independent (each writes its own columns).
	order := make([]int, 0, len(clauses))
	if deciding >= 0 {
		order = append(order, deciding)
	}
	for ci := range clauses {
		if ci != deciding {
			order = append(order, ci)
		}
	}
	bases := make([]int, len(clauses))
	for ci := 1; ci < len(clauses); ci++ {
		bases[ci] = bases[ci-1] + len(clauses[ci-1].Specs)
	}
	var outputOrder []int
	var outPos []int32
	for _, ci := range order {
		c, base := clauses[ci], bases[ci]
		partitions, partOrder := partitionIndex(c, all)
		offsets := make(map[string]int, len(partOrder))
		if ci == deciding {
			off := 0
			for _, k := range partOrder {
				offsets[k] = off
				off += len(partitions[k])
			}
		}
		// Partitions are independent — each sorts its own index
		// slice and writes its own positions of the columns — so they
		// run concurrently, one goroutine per core; a panic in a row's
		// closure is re-raised here, as the readers' shards do it.
		var g shardGroup
		sem := make(chan struct{}, runtime.GOMAXPROCS(0))
		for _, k := range partOrder {
			idx := partitions[k]
			off := offsets[k]
			sem <- struct{}{}
			g.Go(func() {
				defer func() { <-sem }()
				if ci == deciding {
					windowPartition(c, all, idx, cols[base:base+len(c.Specs)], off, nil)
				} else {
					windowPartition(c, all, idx, cols[base:base+len(c.Specs)], -1, outPos)
				}
			})
		}
		g.Wait()
		if ci == deciding {
			outputOrder = make([]int, 0, n)
			for _, k := range partOrder {
				outputOrder = append(outputOrder, partitions[k]...)
			}
			if len(clauses) > 1 {
				outPos = make([]int32, n)
				parallelRanges(n, func(lo, hi int) {
					for p := lo; p < hi; p++ {
						outPos[outputOrder[p]] = int32(p)
					}
				})
			}
		}
	}
	if outputOrder == nil {
		return all, cols
	}
	// The rows gathered into output order on every core: read at random
	// from a slice of millions of rows, each row is a cache miss, and on
	// one core the emit took 3.7 s of the benchmark window (DFC158
	// §3.10). The results need no gather: the columns are in output
	// order already.
	ordered := make([]T, n)
	parallelRanges(n, func(lo, hi int) {
		for p := lo; p < hi; p++ {
			ordered[p] = all[outputOrder[p]]
		}
	})
	return ordered, cols
}

// rangeMin is the row count below which a parallel pass over the rows
// runs inline, and the fewest rows a range of one gets.
const rangeMin = 1 << 16

// parallelRanges runs fn over [0, n) as contiguous ranges, one goroutine
// per core, inline when n is small.
func parallelRanges(n int, fn func(lo, hi int)) {
	r := min(runtime.GOMAXPROCS(0), n/rangeMin)
	if r < 2 {
		fn(0, n)
		return
	}
	var g shardGroup
	for i := range r {
		g.Go(func() { fn(i*n/r, (i+1)*n/r) })
	}
	g.Wait()
}

// partitionIndex is the clause's partition map — each partition's row
// indices, ascending — and the partitions in first-seen order. The rows
// are keyed as ranges on every core, each range keeping its own map and
// first-seen order; the ranges merge in order, so a partition's first
// range is the first row that bears it, and its indices concatenate
// ascending — the one map a single pass builds, which ran 1.08 s of the
// benchmark window on one core (DFC158 §3.10). Each partition's slice is
// allocated at its exact length, and the ranges copy into it
// concurrently.
func partitionIndex[T any](c WindowClause[T], all []T) (map[string][]int, []string) {
	return partitionIndexRanges(c, all, min(runtime.GOMAXPROCS(0), len(all)/rangeMin))
}

// partitionIndexRanges is partitionIndex over r ranges (r < 2: one pass).
func partitionIndexRanges[T any](c WindowClause[T], all []T, r int) (map[string][]int, []string) {
	n := len(all)
	key := func(r T) string {
		if c.Partition == nil {
			return ""
		}
		return c.Partition(r)
	}
	if r < 2 {
		partitions := make(map[string][]int)
		var partOrder []string
		for i, row := range all {
			k := key(row)
			if _, seen := partitions[k]; !seen {
				partOrder = append(partOrder, k)
			}
			partitions[k] = append(partitions[k], i)
		}
		return partitions, partOrder
	}
	type rangeIndex struct {
		parts map[string][]int
		order []string
		start map[string]int // the partition's offset this range copies to
	}
	ranges := make([]rangeIndex, r)
	var g shardGroup
	for i := range r {
		g.Go(func() {
			lo, hi := i*n/r, (i+1)*n/r
			ri := rangeIndex{parts: make(map[string][]int), start: make(map[string]int)}
			for j := lo; j < hi; j++ {
				k := key(all[j])
				if _, seen := ri.parts[k]; !seen {
					ri.order = append(ri.order, k)
				}
				ri.parts[k] = append(ri.parts[k], j)
			}
			ranges[i] = ri
		})
	}
	g.Wait()
	total := make(map[string]int)
	var partOrder []string
	for _, ri := range ranges {
		for _, k := range ri.order {
			if _, seen := total[k]; !seen {
				partOrder = append(partOrder, k)
			}
			ri.start[k] = total[k]
			total[k] += len(ri.parts[k])
		}
	}
	partitions := make(map[string][]int, len(partOrder))
	for _, k := range partOrder {
		partitions[k] = make([]int, total[k])
	}
	var cp shardGroup
	for _, ri := range ranges {
		cp.Go(func() {
			for k, idx := range ri.parts {
				copy(partitions[k][ri.start[k]:], idx)
			}
		})
	}
	cp.Wait()
	return partitions, partOrder
}

// windowPartition sorts one partition's index slice and fills the
// clause's columns at the partition's rows: sorted position pos goes to
// output position off+pos when the clause decides the output order
// (off >= 0), else to the row's output position — outPos[idx[pos]], or
// idx[pos] itself when no clause orders the output.
func windowPartition[T any](c WindowClause[T], all []T, idx []int, cols []windowColumn, off int, outPos []int32) {
	if c.HasOrder && c.Compare != nil {
		// A stable sort by pdqsort: the row's index is the tiebreak, so
		// peers keep input order exactly as sort.SliceStable kept it, with
		// O(n log n) comparisons and far fewer moves than its symmerge
		// (which was 21 s of the 35 s benchmark window, DFC158 §4.3) and
		// no reflection swapper. A typed key sorts (key, index) pairs; the
		// comparator sorts the index by the rows.
		switch {
		case c.KeyString != nil:
			sortIdxByString(all, idx, c.KeyString, c.Desc)
		case c.KeyInt != nil:
			sortIdxByKey(all, idx, c.KeyInt, c.Desc, cmp.Compare[int64])
		case c.KeyFloat != nil:
			sortIdxByKey(all, idx, c.KeyFloat, c.Desc, compareFloatLikeGenerated)
		default:
			sortIdx(idx, func(a, b int) int {
				if r := c.Compare(all[a], all[b]); r != 0 {
					return r
				}
				return cmp.Compare(a, b)
			})
		}
	}
	partLen := len(idx)
	at := func(pos int) int { // the output position of sorted position pos
		switch {
		case off >= 0:
			return off + pos
		case outPos != nil:
			return int(outPos[idx[pos]])
		}
		return idx[pos]
	}
	done := make([]bool, len(c.Specs))
	for s, spec := range c.Specs {
		col := &cols[s]
		// A fold over the running frame: one pass, the state
		// carried across the partition (O(n), not O(n²)).
		if spec.Kind == WFold && isRunningFrame(c.Frame) {
			state := spec.Init()
			for pos := range idx {
				state = spec.Step(state, all[idx[pos]])
				col.set(at(pos), spec.Final(state))
			}
			done[s] = true
			continue
		}
		// The aggregates and ranks over the running frame,
		// carried the same way; the whole-partition frame,
		// computed once (window_running.go).
		if runningSpecValues(spec, c, all, idx, col, at) {
			done[s] = true
		}
	}
	for pos := range idx {
		for s, spec := range c.Specs {
			if done[s] {
				continue
			}
			cols[s].set(at(pos), windowValue(spec, c, all, idx, pos, partLen))
		}
	}
}

// sortChunkMin is the partition size below which sortChunked sorts inline.
const sortChunkMin = 1 << 16

// sortIdx sorts a partition's index slice by cmp, which is strict (the
// index is its tiebreak), so the result is the one stable order.
func sortIdx(idx []int, cmp func(a, b int) int) { sortChunked(idx, cmp) }

// keyed is one row of a partition sorted by a typed key: the key and the
// row's index travel together, so the sort reads the slice it is
// sorting, sequentially, and the row only once (to take the key).
type keyed[K any] struct {
	k K
	i int32
}

// sortIdxByKey sorts idx by key(row), descending when desc, ties by the
// row's index — the order Compare on the same field gives — through
// (key, index) pairs.
func sortIdxByKey[T, K any](all []T, idx []int, key func(T) K, desc bool, cmpKey func(a, b K) int) {
	pairs := make([]keyed[K], len(idx))
	for p, i := range idx {
		pairs[p] = keyed[K]{key(all[i]), int32(i)}
	}
	sortChunked(pairs, func(a, b keyed[K]) int {
		r := cmpKey(a.k, b.k)
		if desc {
			r = -r
		}
		if r != 0 {
			return r
		}
		return cmp.Compare(a.i, b.i)
	})
	for p := range pairs {
		idx[p] = int(pairs[p].i)
	}
}

// keyedStr is a string key with its first eight bytes as one big-endian
// integer beside it: two keys whose prefixes differ compare without
// reading either string's bytes, which sit at random across the heap —
// the benchmark window's remaining 11 s of CPU after the keyed sort
// (DFC158 §3.9). A shorter key is padded with zeros, which orders it
// before a longer key with the same bytes, as the full comparison does;
// equal prefixes fall to strings.Compare.
type keyedStr struct {
	pre uint64
	k   string
	i   int32
}

// strPrefix is s's first eight bytes as a big-endian integer.
func strPrefix(s string) uint64 {
	var p uint64
	for j := 0; j < 8 && j < len(s); j++ {
		p |= uint64(s[j]) << (56 - 8*j)
	}
	return p
}

// sortIdxByString is sortIdxByKey for a string key, through keyedStr.
func sortIdxByString[T any](all []T, idx []int, key func(T) string, desc bool) {
	pairs := make([]keyedStr, len(idx))
	for p, i := range idx {
		k := key(all[i])
		pairs[p] = keyedStr{strPrefix(k), k, int32(i)}
	}
	sortChunked(pairs, func(a, b keyedStr) int {
		var r int
		switch {
		case a.pre < b.pre:
			r = -1
		case a.pre > b.pre:
			r = 1
		default:
			r = strings.Compare(a.k, b.k)
		}
		if desc {
			r = -r
		}
		if r != 0 {
			return r
		}
		return cmp.Compare(a.i, b.i)
	})
	for p := range pairs {
		idx[p] = int(pairs[p].i)
	}
}

// compareFloatLikeGenerated is the three-way comparison the generated
// comparator makes on a float field (`<` then `>`): a NaN compares equal
// to everything, so a typed key sorts exactly as Compare did.
func compareFloatLikeGenerated(a, b float64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// sortChunked sorts items by cmp, which must be strict (no two items
// compare equal), so the result is the one order. A large slice is sorted
// as chunks on parallel goroutines and merged pairwise through a scratch
// buffer: a third of the benchmark file sat in ONE partition (4.7 M of
// 14.6 M rows), so sorting partitions concurrently still left that
// partition's single-threaded sort as the critical path (DFC158 §4.3).
func sortChunked[E any](items []E, cmp func(a, b E) int) {
	p := runtime.GOMAXPROCS(0)
	if len(items) < 2*sortChunkMin || p < 2 {
		slices.SortFunc(items, cmp)
		return
	}
	chunks := min(p, len(items)/sortChunkMin)
	size := (len(items) + chunks - 1) / chunks
	var bounds []int
	var g shardGroup
	for lo := 0; lo < len(items); lo += size {
		hi := min(lo+size, len(items))
		bounds = append(bounds, lo)
		g.Go(func() { slices.SortFunc(items[lo:hi], cmp) })
	}
	bounds = append(bounds, len(items))
	g.Wait()
	src, dst := items, make([]E, len(items))
	for len(bounds) > 2 {
		var g shardGroup
		next := make([]int, 0, len(bounds)/2+2)
		for i := 0; i+1 < len(bounds); i += 2 {
			lo, mid := bounds[i], bounds[i+1]
			hi := mid
			if i+2 < len(bounds) {
				hi = bounds[i+2]
			}
			next = append(next, lo)
			g.Go(func() { mergeSorted(dst[lo:hi], src[lo:mid], src[mid:hi], cmp) })
		}
		next = append(next, len(items))
		g.Wait()
		bounds = next
		src, dst = dst, src
	}
	if &src[0] != &items[0] {
		copy(items, src)
	}
}

// mergeSorted merges two sorted runs into out (len(out) == len(a)+len(b)).
func mergeSorted[E any](out, a, b []E, cmp func(a, b E) int) {
	i, j, k := 0, 0, 0
	for i < len(a) && j < len(b) {
		if cmp(a[i], b[j]) <= 0 {
			out[k] = a[i]
			i++
		} else {
			out[k] = b[j]
			j++
		}
		k++
	}
	k += copy(out[k:], a[i:])
	copy(out[k:], b[j:])
}

// windowValue computes one spec for the row at pos of the sorted
// partition idx — the same arithmetic as ssql's computeWindowFunc.
func windowValue[T any](spec WindowSpec[T], c WindowClause[T], all []T, idx []int, pos, partLen int) any {
	frame := c.Frame
	if frame.Range {
		start, end := rangeBounds(c, all, idx, pos, partLen)
		frame = WindowFrame{Preceding: pos - start, Following: end - pos}
	}
	fs := func() int { // frame start
		if frame.Preceding < 0 || pos-frame.Preceding < 0 {
			return 0
		}
		return pos - frame.Preceding
	}
	fe := func() int { // frame end (inclusive)
		if frame.Following < 0 || pos+frame.Following >= partLen {
			return partLen - 1
		}
		return pos + frame.Following
	}
	peers := func(a, b int) bool { return c.Compare == nil || c.Compare(all[idx[a]], all[idx[b]]) == 0 }
	switch spec.Kind {
	case WRowNumber:
		return int64(pos + 1)
	case WRank:
		rank := pos + 1
		for i := pos - 1; i >= 0 && peers(i, pos); i-- {
			rank = i + 1
		}
		return int64(rank)
	case WDenseRank:
		dr := 1
		for i := 1; i <= pos; i++ {
			if !peers(i-1, i) {
				dr++
			}
		}
		return int64(dr)
	case WNtile:
		if spec.N <= 0 {
			return int64(1)
		}
		return int64((pos*spec.N)/partLen) + 1
	case WPercentRank:
		if partLen <= 1 {
			return float64(0)
		}
		rank := pos + 1
		for i := pos - 1; i >= 0 && peers(i, pos); i-- {
			rank = i + 1
		}
		return float64(rank-1) / float64(partLen-1)
	case WCumeDist:
		last := pos
		for last+1 < partLen && peers(last+1, pos) {
			last++
		}
		return float64(last+1) / float64(partLen)
	case WLag, WLead:
		src := pos - spec.N
		if spec.Kind == WLead {
			src = pos + spec.N
		}
		if src < 0 || src >= partLen {
			return spec.Default
		}
		v, ok := spec.Val(all[idx[src]])
		if !ok || v == nil {
			return spec.Default
		}
		return v
	case WFirst:
		v, _ := spec.Val(all[idx[fs()]])
		return v
	case WLast:
		v, _ := spec.Val(all[idx[fe()]])
		return v
	case WNth:
		i := fs() + spec.N - 1
		if spec.N < 1 || i > fe() {
			return nil
		}
		v, _ := spec.Val(all[idx[i]])
		return v
	case WSum:
		if spec.Dec != nil {
			sum, have := ssql.Decimal{}, false
			for i := fs(); i <= fe(); i++ {
				if v, ok := spec.Dec(all[idx[i]]); ok {
					if !have {
						sum, have = v, true
					} else {
						sum = sum.Add(v)
					}
				}
			}
			return sum
		}
		if spec.Int != nil || spec.Wide != nil {
			var sum ssql.Int128
			for i := fs(); i <= fe(); i++ {
				if v, ok := spec.wide(all[idx[i]]); ok {
					sum = sum.Add(v)
				}
			}
			return sum
		}
		var sum ssql.CompensatedSum
		for i := fs(); i <= fe(); i++ {
			if v, ok := spec.Num(all[idx[i]]); ok {
				sum.Add(v)
			}
		}
		return sum.Value()
	case WAvg:
		cnt := 0
		if spec.Dec != nil {
			var sum ssql.Decimal
			for i := fs(); i <= fe(); i++ {
				if v, ok := spec.Dec(all[idx[i]]); ok {
					sum = sum.Add(v)
					cnt++
				}
			}
			if cnt == 0 {
				return nil
			}
			return sum.Float64() / float64(cnt)
		}
		if spec.Int != nil || spec.Wide != nil {
			var sum ssql.Int128
			for i := fs(); i <= fe(); i++ {
				if v, ok := spec.wide(all[idx[i]]); ok {
					sum = sum.Add(v)
					cnt++
				}
			}
			if cnt == 0 {
				return float64(0)
			}
			return sum.Float64() / float64(cnt)
		}
		var sum ssql.CompensatedSum
		for i := fs(); i <= fe(); i++ {
			if v, ok := spec.Num(all[idx[i]]); ok {
				sum.Add(v)
				cnt++
			}
		}
		if cnt == 0 {
			return float64(0)
		}
		return sum.Value() / float64(cnt)
	case WCount:
		return int64(fe() - fs() + 1)
	case WFold:
		state := spec.Init()
		for i := fs(); i <= fe(); i++ {
			state = spec.Step(state, all[idx[i]])
		}
		return spec.Final(state)
	case WCountField:
		var cnt int64
		for i := fs(); i <= fe(); i++ {
			if v, ok := spec.Val(all[idx[i]]); ok && v != nil {
				cnt++
			}
		}
		return cnt
	case WMin, WMax:
		var best any
		for i := fs(); i <= fe(); i++ {
			v, ok := spec.Val(all[idx[i]])
			if !ok || v == nil {
				continue
			}
			if best == nil {
				best = v
				continue
			}
			c := ssql.CompareAny(v, best)
			if (spec.Kind == WMin && c < 0) || (spec.Kind == WMax && c > 0) {
				best = v
			}
		}
		return best
	}
	return nil
}

// isRunningFrame is the default ROWS frame, UNBOUNDED PRECEDING → CURRENT
// ROW — the one frame a fold carries across rows.
func isRunningFrame(f WindowFrame) bool {
	return !f.Range && f.Preceding < 0 && f.Following == 0
}

// rangeBounds resolves a RANGE frame to [start, end] indices — the same
// scan as ssql's rangeFrameBounds.
func rangeBounds[T any](c WindowClause[T], all []T, idx []int, pos, partLen int) (int, int) {
	dir := 1.0
	if c.Desc {
		dir = -1
	}
	cur := c.RangeKey(all[idx[pos]])
	start, end := pos, pos
	for start > 0 {
		d := (cur - c.RangeKey(all[idx[start-1]])) * dir
		if d < 0 || (c.Frame.RangePreceding >= 0 && d > c.Frame.RangePreceding) {
			break
		}
		start--
	}
	for end+1 < partLen {
		d := (c.RangeKey(all[idx[end+1]]) - cur) * dir
		if d < 0 || (c.Frame.RangeFollowing >= 0 && d > c.Frame.RangeFollowing) {
			break
		}
		end++
	}
	return start, end
}
