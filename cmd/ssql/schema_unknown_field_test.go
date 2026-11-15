package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestSchemaModeRejectsUnknownFields: schema mode is as strict as exec
// about field names (DFC170). Every flag argument -spec-json says takes a
// field (the type matrix's walk, DFC163 §7) is given a column the input
// lacks; when exec refuses it naming that column, schema mode must refuse
// it too, and schema mode must not refuse what exec accepts. Until
// 2026-11-15 every command answering schema mode before its own field
// check let `group-by department` through silently: the DFC169 FAQ's
// pre-flight, Ctrl-O and `run`'s check all trusted that answer.
func TestSchemaModeRejectsUnknownFields(t *testing.T) {
	if testing.Short() {
		t.Skip("runs every field-taking flag twice")
	}
	bin := corpusBin(t)
	data := corpusData(t)
	repl := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data)
	const missing = "nope"
	cells, _ := matrixCells(t, bin)
	seen := map[string]bool{}
	for _, c := range cells {
		if c.empties || c.typ.col != "s" {
			continue // one cell per flag: the string column's variants
		}
		// The cell's stage with the missing column in place of the
		// string column the matrix put there; a join's right side keeps
		// its real column, so the refusal is the stage's, not the side's.
		stage := strings.SplitN(c.pipeline, " | {{.bin}} ", 2)[1]
		stage = strings.ReplaceAll(" "+stage+" ", " s ", " "+missing+" ")
		stage = strings.ReplaceAll(stage, " include "+missing+" k ", " include s k ")
		pipeline := repl.Replace(matrixFrom("matrix.csv", "s") + " | {{.bin}} " + strings.TrimSpace(stage))
		name := strings.TrimSuffix(c.name, "_s")
		if seen[pipeline] {
			continue
		}
		seen[pipeline] = true
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := []string{"TZ=UTC"}
			_, execErr := schemaFieldRun(pipeline, env)
			_, schemaErr := schemaFieldRun(pipeline+" | "+bin+" generate schema", append(env, "SSQL_MODE=schema"))
			execRefused := execErr != nil && strings.Contains(execErr.Error(), missing)
			schemaRefused := schemaErr != nil && strings.Contains(schemaErr.Error(), missing)
			d := ""
			switch {
			case execRefused && !schemaRefused:
				d = fmt.Sprintf("exec refuses the missing column, schema mode does not (%v)\n  exec: %v", schemaErr, execErr)
			case !execRefused && schemaErr != nil:
				d = fmt.Sprintf("schema mode refuses what exec does not: %v\n  exec: %v", schemaErr, execErr)
			}
			if reason, ok := schemaUnknownKnown[name]; ok {
				if d == "" {
					t.Errorf("listed in schemaUnknownKnown (%s) but now agrees: delete the entry", reason)
				}
				t.Skipf("known: %s", reason)
			}
			if d != "" {
				t.Errorf("%s\n  pipeline: %s", d, pipeline)
			}
		})
	}
}

// schemaUnknownKnown lists the flags whose schema mode still accepts a
// missing column exec refuses; each leaves with its fix (DFC170 §4).
var schemaUnknownKnown = map[string]string{}

// schemaFieldRun runs a pipeline, failing it when any stage fails.
func schemaFieldRun(pipeline string, env []string) (string, error) {
	cmd := exec.Command("bash", "-c", "set -o pipefail; "+pipeline)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
