package commands

import (
	"errors"
	"strings"
	"testing"
)

// TestLowerExprAgg pins the -expr aggregation lowering: patcher normal form
// → accumulator terms (deduped by element source, keeping the element's own
// type), a shared count, and the outer expression over placeholders,
// float64-coerced.
func TestLowerExprAgg(t *testing.T) {
	schema := exprGoTestSchema() // pop/qty int64, price float64, city string, …

	t.Run("sum arithmetic over count", func(t *testing.T) {
		plan, err := lowerExprAgg(exprSpec{expression: "sum(pop * 2) / count()", result: "v"}, schema, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Terms) != 1 || plan.Terms[0].ElemSrc != "(r.Pop * 2)" || plan.Terms[0].Type != exprGoInt {
			t.Errorf("terms: %+v", plan.Terms)
		}
		if !plan.UsesCount {
			t.Errorf("count() not detected")
		}
		if plan.Result != "(float64(a.ea0_t0) / float64(a.ea0_cnt))" {
			t.Errorf("result: %s", plan.Result)
		}
	})

	t.Run("avg desugars to sum over len", func(t *testing.T) {
		plan, err := lowerExprAgg(exprSpec{expression: "avg(pop)", result: "m"}, schema, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Terms) != 1 || plan.Terms[0].ElemSrc != "r.Pop" || !plan.UsesCount {
			t.Errorf("plan: %+v", plan)
		}
	})

	t.Run("identical terms dedupe", func(t *testing.T) {
		plan, err := lowerExprAgg(exprSpec{expression: "sum(pop) + sum(pop)", result: "d"}, schema, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Terms) != 1 {
			t.Errorf("identical sum terms must share one accumulator: %+v", plan.Terms)
		}
		if plan.Result != "float64((a.ea0_t0 + a.ea0_t0))" {
			t.Errorf("result: %s", plan.Result)
		}
	})

	t.Run("int accumulator keeps int semantics", func(t *testing.T) {
		// sum of ints is an int in the VM; `% 5` must stay integer modulo,
		// which a blanket float64 accumulator would refuse.
		plan, err := lowerExprAgg(exprSpec{expression: "sum(pop) % 5", result: "m"}, schema, 0)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Terms[0].Type != exprGoInt {
			t.Errorf("int sum must accumulate int64: %+v", plan.Terms[0])
		}
		if plan.Result != "float64((a.ea0_t0 % 5))" {
			t.Errorf("result: %s", plan.Result)
		}
	})

	t.Run("float element", func(t *testing.T) {
		plan, err := lowerExprAgg(exprSpec{expression: "sum(price * float(qty))", result: "rev"}, schema, 0)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Terms[0].Type != exprGoFloat {
			t.Errorf("float element must accumulate float64: %+v", plan.Terms[0])
		}
	})

	t.Run("refusals", func(t *testing.T) {
		cases := []struct {
			name    string
			expr    string
			wantErr string
		}{
			{"no aggregation", "1 + 2", "contains no aggregation"},
			{"outer field is the VM's value array", "sum(pop) / len(pop)", "value ARRAY"},
			{"non-numeric sum element", `sum(city)`, "numeric"},
		}
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				_, err := lowerExprAgg(exprSpec{expression: tt.expr, result: "x"}, schema, 0)
				if err == nil {
					t.Fatalf("lowering succeeded, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not contain %q", err, tt.wantErr)
				}
				var unknownField *exprUnknownFieldError
				if errors.As(err, &unknownField) {
					t.Errorf("refusal must not carry the loud unknown-field type: %v", err)
				}
			})
		}
	})

	t.Run("unknown field inside sum is loud", func(t *testing.T) {
		// The VM's own compiler (CompileAggExprPatched replicates it
		// exactly) rejects the unknown name — invalid in every mode, so
		// the lowering wraps it as a loud codegen error, not a fallback.
		_, err := lowerExprAgg(exprSpec{expression: "sum(nope * 2)", result: "x"}, schema, 0)
		if err == nil {
			t.Fatal("lowering succeeded for unknown field")
		}
		if !exprIsLoud(err) {
			t.Errorf("unknown field inside sum() must be loud, got %v", err)
		}
	})
}
