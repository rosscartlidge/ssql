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
		{"group-by rollup", "from csv " + csv + " | " + bin + " group-by name dept -count n -rollup",
			[]string{"name", "dept", "n", "name_n", "name_dept_n"}},
		{"group-by cube", "from csv " + csv + " | " + bin + " group-by name dept -count n -cube",
			[]string{"name", "dept", "n", "name_n", "dept_n", "name_dept_n"}},
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

// Schema mode carries TYPES from a delimited source (a sampled read with
// the source's own config, so -type holds) through the rules: a
// surviving field keeps its type, rename moves it, a created field is
// "any". `generate schema -data` puts the list on the wire as
// (field, type) records, so it can flow into any sink.
func TestSchemaModeTypesAndData(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "people.csv")
	if err := os.WriteFile(csv, []byte("name,dept,salary,rate,hired\nAlice,eng,100,1.5,2026-01-05T09:00:00Z\nBob,ops,90,2,2026-02-01T09:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		pipeline string
		want     string
	}{
		{"source types", "ssql from csv " + csv, "field,type\nname,string\ndept,string\nsalary,int\nrate,float\nhired,string\n"},
		{"-type holds", "ssql from csv " + csv + " -type hired time | ssql include name hired", "field,type\nname,string\nhired,time\n"},
		{"rename moves the type, group-by creates any", "ssql from csv " + csv + " | ssql rename -as salary pay | ssql group-by dept -sum pay total", "field,type\ndept,string\ntotal,any\n"},
		{"through a sink", "ssql from csv " + csv + " | ssql exclude rate | ssql to table", "field,type\nname,string\ndept,string\nsalary,int\nhired,string\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script := bin + " generate schema -pipeline " + shellQuoteForTest(strings.ReplaceAll(c.pipeline, "ssql ", bin+" ")) + " -data | " + bin + " to csv"
			out, err := exec.Command("bash", "-c", script).CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", script, err, out)
			}
			if string(out) != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", out, c.want)
			}
		})
	}
}

func shellQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
