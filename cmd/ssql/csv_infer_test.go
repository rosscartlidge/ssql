package main

// CSV type inference samples the leading rows and fails LOUDLY on a
// later cell that does not fit (DFC124 §3; root package tests in
// csv_infer_test.go pin the reader). These tests pin the two lanes a
// user actually runs: `ssql from csv` and a `generate go` record
// program — both must exit non-zero with a message naming the row,
// the column, and the override.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lateFloatCSV: 1001 int rows (past DefaultInferRows) then a float.
func lateFloatCSV(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("id,v\n")
	for i := 1; i <= 1001; i++ {
		fmt.Fprintf(&b, "%d,%d\n", i, i)
	}
	b.WriteString("1002,1.5\n")
	p := filepath.Join(t.TempDir(), "late.csv")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCSVLateMismatchIsLoudInExec(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	csv := lateFloatCSV(t)

	out, err := exec.Command("bash", "-c", bin+" from csv "+csv+" | "+bin+" count; exit ${PIPESTATUS[0]}").CombinedOutput()
	if err == nil {
		t.Fatalf("from csv exited 0 on a cell that does not fit the inferred type:\n%s", out)
	}
	for _, want := range []string{`row 1002`, `column "v"`, `"1.5" is not int`, "first 1000 rows", "-type v"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("message should contain %q; got:\n%s", want, out)
		}
	}

	// The override the message names makes the same file read cleanly.
	out, err = exec.Command("bash", "-c", bin+" from csv "+csv+" -type v float | "+bin+" count").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "1002" {
		t.Fatalf("with -type v float: err=%v out=%q (want 1002)", err, out)
	}
}

func TestCSVLateMismatchIsLoudInGeneratedRecordCode(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	csv := lateFloatCSV(t)

	gen := exec.Command("bash", "-c", "export SSQL_MODE=record && "+bin+" from csv "+csv+" | "+bin+" count | "+bin+" generate go")
	src, err := gen.CombinedOutput()
	if err != nil {
		t.Fatalf("generate go: %v\n%s", err, src)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	repo, _ := filepath.Abs("../..")
	mod := "module latetest\n\ngo 1.24\n\nrequire github.com/rosscartlidge/ssql/v4 v4.0.0\n\nreplace github.com/rosscartlidge/ssql/v4 => " + repo + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"mod", "tidy"}, {"build", "-o", "prog", "."}} {
		c := exec.Command("go", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s\n--- source:\n%s", args, err, out, src)
		}
	}
	run := exec.Command(filepath.Join(dir, "prog"))
	run.Dir = dir
	out, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("generated program exited 0 on the late float:\n%s", out)
	}
	s := string(out)
	if !strings.Contains(s, "Error:") || !strings.Contains(s, `row 1002, column "v"`) {
		t.Errorf("generated program should report the cell as an Error, got:\n%s", s)
	}
	if strings.Contains(s, "goroutine ") {
		t.Errorf("generated program printed a stack trace instead of an error:\n%s", s)
	}
}
