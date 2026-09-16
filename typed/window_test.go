package typed

import (
	"slices"
	"testing"
)

type winRow struct {
	Dept   string
	Name   string
	Salary int64
	Prev   *int64
}

type winOut struct {
	Name string
	Rn   int64
	Rk   int64
	Run  float64
	Prev *int64
	Nth  *int64
}

// TestWindow: the typed window runtime mirrors ssql.Window — partitions
// in first-seen order, per-clause sort, rank ties, LAG absent at the
// partition edge, running sums, and every partition emitted (the first
// cut emitted only the first partition; the equivalence gate caught it).
func TestWindow(t *testing.T) {
	rows := []winRow{
		{"eng", "Alice", 95000, nil}, {"sales", "Bob", 65000, nil}, {"eng", "Carol", 105000, nil},
		{"eng", "Eve", 88000, nil}, {"sales", "Frank", 82000, nil}, {"eng", "Dan", 95000, nil}, // Dan ties Alice
	}
	clause := WindowClause[winRow]{
		Partition: func(r winRow) string { return r.Dept },
		HasOrder:  true,
		Compare: func(a, b winRow) int {
			if a.Salary < b.Salary {
				return -1
			}
			if a.Salary > b.Salary {
				return 1
			}
			return 0
		},
		Frame: WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec[winRow]{
			{Kind: WRowNumber},
			{Kind: WRank},
			{Kind: WSum, Num: func(r winRow) (float64, bool) { return float64(r.Salary), true }},
			{Kind: WLag, N: 1, Val: func(r winRow) (any, bool) { return r.Salary, true }},
			{Kind: WNth, N: 2, Val: func(r winRow) (any, bool) { return r.Salary, true }},
		},
	}
	build := func(r winRow, res []any) winOut {
		o := winOut{Name: r.Name}
		if v, ok := res[0].(int64); ok {
			o.Rn = v
		}
		if v, ok := res[1].(int64); ok {
			o.Rk = v
		}
		if v, ok := res[2].(float64); ok {
			o.Run = v
		}
		if v, ok := res[3].(int64); ok {
			o.Prev = &v
		}
		if v, ok := res[4].(int64); ok {
			o.Nth = &v
		}
		return o
	}
	got := slices.Collect(Window([]WindowClause[winRow]{clause}, build)(slices.Values(rows)))
	if len(got) != len(rows) {
		t.Fatalf("emitted %d rows, want %d (every partition)", len(got), len(rows))
	}
	// Output order: eng (first seen) sorted by salary, then sales.
	names := make([]string, len(got))
	for i, o := range got {
		names[i] = o.Name
	}
	want := []string{"Eve", "Alice", "Dan", "Carol", "Bob", "Frank"}
	if !slices.Equal(names, want) {
		t.Fatalf("output order %v, want %v", names, want)
	}
	byName := map[string]winOut{}
	for _, o := range got {
		byName[o.Name] = o
	}
	if byName["Dan"].Rk != 2 || byName["Alice"].Rk != 2 || byName["Carol"].Rk != 4 {
		t.Fatalf("rank ties wrong: Alice %d Dan %d Carol %d", byName["Alice"].Rk, byName["Dan"].Rk, byName["Carol"].Rk)
	}
	if byName["Carol"].Run != 88000+95000+95000+105000 {
		t.Fatalf("running sum for Carol = %v", byName["Carol"].Run)
	}
	if byName["Eve"].Prev != nil || byName["Bob"].Prev != nil {
		t.Fatalf("LAG at the partition edge must be absent")
	}
	if p := byName["Alice"].Prev; p == nil || *p != 88000 {
		t.Fatalf("LAG for Alice = %v", p)
	}
	if byName["Eve"].Nth != nil || byName["Alice"].Nth == nil || *byName["Alice"].Nth != 95000 {
		t.Fatalf("NTH_VALUE(2): Eve %v Alice %v", byName["Eve"].Nth, byName["Alice"].Nth)
	}

	t.Run("RANGE frame and no-order input order", func(t *testing.T) {
		rc := WindowClause[winRow]{
			HasOrder: true,
			Compare:  clause.Compare,
			RangeKey: func(r winRow) float64 { return float64(r.Salary) },
			Frame:    WindowFrame{Range: true, RangePreceding: 10000, RangeFollowing: 0},
			Specs:    []WindowSpec[winRow]{{Kind: WCount}},
		}
		out := slices.Collect(Window([]WindowClause[winRow]{rc}, func(r winRow, res []any) winOut {
			o := winOut{Name: r.Name}
			if v, ok := res[0].(int64); ok {
				o.Rn = v
			}
			return o
		})(slices.Values(rows)))
		cnt := map[string]int64{}
		for _, o := range out {
			cnt[o.Name] = o.Rn
		}
		// Salaries sorted: 65000 82000 88000 95000 95000 105000. Within 10000 below
		// (peers included): Bob 1; Frank 1 (65000 is 17000 below); Eve 2 (82000);
		// Alice 3 (88000 and the two 95000s — 82000 is 13000 below); Dan 3; Carol 3 (95000×2 + itself).
		wantCnt := map[string]int64{"Bob": 1, "Frank": 1, "Eve": 2, "Alice": 3, "Dan": 3, "Carol": 3}
		for n, w := range wantCnt {
			if cnt[n] != w {
				t.Errorf("RANGE count for %s = %d, want %d", n, cnt[n], w)
			}
		}
		// No ORDER BY anywhere: input order.
		plain := WindowClause[winRow]{Specs: []WindowSpec[winRow]{{Kind: WRowNumber}}}
		out2 := slices.Collect(Window([]WindowClause[winRow]{plain}, func(r winRow, res []any) winOut { return winOut{Name: r.Name} })(slices.Values(rows)))
		if out2[0].Name != "Alice" || out2[len(out2)-1].Name != "Dan" {
			t.Fatalf("without ORDER BY the output must be input order, got %v…%v", out2[0].Name, out2[len(out2)-1].Name)
		}
	})
}
