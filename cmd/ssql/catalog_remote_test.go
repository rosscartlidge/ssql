package main

// Rig-gated integration tests for catalog pruning over SSH — the LXD rig
// (doc/research/ssh-test-environment.md). Opt-in via
// SSQL_TEST_SSH_HOST=<node>; without it the tests skip gracefully.
// The remote node needs /usr/bin/ssql ≥ v4.56.1 (the shard side only runs
// `from` and pushed-down `where`; pruning itself is local).
//
// These lock the two 2026-08-11 findings end-to-end:
//   - the RANGE-extraction row leak: the optimizer used to delete the row
//     filter when lifting a range-column condition into pruning flags, so
//     straddling shards leaked non-matching rows;
//   - +if pruning flags: exact columns negate exactly, range columns prune
//     only when the whole range satisfies the positive condition.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func sshTestHost(t *testing.T) string {
	t.Helper()
	host := os.Getenv("SSQL_TEST_SSH_HOST")
	if host == "" {
		t.Skip("SSQL_TEST_SSH_HOST not set — skipping SSH catalog integration test")
	}
	out, err := exec.Command("ssh", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes",
		host, "/usr/bin/ssql", "version").CombinedOutput()
	if err != nil {
		t.Skipf("remote ssql not reachable on %s: %v\n%s", host, err, out)
	}
	return host
}

func catalogRemoteSetup(t *testing.T, host string) (catalogPath string) {
	t.Helper()
	dir := "/data/ssql-catalog-test"
	shards := map[string]string{
		"shard1.csv": "date,val\n2026-01-01,a\n2026-02-01,b\n",
		"shard2.csv": "date,val\n2026-02-15,c\n2026-03-15,d\n", // straddles 2026-03-01
		"shard3.csv": "date,val\n2026-04-01,e\n2026-05-01,f\n",
	}
	if out, err := exec.Command("ssh", host, "mkdir", "-p", dir).CombinedOutput(); err != nil {
		t.Fatalf("remote mkdir: %v\n%s", err, out)
	}
	for name, content := range shards {
		cmd := exec.Command("ssh", host, "sh", "-c", fmt.Sprintf("cat > %s/%s", dir, name))
		cmd.Stdin = strings.NewReader(content)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("push %s: %v\n%s", name, err, out)
		}
	}
	catalogPath = filepath.Join(t.TempDir(), "catalog.csv")
	catalog := "host,path,region,date_from,date_to\n" +
		host + "," + dir + "/shard1.csv,east,2026-01-01,2026-02-01\n" +
		host + "," + dir + "/shard2.csv,west,2026-02-15,2026-03-15\n" +
		host + "," + dir + "/shard3.csv,east,2026-04-01,2026-05-01\n"
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	return catalogPath
}

func catalogRun(t *testing.T, bin, pipeline string) []string {
	t.Helper()
	out, err := exec.Command("bash", "-c", pipeline).Output()
	if err != nil {
		t.Fatalf("pipeline failed: %v\n  %s", err, pipeline)
	}
	var rows []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "_schema") {
			continue
		}
		rows = append(rows, line)
	}
	sort.Strings(rows)
	return rows
}

func TestCatalogSSHPruning(t *testing.T) {
	if testing.Short() {
		t.Skip("SSH integration test")
	}
	host := sshTestHost(t)
	bin := buildSSQLForTypedTest(t)
	cat := catalogRemoteSetup(t, host)

	baseline := catalogRun(t, bin,
		bin+" from catalog "+cat+" 2>/dev/null | "+bin+" where -if date ge 2026-03-01 | "+bin+" to jsonl")

	t.Run("range extraction does not leak straddling-shard rows", func(t *testing.T) {
		// The exact form the optimizer now produces: prune + shard-side
		// filter. Before the 2026-08-11 fix it produced pruning ONLY, and
		// shard2's 2026-02-15 row leaked.
		optimized := catalogRun(t, bin,
			bin+" from catalog "+cat+" -if date ge 2026-03-01 -- where -if date ge 2026-03-01 2>/dev/null | "+bin+" to jsonl")
		if strings.Join(optimized, "|") != strings.Join(baseline, "|") {
			t.Errorf("optimized form diverges from baseline:\n  baseline:  %v\n  optimized: %v", baseline, optimized)
		}
	})

	t.Run("negated range pruning is the conservative dual", func(t *testing.T) {
		// +if date ge X: shard3 (entirely >= X) skipped; shard1 kept;
		// straddling shard2 kept conservatively — pruning flags are
		// shard-level, so BOTH shard2 rows appear (pair with a where/
		// pushdown for row exactness).
		got := catalogRun(t, bin,
			bin+" from catalog "+cat+" +if date ge 2026-03-01 2>/dev/null | "+bin+" to jsonl")
		for _, mustHave := range []string{`"2026-01-01"`, `"2026-02-15"`, `"2026-03-15"`} {
			if !strings.Contains(strings.Join(got, " "), mustHave) {
				t.Errorf("expected kept-shard row %s in %v", mustHave, got)
			}
		}
		if strings.Contains(strings.Join(got, " "), `"2026-04-01"`) {
			t.Errorf("shard3 (entirely >= cutoff) must be pruned under +if: %v", got)
		}
	})

	t.Run("negated exact pruning inverts", func(t *testing.T) {
		got := catalogRun(t, bin,
			bin+" from catalog "+cat+" +if region eq east 2>/dev/null | "+bin+" to jsonl")
		joined := strings.Join(got, " ")
		if !strings.Contains(joined, `"west"`) || strings.Contains(joined, `"east"`) {
			t.Errorf("+if region eq east must keep only west shards: %v", got)
		}
	})
}
