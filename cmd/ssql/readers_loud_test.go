package main

// Readers fail LOUDLY, in every lane (v4.91.0 "readers finish the job").
// On the same fixture — 1001 int rows then one "1.5" — the typed lane
// used to keep the CSV row with the cell zeroed, drop the JSONL row,
// and turn a missing file into an empty stream, all with exit 0; exec
// TSV typed each value on its own. Now every lane exits non-zero
// naming the row, and `-type v float` on the from stage makes every
// lane agree on the same 1002 rows. The typed lane's panic must reach
// main as "Error: …" (no goroutine trace) even when it originates in a
// parallel shard.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lateFixtures writes late.csv, late.tsv and late.jsonl into one dir.
func lateFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var csv, tsv, jsonl strings.Builder
	csv.WriteString("id,v\n")
	tsv.WriteString("id\tv\n")
	for i := 1; i <= 1001; i++ {
		fmt.Fprintf(&csv, "%d,%d\n", i, i)
		fmt.Fprintf(&tsv, "%d\t%d\n", i, i)
		fmt.Fprintf(&jsonl, `{"id":%d,"v":%d}`+"\n", i, i)
	}
	csv.WriteString("1002,1.5\n")
	tsv.WriteString("1002\t1.5\n")
	jsonl.WriteString(`{"id":1002,"v":1.5}` + "\n")
	for name, body := range map[string]string{"late.csv": csv.String(), "late.tsv": tsv.String(), "late.jsonl": jsonl.String()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runGeneratedPipeline generates Go for the pipeline in the given mode
// ("record", "typed", "parallel"), builds it against the checkout, runs
// it in dir and returns combined output + run error. Generation and
// build failures are fatal (they are not the behaviour under test).
func runGeneratedPipeline(t *testing.T, bin, dir, mode, pipeline string) (string, error) {
	t.Helper()
	gen := exec.Command("bash", "-c", "export SSQL_MODE="+mode+" && "+pipeline+" | "+bin+" generate go")
	gen.Dir = dir
	src, err := gen.CombinedOutput()
	if err != nil {
		t.Fatalf("generate go (%s): %v\n%s", mode, err, src)
	}
	prog := filepath.Join(dir, "prog_"+mode+"_"+fmt.Sprint(len(pipeline)))
	if err := os.MkdirAll(prog, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prog, "main.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	repo, _ := filepath.Abs("../..")
	mod := "module latetest\n\ngo 1.24\n\nrequire github.com/rosscartlidge/ssql/v4 v4.0.0\n\nreplace github.com/rosscartlidge/ssql/v4 => " + repo + "\n"
	if err := os.WriteFile(filepath.Join(prog, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"mod", "tidy"}, {"build", "-o", "prog", "."}} {
		c := exec.Command("go", args...)
		c.Dir = prog
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("go %v (%s): %v\n%s\n--- source:\n%s", args, mode, err, out, src)
		}
	}
	run := exec.Command(filepath.Join(prog, "prog"))
	run.Dir = dir
	out, err := run.CombinedOutput()
	return string(out), err
}

// assertLoudFailure: non-zero exit, an "Error:" line, the row named, no
// stack trace.
func assertLoudFailure(t *testing.T, lane, out string, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: exited 0 on data that does not fit the column type:\n%s", lane, out)
		return
	}
	if !strings.Contains(out, "Error:") {
		t.Errorf("%s: no Error: line:\n%s", lane, out)
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("%s: message should contain %q:\n%s", lane, w, out)
		}
	}
	if strings.Contains(out, "goroutine ") {
		t.Errorf("%s: printed a stack trace instead of an error:\n%s", lane, out)
	}
}

const lateAgg = " | %s group-by -sum v total -count n | %s to table"

func TestLateMismatchIsLoudInEveryLane(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := lateFixtures(t)
	agg := fmt.Sprintf(lateAgg, bin, bin)

	// exec: CSV and TSV (TSV typed per value before v4.91.0 — no error,
	// a mixed column).
	for _, format := range []string{"csv", "tsv"} {
		c := exec.Command("bash", "-c", bin+" from "+format+" late."+format+agg+"; exit ${PIPESTATUS[0]}")
		c.Dir = dir
		out, err := c.CombinedOutput()
		assertLoudFailure(t, "exec "+format, string(out), err, `row 1002`, `column "v"`, `"1.5" is not int`, "-type v")
	}

	// Generated programs: record CSV/TSV; typed CSV/TSV/JSONL — the
	// group-by keeps the parallel readers, so the panic starts in a shard.
	for _, tc := range []struct{ mode, format, want string }{
		{"record", "csv", `row 1002, column "v"`},
		{"record", "tsv", `row 1002, column "v"`},
		{"typed", "csv", `typed.ReadCSVParallel late.csv: row 1002: column "v": "1.5" is not int64`},
		{"typed", "tsv", `typed.ReadDelimParallel late.tsv: row 1002: column "v": "1.5" is not int64`},
		{"typed", "jsonl", `typed.ReadJSONLParallel late.jsonl: line 1002: field "v": "1.5" is not int64`},
	} {
		out, err := runGeneratedPipeline(t, bin, dir, tc.mode, bin+" from "+tc.format+" late."+tc.format+agg)
		assertLoudFailure(t, tc.mode+" "+tc.format, out, err, tc.want)
	}

	// The serial typed reader too (limit downgrades the source).
	out, err := runGeneratedPipeline(t, bin, dir, "typed", bin+" from csv late.csv | "+bin+" limit 1005 | "+bin+" count")
	assertLoudFailure(t, "typed serial csv", out, err, "typed.ReadCSV late.csv: row 1002")
}

