package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunKeybinding checks the guard: a non-ssql line must do nothing (no
// notice, no attempt to compile). The actual compile-and-run path is
// exercised by TestRunKeybindingPTY and by the generate-go corpus.
func TestRunKeybinding(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
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
eval "$(ssql -run-keybinding)" 2>/dev/null
READLINE_LINE=%q
READLINE_POINT=${#READLINE_LINE}
_ssql_typed_run
true
`, binDir, line)
		out, err := exec.Command("bash", "-c", script).Output()
		if err != nil {
			t.Fatalf("run %q: %v", line, err)
		}
		return string(out)
	}

	// A non-ssql line is left alone — no compile notice.
	if got := run("echo hello world"); strings.TrimSpace(got) != "" {
		t.Errorf("non-ssql line produced output: %q", got)
	}
}

// TestRunKeybindingEmitted confirms `-run-keybinding` emits the function,
// the typed/generate-go invocation, and the Alt-r binds.
func TestRunKeybindingEmitted(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "-run-keybinding").Output()
	if err != nil {
		t.Fatalf("-run-keybinding: %v", err)
	}
	for _, want := range []string{
		"_ssql_typed_run", "READLINE_LINE", "SSQL_MODE=typed", "generate go -run",
		`bind -m emacs -x '"\er": _ssql_typed_run'`,
		`bind -m vi-insert -x '"\er": _ssql_typed_run'`,
		`bind -m vi-command -x '"\er": _ssql_typed_run'`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("run keybinding script missing %q", want)
		}
	}
}
