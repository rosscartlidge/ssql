package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestValidatePipeline(t *testing.T) {
	ok := []string{
		"ssql from employees.csv | ssql group-by dept -count n | ssql to table",
		"ssql from orders.csv | ssql join <(ssql from customers.csv | ssql include customer_id tier) -using customer_id",
		`ssql from e.csv | ssql where -if-expr 'salary > 100 && (age < 30)' | ssql update -set-expr b "a > 1 ? \"x\" : \"y\""`,
		// A pipeline document handed to ssql run (the docs' own form).
		`echo '[["from","csv","-arg","employees.csv"],["to","table"]]' | ssql run -stdin`,
		`echo '[["from","employees.csv"],["count"]]' | ssql run -check -stdin`,
		`ssql run <(echo '[["from","employees.csv"],["sort","salary","-desc"],["to","table"]]')`,
	}
	for _, p := range ok {
		if _, err := validatePipeline(p); err != nil {
			t.Errorf("%s: refused: %v", p, err)
		}
	}
	bad := map[string]string{
		"ssql from e.csv; rm -rf x":                          ";",
		"ssql from e.csv | sh -c 'id'":                       "ssql command",
		"ssql from e.csv > out.txt":                          ">",
		"ssql from $(echo e.csv)":                            "$",
		"ssql from /etc/passwd":                              "sample files",
		"ssql from ../secret.csv":                            "sample files",
		"ssql from e.csv | ssql join <(cat x.csv) -using id": "ssql command",
		"ssql from e.csv & ssql from f.csv":                  "&",
		"ssql from `id`":                                     "`",
		`ssql from "$HOME/x.csv"`:                            "shell expansion",
		"ssql from \"`id`.csv\"":                             "shell expansion",
		// echo only hands a document to ssql run, and the document reads
		// only the samples.
		`echo hi`: "ssql command",
		`echo '[["from","e.csv"]]' | ssql from e.csv`:                   "ssql command",
		`ssql from e.csv | echo '[["from","e.csv"]]' | ssql run -stdin`: "ssql command",
		`echo '[["from","/etc/passwd"]]' | ssql run -stdin`:             "sample files",
		`ssql run <(echo '[["from","../x.csv"]]')`:                      "sample files",
		`echo 'not json' | ssql run -stdin`:                             "pipeline document",
		`echo -e '[["from","e.csv"]]' | ssql run -stdin`:                "pipeline document",
		`ssql join <(echo '[["from","e.csv"]]') -using id`:              "ssql command",
	}
	for p, want := range bad {
		_, err := validatePipeline(p)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want a refusal mentioning %q, got %v", p, want, err)
		}
	}
}

