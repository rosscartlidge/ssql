package main

// End-to-end test for the Op descriptor path (DFC123 slice 2): the
// optimiser consumes Op.Argv instead of re-tokenizing the
// shell-quoted Command string, so values that shell quoting can't
// round-trip (embedded single quotes) survive optimisation intact.
// Before Op, `where -if name eq O'Brien` came out of `generate ssql`
// as `"OBrien"` — a silently different pipeline.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSSQLPreservesQuotedValues(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "q.csv")
	if err := os.WriteFile(csv, []byte("name,val\nO'Brien,1\nNew York,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The inner stages resolve `ssql` via PATH — pin them to the
	// just-built binary (mirrors TestOptimiseKeybinding).
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(binDir, "ssql")); err != nil {
		t.Fatal(err)
	}
	// Two wheres so where-merge fires — the merged stage must carry
	// both values undamaged.
	pipeline := "ssql from " + csv + " | ssql where -if name eq \"O'Brien\" | ssql where -if val gt 0 | ssql to table"
	script := "export PATH=" + binDir + ":$PATH\n" +
		bin + " generate ssql -pipeline " + "'" + strings.ReplaceAll(pipeline, "'", `'\''`) + "'"
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("generate ssql: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, `O'\''Brien`) {
		t.Errorf("embedded quote not preserved through optimisation:\n%s", got)
	}
	if strings.Contains(got, "OBrien") {
		t.Errorf("value was mangled (the pre-Op tokenizer bug):\n%s", got)
	}
	if !strings.Contains(got, "-if val gt 0") {
		t.Errorf("where-merge did not fire:\n%s", got)
	}
}
