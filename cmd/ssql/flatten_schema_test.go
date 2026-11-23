package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFlattenSchemaMode: flatten's new names are data, so schema mode
// knows them only when the input header already lists FIELD.key (a JSONL
// source); otherwise its answer is unknown (no header), never a header
// without them, which made the FAQ's pre-flight refuse a correct
// `cast -type payload json | flatten payload | group-by payload.user_id`
// (DFC169 §12.7, 2026-11-23). A field whose type can never hold an object
// is refused in both modes, and JSON text gets the cast it needs.
func TestFlattenSchemaMode(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	if err := os.WriteFile(log, []byte(`2026-11-23T01:00:00Z {"user_id":7,"amount":3.5}
2026-11-23T01:01:00Z {"user_id":7,"amount":1}
2026-11-23T01:02:00Z {"user_id":9,"amount":2}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	obj := filepath.Join(dir, "obj.jsonl")
	if err := os.WriteFile(obj, []byte("{\"id\":1,\"p\":{\"a\":1,\"b\":\"x\"}}\n{\"id\":2,\"p\":{\"a\":2,\"b\":\"y\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extract := bin + " from lines " + log + " | " + bin + ` extract -field line -re '(?P<ts>\S+) (?P<payload>\{.*\})' -skip`
	run := func(mode, pipeline string) (string, error) {
		env := ""
		if mode != "" {
			env = "export SSQL_MODE=" + mode + "; "
		}
		out, err := exec.Command("bash", "-c", "set -o pipefail; "+env+pipeline).CombinedOutput()
		return string(out), err
	}

	// A string field: refused in both modes, with the cast that fixes it.
	for _, mode := range []string{"", "schema"} {
		out, err := run(mode, extract+" | "+bin+" flatten payload")
		if err == nil || !strings.Contains(out, "cast -type payload json") {
			t.Errorf("mode %q: flatten of a string field: %v\n%s", mode, err, out)
		}
	}

	// The cast: exec runs it, and schema mode does not refuse the
	// group-by on the keys it cannot see.
	good := extract + " | " + bin + " cast -type payload json | " + bin + " flatten payload | " + bin + " group-by payload.user_id -count requests -sum payload.amount total"
	if out, err := run("", good+" | "+bin+" to csv"); err != nil || !strings.Contains(out, "7,2,4.5") {
		t.Errorf("exec of the cast pipeline: %v\n%s", err, out)
	}
	if out, err := run("schema", good+" | "+bin+" generate schema"); err != nil {
		t.Errorf("schema mode refused the cast pipeline: %v\n%s", err, out)
	}

	// Keys the header already lists: schema mode still answers them.
	out, err := run("schema", bin+" from jsonl "+obj+" | "+bin+" flatten p | "+bin+" generate schema")
	if err != nil || strings.Fields(out)[0] != "id" || !strings.Contains(out, "p.a") || !strings.Contains(out, "p.b") {
		t.Errorf("flatten of a JSONL object in schema mode: %v\n%s", err, out)
	}
}
