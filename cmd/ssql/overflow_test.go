package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntOverflowEveryLane is DFC155 B's gate: an int64 `+`, `-` or `*`
// that overflows stops every lane — exec, record and typed codegen, and
// DuckDB — instead of wrapping (until v4.123.0 three lanes answered wrong
// where DuckDB stopped), and C's: an int128 operation that overflows
// stops the same way, while an integer SUM never stops — it widens to an
// int128, DuckDB's SUM(BIGINT) → HUGEINT. The equivalence harness cannot
// hold the failures: its lanes must succeed. The second half pins the
// sums: exact in every lane past 2^53 and past 2^63.
// overflowed reports ssql's overflow error in an output: an integer's
// (DFC155 B, C) or a decimal's (E).
func overflowed(out string) bool {
	return strings.Contains(out, "integer overflow") || strings.Contains(out, "decimal overflow")
}

// dataFusionOverflowed reports DataFusion's words for the stop: the
// checked int64 form's cast back to BIGINT, or decimal arithmetic.
func dataFusionOverflowed(out string) bool {
	return strings.Contains(out, "out of range Int64") || strings.Contains(out, "Overflow") || strings.Contains(out, "too large to store")
}

// dataFusionRows runs pipelines on DataFusion two ways: generate sql
// -dialect datafusion through the Python bindings (SSQL_DATAFUSION_PYTHON)
// and generate rust's program (SSQL_RUST_LANE=1), every pipeline's
// function built into one crate up front. A refusal by design skips the
// row; the overflow rows were added 2026-10-30, when they found
// DataFusion's int64 operators wrapping.
type dataFusionRows struct {
	py      string
	rustBin string
	rustFns map[string]string // pipeline name → function, when it rendered
}

func newDataFusionRows(t *testing.T, bin string, pipelines map[string]string) *dataFusionRows {
	r := &dataFusionRows{py: os.Getenv("SSQL_DATAFUSION_PYTHON"), rustFns: map[string]string{}}
	if !rustLaneEnabled() {
		return r
	}
	var funcs, names []string
	for name, pipeline := range pipelines {
		fn := rustCaseFunc("overflow_" + name)
		code, refused, err := rustGenerateFunc(bin, pipeline, fn, nil)
		if err != nil {
			// The pipelines were written for the Go lanes; one SQL cannot
			// express (a limit -last without a sort) is the dialect's
			// refusal in other words, as the rust lane logs it.
			first, _, _ := strings.Cut(strings.TrimSpace(err.Error()), "\n")
			t.Logf("rust: %s did not generate: %s", name, first)
			continue
		}
		if refused != "" {
			continue
		}
		funcs, names = append(funcs, code), append(names, fn)
		r.rustFns[name] = fn
	}
	b, err := rustBuildCrate(t, "ssqloverflow", funcs, names)
	if err != nil {
		t.Fatalf("rust: %v", err)
	}
	r.rustBin = b
	return r
}

