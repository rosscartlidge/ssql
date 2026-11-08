package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestGenerateRustWritesTheSinkFile: `to csv FILE` and `to tsv FILE` under
// generate rust write the file (refused until 2026-11-08, and printed to
// stdout before 2026-11-07), as do `to json FILE` and `to jsonl FILE`
// (refused until 2026-11-09). The file reads back as exec's does; the bytes
// are exec's except where the spelling differs (a float: Arrow writes 1e20
// and 2.0, exec 1e+20 and 2; a time: Arrow writes the instant, exec the
// cell's own offset), so the byte check leaves those columns out. A result
// with no rows is the header, as exec's `to csv` writes it.
func TestGenerateRustWritesTheSinkFile(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles Rust programs")
	}
	if os.Getenv("SSQL_RUST_LANE") == "" {
		t.Skip("set SSQL_RUST_LANE=1 to compile the generated Rust (minutes on the first build)")
	}
	bin := corpusBin(t)
	// DataFusion reads a declared time only in UTC (generate rust refuses
	// another zone); exec runs in the same zone.
	utc := "TZ=UTC PATH=" + filepath.Dir(bin) + ":$PATH "
	pipe := func(stages string) string {
		return "ssql " + strings.ReplaceAll(stages, " | ", " | ssql ")
	}
	rust := func(t *testing.T, dir, stages string) {
		t.Helper()
		out, err := sinkShell(t, dir, utc+bin+" generate rust -run -pipeline '"+pipe(stages)+"'")
		if err != nil {
			t.Fatalf("generate rust -run: %v\n%s", err, out)
		}
		if out != "" {
			t.Errorf("the rows belong in the file, stdout has:\n%s", out)
		}
	}
	execRun := func(t *testing.T, dir, stages string) {
		t.Helper()
		if out, err := sinkShell(t, dir, utc+bin+" "+strings.ReplaceAll(stages, " | ", " | "+bin+" ")); err != nil {
			t.Fatalf("exec: %v\n%s", err, out)
		}
	}
	for _, kind := range []string{"csv", "tsv"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte(sinkSQLFixture), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "exec"), 0o755); err != nil {
				t.Fatal(err)
			}
			file := "o." + kind

			// every column, read back
			execRun(t, dir, sinkSQLSource+" | where -if k gt 0 | to "+kind+" exec/"+file)
			rust(t, dir, sinkSQLSource+" | where -if k gt 0 | to "+kind+" "+file)
			got, want := sinkRows(t, dir, bin, file), sinkRows(t, dir, bin, "exec/"+file)
			if len(want) != 3 || !reflect.DeepEqual(got, want) {
				t.Errorf("rows differ from exec's file\n got %v\nwant %v", got, want)
			}

			// the bytes, where the spelling agrees: a quote and the
			// delimiter in a cell, empties, a decimal's trailing zero, bools
			bytesFile := "b." + kind
			execRun(t, dir, sinkSQLSource+" | exclude f t | to "+kind+" exec/"+bytesFile)
			rust(t, dir, sinkSQLSource+" | exclude f t | to "+kind+" "+bytesFile)
			gotB, _ := os.ReadFile(filepath.Join(dir, bytesFile))
			wantB, _ := os.ReadFile(filepath.Join(dir, "exec", bytesFile))
			if string(gotB) != string(wantB) || len(wantB) == 0 {
				t.Errorf("bytes differ\n got %q\nwant %q", gotB, wantB)
			}

			// a time exec spells as a UTC instant is spelled the same: with
			// the Z, which ssql's own reader does not need (it reads a time
			// with no offset as UTC) but every other reader does
			timeFile := "z." + kind
			execRun(t, dir, sinkSQLSource+" | where -if k eq 1 | include k t | to "+kind+" exec/"+timeFile)
			rust(t, dir, sinkSQLSource+" | where -if k eq 1 | include k t | to "+kind+" "+timeFile)
			gotT, _ := os.ReadFile(filepath.Join(dir, timeFile))
			wantT, _ := os.ReadFile(filepath.Join(dir, "exec", timeFile))
			if string(gotT) != string(wantT) || !strings.Contains(string(wantT), "Z") {
				t.Errorf("a UTC time\n got %q\nwant %q", gotT, wantT)
			}

			// no rows: the header. exec's `to tsv` writes an empty file (its
			// writer takes the columns from the first record; TODO), so tsv
			// is held to the header itself.
			emptyFile := "e." + kind
			execRun(t, dir, sinkSQLSource+" | where -if k gt 100 | to "+kind+" exec/"+emptyFile)
			rust(t, dir, sinkSQLSource+" | where -if k gt 100 | to "+kind+" "+emptyFile)
			gotE, _ := os.ReadFile(filepath.Join(dir, emptyFile))
			wantE, _ := os.ReadFile(filepath.Join(dir, "exec", emptyFile))
			if kind == "tsv" {
				wantE = []byte("k\ts\tf\td\tt\tb\n")
			}
			if string(gotE) != string(wantE) || len(wantE) == 0 {
				t.Errorf("no rows\n got %q\nwant %q", gotE, wantE)
			}
		})
	}

	// json and jsonl: the rows read back as exec's; the JSONL bytes are
	// exec's where the spelling agrees and the JSON array is exec's as
	// JSON (exec indents it, Arrow does not); a UTC time keeps its Z; no
	// rows is an empty file (jsonl) or [] (json), as exec writes them. The
	// byte checks leave out s as well: DataFusion reads an empty CSV cell
	// as null, exec as "", which the reader decides, not the sink.
	for _, kind := range []string{"json", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte(sinkSQLFixture), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "exec"), 0o755); err != nil {
				t.Fatal(err)
			}
			both := func(stages, file string) (got, want []byte) {
				t.Helper()
				execRun(t, dir, sinkSQLSource+" | "+stages+" | to "+kind+" exec/"+file)
				rust(t, dir, sinkSQLSource+" | "+stages+" | to "+kind+" "+file)
				got, _ = os.ReadFile(filepath.Join(dir, file))
				want, _ = os.ReadFile(filepath.Join(dir, "exec", file))
				return got, want
			}

			both("where -if k gt 0", "o."+kind)
			got, want := sinkRows(t, dir, bin, "o."+kind), sinkRows(t, dir, bin, "exec/o."+kind)
			if len(want) != 3 || !reflect.DeepEqual(got, want) {
				t.Errorf("rows differ from exec's file\n got %v\nwant %v", got, want)
			}

			gotB, wantB := both("exclude f t s", "b."+kind)
			if kind == "jsonl" && string(gotB) != string(wantB) {
				t.Errorf("bytes differ\n got %q\nwant %q", gotB, wantB)
			}
			if kind == "json" && !sameJSON(t, gotB, wantB) {
				t.Errorf("JSON differs\n got %s\nwant %s", gotB, wantB)
			}
			if len(wantB) == 0 {
				t.Errorf("exec wrote nothing")
			}

			gotT, _ := both("where -if k eq 1 | include k t", "z."+kind)
			if !strings.Contains(string(gotT), `"2024-03-05T01:02:03Z"`) {
				t.Errorf("a UTC time is an instant with its Z:\n%s", gotT)
			}

			gotE, wantE := both("where -if k gt 100", "e."+kind)
			if string(gotE) != string(wantE) {
				t.Errorf("no rows\n got %q\nwant %q", gotE, wantE)
			}
		})
	}

	t.Run("stdout_json", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte(sinkSQLFixture), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := sinkShell(t, dir, utc+bin+" generate rust -run -pipeline '"+pipe(sinkSQLSource+" | exclude f t s | to json")+"'")
		if err != nil {
			t.Fatalf("generate rust -run: %v\n%s", err, out)
		}
		want, err := sinkShell(t, dir, utc+bin+" "+sinkSQLSource+" | "+bin+" exclude f t s | "+bin+" to json")
		if err != nil {
			t.Fatalf("exec: %v\n%s", err, want)
		}
		if !sameJSON(t, []byte(out), []byte(want)) {
			t.Errorf("stdout is not exec's JSON array (it printed JSONL until 2026-11-09)\n got %s\nwant %s", out, want)
		}
	})

	t.Run("unwritable", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte(sinkSQLFixture), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := sinkShell(t, dir, utc+bin+" generate rust -run -pipeline 'ssql from csv t.csv | ssql to csv /nonexistent/x.csv'")
		if err == nil {
			t.Fatalf("exit 0 with nothing written:\n%s", out)
		}
		if !strings.Contains(out, "/nonexistent/x.csv") {
			t.Errorf("the error should name the file:\n%s", out)
		}
	})
}

// sameJSON reports whether two JSON documents hold the same value, numbers
// compared by their text.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	decode := func(p []byte) any {
		var v any
		dec := json.NewDecoder(bytes.NewReader(p))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("%v in %s", err, p)
		}
		return v
	}
	return reflect.DeepEqual(decode(a), decode(b))
}
