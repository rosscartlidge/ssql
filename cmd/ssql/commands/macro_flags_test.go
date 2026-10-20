package commands

import (
	"reflect"
	"strings"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// macroEntry is the parser's shape for one -set-macro / -if-macro
// occurrence (autocli's map per occurrence, the repeated arg a list).
func macroEntry(field, name string, args ...string) map[string]any {
	vals := make([]any, len(args))
	for i, a := range args {
		vals[i] = a
	}
	e := map[string]any{"macro": name, "args": vals}
	if field != "" {
		e["field"] = field
	}
	return e
}

func TestExpandClauseMacros(t *testing.T) {
	writeMacros(t, testMacros)
	clause := cf.Clause{Flags: map[string]any{
		"-set-macro": []any{macroEntry("s3", "add3", "a", "b", "c")},
		"-if-macro":  []any{func() map[string]any { e := macroEntry("", "cheap", "price", "cost"); e["_negated"] = true; return e }()},
		"-set":       []any{map[string]any{"field": "tag", "value": "x"}},
	}}
	if err := expandClauseMacros(&clause); err != nil {
		t.Fatal(err)
	}
	if _, ok := clause.Flags["-set-macro"]; ok {
		t.Error("-set-macro left in the clause")
	}
	if _, ok := clause.Flags["-if-macro"]; ok {
		t.Error("-if-macro left in the clause")
	}
	wantSet := []any{map[string]any{"field": "s3", "expression": "x + y + z"}}
	if got := clause.Flags["-set-expr"]; !reflect.DeepEqual(got, wantSet) {
		t.Errorf("-set-expr = %#v", got)
	}
	wantIf := []any{map[string]any{"expression": "price < cost * 1.1", "_negated": true}}
	if got := clause.Flags["-if-expr"]; !reflect.DeepEqual(got, wantIf) {
		t.Errorf("-if-expr = %#v", got)
	}
	wantParams := []any{
		map[string]any{"name": "x", "type": "int", "field": "a"},
		map[string]any{"name": "y", "type": "int", "field": "b"},
		map[string]any{"name": "z", "type": "int", "field": "c"},
		map[string]any{"name": "price", "type": "float", "field": "price"},
		map[string]any{"name": "cost", "type": "float", "field": "cost"},
	}
	if got := clause.Flags["-param-field"]; !reflect.DeepEqual(got, wantParams) {
		t.Errorf("-param-field = %#v", got)
	}
	// The lowered clause parses as DFC135 bindings and the readers agree.
	conds := parseExprConds(clause.Flags["-if-expr"])
	all, err := clauseSets(clause)
	if err != nil {
		t.Fatal(err)
	}
	sets := setExprsOf(all)
	params, err := clauseExprParams(clause, conds, sets)
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 5 || params[0].Field != "a" || params[4].Name != "cost" || !conds[0].Negated {
		t.Errorf("lowered clause: params %+v conds %+v", params, conds)
	}

	// A clause with no macro call is untouched.
	plain := cf.Clause{Flags: map[string]any{"-set": []any{"x"}}}
	if err := expandClauseMacros(&plain); err != nil || len(plain.Flags) != 1 {
		t.Errorf("plain clause: %v %v", plain.Flags, err)
	}
}

// A -set-macro written between two -set-expr lowers to the second of
// three -set-expr entries, and the clause's Order says so, so the sets
// still apply in the order written.
func TestExpandClauseMacrosKeepsOrder(t *testing.T) {
	writeMacros(t, testMacros)
	clause := cf.Clause{
		Flags: map[string]any{
			"-set-expr":  []any{map[string]any{"field": "e1", "expression": "1"}, map[string]any{"field": "e2", "expression": "2"}},
			"-set-macro": []any{macroEntry("s3", "add3", "a", "b", "c")},
			"-set":       []any{map[string]any{"field": "tag", "value": "x"}},
			"-if-macro":  []any{macroEntry("", "cheap", "price", "cost")},
		},
		Order: []string{"-set-expr", "-set-macro", "-if-macro", "-set", "-set-expr"},
	}
	if err := expandClauseMacros(&clause); err != nil {
		t.Fatal(err)
	}
	want := []string{"-set-expr", "-set-expr", "-if-expr", "-set", "-set-expr", "-param-field", "-param-field", "-param-field", "-param-field", "-param-field"}
	if !reflect.DeepEqual(clause.Order, want) {
		t.Errorf("Order = %q\n  want %q", clause.Order, want)
	}
	sets, err := clauseSets(clause)
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, s := range sets {
		fields = append(fields, s.field)
	}
	if !reflect.DeepEqual(fields, []string{"e1", "s3", "tag", "e2"}) {
		t.Errorf("sets in order = %v", fields)
	}
	if sets[1].expression != "x + y + z" {
		t.Errorf("lowered entry = %+v", sets[1])
	}
}

func TestExpandClauseMacrosErrors(t *testing.T) {
	writeMacros(t, testMacros)
	cases := []struct {
		name  string
		flags map[string]any
		want  string
	}{
		{"unknown", map[string]any{"-set-macro": []any{macroEntry("s", "nope", "a")}}, "unknown macro nope; defined: add3, cheap, margin, pi"},
		{"count", map[string]any{"-set-macro": []any{macroEntry("s", "add3", "a", "b")}}, "-set-macro add3: takes 3 field(s) add3(x int, y int, z int), got 2"},
		{"empty field", map[string]any{"-if-macro": []any{macroEntry("", "cheap", "price", "")}}, "a field name is required for each parameter"},
		{"two macros share a parameter", map[string]any{
			"-set-macro": []any{macroEntry("m", "margin", "p", "c")},
			"-if-macro":  []any{macroEntry("", "cheap", "p", "c")},
		}, "-if-macro cheap: parameter price is already declared in this clause by macro margin"},
		{"macro parameter vs the clause's -param", map[string]any{
			"-set-macro": []any{macroEntry("s", "add3", "a", "b", "c")},
			"-param":     []any{map[string]any{"name": "x", "type": "int", "value": "1"}},
		}, "-set-macro add3: parameter x is already declared in this clause by -param x"},
	}
	for _, c := range cases {
		clause := cf.Clause{Flags: c.flags}
		err := expandClauseMacros(&clause)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want containing %q", c.name, err, c.want)
		}
	}
}

