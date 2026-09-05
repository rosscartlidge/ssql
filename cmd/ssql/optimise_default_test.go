package main

// `generate go` optimises by default (+O turns it off): the rewrites
// `generate ssql` prints are applied before code is generated, the
// header records the pipeline as typed AND the one implemented, a
// pipeline nothing rewrites takes a zero-cost fast path, and the
// re-execution of the rewritten pipeline uses THIS binary rather than
// whatever `ssql` is first on PATH.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// codelabParquet is the checked-in employees.parquet fixture (dept, age,
// salary, … columns); the pruning rule applies to any pipeline that
// reads a subset of its columns.
func codelabParquet(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../doc/codelab-data/employees.parquet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	return p
}

// barePathEnv strips ~/go/bin and friends so a bare `ssql` cannot resolve:
// the optimised re-execution must name the binary that produced it.
func barePathEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "PATH=") {
			env = append(env, e)
		}
	}
	return append(env, "PATH=/usr/bin:/bin")
}

func TestGenerateGoOptimisesByDefault(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	pq := codelabParquet(t)
	pipe := bin + " from parquet " + pq + " | " + bin + " group-by dept -count n | " + bin + " to csv"

	for _, mode := range []string{"typed", "record"} {
		cmd := exec.Command(bin, "generate", "go", "-mode", mode, "-pipeline", pipe)
		cmd.Env = barePathEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("[%s] generate go (default -O) with bare PATH: %v\n%s", mode, err, out)
		}
		src := string(out)
		for _, want := range []string{
			"-columns dept |", // the header shows the pipeline implemented …
			"Optimised by generate go (default; +O disables) from the pipeline as typed:",
			"parquet-column-pruning:", // … the pipeline typed, and the rule between them
		} {
			if !strings.Contains(src, want) {
				t.Errorf("[%s] optimised source lacks %q:\n%s", mode, want, src[:min(len(src), 1500)])
			}
		}
		pruned := map[string]string{"typed": `typed.ParquetColumns("dept")`, "record": `[]string{"dept"}`}[mode]
		if !strings.Contains(src, pruned) {
			t.Errorf("[%s] generated read is not pruned to dept (want %s)", mode, pruned)
		}
	}
}

func TestGenerateGoPlusOTurnsOptimiserOff(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	pq := codelabParquet(t)
	pipe := bin + " from parquet " + pq + " | " + bin + " group-by dept -count n | " + bin + " to csv"
	out, err := exec.Command(bin, "generate", "go", "+O", "-mode", "typed", "-pipeline", pipe).CombinedOutput()
	if err != nil {
		t.Fatalf("generate go +O: %v\n%s", err, out)
	}
	src := string(out)
	for _, reject := range []string{"-columns", "Optimised by", "ParquetColumns("} {
		if strings.Contains(src, reject) {
			t.Errorf("+O source still carries %q", reject)
		}
	}
	if !strings.Contains(src, "typed.ReadParquetParallel[") {
		t.Errorf("+O source should read the parquet unpruned:\n%s", src[:min(len(src), 1200)])
	}
}

// A pipeline no rule can improve must produce byte-identical source
// with and without the optimiser — the default costs nothing there and
// adds no note.
func TestGenerateGoOptimiserFastPath(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	csvPath, err := filepath.Abs("../../doc/codelab-data/employees.csv")
	if err != nil {
		t.Fatal(err)
	}
	pipe := bin + " from csv " + csvPath + " | " + bin + " where -if age gt 30 | " + bin + " to csv"
	def, err := exec.Command(bin, "generate", "go", "-mode", "typed", "-pipeline", pipe).CombinedOutput()
	if err != nil {
		t.Fatalf("default: %v\n%s", err, def)
	}
	off, err := exec.Command(bin, "generate", "go", "+O", "-mode", "typed", "-pipeline", pipe).CombinedOutput()
	if err != nil {
		t.Fatalf("+O: %v\n%s", err, off)
	}
	if string(def) != string(off) {
		t.Errorf("fast path: default and +O differ on a pipeline with no applicable rule")
	}
	if strings.Contains(string(def), "Optimised by") {
		t.Errorf("no rule applied, yet the header claims an optimisation")
	}
}

// -run through the default optimiser produces the same rows as the
// unoptimised program (the rewrite is invisible in the output).
func TestGenerateGoOptimisedRunMatchesPlusO(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	pq := codelabParquet(t)
	pipe := bin + " from parquet " + pq + " | " + bin + " group-by dept -count n -sum salary total | " + bin + " sort dept | " + bin + " to csv"
	env := append(os.Environ(), "SSQL_MODULE_DIR="+mustRepoRoot(t))
	run := func(args ...string) string {
		cmd := exec.Command(bin, append([]string{"generate", "go", "-run", "-mode", "typed", "-pipeline", pipe}, args...)...)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("generate go -run %v: %v", args, err)
		}
		return string(out)
	}
	def, off := run(), run("+O")
	if def != off || !strings.Contains(def, "Engineering") {
		t.Errorf("optimised -run output differs from +O:\n--- default\n%s--- +O\n%s", def, off)
	}
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}
