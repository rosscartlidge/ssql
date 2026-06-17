package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFieldKeybinding exercises the `bind -x` field-completion function.
// It sets READLINE_LINE/READLINE_POINT and calls the function directly —
// valid because READLINE_LINE is a plain variable the function reads, and
// real bash populates it with the entire line (pty-verified), which is
// exactly what we set here. (Contrast with COMP_LINE, which bash scopes to
// the current command — the reason Tab completion can't work.)
func TestFieldKeybinding(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "a.csv")
	if err := os.WriteFile(csv, []byte("name,dept,salary\nA,x,1\n"), 0o644); err != nil {
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
eval "$(ssql -field-keybinding)" 2>/dev/null
READLINE_LINE=%q
READLINE_POINT=${#READLINE_LINE}
_ssql_complete_field >/dev/null 2>&1
printf '%%s' "$READLINE_LINE"
`, binDir, line)
		out, err := exec.Command("bash", "-c", script).Output()
		if err != nil {
			t.Fatalf("run %q: %v", line, err)
		}
		return string(out)
	}

	base := "ssql from csv " + csv
	cases := []struct{ in, want string }{
		// unique prefix → complete + trailing space
		{base + " | ssql group-by d", base + " | ssql group-by dept "},
		{base + " | ssql group-by sa", base + " | ssql group-by salary "},
		// rename upstream is honoured — offers the new name
		{base + " | ssql rename -as name person | ssql group-by p",
			base + " | ssql rename -as name person | ssql group-by person "},
		// ambiguous (name/dept/salary, no common prefix) → line unchanged (lists instead)
		{base + " | ssql group-by ", base + " | ssql group-by "},
		// no upstream pipe → no-op
		{"ssql group-by ", "ssql group-by "},
	}
	for _, c := range cases {
		if got := run(c.in); got != c.want {
			t.Errorf("in=%q\n got=%q\nwant=%q", c.in, got, c.want)
		}
	}
}

// TestFieldKeybindingEmitted confirms `-field-keybinding` emits the
// function and the bind.
func TestFieldKeybindingEmitted(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "-field-keybinding").Output()
	if err != nil {
		t.Fatalf("-field-keybinding: %v", err)
	}
	for _, want := range []string{"_ssql_complete_field", "READLINE_LINE", "SSQL_MODE=schema", "generate schema", `bind -x '"\C-x\C-f": _ssql_complete_field'`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("keybinding script missing %q", want)
		}
	}
}