// each runs the pipeline on every DataFusion row there is (lane is
// "datafusion" or "rust"); a row that refuses it by design is not called.
func (r *dataFusionRows) each(t *testing.T, bin, name, pipeline string, f func(lane, out string, err error)) {
	if r.py != "" {
		sql, err := shell("export SSQL_MODE=record && " + pipeline + " | " + bin + " to jsonl | " + bin + " generate sql -dialect datafusion")
		switch {
		case err != nil:
			t.Logf("datafusion: %s does not generate: %s", name, strings.TrimSpace(sql))
		default:
			script := filepath.Join(t.TempDir(), "run.py")
			if err := os.WriteFile(script, []byte(datafusionRunner), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(r.py, script)
			cmd.Stdin = strings.NewReader(sql)
			out, err := cmd.CombinedOutput()
			f("datafusion", string(out), err)
		}
	}
	if fn, ok := r.rustFns[name]; ok {
		out, err := exec.Command(r.rustBin, fn).CombinedOutput()
		f("rust", string(out), err)
	}
}

func TestIntOverflowEveryLane(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs generated programs")
	}
	bin := corpusBin(t)
	duckdb := duckdbBinary()

	overflow := map[string]string{
		"add":  bin + " from range 3 | " + bin + " update -set-expr big '9223372036854775807 + n'",
		"sub":  bin + " from range 3 | " + bin + " update -set-expr big '-9223372036854775807 - n'",
		"mul":  bin + " from range 3 | " + bin + " update -set-expr big 'n * 4611686018427387904'",
		"fold": bin + " from range 94 | " + bin + " window -stream-expr '{a:0, b:1}' '{a:b, b:a+b}' 'b' fib",
		// the batch after B (2026-10-22): unary minus
		"neg": bin + " from range 1 -start -9223372036854775808 | " + bin + " update -set-expr m '-n'",
		// C (2026-10-25): the int128 bound, and the int128 fold past fib(184)
		"wide_mul":  bin + " from range 3 | " + bin + " cast -type n int128 | " + bin + " update -set-expr big 'n * int128(\"170141183460469231731687303715884105727\")'",
		"wide_fold": bin + " from range 184 | " + bin + " window -stream-expr '{a:int128(0), b:int128(1)}' '{a:b, b:a+b}' 'b' fib",
		// E (2026-10-27): the 38-digit decimal bound
		"dec_mul": bin + " from range 3 | " + bin + " cast -type n decimal:2 | " + bin + " update -set-expr big 'n * decimal(\"999999999999999999999999999999999999.99\")'",
		// columns an earlier update computed (2026-10-31): the SQL
		// translator knew only source kinds, so DataFusion wrapped these
		"derived_add": bin + " from range 3 | " + bin + " update -set-expr m 'n + 1' | " + bin + " update -set-expr big '9223372036854775807 + m'",
		"set_add":     bin + " from range 3 | " + bin + " update -set one 1 | " + bin + " update -set-expr big '9223372036854775807 + one + n'",
	}
	// An integer sum is exact past 2^53 in every lane, wherever a sum is
	// made: group-by -sum, -expr sum(), window -sum running and sliding —
	// and past 2^63 it widens to an int128 instead of stopping (DFC155 C).
	sums := map[string]struct{ pipeline, want string }{
		"group_by_sum":        {bin + " from range 2 -start 9007199254740993 | " + bin + " group-by -sum n t", "18014398509481987"},
		"group_by_sum_widens": {bin + " from range 2 -start 9223372036854775806 | " + bin + " group-by -sum n t", "18446744073709551613"},
		"expr_sum_widens":     {bin + " from range 2 -start 9223372036854775806 | " + bin + " update -set-expr k 1 | " + bin + " group-by k -expr 'sum(n)' t", "18446744073709551613"},
		"window_sum_widens":   {bin + " from range 2 -start 9223372036854775806 | " + bin + " window -sum n t | " + bin + " limit -last 1", "18446744073709551613"},
		"expr_sum":            {bin + " from range 2 -start 9007199254740993 | " + bin + " update -set-expr k 1 | " + bin + " group-by k -expr 'sum(n)' t", "18014398509481987"},
		"window_running_sum":  {bin + " from range 2 -start 9007199254740993 | " + bin + " window -sum n t | " + bin + " limit -last 1", "18014398509481987"},
		"window_sliding_sum":  {bin + " from range 3 -start 9007199254740993 | " + bin + " window -sum n t -preceding 1 | " + bin + " limit -last 1", "18014398509481989"},
		"pivot_sum":           {bin + " from range 2 -start 9007199254740993 | " + bin + " update -set-expr d '\"a\"' -set-expr q '\"x\"' | " + bin + " pivot -row d -col q -val n -func sum", "18014398509481987"},
		// the window sums with an -order SQL can see (the datafusion and
		// rust rows; the forms above end in limit -last, which SQL refuses)
		"window_ordered_sum_widens": {bin + " from range 2 -start 9223372036854775806 | " + bin + " window -order n -sum n t | " + bin + " where -if n eq 9223372036854775807", "18446744073709551613"},
		"window_ordered_sliding":    {bin + " from range 3 -start 9007199254740993 | " + bin + " window -order n -sum n t -preceding 1 | " + bin + " where -if n eq 9007199254740995", "18014398509481989"},
		// a route's total along join -follow (DFC161): two hops weighing
		// 2^63 − 2 and 2^63 − 1 — an int128, not an overflow; the keyed
		// default mode runs on DuckDB, -all-paths on DataFusion and rust
		"follow_sum_widens":     {bin + " from range 1 -start 0 | " + bin + " join <(" + bin + " from range 2 -start 9223372036854775806 | " + bin + " update -set-expr src 'n - 9223372036854775806' -set-expr dst 'n - 9223372036854775805' -set-expr w n | " + bin + " exclude n) -on n src -follow dst -sum w t | " + bin + " where -if n eq 2", "18446744073709551613"},
		"follow_all_sum_widens": {bin + " from range 1 -start 0 | " + bin + " join <(" + bin + " from range 2 -start 9223372036854775806 | " + bin + " update -set-expr src 'n - 9223372036854775806' -set-expr dst 'n - 9223372036854775805' -set-expr w n | " + bin + " exclude n) -on n src -follow dst -all-paths -sum w t | " + bin + " where -if n eq 2", "18446744073709551613"},
		// E: decimal sums past 2^53 cents, exact at the scale
		"dec_group_by_sum": {bin + " from range 3 -start 9007199254740993 | " + bin + " cast -type n decimal:2 | " + bin + " group-by -sum n t", "27021597764222982.00"},
		"dec_expr_sum":     {bin + " from range 3 -start 9007199254740993 | " + bin + " cast -type n decimal:2 | " + bin + " update -set-expr k 1 | " + bin + " group-by k -expr 'sum(n)' t", "27021597764222982.00"},
		"dec_window_sum":   {bin + " from range 3 -start 9007199254740993 | " + bin + " cast -type n decimal:2 | " + bin + " window -sum n t | " + bin + " limit -last 1", "27021597764222982.00"},
	}
	all := map[string]string{}
	for name, p := range overflow {
		all[name] = p
	}
	for name, c := range sums {
		all["sum_"+name] = c.pipeline
	}
	df := newDataFusionRows(t, bin, all)

	for name, pipeline := range overflow {
		t.Run("overflow_"+name, func(t *testing.T) {
			// exec
			out, err := shell(pipeline + " | " + bin + " to jsonl")
			if err == nil || !overflowed(out) {
				t.Errorf("exec: want an overflow failure, got err=%v\n%s", err, out)
			}
			// generated Go, each mode
			for _, mode := range []string{"record", "typed", "parallel"} {
				src, err := shell("export SSQL_MODE=" + mode + " && " + pipeline + " | " + bin + " to jsonl | " + bin + " generate go")
				if err != nil {
					t.Fatalf("%s: generate failed: %v\n%s", mode, err, src)
				}
				out, err := goRunGeneratedModuleEnv(t, "overflowtest", map[string]string{"main.go": src}, nil)
				if err == nil || !overflowed(out) {
					t.Errorf("%s: want an overflow failure, got err=%v\n%s", mode, err, out)
				}
			}
			// DataFusion, as SQL and as the Rust program.
			df.each(t, bin, name, pipeline, func(lane, out string, err error) {
				if err == nil || !dataFusionOverflowed(out) {
					t.Errorf("%s: want an overflow failure, got err=%v\n%s", lane, err, out)
				}
			})
			// DuckDB: its own words for the same stop. No SQL form: -stream-expr.
			if duckdb == "" || name == "fold" || name == "wide_fold" {
				return
			}
			sql, err := shell("export SSQL_MODE=record && " + pipeline + " | " + bin + " to jsonl | " + bin + " generate sql")
			if err != nil {
				t.Fatalf("generate sql: %v\n%s", err, sql)
			}
			cmd := exec.Command(duckdb, "-json", "-c", sql)
			dout, derr := cmd.CombinedOutput()
			if derr == nil || !strings.Contains(string(dout), "Overflow") {
				t.Errorf("duckdb: want an overflow failure, got err=%v\n%s", derr, dout)
			}
		})
	}

	for name, c := range sums {
		t.Run("int_sum_exact_past_2_53_"+name, func(t *testing.T) {
			want := c.want // odd: no float64 holds it (DuckDB -json quotes its HUGEINT)
			check := func(lane, out string) {
				if !strings.Contains(out, want) {
					t.Errorf("%s: want %s in the raw output, got\n%s", lane, want, out)
				}
			}
			out, err := shell(c.pipeline + " | " + bin + " to jsonl")
			if err != nil {
				t.Fatalf("exec: %v\n%s", err, out)
			}
			check("exec", out)
			for _, mode := range []string{"record", "typed", "parallel"} {
				src, err := shell("export SSQL_MODE=" + mode + " && " + c.pipeline + " | " + bin + " to jsonl | " + bin + " generate go")
				if err != nil {
					t.Fatalf("%s: generate failed: %v\n%s", mode, err, src)
				}
				out, err := goRunGeneratedModuleEnv(t, "overflowtest", map[string]string{"main.go": src}, nil)
				if err != nil {
					t.Fatalf("%s: %v\n%s", mode, err, out)
				}
				check(mode, out)
			}
			df.each(t, bin, "sum_"+name, c.pipeline, func(lane, out string, err error) {
				if err != nil {
					t.Fatalf("%s: %v\n%s", lane, err, out)
				}
				check(lane, out)
			})
			if duckdb != "" && (strings.HasPrefix(name, "group_by_sum") || strings.HasPrefix(name, "follow_")) { // the window forms need -order for SQL; the -expr form has no translation
				sql, err := shell("export SSQL_MODE=record && " + c.pipeline + " | " + bin + " to jsonl | " + bin + " generate sql")
				if err != nil {
					t.Fatalf("generate sql: %v\n%s", err, sql)
				}
				dout, derr := exec.Command(duckdb, "-json", "-c", sql).CombinedOutput()
				if derr != nil {
					t.Fatalf("duckdb: %v\n%s", derr, dout)
				}
				check("duckdb", string(dout))
			}
		})
	}

	t.Run("generate_sql_refuses_a_stdin_source", func(t *testing.T) {
		// It panicked with "index out of range [0] with length 0" until 2026-10-22.
		out, err := shell("export SSQL_MODE=record && echo '{\"a\":1}' | " + bin + " from jsonl | " + bin + " to jsonl | " + bin + " generate sql")
		if err == nil || !strings.Contains(out, "needs a file") || strings.Contains(out, "runtime error") {
			t.Errorf("want a clear refusal, got err=%v\n%s", err, out)
		}
	})
}

