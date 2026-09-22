package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func paramFlag(triples ...[3]string) []any {
	var out []any
	for _, t := range triples {
		out = append(out, map[string]any{"name": t[0], "type": t[1], "value": t[2]})
	}
	return out
}

func TestParseExprParams(t *testing.T) {
	ps, err := parseExprParams(paramFlag(
		[3]string{"rate", "float", "1.5"}, [3]string{"n", "int", "7"}, [3]string{"who", "string", "12"},
		[3]string{"on", "bool", "yes"}, [3]string{"since", "time", "2026-03-01"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []any{1.5, int64(7), "12", true, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	for i, p := range ps {
		if p.Value != want[i] {
			t.Errorf("%s: value %#v, want %#v", p.Name, p.Value, want[i])
		}
	}
	// A declared type is never re-inferred: string "12" stays a string.
	if _, ok := ps[2].Value.(string); !ok {
		t.Errorf("string param became %T", ps[2].Value)
	}

	bad := map[string][3]string{
		"not an int":     {"n", "int", "seven"},
		"bad type":       {"n", "integer64", "7"},
		"auto type":      {"n", "auto", "7"},
		"keyword name":   {"and", "int", "7"},
		"digit-led name": {"1x", "int", "7"},
		"dotted name":    {"a.b", "int", "7"},
	}
	for name, tr := range bad {
		if _, err := parseExprParams(paramFlag(tr)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := parseExprParams(paramFlag([3]string{"n", "int", "1"}, [3]string{"n", "int", "2"})); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("duplicate: got %v", err)
	}
}

func TestCheckExprParamsUsed(t *testing.T) {
	ps, _ := parseExprParams(paramFlag([3]string{"lo", "int", "1"}, [3]string{"hi", "int", "9"}))
	if err := checkExprParamsUsed(ps, []string{"x > lo", "x < hi"}); err != nil {
		t.Errorf("both used across two expressions: %v", err)
	}
	err := checkExprParamsUsed(ps, []string{"x > lo"})
	if err == nil || !strings.Contains(err.Error(), "-param hi") {
		t.Errorf("unused hi: got %v", err)
	}
	// A call is not a use of an identifier: len(lo) uses lo, lo() does not name a param.
	if err := checkExprParamsUsed(ps[:1], []string{"len(lo) > 0"}); err != nil {
		t.Errorf("identifier as call argument: %v", err)
	}
}

// The value of a parameter never reaches the SQL text as syntax: it is a
// literal of its declared type, quoted as every string literal is.
func TestExprParamSQL(t *testing.T) {
	ps, _ := parseExprParams(paramFlag(
		[3]string{"who", "string", "x' OR '1'='1"}, [3]string{"n", "int", "007"}, [3]string{"f", "float", "2.50"},
		[3]string{"b", "bool", "TRUE"}, [3]string{"t", "time", "2026-03-01T10:00:00Z"}))
	want := []string{"'x'' OR ''1''=''1'", "7", "2.5", "TRUE", "TIMESTAMP '2026-03-01 10:00:00'"}
	for i, p := range ps {
		if got := p.sqlLiteral(); got != want[i] {
			t.Errorf("%s: %s, want %s", p.Name, got, want[i])
		}
	}
	m := map[string]ExprParam{}
	for _, p := range ps {
		m[p.Name] = p
	}
	got, err := exprToSQLParams(`city == who && pop > n`, m)
	if err != nil {
		t.Fatal(err)
	}
	if want := `((city = 'x'' OR ''1''=''1') AND (pop > 7))`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// Without the binding the same identifier is a column.
	if got, _ := exprToSQL(`pop > n`); got != `(pop > n)` {
		t.Errorf("unbound identifier rendered as %s", got)
	}
}

func TestSQLClauseParams(t *testing.T) {
	got, err := sqlClauseParams([]string{"-if-expr", "a > lo", "-param", "lo", "int", "1", "+", "-if-expr", "a < lo", "-p", "lo", "int", "9", "+", "-if", "a", "eq", "1"}, "+")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0]["lo"].Value != int64(1) || got[1]["lo"].Value != int64(9) || got[2] != nil {
		t.Errorf("per-clause params wrong: %+v", got)
	}
	if _, err := sqlClauseParams([]string{"-param", "lo", "int"}, "+"); err == nil {
		t.Error("incomplete -param must error")
	}
	if _, err := sqlClauseParams([]string{"-param", "lo", "int", "x"}, "+"); err == nil {
		t.Error("a value not of the type must error in the SQL lane too")
	}
}

// The transpiler binds a parameter before fields, errs loudly on a name
// that is both, and refuses quietly (VM tier) on a time parameter.
func TestExprToGoParams(t *testing.T) {
	ps, _ := parseExprParams(paramFlag([3]string{"rate", "float", "1.5"}, [3]string{"since", "time", "2026-01-01"}))
	vars := exprParamsGoVars(ps)
	schema := &lib.TypedSchema{TypeName: "Row", Fields: []lib.TypedSchemaField{
		{Name: "price", GoName: "Price", GoType: "int64"}, {Name: "rate", GoName: "Rate", GoType: "float64"}}}

	res, err := exprToGoParams(`price * rate`, &lib.TypedSchema{TypeName: "Row", Fields: schema.Fields[:1]}, "r", vars)
	if err != nil || !strings.Contains(res.Src, "*flagParamRate") || res.Type != exprGoFloat {
		t.Errorf("typed: %+v, %v", res, err)
	}
	res, err = exprToGoRecordParams(`price * rate`, map[string]string{"price": "int64"}, "r", vars)
	if err != nil || !strings.Contains(res.Src, "*flagParamRate") {
		t.Errorf("record: %+v, %v", res, err)
	}

	_, err = exprToGoParams(`price * rate`, schema, "r", vars)
	if !exprIsLoud(err) || !strings.Contains(err.Error(), "same name as a field") {
		t.Errorf("collision must be loud: %v", err)
	}
	_, err = exprToGoParams(`price > since`, schema, "r", vars)
	if err == nil || exprIsLoud(err) {
		t.Errorf("time parameter must refuse quietly: %v", err)
	}

	// Lifted flags: one per parameter, typed; time travels as a string.
	cps := exprParamsCodeParams(ps)
	if cps[0].Type != "float" || cps[0].Name != "param-rate" || cps[0].Default != "1.5" || cps[1].Type != "" {
		t.Errorf("code params: %+v", cps)
	}
	thunk := exprParamsGoThunk(ps)
	if !strings.Contains(thunk, `"rate": *flagParamRate`) || !strings.Contains(thunk, `ssql.MustParseTime(*flagParamSince, "-param-since")`) {
		t.Errorf("thunk: %s", thunk)
	}
	_ = ssql.FieldTypeTime
}
