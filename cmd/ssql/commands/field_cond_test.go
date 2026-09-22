package commands

import (
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func TestParseFieldConditions(t *testing.T) {
	conds, err := parseFieldConditions([]any{
		map[string]any{"field": "a", "operator": "lt", "other": "b"},
		map[string]any{"field": "s", "operator": "regex", "other": "p", "_negated": true},
	})
	if err != nil || len(conds) != 2 || !conds[0].FieldRHS || conds[0].Value != "b" || !conds[1].Negated {
		t.Errorf("got %+v, %v", conds, err)
	}
	if _, err := parseFieldConditions([]any{map[string]any{"field": "a", "operator": "near", "other": "b"}}); err == nil {
		t.Error("unknown operator accepted")
	}
	if got := conditionFields(conds); strings.Join(got, ",") != "a,b,s,p" {
		t.Errorf("both sides validated: %v", got)
	}
}

func TestFieldCondTypedGo(t *testing.T) {
	schema := &lib.TypedSchema{TypeName: "Row", Fields: []lib.TypedSchemaField{
		{Name: "a", GoName: "A", GoType: "int64"}, {Name: "f", GoName: "F", GoType: "float64"},
		{Name: "s", GoName: "S", GoType: "string"}, {Name: "p", GoName: "P", GoType: "string"},
		{Name: "t1", GoName: "T1", GoType: "time.Time"}, {Name: "t2", GoName: "T2", GoType: "time.Time"},
	}}
	cases := []struct {
		c    Condition
		want string
		ok   bool
	}{
		{Condition{Field: "a", Operator: "lt", Value: "f", FieldRHS: true}, "float64(r.A) < r.F", true},
		{Condition{Field: "s", Operator: "contains", Value: "p", FieldRHS: true, Negated: true}, "!(strings.Contains(r.S, r.P))", true},
		{Condition{Field: "s", Operator: "regex", Value: "p", FieldRHS: true}, "exprfn.RegexMatch(r.P, r.S)", true},
		{Condition{Field: "t1", Operator: "lt", Value: "t2", FieldRHS: true}, "(r.T1.Before(r.T2))", true},
		{Condition{Field: "a", Operator: "contains", Value: "s", FieldRHS: true}, "", false},  // int against a string operator
		{Condition{Field: "a", Operator: "eq", Value: "s", FieldRHS: true}, "", false},        // mixed kinds: record decides
		{Condition{Field: "t1", Operator: "contains", Value: "t2", FieldRHS: true}, "", false}, // time with a string operator
	}
	for _, c := range cases {
		res, ok, err := fieldCondTypedGo(schema, c.c)
		if err != nil {
			t.Fatalf("%+v: %v", c.c, err)
		}
		if ok != c.ok || (ok && !strings.Contains(res.Src, c.want)) {
			t.Errorf("%s %s %s: ok=%v src=%q, want ok=%v containing %q", c.c.Field, c.c.Operator, c.c.Value, ok, res.Src, c.ok, c.want)
		}
	}
	if _, _, err := fieldCondTypedGo(schema, Condition{Field: "a", Operator: "eq", Value: "nope", FieldRHS: true}); err == nil {
		t.Error("unknown field must be loud")
	}
}

func TestTranslateFieldCondition(t *testing.T) {
	cases := map[[3]string]string{
		{"a", "lt", "b"}:              "a < b",
		{"a", "ne", "1st"}:            `a != "1st"`,
		{"s", "contains", "p"}:        "strpos(s, p) > 0",
		{"s", "startswith", "p"}:      "left(s, length(p)) = p",
		{"s", "endswith", "p"}:        "right(s, length(p)) = p",
		{"s", "regex", "p"}:           "regexp_matches(s, p)",
		{"select", "eq", "from"}:      `"select" = "from"`,
	}
	for in, want := range cases {
		if got := translateFieldCondition(in[0], in[1], in[2]); got != want {
			t.Errorf("%v: got %s, want %s", in, got, want)
		}
	}
}

// -param-field: parsed beside -param, one namespace; the SQL rendering is
// the column, CAST when the known kind differs; field params never lift
// to a flag of the binary; the typed binding is the struct field or a cast.
func TestParamField(t *testing.T) {
	ps, err := parseExprParams(
		[]any{map[string]any{"name": "lo", "type": "int", "value": "5"}},
		[]any{map[string]any{"name": "n", "type": "float", "field": "k"}, map[string]any{"name": "s", "type": "string", "field": "1st"}})
	if err != nil || len(ps) != 3 || ps[1].Field != "k" || ps[2].Field != "1st" {
		t.Fatalf("%+v %v", ps, err)
	}
	if _, err := parseExprParams([]any{map[string]any{"name": "n", "type": "int", "value": "1"}}, []any{map[string]any{"name": "n", "type": "int", "field": "k"}}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("one namespace: %v", err)
	}
	if got := exprParamFieldNames(ps); strings.Join(got, ",") != "k,1st" {
		t.Errorf("field names: %v", got)
	}
	if cps := exprParamsCodeParams(ps); len(cps) != 1 || cps[0].Name != "param-lo" {
		t.Errorf("only the static param lifts: %+v", cps)
	}
	if got := exprParamsGoFields(ps); got != `runtime.FieldParams{"n": {Field: "k", Type: ssql.FieldTypeFloat}, "s": {Field: "1st", Type: ssql.FieldTypeString}}` {
		t.Errorf("fields literal: %s", got)
	}
	prev := sqlColumnKinds
	sqlColumnKinds = map[string]string{"k": "int", "1st": "string"}
	defer func() { sqlColumnKinds = prev }()
	if got := ps[1].sqlLiteral(); got != "CAST(k AS DOUBLE)" {
		t.Errorf("cast view: %s", got)
	}
	if got := ps[2].sqlLiteral(); got != `"1st"` {
		t.Errorf("same kind, no cast: %s", got)
	}
	schema := &lib.TypedSchema{TypeName: "Row", Fields: []lib.TypedSchemaField{{Name: "k", GoName: "K", GoType: "int64"}, {Name: "1st", GoName: "F1st", GoType: "string"}}}
	vars := exprParamsGoFieldVarsTyped(exprParamsGoVars(ps), ps, schema, "r")
	if vars["n"].Src != `ssql.MustCast[float64](r.K, ssql.FieldTypeFloat, "k")` || vars["s"].Src != "r.F1st" || vars["lo"].Src != "int64(*flagParamLo)" {
		t.Errorf("typed bindings: %+v", vars)
	}
}
