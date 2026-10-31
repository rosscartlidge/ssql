package typed

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

type followEdge struct {
	Src, Dst string
	Mins     int64
}

type followSeed struct{ City string }

type followAcc struct{ Mins ssql.Int128 }

func followSpecFor(mode FollowMode) FollowSpec[followSeed, followEdge, string, followAcc, string] {
	key := func(s string) (string, bool) { return s, s != "" }
	fs := FollowSpec[followSeed, followEdge, string, followAcc, string]{
		Mode:     mode,
		SeedNode: func(s followSeed) (string, bool) { return key(s.City) },
		Src:      func(e followEdge) (string, bool) { return key(e.Src) },
		Dst:      func(e followEdge) (string, bool) { return key(e.Dst) },
		Swap:     func(e followEdge) followEdge { e.Src, e.Dst = e.Dst, e.Src; return e },
		Step:     func(a followAcc, e followEdge) followAcc { a.Mins = a.Mins.Add(ssql.Int128From(e.Mins)); return a },
		Emit: func(s followSeed, node string, depth int64, a followAcc) string {
			return fmt.Sprintf("%s>%s %d %v", s.City, node, depth, a.Mins)
		},
	}
	if mode == FollowLowestSum {
		fs.CmpLowest = func(a, b followAcc) int { return a.Mins.Cmp(b.Mins) }
		fs.Negative = func(e followEdge) bool { return e.Mins < 0 }
		fs.LowestField = "mins"
	} else {
		fs.CmpSums = func(a, b followAcc) int { return a.Mins.Cmp(b.Mins) }
	}
	return fs
}

var followFlights = []followEdge{
	{"SYD", "MEL", 90}, {"MEL", "SYD", 90}, {"SYD", "BNE", 95}, {"BNE", "CNS", 140}, {"MEL", "ADL", 75},
	{"ADL", "PER", 190}, {"MEL", "HBA", 75}, {"BNE", "PER", 330}, {"CNS", "DRW", 150}, {"PER", "DRW", 210},
}

// TestFollowMatchesRecordLane: the typed walk is the record walk (one
// package does both); this pins the adapter, mode by mode.
func TestFollowMatchesRecordLane(t *testing.T) {
	var recEdges []ssql.Record
	for _, e := range followFlights {
		recEdges = append(recEdges, ssql.MakeMutableRecord().String("src", e.Src).String("dst", e.Dst).Int("mins", e.Mins).Freeze())
	}
	seed := ssql.MakeMutableRecord().String("city", "SYD").Freeze()
	for _, mode := range []FollowMode{FollowFewestHops, FollowLowestSum, FollowAllPaths} {
		cfg := ssql.FollowConfig{NodeField: "city", SrcField: "src", DstField: "dst", DepthField: "hops", Mode: ssql.FollowMode(mode)}
		if mode == FollowLowestSum {
			cfg.Lowest = ssql.FollowSum{Field: "mins", Name: "total"}
		} else {
			cfg.Sums = []ssql.FollowSum{{Field: "mins", Name: "total"}}
		}
		var want []string
		for r := range ssql.Follow(slices.Values(recEdges), cfg)(slices.Values([]ssql.Record{seed})) {
			node, _ := ssql.Get[string](r, "city")
			hops, _ := ssql.Get[int64](r, "hops")
			total, _ := ssql.Get[any](r, "total")
			want = append(want, fmt.Sprintf("SYD>%s %d %v", node, hops, total))
		}
		got := slices.Collect(Follow(slices.Values(followFlights), followSpecFor(mode))(slices.Values([]followSeed{{"SYD"}})))
		if !slices.Equal(got, want) {
			t.Errorf("mode %d:\ngot  %v\nwant %v", mode, got, want)
		}
	}
}

func TestFollowParallelMatchesSerial(t *testing.T) {
	var seeds []followSeed
	for i := 0; i < 200; i++ {
		seeds = append(seeds, followSeed{[]string{"SYD", "MEL", "BNE", "PER", "HBA", ""}[i%6]})
	}
	for _, mode := range []FollowMode{FollowFewestHops, FollowLowestSum, FollowAllPaths} {
		fs := followSpecFor(mode)
		serial := slices.Collect(Follow(slices.Values(followFlights), fs)(slices.Values(seeds)))
		var parallel []string
		for v := range FollowParallel(ParallelFromSlice(seeds, 8), slices.Values(followFlights), fs).Serial() {
			parallel = append(parallel, v)
		}
		// ParallelFromSlice is an unordered stream, so Serial fans in:
		// the same rows, in shard interleaving.
		slices.Sort(serial)
		slices.Sort(parallel)
		if !slices.Equal(serial, parallel) {
			t.Errorf("mode %d: parallel differs from serial (%d vs %d rows)", mode, len(parallel), len(serial))
		}
	}
}

func TestFollowNegativeWeight(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "negative mins") {
			t.Fatalf("want the negative-weight error, got %v", r)
		}
	}()
	edges := []followEdge{{"A", "B", -1}}
	_ = slices.Collect(Follow(slices.Values(edges), followSpecFor(FollowLowestSum))(slices.Values([]followSeed{{"A"}})))
}
