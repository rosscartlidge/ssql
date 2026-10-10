package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSidecarRoundTrip (DFC146): `to csv FILE -sidecar` writes a
// Frictionless datapackage.json beside the file in exec, record and
// typed programs, with the pipeline's types; `from csv` then reads the
// file back typed by it, so a CSV round trip keeps its types (a date
// stays time, text that looks numeric stays text) — and `generate
// schema -datapackage` prints the same schema for a pipeline.
func TestSidecarRoundTrip(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	root := mustRepoRoot(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.csv")
	// `code` is text the sample would call a number; `when` a date the
	// sample would leave as text. The source sidecar types them.
	if err := os.WriteFile(src, []byte("id,amount,when,code\n1,10,2026-01-05,1e3\n2,20,2026-01-06,2.50\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "datapackage.json"), []byte(`{"resources":[{"path":"src.csv","schema":{"fields":[{"name":"id","type":"integer"},{"name":"amount","type":"number"},{"name":"when","type":"date"},{"name":"code","type":"string"}]}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	fieldsOf := func(t *testing.T, pkgPath, resource string) map[string]string {
		t.Helper()
		data, err := os.ReadFile(pkgPath)
		if err != nil {
			t.Fatalf("%s: %v", pkgPath, err)
		}
		var pkg struct {
			Resources []struct {
				Path   string
				Schema struct{ Fields []struct{ Name, Type string } }
			}
		}
		if err := json.Unmarshal(data, &pkg); err != nil {
			t.Fatalf("%s: %v\n%s", pkgPath, err, data)
		}
		for _, r := range pkg.Resources {
			if r.Path == resource {
				m := map[string]string{}
				for _, f := range r.Schema.Fields {
					m[f.Name] = f.Type
				}
				return m
			}
		}
		t.Fatalf("%s: no resource %s\n%s", pkgPath, resource, data)
		return nil
	}

	for _, mode := range []string{"exec", "record", "typed"} {
		t.Run(mode, func(t *testing.T) {
			out := filepath.Join(dir, mode)
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatal(err)
			}
			pipeline := bin + " from csv " + src + " | " + bin + " update -set-expr total 'amount * 2' | " + bin + " to csv " + out + "/o.csv -sidecar"
			script := pipeline
			if mode != "exec" {
				script = "(export SSQL_MODE=" + mode + "; " + pipeline + ") | " + bin + " generate go -run"
			}
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "SSQL_MODULE_DIR="+root)
			if o, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", script, err, o)
			}
			got := fieldsOf(t, filepath.Join(out, "datapackage.json"), "o.csv")
			want := map[string]string{"id": "integer", "amount": "number", "when": "datetime", "code": "string", "total": "number"}
			for k, w := range want {
				if got[k] != w {
					t.Errorf("%s: field %s = %q, want %q (all: %v)", mode, k, got[k], w, got)
				}
			}
			// Read back: the written package types the written file.
			o, err := exec.Command("bash", "-c", bin+" from csv "+out+"/o.csv | head -1").Output()
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range []string{`"when":"time"`, `"code":"string"`, `"total":"float"`} {
				if !strings.Contains(string(o), w) {
					t.Errorf("%s: read back header %s lacks %s", mode, strings.TrimSpace(string(o)), w)
				}
			}
		})
	}

	// The schema for a pipeline, without writing data.
	o, err := exec.Command(bin, "generate", "schema", "-datapackage", "-pipeline",
		bin+" from csv "+src+" | "+bin+" group-by code -sum amount total").Output()
	if err != nil {
		t.Fatal(err)
	}
	var ts struct{ Fields []struct{ Name, Type string } }
	if err := json.Unmarshal(o, &ts); err != nil {
		t.Fatalf("-datapackage: %v\n%s", err, o)
	}
	if len(ts.Fields) != 2 || ts.Fields[0].Name != "code" || ts.Fields[0].Type != "string" || ts.Fields[1].Type != "number" {
		t.Errorf("-datapackage: %s", o)
	}

	// -sidecar needs a file to put the package beside.
	if o, err := exec.Command("bash", "-c", bin+" from csv "+src+" | "+bin+" to csv -sidecar").CombinedOutput(); err == nil || !strings.Contains(string(o), "needs an output FILE") {
		t.Errorf("to csv -sidecar to stdout: err=%v\n%s", err, o)
	}
}
