package typed

import (
	"slices"
	"testing"
)

type sale struct {
	Region string
	Amount float64
}

func TestStandaloneCount(t *testing.T) {
	if got := Count(slices.Values([]int{1, 2, 3})); got != 3 {
		t.Errorf("Count: got %d, want 3", got)
	}
	if got := Count(slices.Values([]int{})); got != 0 {
		t.Errorf("Count empty: got %d, want 0", got)
	}
}

func TestStandaloneSum(t *testing.T) {
	in := []sale{{Region: "N", Amount: 10}, {Region: "S", Amount: 20}, {Region: "N", Amount: 5}}
	got := Sum(slices.Values(in), func(s sale) float64 { return s.Amount })
	if got != 35 {
		t.Errorf("Sum: got %v, want 35", got)
	}
}

func TestStandaloneMinMax(t *testing.T) {
	in := []sale{{Amount: 10}, {Amount: 5}, {Amount: 20}}
	min, ok := Min(slices.Values(in), func(s sale) float64 { return s.Amount })
	if !ok || min != 5 {
		t.Errorf("Min: got %v ok=%v", min, ok)
	}
	max, ok := Max(slices.Values(in), func(s sale) float64 { return s.Amount })
	if !ok || max != 20 {
		t.Errorf("Max: got %v ok=%v", max, ok)
	}
}

func TestStandaloneMinMaxEmpty(t *testing.T) {
	_, ok := Min(slices.Values([]sale{}), func(s sale) float64 { return s.Amount })
	if ok {
		t.Errorf("Min on empty should report ok=false")
	}
}

func TestStandaloneAvg(t *testing.T) {
	in := []sale{{Amount: 10}, {Amount: 20}, {Amount: 30}}
	avg, n := Avg(slices.Values(in), func(s sale) float64 { return s.Amount })
	if avg != 20 || n != 3 {
		t.Errorf("Avg: got avg=%v n=%v, want 20, 3", avg, n)
	}
}

