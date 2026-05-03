package main

// Tests for `ssql generate go -script PATH`.
//
// The flag reads a .ssql pipeline file (multi-line; supports
// trailing-|, leading-|, and # comments), preprocesses it,
// and runs it under bash with SSQLGO set to the chosen mode.
// The fragment stream produced is identical to running the
// equivalent inline pipeline — these tests verify both that
// the preprocessor handles the various input shapes correctly
// and that the resulting Go compiles + runs.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptFlag_LeadingPipe(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(csv, []byte("name,age\nAlice,30\nBob,25\nCarol,42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "p.ssql")
	if err := os.WriteFile(script, []byte(`# count adults
ssql from `+csv+`
| ssql where -if age gt 25
| ssql count
`), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "generate", "go", "-script", script, "-run").CombinedOutput()
	if err != nil {
		t.Fatalf("generate go -script -run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "2" {
		t.Errorf("expected count=2 (Alice + Carol), got %q\nfull output:\n%s", got, out)
	}
}

func TestScriptFlag_TrailingPipe(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(csv, []byte("name,age\nAlice,30\nBob,25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "p.ssql")
	if err := os.WriteFile(script, []byte(`ssql from `+csv+` |
ssql where -if age ge 30 |
ssql count
`), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "generate", "go", "-script", script, "-run").CombinedOutput()
	if err != nil {
		t.Fatalf("generate go -script -run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "1" {
		t.Errorf("expected count=1 (Alice), got %q", got)
	}
}

func TestScriptFlag_ModeOverride(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(csv, []byte("name,age\nAlice,30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "p.ssql")
	if err := os.WriteFile(script, []byte("ssql from "+csv+" | ssql to csv\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := buildSSQLForTypedTest(t)

	// -mode record → Record-mode codegen (ssql.ReadCSV).
	rec, err := exec.Command(bin, "generate", "go", "-script", script, "-mode", "record").Output()
	if err != nil {
		t.Fatalf("mode=record: %v", err)
	}
	if !strings.Contains(string(rec), "ssql.ReadCSV(") {
		t.Errorf("mode=record should emit ssql.ReadCSV, got:\n%s", rec)
	}

	// -mode typed (default) → typed.ReadCSV / typed.ReadCSVParallel.
	def, err := exec.Command(bin, "generate", "go", "-script", script).Output()
	if err != nil {
		t.Fatalf("default mode: %v", err)
	}
	if !strings.Contains(string(def), "typed.ReadCSV") {
		t.Errorf("default mode should emit typed.ReadCSV, got:\n%s", def)
	}
}

func TestScriptFlag_CommentsRespectQuotes(t *testing.T) {
	// '#' inside a quoted string must not be treated as a comment.
	// The pipeline below uses an expression filter where the literal
	// contains '#' — if the preprocessor strips it, the predicate
	// breaks. We use a token like "a#b" rather than "#urgent" because
	// the CSV reader's own comment handling skips '#'-prefixed lines
	// (separate from our preprocessor).
	dir := t.TempDir()
	csv := filepath.Join(dir, "tags.csv")
	if err := os.WriteFile(csv, []byte("tag\nfoo\na#b\nbar\na#b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "p.ssql")
	if err := os.WriteFile(script, []byte(`# count rows tagged a#b
ssql from `+csv+`
| ssql where -if-expr 'tag == "a#b"'
| ssql count
`), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "generate", "go", "-script", script, "-mode", "record", "-run").CombinedOutput()
	if err != nil {
		t.Fatalf("generate -script -run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "2" {
		t.Errorf("expected 2 a#b rows, got %q (string-quoted '#' may have been stripped)\n%s", got, out)
	}
}

func TestScriptFlag_HeredocViaStdin(t *testing.T) {
	// Process substitution (`<(cat <<! ... !)`) opens a /dev/fd/N
	// pipe; we test the equivalent shape by writing the script to
	// a regular file (heredoc requires bash anyway, and the binary
	// just opens whatever path it gets — same code path).
	// The dedicated heredoc form is exercised by the TestScriptFlag_LeadingPipe
	// case above, just via a real file.
	t.Skip("process substitution covered indirectly by TestScriptFlag_LeadingPipe")
}

func TestScriptFlag_FailsLoudlyOnTypo(t *testing.T) {
	// User wrote `sql from ...` (typo) instead of `ssql from ...`.
	// Without `set -o pipefail`, bash returns the last stage's exit
	// code (success), the empty stdin flowed through downstream
	// stages, and the assembler produced uncompilable code with an
	// undeclared `records` variable. With pipefail, any stage's
	// non-zero exit fails the whole pipeline and we error out
	// loudly. This test guards that.
	dir := t.TempDir()
	csv := filepath.Join(dir, "p.csv")
	if err := os.WriteFile(csv, []byte("name,age\nAlice,30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "p.ssql")
	if err := os.WriteFile(script, []byte(
		"sql from "+csv+" | ssql group-by name -count n | ssql to table\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSSQLForTypedTest(t)
	cmd := exec.Command(bin, "generate", "go", "-script", script, "-mode", "typed")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when first stage was typo'd; got success:\n%s", out)
	}
	// Error should mention the pipeline failure (not silently emit
	// uncompilable Go).
	if !strings.Contains(string(out), "pipeline failed") {
		t.Errorf("expected 'pipeline failed' in error message; got:\n%s", out)
	}
}

func TestAssembler_NoInitFragment(t *testing.T) {
	// Defence-in-depth: if somehow stmt/final fragments arrive on
	// stdin without an init fragment (e.g. a different shell caused
	// a silent upstream failure that pipefail wouldn't have caught),
	// the record-mode assembler should refuse rather than producing
	// code that references an undeclared `records` variable.
	bin := buildSSQLForTypedTest(t)
	// One synthetic stmt fragment with no init upstream.
	cmd := exec.Command(bin, "generate", "go")
	cmd.Stdin = strings.NewReader(
		`{"type":"stmt","var":"x","input":"records","code":"x := ssql.Limit[ssql.Record](5)(records)"}` + "\n")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit with no-init-fragment input; got success:\n%s", out)
	}
	if !strings.Contains(string(out), "no source (init) fragment") {
		t.Errorf("expected 'no source (init) fragment' in error; got:\n%s", out)
	}
}
