package ssql

import "testing"

// TestWindowFuncCode: every window function renders the constructor call
// that rebuilds it — including NTILE's N, which the CLI's %v-and-parse
// reconstruction lost (it emitted WNtile(0) in every generated program).
func TestWindowFuncCode(t *testing.T) {
	cases := map[string]WindowFunc{
		"ssql.WRowNumber()":      WRowNumber(),
		"ssql.WRank()":           WRank(),
		"ssql.WDenseRank()":      WDenseRank(),
		"ssql.WNtile(4)":         WNtile(4),
		"ssql.WPercentRank()":    WPercentRank(),
		`ssql.WLag("price", 2)`:  WLag("price", 2),
		`ssql.WLead("price", 1)`: WLead("price", 1),
		`ssql.WFirst("a b")`:     WFirst("a b"), // a field with a space survives %q
		`ssql.WLast("x")`:        WLast("x"),
		`ssql.WSum("amount")`:    WSum("amount"),
		`ssql.WAvg("amount")`:    WAvg("amount"),
		"ssql.WCount()":          WCount(),
		`ssql.WMin("v")`:         WMin("v"),
		`ssql.WMax("v")`:         WMax("v"),
	}
	for want, fn := range cases {
		if got := WindowFuncCode(fn); got != want {
			t.Errorf("WindowFuncCode(%T) = %s, want %s", fn, got, want)
		}
	}
}
