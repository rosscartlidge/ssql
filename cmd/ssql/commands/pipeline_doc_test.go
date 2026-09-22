package commands

import (
	"strings"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

// A small root with the shapes the document layer must handle: a nested
// subcommand path, a positional, a two-argument flag, a required flag.
func docTestRoot() *cf.Command {
	return cf.NewCommand("t").
		Subcommand("from").
			Subcommand("csv").
				Flag("FILE").String().Global().Done().
				Handler(func(*cf.Context) error { return nil }).
				Done().
			Done().
		Subcommand("join").
			Flag("FILE").String().Global().Required().Done().
			Flag("-on").Arg("left").Done().Arg("right").Done().Local().Done().
			Handler(func(*cf.Context) error { return nil }).
			Done().
		Subcommand("to").
			Subcommand("csv").
				Handler(func(*cf.Context) error { return nil }).
				Done().
			Done().
		Subcommand("run").
			Flag("FILE").String().Global().Done().
			Handler(func(*cf.Context) error { return nil }).
			Done().
		Build()
}

func TestParsePipelineDoc(t *testing.T) {
	stages, err := parsePipelineDoc([]byte(`[
		["from", "csv", "-arg", "-weird.csv"],
		["join", ["from", "csv", "-arg", "b.csv"], "-on", "id", "id"],
		["join", [["from", "csv", "-arg", "c.csv"], ["to", "csv"]], "-on", "id", "id"],
		["to", "csv"]
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 4 || stages[0][3].Text != "-weird.csv" {
		t.Fatalf("stages: %+v", stages)
	}
	if n := stages[1][1].Nested; len(n) != 1 || n[0][3].Text != "b.csv" {
		t.Errorf("one-stage shorthand: %+v", n)
	}
	if n := stages[2][1].Nested; len(n) != 2 || n[1][0].Text != "to" {
		t.Errorf("two-stage nested: %+v", n)
	}
	if got := renderPipelineDoc(stages); got != `ssql from csv -arg -weird.csv | ssql join <(ssql from csv -arg b.csv) -on id id | ssql join <(ssql from csv -arg c.csv | ssql to csv) -on id id | ssql to csv` {
		t.Errorf("render: %s", got)
	}

	// The object form, for documents that will carry policy beside stages.
	if s, err := parsePipelineDoc([]byte(`{"pipeline": [["to","csv"]]}`)); err != nil || len(s) != 1 {
		t.Errorf("object form: %v %v", s, err)
	}

	bad := map[string]string{
		"not a list":            `{"stages": []}`,
		"empty":                 `[]`,
		"empty stage":           `[[]]`,
		"number element":        `[["limit", 5]]`,
		"null element":          `[["limit", null]]`,
		"nested as command":     `[[["from","csv"], "x"]]`,
		"trailing content":      `[["to","csv"]] [["to","csv"]]`,
		"malformed":             `[["to","csv"`,
	}
	for name, doc := range bad {
		if _, err := parsePipelineDoc([]byte(doc)); err == nil {
			t.Errorf("%s: accepted %s", name, doc)
		}
	}
	// A top-level list of strings is the one-stage shorthand; a stage whose
	// "command" is a whole command line is then an unknown command.
	if s, err := parsePipelineDoc([]byte(`["from csv a.csv"]`)); err != nil || len(s) != 1 || len(s[0]) != 1 {
		t.Errorf("one-stage shorthand at top level: %v %v", s, err)
	}
	// A number is the likely mistake; the message must say what to do.
	_, err = parsePipelineDoc([]byte(`[["limit", 5]]`))
	if err == nil || !strings.Contains(err.Error(), "write it as a string") {
		t.Errorf("number hint: %v", err)
	}
}

// Every stage is checked against the real grammar, nested ones included,
// and the failing stage is named; the command path may not be data.
func TestCheckPipelineDoc(t *testing.T) {
	root := docTestRoot()
	ok := `[["from","csv","-arg","-x.csv"],["join",["from","csv","-arg","b.csv"],"-on","id","id"],["to","csv"]]`
	stages, _ := parsePipelineDoc([]byte(ok))
	if err := checkPipelineDoc(root, stages, ""); err != nil {
		t.Errorf("valid document refused: %v", err)
	}
	bad := map[string][2]string{
		"unknown command":       {`[["frob","x"]]`, "stage 1 (frob)"},
		"unknown flag":          {`[["from","csv","a.csv"],["to","csv","-bogus"]]`, "stage 2 (to csv)"},
		"flag arity":            {`[["join","b.csv","-on","id"]]`, "-on"},
		"required flag missing": {`[["join","-on","id","id"]]`, "stage 1 (join)"},
		"nested stage invalid":  {`[["join",["from","xml","-arg","b"],"-on","id","id"]]`, "argument 2"},
		"flag as command":       {`[["-shell-init"]]`, "must be a command name"},
		"separator as command":  {`[["+"]]`, "must be a command name"},
		"run inside run":        {`[["run","other.json"]]`, "does not run documents"},
		"intermediate node":     {`[["from"]]`, "stage 1 (from)"},
		"command line as name":  {`["from csv a.csv"]`, "unknown command"},
	}
	for name, c := range bad {
		stages, err := parsePipelineDoc([]byte(c[0]))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		err = checkPipelineDoc(root, stages, "")
		if err == nil || !strings.Contains(err.Error(), c[1]) {
			t.Errorf("%s: got %v, want containing %q", name, err, c[1])
		}
	}
}
