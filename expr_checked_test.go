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
		{"-small", "neg"}, {"sum([big, two], # * 2)", "*"},
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
	// sum() widens past 2^63 instead (DFC155 C): an integer sum is an int128
	if got, err := expr.Run(compileStd(t, "sum([big, 1])"), env); err != nil || got != MustParseInt128("9223372036854775808") {
		t.Errorf("sum([big, 1]): got %v, %v; want the int128 2^63", got, err)
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
		{"-n", int(-7)},
		{"-f", -1.5},
		{"-ptr", int(-3)},
		{"sum([1, 2, 3])", Int128From(6)}, // sum() is expr's: an int 0 start, each element added
		{"sum([1, 2.5])", 3.5},     // a float among the elements makes it a float
		{"sum([])", Int128From(0)},
		{"sum([1, 2], # * 2)", Int128From(6)}, // the mapper form
		{"sum(map([n, n], # + 1))", Int128From(16)},
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
	if got := Sum("v")(recs).getValue(); got != Int128From(18014398509481987) {
		t.Errorf("int sum: got %v (%T), want the exact Int128", got, got)
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
	// an int64 sum widens past 2^63, as DuckDB's SUM(BIGINT) does (DFC155 C)
	wide := []Record{NewRecord(map[string]any{"v": int64(math.MaxInt64)}), NewRecord(map[string]any{"v": int64(1)})}
	if got := Sum("v")(wide).getValue(); got != MustParseInt128("9223372036854775808") {
		t.Errorf("wide sum: got %v (%T)", got, got)
	}
	// an int128 sum that does not fit is the library's error, not a wrap
	over := []Record{NewRecord(map[string]any{"v": MaxInt128}), NewRecord(map[string]any{"v": int64(1)})}
	err := Run(func() error { Sum("v")(over); return nil })
	var oe *Int128OverflowError
	if !errors.As(err, &oe) {
		t.Errorf("overflowing sum: want *Int128OverflowError, got %v", err)
	}
}

// benchProgram compiles src the way every ssql site does (compileStd's
// options) for a benchmark.
func benchProgram(b *testing.B, src string) *vm.Program {
	b.Helper()
	p, err := expr.Compile(src, append([]expr.Option{expr.Env(map[string]any{}), expr.AllowUndefinedVariables()}, ExprStandardOptions()...)...)
	if err != nil {
		b.Fatalf("compile %q: %v", src, err)
	}
	return p
}

// BenchmarkExprCompare is the HEAD-vs-branch gate for DFC155 C's
// comparison patch: a `where`-shaped expression with three comparisons
// over the untyped env map ssql builds. The arithmetic patch (B) cost
// ~20 ns per operator; this says what the comparisons cost.
func BenchmarkExprCompare(b *testing.B) {
	p := benchProgram(b, `n > 5 && f < 2.5 && s == "ab"`)
	env := map[string]any{"n": int64(7), "f": 1.5, "s": "ab"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := expr.Run(p, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExprArith is the same gate for the arithmetic patch, which C
// extends with int128 arms and `/ % **`.
func BenchmarkExprArith(b *testing.B) {
	p := benchProgram(b, `n + n * 2 - 1`)
	env := map[string]any{"n": int64(7)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := expr.Run(p, env); err != nil {
			b.Fatal(err)
		}
	}
}

// DFC155 C: the VM's int128 arithmetic and comparisons, through the same
// patch — and every pair that holds no Int128 keeps expr's own result.
func TestExprInt128Semantics(t *testing.T) {
	w := MustParseInt128("123456789012345678901234567890")
	env := map[string]any{"w": w, "five": Int128From(5), "n": int64(7), "f": 1.5, "s": "ab", "neg": Int128From(-3), "xs": []any{w, int64(1)}}
	for _, c := range []struct {
		src  string
		want any
	}{
		{"w + 1", MustParseInt128("123456789012345678901234567891")},
		{"1 + w", MustParseInt128("123456789012345678901234567891")},
		{"w - w", Int128{}},
		{"w * n", MustParseInt128("864197523086419752308641975230")},
		{"n * five", Int128From(35)},
		{"five + n", Int128From(12)},
		{"-neg", Int128From(3)},
		{"w % 7", Int128{}}, {"neg % 2", Int128From(-1)}, // 123456789012345678901234567890 is a multiple of 7; the remainder has the dividend's sign
		{"five / 2", 2.5},
		{"w / w", 1.0},
		{"five ** 2", 25.0},
		{"five + f", 6.5},
		{"f * five", 7.5},
		{"w > n", true}, {"n > w", false}, {"w >= w", true}, {"w < five", false},
		{"five == 5", true}, {"5 == five", true}, {"five != 5", false}, {"five == 6", false},
		{"five > 4.5", true}, {"five < 5.5", true}, {"five == 5.0", true},
		{"neg < 0", true},
		{"n == 7", true}, {"n > 5 && f < 2.5 && s == \"ab\"", true}, {"s < \"b\"", true}, // untouched pairs
		{"sum(xs)", MustParseInt128("123456789012345678901234567891")},
		{"sum([1, 2, 3])", Int128From(6)},   // an integer sum is an int128
		{"sum([1, 2.5])", 3.5},               // a float among them: a float
		{"sum([five, 0.5])", 5.5},
		{"int128(\"123456789012345678901234567890\")", w},
		{"int128(n)", Int128From(7)},
		{"int128(2.0)", Int128From(2)},
		{"int128(five) * int128(\"10000000000000000000\")", MustParseInt128("50000000000000000000")},
		{"int(five)", 5}, {"int(2.7)", 2}, {"float(five)", 5.0},
		{"abs(neg)", Int128From(3)}, {"abs(-2)", 2},
		{"min(w, n)", Int128From(7)}, {"max(n, w)", w}, {"min(five, 2.5)", 2.5}, {"max(five, 2.5)", Int128From(5)},
		{"min(1, 2)", 1}, {"max(1.5, 2)", 2},
		{"string(five)", "5"},
		{"five + nil ?? 0", Int128From(5)},
		{"n + 1", 8}, {"n / 2", 3.5}, {"n % 3", 1}, {"2 ** 3", 8.0},
	} {
		got, err := expr.Run(compileStd(t, c.src), env)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %v (%T), want %v (%T)", c.src, got, got, c.want, c.want)
		}
	}
	// Overflow and narrowing are errors, never wraps.
	for _, c := range []struct{ src, want string }{
		{"int128(\"170141183460469231731687303715884105727\") + 1", "integer overflow"},
		{"int128(\"170141183460469231731687303715884105727\") * 2", "does not fit an int128"},
		{"-int128(\"-170141183460469231731687303715884105728\")", "does not fit an int128"},
		{"int(w)", "does not fit an int64"},
		{"w % 0", "divide by zero"},
		{"int128(\"abc\")", "is not an integer"},
		{"int128(1.5)", "not a whole number"},
	} {
		_, err := expr.Run(compileStd(t, c.src), env)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error %q, got %v", c.src, c.want, err)
		}
	}
	// Fibonacci in int128 state is exact to fib(184) — after 183 steps the
	// state is {fib(183), fib(184)} — and the 184th step, fib(185), is the
	// error (the int64 fold reached fib(92) in 91 steps the same way).
	step := compileStd(t, "{a: b, b: a + b}")
	state := map[string]any{"a": Int128{}, "b": Int128From(1)}
	for i := 1; i <= 183; i++ {
		out, err := expr.Run(step, state)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		state = out.(map[string]any)
	}
	if got := state["b"].(Int128).String(); got != "127127879743834334146972278486287885163" {
		t.Errorf("fib(184): %s", got)
	}
	if _, err := expr.Run(step, state); err == nil || !strings.Contains(err.Error(), "integer overflow") {
		t.Errorf("fib(185): want overflow, got %v", err)
	}
}
