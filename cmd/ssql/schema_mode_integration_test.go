package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSchemaModePipeline runs real SSQL_MODE=schema subprocesses end to
// end: a source emits a schema header, transforms rewrite it, and
// `generate schema` prints the field list — the engine behind the bash
// completion shim.
func TestSchemaModePipeline(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(csv, []byte("name,dept,salary\nAlice,eng,100\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		pipeline string
		want     []string
	}{
		{"source", "from csv " + csv, []string{"name", "dept", "salary"}},
		{"rename", "from csv " + csv + " | " + bin + " rename -as name person", []string{"person", "dept", "salary"}},
		{"exclude", "from csv " + csv + " | " + bin + " exclude salary", []string{"name", "dept"}},
		{"group-by", "from csv " + csv + " | " + bin + " group-by dept -count n", []string{"dept", "n"}},
		{"pivot undeterminable", "from csv " + csv + " | " + bin + " pivot dept salary", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script := "(export SSQL_MODE=schema; " + bin + " " + c.pipeline + ") | " + bin + " generate schema"
			out, err := exec.Command("bash", "-c", script).Output()
			if err != nil {
				t.Fatalf("pipeline failed: %v", err)
			}
			var got []string
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line != "" {
					got = append(got, line)
				}
			}
			if !equalStrings(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestCompletionScriptIncludesSchemaWrapper confirms `-completion-script`
// emits both autocli's completer and ssql's pipeline-aware wrapper.
func TestCompletionScriptIncludesSchemaWrapper(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin, "-completion-script").Output()
	if err != nil {
		t.Fatalf("-completion-script: %v", err)
	}
	for _, want := range []string{"_autocli_complete", "_ssql_schema_complete", "SSQL_MODE=schema", "generate schema", "complete -F _ssql_schema_complete ssql"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("completion script missing %q", want)
		}
	}
}

// TestBashCompletionWrapper exercises the _ssql_schema_complete function
// the way REAL bash invokes it: COMP_WORDS is scoped to the simple
// command under the cursor (the stage after the last pipe), and the full
// pipeline lives in COMP_LINE. (An earlier version read the upstream from
// COMP_WORDS and silently fell through — this test guards against that.)
func TestBashCompletionWrapper(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "p.csv")
	if err := os.WriteFile(csv, []byte("name,dept,salary\nA,x,1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// `command ssql generate schema` in the wrapper needs ssql on PATH.
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(binDir, "ssql")); err != nil {
		t.Fatal(err)
	}

	// Completing `ssql from p.csv | ssql rename -as name person | ssql group-by <TAB>`.
	script := `
export PATH="` + binDir + `:$PATH"
eval "$(ssql -completion-script)"
COMP_LINE="ssql from ` + csv + ` | ssql rename -as name person | ssql group-by "
COMP_POINT=${#COMP_LINE}
COMP_WORDS=(ssql group-by "")
COMP_CWORD=2
_ssql_schema_complete
echo "${COMPREPLY[@]}"
`
	out, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("completion run failed: %v", err)
	}
	got := strings.Fields(string(out))
	want := []string{"person", "dept", "salary"} // 'name' renamed to 'person'
	if !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
