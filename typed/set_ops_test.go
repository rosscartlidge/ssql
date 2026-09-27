package typed

import (
	"slices"
	"sort"
	"testing"
)

type setRow struct {
	ID string
	V  string
}

type setRight struct {
	Cust string
	W    int64
}

func setRowNames(rows []setRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.ID+":"+r.V)
	}
	return out
}

func TestExceptIntersectWholeRow(t *testing.T) {
	left := slices.Values([]setRow{{"a", "1"}, {"b", "2"}, {"a", "1"}, {"c", "3"}, {"a", "1"}})
	right := slices.Values([]setRow{{"a", "1"}, {"d", "4"}})
	id := func(r setRow) setRow { return r }

	cases := []struct {
		name string
		got  []setRow
		want []string
	}{
		{"except distinct", slices.Collect(Except(right, id, id, false)(left)), []string{"b:2", "c:3"}},
		{"except all keeps duplicates", slices.Collect(Except(right, id, id, true)(left)), []string{"b:2", "c:3"}},
		{"EXCEPT ALL cancels one", slices.Collect(ExceptAll(right)(left)), []string{"b:2", "a:1", "c:3", "a:1"}},
		{"intersect distinct", slices.Collect(Intersect(right, id, id, false)(left)), []string{"a:1"}},
		{"intersect all keeps duplicates", slices.Collect(Intersect(right, id, id, true)(left)), []string{"a:1", "a:1", "a:1"}},
		{"INTERSECT ALL min count", slices.Collect(IntersectAll(right)(left)), []string{"a:1"}},
	}
	for _, c := range cases {
		if got := setRowNames(c.got); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestExceptIntersectKeyed(t *testing.T) {
	leftRows := []setRow{{"a", "x"}, {"b", "y"}, {"a", "z"}, {"a", "x"}}
	rightRows := []setRight{{"a", 1}, {"a", 2}, {"d", 3}}
	left := slices.Values(leftRows)
	right := slices.Values(rightRows)
	lk := func(r setRow) string { return r.ID }
	rk := func(r setRight) string { return r.Cust }

	cases := []struct {
		name string
		got  []setRow
		want []string
	}{
		{"anti-join distinct", slices.Collect(Except(right, lk, rk, false)(left)), []string{"b:y"}},
		{"semi-join distinct", slices.Collect(Intersect(right, lk, rk, false)(left)), []string{"a:x", "a:z"}},
		{"semi-join all", slices.Collect(Intersect(right, lk, rk, true)(left)), []string{"a:x", "a:z", "a:x"}},
	}
	for _, c := range cases {
		if got := setRowNames(c.got); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}

	// Parallel forms: a per-row filter, so the multiset of results is
	// the serial all=true result; shard order is not input order.
	for n := 1; n <= 4; n++ {
		stream := ParallelFromSlice(leftRows, n)
		got := setRowNames(slices.Collect(IntersectParallel(stream, right, lk, rk).Serial()))
		sort.Strings(got)
		if want := []string{"a:x", "a:x", "a:z"}; !slices.Equal(got, want) {
			t.Errorf("IntersectParallel n=%d: got %v want %v", n, got, want)
		}
		got = setRowNames(slices.Collect(ExceptParallel(stream, right, lk, rk).Serial()))
		if want := []string{"b:y"}; !slices.Equal(got, want) {
			t.Errorf("ExceptParallel n=%d: got %v want %v", n, got, want)
		}
		// The distinct form composes with DistinctParallel.
		got = setRowNames(slices.Collect(DistinctParallel(IntersectParallel(stream, right, lk, rk), func(r setRow) setRow { return r })))
		sort.Strings(got)
		if want := []string{"a:x", "a:z"}; !slices.Equal(got, want) {
			t.Errorf("distinct IntersectParallel n=%d: got %v want %v", n, got, want)
		}
	}
}
