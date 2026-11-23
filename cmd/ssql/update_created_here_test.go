package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestUpdateReadsAFieldItCreates: every assignment of one update sees the
// row as it entered (`ssql conventions -category evaluation`), so an
// expression reading a field the same update creates is refused — up
// front from the header, in schema mode too (the FAQ's pre-flight), and
// at the first row of headerless input — with the second update that
// fixes it. Exec said only "unknown field(s): amt" after schema mode had
// accepted it, and the FAQ's model never found the fix (2026-11-23).
func TestUpdateReadsAFieldItCreates(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	run := func(mode, pipeline string) (string, error) {
		env := ""
		if mode != "" {
			env = "export SSQL_MODE=" + mode + "; "
		}
		out, err := exec.Command("bash", "-c", "set -o pipefail; "+env+pipeline).CombinedOutput()
		return string(out), err
	}
	const want = "this update creates it"
	bad := bin + ` from range 1 | ` + bin + ` update -set-expr amt '1' -set-expr check 'amt * 3'`
	for _, mode := range []string{"", "schema"} {
		if out, err := run(mode, bad); err == nil || !strings.Contains(out, want) || !strings.Contains(out, "second update") {
			t.Errorf("mode %q: %v\n%s", mode, err, out)
		}
	}
	// -if-expr reads it too; headerless input refuses at the first row.
	if out, err := run("", `echo '{"n":1}' | `+bin+` update -set t 1 -if-expr 't > 0' -set u 2`); err == nil || !strings.Contains(out, want) {
		t.Errorf("headerless -if-expr: %v\n%s", err, out)
	}
	// Reading a field the input HAS is the original value, not an error.
	if out, err := run("", bin+` from range 2 | `+bin+` update -set-expr n 'n * 10' -set-expr m 'n + 1' | `+bin+` to csv`); err != nil || !strings.Contains(out, "10,2") {
		t.Errorf("reassigned input field: %v\n%s", err, out)
	}
	// The fix the message names.
	if out, err := run("", bin+` from range 1 | `+bin+` update -set-expr amt '1' | `+bin+` update -set-expr check 'amt * 3' | `+bin+` to csv`); err != nil || !strings.Contains(out, "1,1,3") {
		t.Errorf("two updates: %v\n%s", err, out)
	}
}
