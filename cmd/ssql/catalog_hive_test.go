package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `from catalog -hive` (DFC149 §4.1/§4.2): a hive layout is a derived
// catalog. The glob's files are the shards, each key=value directory a
// string column on the shard's records and an exact column for pruning;
// a where on it is lifted into -if by the optimiser, so only the
// matching files are opened. Every disagreement between the layout and
// the data is an error naming the file. The equivalence corpus holds the
// results (catalog_hive_glob, catalog_hive_where); this is the rest.
func TestCatalogHive(t *testing.T) {
	if _, err := os.Stat("/usr/bin/ssql"); err == nil {
		t.Skip("/usr/bin/ssql exists on this machine — the shards would run it, not the test build")
	}
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	files := map[string]string{
		"pqhive/region=north/data.parquet":    corpusParquet(false, pqRow{6, "hal", "", 9}, pqRow{3, "gus", "", 2.5}),
		"pqhive/region=south/data.parquet":    corpusParquet(false, pqRow{8, "ivy", "", 4}, pqRow{1, "jon", "", 11.5}),
		"pqclash/region=west/data.parquet":    corpusParquet(true, pqRow{2, "kim", "west", 6}),
		"pq/a.parquet":                        corpusParquet(false, pqRow{7, "cal", "", 12.5}),
		"two/year=2023/region=east/d.parquet": corpusParquet(false, pqRow{4, "fay", "", 15}),
		// A catalog whose own column clashes with the layout's key.
		"clash.csv": "host,path,region\nlocal,pqhive/region=north/data.parquet,n\n",
		// Rows of different layouts.
		"mixed.csv":   "host,path\nlocal,pqhive/region=north/data.parquet\nlocal,pq/a.parquet\n",
		"twokeys.csv": "host,path\nlocal,pqhive/region=north/data.parquet\nlocal,two/year=2023/region=east/d.parquet\n",
		// A catalog with a row column AND hive keys: both become columns.
		"both.csv": "host,path,source\nlocal,pqhive/region=*/data.parquet,nl\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	pathDir := t.TempDir()
	if err := os.Symlink(bin, filepath.Join(pathDir, "ssql")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir+":"+filepath.Dir(goBin)+":/usr/bin:/bin")
	run := func(script string) (string, error) {
		c := exec.Command("bash", "-c", script)
		c.Dir = dir
		out, err := c.CombinedOutput()
		return string(out), err
	}
	glob := `'pqhive/region=*/data.parquet'`

	// Pruning: -if on the derived column opens one file.
	out, err := run(bin + " from catalog -hive " + glob + " -if region eq south -catalog-used used.csv | " + bin + " count")
	if err != nil || strings.TrimSpace(strings.Split(out, "\n")[1]) != "2" {
		t.Errorf("pruned count: %v\n%s", err, out)
	}
	used, _ := os.ReadFile(filepath.Join(dir, "used.csv"))
	if !strings.Contains(string(used), "region=south") || strings.Contains(string(used), "region=north") || !strings.Contains(string(used), ",south") {
		t.Errorf("-catalog-used must hold the one file with its derived column:\n%s", used)
	}

	// The optimiser lifts a where on the derived column into -if.
	out, err = run("export SSQL_MODE=record; " + bin + " from catalog -hive " + glob + " | " + bin + " where -if region eq north | " + bin + " count | " + bin + " generate ssql")
	if err != nil || !strings.Contains(out, "-if region eq north") || !strings.Contains(out, "-hive") || strings.Contains(out, "where") {
		t.Errorf("generate ssql must prune by the derived column:\n%s", out)
	}

	// run applies the same rule to a document (a named view): the where
	// becomes -if on the from stage and one file is opened.
	if err := os.WriteFile(filepath.Join(dir, "south.json"), []byte(`[["from","catalog","-hive","pqhive/region=*/data.parquet"],["where","-if","region","eq","south"],["count"]]`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run(bin + " run -explain south.json 2>&1")
	if err != nil || !strings.Contains(out, "[catalog-predicate-extraction]") || !strings.Contains(out, "run: ssql from catalog 'pqhive/region=*/data.parquet' -if region eq south -hive | ssql count") || !strings.HasSuffix(strings.TrimSpace(out), "\n2") {
		t.Errorf("run must prune through the derived column: %v\n%s", err, out)
	}

	// generate sql applies it too (default -optimise, 2026-10-15): the SQL
	// names the one surviving file and the header records the rewrite;
	// +O translates the pipeline as typed, every file and a WHERE.
	sqlOf := func(flags string) string {
		out, err := run("export SSQL_MODE=record; " + bin + " from catalog -hive " + glob + " | " + bin + " where -if region eq north | " + bin + " count | " + bin + " generate sql " + flags + " 2>&1")
		if err != nil {
			t.Errorf("generate sql %s: %v\n%s", flags, err, out)
		}
		return out
	}
	out = sqlOf("-explain")
	if !strings.Contains(out, "[catalog-predicate-extraction]") || !strings.Contains(out, "-- Optimised by generate sql") || !strings.Contains(out, "region=north/data.parquet") || strings.Contains(out, "region=south") || strings.Contains(out, "WHERE") {
		t.Errorf("generate sql must prune by the derived column:\n%s", out)
	}
	out = sqlOf("+O")
	if !strings.Contains(out, "region=north/data.parquet") || !strings.Contains(out, "region=south/data.parquet") || !strings.Contains(out, "WHERE") || strings.Contains(out, "Optimised") {
		t.Errorf("generate sql +O must translate the pipeline as typed:\n%s", out)
	}

	// Schema mode: the derived column, as a string, after the file's.
	out, err = run("export SSQL_MODE=schema; " + bin + " from catalog -hive " + glob + " | head -1")
	if err != nil || !strings.Contains(out, `"region":"string"`) || !strings.Contains(out, `"fields":["id","name","amount","region"]`) {
		t.Errorf("schema mode: %v\n%s", err, out)
	}

	// A catalog CSV under -hive: its own columns and the layout's.
	out, err = run(bin + " from catalog -hive both.csv | " + bin + " sort id | " + bin + " include id source region | " + bin + " to csv")
	if err != nil || out != "id,source,region\n1,nl,south\n3,nl,north\n6,nl,north\n8,nl,south\n" {
		t.Errorf("catalog CSV with -hive: %v\n%q", err, out)
	}

	// Errors, each naming what disagrees.
	for _, tc := range []struct{ args, want string }{
		{"-hive pq/a.parquet", "glob"},                     // a plain path is read as a catalog CSV
		{"-hive 'pq/*.parquet'", "no key=value directory"}, // nothing to derive
		{"-hive mixed.csv", "no key=value directory"},      // one row's file has no keys at all
		{"-hive twokeys.csv", "must share one layout"},     // [region] vs [year region]
		{"-hive clash.csv", `partition key "region" is also a catalog column`},
		{"-hive 'pqclash/region=*/data.parquet'", `partition key "region" is also a column of the file`},
	} {
		out, err := run(bin + " from catalog " + tc.args + " | " + bin + " count")
		if err == nil && !strings.Contains(out, "Error") {
			t.Errorf("%s: expected an error, got\n%s", tc.args, out)
			continue
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: want %q in\n%s", tc.args, tc.want, out)
		}
	}
	// The file-column clash is caught in a generated program too.
	out, err = runGeneratedPipeline(t, bin, dir, "record", bin+" from catalog -hive 'pqclash/region=*/data.parquet' | "+bin+" count")
	if err == nil || !strings.Contains(out, `partition key "region" is also a column of the file`) {
		t.Errorf("record program on a clash: %v\n%s", err, out)
	}
}
