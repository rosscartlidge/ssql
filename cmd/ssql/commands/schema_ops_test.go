//go:build !slim

package commands

import (
	"slices"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

func TestServeSchemaWalk(t *testing.T) {
	srv := &serveState{schema: []string{"name", "dept", "salary"}}

	// from-loaded source → the loaded schema.
	got, ok := serveSchemaWalk(srv, [][]string{{"from-loaded"}})
	if !ok || !slices.Equal(got, srv.schema) {
		t.Errorf("from-loaded: got %v ok=%v, want %v true", got, ok, srv.schema)
	}

	// from-loaded | where … : where is identity (unregistered) → schema unchanged.
	got, ok = serveSchemaWalk(srv, [][]string{{"from-loaded"}, {"where", "-if", "salary", "gt", "1"}})
	if !ok || !slices.Equal(got, srv.schema) {
		t.Errorf("from-loaded|where: got %v ok=%v, want %v true", got, ok, srv.schema)
	}

	// Wrong state type → the source op reports undeterminable.
	if _, ok := serveSchemaWalk("not-a-serveState", [][]string{{"from-loaded"}}); ok {
		t.Errorf("expected ok=false when state is not *serveState")
	}

	// Empty upstream → nothing, but ok.
	if got, ok := serveSchemaWalk(srv, nil); !ok || got != nil {
		t.Errorf("empty upstream: got %v ok=%v, want nil true", got, ok)
	}
}

// TestMutatingSchemaOps exercises each mutating op's argv decode.
func TestMutatingSchemaOps(t *testing.T) {
	in := []string{"name", "dept", "salary"}
	cases := []struct {
		cmd  string
		args []string
		want []string
		ok   bool
	}{
		{"rename", []string{"-as", "name", "person"}, []string{"person", "dept", "salary"}, true},
		{"rename", []string{"-as", "name", "person", "-as", "dept", "team"}, []string{"person", "team", "salary"}, true},
		{"include", []string{"dept", "salary"}, []string{"dept", "salary"}, true},
		{"include", []string{"dept", "nope"}, []string{"dept"}, true}, // unknown dropped
		{"exclude", []string{"salary"}, []string{"name", "dept"}, true},
		{"update", []string{"-set", "bonus", "1000"}, []string{"name", "dept", "salary", "bonus"}, true},
		{"update", []string{"-set-expr", "tax", "salary*0.3"}, []string{"name", "dept", "salary", "tax"}, true},
		{"update", []string{"-set", "salary", "0"}, []string{"name", "dept", "salary"}, true}, // existing not duplicated
		{"group-by", []string{"dept"}, []string{"dept"}, true},
		{"group-by", []string{"dept", "-count", "n", "-sum", "salary", "total"}, []string{"dept", "n", "total"}, true},
		{"group-by", []string{"dept", "-rollup", "-count", "n"}, nil, false}, // undeterminable
		{"window", []string{"-partition", "dept", "-order", "salary", "-row-number", "rn"}, []string{"name", "dept", "salary", "rn"}, true},
		{"window", []string{"-lag", "salary", "1", "prev"}, []string{"name", "dept", "salary", "prev"}, true},
		{"pivot", []string{"dept", "salary"}, nil, false},
		{"where", []string{"-if", "salary", "gt", "1"}, in, true}, // identity default
	}
	for _, c := range cases {
		got, ok := lookupSchemaOp(c.cmd)(nil, in, c.args)
		if ok != c.ok || (ok && !slices.Equal(got, c.want)) {
			t.Errorf("%s %v: got %v ok=%v, want %v ok=%v", c.cmd, c.args, got, ok, c.want, c.ok)
		}
	}
}

// TestServeSchemaWalk_MutatingUpstream is the case identity got wrong:
// a rename in the upstream means group-by must see the NEW name.
func TestServeSchemaWalk_MutatingUpstream(t *testing.T) {
	srv := &serveState{schema: []string{"name", "dept", "salary"}}
	// from-loaded | rename name person | exclude salary
	got, ok := serveSchemaWalk(srv, [][]string{
		{"from-loaded"},
		{"rename", "-as", "name", "person"},
		{"exclude", "salary"},
	})
	if !ok {
		t.Fatal("walk failed")
	}
	if want := []string{"person", "dept"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// A pivot anywhere upstream poisons the walk.
	if _, ok := serveSchemaWalk(srv, [][]string{{"from-loaded"}, {"pivot", "dept", "salary"}}); ok {
		t.Errorf("expected ok=false with pivot upstream")
	}
}

// TestServeCLICompletesUpstreamFields is the slice-5 integration point:
// the real serve command tree, completing `from-loaded | group-by <TAB>`
// with the walk's fields seeded, offers the loaded schema on group-by's
// FIELDS positional — proving autocli's FieldsFromFlag chain surfaces
// upstream fields end-to-end.
func TestServeCLICompletesUpstreamFields(t *testing.T) {
	cli := buildServeCLI()
	srv := &serveState{schema: []string{"name", "dept", "salary"}}

	fields, ok := serveSchemaWalk(srv, [][]string{{"from-loaded"}})
	if !ok {
		t.Fatal("walk failed")
	}
	seed := cf.CompletionContext{UpstreamFields: fields, State: srv}

	// `group-by <TAB>` — FIELDS positional.
	got, err := cli.CompleteWithContext([]string{"group-by", ""}, 2, seed)
	if err != nil {
		t.Fatalf("CompleteWithContext: %v", err)
	}
	for _, want := range srv.schema {
		if !slices.Contains(got, want) {
			t.Errorf("group-by completion missing %q; got %v", want, got)
		}
	}

	// Prefix: `group-by de<TAB>` → only dept.
	got, err = cli.CompleteWithContext([]string{"group-by", "de"}, 2, seed)
	if err != nil {
		t.Fatalf("CompleteWithContext: %v", err)
	}
	if !slices.Contains(got, "dept") {
		t.Errorf("prefix 'de' should offer dept; got %v", got)
	}
	if slices.Contains(got, "name") {
		t.Errorf("prefix 'de' should not offer name; got %v", got)
	}

	// Full composition for a MUTATING upstream — what the shell does on
	// `from-loaded | rename name person | group-by <TAB>`: walk the
	// upstream through serveSchemaWalk, seed the result, complete the
	// current stage. group-by must offer the renamed `person`, not `name`.
	walked, ok := serveSchemaWalk(srv, [][]string{{"from-loaded"}, {"rename", "-as", "name", "person"}})
	if !ok {
		t.Fatal("walk failed")
	}
	got, err = cli.CompleteWithContext([]string{"group-by", ""}, 2,
		cf.CompletionContext{UpstreamFields: walked, State: srv})
	if err != nil {
		t.Fatalf("CompleteWithContext: %v", err)
	}
	if !slices.Contains(got, "person") || slices.Contains(got, "name") {
		t.Errorf("after rename, group-by should offer person not name; got %v", got)
	}
}
