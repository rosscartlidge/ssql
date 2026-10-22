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
// where DuckDB stopped). The equivalence harness cannot hold this case:
// its lanes must succeed. The second half pins the sum: an integer
// column sums exactly in int64 in every lane, past 2^53 where a float
// accumulator rounds; the harness cannot hold that either, since it
// parses every number as a float64.
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
		"sum":  bin + " from range 2 -start 9223372036854775806 | " + bin + " group-by -sum n t",
		"fold": bin + " from range 94 | " + bin + " window -stream-expr '{a:0, b:1}' '{a:b, b:a+b}' 'b' fib",
		// the batch after B (2026-10-22): unary minus, sum() in an aggregation expression, window sums
		"neg":        bin + " from range 1 -start -9223372036854775808 | " + bin + " update -set-expr m '-n'",
		"expr_sum":   bin + " from range 2 -start 9223372036854775806 | " + bin + " update -set-expr k 1 | " + bin + " group-by k -expr 'sum(n)' t",
		"window_sum": bin + " from range 2 -start 9223372036854775806 | " + bin + " window -sum n t",
	}
	for name, pipeline := range overflow {
		t.Run("overflow_"+name, func(t *testing.T) {
			// exec
			out, err := shell(pipeline + " | " + bin + " to jsonl")
			if err == nil || !strings.Contains(out, "integer overflow") {
				t.Errorf("exec: want an integer overflow failure, got err=%v\n%s", err, out)
			}
			// generated Go, each mode
			for _, mode := range []string{"record", "typed", "parallel"} {
				src, err := shell("export SSQL_MODE=" + mode + " && " + pipeline + " | " + bin + " to jsonl | " + bin + " generate go")
				if err != nil {
					t.Fatalf("%s: generate failed: %v\n%s", mode, err, src)
				}
				out, err := goRunGeneratedModuleEnv(t, "overflowtest", map[string]string{"main.go": src}, nil)
				if err == nil || !strings.Contains(out, "integer overflow") {
					t.Errorf("%s: want an integer overflow failure, got err=%v\n%s", mode, err, out)
				}
			}
			// DuckDB: its own words for the same stop. No SQL form: -stream-expr,
			// group-by -expr, and a window without -order. DuckDB sums BIGINT
			// into HUGEINT, so it answers where ssql stops (DFC155 §C, the
			// documented divergence): the sum cases stop here too.
			if duckdb == "" || name == "fold" || name == "expr_sum" || name == "window_sum" || name == "sum" {
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

	// An integer sum is exact past 2^53 in every lane, wherever a sum is
	// made: group-by -sum, -expr sum(), window -sum running and sliding.
	for name, c := range map[string]struct{ pipeline, want string }{
		"group_by_sum":       {bin + " from range 2 -start 9007199254740993 | " + bin + " group-by -sum n t", "18014398509481987"},
		"expr_sum":           {bin + " from range 2 -start 9007199254740993 | " + bin + " update -set-expr k 1 | " + bin + " group-by k -expr 'sum(n)' t", "18014398509481987"},
		"window_running_sum": {bin + " from range 2 -start 9007199254740993 | " + bin + " window -sum n t | " + bin + " limit -last 1", "18014398509481987"},
		"window_sliding_sum": {bin + " from range 3 -start 9007199254740993 | " + bin + " window -sum n t -preceding 1 | " + bin + " limit -last 1", "18014398509481989"},
		"pivot_sum":          {bin + " from range 2 -start 9007199254740993 | " + bin + " update -set-expr d '\"a\"' -set-expr q '\"x\"' | " + bin + " pivot -row d -col q -val n -func sum", "18014398509481987"},
	} {
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
			if duckdb != "" && name == "group_by_sum" { // the window forms need -order for SQL; the -expr form has no translation
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
