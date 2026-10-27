package ssql

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// TestWindowRunningMatchesPerRow pins the one-pass precompute against the
// per-row scan it replaces, on data with ties, missing values and mixed
// numeric types, for every function it covers and both frames it covers:
// the two paths must give the same value at every position.
func TestWindowRunningMatchesPerRow(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var all []Record
	for i := 0; i < 400; i++ {
		fields := map[string]any{"k": int64(rng.Intn(12))} // ties in the order
		switch rng.Intn(4) {
		case 0:
			fields["v"] = int64(rng.Intn(50) - 25)
		case 1:
			fields["v"] = rng.Float64()*100 - 50
		case 2:
			fields["v"] = fmt.Sprintf("s%02d", rng.Intn(30))
		case 3:
			// missing v
		}
		all = append(all, NewRecord(fields))
	}
	indices := make([]int, len(all))
	for i := range indices {
		indices[i] = i
	}
	order := []OrderField{{Field: "k"}}
	specs := []WindowSpec{
		{ResultName: "sum", Function: wSum{Field: "v"}},
		{ResultName: "avg", Function: wAvg{Field: "v"}},
		{ResultName: "cnt", Function: wCount{}},
		{ResultName: "cf", Function: wCountField{Field: "v"}},
		{ResultName: "min", Function: wMin{Field: "v"}},
		{ResultName: "max", Function: wMax{Field: "v"}},
		{ResultName: "rank", Function: wRank{}},
		{ResultName: "dr", Function: wDenseRank{}},
		{ResultName: "pr", Function: wPercentRank{}},
		{ResultName: "cd", Function: wCumeDist{}},
	}
	for _, frame := range []WindowFrame{{Preceding: -1, Following: 0}, {Preceding: -1, Following: -1}} {
		cfg := WindowConfig{OrderBy: order, Frame: frame, Specs: specs}
		sorted := append([]int(nil), indices...)
		sortIndicesForTest(all, sorted, order)
		running := runningWindowValues(cfg, all, sorted)
		for si, spec := range specs {
			vals, ok := running[si]
			if !ok {
				if isRunningFrame(frame) {
					t.Errorf("frame %+v: %s not precomputed", frame, spec.ResultName)
				}
				continue
			}
			for pos := range sorted {
				want := computeWindowFunc(spec.Function, all, sorted, pos, len(sorted), frame, order)
				if !reflect.DeepEqual(vals[pos], want) {
					t.Fatalf("frame %+v %s at %d: running %v (%T), per-row %v (%T)", frame, spec.ResultName, pos, vals[pos], vals[pos], want, want)
				}
			}
		}
	}
}

func sortIndicesForTest(all []Record, idx []int, order []OrderField) {
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && CompareRecordFields(all[idx[j-1]], all[idx[j]], order) > 0; j-- {
			idx[j-1], idx[j] = idx[j], idx[j-1]
		}
	}
}
