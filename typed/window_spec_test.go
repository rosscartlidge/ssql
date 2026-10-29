package typed

import (
	"reflect"
	"slices"
	"testing"
)

// TestWindowSpecConstructorsMatchLiterals: a spec built by a constructor
// (SumOf, LagOf, MedianOf, …; the Ptr forms over a nullable field) gives
// the same results as the struct literal with the readers spelled out,
// and a clause that names only its key (KeyInt / KeyString / KeyFloat
// with Desc) orders and finds peers as one with the comparator spelled.
func TestWindowSpecConstructorsMatchLiterals(t *testing.T) {
	type row struct {
		D string
		N int64
		F float64
		P *int64
		S string
	}
	p := func(v int64) *int64 { return &v }
	rows := []row{
		{"a", 3, 1.5, p(3), "x"}, {"a", 1, 2.5, nil, "y"}, {"a", 3, 0.5, p(1), "x"}, {"b", 2, 2.5, p(2), "z"},
		{"b", 2, 1.5, nil, "z"}, {"b", 5, 3.5, p(5), "w"}, {"a", 4, 1.5, p(4), "y"},
	}
	byN := func(a, b row) int {
		switch {
		case a.N < b.N:
			return -1
		case a.N > b.N:
			return 1
		}
		return 0
	}
	literal := WindowClause[row]{
		Partition: func(r row) string { return r.D },
		HasOrder:  true, Compare: byN, KeyInt: func(r row) int64 { return r.N },
		Frame: WindowFrame{Preceding: 1, Following: 1},
		Specs: []WindowSpec[row]{
			{Kind: WRowNumber}, {Kind: WRank}, {Kind: WCount},
			{Kind: WSum, Num: func(r row) (float64, bool) { return float64(r.N), true }, Int: func(r row) (int64, bool) { return r.N, true }},
			{Kind: WAvg, Num: func(r row) (float64, bool) { return r.F, true }},
			{Kind: WSum, Num: func(r row) (float64, bool) {
				if r.P == nil {
					return 0, false
				}
				return float64(*r.P), true
			}, Int: func(r row) (int64, bool) {
				if r.P == nil {
					return 0, false
				}
				return *r.P, true
			}},
			{Kind: WMin, Val: func(r row) (any, bool) { return r.S, true }},
			{Kind: WLag, N: 1, Val: func(r row) (any, bool) { return r.S, true }},
			{Kind: WLag, N: 1, Default: "-", Val: func(r row) (any, bool) { return r.S, true }},
			{Kind: WFirst, Val: func(r row) (any, bool) {
				if r.P == nil {
					return nil, false
				}
				return *r.P, true
			}},
			{Kind: WCountField, Val: func(r row) (any, bool) {
				if r.P == nil {
					return nil, false
				}
				return *r.P, true
			}},
			{Kind: WAgg, Agg: WAggQuantile, P: 0.5, Num: func(r row) (float64, bool) { return float64(r.N), true }},
			{Kind: WAgg, Agg: WAggStdDev, Num: func(r row) (float64, bool) { return r.F, true }},
			{Kind: WAgg, Agg: WAggMode, Val: func(r row) (any, bool) { return r.S, true }},
			{Kind: WNtile, N: 2}, {Kind: WPercentRank},
		},
	}
	built := literal
	built.Compare = nil // the key alone
	built.Specs = []WindowSpec[row]{
		RowNumber[row](), Rank[row](), CountRows[row](),
		SumOf(func(r row) int64 { return r.N }),
		AvgOf(func(r row) float64 { return r.F }),
		SumOfPtr(func(r row) *int64 { return r.P }),
		MinOf(func(r row) string { return r.S }),
		LagOf(func(r row) string { return r.S }, 1),
		LagOf(func(r row) string { return r.S }, 1).WithDefault("-"),
		FirstOfPtr(func(r row) *int64 { return r.P }),
		CountOfPtr(func(r row) *int64 { return r.P }),
		MedianOf(func(r row) int64 { return r.N }),
		StdDevOf(func(r row) float64 { return r.F }),
		ModeOf(func(r row) string { return r.S }),
		Ntile[row](2), PercentRank[row](),
	}
	build := func(r row, res WindowRow) []any { return append([]any{r.D, r.N}, res.Values()...) }
	want := slices.Collect(Window([]WindowClause[row]{literal}, build)(slices.Values(rows)))
	got := slices.Collect(Window([]WindowClause[row]{built}, build)(slices.Values(rows)))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("constructors differ from the literals:\n got  %v\n want %v", got, want)
	}
	// The derived comparator, descending and by the other key kinds.
	for _, c := range []struct {
		name string
		lit  WindowClause[row]
	}{
		{"int desc", WindowClause[row]{HasOrder: true, Desc: true, KeyInt: func(r row) int64 { return r.N },
			Compare: func(a, b row) int { return byN(b, a) }}},
		{"string", WindowClause[row]{HasOrder: true, KeyString: func(r row) string { return r.S },
			Compare: func(a, b row) int {
				switch {
				case a.S < b.S:
					return -1
				case a.S > b.S:
					return 1
				}
				return 0
			}}},
		{"float desc", WindowClause[row]{HasOrder: true, Desc: true, KeyFloat: func(r row) float64 { return r.F },
			Compare: func(a, b row) int {
				switch {
				case b.F < a.F:
					return -1
				case b.F > a.F:
					return 1
				}
				return 0
			}}},
	} {
		c.lit.Specs = []WindowSpec[row]{Rank[row](), DenseRank[row](), CumeDist[row]()}
		keyOnly := c.lit
		keyOnly.Compare = nil
		want := slices.Collect(Window([]WindowClause[row]{c.lit}, build)(slices.Values(rows)))
		got := slices.Collect(WindowParallel([]WindowClause[row]{keyOnly}, build)(ParallelFromSlice(rows, 3)).Serial())
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the derived comparator differs from the spelled one:\n got  %v\n want %v", c.name, got, want)
		}
	}
}
