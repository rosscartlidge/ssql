package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `ssql run -check` validates a document's FIELD references as well as
// its grammar (DFC138 §4): the source answers its header under schema
// mode, every later stage runs on that header alone, and a stage that
// reads a field no earlier stage produced fails with the stage number
// and the fields available there. The commands are the authority: no
// table says which arguments are reads, `update -set new 1` is legal
// because update accepts it, and a field created upstream is in the
// header a later stage sees.
func TestRunCheckFields(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "orders.csv"), []byte("order_id,customer_id,amount,status\n1,1,10,shipped\n2,2,5,pending\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "customers.csv"), []byte("customer_id,name,tier\n1,acme,gold\n2,globex,silver\n"), 0o644)

	cases := []struct {
		name string
		doc  string
		want string // "" = accepted; else a substring of the error
	}{
		{"typo in where", `[["from","csv","orders.csv"],["where","-if","statuss","eq","shipped"],["to","table"]]`,
			`stage 2 (where): where references unknown field(s): statuss (available: amount, customer_id, order_id, status)`},
		{"field created upstream is fine", `[["from","csv","orders.csv"],["update","-set","flag","1"],["where","-if","flag","eq","1"],["group-by","status","-count","n"],["sort","-desc","n"],["to","table"]]`, ""},
		{"field consumed by group-by is gone", `[["from","csv","orders.csv"],["group-by","status","-count","n"],["where","-if","amount","gt","1"],["to","table"]]`,
			`stage 3 (where): where references unknown field(s): amount (available: n, status)`},
		{"joined field is visible", `[["from","csv","orders.csv"],["join","customers.csv","-using","customer_id"],["where","-if","tier","eq","gold"],["include","order_id","name"],["to","csv"]]`, ""},
		{"typo inside a nested pipeline", `[["from","csv","orders.csv"],["join",[["from","csv","customers.csv"],["where","-if","tierr","eq","gold"]],"-using","customer_id"],["to","table"]]`,
			`stage 2 (join), argument 2: stage 2 (where): where references unknown field(s): tierr (available: customer_id, name, tier)`},
		{"typo in update's condition", `[["from","csv","orders.csv"],["update","-if","amountt","gt","1","-set","big","1"],["to","table"]]`,
			`stage 2 (update): update references unknown field(s): amountt`},
		{"typo in sort", `[["from","csv","orders.csv"],["sort","amountt"],["to","table"]]`, `stage 2 (sort): sort references unknown field(s): amountt`},
		{"typo in a source flag is the source's error", `[["from","csv","orders.csv","-type","amountt","float"],["to","table"]]`, `stage 1 (from csv)`},
		{"grammar error still first", `[["from","csv","orders.csv"],["where","-iff","x","eq","1"],["to","table"]]`, `-iff`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := filepath.Join(dir, "doc.json")
			os.WriteFile(doc, []byte(c.doc), 0o644)
			cmd := exec.Command(bin, "run", "-check", doc)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "SSQL_MODE=record") // an inherited mode must not turn the check into codegen
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			err := cmd.Run()
			if c.want == "" {
				if err != nil {
					t.Fatalf("expected the document to pass -check, got %v\n%s", err, errb.String())
				}
				return
			}
			if err == nil {
				t.Fatalf("expected -check to refuse the document")
			}
			if !strings.Contains(errb.String(), c.want) {
				t.Fatalf("error should contain %q, got:\n%s", c.want, errb.String())
			}
		})
	}
	// -check runs nothing: no output file appears for a sink that writes one
	doc := filepath.Join(dir, "sink.json")
	os.WriteFile(doc, []byte(`[["from","csv","orders.csv"],["to","csv","out.csv"]]`), 0o644)
	cmd := exec.Command(bin, "run", "-check", doc)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.csv")); err == nil {
		t.Fatalf("-check wrote the sink's file")
	}
}
