package typed

import (
	"iter"
	"sort"
)

// AsofOptions shapes [AsofJoin]: the direction, strictness and tolerance
// of the match. The default is backward (the nearest right row at or
// before the left time).
type AsofOptions struct {
	Forward   bool    // the nearest at or after instead
	Strict    bool    // exclude an equal time
	Tolerance float64 // when > 0, the largest distance that matches, in T's unit
}

type asofEntry[R any, T int64 | float64] struct {
	t T
	r R
}

type asofIndex[R any, K comparable, T int64 | float64] map[K][]asofEntry[R, T]

func buildAsofIndex[R any, K comparable, T int64 | float64](right iter.Seq[R], rightKey func(R) K, rightTime func(R) T) asofIndex[R, K, T] {
	idx := make(asofIndex[R, K, T])
	for r := range right {
		k := rightKey(r)
		idx[k] = append(idx[k], asofEntry[R, T]{t: rightTime(r), r: r})
	}
	for _, series := range idx {
		sort.SliceStable(series, func(i, j int) bool { return series[i].t < series[j].t })
	}
	return idx
}

// find returns the matching right row for (k, t), as [ssql.AsofJoin]
// does: the nearest at or before (or after), ties taking the last in
// input order, within the tolerance.
func (idx asofIndex[R, K, T]) find(k K, t T, o AsofOptions) (R, bool) {
	var zero R
	series := idx[k]
	if len(series) == 0 {
		return zero, false
	}
	var i int
	if !o.Forward {
		n := sort.Search(len(series), func(i int) bool {
			if o.Strict {
				return series[i].t >= t
			}
			return series[i].t > t
		})
		if n == 0 {
			return zero, false
		}
		i = n - 1
	} else {
		i = sort.Search(len(series), func(i int) bool {
			if o.Strict {
				return series[i].t > t
			}
			return series[i].t >= t
		})
		if i == len(series) {
			return zero, false
		}
		for i+1 < len(series) && series[i+1].t == series[i].t {
			i++
		}
	}
	if o.Tolerance > 0 {
		d := float64(t) - float64(series[i].t)
		if d < 0 {
			d = -d
		}
		if d > o.Tolerance {
			return zero, false
		}
	}
	return series[i].r, true
}

// AsofJoin gives each left row the right row that is current as of its
// time (DFC137 §2): among the right rows with the same key, the nearest
// at or before the left time (or at or after with Forward), ties taking
// the last in input order. T is the ordered axis: int64 nanoseconds for
// a time field, or the number itself. Inner semantics: a left row with
// no match is dropped. The right side is indexed in full; the left
// streams in input order.
func AsofJoin[L, R, O any, K comparable, T int64 | float64](
	right iter.Seq[R],
	leftKey func(L) K,
	rightKey func(R) K,
	leftTime func(L) T,
	rightTime func(R) T,
	opts AsofOptions,
	merge func(L, R) O,
) func(iter.Seq[L]) iter.Seq[O] {
	return func(left iter.Seq[L]) iter.Seq[O] {
		return func(yield func(O) bool) {
			idx := buildAsofIndex(right, rightKey, rightTime)
			for l := range left {
				if r, ok := idx.find(leftKey(l), leftTime(l), opts); ok {
					if !yield(merge(l, r)) {
						return
					}
				}
			}
		}
	}
}

// AsofJoinParallel is the sharded [AsofJoin]: the index is built once
// and every left shard probes it independently, as [HashJoinParallel]
// probes its map.
func AsofJoinParallel[L, R, O any, K comparable, T int64 | float64](
	left Stream[L],
	right iter.Seq[R],
	leftKey func(L) K,
	rightKey func(R) K,
	leftTime func(L) T,
	rightTime func(R) T,
	opts AsofOptions,
	merge func(L, R) O,
) Stream[O] {
	idx := buildAsofIndex(right, rightKey, rightTime)
	out := make([]iter.Seq[O], len(left.shards))
	for i, shard := range left.shards {
		shard := shard
		out[i] = func(yield func(O) bool) {
			for l := range shard {
				if r, ok := idx.find(leftKey(l), leftTime(l), opts); ok {
					if !yield(merge(l, r)) {
						return
					}
				}
			}
		}
	}
	return Stream[O]{shards: out, n: left.n}
}
