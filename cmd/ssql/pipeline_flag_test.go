package main

// Tests for the inline `-pipeline '...'` flag on generate go/sql/ssql
// (the ssqlgen shell helper's replacement — one-shot codegen with no
// export/subshell ceremony). Shares runPipelineForFragments with
// -script, so preprocessing (comments, continuations) is covered by
// the -script tests; these pin the flag surface itself.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writePipelineTestCSV(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	csv := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(csv, []byte("name,age\nAlice,30\nBob,25\nCarol,42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return csv
}

func TestPipelineFlag_GenerateGoRun(t *testing.T) {
	csv := writePipelineTestCSV(t)
	bin := buildSSQLForTypedTest(t)
	pipeline := "ssql from " + csv + " | ssql where -if age gt 25 | ssql count"
	out, err := exec.Command(bin, "generate", "go", "-run", "-pipeline", pipeline).CombinedOutput()
	if err != nil {
		t.Fatalf("generate go -run -pipeline: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "2" {
		t.Errorf("expected count=2 (Alice + Carol), got %q\nfull output:\n%s", got, out)
	}
}

func TestPipelineFlag_GenerateGoRecordMode(t *testing.T) {
	csv := writePipelineTestCSV(t)
	bin := buildSSQLForTypedTest(t)
	pipeline := "ssql from " + csv + " | ssql limit 1 | ssql to csv"
	out, err := exec.Command(bin, "generate", "go", "-mode", "record", "-run", "-pipeline", pipeline).CombinedOutput()
	if err != nil {
		t.Fatalf("generate go -mode record -run -pipeline: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Alice") {
		t.Errorf("expected Alice in record-mode output, got:\n%s", out)
	}
}

func TestPipelineFlag_GenerateSQL(t *testing.T) {
	csv := writePipelineTestCSV(t)
	bin := buildSSQLForTypedTest(t)
	pipeline := "ssql from " + csv + " | ssql group-by age -count n | ssql to table"
	out, err := exec.Command(bin, "generate", "sql", "-pipeline", pipeline).CombinedOutput()
	if err != nil {
		t.Fatalf("generate sql -pipeline: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "SELECT") || !strings.Contains(string(out), "GROUP BY") {
		t.Errorf("expected SQL with SELECT + GROUP BY, got:\n%s", out)
	}
}

func TestPipelineFlag_GenerateSSQL(t *testing.T) {
	csv := writePipelineTestCSV(t)
	bin := buildSSQLForTypedTest(t)
	pipeline := "ssql from csv " + csv + " | ssql sort -desc age | ssql limit 2 | ssql to table"
	out, err := exec.Command(bin, "generate", "ssql", "-pipeline", pipeline).CombinedOutput()
	if err != nil {
		t.Fatalf("generate ssql -pipeline: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "top 2") {
		t.Errorf("expected optimizer to rewrite sort+limit as top 2, got:\n%s", out)
	}
}

func TestPipelineFlag_ScriptExclusive(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "generate", "go", "-script", "x.ssql", "-pipeline", "ssql from x.csv").CombinedOutput()
	if err == nil {
		t.Fatalf("expected -script + -pipeline to error, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "mutually exclusive") {
		t.Errorf("expected mutual-exclusion message, got:\n%s", out)
	}
}

func TestPipelineFlag_FailingStageLoud(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "generate", "go", "-run", "-pipeline", "ssql from /nonexistent-dir/nope.csv | ssql count").CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure for missing input file, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "pipeline failed") {
		t.Errorf("expected loud pipeline-failed error, got:\n%s", out)
	}
}
