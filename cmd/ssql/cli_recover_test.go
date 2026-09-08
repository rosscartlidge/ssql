package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIPanicIsAnErrorLine: a panic from deep in a pipeline (here an
// aggregation expression whose result is a map) reaches the user as one
// "Error: …" line with exit 1, not a goroutine dump — the rule generated
// programs already follow. SSQL_DEBUG=1 keeps the trace.
func TestCLIPanicIsAnErrorLine(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.csv"), []byte("k,n\nx,1\nx,2\ny,3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pipeline := bin + " from csv a.csv | " + bin + " group-by k -stream-expr '{s:0}' '{s:s+n}' '{x:s}' r"
	c := exec.Command("bash", "-c", pipeline)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err == nil {
		t.Fatalf("a map-valued aggregation must fail:\n%s", out)
	}
	s := string(out)
	if !strings.Contains(s, "Error: StreamExprAgg: expression returned map") || strings.Contains(s, "goroutine ") || strings.Contains(s, "panic:") {
		t.Errorf("want one Error line, no trace:\n%s", s)
	}

	c = exec.Command("bash", "-c", "export SSQL_DEBUG=1; "+pipeline)
	c.Dir = dir
	out, _ = c.CombinedOutput()
	if !strings.Contains(string(out), "goroutine ") {
		t.Errorf("SSQL_DEBUG=1 should keep the trace:\n%s", out)
	}
}
