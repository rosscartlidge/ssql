package ssql

import (
	"iter"
	"sort"
	"time"
)

// AsofConfig configures [AsofJoin] (DFC137 §2).
type AsofConfig struct {
	// LeftKeys and RightKeys are the equality part, pairwise; both may be
	// empty, in which case the whole right side is one series.
	LeftKeys, RightKeys []string
	// LeftTime and RightTime are the ordered fields (numeric or time).
	LeftTime, RightTime string
	// Forward matches the nearest right row at or after the left time;
	// the default is backward, the nearest at or before (the quote in
	// force when the trade happened).
	Forward bool
	// Strict excludes equal times (before / after, never at).
	Strict bool
	// Tolerance, when > 0, is the largest distance that still matches:
	// nanoseconds for time fields, the number itself for numeric ones.
	Tolerance float64
	// KeepUnmatched keeps left rows with no match, the right fields
	// absent (a left join); otherwise they are dropped (inner).
	KeepUnmatched bool
}

// asofOrd is a value on the ordered axis: times as nanoseconds (exact),
// numbers as float64. A time never compares with a number.
type asofOrd struct {
	t      int64
	f      float64
	isTime bool
}

func asofOrdOf(v any) (asofOrd, bool) {
	switch x := v.(type) {
	case time.Time:
		return asofOrd{t: x.UnixNano(), isTime: true}, true
	case int64:
		return asofOrd{f: float64(x)}, true
	case float64:
		return asofOrd{f: x}, true
	case int:
		return asofOrd{f: float64(x)}, true
	}
	return asofOrd{}, false
}

// cmp returns <0, 0, >0 for a vs b; ok=false when the kinds differ.
func (a asofOrd) cmp(b asofOrd) (int, bool) {
	if a.isTime != b.isTime {
		return 0, false
	}
	if a.isTime {
		switch {
		case a.t < b.t:
			return -1, true
		case a.t > b.t:
			return 1, true
		}
		return 0, true
	}
	switch {
	case a.f < b.f:
		return -1, true
	case a.f > b.f:
		return 1, true
	}
	return 0, true
}

// distance is |a-b| in the axis's unit (nanoseconds or the number).
func (a asofOrd) distance(b asofOrd) float64 {
	if a.isTime {
		d := a.t - b.t
		if d < 0 {
			d = -d
		}
		return float64(d)
	}
	d := a.f - b.f
	if d < 0 {
		d = -d
	}
	return d
}

type asofEntry struct {
	ord asofOrd
	rec Record
}

// AsofJoin gives each left row the right row that is current AS OF its
// time: among the right rows with the same key, the nearest at or before
// the left time (or at or after with Forward), the way a trade takes the
// quote in force when it happened. Two series sampled at different
// instants have no equal timestamps, so an equi-join finds nothing.
//
// The right side is read in full and indexed per key, sorted by time;
// the left streams in input order and each row binary-searches its
// series, so the output is in left order. Ties on the right time take
// the last in input order (deterministic where SQL engines pick any).
// A left row whose key or time is absent, or whose time is of the other
// kind, matches nothing (DFC124). The merged row is the left row's
// fields, then the right row's, except a right key or time field that
// shares its name with the left's (the left value is the row's).
func AsofJoin(right iter.Seq[Record], cfg AsofConfig) Filter[Record, Record] {
	return func(left iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			rightKey := FieldsKey(cfg.RightKeys...)
			leftKey := FieldsKey(cfg.LeftKeys...)
			index := make(map[string][]asofEntry)
			for r := range right {
				k, ok := rightKey(r)
				if !ok {
					continue
				}
				v, ok := Get[any](r, cfg.RightTime)
				if !ok {
					continue
				}
				ord, ok := asofOrdOf(v)
				if !ok {
					continue
				}
				index[k] = append(index[k], asofEntry{ord: ord, rec: r})
			}
			for _, series := range index {
				sort.SliceStable(series, func(i, j int) bool {
					c, _ := series[i].ord.cmp(series[j].ord)
					return c < 0
				})
			}
			// The right's key and time fields that share a left name are
			// not copied: the left value is the row's.
			skip := map[string]bool{}
			for i, rk := range cfg.RightKeys {
				if rk == cfg.LeftKeys[i] {
					skip[rk] = true
				}
			}
			if cfg.RightTime == cfg.LeftTime {
				skip[cfg.RightTime] = true
			}
			for l := range left {
				match, ok := asofMatch(index, cfg, leftKey, l)
				if !ok {
					if !cfg.KeepUnmatched {
						continue
					}
					if !yield(l) {
						return
					}
					continue
				}
				m := MakeMutableRecordWithCapacity(l.Len() + match.Len())
				for k, v := range l.All() {
					m.put(k, v)
				}
				for k, v := range match.All() {
					if skip[k] {
						continue
					}
					m.put(k, v)
				}
				if !yield(m.Freeze()) {
					return
				}
			}
		}
	}
}

// asofMatch finds the right row for l, or ok=false.
func asofMatch(index map[string][]asofEntry, cfg AsofConfig, leftKey SetKeyFunc, l Record) (Record, bool) {
	k, ok := leftKey(l)
	if !ok {
		return Record{}, false
	}
	series := index[k]
	if len(series) == 0 {
		return Record{}, false
	}
	v, ok := Get[any](l, cfg.LeftTime)
	if !ok {
		return Record{}, false
	}
	lo, ok := asofOrdOf(v)
	if !ok {
		return Record{}, false
	}
	if _, ok := lo.cmp(series[0].ord); !ok {
		return Record{}, false // a time against numbers, or the reverse
	}
	var i int
	if !cfg.Forward {
		// the last entry with ord <= lo (strict: < lo)
		n := sort.Search(len(series), func(i int) bool {
			c, _ := series[i].ord.cmp(lo)
			if cfg.Strict {
				return c >= 0
			}
			return c > 0
		})
		if n == 0 {
			return Record{}, false
		}
		i = n - 1
	} else {
		// the first entry with ord >= lo (strict: > lo), then the last of
		// its equals: ties take the last in input order either way
		i = sort.Search(len(series), func(i int) bool {
			c, _ := series[i].ord.cmp(lo)
			if cfg.Strict {
				return c > 0
			}
			return c >= 0
		})
		if i == len(series) {
			return Record{}, false
		}
		for i+1 < len(series) {
			if c, _ := series[i+1].ord.cmp(series[i].ord); c != 0 {
				break
			}
			i++
		}
	}
	if cfg.Tolerance > 0 && lo.distance(series[i].ord) > cfg.Tolerance {
		return Record{}, false
	}
	return series[i].rec, true
}