// TestWideIntegersThroughNestedValues: a list element past 2^53 survives
// explode, a dotted path and flatten exactly in every Go lane (the nested
// parser used to go through float64; the Fibonacci list exploded to
// floats from fib(79) on, and 2^53+1 to 2^53).
func TestWideIntegersThroughNestedValues(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs generated programs")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "wide.jsonl")
	if err := os.WriteFile(data, []byte(`{"id":1,"fib":[9007199254740993,9007199254740995],"o":{"n":9223372036854775807}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ pipeline, want string }{
		"explode": {bin + " from jsonl " + data + " | " + bin + " explode fib | " + bin + " include fib", `{"fib":9007199254740993}` + "\n" + `{"fib":9007199254740995}`},
		"path":    {bin + " from jsonl " + data + " | " + bin + " update -set-expr n 'o.n' -set-expr f 'fib[1]' | " + bin + " include n f", `{"n":9223372036854775807,"f":9007199254740995}`},
		"flatten": {bin + " from jsonl " + data + " | " + bin + " flatten o | " + bin + " include o.n", `{"o.n":9223372036854775807}`},
	} {
		t.Run(name, func(t *testing.T) {
			check := func(lane, out string) {
				if !strings.Contains(out, c.want) {
					t.Errorf("%s: want\n%s\nin\n%s", lane, c.want, out)
				}
			}
			out, err := shell(c.pipeline + " | " + bin + " to jsonl")
			if err != nil {
				t.Fatalf("exec: %v\n%s", err, out)
			}
			check("exec", out)
			for _, mode := range []string{"record", "typed", "parallel"} {
				src, err := shell("export SSQL_MODE=" + mode + " && " + c.pipeline + " | " + bin + " to jsonl | " + bin + " generate go")
				if err != nil {
					t.Fatalf("%s: generate failed: %v\n%s", mode, err, src)
				}
				out, err := goRunGeneratedModuleEnv(t, "widetest", map[string]string{"main.go": src}, nil)
				if err != nil {
					t.Fatalf("%s: %v\n%s", mode, err, out)
				}
				check(mode, out)
			}
		})
	}
}

// shell runs a bash pipeline and returns its combined output.
func shell(script string) (string, error) {
	cmd := exec.Command("bash", "-c", "set -o pipefail; "+script)
	cmd.Env = append(os.Environ(), "TZ=UTC")
	out, err := cmd.CombinedOutput()
	return string(out), err
}
