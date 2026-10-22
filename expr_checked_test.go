package ssql

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/rosscartlidge/ssql/v4/exprfn"
)

// compileStd compiles the way every ssql site does: an untyped map env
// with undefined variables allowed, plus the standard options.
func compileStd(t *testing.T, src string) *vm.Program {
	t.Helper()
	p, err := expr.Compile(src, append([]expr.Option{expr.Env(map[string]any{}), expr.AllowUndefinedVariables()}, ExprStandardOptions()...)...)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return p
}

func TestExprIntOverflowIsAnError(t *testing.T) {
	env := map[string]any{"big": int64(math.MaxInt64), "small": int64(math.MinInt64), "two": int64(2), "n": int64(7)}
	for _, c := range []struct{ src, op string }{
		{"big + 1", "+"}, {"big + n", "+"}, {"small - 1", "-"}, {"big * two", "*"}, {"small * -1", "*"},
		{"(big - 1) + two", "+"}, {"n * (big - 1)", "*"},
	} {
		_, err := expr.Run(compileStd(t, c.src), env)
		var oe *exprfn.OverflowError
		if !errors.As(err, &oe) || oe.Op != c.op {
			t.Errorf("%s: want an *OverflowError for %s, got %v", c.src, c.op, err)
		}
		if err != nil && !strings.Contains(err.Error(), "integer overflow") {
			t.Errorf("%s: message %q should say integer overflow", c.src, err)
		}
	}
}

// Everything that is not an int64 pair keeps expr's own semantics,
// result kind included: the patch must be invisible there.
func TestExprCheckedArithmeticKeepsEverySemantics(t *testing.T) {
	three := int64(3)
	env := map[string]any{"n": int64(7), "f": 1.5, "s": "ab", "date": "2026-01-01", "len": int64(3), "ptr": &three}
	for _, c := range []struct {
		src  string
		want any
	}{
		{"n + 1", int(8)}, // expr's runtime makes every integer pair an int
		{"2 + 3", int(5)}, // two literals
		{"n * 2 - 1", int(13)},
		{"len(s) + n", int(9)}, // len() is an int
		{"-n * 3", int(-21)},
		{"n + f", 8.5},
		{"f * 2", 3.0},
		{"n / 2", 3.5},
		{`s + "c"`, "abc"},
		{"len + 1", int(4)},           // a field named like a builtin stays a field
		{`date + "!"`, "2026-01-01!"}, // and so does date
		{"n % 4", int(3)},
		{"n ** 2", 49.0},
		{"7 - 2 * 3 + 1", int(2)},
		{"n - ptr", int(4)}, // a pointer operand (a nullable window value) is dereferenced, as expr's own operator does
	} {
		got, err := expr.Run(compileStd(t, c.src), env)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %v (%T), want %v (%T)", c.src, got, got, c.want, c.want)
		}
	}
	// expr's own type error, with expr's own words
	_, err := expr.Run(compileStd(t, `s + n`), env)
	if err == nil || !strings.Contains(err.Error(), "invalid operation: string + int64") {
		t.Errorf("string + int64: want expr's type error, got %v", err)
	}
}

func TestSumAndAvgAccumulateIntegersExactly(t *testing.T) {
	recs := []Record{
		NewRecord(map[string]any{"v": int64(9007199254740993)}), // 2^53 + 1: not a float64
		NewRecord(map[string]any{"v": int64(9007199254740994)}),
	}
	if got := Sum("v")(recs).getValue(); got != int64(18014398509481987) {
		t.Errorf("int sum: got %v (%T), want the exact int64", got, got)
	}
	if got := Avg("v")(recs).getValue(); got != float64(9007199254740993.5) {
		t.Errorf("int avg: got %v", got)
	}
	// a float among the values makes the sum a float, as before
	mixed := append(recs, NewRecord(map[string]any{"v": 0.5}))
	if got := Sum("v")(mixed).getValue(); got != float64(18014398509481987)+0.5 {
		t.Errorf("mixed sum: got %v (%T)", got, got)
	}
	// numeric strings still count, as Get[float64] made them
	strs := []Record{NewRecord(map[string]any{"v": "2"}), NewRecord(map[string]any{"v": "3"})}
	if got := Sum("v")(strs).getValue(); got != float64(5) {
		t.Errorf("string sum: got %v (%T)", got, got)
	}
	// overflow is the library's error, not a wrap
	over := []Record{NewRecord(map[string]any{"v": int64(math.MaxInt64)}), NewRecord(map[string]any{"v": int64(1)})}
	err := Run(func() error { Sum("v")(over); return nil })
	var oe *exprfn.OverflowError
	if !errors.As(err, &oe) {
		t.Errorf("overflowing sum: want *OverflowError, got %v", err)
	}
}
