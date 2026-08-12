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
	// from tsv auto-detects the delimiter (first non-identifier header
	// byte); schema mode must use the SAME rule — it used to hard-split
	// on tab, so a pipe-delimited file completed one bogus
	// "name|age|dept" field.
	tabTSV := filepath.Join(dir, "tab.tsv")
	if err := os.WriteFile(tabTSV, []byte("name\tage\tdept\nAlice\t30\tEng\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pipeTSV := filepath.Join(dir, "pipe.tsv")
	if err := os.WriteFile(pipeTSV, []byte("name|age|dept\nAlice|30|Eng\n"), 0o644); err != nil {
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
		{"tsv tab", "from tsv " + tabTSV, []string{"name", "age", "dept"}},
		{"tsv pipe-delimited", "from tsv " + pipeTSV, []string{"name", "age", "dept"}},
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
