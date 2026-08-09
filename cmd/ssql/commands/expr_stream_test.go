package commands

import (
	"strings"
	"testing"
)

// TestLowerStreamAgg pins the typed lowering of -stream-expr folds:
// state fields, the every-expressions (all reading OLD state — the
// emission site joins them into ONE multi-assignment), the float64-coerced
// final, widening, and the record-shadows-state resolution order.
func TestLowerStreamAgg(t *testing.T) {
	schema := exprGoTestSchema() // pop/qty int64, price float64, city string, …

	t.Run("classic avg fold", func(t *testing.T) {
		plan, err := lowerStreamAgg(streamExprSpec{
			initExpr: "{s:0, n:0}", everyExpr: "{s: s+pop, n: n+1}", finalExpr: "s/n", result: "avg",
		}, schema, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.States) != 2 || plan.States[0].GoName != "se0_s" || plan.States[0].Type != exprGoInt {
			t.Errorf("states: %+v", plan.States)
		}
		if plan.Every[0] != "(a.se0_s + r.Pop)" || plan.Every[1] != "(a.se0_n + 1)" {
			t.Errorf("every: %v", plan.Every)
		}
		if plan.Final != "(float64(a.se0_s) / float64(a.se0_n))" {
			t.Errorf("final: %s", plan.Final)
		}
	})

	t.Run("every widens int state to float64", func(t *testing.T) {
		plan, err := lowerStreamAgg(streamExprSpec{
			initExpr: "{s:0}", everyExpr: "{s: s + price}", finalExpr: "s", result: "total",
		}, schema, 0)
		if err != nil {
			t.Fatal(err)
		}
		if plan.States[0].Type != exprGoFloat {
			t.Errorf("state not widened: %+v", plan.States[0])
		}
		if plan.Every[0] != "(a.se0_s + r.Price)" {
			t.Errorf("every after widening: %v", plan.Every)
		}
	})

	t.Run("record shadows state", func(t *testing.T) {
		// State key "pop" collides with the schema field: inside every, the
		// bare identifier must resolve to the RECORD field (r.Pop), matching
		// evalStreamAggExpr's env order (maps.Insert(record) after
		// maps.Copy(state)).
		plan, err := lowerStreamAgg(streamExprSpec{
			initExpr: "{pop:0}", everyExpr: "{pop: pop + 1}", finalExpr: "pop", result: "x",
		}, schema, 0)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Every[0] != "(r.Pop + 1)" {
			t.Errorf("record must shadow state: %v", plan.Every)
		}
		// FINAL sees state only — there it IS the state field.
		if !strings.Contains(plan.Final, "a.se0_pop") {
			t.Errorf("final must resolve state: %s", plan.Final)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		cases := []struct {
			name    string
			spec    streamExprSpec
			wantErr string
		}{
			{"non-object init", streamExprSpec{initExpr: "5", everyExpr: "{s:1}", finalExpr: "s"}, "not an object literal"},
			{"non-literal init", streamExprSpec{initExpr: "{s: pop}", everyExpr: "{s: s}", finalExpr: "s"}, "init"},
			{"every drops a key", streamExprSpec{initExpr: "{s:0, n:0}", everyExpr: "{s: s+1}", finalExpr: "s"}, "do not match init keys"},
			{"every adds a key", streamExprSpec{initExpr: "{s:0}", everyExpr: "{s: s+1, extra: 1}", finalExpr: "s"}, "do not match init keys"},
			{"non-numeric final", streamExprSpec{initExpr: "{s:0}", everyExpr: "{s: s+1}", finalExpr: `s > 0 ? "hi" : "lo"`}, "must be numeric"},
			{"state type flip", streamExprSpec{initExpr: "{s:0}", everyExpr: `{s: "x"}`, finalExpr: "s"}, "state field"},
		}
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				_, err := lowerStreamAgg(tt.spec, schema, 0)
				if err == nil {
					t.Fatalf("lowering succeeded, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not contain %q", err, tt.wantErr)
				}
			})
		}
	})

	t.Run("unknown record field is a loud error type", func(t *testing.T) {
		_, err := lowerStreamAgg(streamExprSpec{
			initExpr: "{s:0}", everyExpr: "{s: s + nope}", finalExpr: "s", result: "x",
		}, schema, 0)
		if err == nil || !strings.Contains(err.Error(), `unknown field "nope"`) {
			t.Fatalf("want unknown-field error, got %v", err)
		}
	})
}