func TestLowerMacroArgv(t *testing.T) {
	writeMacros(t, testMacros)
	cases := []struct {
		in, want []string
	}{
		{
			[]string{"-set-macro", "s3", "add3", "a", "b", "c", "-set", "t", "v"},
			[]string{"-set-expr", "s3", "x + y + z", "-param-field", "x", "int", "a", "-param-field", "y", "int", "b", "-param-field", "z", "int", "c", "-set", "t", "v"},
		},
		{
			[]string{"-if", "k", "eq", "1", "+if-macro", "cheap", "p", "c"},
			[]string{"-if", "k", "eq", "1", "+if-expr", "price < cost * 1.1", "-param-field", "price", "float", "p", "-param-field", "cost", "float", "c"},
		},
		{
			[]string{"-set-macro", "k", "pi"},
			[]string{"-set-expr", "k", "3.14159"},
		},
		{
			[]string{"-set", "t", "v"},
			[]string{"-set", "t", "v"},
		},
	}
	for _, c := range cases {
		got, err := lowerMacroArgv(c.in)
		if err != nil {
			t.Errorf("%v: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v:\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	for _, bad := range [][]string{
		{"-set-macro", "s3"},
		{"-set-macro", "s3", "nope", "a"},
		{"-if-macro", "cheap", "p"},
	} {
		if _, err := lowerMacroArgv(bad); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}

func TestApplyMacroLoweringRoundTrip(t *testing.T) {
	writeMacros(t, testMacros)
	typed := []string{"-if", "k", "eq", "1", "-set-macro", "s3", "add3", "a", "b", "c"}
	frag := &lib.CodeFragment{
		Command: commandStringFor("update", typed),
		Op:      &lib.Op{Kind: "update", Argv: append([]string(nil), typed...)},
	}
	if err := applyMacroLowering(frag); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(frag.AsWrittenArgv, typed) {
		t.Errorf("AsWrittenArgv = %q", frag.AsWrittenArgv)
	}
	if argvHasMacroFlag(frag.Op.Argv) {
		t.Errorf("lowered argv still has the macro flag: %q", frag.Op.Argv)
	}
	if want := "ssql update -if k eq 1 -set-expr s3 'x + y + z' -param-field x int a -param-field y int b -param-field z int c"; frag.Command != want {
		t.Errorf("Command = %q\n   want %q", frag.Command, want)
	}
	if len(frag.PlanNotes) != 1 || !strings.Contains(frag.PlanNotes[0], "macros expanded: add3") {
		t.Errorf("PlanNotes = %v", frag.PlanNotes)
	}
	restoreAsWritten(frag)
	if !reflect.DeepEqual(frag.Op.Argv, typed) || frag.AsWrittenArgv != nil {
		t.Errorf("restored argv = %q, as-written %q", frag.Op.Argv, frag.AsWrittenArgv)
	}
	if want := commandStringFor("update", typed); frag.Command != want {
		t.Errorf("restored Command = %q, want %q", frag.Command, want)
	}

	// A fragment without a macro call is left alone, and one without Op too.
	plain := &lib.CodeFragment{Command: "ssql update -set t v", Op: &lib.Op{Kind: "update", Argv: []string{"-set", "t", "v"}}}
	if err := applyMacroLowering(plain); err != nil || plain.AsWrittenArgv != nil || plain.Command != "ssql update -set t v" {
		t.Errorf("plain fragment changed: %+v %v", plain, err)
	}
	if err := applyMacroLowering(&lib.CodeFragment{Command: "x"}); err != nil {
		t.Errorf("nil Op: %v", err)
	}
}

func TestCommandStringFor(t *testing.T) {
	if got, want := commandStringFor("where", []string{"-if-expr", "a > 1", "-param", "s", "string", "it's"}), `ssql where -if-expr 'a > 1' -param s string "it's"`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got := commandStringFor("", nil); got != "ssql" {
		t.Errorf("empty = %q", got)
	}
}
