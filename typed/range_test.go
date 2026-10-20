package typed

import (
	"slices"
	"testing"
)

type rangeRow struct {
	N int64 `ssql:"n"`
}

func rangeMk(v int64) rangeRow { return rangeRow{N: v} }

func collectRange(t *testing.T, start, step, count int64, shards int) []int64 {
	t.Helper()
	var got []int64
	for r := range RangeParallel(start, step, count, shards, rangeMk).Serial() {
		got = append(got, r.N)
	}
	slices.Sort(got)
	return got
}

func serialRange(start, step, count int64) []int64 {
	var got []int64
	for r := range Range(start, step, count, rangeMk) {
		got = append(got, r.N)
	}
	slices.Sort(got)
	return got
}

func TestRangeSerial(t *testing.T) {
	got := serialRange(1, 1, 5)
	if want := []int64{1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("Range(1,1,5) = %v, want %v", got, want)
	}
	got = serialRange(10, -3, 4)
	if want := []int64{1, 4, 7, 10}; !slices.Equal(got, want) {
		t.Fatalf("Range(10,-3,4) = %v, want %v", got, want)
	}
	if n := len(serialRange(1, 1, 0)); n != 0 {
		t.Fatalf("Range count 0 yielded %d rows", n)
	}
	if n := len(serialRange(1, 1, -5)); n != 0 {
		t.Fatalf("Range count -5 yielded %d rows", n)
	}
}

// Every sharding agrees with the serial form: more shards than rows,
// a count that does not divide, one shard, and a negative step.
func TestRangeParallelMatchesSerial(t *testing.T) {
	cases := []struct {
		start, step, count int64
		shards             int
	}{
		{1, 1, 5, 24},   // more shards than rows
		{1, 1, 7, 4},    // 7/4 → chunk 2 → shard 4 starts past the end
		{0, 5, 10, 3},   // 10/3 → chunk 4 → shards 4,4,2
		{1, 1, 100, 1},  // one shard
		{10, -3, 4, 2},  // negative step
		{1, 1, 1000, 0}, // GOMAXPROCS
	}
	for _, c := range cases {
		got := collectRange(t, c.start, c.step, c.count, c.shards)
		want := serialRange(c.start, c.step, c.count)
		if !slices.Equal(got, want) {
			t.Errorf("RangeParallel(%d,%d,%d,%d) = %v, want %v", c.start, c.step, c.count, c.shards, got, want)
		}
	}
}

func TestRangeParallelEmpty(t *testing.T) {
	s := RangeParallel(1, 1, 0, 4, rangeMk)
	if s.Shards() != 0 {
		t.Fatalf("count 0 made %d shards", s.Shards())
	}
	if n := len(collectRange(t, 1, 1, 0, 4)); n != 0 {
		t.Fatalf("count 0 yielded %d rows", n)
	}
}

// A consumer that stops early stops the shard: no further mk calls.
func TestRangeEarlyStop(t *testing.T) {
	calls := 0
	seq := Range(1, 1, 1000, func(v int64) rangeRow { calls++; return rangeRow{N: v} })
	for r := range seq {
		if r.N == 3 {
			break
		}
	}
	if calls != 3 {
		t.Fatalf("mk called %d times after a stop at 3", calls)
	}
	for _, shard := range RangeParallel(1, 1, 1000, 4, rangeMk).shards {
		seen := 0
		for range shard {
			seen++
			if seen == 2 {
				break
			}
		}
		if seen != 2 {
			t.Fatalf("shard yielded %d after a stop at 2", seen)
		}
	}
}
