package typed

import (
	"fmt"
	"slices"
	"testing"
)

// A detail row: full key (a, b) plus a mergeable state (count, sum).
type rollupDetail struct {
	A, B string
	St   *rollupState
}

type rollupState struct {
	N   int64
	Sum float64
}

func (s *rollupState) merge(o *rollupState) { s.N += o.N; s.Sum += o.Sum }

type rollupOut struct {
	A, B                     string
	N, AN, BN, ABN           int64
	Sum, ASum, BSum, ABSum   float64
	Mean, AMean, BMean, ABMn float64
}

func TestRollupEnrichCube(t *testing.T) {
	// Detail groups as a parallel group-by would emit them.
	detail := slices.Values([]rollupDetail{
		{"x", "p", &rollupState{2, 5}}, // rows (x,p,1),(x,p,4)
		{"x", "q", &rollupState{1, 2}},
		{"y", "p", &rollupState{1, 3}},
	})
	newSt := func() *rollupState { return &rollupState{} }
	mergeSt := func(acc, part *rollupState) { acc.merge(part) }
	// cube over (a, b): (), (a), (b), (a, b) — the order ssql.RollupGroupingSets uses
	sets := []RollupSet[rollupDetail, *rollupState]{
		{Key: func(d rollupDetail) string { return "" }, New: newSt, Merge: mergeSt},
		{Key: func(d rollupDetail) string { return d.A }, New: newSt, Merge: mergeSt},
		{Key: func(d rollupDetail) string { return d.B }, New: newSt, Merge: mergeSt},
		{Key: func(d rollupDetail) string { return d.A + "\x00" + d.B }, New: newSt, Merge: mergeSt},
	}
	mean := func(s *rollupState) float64 { return s.Sum / float64(s.N) }
	out := slices.Collect(RollupEnrich(detail, sets,
		func(d rollupDetail) *rollupState { return d.St },
		func(d rollupDetail, p []*rollupState) rollupOut {
			return rollupOut{
				A: d.A, B: d.B,
				N: p[0].N, AN: p[1].N, BN: p[2].N, ABN: p[3].N,
				Sum: p[0].Sum, ASum: p[1].Sum, BSum: p[2].Sum, ABSum: p[3].Sum,
				Mean: mean(p[0]), AMean: mean(p[1]), BMean: mean(p[2]), ABMn: mean(p[3]),
			}
		}))
	// Hand-checked against `ssql group-by a b -count n -sum v tot -cube` on
	// the same four raw rows (x,p,1),(x,q,2),(y,p,3),(x,p,4).
	want := []rollupOut{
		{"x", "p", 4, 3, 3, 2, 10, 7, 8, 5, 2.5, 7.0 / 3, 8.0 / 3, 2.5},
		{"x", "q", 4, 3, 1, 1, 10, 7, 2, 2, 2.5, 7.0 / 3, 2, 2},
		{"y", "p", 4, 1, 3, 1, 10, 3, 8, 3, 2.5, 3, 8.0 / 3, 3},
	}
	if fmt.Sprint(out) != fmt.Sprint(want) {
		t.Errorf("RollupEnrich cube:\n got  %v\n want %v", out, want)
	}
	// The detail state itself must not have been mutated by merging.
	if got := slices.Collect(detail)[0].St.N; got != 2 {
		t.Errorf("detail state mutated: N=%d", got)
	}
}

func TestRollupEnrichEmpty(t *testing.T) {
	out := slices.Collect(RollupEnrich(slices.Values([]rollupDetail{}), nil,
		func(d rollupDetail) *rollupState { return d.St },
		func(d rollupDetail, p []*rollupState) rollupOut { return rollupOut{} }))
	if len(out) != 0 {
		t.Errorf("empty detail should yield nothing, got %d", len(out))
	}
}
