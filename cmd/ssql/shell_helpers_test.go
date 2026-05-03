package main

// Tests for `ssql -shell-helpers` and the `ssqlgen` bash function it
// emits. The function is the friendly-syntax wrapper around the
// `(export SSQLGO=...; <pipeline>) | ssql generate go` pattern.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellHelpers_OutputContainsSsqlgen(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "-shell-helpers").Output()
	if err != nil {
		t.Fatalf("ssql -shell-helpers: %v", err)
	}
	for _, want := range []string{"ssqlgen()", "SSQLGO=", "command ssql generate go"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in -shell-helpers output\n%s", want, out)
		}
	}
}

func TestShellHelpers_SsqlgenDefaultMode(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(csv, []byte("name,age\nAlice,30\nBob,25\nCarol,42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)

	// Eval the helpers, then call ssqlgen in default (typed) mode.
	cmdline := `eval "$(` + bin + ` -shell-helpers)" && ssqlgen 'ssql from ` + csv + ` | ssql where -if age ge 30 | ssql count' -run`
	out, err := exec.Command("bash", "-c", cmdline).Output()
	if err != nil {
		t.Fatalf("ssqlgen invocation: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "2" {
		t.Errorf("expected count=2 (Alice + Carol), got %q", got)
	}
}

func TestShellHelpers_SsqlgenRecordMode(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(csv, []byte("name,age\nAlice,30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)

	cmdline := `eval "$(` + bin + ` -shell-helpers)" && ssqlgen -record 'ssql from ` + csv + ` | ssql to csv'`
	out, err := exec.Command("bash", "-c", cmdline).Output()
	if err != nil {
		t.Fatalf("ssqlgen -record: %v\n%s", err, out)
	}
	// Record mode emits ssql.ReadCSV, not typed.ReadCSV.
	if !strings.Contains(string(out), "ssql.ReadCSV(") {
		t.Errorf("ssqlgen -record should emit ssql.ReadCSV, got:\n%s", out)
	}
}

func TestShellHelpers_SsqlgenMissingPipeline(t *testing.T) {
	// Calling ssqlgen with no pipeline should error with usage.
	bin := buildSSQLForTypedTest(t)
	cmdline := `eval "$(` + bin + ` -shell-helpers)" && ssqlgen`
	c := exec.Command("bash", "-c", cmdline)
	out, err := c.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when ssqlgen called with no pipeline; got success:\n%s", out)
	}
	if !strings.Contains(string(out), "missing pipeline argument") {
		t.Errorf("expected usage message, got:\n%s", out)
	}
}
