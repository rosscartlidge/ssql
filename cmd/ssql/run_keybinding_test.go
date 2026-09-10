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

	csv := filepath.Join(dir, "r.csv")
	if err := os.WriteFile(csv, []byte("a,b\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(line string) string {
		script := fmt.Sprintf(`
export PATH=%q:$PATH
unset TMUX
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

	// A pipeline that can't generate fails fast at codegen — the error must
	// surface (in the popup; inline here), not just scroll past. An unknown
	// field in -set-expr is a PERMANENT loud generation error; the previous
	// fixture ('3*4') was a pre-transpiler limitation that v4.57.0 learned
	// to compile, silently inverting this test.
	got := run("ssql from csv " + csv + " | ssql update -set-expr x 'nosuchfield * 2' | ssql to table")
	if !strings.Contains(got, "pipeline failed") {
		t.Errorf("expected the failure header, got:\n%s", got)
	}
	if !strings.Contains(got, "unknown field") {
		t.Errorf("expected the real generation error, got:\n%s", got)
	}

	// A working pipeline compiles, runs, and reports timing inline on success.
	ok := run("ssql from csv " + csv + " | ssql to table")
	if !strings.Contains(ok, "compiled in") || !strings.Contains(ok, "ran in") {
		t.Errorf("expected inline [ssql: compiled in …, ran in …] timing on success, got:\n%s", ok)
	}

	// The line is a SHELL pipeline: only the ssql stages compile, and what
	// follows them receives the compiled program's output as typed. Until
	// v4.97.0 a trailing `| tr` would have received code fragments.
	tail := run("ssql from csv " + csv + " | ssql to csv | tr a-z A-Z")
	if !strings.Contains(tail, "A,B") || strings.Contains(tail, "a,b") || !strings.Contains(tail, "compiled in") {
		t.Errorf("a trailing non-ssql stage must see the program's output:\n%s", tail)
	}
	out := filepath.Join(dir, "out.csv")
	red := run("ssql from csv " + csv + " | ssql to csv > " + out)
	if data, err := os.ReadFile(out); err != nil || !strings.HasPrefix(string(data), "a,b\n1,2") {
		t.Errorf("a trailing redirection must receive the program's output: %v %q\n%s", err, data, red)
	}
	// A producer before the ssql stages is kept as the program's stdin —
	// but typed codegen cannot read stdin (it samples a FILE for the
	// schema), so today that is a loud, specific refusal, not a crash or
	// a silent CPU-only fallback.
	pre := run("cat " + csv + " | ssql from csv - | ssql count")
	if !strings.Contains(pre, "pipeline failed") || !strings.Contains(pre, "stdin not supported in typed mode") {
		t.Errorf("a leading producer must be refused loudly by typed codegen:\n%s", pre)
	}
	// ssql stages separated by a non-ssql stage cannot compile as one program.
	mixed := run("ssql from csv " + csv + " | sort | ssql count")
	if !strings.Contains(mixed, "cannot compile") || !strings.Contains(mixed, `"sort"`) {
		t.Errorf("an interrupted ssql run must be refused, naming the stage:\n%s", mixed)
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
		"_ssql_typed_run", "READLINE_LINE", "SSQL_MODE=typed", "-split-pipeline", "generate go -build",
		`bind -m emacs -x '"\er": _ssql_typed_run'`,
		`bind -m vi-insert -x '"\er": _ssql_typed_run'`,
		`bind -m vi-command -x '"\er": _ssql_typed_run'`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("run keybinding script missing %q", want)
		}
	}
}
