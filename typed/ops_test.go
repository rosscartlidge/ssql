package typed

import (
	"strings"
	"slices"
	"testing"
)

func TestWhere(t *testing.T) {
	in := []int{1, 2, 3, 4, 5}
	got := slices.Collect(Where(func(v int) bool { return v%2 == 0 })(slices.Values(in)))
	want := []int{2, 4}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWhereEmptyInput(t *testing.T) {
	got := slices.Collect(Where(func(v int) bool { return true })(slices.Values([]int{})))
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestWhereEarlyTermination(t *testing.T) {
	in := []int{1, 2, 3, 4, 5}
	seen := 0
	for v := range Where(func(v int) bool { return true })(slices.Values(in)) {
		seen++
		if v == 2 {
			break
		}
	}
	if seen != 2 {
		t.Errorf("early break should stop iteration after 2 items, saw %d", seen)
	}
}

func TestLimit(t *testing.T) {
	in := []int{1, 2, 3, 4, 5}
	got := slices.Collect(Limit[int](3)(slices.Values(in)))
	want := []int{1, 2, 3}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLimitZero(t *testing.T) {
	in := []int{1, 2, 3}
	got := slices.Collect(Limit[int](0)(slices.Values(in)))
	if len(got) != 0 {
		t.Errorf("Limit(0) should yield nothing, got %v", got)
	}
}

func TestLimitLargerThanInput(t *testing.T) {
	in := []int{1, 2, 3}
	got := slices.Collect(Limit[int](10)(slices.Values(in)))
	if !slices.Equal(got, in) {
		t.Errorf("Limit > len should yield everything, got %v", got)
	}
}

func TestSkip(t *testing.T) {
	in := []int{1, 2, 3, 4, 5}
	got := slices.Collect(Skip[int](2)(slices.Values(in)))
	want := []int{3, 4, 5}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSkipMoreThanInput(t *testing.T) {
	in := []int{1, 2, 3}
	got := slices.Collect(Skip[int](10)(slices.Values(in)))
	if len(got) != 0 {
		t.Errorf("Skip > len should yield nothing, got %v", got)
	}
}

func TestSelectChangesType(t *testing.T) {
	in := []int{1, 2, 3}
	got := slices.Collect(Select(func(v int) string { return string(rune('a' + v - 1)) })(slices.Values(in)))
	want := []string{"a", "b", "c"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestHashJoinInner(t *testing.T) {
	type L struct{ K, V string }
	type R struct{ K, Extra string }
	type O struct{ V, Extra string }

	left := []L{{K: "a", V: "1"}, {K: "b", V: "2"}, {K: "z", V: "9"}}
	right := []R{{K: "a", Extra: "Apple"}, {K: "b", Extra: "Banana"}}

	got := slices.Collect(HashJoin(slices.Values(left), slices.Values(right),
		func(l L) string { return l.K },
		func(r R) string { return r.K },
		func(l L, r R) O { return O{V: l.V, Extra: r.Extra} },
	))
	want := []O{{V: "1", Extra: "Apple"}, {V: "2", Extra: "Banana"}}
	if !slices.Equal(got, want) {
		t.Errorf("inner join mismatch:\n  got:  %#v\n  want: %#v", got, want)
	}
}

func TestHashJoinNoMatches(t *testing.T) {
	type T struct{ K, V string }
	left := []T{{K: "x", V: "1"}}
	right := []T{{K: "y", V: "2"}}
	got := slices.Collect(HashJoin(slices.Values(left), slices.Values(right),
		func(l T) string { return l.K },
		func(r T) string { return r.K },
		func(l, r T) T { return l },
	))
	if len(got) != 0 {
		t.Errorf("no matches should yield empty, got %v", got)
	}
}

func TestHashJoinDuplicateRightKeys(t *testing.T) {
	// HashJoin only keeps the last R per key. Documented behavior.
	type T struct{ K, V string }
	left := []T{{K: "a", V: "L"}}
	right := []T{{K: "a", V: "R1"}, {K: "a", V: "R2"}}
	got := slices.Collect(HashJoin(slices.Values(left), slices.Values(right),
		func(l T) string { return l.K },
		func(r T) string { return r.K },
		func(l, r T) string { return l.V + "+" + r.V },
	))
	want := []string{"L+R2"}
	if !slices.Equal(got, want) {
		t.Errorf("duplicate-right-key handling: got %v, want %v", got, want)
	}
}

func TestHashJoinEmptyRight(t *testing.T) {
	type T struct{ K, V string }
	got := slices.Collect(HashJoin(slices.Values([]T{{K: "a"}}), slices.Values([]T{}),
		func(l T) string { return l.K },
		func(r T) string { return r.K },
		func(l, r T) T { return l },
	))
	if len(got) != 0 {
		t.Errorf("empty right should yield empty result, got %v", got)
	}
}

func TestHashJoinSized(t *testing.T) {
	type T struct{ K, V string }
	left := []T{{K: "a", V: "1"}, {K: "z", V: "9"}}
	right := []T{{K: "a", V: "Apple"}}
	got := slices.Collect(HashJoinSized(slices.Values(left), slices.Values(right), len(right),
		func(l T) string { return l.K },
		func(r T) string { return r.K },
		func(l, r T) string { return l.V + ":" + r.V },
	))
	want := []string{"1:Apple"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestHashJoinSizedZeroHint(t *testing.T) {
	// Hint of 0 should still work — falls back to default growth.
	type T struct{ K, V string }
	got := slices.Collect(HashJoinSized(slices.Values([]T{{K: "a"}}), slices.Values([]T{{K: "a", V: "X"}}), 0,
		func(l T) string { return l.K },
		func(r T) string { return r.K },
		func(l, r T) string { return r.V },
	))
	if !slices.Equal(got, []string{"X"}) {
		t.Errorf("got %v", got)
	}
}

func TestHashJoinMulti(t *testing.T) {
	type T struct{ K, V string }
	left := []T{{K: "a", V: "L"}, {K: "b", V: "M"}}
	right := []T{{K: "a", V: "R1"}, {K: "a", V: "R2"}, {K: "b", V: "R3"}}
	got := slices.Collect(HashJoinMulti(slices.Values(left), slices.Values(right),
		func(l T) string { return l.K },
		func(r T) string { return r.K },
		func(l, r T) string { return l.V + "+" + r.V },
	))
	slices.Sort(got)
	want := []string{"L+R1", "L+R2", "M+R3"}
	if !slices.Equal(got, want) {
		t.Errorf("multi join: got %v, want %v", got, want)
	}
}

func TestLeftJoin(t *testing.T) {
	type L struct{ K, V string }
	type R struct{ K, Extra string }
	type O struct {
		V     string
		Extra string
		Found bool
	}
	left := []L{{K: "a", V: "1"}, {K: "z", V: "9"}}
	right := []R{{K: "a", Extra: "Apple"}}
	got := slices.Collect(LeftJoin(slices.Values(left), slices.Values(right),
		func(l L) string { return l.K },
		func(r R) string { return r.K },
		func(l L, r R, found bool) O { return O{V: l.V, Extra: r.Extra, Found: found} },
	))
	want := []O{
		{V: "1", Extra: "Apple", Found: true},
		{V: "9", Extra: "", Found: false},
	}
	if !slices.Equal(got, want) {
		t.Errorf("left join: got %#v, want %#v", got, want)
	}
}

func TestRightJoin(t *testing.T) {
	type T struct{ K, V string }
	left := []T{{K: "a", V: "L"}}
	right := []T{{K: "a", V: "R"}, {K: "z", V: "Zonly"}}
	type O struct {
		L, R  string
		Found bool
	}
	got := slices.Collect(RightJoin(slices.Values(left), slices.Values(right),
		func(l T) string { return l.K },
		func(r T) string { return r.K },
		func(l, r T, found bool) O { return O{L: l.V, R: r.V, Found: found} },
	))
	want := []O{
		{L: "L", R: "R", Found: true},
		{L: "", R: "Zonly", Found: false},
	}
	if !slices.Equal(got, want) {
		t.Errorf("right join: got %#v, want %#v", got, want)
	}
}

func TestFullJoin(t *testing.T) {
	type T struct{ K, V string }
	left := []T{{K: "a", V: "L1"}, {K: "b", V: "L2"}}
	right := []T{{K: "a", V: "R1"}, {K: "c", V: "R3"}}
	type O struct {
		L, R       string
		LFound     bool
		RFound     bool
	}
	got := slices.Collect(FullJoin(slices.Values(left), slices.Values(right),
		func(l T) string { return l.K },
		func(r T) string { return r.K },
		func(l, r T, lf, rf bool) O { return O{L: l.V, R: r.V, LFound: lf, RFound: rf} },
	))
	// Ordering: matched + left-only in left order, then right-only in
	// (non-deterministic) map order. Sort for stable comparison.
	cmpO := func(a, b O) int {
		if a.L != b.L {
			if a.L < b.L {
				return -1
			}
			return 1
		}
		if a.R < b.R {
			return -1
		}
		if a.R > b.R {
			return 1
		}
		return 0
	}
	slices.SortFunc(got, cmpO)
	want := []O{
		{L: "", R: "R3", LFound: false, RFound: true},
		{L: "L1", R: "R1", LFound: true, RFound: true},
		{L: "L2", R: "", LFound: true, RFound: false},
	}
	slices.SortFunc(want, cmpO)
	if !slices.Equal(got, want) {
		t.Errorf("full join: got %#v, want %#v", got, want)
	}
}

func TestComposition(t *testing.T) {
	// Where -> Limit -> Skip composition.
	in := slices.Values([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	pipe := Skip[int](1)(Limit[int](5)(Where(func(v int) bool { return v%2 == 1 })(in)))
	got := slices.Collect(pipe)
	want := []int{3, 5, 7, 9}
	if !slices.Equal(got, want) {
		t.Errorf("composed pipeline: got %v, want %v", got, want)
	}
}

func TestSortByAsc(t *testing.T) {
	in := []int{3, 1, 4, 1, 5, 9, 2, 6}
	got := slices.Collect(SortBy(func(v int) int { return v })(slices.Values(in)))
	want := []int{1, 1, 2, 3, 4, 5, 6, 9}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortByDesc(t *testing.T) {
	in := []int{3, 1, 4, 1, 5, 9, 2, 6}
	got := slices.Collect(SortByDesc(func(v int) int { return v })(slices.Values(in)))
	want := []int{9, 6, 5, 4, 3, 2, 1, 1}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortByOnStruct(t *testing.T) {
	type row struct {
		Name string
		Age  int64
	}
	in := []row{{"Carol", 42}, {"Alice", 30}, {"Bob", 25}}
	got := slices.Collect(SortBy(func(r row) int64 { return r.Age })(slices.Values(in)))
	want := []row{{"Bob", 25}, {"Alice", 30}, {"Carol", 42}}
	if !slices.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestSortByEmpty(t *testing.T) {
	got := slices.Collect(SortBy(func(v int) int { return v })(slices.Values([]int{})))
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestSortByEarlyTermination(t *testing.T) {
	in := []int{3, 1, 4, 1, 5, 9, 2, 6}
	seen := 0
	for v := range SortBy(func(x int) int { return x })(slices.Values(in)) {
		seen++
		if v == 2 {
			break
		}
	}
	// Sorted: 1,1,2,... — break after seeing 2 means seen==3.
	if seen != 3 {
		t.Errorf("expected 3 iterations before break, got %d", seen)
	}
}

func TestSortByStable(t *testing.T) {
	type row struct {
		Key  int
		Tag  string
	}
	in := []row{{1, "a"}, {2, "b"}, {1, "c"}, {2, "d"}}
	got := slices.Collect(SortByStable(func(r row) int { return r.Key })(slices.Values(in)))
	// Equal-key entries should preserve original order: a before c, b before d.
	want := []row{{1, "a"}, {1, "c"}, {2, "b"}, {2, "d"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestDistinctByKey(t *testing.T) {
	type row struct {
		ID   int
		Name string
	}
	in := []row{{1, "Alice"}, {2, "Bob"}, {1, "Alice2"}, {3, "Carol"}, {2, "Bob2"}}
	got := slices.Collect(Distinct(func(r row) int { return r.ID })(slices.Values(in)))
	want := []row{{1, "Alice"}, {2, "Bob"}, {3, "Carol"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestDistinctEmpty(t *testing.T) {
	got := slices.Collect(Distinct(func(v int) int { return v })(slices.Values([]int{})))
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestDistinctNoDups(t *testing.T) {
	in := []int{1, 2, 3, 4}
	got := slices.Collect(Distinct(func(v int) int { return v })(slices.Values(in)))
	if !slices.Equal(got, in) {
		t.Errorf("got %v, want %v (no-op when no dups)", got, in)
	}
}

func TestDistinctEarlyTermination(t *testing.T) {
	in := []int{1, 2, 1, 3, 4}
	seen := 0
	for v := range Distinct(func(x int) int { return x })(slices.Values(in)) {
		seen++
		if v == 2 {
			break
		}
	}
	if seen != 2 {
		t.Errorf("expected 2 iterations, got %d", seen)
	}
}

func TestConcat(t *testing.T) {
	a := slices.Values([]int{1, 2})
	b := slices.Values([]int{3, 4})
	c := slices.Values([]int{5})
	got := slices.Collect(Concat(a, b, c))
	want := []int{1, 2, 3, 4, 5}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestConcatEmpty(t *testing.T) {
	got := slices.Collect(Concat[int]())
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestConcatPreservesDuplicates(t *testing.T) {
	a := slices.Values([]int{1, 2, 3})
	b := slices.Values([]int{2, 3, 4})
	got := slices.Collect(Concat(a, b))
	want := []int{1, 2, 3, 2, 3, 4}
	if !slices.Equal(got, want) {
		t.Errorf("Concat must preserve dups; got %v, want %v", got, want)
	}
}

func TestUnion(t *testing.T) {
	a := slices.Values([]int{1, 2, 3})
	b := slices.Values([]int{2, 3, 4})
	got := slices.Collect(Union(func(v int) int { return v }, a, b))
	want := []int{1, 2, 3, 4}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestUnionEmpty(t *testing.T) {
	got := slices.Collect(Union(func(v int) int { return v }))
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestUnionByCompositeKey(t *testing.T) {
	type row struct {
		Region  string
		Product string
	}
	a := slices.Values([]row{{"N", "A"}, {"S", "B"}})
	b := slices.Values([]row{{"N", "A"}, {"E", "C"}}) // duplicate (N,A)
	got := slices.Collect(Union(func(r row) row { return r }, a, b))
	want := []row{{"N", "A"}, {"S", "B"}, {"E", "C"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestTakeLast(t *testing.T) {
	in := func(yield func(int) bool) {
		for i := 1; i <= 7; i++ {
			if !yield(i) {
				return
			}
		}
	}
	var got []int
	for v := range TakeLast[int](3)(in) {
		got = append(got, v)
	}
	if len(got) != 3 || got[0] != 5 || got[1] != 6 || got[2] != 7 {
		t.Errorf("TakeLast(3) = %v, want [5 6 7]", got)
	}
	got = nil
	for v := range TakeLast[int](0)(in) {
		got = append(got, v)
	}
	if len(got) != 0 {
		t.Errorf("TakeLast(0) = %v, want nothing", got)
	}
}

func TestReadLinesFromReader(t *testing.T) {
	var got []Line
	for l := range ReadLinesFromReader(strings.NewReader("a\nb\n")) {
		got = append(got, l)
	}
	if len(got) != 2 || got[0].LineNumber != 1 || got[1].Line != "b" {
		t.Errorf("lines = %+v", got)
	}
}
