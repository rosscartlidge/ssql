package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInt128Exec is DFC155 C in the interpreted lane, end to end through
// the binary: a column is int128 by a cast, by a wide source literal or
// by an integer sum, never by overflow; int128 arithmetic is exact to
// 2^127 and loud past it; the header names the type so the next stage
// keeps it. The other lanes are pinned by TestPipelineEquivalence and
// TestIntOverflowEveryLane.
func TestInt128Exec(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	wide := filepath.Join(dir, "wide.csv")
	if err := os.WriteFile(wide, []byte("id,v\n123456789012345678901234567890,1\n5,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, pipeline string) string {
		t.Helper()
		out, err := shell(pipeline)
		if err != nil {
			t.Fatalf("%s: %v\n%s", pipeline, err, out)
		}
		return out
	}
	want := func(t *testing.T, out string, subs ...string) {
		t.Helper()
		for _, s := range subs {
			if !strings.Contains(out, s) {
				t.Errorf("want %q in\n%s", s, out)
			}
		}
	}

	t.Run("fibonacci_exact_to_184", func(t *testing.T) {
		// 183 steps of the fold: the state is {fib(183), fib(184)}, the
		// row's value fib(184), the largest Fibonacci number in 128 bits.
		out := run(t, bin+" from range 183 | "+bin+" window -stream-expr '{a:int128(0), b:int128(1)}' '{a:b, b:a+b}' 'b' fib | "+bin+" to csv | tail -1")
		want(t, out, "183,127127879743834334146972278486287885163")
		out, err := shell(bin + " from range 184 | " + bin + " window -stream-expr '{a:int128(0), b:int128(1)}' '{a:b, b:a+b}' 'b' fib")
		if err == nil || !strings.Contains(out, "integer overflow") || !strings.Contains(out, "does not fit an int128") {
			t.Errorf("fib(185): want the int128 overflow error, got err=%v\n%s", err, out)
		}
	})
	t.Run("sum_widens_past_2_63", func(t *testing.T) {
		out := run(t, bin+" from range 2 -start 9223372036854775806 | "+bin+" group-by -sum n t")
		want(t, out, `"types":{"t":"int128"}`, `"t":18446744073709551613`)
		out = run(t, bin+" from range 2 -start 9223372036854775806 | "+bin+" update -set-expr k 1 | "+bin+" group-by k -expr 'sum(n)' t")
		want(t, out, `"t":18446744073709551613`)
		out = run(t, bin+" from range 2 -start 9223372036854775806 | "+bin+" window -sum n t | "+bin+" to csv | tail -1")
		want(t, out, "18446744073709551613")
	})
	t.Run("wide_csv_literal_types_the_column", func(t *testing.T) {
		out := run(t, bin+" from "+wide+" | "+bin+" where -if id gt 1")
		want(t, out, `"id":"int128"`, `"id":123456789012345678901234567890`, `"id":5`)
		out = run(t, bin+" from "+wide+" | "+bin+" where -if-expr 'id > int128(\"9223372036854775808\")' | "+bin+" to csv")
		want(t, out, "123456789012345678901234567890,1")
		if strings.Contains(out, "\n5,") {
			t.Errorf("5 is not past 2^63:\n%s", out)
		}
	})
	t.Run("cast_widens_and_narrows_loudly", func(t *testing.T) {
		out := run(t, bin+" from range 3 | "+bin+" cast -type n int128 | "+bin+" update -set-expr sq 'n * n * int128(\"10000000000000000000\")' | "+bin+" sort -desc sq")
		want(t, out, `"n":"int128"`, `"sq":"int128"`, `"sq":90000000000000000000`)
		if !strings.Contains(out, "90000000000000000000") || strings.Index(out, "90000000000000000000") > strings.Index(out, "10000000000000000000") {
			t.Errorf("sort -desc on an int128 column:\n%s", out)
		}
		out, err := shell(bin + " from " + wide + " | " + bin + " cast -type id int")
		if err == nil || !strings.Contains(out, "is not an int") {
			t.Errorf("narrowing a wide value: want a cast error, got err=%v\n%s", err, out)
		}
		out = run(t, bin+" from "+wide+" | "+bin+" cast -type id string | "+bin+" to csv")
		want(t, out, "123456789012345678901234567890,1")
	})
	t.Run("join_int64_to_int128_key", func(t *testing.T) {
		out := run(t, bin+" from "+wide+" | "+bin+" join <("+bin+" from range 5) -on id n | "+bin+" to csv")
		want(t, out, "5,2,5")
		if strings.Contains(out, "1234567890") {
			t.Errorf("the wide id has no partner:\n%s", out)
		}
	})
	t.Run("parquet_decimal128_round_trip", func(t *testing.T) {
		// An int128 column is DECIMAL(38,0) in Parquet: ssql reads its own
		// file back exactly, the typed lane too, and DuckDB reads the same
		// digits (as a DECIMAL, which it prints quoted in -json).
		pq := filepath.Join(dir, "wide.parquet")
		run(t, bin+" from "+wide+" | "+bin+" to parquet "+pq)
		out := run(t, bin+" from parquet "+pq+" | "+bin+" sort -desc id -desc v")
		want(t, out, `"id":"int128"`, `"id":123456789012345678901234567890`, `"id":5`)
		src := run(t, "export SSQL_MODE=typed && "+bin+" from parquet "+pq+" | "+bin+" group-by -sum id t -count c | "+bin+" to jsonl | "+bin+" generate go")
		tout, err := goRunGeneratedModuleEnv(t, "int128pq", map[string]string{"main.go": src}, nil)
		if err != nil {
			t.Fatalf("typed: %v\n%s", err, tout)
		}
		want(t, tout, `"t":123456789012345678901234567895`, `"c":2`) // the two-row fixture of this test
		if duckdb := duckdbBinary(); duckdb != "" {
			dout, derr := exec.Command(duckdb, "-json", "-c", "SELECT id FROM '"+pq+"' ORDER BY id DESC LIMIT 1").CombinedOutput()
			if derr != nil || !strings.Contains(string(dout), "123456789012345678901234567890") {
				t.Errorf("duckdb: %v\n%s", derr, dout)
			}
		}
	})
	t.Run("int128_beside_float_is_float", func(t *testing.T) {
		out := run(t, bin+" from "+wide+" | "+bin+" update -set-expr f 'id + 0.5' -set-expr m 'id % 7' -set-expr h 'id / 2' | "+bin+" limit 1")
		want(t, out, `"f":"float"`, `"m":"int128"`, `"h":"float"`, `"m":0`)
	})
}
