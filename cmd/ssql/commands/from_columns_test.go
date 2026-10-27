package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

// The -columns helpers every CSV/TSV source path shares (2026-10-28).
func TestCheckSourceColumns(t *testing.T) {
	hdr := []string{"id", "name", "age"}
	if err := checkSourceColumns(hdr, []string{"age", "id"}, "from csv"); err != nil {
		t.Errorf("known columns: %v", err)
	}
	if err := checkSourceColumns(hdr, nil, "from csv"); err != nil {
		t.Errorf("no -columns: %v", err)
	}
	if err := checkSourceColumns(nil, []string{"zz"}, "from csv"); err != nil {
		t.Errorf("unknown header (a pipe): must not refuse, got %v", err)
	}
	err := checkSourceColumns(hdr, []string{"zz"}, "from tsv")
	if err == nil || !strings.Contains(err.Error(), `from tsv: -columns names "zz"`) || !strings.Contains(err.Error(), "id, name, age") {
		t.Errorf("unknown column: %v", err)
	}
	err = checkSourceColumns(hdr, []string{"id", "id"}, "from csv")
	if err == nil || !strings.Contains(err.Error(), `lists "id" twice`) {
		t.Errorf("repeated column: %v", err)
	}
	if got := strings.Join(sourceColumnsOrder(hdr, []string{"age", "id"}), ","); got != "age,id" {
		t.Errorf("order with -columns %q", got)
	}
	if got := strings.Join(sourceColumnsOrder(hdr, nil), ","); got != "id,name,age" {
		t.Errorf("order without -columns %q", got)
	}
	if code := projectSourceColumnsCode([]string{"b", "a"}); !strings.Contains(code, `ssql.Project(r, "b", "a")`) {
		t.Errorf("record projection code:\n%s", code)
	}
	if projectSourceColumnsCode(nil) != "" {
		t.Error("no -columns: no projection code")
	}
}

// A column named "-desc" cannot be a -columns value (no -arg escape for a
// flag's argument), so the rule leaves such a source alone.
func TestSourceColumnPruningSkipsHostileNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hostile.csv")
	if err := os.WriteFile(path, []byte("name,-desc,x\namy,9,1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmds := []*pipelineCmd{
		{Kind: "from", RawArgs: []string{"csv", path}},
		{Kind: "sort", RawArgs: []string{cf.ArgFlag, "-desc"}},
		{Kind: "include", RawArgs: []string{cf.ArgFlag, "name", cf.ArgFlag, "-desc"}},
	}
	if rules := ruleSourceColumnPruning(cmds); len(rules) != 0 || hasColumnsFlag(cmds[0]) {
		t.Errorf("hostile name pruned: %+v → %v", rules, cmds[0].RawArgs)
	}
	plain := []*pipelineCmd{
		{Kind: "from", RawArgs: []string{path}},
		{Kind: "include", RawArgs: []string{"name"}},
	}
	rules := ruleSourceColumnPruning(plain)
	if len(rules) != 1 || rules[0].Rule != "csv-column-pruning" || strings.Join(plain[0].RawArgs, " ") != "csv "+path+" -columns name" {
		t.Errorf("plain name: rules %+v, args %v", rules, plain[0].RawArgs)
	}
}

// delimitedPruneTarget: the from stages the CSV pruning rule may rewrite.
func TestDelimitedPruneTarget(t *testing.T) {
	cases := []struct {
		args         []string
		file, format string
		explicit, ok bool
	}{
		{[]string{"csv", "a.csv"}, "a.csv", "csv", true, true},
		{[]string{"tsv", "a.tsv", "-type", "x", "int"}, "a.tsv", "tsv", true, true},
		{[]string{"a.csv", "-tz", "UTC"}, "a.csv", "csv", false, true},
		{[]string{"a.tsv"}, "a.tsv", "tsv", false, true},
		{[]string{"csv", "a.csv", "-sample", "5"}, "a.csv", "csv", true, true},
		{[]string{"csv", "a.csv", "b.csv"}, "", "", false, false},          // several files
		{[]string{"csv", "a.csv", "--", "where", "-if", "x"}, "", "", false, false}, // pushdown
		{[]string{"csv", "a.csv", "-records"}, "", "", false, false},
		{[]string{"csv", "*.csv"}, "", "", false, false},
		{[]string{"a.json"}, "", "", false, false},
		{[]string{"parquet", "a.parquet"}, "", "", false, false},
	}
	for _, c := range cases {
		file, format, explicit, ok := delimitedPruneTarget(&pipelineCmd{Kind: "from", RawArgs: c.args})
		if file != c.file || format != c.format || explicit != c.explicit || ok != c.ok {
			t.Errorf("%v: got (%q, %q, %v, %v), want (%q, %q, %v, %v)", c.args, file, format, explicit, ok, c.file, c.format, c.explicit, c.ok)
		}
	}
}
