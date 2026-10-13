package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// SSQL_MODE=schema for a catalog (DFC149 §4.5): the first pruned shard's
// schema, asked of the shard command — with the pushdown stages, so a
// group-by on the shard side shows its output — plus -shard-field and the
// catalog's exact metadata columns as strings, in header order; range
// columns prune only and are not fields. It answered nothing before
// (exit 0, no header), so completion and the typed lane were blind to a
// catalog. The fields must be exactly what the records then carry.
func TestSchemaModeCatalog(t *testing.T) {
	if _, err := os.Stat("/usr/bin/ssql"); err == nil {
		t.Skip("/usr/bin/ssql exists on this machine — the shard would run it, not the test build")
	}
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	for _, f := range []string{"orders_2026-01.csv", "orders_2026-02.csv"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "doc", "codelab-data", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// An exact column (month), a range pair (date_from/date_to) and a
	// static column (region) — the last after the range, to show order.
	catalog := "host,path,format,month,date_from,date_to,region\n" +
		"local,orders_2026-01.csv,csv,2026-01,2026-01-01,2026-01-31,eu\n" +
		"local,orders_2026-02.csv,csv,2026-02,2026-02-01,2026-02-28,eu\n"
	if err := os.WriteFile(filepath.Join(dir, "shards.csv"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	pathDir := t.TempDir()
	if err := os.Symlink(bin, filepath.Join(pathDir, "ssql")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir+":/usr/bin:/bin")

	schema := func(pipeline string) string {
		c := exec.Command("bash", "-c", "export SSQL_MODE=schema; "+pipeline+" | "+bin+" generate schema")
		c.Dir = dir
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", pipeline, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// generate schema prints one "name type" line per field, in order.
	fields := func(s string) string {
		var names []string
		for _, line := range strings.Split(s, "\n") {
			if f := strings.Fields(line); len(f) > 0 {
				names = append(names, f[0])
			}
		}
		return strings.Join(names, " ")
	}

	got := schema(bin + " from catalog shards.csv")
	if want := "order_id customer_id product amount status date month region"; fields(got) != want {
		t.Errorf("catalog fields:\n got %q\nwant %q\n%s", fields(got), want, got)
	}
	if strings.Contains(got, "date_from") || strings.Contains(got, "date_to") {
		t.Errorf("range columns are not fields:\n%s", got)
	}
	// The header carries the shard's own types and string for the added
	// columns (generate schema prints names only).
	c := exec.Command("bash", "-c", "export SSQL_MODE=schema; "+bin+" from catalog shards.csv | head -1")
	c.Dir = dir
	header, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("header: %v\n%s", err, header)
	}
	for _, typed := range []string{`"amount":"int"`, `"month":"string"`, `"region":"string"`} {
		if !strings.Contains(string(header), typed) {
			t.Errorf("catalog header lacks %s:\n%s", typed, header)
		}
	}

	got = schema(bin + " from catalog shards.csv -shard-field origin -if month eq 2026-02")
	if want := "order_id customer_id product amount status date origin month region"; fields(got) != want {
		t.Errorf("-shard-field and pruning:\n got %q\nwant %q", fields(got), want)
	}

	// Pushdown stages change the shard's schema; schema mode runs them.
	got = schema(bin + " from catalog shards.csv -- where -if status eq shipped + group-by status -sum amount total")
	if want := "status total month region"; fields(got) != want {
		t.Errorf("pushdown group-by:\n got %q\nwant %q\n%s", fields(got), want, got)
	}

	// The schema is what the records carry: the same names, in exec.
	c = exec.Command("bash", "-c", bin+" from catalog shards.csv -shard-field origin | "+bin+" limit 1 | "+bin+" to csv")
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("exec: %v\n%s", err, out)
	}
	csvHeader := strings.Split(strings.TrimSpace(string(out)), "\n")[0]
	for _, name := range []string{"order_id", "origin", "month", "region"} {
		if !strings.Contains(csvHeader, name) {
			t.Errorf("exec records lack %q that schema mode promised: %s", name, csvHeader)
		}
	}

	// Nothing left after pruning: the added fields alone, exit 0.
	got = schema(bin + " from catalog shards.csv -if month eq 1999-01")
	if fields(got) != "month region" {
		t.Errorf("pruned to nothing: got %q, want the catalog columns alone", fields(got))
	}

	// A shard that cannot be read is loud.
	c = exec.Command("bash", "-c", "export SSQL_MODE=schema; "+bin+" from catalog shards.csv -- where -if nope eq 1")
	c.Dir = dir
	if out, err := c.CombinedOutput(); err == nil || !strings.Contains(string(out), "nope") {
		t.Errorf("a failing shard must fail schema mode: %v\n%s", err, out)
	}
}

// from ssh answers schema mode the same way, through the remote ssql —
// rig-gated (SSQL_TEST_SSH_HOST), as the other SSH integration tests.
func TestSchemaModeFromSSH(t *testing.T) {
	host := sshTestHost(t)
	bin := buildSSQLForTypedTest(t)
	remote := "/tmp/ssql_schema_mode_test.csv"
	if out, err := exec.Command("ssh", host, "printf 'id,name,n\\n1,a,2\\n' > "+remote).CombinedOutput(); err != nil {
		t.Fatalf("write remote fixture: %v\n%s", err, out)
	}
	defer exec.Command("ssh", host, "rm", "-f", remote).Run()
	c := exec.Command("bash", "-c", "export SSQL_MODE=schema; "+bin+" from ssh "+host+" "+remote+" | head -1")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.HasPrefix(string(out), `{"_schema":{"fields":["id","name","n"]`) {
		t.Errorf("from ssh schema: fields in file order, got:\n%s", out)
	}
	for _, want := range []string{`"id":"int"`, `"name":"string"`, `"n":"int"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("from ssh schema lacks %s:\n%s", want, out)
		}
	}
}
