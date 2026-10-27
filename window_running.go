package ssql

// The one-pass window aggregates (2026-10-27). computeWindowFunc computes
// one row's value by scanning that row's frame, so over the default frame
// (UNBOUNDED PRECEDING → CURRENT ROW) a partition of n rows costs n²/2
// steps — a running sum over 560 k rows was still running at a 30-minute
// cap where DataFusion took 5.7 s (DFC157 §4). The frame's start never
// moves on the running frame, so every aggregate here carries its state
// from row to row exactly as the per-row scan would have built it (the
// same operations in the same order: a compensated float sum carried
// forward is the sum the scan from 0 would compute), and the whole-
// partition frame (UNBOUNDED on both sides) is computed once and shared.
// Everything else — a bounded ROWS frame (O(n·k)), a RANGE frame, lag and
// lead — stays per row. The fold has had this since DFC156
// (runningFoldValues); this widens it to the aggregates. Typed has the
// same split (typed/window.go).

// isWholeFrame reports the frame that is the whole partition: UNBOUNDED
// PRECEDING → UNBOUNDED FOLLOWING (a window without -order and without
// bounds).
func isWholeFrame(frame WindowFrame) bool {
	return !frame.Range && frame.Preceding < 0 && frame.Following < 0
}

// runningWindowValues precomputes, for the specs of cfg whose value over
// the running frame can be carried across the sorted partition, one value
// per position — keyed by spec index, like runningFoldValues. Also, over
// the whole-partition frame, the frame aggregates computed once. Specs
// not covered are left to computeWindowFunc.
func runningWindowValues(cfg WindowConfig, all []Record, indices []int) map[int][]any {
	running, whole := isRunningFrame(cfg.Frame), isWholeFrame(cfg.Frame)
	if !running && !whole {
		return nil
	}
	partLen := len(indices)
	var out map[int][]any
	put := func(si int, vals []any) {
		if out == nil {
			out = make(map[int][]any)
		}
		out[si] = vals
	}
	same := func(a, b int) bool {
		return CompareRecordFields(all[indices[a]], all[indices[b]], cfg.OrderBy) == 0
	}
	for si, spec := range cfg.Specs {
		switch f := spec.Function.(type) {
		case wSum, wAvg, wCount, wCountField, wMin, wMax, wAgg:
			if whole {
				// One value for the partition: the per-row scan over the
				// whole frame gives every row the same answer.
				v := computeWindowFunc(spec.Function, all, indices, 0, partLen, cfg.Frame, cfg.OrderBy)
				vals := make([]any, partLen)
				for i := range vals {
					vals[i] = v
				}
				put(si, vals)
				continue
			}
			if _, isAgg := f.(wAgg); isAgg {
				continue // a registry aggregate has no carried form; per row
			}
			put(si, runningFrameValues(spec.Function, all, indices))
		case wRank, wPercentRank:
			if !running {
				continue
			}
			// The rank is the position of the first row of the current
			// peer group, carried: a new group starts where the row is not
			// a peer of the one before it.
			vals := make([]any, partLen)
			groupStart := 0
			for pos := range indices {
				if pos > 0 && !same(pos-1, pos) {
					groupStart = pos
				}
				rank := groupStart + 1
				if _, pr := f.(wPercentRank); pr {
					if partLen <= 1 {
						vals[pos] = float64(0)
					} else {
						vals[pos] = float64(rank-1) / float64(partLen-1)
					}
				} else {
					vals[pos] = int64(rank)
				}
			}
			put(si, vals)
		case wDenseRank:
			if !running {
				continue
			}
			vals := make([]any, partLen)
			dr := int64(1)
			for pos := range indices {
				if pos > 0 && !same(pos-1, pos) {
					dr++
				}
				vals[pos] = dr
			}
			put(si, vals)
		case wCumeDist:
			if !running {
				continue
			}
			// The end of the current peer group, found once per group.
			vals := make([]any, partLen)
			groupEnd := -1
			for pos := range indices {
				if pos > groupEnd {
					groupEnd = pos
					for groupEnd+1 < partLen && same(groupEnd+1, pos) {
						groupEnd++
					}
				}
				vals[pos] = float64(groupEnd+1) / float64(partLen)
			}
			put(si, vals)
		}
	}
	return out
}

// runningFrameValues carries one frame aggregate across the sorted
// partition: the state after row pos is what the per-row scan of 0..pos
// builds, operation for operation.
func runningFrameValues(fn WindowFunc, all []Record, indices []int) []any {
	vals := make([]any, len(indices))
	switch f := fn.(type) {
	case wSum:
		var sum numericSum
		for pos, idx := range indices {
			if v, ok := Get[any](all[idx], f.Field); ok {
				sum.add(v)
			}
			vals[pos] = sum.value()
		}
	case wAvg:
		var sum numericSum
		for pos, idx := range indices {
			if v, ok := Get[any](all[idx], f.Field); ok {
				sum.add(v)
			}
			if mean, ok := sum.mean(); ok {
				vals[pos] = mean
			} else {
				vals[pos] = float64(0)
			}
		}
	case wCount:
		for pos := range indices {
			vals[pos] = int64(pos + 1)
		}
	case wCountField:
		var n int64
		for pos, idx := range indices {
			if v, ok := Get[any](all[idx], f.Field); ok && v != nil {
				n++
			}
			vals[pos] = n
		}
	case wMin:
		var minVal any
		for pos, idx := range indices {
			v, _ := Get[any](all[idx], f.Field)
			if minVal == nil || CompareAny(v, minVal) < 0 {
				minVal = v
			}
			vals[pos] = minVal
		}
	case wMax:
		var maxVal any
		for pos, idx := range indices {
			v, _ := Get[any](all[idx], f.Field)
			if maxVal == nil || CompareAny(v, maxVal) > 0 {
				maxVal = v
			}
			vals[pos] = maxVal
		}
	}
	return vals
}
