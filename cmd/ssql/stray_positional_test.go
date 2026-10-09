package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestStrayPositionalRefused: an argument a command does not declare is
// an error naming it, not silently dropped — `distinct addr.city` and
// `count foo` ran as the bare command until 2026-10-09 (autocli's
// StrictPositionals, opted into at ssql's root). Declared positionals,
// variadic lists and the format+file forms of `from` are unaffected.
func TestStrayPositionalRefused(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	data := filepath.Join(t.TempDir(), "d.csv")
	if err := os.WriteFile(data, []byte("id,name\n1,a\n1,a\n2,b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(stage string) (string, error) {
		out, err := exec.Command("bash", "-c", "set -o pipefail; "+bin+" from csv "+data+" | "+bin+" "+stage+" | "+bin+" to csv").CombinedOutput()
		return string(out), err
	}
	for _, c := range []struct{ stage, arg string }{
		{"distinct nosuchfield", "nosuchfield"},
		{"count foo", "foo"},
		{"limit 1 extra", "extra"},
		{"sort name extra -desc", ""}, // variadic FIELDS: "extra" is a field name → unknown field, not a stray
	} {
		out, err := run(c.stage)
		if err == nil {
			t.Errorf("%s: expected an error, got output:\n%s", c.stage, out)
			continue
		}
		if c.arg != "" && !strings.Contains(out, "unexpected argument \""+c.arg+"\"") {
			t.Errorf("%s: want the stray argument named, got:\n%s", c.stage, out)
		}
	}
	for _, stage := range []string{"distinct", "limit 1", "sort name -desc", "include id name"} {
		if out, err := run(stage); err != nil {
			t.Errorf("%s: %v\n%s", stage, err, out)
		}
	}
	// count prints a number, not records: no sink after it.
	if out, err := exec.Command("bash", "-c", "set -o pipefail; "+bin+" from csv "+data+" | "+bin+" count").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "3" {
		t.Errorf("count: %v\n%s", err, out)
	}
}
