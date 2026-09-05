package typed

import "iter"

// RollupSet describes one grouping set of a rollup/cube over DETAIL
// rows: Key projects the set's fields out of a detail row (rows with
// equal keys belong to the same parent group), New makes an empty
// partial state, and Merge folds a detail row's partial state into a
// parent's.
type RollupSet[D, A any] struct {
	Key   func(D) string
	New   func() A
	Merge func(acc, part A)
}

// RollupEnrich is the typed form of ssql's `group-by … -rollup/-cube`:
// each detail row (one per full-key group, carrying its mergeable
// aggregation STATE) is enriched with every grouping set's aggregate
// over the raw rows — computed here by merging the detail states that
// share the set's key, never by touching the raw rows again.
//
// That is what makes it fast: the parallel detail group-by
// ([GroupByParallel]) makes the only pass over the data; this stage
// works on #groups rows × #sets, which for a 14.6M-row cube with 161
// detail groups is nothing (the record-mode Rollup re-keyed every raw
// row once per set: 36 s versus the group-by's 3 s).
//
// Correctness rests on the states being mergeable — the same property
// GroupByParallel's Combine phase needs — so COUNT, SUM, MIN, MAX and
// AVG (as sum + n) are exact; anything without a Merge (collect,
// stream folds) has no typed rollup form and falls back to record
// codegen.
//
// state extracts a detail row's partial state; build receives the
// detail row and one merged state per set, in the order of sets, and
// returns the enriched output row. Detail rows are emitted in input
// order.
func RollupEnrich[D, A, O any](detail iter.Seq[D], sets []RollupSet[D, A], state func(D) A, build func(D, []A) O) iter.Seq[O] {
	return func(yield func(O) bool) {
		var rows []D
		for d := range detail {
			rows = append(rows, d)
		}
		if len(rows) == 0 {
			return
		}
		merged := make([]map[string]A, len(sets))
		for i, set := range sets {
			m := make(map[string]A)
			for _, d := range rows {
				k := set.Key(d)
				acc, ok := m[k]
				if !ok {
					acc = set.New()
					m[k] = acc
				}
				set.Merge(acc, state(d))
			}
			merged[i] = m
		}
		parts := make([]A, len(sets))
		for _, d := range rows {
			for i, set := range sets {
				parts[i] = merged[i][set.Key(d)]
			}
			if !yield(build(d, parts)) {
				return
			}
		}
	}
}
