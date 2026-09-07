package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCatalogLocalShardsRunThisBinary: a catalog row with host=local runs
// the ssql the user has, not /usr/bin/ssql (which a go-install user does
// not have — every local shard used to fail with "bash: /usr/bin/ssql: No
// such file or directory"). Covered in exec (the running ssql) and in a
// generated program (which finds ssql on PATH). The fixture is the
// codelab's own: shards.csv over orders.csv split by month.
func TestCatalogLocalShardsRunThisBinary(t *testing.T) {
	if _, err := os.Stat("/usr/bin/ssql"); err == nil {
		t.Skip("/usr/bin/ssql exists on this machine — the fallback would mask the bug")
	}
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	for _, f := range []string{"shards.csv", "orders_2026-01.csv", "orders_2026-02.csv"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "doc", "codelab-data", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	// PATH: an `ssql` that is the test build (what a generated program
	// must find, as a real install has), go (to compile that program),
	// and the system dirs for bash.
	pathDir := t.TempDir()
	if err := os.Symlink(bin, filepath.Join(pathDir, "ssql")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir+":"+filepath.Dir(goBin)+":/usr/bin:/bin")

	// exec: the running ssql serves its own local shards.
	out, err := runIn(dir, bin+" from catalog shards.csv | "+bin+" count")
	if err != nil || strings.TrimSpace(out) != "10" {
		t.Errorf("exec local catalog: count = %q, err = %v (want 10 from 6 + 4 rows)", strings.TrimSpace(out), err)
	}
	out, err = runIn(dir, bin+" from catalog shards.csv -if month ge 2026-02 -- where -if status eq shipped | "+bin+" count")
	if err != nil || strings.TrimSpace(out) != "3" {
		t.Errorf("exec pruned+pushdown: count = %q, err = %v (want 3)", strings.TrimSpace(out), err)
	}

	// generated record program: its executable is the program, so the
	// shards run the ssql on PATH.
	out, err = runGeneratedPipeline(t, bin, dir, "record", bin+" from catalog shards.csv | "+bin+" count")
	if err != nil || strings.TrimSpace(out) != "10" {
		t.Errorf("generated local catalog: count = %q, err = %v\n%s", strings.TrimSpace(out), err, out)
	}
}

func runIn(dir, pipeline string) (string, error) {
	c := exec.Command("bash", "-c", pipeline)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}
