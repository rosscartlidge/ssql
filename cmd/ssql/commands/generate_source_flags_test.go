package commands

import (
	"bytes"
	"encoding/json"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

// Every generate target carries the same fragment-source flags (DFC139
// §3.C): -pipeline, -script, -json and -mode are declared once by
// pipelineSourceFlags and this pins that every leaf calls it. Until
// v4.109 -script and -mode existed on `go` only, by omission.
func TestGenerateTargetsShareSourceFlags(t *testing.T) {
	root := cf.NewCommand("ssql")
	RegisterGenerate(root)
	var buf bytes.Buffer
	if err := root.Build().WriteSpecJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Subcommands map[string]struct {
			Subcommands map[string]struct {
				Flags []struct {
					Names []string `json:"names"`
				} `json:"flags"`
			} `json:"subcommands"`
		} `json:"subcommands"`
	}
	if err := json.Unmarshal(buf.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	gen, ok := spec.Subcommands["generate"]
	if !ok || len(gen.Subcommands) < 5 {
		t.Fatalf("generate has %d targets in -spec-json; expected go, sql, ssql, json, schema", len(gen.Subcommands))
	}
	want := []string{"-pipeline", "-script", "-json", "-mode"}
	for name, leaf := range gen.Subcommands {
		have := map[string]bool{}
		for _, f := range leaf.Flags {
			for _, n := range f.Names {
				have[n] = true
			}
		}
		for _, w := range want {
			if !have[w] {
				t.Errorf("generate %s lacks %s: every target must call pipelineSourceFlags", name, w)
			}
		}
	}
}
