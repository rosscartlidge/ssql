package typed

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

func strconvFloat(v float64) string { return strconv.FormatFloat(v, 'g', 12, 64) }
func fmtAny(v any) string           { return fmt.Sprint(v) }

// TestWindowFrameAggregates: the registry statistics over a frame in
// the typed lane (WAgg) — median and a quantile by ssql.QuantileCont
// over the frame's sorted values, the sample variance and deviation by
// Welford (absent below two rows), the mode with ties to the first seen
// — against values by hand over a three-row moving frame and the
// running frame. The record path's aggregates are the oracle in the
// equivalence suite (window_rolling_stats, window_rolling_set_list_mode,
// window_rolling_stats_bounded).
func TestWindowFrameAggregates(t *testing.T) {
	rows := []winRow{
		{Dept: "a", Name: "x", Salary: 10}, {Dept: "a", Name: "y", Salary: 30}, {Dept: "a", Name: "x", Salary: 20},
		{Dept: "a", Name: "z", Salary: 40}, {Dept: "a", Name: "z", Salary: 50},
	}
	num := func(r winRow) (float64, bool) { return float64(r.Salary), true }
	name := func(r winRow) (any, bool) { return r.Name, true }
	type out struct {
		med, p25, sd, v *float64
		mode            any
	}
	fp := func(res WindowRow, i int) *float64 {
		if v, ok := res.Float(i); ok {
			return &v
		}
		return nil
	}
	build := func(r winRow, res WindowRow) out {
		return out{fp(res, 0), fp(res, 1), fp(res, 2), fp(res, 3), res.Value(4)}
	}
	specs := []WindowSpec[winRow]{
		{Kind: WAgg, Agg: WAggQuantile, P: 0.5, Num: num},
		{Kind: WAgg, Agg: WAggQuantile, P: 0.25, Num: num},
		{Kind: WAgg, Agg: WAggStdDev, Num: num},
		{Kind: WAgg, Agg: WAggVariance, Num: num},
		{Kind: WAgg, Agg: WAggMode, Val: name},
	}
	order := func(a, b winRow) int { return int(a.Salary - b.Salary) }
	f := func(v float64) *float64 { return &v }
	near := func(a, b *float64) bool {
		if a == nil || b == nil {
			return a == b
		}
		return math.Abs(*a-*b) < 1e-9
	}
	check := func(name string, got, want []out) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %d rows, want %d", name, len(got), len(want))
		}
		for i := range want {
			g, w := got[i], want[i]
			if !near(g.med, w.med) || !near(g.p25, w.p25) || !near(g.sd, w.sd) || !near(g.v, w.v) || g.mode != w.mode {
				t.Errorf("%s row %d: got %s, want %s", name, i, fmtOut(g), fmtOut(w))
			}
		}
	}
	// Sorted by salary: x10 x20 y30 z40 z50. ROWS 1 PRECEDING, 1 FOLLOWING.
	moving := WindowClause[winRow]{Partition: func(winRow) string { return "" }, HasOrder: true, Compare: order,
		Frame: WindowFrame{Preceding: 1, Following: 1}, Specs: specs}
	wantMoving := []out{
		{f(15), f(12.5), f(math.Sqrt(50)), f(50), "x"}, // 10,20: mode x (both x)
		{f(20), f(15), f(10), f(100), "x"},             // 10,20,30: x twice
		{f(30), f(25), f(10), f(100), "x"},             // 20,30,40: tie x,y,z → first seen x
		{f(40), f(35), f(10), f(100), "z"},             // 30,40,50: z twice
		{f(45), f(42.5), f(math.Sqrt(50)), f(50), "z"}, // 40,50
	}
	var got []out
	for o := range Window([]WindowClause[winRow]{moving}, build)(slices.Values(rows)) {
		got = append(got, o)
	}
	check("moving", got, wantMoving)
	// The running frame: the first row's variance is absent (one row).
	running := moving
	running.Frame = WindowFrame{Preceding: -1, Following: 0}
	wantRunning := []out{
		{f(10), f(10), nil, nil, "x"},
		{f(15), f(12.5), f(math.Sqrt(50)), f(50), "x"},
		{f(20), f(15), f(10), f(100), "x"},
		{f(25), f(17.5), f(math.Sqrt(500.0 / 3)), f(500.0 / 3), "x"},
		{f(30), f(20), f(math.Sqrt(250)), f(250), "x"}, // x twice, z twice: x first
	}
	got = nil
	for o := range Window([]WindowClause[winRow]{running}, build)(slices.Values(rows)) {
		got = append(got, o)
	}
	check("running", got, wantRunning)
	// The whole-partition frame, computed once: every row the same.
	whole := moving
	whole.Frame = WindowFrame{Preceding: -1, Following: -1}
	got = nil
	for o := range WindowParallel([]WindowClause[winRow]{whole}, build)(ParallelFromSlice(rows, 2)).Serial() {
		got = append(got, o)
	}
	all := out{f(30), f(20), f(math.Sqrt(250)), f(250), "x"}
	check("whole", got, []out{all, all, all, all, all})
}

func fmtOut(o struct {
	med, p25, sd, v *float64
	mode            any
}) string {
	s := func(p *float64) string {
		if p == nil {
			return "nil"
		}
		return strconvFloat(*p)
	}
	return "{" + s(o.med) + " " + s(o.p25) + " " + s(o.sd) + " " + s(o.v) + " " + fmtAny(o.mode) + "}"
}

// TestWindowFrameSetAndList: count-distinct and string-agg over a frame
// in the typed lane — the distinct present values by ssql.DistinctKey,
// the present values in frame order joined by the separator (absent
// when none) — against values by hand over a three-row moving frame.
func TestWindowFrameSetAndList(t *testing.T) {
	rows := []winRow{{Name: "x", Salary: 1}, {Name: "y", Salary: 2}, {Name: "x", Salary: 3}, {Name: "", Salary: 4}, {Name: "z", Salary: 5}}
	c := WindowClause[winRow]{
		HasOrder: true, KeyInt: func(r winRow) int64 { return r.Salary },
		Frame: WindowFrame{Preceding: 1, Following: 1},
		Specs: []WindowSpec[winRow]{
			CountDistinctOf(func(r winRow) string { return r.Name }),
			StringAggOf(func(r winRow) string { return r.Name }, "+"),
			CountDistinctOf(func(r winRow) int64 { return r.Salary / 2 }),
		},
	}
	type out struct {
		n  int64
		s  any
		n2 int64
	}
	var got []out
	for o := range Window([]WindowClause[winRow]{c}, func(r winRow, res WindowRow) out {
		n, _ := res.Int(0)
		n2, _ := res.Int(2)
		return out{n, res.Value(1), n2}
	})(slices.Values(rows)) {
		got = append(got, o)
	}
	want := []out{
		{2, "x+y", 2},   // x,y            | 0,1
		{2, "x+y+x", 2}, // x,y,x          | 0,1,1
		{2, "y+x", 2},   // y,x,""         | 1,1,2
		{2, "x+z", 2},   // x,"",z         | 1,2,2
		{1, "z", 1},     // "",z           | 2,2
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// All missing: string-agg absent, count-distinct 0.
	empty := []winRow{{Name: "", Salary: 1}}
	for o := range Window([]WindowClause[winRow]{c}, func(r winRow, res WindowRow) out {
		n, _ := res.Int(0)
		return out{n, res.Value(1), 0}
	})(slices.Values(empty)) {
		if o.n != 0 || o.s != nil {
			t.Fatalf("all missing: got %v", o)
		}
	}
}