// `-type v float` on the from stage is the remedy the error names; it
// must work in every lane (typed codegen refused it until v4.91.0, and
// `from tsv` / `from jsonl` had no such flag) and give the same answer.
func TestTypeOverrideMakesEveryLaneAgree(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := lateFixtures(t)
	agg := fmt.Sprintf(lateAgg, bin, bin)
	wantRows := []string{"1002", "501502.5"}

	check := func(lane, out string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v\n%s", lane, err, out)
			return
		}
		for _, w := range wantRows {
			if !strings.Contains(out, w) {
				t.Errorf("%s: want %q in output:\n%s", lane, w, out)
			}
		}
	}
	for _, format := range []string{"csv", "tsv", "jsonl"} {
		pipeline := bin + " from " + format + " late." + format + " -type v float" + agg
		c := exec.Command("bash", "-c", pipeline)
		c.Dir = dir
		out, err := c.CombinedOutput()
		check("exec "+format, string(out), err)
		for _, mode := range []string{"record", "typed"} {
			out, err := runGeneratedPipeline(t, bin, dir, mode, pipeline)
			check(mode+" "+format, out, err)
		}
	}
	// -sample and -last carried no overrides into generated code before.
	for _, tail := range []string{" -last 5", " -sample 3 -sample-seed 1"} {
		pipeline := bin + " from csv late.csv" + tail + " -type v float | " + bin + " count"
		out, err := runGeneratedPipeline(t, bin, dir, "record", pipeline)
		if err != nil {
			t.Errorf("record csv%s: %v\n%s", tail, err, out)
		}
	}
}

func TestMissingFileIsLoudInTypedCode(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := lateFixtures(t)
	agg := fmt.Sprintf(lateAgg, bin, bin)
	for _, format := range []string{"csv", "tsv", "jsonl"} {
		// Generate against the real file (sampling needs it), then run
		// the program against a file that is not there.
		gen := exec.Command("bash", "-c", "export SSQL_MODE=typed && "+bin+" from "+format+" late."+format+agg+" | "+bin+" generate go")
		gen.Dir = dir
		src, err := gen.CombinedOutput()
		if err != nil {
			t.Fatalf("generate: %v\n%s", err, src)
		}
		prog := filepath.Join(dir, "missing_"+format)
		os.MkdirAll(prog, 0o755)
		os.WriteFile(filepath.Join(prog, "main.go"), src, 0o644)
		repo, _ := filepath.Abs("../..")
		os.WriteFile(filepath.Join(prog, "go.mod"), []byte("module m\n\ngo 1.24\n\nrequire github.com/rosscartlidge/ssql/v4 v4.0.0\n\nreplace github.com/rosscartlidge/ssql/v4 => "+repo+"\n"), 0o644)
		for _, args := range [][]string{{"mod", "tidy"}, {"build", "-o", "prog", "."}} {
			c := exec.Command("go", args...)
			c.Dir = prog
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("go %v: %v\n%s", args, err, out)
			}
		}
		run := exec.Command(filepath.Join(prog, "prog"), "-input", "nope."+format)
		run.Dir = dir
		out, err := run.CombinedOutput()
		assertLoudFailure(t, "typed "+format+" missing file", string(out), err, "nope."+format, "no such file")
	}
}

// A tee'd JSONL side file starts with a `_schema` header line. The
// generated union/merge templates read side files with the
// schema-unaware reader until v4.91.0, so the header became a phantom
// record (TODO item from 2026-09-04).
func TestUnionMergeSideFileSchemaHeaderIsNotARecord(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	a := "id,v\n1,10\n2,20\n3,30\n"
	if err := os.WriteFile(filepath.Join(dir, "a.csv"), []byte(a), 0o644); err != nil {
		t.Fatal(err)
	}
	// Produce the side file the way users do: tee.
	tee := exec.Command("bash", "-c", bin+" from csv a.csv | "+bin+" tee teed.jsonl > /dev/null")
	tee.Dir = dir
	if out, err := tee.CombinedOutput(); err != nil {
		t.Fatalf("tee: %v\n%s", err, out)
	}
	head, _ := os.ReadFile(filepath.Join(dir, "teed.jsonl"))
	if !strings.HasPrefix(string(head), `{"_schema"`) {
		t.Fatalf("fixture should start with a _schema header:\n%s", head)
	}
	for _, cmd := range []string{"union -all -file teed.jsonl", "merge teed.jsonl -by id"} {
		pipeline := bin + " from csv a.csv | " + bin + " " + cmd + " | " + bin + " count"
		out, err := runGeneratedPipeline(t, bin, dir, "record", pipeline)
		if err != nil {
			t.Errorf("%s: %v\n%s", cmd, err, out)
			continue
		}
		if got := strings.TrimSpace(out); got != "6" {
			t.Errorf("%s: count = %s, want 6 (3 + 3; a phantom _schema record gives 7)", cmd, got)
		}
	}
}