func TestGroupByCount(t *testing.T) {
	in := []sale{
		{Region: "N", Amount: 10},
		{Region: "S", Amount: 20},
		{Region: "N", Amount: 5},
		{Region: "E", Amount: 15},
		{Region: "N", Amount: 1},
	}
	type Result struct {
		Region string
		N      int64
	}
	got := slices.Collect(GroupBy(slices.Values(in),
		func(s sale) string { return s.Region },
		func() Aggregator[sale, int64] { return &Counter[sale]{} },
		func(k string, n int64) Result { return Result{Region: k, N: n} },
	))
	// Insertion order preserved: N, S, E
	want := []Result{
		{Region: "N", N: 3},
		{Region: "S", N: 1},
		{Region: "E", N: 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("GroupBy count: got %#v, want %#v", got, want)
	}
}

func TestGroupBySum(t *testing.T) {
	in := []sale{
		{Region: "N", Amount: 10},
		{Region: "S", Amount: 20},
		{Region: "N", Amount: 5},
	}
	type Result struct {
		Region string
		Total  float64
	}
	got := slices.Collect(GroupBy(slices.Values(in),
		func(s sale) string { return s.Region },
		NewSummer(func(s sale) float64 { return s.Amount }),
		func(k string, total float64) Result { return Result{Region: k, Total: total} },
	))
	want := []Result{
		{Region: "N", Total: 15},
		{Region: "S", Total: 20},
	}
	if !slices.Equal(got, want) {
		t.Errorf("GroupBy sum: got %#v, want %#v", got, want)
	}
}

func TestGroupByAvg(t *testing.T) {
	in := []sale{
		{Region: "N", Amount: 10},
		{Region: "N", Amount: 30},
		{Region: "S", Amount: 100},
	}
	type Result struct {
		Region string
		Avg    float64
	}
	got := slices.Collect(GroupBy(slices.Values(in),
		func(s sale) string { return s.Region },
		NewAverager(func(s sale) float64 { return s.Amount }),
		func(k string, avg float64) Result { return Result{Region: k, Avg: avg} },
	))
	want := []Result{
		{Region: "N", Avg: 20},
		{Region: "S", Avg: 100},
	}
	if !slices.Equal(got, want) {
		t.Errorf("GroupBy avg: got %#v, want %#v", got, want)
	}
}

func TestGroupByOrderedStreaming(t *testing.T) {
	// Pre-sorted input — verifies streaming behaviour.
	in := []sale{
		{Region: "E", Amount: 1},
		{Region: "E", Amount: 2},
		{Region: "N", Amount: 10},
		{Region: "N", Amount: 20},
		{Region: "S", Amount: 5},
	}
	type Result struct {
		Region string
		Total  float64
	}
	got := slices.Collect(GroupByOrdered(slices.Values(in),
		func(s sale) string { return s.Region },
		NewSummer(func(s sale) float64 { return s.Amount }),
		func(k string, total float64) Result { return Result{Region: k, Total: total} },
	))
	want := []Result{
		{Region: "E", Total: 3},
		{Region: "N", Total: 30},
		{Region: "S", Total: 5},
	}
	if !slices.Equal(got, want) {
		t.Errorf("GroupByOrdered: got %#v, want %#v", got, want)
	}
}

func TestGroupByOrderedEmpty(t *testing.T) {
	type Result struct {
		Region string
		N      int64
	}
	got := slices.Collect(GroupByOrdered(slices.Values([]sale{}),
		func(s sale) string { return s.Region },
		func() Aggregator[sale, int64] { return &Counter[sale]{} },
		func(k string, n int64) Result { return Result{Region: k, N: n} },
	))
	if len(got) != 0 {
		t.Errorf("GroupByOrdered empty: got %#v, want []", got)
	}
}

func TestGroupBySingleGroup(t *testing.T) {
	in := []sale{{Region: "N", Amount: 10}, {Region: "N", Amount: 20}}
	type Result struct {
		Region string
		Total  float64
	}
	got := slices.Collect(GroupBy(slices.Values(in),
		func(s sale) string { return s.Region },
		NewSummer(func(s sale) float64 { return s.Amount }),
		func(k string, total float64) Result { return Result{Region: k, Total: total} },
	))
	want := []Result{{Region: "N", Total: 30}}
	if !slices.Equal(got, want) {
		t.Errorf("GroupBy single group: got %#v, want %#v", got, want)
	}
}

// ---- GroupByParallel tests ----

type RegionCount struct {
	Region string
	N      int64
}

type RegionTotal struct {
	Region string
	Total  float64
}

// sortBy lets us compare unordered group-by output between serial and
// parallel without depending on shard order.
func sortRegionCount(xs []RegionCount) {
	slices.SortFunc(xs, func(a, b RegionCount) int {
		if a.Region < b.Region {
			return -1
		}
		if a.Region > b.Region {
			return 1
		}
		return 0
	})
}

func sortRegionTotal(xs []RegionTotal) {
	slices.SortFunc(xs, func(a, b RegionTotal) int {
		if a.Region < b.Region {
			return -1
		}
		if a.Region > b.Region {
			return 1
		}
		return 0
	})
}

func TestGroupByParallelCount(t *testing.T) {
	in := []sale{
		{Region: "N", Amount: 10},
		{Region: "S", Amount: 20},
		{Region: "N", Amount: 5},
		{Region: "E", Amount: 15},
		{Region: "N", Amount: 1},
		{Region: "S", Amount: 7},
	}
	stream := ParallelFromSlice(in, 3)
	got := slices.Collect(GroupByParallel(stream,
		func(s sale) string { return s.Region },
		NewCounter[sale](),
		func(k string, n int64) RegionCount { return RegionCount{Region: k, N: n} },
	))
	sortRegionCount(got)
	want := []RegionCount{
		{Region: "E", N: 1},
		{Region: "N", N: 3},
		{Region: "S", N: 2},
	}
	if !slices.Equal(got, want) {
		t.Errorf("GroupByParallel count: got %#v, want %#v", got, want)
	}
}

func TestGroupByParallelSum(t *testing.T) {
	in := []sale{
		{Region: "N", Amount: 10},
		{Region: "S", Amount: 20},
		{Region: "N", Amount: 5},
		{Region: "E", Amount: 15},
		{Region: "N", Amount: 1},
		{Region: "S", Amount: 7},
	}
	stream := ParallelFromSlice(in, 4)
	got := slices.Collect(GroupByParallel(stream,
		func(s sale) string { return s.Region },
		NewParallelSummer(func(s sale) float64 { return s.Amount }),
		func(k string, total float64) RegionTotal { return RegionTotal{Region: k, Total: total} },
	))
	sortRegionTotal(got)
	want := []RegionTotal{
		{Region: "E", Total: 15},
		{Region: "N", Total: 16},
		{Region: "S", Total: 27},
	}
	if !slices.Equal(got, want) {
		t.Errorf("GroupByParallel sum: got %#v, want %#v", got, want)
	}
}

func TestGroupByParallelAvg(t *testing.T) {
	in := []sale{
		{Region: "N", Amount: 10},
		{Region: "N", Amount: 20},
		{Region: "S", Amount: 30},
	}
	stream := ParallelFromSlice(in, 2)
	got := slices.Collect(GroupByParallel(stream,
		func(s sale) string { return s.Region },
		NewParallelAverager(func(s sale) float64 { return s.Amount }),
		func(k string, avg float64) RegionTotal { return RegionTotal{Region: k, Total: avg} },
	))
	sortRegionTotal(got)
	want := []RegionTotal{
		{Region: "N", Total: 15},
		{Region: "S", Total: 30},
	}
	if !slices.Equal(got, want) {
		t.Errorf("GroupByParallel avg: got %#v, want %#v", got, want)
	}
}

func TestGroupByParallelEmptyStream(t *testing.T) {
	stream := ParallelFromSlice[sale](nil, 4)
	got := slices.Collect(GroupByParallel(stream,
		func(s sale) string { return s.Region },
		NewCounter[sale](),
		func(k string, n int64) RegionCount { return RegionCount{Region: k, N: n} },
	))
	if len(got) != 0 {
		t.Errorf("GroupByParallel empty: got %#v, want []", got)
	}
}

func TestGroupByParallelMatchesSerial(t *testing.T) {
	// Build a deterministic 1000-row input with 7 distinct regions.
	regions := []string{"N", "S", "E", "W", "NE", "NW", "SE"}
	in := make([]sale, 1000)
	for i := 0; i < 1000; i++ {
		in[i] = sale{Region: regions[i%len(regions)], Amount: float64(i)}
	}

	// Serial: count + sum.
	type Result struct {
		Region string
		N      int64
	}
	serial := slices.Collect(GroupBy(slices.Values(in),
		func(s sale) string { return s.Region },
		func() Aggregator[sale, int64] { return &Counter[sale]{} },
		func(k string, n int64) Result { return Result{Region: k, N: n} },
	))
	slices.SortFunc(serial, func(a, b Result) int {
		if a.Region < b.Region {
			return -1
		}
		if a.Region > b.Region {
			return 1
		}
		return 0
	})

	// Parallel: same workload, several shard counts.
	for _, n := range []int{1, 2, 4, 7, 16} {
		stream := ParallelFromSlice(in, n)
		par := slices.Collect(GroupByParallel(stream,
			func(s sale) string { return s.Region },
			NewCounter[sale](),
			func(k string, n int64) Result { return Result{Region: k, N: n} },
		))
		slices.SortFunc(par, func(a, b Result) int {
			if a.Region < b.Region {
				return -1
			}
			if a.Region > b.Region {
				return 1
			}
			return 0
		})
		if !slices.Equal(par, serial) {
			t.Errorf("GroupByParallel n=%d: parity broken vs serial.\n  serial=%#v\n  parallel=%#v", n, serial, par)
		}
	}
}
