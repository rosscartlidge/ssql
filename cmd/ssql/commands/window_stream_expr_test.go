package commands

import (
	"strings"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// TestParseWindowStreamExpr: a clause's -stream-expr decodes (through the
// decoder group-by uses) into a WStreamExpr spec, and the result column's
// placeholder type is "float" until the first row types it.
func TestParseWindowStreamExpr(t *testing.T) {
	clause := cf.Clause{Flags: map[string]any{
		"-order": []any{"n"},
		"-stream-expr": []any{map[string]any{
			"init": "{a:0, b:1}", "every": "{a:b, b:a+b}", "final": "b", "result-name": "fib",
		}},
	}}
	configs, err := parseWindowClauses([]cf.Clause{clause})
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || len(configs[0].Specs) != 1 {
		t.Fatalf("configs = %+v", configs)
	}
	spec := configs[0].Specs[0]
	d := ssql.DescribeWindowFunc(spec.Function)
	if spec.ResultName != "fib" || d.Kind != "stream_expr" || d.Every != "{a:b, b:a+b}" {
		t.Errorf("spec = %+v, desc = %+v", spec, d)
	}
	if got := inferWindowResultType(spec.Function, lib.NewSchema()); got != "float" {
		t.Errorf("placeholder type %q, want float", got)
	}
	if got := windowFoldResults(configs); len(got) != 1 || got[0] != "fib" {
		t.Errorf("windowFoldResults = %v", got)
	}
}

// TestWindowStreamExprSQLRefuses: generate sql has no fold; the window
// translator fails loudly naming the flag (never a silently dropped
// column).
func TestWindowStreamExprSQLRefuses(t *testing.T) {
	q := &sqlQuery{fromClause: "read_csv('x.csv')"}
	err := translateWindow(q, []string{"-order", "n", "-stream-expr", "{s:0}", "{s: s + n}", "s", "run"})
	if err == nil || !strings.Contains(err.Error(), "-stream-expr") {
		t.Fatalf("translateWindow: want a -stream-expr refusal, got %v", err)
	}
}

// TestExtractWindowFieldsStreamExpr: the optimiser cannot see which fields
// a fold's expressions read, so a window with -stream-expr reports
// "all fields" (no projection pruning above it).
func TestExtractWindowFieldsStreamExpr(t *testing.T) {
	fields, all := extractWindowFields([]string{"-order", "n", "-stream-expr", "{s:0}", "{s: s + n}", "s", "run"})
	if !all || fields != nil {
		t.Errorf("extractWindowFields = %v, %v; want nil, true", fields, all)
	}
	fields, all = extractWindowFields([]string{"-order", "n", "-sum", "n", "run"})
	if all || len(fields) != 2 {
		t.Errorf("without a fold: %v, %v", fields, all)
	}
}

// TestRenderWindowFoldSpec: the typed spec literal builds the state with
// its INIT values, steps with ONE simultaneous assignment (the swap
// semantics) and reads FINAL in its own type.
func TestRenderWindowFoldSpec(t *testing.T) {
	plan, err := lowerStreamAgg(streamExprSpec{initExpr: "{a:0, b:1}", everyExpr: "{a:b, b:a+b}", finalExpr: "b", result: "fib"}, exprGoTestSchema(), 0)
	if err != nil {
		t.Fatal(err)
	}
	lit := renderWindowFoldSpec("RowFold0", "Row", plan)
	for _, want := range []string{
		"Kind: typed.WFold",
		"&RowFold0{se0_a: 0, se0_b: 1}",
		"a.se0_a, a.se0_b = a.se0_b, (a.se0_a + a.se0_b)",
		"Final: func(s any) any { a := s.(*RowFold0); return a.se0_b }",
	} {
		if !strings.Contains(lit, want) {
			t.Errorf("spec literal lacks %q:\n%s", want, lit)
		}
	}
	def := renderWindowFoldState("RowFold0", plan)
	if !strings.Contains(def, "se0_b int64") {
		t.Errorf("state def: %s", def)
	}
	// A constant FINAL reads no state: no unused `a`.
	plan, err = lowerStreamAgg(streamExprSpec{initExpr: "{k:0}", everyExpr: "{k: k + 1}", finalExpr: "1", result: "one"}, exprGoTestSchema(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if lit := renderWindowFoldSpec("RowFold1", "Row", plan); !strings.Contains(lit, "Final: func(s any) any { return 1 }") {
		t.Errorf("constant final: %s", lit)
	}
}