// A trailing save to a plain file is advice, not something to run: the
// pipeline before it is verified. Every other redirection stays refused.
func TestRunnable(t *testing.T) {
	ok := map[string]string{
		"ssql generate go -pipeline 'ssql from e.csv | ssql count' > report.go": "ssql generate go -pipeline 'ssql from e.csv | ssql count'",
		"ssql from e.csv | ssql to csv >> all.csv":                              "ssql from e.csv | ssql to csv",
		"ssql from e.csv | ssql count":                                          "ssql from e.csv | ssql count",
	}
	for in, want := range ok {
		if got := runnable(in); got != want {
			t.Errorf("runnable(%q) = %q, want %q", in, got, want)
		}
		if _, err := validatePipeline(runnable(in)); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	for _, bad := range []string{
		"ssql from e.csv > /etc/x",
		"ssql from e.csv > ../x.csv",
		"ssql from e.csv > a.csv > b.csv",
		"ssql from e.csv 2>&1",
		"ssql from e.csv > out.csv | ssql count",
		"ssql from e.csv > $HOME",
	} {
		if _, err := validatePipeline(runnable(bad)); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}

// The facts block's release is the CHANGELOG's first released section,
// past [Unreleased].
func TestLatestRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	os.WriteFile(path, []byte("# Changelog\n\n## [Unreleased]\n\n### Fixed\n\n## [4.148.0] - 2026-11-15\n\n- schema mode is a lane\n\n## [4.147.0] - 2026-11-14\n\n- older\n"), 0o644)
	v, d, notes, err := latestRelease(path)
	if err != nil || v != "4.148.0" || d != "2026-11-15" || notes != "- schema mode is a lane" {
		t.Errorf("got %q %q %q %v", v, d, notes, err)
	}
	os.WriteFile(path, []byte("## [Unreleased]\n"), 0o644)
	if _, _, _, err := latestRelease(path); err == nil {
		t.Errorf("a CHANGELOG with no release must fail loudly")
	}
}

func TestDataFor(t *testing.T) {
	cases := map[string]string{
		"ssql from e.csv | ssql to table":                      "ssql from e.csv | ssql to jsonl",
		"ssql from e.csv | ssql count":                         "ssql from e.csv | ssql count",
		"ssql from e.csv | ssql to csv out.csv":                "ssql from e.csv | ssql to csv out.csv | ssql to jsonl",
		"ssql from e.csv":                                      "ssql from e.csv | ssql to jsonl",
		"ssql from e.csv | ssql to table -max-width 20":        "ssql from e.csv | ssql to jsonl",
		"ssql from e.csv | ssql to table -time-format '%Y-%m'": "ssql from e.csv | ssql to csv -time-format '%Y-%m' | ssql from csv | ssql to jsonl",
	}
	for in, want := range cases {
		if got := dataFor(in); got != want {
			t.Errorf("dataFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// The grader compares values, not column names, and rows as a multiset
// unless the order is the answer.
func TestCanonRows(t *testing.T) {
	a := `{"_schema":{"fields":["dept","n"]}}
{"dept":"Eng","n":17}
{"dept":"Sales","n":10}`
	renamed := `{"dept":"Sales","count":10}
{"dept":"Eng","count":17.0}`
	if !slices.Equal(canonRows(a, false), canonRows(renamed, false)) {
		t.Errorf("a renamed column or another row order must compare equal")
	}
	if slices.Equal(canonRows(a, true), canonRows(renamed, true)) {
		t.Errorf("ordered: another row order must differ")
	}
	extra := `{"dept":"Eng","n":17,"x":1}
{"dept":"Sales","n":10,"x":1}`
	if slices.Equal(canonRows(a, false), canonRows(extra, false)) {
		t.Errorf("an extra column must differ")
	}
	wrong := `{"dept":"Eng","n":16}
{"dept":"Sales","n":10}`
	if slices.Equal(canonRows(a, false), canonRows(wrong, false)) {
		t.Errorf("a wrong value must differ")
	}
	if !slices.Equal(canonRows("21\n", false), canonRows("21", false)) {
		t.Errorf("a count compares as its text")
	}
}

func TestExtractPipeline(t *testing.T) {
	reply := "Here:\n```bash\nssql from e.csv \\\n  | ssql count\n```\nDone."
	if got := extractPipeline(reply); got != "ssql from e.csv | ssql count" {
		t.Errorf("got %q", got)
	}
}

// A block's commands: a line continues the one before when that ends in
// \ or |, or when it starts with |; any other line is a new command.
func TestBlockCommands(t *testing.T) {
	for _, c := range []struct {
		block string
		want  []string
	}{
		{"ssql from e.csv \\\n  | ssql count", []string{"ssql from e.csv | ssql count"}},
		{"ssql from e.csv |\n  ssql count", []string{"ssql from e.csv | ssql count"}},
		{"ssql from e.csv\n  | ssql count", []string{"ssql from e.csv | ssql count"}},
		{"# one\nssql from a.csv | ssql count\n\n# two\nssql from b.csv | ssql to table",
			[]string{"ssql from a.csv | ssql count", "ssql from b.csv | ssql to table"}},
	} {
		got := blockCommands("x\n```bash\n" + c.block + "\n```\n")
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: %q, want %q", c.block, got, c.want)
		}
	}
}

func TestGradeRefusal(t *testing.T) {
	c := evalCase{Kind: "refuse", Forbid: []string{"from pdf"}}
	denies := answer{Reply: "UNSUPPORTED: ssql has no `from pdf`; convert the PDF to CSV first.", Unsupported: true}
	if ok, why := gradeAnswer(nil, c, denies); !ok {
		t.Errorf("a refusal naming the missing command in prose failed: %s", why)
	}
	invents := answer{Reply: "UNSUPPORTED: maybe\n```bash\nssql from pdf report.pdf\n```", Unsupported: true}
	if ok, _ := gradeAnswer(nil, c, invents); ok {
		t.Errorf("a pipeline using the invented command passed")
	}
	if ok, _ := gradeAnswer(nil, c, answer{Reply: "ssql from csv x.csv"}); ok {
		t.Errorf("an answer without UNSUPPORTED passed")
	}
}

// A doc case with a status fails an answer the page would show otherwise,
// even when the reply holds every fact.
func TestGradeStatus(t *testing.T) {
	reply := "```bash\nssql generate go -run -pipeline 'ssql from e.csv'\n```"
	pipe := "ssql generate go -run -pipeline 'ssql from e.csv'"
	c := evalCase{Kind: "doc", Facts: []string{"generate go"}, Status: "verified"}
	if ok, why := gradeAnswer(nil, c, answer{Reply: reply, Pipeline: pipe, Ran: true, RanOn: "generated"}); !ok {
		t.Errorf("a generated program failed: %s", why)
	}
	ok, why := gradeAnswer(nil, c, answer{Reply: reply, Pipeline: pipe, Failure: "go: downloading go1.26.1"})
	if ok || !strings.Contains(why, "go: downloading") {
		t.Errorf("an unverified command passed (%v) or did not say why: %s", ok, why)
	}
	brew := "```bash\nbrew tap rosscartlidge/ssql && brew install ssql\n```"
	setup := evalCase{Kind: "doc", Facts: []string{"install"}, Status: "setup"}
	if ok, why := gradeAnswer(nil, setup, answer{Reply: brew, Pipeline: "brew tap rosscartlidge/ssql && brew install ssql", Setup: true}); !ok {
		t.Errorf("install lines shown failed: %s", why)
	}
	if ok, _ := gradeAnswer(nil, setup, answer{Reply: brew, Pipeline: "brew tap rosscartlidge/ssql && brew install ssql", Failure: `"&" is shell syntax`}); ok {
		t.Errorf("refused install lines passed")
	}
	if ok, _ := gradeAnswer(nil, evalCase{Kind: "doc", Facts: []string{"generate go"}}, answer{Reply: reply, Pipeline: pipe}); !ok {
		t.Errorf("a case without a status now checks it")
	}
}

func TestUnsupportedDetection(t *testing.T) {
	for _, r := range []string{"UNSUPPORTED: no", "**UNSUPPORTED:** no", "  # Unsupported\nno", "> UNSUPPORTED - no"} {
		if !unsupported.MatchString(r) {
			t.Errorf("%q not read as a refusal", r)
		}
	}
	for _, r := range []string{"ssql cannot be unsupported", "```bash\nssql from x\n```"} {
		if unsupported.MatchString(r) {
			t.Errorf("%q read as a refusal", r)
		}
	}
}

func TestRowsHold(t *testing.T) {
	row := func(vals ...string) string { return strings.Join(vals, "\x1f") }
	want := []string{row("4", "alice"), row("7", "bob")}
	for _, c := range []struct {
		got     []string
		ordered bool
		holds   bool
	}{
		{[]string{row("1", "4", "alice"), row("2", "7", "bob")}, false, true}, // an extra id column
		{[]string{row("2", "7", "bob"), row("1", "4", "alice")}, false, true}, // any order
		{[]string{row("2", "7", "bob"), row("1", "4", "alice")}, true, false}, // the order is the answer
		{[]string{row("4", "alice")}, false, false},                           // a row missing
		{[]string{row("4", "alice"), row("8", "bob")}, false, false},          // a wrong value
		{[]string{row("2024-11-01T00:00:00Z", "3")}, false, false},            // another format is not the value
	} {
		if got := rowsHold(c.got, want, c.ordered); got != c.holds {
			t.Errorf("%q ordered=%v: %v, want %v", c.got, c.ordered, got, c.holds)
		}
	}
}
