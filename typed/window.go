package typed

import (
	"iter"
	"sort"

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
)

// WindowSpec is one function over a clause's frame. Num reads a numeric
// field (sum/avg); Val reads any field (lag, lead, first, last, nth,
// count-field, min, max — min/max compare with ssql.CompareAny, as exec
// does). A missing value is ok=false / nil.
type WindowSpec[T any] struct {
	Kind    WindowKind
	N       int // NTILE's n, LAG/LEAD's offset, NTH_VALUE's n
	Default any // LAG/LEAD default (nil = absent)
	Num     func(T) (float64, bool)
	Val     func(T) (any, bool)
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
			n := len(all)
			if n == 0 {
				return
			}
			total := 0
			for _, c := range clauses {
				total += len(c.Specs)
			}
			results := make([][]any, n)
			for i := range results {
				results[i] = make([]any, total)
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
				for _, k := range partOrder {
					idx := partitions[k]
					if c.HasOrder && c.Compare != nil {
						sort.SliceStable(idx, func(a, b int) bool { return c.Compare(all[idx[a]], all[idx[b]]) < 0 })
					}
					if !orderDecided && c.HasOrder {
						outputOrder = append(outputOrder, idx...)
					}
					partLen := len(idx)
					for pos := range idx {
						for s, spec := range c.Specs {
							results[idx[pos]][base+s] = windowValue(spec, c, all, idx, pos, partLen)
						}
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
	}
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
		var sum ssql.CompensatedSum
		for i := fs(); i <= fe(); i++ {
			if v, ok := spec.Num(all[idx[i]]); ok {
				sum.Add(v)
			}
		}
		return sum.Value()
	case WAvg:
		var sum ssql.CompensatedSum
		cnt := 0
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
