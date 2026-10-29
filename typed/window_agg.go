package typed

import (
	"math"
	"sort"
	"strings"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// aggMissing is the record path's rule for a value that counts as absent
// in an aggregate: nil, or the empty string.
func aggMissing(v any) bool {
	if v == nil {
		return true
	}
	s, isString := v.(string)
	return isString && s == ""
}

// frameAggregate is a WAgg spec's value over the frame [start, end] of
// the sorted partition idx — the typed form of ssql.applyFrameAggregate:
// nil (absent) for a frame below the statistic's minimum rows (two for
// the sample variance and deviation) or with no present value.
func frameAggregate[T any](spec WindowSpec[T], all []T, idx []int, start, end int) any {
	if spec.Agg == WAggVariance || spec.Agg == WAggStdDev {
		if end-start+1 < 2 {
			return nil
		}
	}
	switch spec.Agg {
	case WAggQuantile:
		vals := make([]float64, 0, end-start+1)
		for i := start; i <= end; i++ {
			if v, ok := spec.Num(all[idx[i]]); ok {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			return nil
		}
		sort.Float64s(vals)
		return ssql.QuantileCont(vals, spec.P)
	case WAggVariance, WAggStdDev:
		var w ssql.Welford
		for i := start; i <= end; i++ {
			if v, ok := spec.Num(all[idx[i]]); ok {
				w.Add(v)
			}
		}
		if w.N == 0 {
			return nil
		}
		if spec.Agg == WAggStdDev {
			return math.Sqrt(w.Variance())
		}
		return w.Variance()
	case WAggCountDistinct:
		seen := make(map[any]struct{})
		for i := start; i <= end; i++ {
			if v, ok := spec.Val(all[idx[i]]); ok && !aggMissing(v) {
				seen[ssql.DistinctKey(v)] = struct{}{}
			}
		}
		return int64(len(seen))
	case WAggStringAgg:
		var parts []string
		for i := start; i <= end; i++ {
			if v, ok := spec.Val(all[idx[i]]); ok && !aggMissing(v) {
				parts = append(parts, ssql.AggValueString(v))
			}
		}
		if len(parts) == 0 {
			return nil // the record path's "" marker: absent
		}
		return strings.Join(parts, spec.Sep)
	case WAggArgMax, WAggArgMin:
		// FIELD at the extreme BY: a strictly better BY replaces, so ties
		// keep the first in frame order; a row missing either is skipped.
		sign := 1
		if spec.Agg == WAggArgMin {
			sign = -1
		}
		var bestBy, carried any
		found := false
		for i := start; i <= end; i++ {
			b, ok := spec.By(all[idx[i]])
			if !ok || aggMissing(b) {
				continue
			}
			v, ok := spec.Val(all[idx[i]])
			if !ok || aggMissing(v) {
				continue
			}
			if !found || ssql.CompareAny(b, bestBy)*sign > 0 {
				bestBy, carried, found = b, v, true
			}
		}
		if !found {
			return nil
		}
		return carried
	case WAggMode:
		type entry struct {
			value any
			n     int64
			first int64
		}
		counts := make(map[any]*entry)
		var order int64
		for i := start; i <= end; i++ {
			v, ok := spec.Val(all[idx[i]])
			if !ok || aggMissing(v) {
				continue
			}
			k := ssql.DistinctKey(v)
			if e, seen := counts[k]; seen {
				e.n++
			} else {
				counts[k] = &entry{value: v, n: 1, first: order}
			}
			order++
		}
		var best *entry
		for _, e := range counts {
			if best == nil || e.n > best.n || (e.n == best.n && e.first < best.first) {
				best = e
			}
		}
		if best == nil {
			return nil
		}
		return best.value
	}
	return nil
}
