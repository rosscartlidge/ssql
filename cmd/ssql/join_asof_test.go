package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The optimiser's join predicate pushdown moves a right-only `where`
// into the join's <(…)> source. That is sound for an inner equi-join and
// WRONG for an ASOF join (the match is the nearest right row that
// exists; filtering the right side changes which one) and for a left
// join (unmatched rows the filter would drop survive). The guard is in
// ruleJoinPredicatePushdown; this pins it against a plain join where
// the rewrite still happens, so the guard is known to discriminate.
func TestAsofJoinPushdownGuard(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	trades := filepath.Join(dir, "trades.csv")
	quotes := filepath.Join(dir, "quotes.csv")
	os.WriteFile(trades, []byte("trade,sym,ts\nt1,A,49\nt2,A,30\n"), 0o644)
	os.WriteFile(quotes, []byte("sym,ts,q\nA,30,a30\nA,10,a10\n"), 0o644)
	// the plain join's right side has no ts (it would collide)
	names := filepath.Join(dir, "names.csv")
	os.WriteFile(names, []byte("sym,q\nA,a30\n"), 0o644)

	optimise := func(right, joinFlags string) string {
		t.Helper()
		pipeline := "export SSQL_MODE=record && (" + bin + " from csv " + trades + " | " + bin + " join <(" + bin + " from csv " + right + ") -using sym " + joinFlags + " | " + bin + " where -if q ne a10 | " + bin + " to csv) | " + bin + " generate ssql"
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+pipeline)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("generate ssql [%s]: %v\n%s", joinFlags, err, errb.String())
		}
		return out.String()
	}
	pushedInto := func(s string) bool {
		// the where is inside the <(…)> when it precedes the closing paren
		open := strings.Index(s, "<(")
		close := strings.Index(s, ")")
		return open >= 0 && close > open && strings.Contains(s[open:close], "where")
	}
	if s := optimise(names, ""); !pushedInto(s) {
		t.Errorf("plain inner join: the right-only where should be pushed into the source, got\n%s", s)
	}
	if s := optimise(quotes, "-asof ts"); pushedInto(s) {
		t.Errorf("join -asof: the right-only where must NOT be pushed into the source, got\n%s", s)
	}
	for _, flags := range []string{"-type left"} {
		if s := optimise(names, flags); pushedInto(s) {
			t.Errorf("join %s: the right-only where must NOT be pushed into the source, got\n%s", flags, s)
		}
	}
}
