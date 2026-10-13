package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestToLinesAllLanes: `to lines FIELD` writes one field's text per
// record, byte-identical in exec, record, typed and parallel, and it is
// the piece that makes a views table work (DFC148 §4.4): a row selected
// with where, written with to lines, runs through `run -stdin`. Two rows
// reaching run is a named error; an unknown field is loud in exec and in
// generate mode.
func TestToLinesAllLanes(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	files := map[string]string{
		"emp.csv": "id,name,age\n1,ann,25\n2,bob,35\n3,cy,41\n",
		"views.csv": "name,pipeline\n" +
			`all,"[[""from"",""csv"",""emp.csv""]]"` + "\n" +
			`older,"[[""from"",""csv"",""emp.csv""],[""where"",""-if"",""age"",""gt"",""30""]]"` + "\n",
		"views.json": `[{"name":"all","pipeline":[["from","csv","emp.csv"]]},` +
			`{"name":"older","pipeline":[["from","csv","emp.csv"],["where","-if","age","gt","30"]]}]` + "\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shell := func(script string) (string, error) {
		c := exec.Command("bash", "-c", script)
		c.Dir = dir
		out, err := c.CombinedOutput()
		return string(out), err
	}
	wantLines := `[["from","csv","emp.csv"]]` + "\n" +
		`[["from","csv","emp.csv"],["where","-if","age","gt","30"]]` + "\n"
	wantOlder := "id,name,age\n2,bob,35\n3,cy,41\n"

	// Every lane writes the same text, to stdout and to a file.
	pipeline := bin + " from csv views.csv | " + bin + " to lines pipeline"
	out, err := shell(pipeline)
	if err != nil || out != wantLines {
		t.Fatalf("exec: %v\n%q", err, out)
	}
	for _, lane := range []string{"record", "typed", "parallel"} {
		if o, err := runGeneratedPipeline(t, bin, dir, lane, pipeline); err != nil || o != wantLines {
			t.Errorf("%s: %v\n%q", lane, err, o)
		}
		if o, err := runGeneratedPipeline(t, bin, dir, lane, pipeline+" out_"+lane+".txt"); err != nil {
			t.Errorf("%s to file: %v\n%s", lane, err, o)
		} else if b, _ := os.ReadFile(filepath.Join(dir, "out_"+lane+".txt")); string(b) != wantLines {
			t.Errorf("%s file: %q", lane, b)
		}
	}

	// The named view, from the CSV and the JSON form of the views table,
	// in every lane that produces the document.
	for _, src := range []string{"from csv views.csv", "from json views.json"} {
		sel := bin + " " + src + " | " + bin + " where -if name eq older | " + bin + " to lines pipeline"
		consume := " | " + bin + " run -stdin | " + bin + " to csv"
		if o, err := shell(sel + consume); err != nil || o != wantOlder {
			t.Errorf("exec %s: %v\n%q", src, err, o)
		}
		for _, lane := range []string{"record", "typed"} {
			doc, err := runGeneratedPipeline(t, bin, dir, lane, sel)
			if err != nil {
				t.Errorf("%s %s: %v\n%s", lane, src, err, doc)
				continue
			}
			if err := os.WriteFile(filepath.Join(dir, "doc.json"), []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}
			if o, err := shell(bin + " run -stdin < doc.json | " + bin + " to csv"); err != nil || o != wantOlder {
				t.Errorf("%s %s through run: %v\n%q (document %q)", lane, src, err, o, doc)
			}
		}
	}

	// Two rows reach run: a named error, not a parse of the concatenation.
	o, err := shell(pipeline + " | " + bin + " run -stdin")
	if err == nil || !strings.Contains(o, "a second document follows the first") {
		t.Errorf("two documents: %v\n%s", err, o)
	}

	// An unknown field is loud, naming the fields: in exec; at generate
	// time in typed mode (the struct is known); in the record program's
	// first record (the fields are not known at generate time), and at
	// generate time after an upstream that declares its fields.
	want := `unknown field "nope" (available: name, pipeline)`
	o, err = shell(bin + " from csv views.csv | " + bin + " to lines nope")
	if err == nil || !strings.Contains(o, want) {
		t.Errorf("exec unknown field: %v\n%s", err, o)
	}
	o, err = shell("export SSQL_MODE=typed && " + bin + " from csv views.csv | " + bin + " to lines nope | " + bin + " generate go")
	if err == nil || !strings.Contains(o, want) {
		t.Errorf("typed unknown field at generate time: %v\n%s", err, o)
	}
	o, err = runGeneratedPipeline(t, bin, dir, "record", bin+" from csv views.csv | "+bin+" to lines nope")
	if err == nil || !strings.Contains(o, want) || strings.Contains(o, "goroutine ") {
		t.Errorf("record unknown field at run time: %v\n%s", err, o)
	}
	o, err = shell("export SSQL_MODE=record && " + bin + " from csv views.csv | " + bin + " group-by name -count n | " + bin + " to lines nope | " + bin + " generate go")
	if err == nil || !strings.Contains(o, `unknown field "nope" (available: name, n)`) {
		t.Errorf("record unknown field after group-by, at generate time: %v\n%s", err, o)
	}
}
