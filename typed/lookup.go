package typed

import "iter"

// LookupJoin is the typed form of ssql.InnerLookupJoin: several lookups
// into ONE right side, clause i matching leftKeys[i] of the left row
// against rightKeys[i] of the right rows. A left row survives only when
// every clause matches; with several matches per clause it yields one
// row per combination, the first clause outermost (the order the record
// lookup multiplies in). merge receives the left row and one right row
// per clause, in clause order; the slice is reused between calls, so
// merge must copy what it keeps.
//
// The right side is read once and indexed once per clause. Every clause
// compares the same key type K; the generator widens numeric keys first.
//
// Example (`join kind.csv -on a_kind kind -as kind_name a_kind_name -
// -on z_kind kind -as kind_name z_kind_name`):
//
//	joined := typed.LookupJoin(links, kinds,
//		[]func(l Link) string{
//			func(l Link) string { return l.AKind },
//			func(l Link) string { return l.ZKind },
//		},
//		[]func(r Kind) string{
//			func(r Kind) string { return r.Kind },
//			func(r Kind) string { return r.Kind },
//		},
//		func(l Link, rs []Kind) Named {
//			return Named{AKind: l.AKind, ZKind: l.ZKind, AKindName: rs[0].KindName, ZKindName: rs[1].KindName}
//		})
func LookupJoin[L, R, O any, K comparable](
	left iter.Seq[L],
	right iter.Seq[R],
	leftKeys []func(L) K,
	rightKeys []func(R) K,
	merge func(L, []R) O,
) iter.Seq[O] {
	idx := lookupIndex(right, rightKeys)
	return func(yield func(O) bool) {
		p := newLookupProbe(idx, leftKeys, merge)
		for l := range left {
			if !p.row(l, yield) {
				return
			}
		}
	}
}

// LookupJoinParallel is [LookupJoin] over a Stream: the right side is
// indexed once, then every left shard probes the shared read-only
// indices in parallel, keeping the shards' order.
func LookupJoinParallel[L, R, O any, K comparable](
	left Stream[L],
	right iter.Seq[R],
	leftKeys []func(L) K,
	rightKeys []func(R) K,
	merge func(L, []R) O,
) Stream[O] {
	idx := lookupIndex(right, rightKeys)
	out := make([]iter.Seq[O], len(left.shards))
	for i, shard := range left.shards {
		shard := shard
		out[i] = func(yield func(O) bool) {
			p := newLookupProbe(idx, leftKeys, merge)
			for l := range shard {
				if !p.row(l, yield) {
					return
				}
			}
		}
	}
	return Stream[O]{shards: out, n: left.n, ordered: left.ordered}
}

// lookupIndex reads right once and indexes it by each clause's key.
func lookupIndex[R any, K comparable](right iter.Seq[R], rightKeys []func(R) K) []map[K][]R {
	idx := make([]map[K][]R, len(rightKeys))
	for i := range idx {
		idx[i] = make(map[K][]R)
	}
	for r := range right {
		for i, key := range rightKeys {
			k := key(r)
			idx[i][k] = append(idx[i][k], r)
		}
	}
	return idx
}

// lookupProbe holds one goroutine's buffers for probing the indices.
type lookupProbe[L, R, O any, K comparable] struct {
	idx      []map[K][]R
	leftKeys []func(L) K
	merge    func(L, []R) O
	matches  [][]R // each clause's matches for the current row
	pos      []int // the combination being emitted
	rs       []R   // the right rows handed to merge
}

func newLookupProbe[L, R, O any, K comparable](idx []map[K][]R, leftKeys []func(L) K, merge func(L, []R) O) *lookupProbe[L, R, O, K] {
	n := len(leftKeys)
	return &lookupProbe[L, R, O, K]{idx: idx, leftKeys: leftKeys, merge: merge,
		matches: make([][]R, n), pos: make([]int, n), rs: make([]R, n)}
}

// row yields l's combinations; false when yield stopped.
func (p *lookupProbe[L, R, O, K]) row(l L, yield func(O) bool) bool {
	for i, key := range p.leftKeys {
		p.matches[i] = p.idx[i][key(l)]
		if len(p.matches[i]) == 0 {
			return true // inner: a clause without a match drops the row
		}
		p.pos[i] = 0
	}
	for {
		for i, m := range p.matches {
			p.rs[i] = m[p.pos[i]]
		}
		if !yield(p.merge(l, p.rs)) {
			return false
		}
		// next combination, the last clause fastest
		i := len(p.pos) - 1
		for ; i >= 0; i-- {
			p.pos[i]++
			if p.pos[i] < len(p.matches[i]) {
				break
			}
			p.pos[i] = 0
		}
		if i < 0 {
			return true
		}
	}
}
