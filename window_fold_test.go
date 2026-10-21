package ssql

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func foldRows(n int) []Record {
	rows := make([]Record, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, NewRecord(map[string]any{"n": int64(i), "g": fmt.Sprint("g", i%2)}))
	}
	return rows
}

func collectField[T any](t *testing.T, out []Record, field string) []T {
	t.Helper()
	var vals []T
	for _, r := range out {
		v, ok := Get[T](r, field)
		if !ok {
			t.Fatalf("row %v: field %q missing or not %T", r, field, *new(T))
		}
		vals = append(vals, v)
	}
	return vals
}

// TestWindowStreamExprRunning: the default frame is a running fold — the
// state carries from row to row, so a recurrence is one window stage
// (Ross's Fibonacci fold needed group-by plus explode before). The int
// fold stays int64 (exact to 2^63, like group-by's since v4.121.2).
func TestWindowStreamExprRunning(t *testing.T) {
	cfg := WindowConfig{
		OrderBy: []OrderField{{Field: "n"}},
		Frame:   WindowFrame{Preceding: -1, Following: 0},
		Specs: []WindowSpec{
			{Function: WStreamExpr(`{a:0, b:1}`, `{a:b, b:a+b}`, `b`), ResultName: "fib"},
			{Function: WStreamExpr(`{s:0}`, `{s: s + n}`, `s`), ResultName: "run"},
		},
	}
	out := slices.Collect(Window([]WindowConfig{cfg})(slices.Values(foldRows(10))))
	if got, want := collectField[int64](t, out, "fib"), []int64{1, 2, 3, 5, 8, 13, 21, 34, 55, 89}; !slices.Equal(got, want) {
		t.Errorf("fib = %v, want %v", got, want)
	}
	if got, want := collectField[int64](t, out, "run"), []int64{1, 3, 6, 10, 15, 21, 28, 36, 45, 55}; !slices.Equal(got, want) {
		t.Errorf("run = %v, want %v", got, want)
	}
}

// TestWindowStreamExprFrames: a bounded, a following and a whole-partition
// frame restart the fold at the frame's first row, and the fold's sum
// equals WSum over the same frame (the independent oracle); partitions
// restart it too.
func TestWindowStreamExprFrames(t *testing.T) {
	frames := []WindowFrame{
		{Preceding: 1, Following: 0},
		{Preceding: 1, Following: 1},
		{Preceding: -1, Following: -1},
		{Preceding: 0, Following: 2},
	}
	for _, fr := range frames {
		cfg := WindowConfig{
			PartitionBy: []string{"g"},
			OrderBy:     []OrderField{{Field: "n"}},
			Frame:       fr,
			Specs: []WindowSpec{
				{Function: WStreamExpr(`{s:0}`, `{s: s + n}`, `s`), ResultName: "fold"},
				{Function: WSum("n"), ResultName: "sum"},
				{Function: WStreamExpr(`{first: nil}`, `{first: first ?? n}`, `first`), ResultName: "head"},
				{Function: WFirst("n"), ResultName: "first"},
			},
		}
		out := slices.Collect(Window([]WindowConfig{cfg})(slices.Values(foldRows(9))))
		if len(out) != 9 {
			t.Fatalf("frame %+v: %d rows", fr, len(out))
		}
		for _, r := range out {
			fold, _ := Get[int64](r, "fold")
			sum, _ := Get[float64](r, "sum")
			if float64(fold) != sum {
				t.Errorf("frame %+v row %v: fold %d != sum %v", fr, r, fold, sum)
			}
			head, _ := Get[int64](r, "head")
			first, _ := Get[int64](r, "first")
			if head != first {
				t.Errorf("frame %+v row %v: head %d != first %d", fr, r, head, first)
			}
		}
	}
}

// TestStreamWindowStreamExpr: the -presorted streaming path gives the
// materialised path's values for the running frame and a bounded frame,
// and resets at a partition boundary.
func TestStreamWindowStreamExpr(t *testing.T) {
	rows := foldRows(8)
	slices.SortStableFunc(rows, func(a, b Record) int {
		ga, _ := Get[string](a, "g")
		gb, _ := Get[string](b, "g")
		return strings.Compare(ga, gb)
	})
	for _, fr := range []WindowFrame{{Preceding: -1}, {Preceding: 2}} {
		cfg := WindowConfig{
			PartitionBy: []string{"g"},
			OrderBy:     []OrderField{{Field: "n"}},
			Frame:       fr,
			Specs: []WindowSpec{
				{Function: WStreamExpr(`{s:0, k:0}`, `{s: s + n, k: k + 1}`, `s * 10 + k`), ResultName: "v"},
			},
		}
		want := slices.Collect(Window([]WindowConfig{cfg})(slices.Values(rows)))
		got := slices.Collect(MustStreamWindow([]WindowConfig{cfg})(slices.Values(rows)))
		w, g := collectField[int64](t, want, "v"), collectField[int64](t, got, "v")
		if !slices.Equal(w, g) {
			t.Errorf("frame %+v: streaming %v != materialised %v", fr, g, w)
		}
	}
}

