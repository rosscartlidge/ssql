package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHelpKeybinding drives the help-at-cursor function by setting
// READLINE_LINE/READLINE_POINT and calling it with TMUX unset (so it takes
// the inline-print branch we can capture). The interactive keypress path is
// covered by TestHelpKeybindingPTY.
func TestHelpKeybinding(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(binDir, "ssql")); err != nil {
		t.Fatal(err)
	}

	run := func(line string, point int) string {
		script := fmt.Sprintf(`
export PATH=%q:$PATH
unset TMUX
eval "$(ssql -help-keybinding)" 2>/dev/null
READLINE_LINE=%q
READLINE_POINT=%d
_ssql_help_at
true   # swallow the guard-path return status so Output() doesn't error
`, binDir, line, point)
		out, err := exec.Command("bash", "-c", script).Output()
		if err != nil {
			t.Fatalf("run %q: %v", line, err)
		}
		return string(out)
	}

	// Cursor at end of a stage whose last word is the "-sum" flag → help
	// should describe -sum and its arguments.
	line := "ssql group-by dept -sum"
	got := run(line, len(line))
	if !strings.Contains(got, "-sum") || !strings.Contains(strings.ToLower(got), "sum field values") {
		t.Errorf("help for -sum missing description:\n%s", got)
	}

	// Works inside a pipeline too (help needs no upstream data).
	piped := "ssql from csv x.csv | ssql group-by dept -sum"
	if got := run(piped, len(piped)); !strings.Contains(strings.ToLower(got), "sum field values") {
		t.Errorf("help in a pipeline stage missing description:\n%s", got)
	}

	// On an EXPRESSION argument, the help appends the function reference —
	// writing an expression is hard without knowing the functions.
	exprLine := "ssql from csv x.csv | ssql update -set-expr total "
	got = run(exprLine, len(exprLine))
	if !strings.Contains(got, "EXPRESSION FUNCTIONS") {
		t.Errorf("help on an expression arg should append the function reference:\n%s", got)
	}
	if !strings.Contains(got, "round(num)") {
		t.Errorf("help on an expression arg missing the function list:\n%s", got)
	}
	// On a non-expression arg (the field name of -set-expr), it must NOT append.
	fieldLine := "ssql from csv x.csv | ssql update -set-expr "
	if got := run(fieldLine, len(fieldLine)); strings.Contains(got, "EXPRESSION FUNCTIONS") {
		t.Errorf("help on the field arg should NOT append functions:\n%s", got)
	}

	// A non-ssql stage must produce nothing (guard against running junk).
	if got := run("echo hello world", len("echo hello world")); strings.TrimSpace(got) != "" {
		t.Errorf("non-ssql line produced help: %q", got)
	}
}

// TestHelpKeybindingEmitted confirms `-help-keybinding` emits the function,
// the -help-at protocol call, the tmux/inline display adapter, and the binds.
func TestHelpKeybindingEmitted(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "-help-keybinding").Output()
	if err != nil {
		t.Fatalf("-help-keybinding: %v", err)
	}
	for _, want := range []string{
		"_ssql_help_at", "READLINE_LINE", "-help-at", "display-popup", "$TMUX",
		`bind -m emacs -x '"\eh": _ssql_help_at'`,
		`bind -m vi-insert -x '"\eh": _ssql_help_at'`,
		`bind -m vi-command -x '"\eh": _ssql_help_at'`,
		// Alt-H cheat-sheet of the whole key-binding family.
		"_ssql_help_keys", "ssql key bindings",
		`bind -m emacs -x '"\eH": _ssql_help_keys'`,
		`bind -m vi-insert -x '"\eH": _ssql_help_keys'`,
		`bind -m vi-command -x '"\eH": _ssql_help_keys'`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("help keybinding script missing %q", want)
		}
	}
}
