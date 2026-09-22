package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// DFC134: a pipeline built from untrusted data must not be steerable by
// that data. Every element here is passed as ONE argv element (no shell),
// so what is tested is ssql's own reading of it.
//
//   - flag slots bind by arity, so any string is a value (§3.1), including
//     the root flags main.go used to look for in EVERY argument: `where -if
//     name eq -shell-init` printed the bash completion script
//   - positional slots are safe through -arg (§5.2); bare, they are not, and
//     the last case pins that contrast so nobody mistakes the bare form for
//     safe
func TestArgumentsAreNotSyntax(t *testing.T) {
	bin := corpusBin(t)
	hostile := filepath.Join(corpusData(t), "hostile.csv")

	run := func(t *testing.T, stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("ssql %q: %v\n%s", args, err, errb.String())
		}
		return out.String()
	}
	records := run(t, "", "from", "csv", hostile)

	t.Run("a value spelled like a root flag is a value", func(t *testing.T) {
		for _, v := range []string{"-shell-init", "--shell-init", "-field-keybinding", "-completion-script", "-spec-json", "-help", "-generate", "-arg", "--", "+", "-"} {
			out := run(t, records, "where", "-if", "name", "eq", v)
			if strings.Contains(out, "complete ") || strings.Contains(out, "bind ") || strings.Contains(out, "USAGE") || strings.Contains(out, `"code"`) {
				t.Errorf("where -if name eq %s produced something other than records:\n%.300s", v, out)
			}
			if strings.Contains(out, "amy") {
				t.Errorf("where -if name eq %s matched rows it should not:\n%s", v, out)
			}
		}
	})

	t.Run("-arg carries any column name", func(t *testing.T) {
		out := run(t, records, "include", "-arg", "-generate", "-arg", "-", "-arg", "+x", "-arg", "-desc")
		csv := run(t, out, "to", "csv")
		if want := "-generate,-,+x,-desc\n3,k,q,7\n"; !strings.HasPrefix(csv, want) {
			t.Errorf("include through -arg:\n got %q\nwant prefix %q", csv, want)
		}
	})

	t.Run("bare, the same column name is a flag (the hole -arg closes)", func(t *testing.T) {
		out := run(t, records, "include", "name", "-generate")
		if !strings.Contains(out, `"code"`) {
			t.Errorf("expected the bare form to switch include into code generation; if this changed, update DFC134 §3.2:\n%.300s", out)
		}
	})
}