// TestWindowStreamExprAbsentAndTypes: a nil FINAL is the absent value (a
// fold that carries the previous row has none at the first), a float
// fold is float64, a string fold a string.
func TestWindowStreamExprAbsentAndTypes(t *testing.T) {
	cfg := WindowConfig{
		OrderBy: []OrderField{{Field: "n"}},
		Specs: []WindowSpec{
			{Function: WStreamExpr(`{prev: nil, cur: nil}`, `{prev: cur, cur: n}`, `prev`), ResultName: "lag"},
			{Function: WStreamExpr(`{s: 0}`, `{s: s + n / 2}`, `s`), ResultName: "half"},
			{Function: WStreamExpr(`{s: ""}`, `{s: s + string(n)}`, `s`), ResultName: "digits"},
		},
	}
	cfg.Frame = WindowFrame{Preceding: -1}
	out := slices.Collect(Window([]WindowConfig{cfg})(slices.Values(foldRows(3))))
	if _, ok := Get[any](out[0], "lag"); ok {
		t.Errorf("first row's lag should be absent, got %v", out[0])
	}
	if v, _ := Get[int64](out[2], "lag"); v != 2 {
		t.Errorf("third row's lag = %v, want 2", v)
	}
	if v, _ := Get[float64](out[2], "half"); v != 3 {
		t.Errorf("half = %v, want 3", v)
	}
	if v, _ := Get[string](out[2], "digits"); v != "123" {
		t.Errorf("digits = %q, want 123", v)
	}
}

// TestWindowStreamExprLoud: an unknown field inside EVERY and a wrong
// state shape panic with the expression in the message — never a silent
// zero.
func TestWindowStreamExprLoud(t *testing.T) {
	for _, c := range []struct{ init, every, final, want string }{
		{`{s:0}`, `{s: s + nosuch}`, `s`, "nosuch"},
		{`0`, `{s: s}`, `s`, "init expression must return object"},
		{`{s:0}`, `s + n`, `s`, "every expression must return object"},
	} {
		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%v: expected a panic", c)
				}
				if msg := fmt.Sprint(r); !strings.Contains(msg, c.want) || !strings.Contains(msg, "WStreamExpr") {
					t.Errorf("panic %q should mention %q and WStreamExpr", msg, c.want)
				}
			}()
			cfg := WindowConfig{Frame: WindowFrame{Preceding: -1}, Specs: []WindowSpec{{Function: WStreamExpr(c.init, c.every, c.final), ResultName: "x"}}}
			for range Window([]WindowConfig{cfg})(slices.Values(foldRows(2))) {
			}
		}()
	}
}

// TestWindowFuncCodeStreamExpr: the fold renders its constructor (what
// `generate go` emits) and describes itself for the typed emitter.
func TestWindowFuncCodeStreamExpr(t *testing.T) {
	fn := WStreamExpr(`{a:0, b:1}`, `{a:b, b:a+b}`, `b`)
	if got, want := WindowFuncCode(fn), `ssql.WStreamExpr("{a:0, b:1}", "{a:b, b:a+b}", "b")`; got != want {
		t.Errorf("WindowFuncCode = %s, want %s", got, want)
	}
	d := DescribeWindowFunc(fn)
	if d.Kind != "stream_expr" || d.Init != `{a:0, b:1}` || d.Every != `{a:b, b:a+b}` || d.Final != `b` {
		t.Errorf("DescribeWindowFunc = %+v", d)
	}
	if k := WindowFuncResultKind(fn); k != "" {
		t.Errorf("result kind %q, want \"\" (typed from the first value)", k)
	}
	if _, ok := WindowFuncField(fn); ok {
		t.Error("a fold names no single source field")
	}
}

// TestStreamExprAggCompilesOnce: the group aggregate shares one compiled
// fold across groups (it used to recompile per group) and still gives
// each group its own state.
func TestStreamExprAggCompilesOnce(t *testing.T) {
	agg := StreamExprAgg(`{s:0}`, `{s: s + n}`, `s`)
	a := agg(foldRows(3)).GetValue()
	b := agg(foldRows(4)).GetValue()
	if a != int64(6) || b != int64(10) {
		t.Errorf("got %v and %v, want 6 and 10", a, b)
	}
}
