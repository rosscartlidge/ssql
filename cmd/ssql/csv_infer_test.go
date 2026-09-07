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
	out, err := runGeneratedPipeline(t, bin, filepath.Dir(csv), "record", bin+" from csv "+csv+" | "+bin+" count")
	assertLoudFailure(t, "record csv", out, err, `row 1002, column "v"`)
}
