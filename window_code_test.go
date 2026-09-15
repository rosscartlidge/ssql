package ssql

import "testing"

// TestWindowFuncCode: every window function renders the constructor call
// that rebuilds it — including NTILE's N, which the CLI's %v-and-parse
// reconstruction lost (it emitted WNtile(0) in every generated program).
func TestWindowFuncCode(t *testing.T) {
	cases := map[string]WindowFunc{
		"ssql.WRowNumber()":                  WRowNumber(),
		"ssql.WRank()":                       WRank(),
		"ssql.WDenseRank()":                  WDenseRank(),
		"ssql.WNtile(4)":                     WNtile(4),
		"ssql.WPercentRank()":                WPercentRank(),
		`ssql.WLag("price", 2)`:              WLag("price", 2),
		`ssql.WLead("price", 1)`:             WLead("price", 1),
		`ssql.WFirst("a b")`:                 WFirst("a b"), // a field with a space survives %q
		`ssql.WLast("x")`:                    WLast("x"),
		`ssql.WSum("amount")`:                WSum("amount"),
		`ssql.WAvg("amount")`:                WAvg("amount"),
		"ssql.WCount()":                      WCount(),
		`ssql.WMin("v")`:                     WMin("v"),
		`ssql.WMax("v")`:                     WMax("v"),
		"ssql.WCumeDist()":                   WCumeDist(),
		`ssql.WNthValue("v", 2)`:             WNthValue("v", 2),
		`ssql.WLagDefault("v", 1, int64(0))`: WLagDefault("v", 1, int64(0)),
		`ssql.WLeadDefault("v", 2, "none")`:  WLeadDefault("v", 2, "none"),
		`ssql.WCountField("v")`:              WCountField("v"),
	}
	for want, fn := range cases {
		if got := WindowFuncCode(fn); got != want {
			t.Errorf("WindowFuncCode(%T) = %s, want %s", fn, got, want)
		}
	}
}

// TestWindowFuncCodeAggregate: a registry aggregate over a frame renders a
// WAggregate call that embeds the aggregate's own constructor code.
func TestWindowFuncCodeAggregate(t *testing.T) {
	fn := WAggregate(WAggSpec{Name: "stddev", Field: "v", Kind: "float", MinRows: 2, Agg: StdDev("v"), Code: `ssql.StdDev("v")`})
	want := `ssql.WAggregate(ssql.WAggSpec{Name: "stddev", Field: "v", Extra: "", Kind: "float", MinRows: 2, Agg: ssql.StdDev("v"), Code: "ssql.StdDev(\"v\")"})`
	if got := WindowFuncCode(fn); got != want {
		t.Fatalf("WindowFuncCode(wAgg) =\n%s\nwant\n%s", got, want)
	}
	if f, ok := WindowFuncField(fn); !ok || f != "v" {
		t.Fatalf("WindowFuncField = %q, %v", f, ok)
	}
	if k := WindowFuncResultKind(fn); k != "float" {
		t.Fatalf("WindowFuncResultKind = %q", k)
	}
}
