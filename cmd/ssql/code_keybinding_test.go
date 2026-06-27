package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodeKeybinding drives the show-generated-code function with TMUX unset
// (inline branch, capturable). A real ssql pipeline should render generated
// Go; a non-ssql line is left alone.
func TestCodeKeybinding(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "k.csv")
	if err := os.WriteFile(csv, []byte("name,age\nA,30\nB,25\n"), 0o644); err != nil {
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
unset TMUX
eval "$(ssql -code-keybinding)" 2>/dev/null
READLINE_LINE=%q
READLINE_POINT=${#READLINE_LINE}
_ssql_show_go
true
`, binDir, line)
		out, err := exec.Command("bash", "-c", script).Output()
		if err != nil {
			t.Fatalf("run %q: %v", line, err)
		}
		return string(out)
	}

	// A real pipeline renders generated typed Go.
	line := "ssql from csv " + csv + " | ssql where -if age gt 20 | ssql to table"
	got := run(line)
	if !strings.Contains(got, "package main") || !strings.Contains(got, "func main") {
		t.Errorf("expected generated Go, got:\n%s", got)
	}
	if !strings.Contains(got, "typed mode") {
		t.Errorf("expected the typed-mode generation header:\n%s", got)
	}

	// A non-ssql line is left alone.
	if got := run("echo hello world"); strings.TrimSpace(got) != "" {
		t.Errorf("non-ssql line produced output: %q", got)
	}

	// A pipeline that can't generate shows the error (not silence). A bad
	// operator in typed mode fails generation; the real message must surface.
	bad := "ssql from csv " + csv + " | ssql where -if age badop 5 | ssql to table"
	got = run(bad)
	if !strings.Contains(got, "cannot generate Go") {
		t.Errorf("expected the generation-failure header, got:\n%s", got)
	}
	if !strings.Contains(got, "badop") {
		t.Errorf("expected the real error (mentioning the bad operator), got:\n%s", got)
	}
	if strings.Contains(got, "package main") {
		t.Errorf("failure case should not show generated code, got:\n%s", got)
	}
}

// TestCodeKeybindingEmitted confirms `-code-keybinding` emits the function,
// the typed/generate-go invocation, the shared popup display, and the binds.
func TestCodeKeybindingEmitted(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "-code-keybinding").Output()
	if err != nil {
		t.Fatalf("-code-keybinding: %v", err)
	}
	for _, want := range []string{
		"_ssql_show_go", "_ssql_show_help", "READLINE_LINE", "SSQL_MODE=typed",
		"generate go", "display-popup", "$TMUX",
		`bind -m emacs -x '"\eg": _ssql_show_go'`,
		`bind -m vi-insert -x '"\eg": _ssql_show_go'`,
		`bind -m vi-command -x '"\eg": _ssql_show_go'`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("code keybinding script missing %q", want)
		}
	}
}
