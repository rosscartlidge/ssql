package ssql

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

// followEdges builds edge rows from "src dst w" triples; w "" is absent.
func followEdges(triples ...string) []Record {
	var rs []Record
	for _, t := range triples {
		f := strings.Fields(t)
		m := MakeMutableRecord().String("src", f[0]).String("dst", f[1])
		if len(f) > 2 {
			var w int64
			fmt.Sscan(f[2], &w)
			m = m.Int("w", w)
		}
		rs = append(rs, m.Freeze())
	}
	return rs
}

func followSeeds(nodes ...string) []Record {
	var rs []Record
	for _, n := range nodes {
		rs = append(rs, MakeMutableRecord().String("city", n).Freeze())
	}
	return rs
}

// followRun returns each output row as "city depth sum path".
func followRun(t *testing.T, edges, seeds []Record, cfg FollowConfig) []string {
	t.Helper()
	cfg.NodeField, cfg.SrcField, cfg.DstField = "city", "src", "dst"
	cfg.DepthField, cfg.PathField = "hops", "path"
	var out []string
	for r := range Follow(slices.Values(edges), cfg)(slices.Values(seeds)) {
		var parts []string
		for _, f := range r.Keys() {
			v, _ := Get[any](r, f)
			parts = append(parts, fmt.Sprintf("%v", v))
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

var followFlights = followEdges(
	"SYD MEL 90", "MEL SYD 90", "SYD BNE 95", "BNE CNS 140", "MEL ADL 75",
	"ADL PER 190", "MEL HBA 75", "BNE PER 330", "CNS DRW 150", "PER DRW 210")

func TestFollowFewestHops(t *testing.T) {
	got := followRun(t, followFlights, followSeeds("SYD"), FollowConfig{Sums: []FollowSum{{"w", "mins"}}})
	want := []string{
		`SYD 0 0 ["SYD"]`,
		`BNE 1 95 ["SYD","BNE"]`,
		`MEL 1 90 ["SYD","MEL"]`,
		`CNS 2 235 ["SYD","BNE","CNS"]`,
		`PER 2 425 ["SYD","BNE","PER"]`,
		`ADL 2 165 ["SYD","MEL","ADL"]`,
		`HBA 2 165 ["SYD","MEL","HBA"]`,
		`DRW 3 385 ["SYD","BNE","CNS","DRW"]`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFollowLowestSum(t *testing.T) {
	got := followRun(t, followFlights, followSeeds("SYD"), FollowConfig{Mode: FollowLowestSum, Lowest: FollowSum{"w", "mins"}})
	want := []string{
		`SYD 0 0 ["SYD"]`,
		`MEL 1 90 ["SYD","MEL"]`,
		`BNE 1 95 ["SYD","BNE"]`,
		`ADL 2 165 ["SYD","MEL","ADL"]`,
		`HBA 2 165 ["SYD","MEL","HBA"]`,
		`CNS 2 235 ["SYD","BNE","CNS"]`,
		`PER 3 355 ["SYD","MEL","ADL","PER"]`,
		`DRW 3 385 ["SYD","BNE","CNS","DRW"]`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFollowAllPaths(t *testing.T) {
	got := followRun(t, followFlights, followSeeds("SYD"), FollowConfig{Mode: FollowAllPaths, Sums: []FollowSum{{"w", "mins"}}})
	// The recursive CTE's 11 rows (DFC161 §1), depth first in edge order.
	want := []string{
		`SYD 0 0 ["SYD"]`,
		`MEL 1 90 ["SYD","MEL"]`,
		`ADL 2 165 ["SYD","MEL","ADL"]`,
		`PER 3 355 ["SYD","MEL","ADL","PER"]`,
		`DRW 4 565 ["SYD","MEL","ADL","PER","DRW"]`,
		`HBA 2 165 ["SYD","MEL","HBA"]`,
		`BNE 1 95 ["SYD","BNE"]`,
		`CNS 2 235 ["SYD","BNE","CNS"]`,
		`DRW 3 385 ["SYD","BNE","CNS","DRW"]`,
		`PER 2 425 ["SYD","BNE","PER"]`,
		`DRW 3 635 ["SYD","BNE","PER","DRW"]`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestFollowTiesByValue: the diamond lists A→C before A→B, so the route
// found first runs through C; the tie rule (the least path) picks B in
// both keyed modes. A duplicate edge keeps its lighter weight.
func TestFollowTiesByValue(t *testing.T) {
	edges := followEdges("A C 1", "A B 1", "C D 1", "B D 1", "A E 5", "A E 2")
	for _, mode := range []FollowMode{FollowFewestHops, FollowLowestSum} {
		cfg := FollowConfig{Mode: mode, Sums: []FollowSum{{"w", "s"}}}
		if mode == FollowLowestSum {
			cfg = FollowConfig{Mode: mode, Lowest: FollowSum{"w", "s"}}
		}
		got := followRun(t, edges, followSeeds("A"), cfg)
		if !slices.Contains(got, `D 2 2 ["A","B","D"]`) || !slices.Contains(got, `E 1 2 ["A","E"]`) {
			t.Errorf("mode %d: %v", mode, got)
		}
	}
}

func TestFollowBothSelfLoopAndLimits(t *testing.T) {
	// X→Y is listed both ways: undirected, it is one pair, not two.
	got := followRun(t, followEdges("X Y 1", "Y X 1", "Y Y 3"), followSeeds("X"), FollowConfig{Mode: FollowAllPaths, Both: true})
	if want := []string{`X 0 ["X"]`, `Y 1 ["X","Y"]`}; !slices.Equal(got, want) {
		t.Errorf("both: got %v want %v", got, want)
	}
	// Undirected reaches backwards along a one-way edge.
	got = followRun(t, followEdges("P Q 1"), followSeeds("Q"), FollowConfig{Both: true})
	if want := []string{`Q 0 ["Q"]`, `P 1 ["Q","P"]`}; !slices.Equal(got, want) {
		t.Errorf("both reverse: got %v want %v", got, want)
	}
	// A seed with no edges, and one with no node, are their own rows.
	got = followRun(t, followFlights, followSeeds("HBA", ""), FollowConfig{})
	if want := []string{`HBA 0 ["HBA"]`, ` 0 [""]`}; !slices.Equal(got, want) {
		t.Errorf("no edges: got %v want %v", got, want)
	}
	// A depth limit, several seeds in seed order.
	got = followRun(t, followFlights, followSeeds("CNS", "ADL"), FollowConfig{Mode: FollowAllPaths, MaxDepth: 1})
	if want := []string{`CNS 0 ["CNS"]`, `DRW 1 ["CNS","DRW"]`, `ADL 0 ["ADL"]`, `PER 1 ["ADL","PER"]`}; !slices.Equal(got, want) {
		t.Errorf("max depth: got %v want %v", got, want)
	}
}

func TestFollowSums(t *testing.T) {
	// Integer totals are Int128: two hops of MaxInt64 do not overflow.
	big := []Record{
		MakeMutableRecord().String("src", "a").String("dst", "b").Int("w", math.MaxInt64).Freeze(),
		MakeMutableRecord().String("src", "b").String("dst", "c").Int("w", math.MaxInt64).Freeze(),
	}
	got := followRun(t, big, followSeeds("a"), FollowConfig{Sums: []FollowSum{{"w", "s"}}})
	if want := `c 2 18446744073709551614 ["a","b","c"]`; got[2] != want {
		t.Errorf("int128: got %q want %q", got[2], want)
	}
	// An absent weight makes the total absent from that hop on.
	got = followRun(t, followEdges("a b", "b c 4"), followSeeds("a"), FollowConfig{Sums: []FollowSum{{"w", "s"}}})
	if want := `c 2 <nil> ["a","b","c"]`; got[2] != want {
		t.Errorf("absent: got %q want %q", got[2], want)
	}
}

func TestFollowErrors(t *testing.T) {
	cases := map[string]struct {
		edges []Record
		cfg   FollowConfig
		want  string
	}{
		"negative": {followEdges("a b -1"), FollowConfig{Mode: FollowLowestSum, Lowest: FollowSum{"w", "s"}}, "negative w"},
		"absent":   {followEdges("a b"), FollowConfig{Mode: FollowLowestSum, Lowest: FollowSum{"w", "s"}}, "has no w"},
		"depth":    {followEdges("a b 1"), FollowConfig{Mode: FollowLowestSum, Lowest: FollowSum{"w", "s"}, MaxDepth: 2}, "depth limit"},
		"collide":  {followEdges("a b 1"), FollowConfig{Sums: []FollowSum{{"w", "city"}}}, "already a seed column"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil || !strings.Contains(fmt.Sprint(r), c.want) {
					t.Fatalf("panic %v, want %q", r, c.want)
				}
			}()
			followRun(t, c.edges, followSeeds("a"), c.cfg)
		})
	}
}
