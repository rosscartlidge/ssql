package commands

import (
	"testing"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func frag(kind string, argv ...string) *lib.CodeFragment {
	return &lib.CodeFragment{Type: "stmt", Command: "ssql " + kind, Op: &lib.Op{Kind: kind, Argv: argv}}
}

// The document comes from Op.Argv, never from the command string; a func
// fragment becomes a nested pipeline in the /dev/fd argument it fed.
func TestPipelineDocFromFragments(t *testing.T) {
	right := &lib.CodeFragment{Type: "func", FuncName: "rightSource1", Command: "ssql from b.csv | ssql where -if x gt 1",
		FuncBody: []*lib.CodeFragment{frag("from", "b.csv"), frag("where", "-if", "x", "gt", "1")}}
	doc, err := pipelineDocFromFragments([]*lib.CodeFragment{
		frag("from", "csv", "-arg", "-weird.csv"),
		right,
		frag("join", "/dev/fd/63", "-using", "id"),
		{Type: "stmt", Command: ""}, // a continuation fragment (group-by's second): not a stage
		frag("where", "-if-expr", "n > lo", "-param", "lo", "int", "5"),
		{Type: "final", Command: "ssql to csv", Op: &lib.Op{Kind: "to", Argv: []string{"csv"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc) != 4 {
		t.Fatalf("stages: %v", doc)
	}
	join := doc[1].([]any)
	nested, ok := join[1].([]any)
	if !ok || len(nested) != 2 || join[0] != "join" || join[2] != "-using" {
		t.Errorf("nested join: %v", join)
	}
	if doc[0].([]any)[3] != "-weird.csv" {
		t.Errorf("argv verbatim: %v", doc[0])
	}

	bad := map[string][]*lib.CodeFragment{
		"no op":               {{Type: "init", Command: "ssql from a.csv"}},
		"error fragment":      {{Type: "error", Code: "boom"}},
		"trailing func":       {frag("from", "a.csv"), right},
		"nothing":             {},
	}
	for name, frags := range bad {
		if _, err := pipelineDocFromFragments(frags); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A func fragment with no /dev/fd in the stage's argv is how the stage
	// read a file it names directly (join customers.csv); the argv wins.
	doc, err = pipelineDocFromFragments([]*lib.CodeFragment{frag("from", "a.csv"), right, frag("join", "customers.csv", "-using", "id")})
	if err != nil || doc[1].([]any)[1] != "customers.csv" {
		t.Errorf("direct file join: %v %v", doc, err)
	}
	// A /dev/fd path with no func fragment behind it is a literal path the
	// caller arranged (the runner's own nested mechanism produces exactly
	// that); it is kept verbatim, and run -check will judge it.
	doc, err = pipelineDocFromFragments([]*lib.CodeFragment{frag("from", "a.csv"), frag("join", "/dev/fd/63", "-using", "id")})
	if err != nil || doc[1].([]any)[1] != "/dev/fd/63" {
		t.Errorf("bare /dev/fd: %v %v", doc, err)
	}
}
