package typed

import (
	"cmp"
	"iter"
	"runtime"
	"slices"

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
type WindowClause[T any] struct {
	Partition func(T) string // partition key ("" for the whole input)
	HasOrder  bool
	Compare   func(a, b T) int // order comparator (0 = peers); nil when !HasOrder
	Desc      bool             // the single order field is descending (RANGE direction)
	RangeKey  func(T) float64  // the single order field as a number / Unix seconds, for RANGE frames
	Frame     WindowFrame
	Specs     []WindowSpec[T]
}

// Window evaluates clauses over the input and builds one output per
// row; results are passed to build in clause order, then spec order.
func Window[T, O any](clauses []WindowClause[T], build func(T, []any) O) func(iter.Seq[T]) iter.Seq[O] {
	return func(in iter.Seq[T]) iter.Seq[O] {
		return func(yield func(O) bool) {
			var all []T
			for r := range in {
				all = append(all, r)
			}
			windowRows(all, clauses, build, yield)
		}
	}
}

// WindowParallel is Window over a Stream: the shards are materialised
// concurrently, each into its own slice, and concatenated in shard order
// — which is the input order, since every parallel source's shards are
// contiguous runs of it (ReadCSVParallel's byte ranges, the Parquet
// reader's row groups) — so the partitions' first-seen order and the
// output order are exactly Window's over the same rows. The window itself
// is a barrier (every row is held until its partition is known), so the
// output is an iter.Seq, as GroupByParallel's is. Before this form the
// window accepted only an iter.Seq and the planner read the source
// serially: 2.4 s of the 14.6 M-row benchmark window plus the growth of
// one slice by appends (DFC158 §4.3).
func WindowParallel[T, O any](clauses []WindowClause[T], build func(T, []any) O) func(Stream[T]) iter.Seq[O] {
	return func(in Stream[T]) iter.Seq[O] {
		return func(yield func(O) bool) {
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
			n := 0
			for _, p := range parts {
				n += len(p)
			}
			all := make([]T, 0, n)
			for _, p := range parts {
				all = append(all, p...)
			}
			windowRows(all, clauses, build, yield)
		}
	}
}

// windowRows is the window over the materialised rows, in input order.
func windowRows[T, O any](all []T, clauses []WindowClause[T], build func(T, []any) O, yield func(O) bool) {
	n := len(all)
	if n == 0 {
		return
	}
	total := 0
	for _, c := range clauses {
		total += len(c.Specs)
	}
	// One allocation for every row's results, not one per row.
	flat := make([]any, n*total)
	results := make([][]any, n)
	for i := range results {
		results[i] = flat[i*total : (i+1)*total : (i+1)*total]
	}
	var outputOrder []int
	orderDecided := false // the first clause with an ORDER BY fixes the output order
	base := 0
	for _, c := range clauses {
		partitions := make(map[string][]int)
		var partOrder []string
		for i, r := range all {
			k := ""
			if c.Partition != nil {
				k = c.Partition(r)
			}
			if _, seen := partitions[k]; !seen {
				partOrder = append(partOrder, k)
			}
			partitions[k] = append(partitions[k], i)
		}
		// Partitions are independent — each sorts its own index
		// slice and writes its own rows of results — so they run
		// concurrently, one goroutine per core; a panic in a row's
		// closure is re-raised here, as the readers' shards do it.
		// The output order is assembled afterwards, in first-seen
		// partition order.
		var g shardGroup
		sem := make(chan struct{}, runtime.GOMAXPROCS(0))
		for _, k := range partOrder {
			idx := partitions[k]
			sem <- struct{}{}
			g.Go(func() {
				defer func() { <-sem }()
				windowPartition(c, all, idx, results, base)
			})
		}
		g.Wait()
		if !orderDecided && c.HasOrder {
			for _, k := range partOrder {
				outputOrder = append(outputOrder, partitions[k]...)
			}
		}
		if c.HasOrder {
			orderDecided = true
		}
		base += len(c.Specs)
	}
	if outputOrder == nil {
		outputOrder = make([]int, n)
		for i := range outputOrder {
			outputOrder[i] = i
		}
	}
	for _, i := range outputOrder {
		if !yield(build(all[i], results[i])) {
			return
		}
	}
}

// windowPartition sorts one partition's index slice and fills its rows
// of results for every spec of the clause.
func windowPartition[T any](c WindowClause[T], all []T, idx []int, results [][]any, base int) {
	if c.HasOrder && c.Compare != nil {
		// A stable sort by pdqsort: the row's index is the tiebreak, so
		// peers keep input order exactly as sort.SliceStable kept it, with
		// O(n log n) comparisons and far fewer moves than its symmerge
		// (which was 21 s of the 35 s benchmark window, DFC158 §4.3) and
		// no reflection swapper.
		sortIdx(idx, func(a, b int) int {
			if r := c.Compare(all[a], all[b]); r != 0 {
				return r
			}
			return cmp.Compare(a, b)
		})
	}
	partLen := len(idx)
	done := make([]bool, len(c.Specs))
	for s, spec := range c.Specs {
		// A fold over the running frame: one pass, the state
		// carried across the partition (O(n), not O(n²)).
		if spec.Kind == WFold && isRunningFrame(c.Frame) {
			state := spec.Init()
			for pos := range idx {
				state = spec.Step(state, all[idx[pos]])
				results[idx[pos]][base+s] = spec.Final(state)
			}
			done[s] = true
			continue
		}
		// The aggregates and ranks over the running frame,
		// carried the same way; the whole-partition frame,
		// computed once (window_running.go).
		if vals := runningSpecValues(spec, c, all, idx); vals != nil {
			for pos := range idx {
				results[idx[pos]][base+s] = vals[pos]
			}
			done[s] = true
		}
	}
	for pos := range idx {
		for s, spec := range c.Specs {
			if done[s] {
				continue
			}
			results[idx[pos]][base+s] = windowValue(spec, c, all, idx, pos, partLen)
		}
	}
}

// sortChunkMin is the partition size below which sortIdx sorts inline.
const sortChunkMin = 1 << 16

// sortIdx sorts a partition's index slice by cmp, which is strict (the
// index is its tiebreak), so the result is the one stable order. A large
// partition is sorted as chunks on parallel goroutines and merged pairwise
// through a scratch buffer: a third of the benchmark file sat in ONE
// partition (4.7 M of 14.6 M rows), so sorting partitions concurrently
// still left that partition's single-threaded sort as the critical path
// (DFC158 §4.3).
func sortIdx(idx []int, cmp func(a, b int) int) {
	p := runtime.GOMAXPROCS(0)
	if len(idx) < 2*sortChunkMin || p < 2 {
		slices.SortFunc(idx, cmp)
		return
	}
	chunks := min(p, len(idx)/sortChunkMin)
	size := (len(idx) + chunks - 1) / chunks
	var bounds []int
	var g shardGroup
	for lo := 0; lo < len(idx); lo += size {
		hi := min(lo+size, len(idx))
		bounds = append(bounds, lo)
		g.Go(func() { slices.SortFunc(idx[lo:hi], cmp) })
	}
	bounds = append(bounds, len(idx))
	g.Wait()
	src, dst := idx, make([]int, len(idx))
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
			g.Go(func() { mergeIdx(dst[lo:hi], src[lo:mid], src[mid:hi], cmp) })
		}
		next = append(next, len(idx))
		g.Wait()
		bounds = next
		src, dst = dst, src
	}
	if &src[0] != &idx[0] {
		copy(idx, src)
	}
}

// mergeIdx merges two sorted runs into out (len(out) == len(a)+len(b)).
func mergeIdx(out, a, b []int, cmp func(a, b int) int) {
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
