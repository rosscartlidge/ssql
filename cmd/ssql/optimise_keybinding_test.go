package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOptimiseKeybinding drives the in-situ optimise function by setting
// READLINE_LINE and calling it — valid because READLINE_LINE is a plain
// variable the function reads/writes, and real bash hands it the full
// line (pty-verified). The interactive binding is covered by
// TestOptimiseKeybindingPTY.
func TestOptimiseKeybinding(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "o.csv")
	if err := os.WriteFile(csv, []byte("name,age,dept,salary\nA,30,eng,100\nB,25,eng,80\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(binDir, "ssql")); err != nil {
		t.Fatal(err)
	}

	run := func(line string) string {
		script := fmt.Sprintf(`
export PATH=%q:$PATH
eval "$(ssql -optimise-keybinding)" 2>/dev/null
READLINE_LINE=%q
READLINE_POINT=${#READLINE_LINE}
_ssql_optimise_pipeline
printf '%%s' "$READLINE_LINE"
`, binDir, line)
		out, err := exec.Command("bash", "-c", script).Output()
		if err != nil {
			t.Fatalf("run %q: %v", line, err)
		}
		return string(out)
	}

	// Two `where`s + `sort … | limit` should merge and become `top`.
	in := "ssql from csv " + csv + " | ssql where -if age gt 20 | ssql where -if dept eq eng | ssql sort -desc salary | ssql limit 2"
	got := run(in)
	if n := strings.Count(got, "ssql where"); n != 1 {
		t.Errorf("expected the two where clauses merged into one, got %d:\n%s", n, got)
	}
	if !strings.Contains(got, "dept eq eng") || !strings.Contains(got, "age gt 20") {
		t.Errorf("merged where lost a clause:\n%s", got)
	}
	if !strings.Contains(got, "ssql top 2") {
		t.Errorf("sort|limit not rewritten to top:\n%s", got)
	}

	// A non-ssql line must be left untouched (guard against eval'ing junk).
	if got := run("echo hello world"); got != "echo hello world" {
		t.Errorf("non-ssql line changed: %q", got)
	}
}

// TestOptimiseKeybindingEmitted confirms `-optimise-keybinding` emits the
// function and the binds.
func TestOptimiseKeybindingEmitted(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "-optimise-keybinding").Output()
	if err != nil {
		t.Fatalf("-optimise-keybinding: %v", err)
	}
	for _, want := range []string{"_ssql_optimise_pipeline", "READLINE_LINE", "generate ssql", "SSQL_MODE=record",
		`bind -m emacs -x '"\C-t": _ssql_optimise_pipeline'`,
		`bind -m vi-insert -x '"\C-t": _ssql_optimise_pipeline'`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("optimise keybinding script missing %q", want)
		}
	}
}
