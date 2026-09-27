package typed

import (
	"slices"
	"sort"
	"testing"
)

type asofTrade struct {
	ID  string
	Sym string
	Ts  int64
}

type asofQuote struct {
	Sym string
	Ts  int64
	Q   string
}

func TestAsofJoin(t *testing.T) {
	quotes := []asofQuote{{"A", 30, "a30x"}, {"A", 10, "a10"}, {"B", 20, "b20"}, {"A", 30, "a30y"}, {"A", 50, "a50"}}
	trades := []asofTrade{{"t1", "A", 5}, {"t2", "A", 30}, {"t3", "A", 49}, {"t4", "B", 100}, {"t5", "Z", 1}}
	lk := func(l asofTrade) string { return l.Sym }
	rk := func(r asofQuote) string { return r.Sym }
	lt := func(l asofTrade) int64 { return l.Ts }
	rt := func(r asofQuote) int64 { return r.Ts }
	merge := func(l asofTrade, r asofQuote) string { return l.ID + "@" + r.Q }

	cases := []struct {
		name string
		opts AsofOptions
		want []string
	}{
		{"backward", AsofOptions{}, []string{"t2@a30y", "t3@a30y", "t4@b20"}},
		{"backward strict", AsofOptions{Strict: true}, []string{"t2@a10", "t3@a30y", "t4@b20"}},
		{"forward", AsofOptions{Forward: true}, []string{"t1@a10", "t2@a30y", "t3@a50"}},
		{"forward strict", AsofOptions{Forward: true, Strict: true}, []string{"t1@a10", "t2@a50", "t3@a50"}},
		{"tolerance", AsofOptions{Tolerance: 10}, []string{"t2@a30y"}},
	}
	for _, c := range cases {
		got := slices.Collect(AsofJoin(slices.Values(quotes), lk, rk, lt, rt, c.opts, merge)(slices.Values(trades)))
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
		for n := 1; n <= 3; n++ {
			par := slices.Collect(AsofJoinParallel(ParallelFromSlice(trades, n), slices.Values(quotes), lk, rk, lt, rt, c.opts, merge).Serial())
			sort.Strings(par)
			want := slices.Clone(c.want)
			sort.Strings(want)
			if !slices.Equal(par, want) {
				t.Errorf("%s parallel n=%d: got %v want %v", c.name, n, par, want)
			}
		}
	}
}
