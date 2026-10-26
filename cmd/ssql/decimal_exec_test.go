package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDecimalExec is DFC155 E in the interpreted lane, end to end through
// the binary: a column is decimal:S by declaration (a cast, -type on the
// source, a header), its sums are exact to the scale where a float
// drifts, every rescale rounds half away from zero, a literal compares
// exactly, and the header names the type — with its scale — so the next
// stage keeps it. The other lanes are pinned by TestPipelineEquivalence
// and TestIntOverflowEveryLane.
func TestDecimalExec(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Shuffled, with values a float sum gets wrong (0.1 + 0.2) and ones a
	// declared scale must round (12.345 → 12.35, -5.005 → -5.01, 0.125 → 0.13).
	money := write("money.csv", "k,amt\nb,0.20\na,0.10\nb,12.345\na,0.30\nb,0.10\na,-5.005\nb,0.125\na,0.20\n")
	big := write("big.csv", "k,amt\nx,90071992547409.92\nx,0.01\nx,0.01\n")
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

	t.Run("sum_exact_to_the_cent", func(t *testing.T) {
		out := run(t, bin+" from csv "+money+" -type amt decimal:2 | "+bin+" group-by k -sum amt t -avg amt a -min amt lo -max amt hi | "+bin+" sort k")
		// The header types the sum at the column's scale (DuckDB's
		// SUM(DECIMAL(38,2)) is DECIMAL(38,2)), the average a float.
		want(t, out, `"t":"decimal:2"`, `"a":"float"`, `"lo":"decimal:2"`,
			`{"k":"a","t":-4.41,"a":-1.1025,"lo":-5.01,"hi":0.30}`,
			`{"k":"b","t":12.78,"a":3.195,"lo":0.10,"hi":12.35}`)
		// The same column read as a float drifts: the case that justifies the type.
		out = run(t, bin+" from csv "+money+" | "+bin+" group-by k -sum amt t | "+bin+" sort k | "+bin+" to csv")
		want(t, out, "12.770000000000001")
	})
	t.Run("exact_past_2_53_cents", func(t *testing.T) {
		out := run(t, bin+" from csv "+big+" -type amt decimal:2 | "+bin+" group-by k -sum amt t | "+bin+" to csv")
		want(t, out, "90071992547409.94")
		out = run(t, bin+" from csv "+big+" -type amt decimal:2 | "+bin+" window -sum amt run | "+bin+" to csv | tail -1")
		want(t, out, "90071992547409.94")
	})
	t.Run("cast_rounds_half_away_from_zero", func(t *testing.T) {
		out := run(t, bin+" from csv "+money+" | "+bin+" cast -type amt string | "+bin+" cast -type amt decimal:2 | "+bin+" sort amt | "+bin+" to csv")
		want(t, out, "a,-5.01\n", "b,0.13\n", "b,12.35\n")
		out = run(t, bin+" from csv "+money+" -type amt decimal:2 | "+bin+" cast -type amt decimal:0 | "+bin+" sort amt | "+bin+" to csv")
		want(t, out, "a,-5\n", "b,12\n")
		// To an int: toward zero, as a float casts.
		out = run(t, bin+" from csv "+money+" -type amt decimal:2 | "+bin+" cast -type amt int | "+bin+" where -if k eq a | "+bin+" sort amt | "+bin+" to csv")
		want(t, out, "a,-5\n", "a,0\n")
	})
	t.Run("literal_compares_exactly", func(t *testing.T) {
		out := run(t, bin+" from csv "+money+" -type amt decimal:2 | "+bin+" where -if amt gt 0.15 | "+bin+" sort -desc amt | "+bin+" to csv")
		want(t, out, "k,amt\nb,12.35\na,0.30\nb,0.20\na,0.20\n")
		out = run(t, bin+" from csv "+money+" -type amt decimal:2 | "+bin+" where -if amt eq 0.1 | "+bin+" to csv")
		want(t, out, "a,0.10\n", "b,0.10\n")
	})
	t.Run("header_keeps_the_scale_across_a_hop", func(t *testing.T) {
		out := run(t, "SSQL_MODE=schema "+bin+" from csv "+money+" -type amt decimal:2 | "+bin+" sort amt | head -1")
		want(t, out, `"amt":"decimal:2"`)
		// A file `to jsonl` wrote has no header: its numbers are typed on
		// the way back in, so the reader declares the column (a decimal
		// is never inferred from 12.35).
		hop := filepath.Join(dir, "hop.jsonl")
		run(t, bin+" from csv "+money+" -type amt decimal:2 | "+bin+" to jsonl > "+hop)
		out = run(t, bin+" from jsonl "+hop+" -type amt decimal:2 | "+bin+" group-by k -sum amt t | "+bin+" sort k | "+bin+" to csv")
		want(t, out, "a,-4.41\n", "b,12.78\n")
		out = run(t, "SSQL_MODE=schema "+bin+" from jsonl "+hop+" | head -1")
		want(t, out, `"amt":"float"`) // undeclared: a float, as the text says
	})
	t.Run("sliding_window_and_pivot", func(t *testing.T) {
		out := run(t, bin+" from csv "+money+" -type amt decimal:2 | "+bin+" sort amt | "+bin+" window -sum amt s2 -preceding 1 | "+bin+" to csv | head -4")
		want(t, out, "a,-5.01,-5.01\n", "a,0.10,-4.91\n", "b,0.10,0.20\n")
		out = run(t, bin+" from csv "+money+" -type amt decimal:2 | "+bin+" pivot -row k -col k -val amt -func sum | "+bin+" sort k | "+bin+" to csv")
		want(t, out, "-4.41", "12.78")
	})
	t.Run("join_key_across_types", func(t *testing.T) {
		ids := write("ids.csv", "id,n\n1,one\n5,five\n")
		dec := write("dec.csv", "id,amt\n1.00,x\n5.00,y\n2.50,z\n")
		out := run(t, bin+" from csv "+dec+" -type id decimal:2 | "+bin+" join "+ids+" -on id id | "+bin+" sort id | "+bin+" to csv")
		want(t, out, "1.00,x,one\n", "5.00,y,five\n")
		if strings.Contains(out, "2.50") {
			t.Errorf("2.50 matched an integer key:\n%s", out)
		}
	})
	t.Run("overflow_is_loud", func(t *testing.T) {
		huge := write("huge.csv", "amt\n99999999999999999999999999999999999999\n1\n")
		out, err := shell(bin + " from csv " + huge + " -type amt decimal:0 | " + bin + " group-by -sum amt t")
		if err == nil || !strings.Contains(out, "decimal overflow") || !strings.Contains(out, "does not fit DECIMAL(38,0)") {
			t.Errorf("want the decimal overflow error, got err=%v\n%s", err, out)
		}
	})
	t.Run("parquet_decimal128_round_trip", func(t *testing.T) {
		// A decimal column is DECIMAL(38,S) in Parquet: ssql reads its own
		// file back at the scale, the typed lane too, and DuckDB reads the
		// same digits (a DECIMAL, quoted in -json).
		pq := filepath.Join(dir, "money.parquet")
		run(t, bin+" from csv "+money+" -type amt decimal:2 | "+bin+" to parquet "+pq)
		out := run(t, bin+" from parquet "+pq+" | "+bin+" group-by k -sum amt t | "+bin+" sort k")
		want(t, out, `"t":"decimal:2"`, `{"k":"a","t":-4.41}`, `{"k":"b","t":12.78}`)
		src := run(t, "export SSQL_MODE=typed && "+bin+" from parquet "+pq+" | "+bin+" group-by k -sum amt t | "+bin+" sort k | "+bin+" to jsonl | "+bin+" generate go")
		tout, err := goRunGeneratedModuleEnv(t, "decimalpq", map[string]string{"main.go": src}, nil)
		if err != nil {
			t.Fatalf("typed: %v\n%s", err, tout)
		}
		want(t, tout, `{"k":"a","t":-4.41}`, `{"k":"b","t":12.78}`)
		if duckdb := duckdbBinary(); duckdb != "" {
			dout, derr := exec.Command(duckdb, "-json", "-c", "SELECT typeof(amt) ty, sum(amt) t FROM '"+pq+"' GROUP BY ty").CombinedOutput()
			if derr != nil || !strings.Contains(string(dout), "DECIMAL(38,2)") || !strings.Contains(string(dout), `"8.37"`) {
				t.Errorf("duckdb: %v\n%s", derr, dout)
			}
		}
	})
	t.Run("describe_names_the_type", func(t *testing.T) {
		out := run(t, bin+" from csv "+money+" -type amt decimal:2 | "+bin+" describe | "+bin+" where -if field eq amt | "+bin+" to csv")
		want(t, out, "amt,decimal,8,0,6,-5.01,12.35")
	})
}
