package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `where` after `join -follow` filters ROUTES; moved before the join it
// would filter SEEDS (and the node field is rewritten by the traversal),
// so the optimiser must leave it where it is. A plain inner join, where
// the left-only predicate does move ahead of the join, shows the
// assertion discriminates (DFC161).
func TestFollowJoinPushdownGuard(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	seeds := filepath.Join(dir, "seeds.csv")
	flights := filepath.Join(dir, "flights.csv")
	os.WriteFile(seeds, []byte("city,label\nSYD,home\n"), 0o644)
	os.WriteFile(flights, []byte("src,dst,mins\nSYD,MEL,90\nMEL,ADL,75\n"), 0o644)

	optimise := func(joinFlags string) string {
		t.Helper()
		pipeline := "export SSQL_MODE=record && (" + bin + " from csv " + seeds + " | " + bin + " join " + flights + " -on city src " + joinFlags + " | " + bin + " where -if city ne ADL | " + bin + " to csv) | " + bin + " generate ssql"
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+pipeline)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("generate ssql [%s]: %v\n%s", joinFlags, err, errb.String())
		}
		return out.String()
	}
	whereBeforeJoin := func(s string) bool {
		w, j := strings.Index(s, " where "), strings.Index(s, " join ")
		return w >= 0 && j >= 0 && w < j
	}
	if s := optimise(""); !whereBeforeJoin(s) {
		t.Errorf("plain inner join: the left-only where should move before the join, got\n%s", s)
	}
	if s := optimise("-follow dst -depth hops"); whereBeforeJoin(s) {
		t.Errorf("join -follow: the where must stay after the traversal, got\n%s", s)
	}
}
