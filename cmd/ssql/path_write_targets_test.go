package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPathWriteTargetsRefused: every command that WRITES a field refuses
// a dotted path into a nested value by name, with the way to do it in
// the message (the 2026-10-09 survey found cast, rename, exclude, fill,
// update -set and from -type passing validation and then doing nothing
// — cast even left the schema header claiming the new type).
func TestPathWriteTargetsRefused(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(data, []byte(`{"id":1,"user":"alice","addr":{"city":"NYC","zip":"10001"}}
{"id":2,"user":"bob","addr":{"city":"SF","zip":"94105"}}
{"id":3,"user":"eve"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, stage, want string
	}{
		{"cast", "cast -type addr.zip int", "cast: \"addr.zip\" is a path into the nested value \"addr\""},
		{"rename", "rename -as addr.city city", "rename: \"addr.city\" is a path"},
		{"exclude", "exclude addr.city", "exclude: \"addr.city\" is a path"},
		{"fill -down", "fill -down addr.city", "fill: \"addr.city\" is a path"},
		{"fill -default", "fill -default addr.city X", "fill: \"addr.city\" is a path"},
		{"update -set", "update -set addr.city X", "update: \"addr.city\" is a path"},
		{"update -set-expr", "update -set-expr addr.zip '1'", "update: \"addr.zip\" is a path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", bin+" from jsonl "+data+" | "+bin+" "+c.stage+" | "+bin+" to csv")
			out, _ := cmd.CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: want the refusal %q, got:\n%s", c.stage, c.want, out)
			}
			if !strings.Contains(string(out), "cannot be written") && !strings.Contains(string(out), "not written") {
				t.Errorf("%s: the message should say a path is not writable, got:\n%s", c.stage, out)
			}
		})
	}
	t.Run("from -type", func(t *testing.T) {
		out, _ := exec.Command("bash", "-c", bin+" from jsonl "+data+" -type addr.zip int | "+bin+" to csv").CombinedOutput()
		if !strings.Contains(string(out), "path into a nested value") {
			t.Errorf("from -type on a path must be refused, got:\n%s", out)
		}
	})
	t.Run("literal dotted name is still a plain new column", func(t *testing.T) {
		out, err := exec.Command("bash", "-c", bin+" from jsonl "+data+" | "+bin+" update -set a.b 1 | "+bin+" include id a.b | "+bin+" to csv").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "id,a.b\n1,1") {
			t.Errorf("a dotted name whose head is not a field is a literal column: %v\n%s", err, out)
		}
	})
}

// TestTypedToTableKeepsPathColumn: `to table addr.city id` in the typed
// lane showed every column but the path until 2026-10-09 (the typed
// table knew no such struct field and dropped it silently); now the
// stage falls back to record mode, which resolves the path.
func TestTypedToTableKeepsPathColumn(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(data, []byte(`{"id":1,"user":"alice","addr":{"city":"NYC","zip":"10001"}}
{"id":2,"user":"bob","addr":{"city":"SF","zip":"94105"}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "generate", "go", "-run", "-pipeline", bin+" from jsonl "+data+" | "+bin+" to table addr.city id")
	cmd.Env = append(os.Environ(), "SSQL_MODE=typed", "SSQL_MODULE_DIR="+mustRepoRoot(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("typed to table with a path column: %v\n%s", err, out)
	}
	header := strings.SplitN(string(out), "\n", 2)[0]
	if !strings.Contains(header, "addr.city") || !strings.Contains(string(out), "NYC") {
		t.Errorf("the path column must be shown, got:\n%s", out)
	}
}
