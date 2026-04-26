package typed

import (
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
