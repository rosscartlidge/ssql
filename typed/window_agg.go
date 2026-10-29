package typed

import (
	"math"
	"sort"
	"time"

	ssql "github.com/rosscartlidge/ssql/v4"
)

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
			if !ok || v == nil {
				continue
			}
			if s, isString := v.(string); isString && s == "" {
				continue // the record path's missing marker
			}
			k := v
			if t, isTime := v.(time.Time); isTime {
				k = t.UnixNano()
			}
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
