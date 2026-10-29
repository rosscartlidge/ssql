package typed

import "github.com/rosscartlidge/ssql/v4"

// The one-pass window aggregates (2026-10-27), the typed twin of the root
// package's window_running.go: windowValue scans the row's frame, so the
// default frame made a partition of n rows n²/2 work (a running sum over
// 560 k rows was still running at a 30-minute cap, DFC157 §4). Over the
// running frame the state is carried across the sorted partition, built
// by the same operations in the same order as the per-row scan; over the
// whole-partition frame the frame aggregates are computed once. A bounded
// ROWS frame, a RANGE frame, lag and lead stay per row.

// isWholeFrame is the whole-partition frame: UNBOUNDED on both sides.
func isWholeFrame(f WindowFrame) bool {
	return !f.Range && f.Preceding < 0 && f.Following < 0
}

// runningSpecValues writes one value per position of the partition into
// the spec's column — at(pos) is the output position of sorted position
// pos — for a spec whose frame allows it, and reports whether it did;
// false means the spec stays per row.
func runningSpecValues[T any](spec WindowSpec[T], c WindowClause[T], all []T, idx []int, col *windowColumn, at func(int) int) bool {
	partLen := len(idx)
	switch {
	case isWholeFrame(c.Frame):
		switch spec.Kind {
		case WSum, WAvg, WCount, WCountField, WMin, WMax, WFold, WFirst, WLast, WNth, WAgg:
			v := windowValue(spec, c, all, idx, 0, partLen)
			for pos := range idx {
				col.set(at(pos), v)
			}
			return true
		}
		return false
	case !isRunningFrame(c.Frame):
		return false
	}
	peers := func(a, b int) bool { return c.Compare == nil || c.Compare(all[idx[a]], all[idx[b]]) == 0 }
	switch spec.Kind {
	case WSum:
		switch {
		case spec.Dec != nil:
			sum, have := ssql.Decimal{}, false
			for pos := range idx {
				if v, ok := spec.Dec(all[idx[pos]]); ok {
					if !have {
						sum, have = v, true
					} else {
						sum = sum.Add(v)
					}
				}
				col.anys[at(pos)] = sum
			}
		case spec.Int != nil || spec.Wide != nil:
			var sum ssql.Int128
			for pos := range idx {
				if v, ok := spec.wide(all[idx[pos]]); ok {
					sum = sum.Add(v)
				}
				col.wides[at(pos)] = sum
			}
		default:
			var sum ssql.CompensatedSum
			for pos := range idx {
				if v, ok := spec.Num(all[idx[pos]]); ok {
					sum.Add(v)
				}
				col.floats[at(pos)] = sum.Value()
			}
		}
	case WAvg:
		cnt := 0
		switch {
		case spec.Dec != nil:
			var sum ssql.Decimal
			for pos := range idx {
				if v, ok := spec.Dec(all[idx[pos]]); ok {
					sum = sum.Add(v)
					cnt++
				}
				if cnt == 0 {
					col.setAbsent(at(pos))
				} else {
					col.floats[at(pos)] = sum.Float64() / float64(cnt)
				}
			}
		case spec.Int != nil || spec.Wide != nil:
			var sum ssql.Int128
			for pos := range idx {
				if v, ok := spec.wide(all[idx[pos]]); ok {
					sum = sum.Add(v)
					cnt++
				}
				if cnt == 0 {
					col.floats[at(pos)] = 0
				} else {
					col.floats[at(pos)] = sum.Float64() / float64(cnt)
				}
			}
		default:
			var sum ssql.CompensatedSum
			for pos := range idx {
				if v, ok := spec.Num(all[idx[pos]]); ok {
					sum.Add(v)
					cnt++
				}
				if cnt == 0 {
					col.floats[at(pos)] = 0
				} else {
					col.floats[at(pos)] = sum.Value() / float64(cnt)
				}
			}
		}
	case WCount:
		for pos := range idx {
			col.ints[at(pos)] = int64(pos + 1)
		}
	case WCountField:
		var cnt int64
		for pos := range idx {
			if v, ok := spec.Val(all[idx[pos]]); ok && v != nil {
				cnt++
			}
			col.ints[at(pos)] = cnt
		}
	case WMin, WMax:
		var best any
		for pos := range idx {
			v, ok := spec.Val(all[idx[pos]])
			if ok && v != nil {
				if best == nil {
					best = v
				} else if cmp := ssql.CompareAny(v, best); (spec.Kind == WMin && cmp < 0) || (spec.Kind == WMax && cmp > 0) {
					best = v
				}
			}
			col.anys[at(pos)] = best
		}
	case WRank, WPercentRank:
		groupStart := 0
		for pos := range idx {
			if pos > 0 && !peers(pos-1, pos) {
				groupStart = pos
			}
			rank := groupStart + 1
			if spec.Kind == WPercentRank {
				if partLen <= 1 {
					col.floats[at(pos)] = 0
				} else {
					col.floats[at(pos)] = float64(rank-1) / float64(partLen-1)
				}
			} else {
				col.ints[at(pos)] = int64(rank)
			}
		}
	case WDenseRank:
		dr := int64(1)
		for pos := range idx {
			if pos > 0 && !peers(pos-1, pos) {
				dr++
			}
			col.ints[at(pos)] = dr
		}
	case WCumeDist:
		groupEnd := -1
		for pos := range idx {
			if pos > groupEnd {
				groupEnd = pos
				for groupEnd+1 < partLen && peers(groupEnd+1, pos) {
					groupEnd++
				}
			}
			col.floats[at(pos)] = float64(groupEnd+1) / float64(partLen)
		}
	default:
		return false
	}
	return true
}
