package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGroupByUnknownFieldHints: an unknown group-by field gets the hint
// for the mistake actually made. `-count order_id count` (SQL's
// COUNT(col) habit) has -count take order_id as its result name and
// leaves "count" a grouping field; the dashless guess ("flags need the
// dash") sent the FAQ's model the wrong way twice (DFC169 §11.3,
// 2026-11-21). `count count` really is a forgotten dash. Both the run and
// schema mode (what the FAQ and Ctrl-O check against) say it.
func TestGroupByUnknownFieldHints(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	data := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(data, []byte("order_id,customer_id\n1,a\n2,a\n3,b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ stage, want, notWant string }{
		{"group-by customer_id -count order_id count",
			`-count takes only the new column's name (it counts rows), so it took "order_id"`, "flags need the dash"},
		{"group-by customer_id count count", "flags need the dash", "takes only"},
	} {
		for _, env := range []string{"", "SSQL_MODE=schema "} {
			out, err := exec.Command("bash", "-c", "set -o pipefail; "+bin+" from csv "+data+" | "+env+bin+" "+c.stage).CombinedOutput()
			if err == nil {
				t.Errorf("%s%s: expected an error, got:\n%s", env, c.stage, out)
				continue
			}
			if !strings.Contains(string(out), c.want) || strings.Contains(string(out), c.notWant) {
				t.Errorf("%s%s: want %q and not %q, got:\n%s", env, c.stage, c.want, c.notWant, out)
			}
		}
	}
}
