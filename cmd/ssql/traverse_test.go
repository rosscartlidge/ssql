package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `where` after `traverse` filters ROUTES; moved before the traversal
// it would filter SEEDS (and the node field is rewritten by the
// traversal), so the optimiser must leave it where it is. A plain inner
// join, where the left-only predicate does move ahead of the join, shows
// the assertion discriminates (DFC161).
func TestTraversePushdownGuard(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	seeds := filepath.Join(dir, "seeds.csv")
	flights := filepath.Join(dir, "flights.csv")
	os.WriteFile(seeds, []byte("city,label\nSYD,home\n"), 0o644)
	os.WriteFile(flights, []byte("src,dst,mins\nSYD,MEL,90\nMEL,ADL,75\n"), 0o644)

	optimise := func(stage string) string {
		t.Helper()
		pipeline := "export SSQL_MODE=record && (" + bin + " from csv " + seeds + " | " + bin + " " + stage + " | " + bin + " where -if city ne ADL | " + bin + " to csv) | " + bin + " generate ssql"
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+pipeline)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("generate ssql [%s]: %v\n%s", stage, err, errb.String())
		}
		return out.String()
	}
	whereBefore := func(s, cmd string) bool {
		w, j := strings.Index(s, " where "), strings.Index(s, " "+cmd+" ")
		return w >= 0 && j >= 0 && w < j
	}
	if s := optimise("join " + flights + " -on city src"); !whereBefore(s, "join") {
		t.Errorf("plain inner join: the left-only where should move before the join, got\n%s", s)
	}
	if s := optimise("traverse " + flights + " -from city src -to dst -depth hops"); whereBefore(s, "traverse") {
		t.Errorf("traverse: the where must stay after the traversal, got\n%s", s)
	}
}

// The v4.135 spelling, `join … -follow`, is gone: it must fail loudly,
// never run as a plain join.
func TestJoinFollowRemoved(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	seeds := filepath.Join(dir, "seeds.csv")
	flights := filepath.Join(dir, "flights.csv")
	os.WriteFile(seeds, []byte("city\nSYD\n"), 0o644)
	os.WriteFile(flights, []byte("src,dst,mins\nSYD,MEL,90\n"), 0o644)
	cmd := exec.Command("bash", "-c", "set -o pipefail; "+bin+" from csv "+seeds+" | "+bin+" join "+flights+" -on city src -follow dst | "+bin+" to csv")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("join -follow ran:\n%s", out)
	}
	if !strings.Contains(string(out), "-follow") {
		t.Errorf("the error should name -follow, got:\n%s", out)
	}
}
