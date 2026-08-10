package commands

import (
	"testing"
)

// TestExprToFlagConds pins the canonicalization recognizer (convergence
// Phase C): which -if-expr shapes become optimizer-visible structured
// conditions, and — just as important — which are refused.
func TestExprToFlagConds(t *testing.T) {
	accept := []struct {
		name string
		expr string
		want []whereCondition
	}{
		{"int compare", "pop > 15", []whereCondition{{Field: "pop", Operator: "gt", Value: "15"}}},
		{"string eq", `city == "Oslo"`, []whereCondition{{Field: "city", Operator: "eq", Value: "Oslo"}}},
		{"conjunction", "pop > 5 && pop <= 9", []whereCondition{
			{Field: "pop", Operator: "gt", Value: "5"}, {Field: "pop", Operator: "le", Value: "9"}}},
		{"word and", `pop >= 5 and city != "x"`, []whereCondition{
			{Field: "pop", Operator: "ge", Value: "5"}, {Field: "city", Operator: "ne", Value: "x"}}},
		{"flipped operands normalize", "15 < pop", []whereCondition{{Field: "pop", Operator: "gt", Value: "15"}}},
		{"string ops", `city contains "an" && city startsWith "H"`, []whereCondition{
			{Field: "city", Operator: "contains", Value: "an"}, {Field: "city", Operator: "startswith", Value: "H"}}},
		{"bool eq", "active == true", []whereCondition{{Field: "active", Operator: "eq", Value: "true"}}},
	}
	for _, tt := range accept {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := exprToFlagConds(tt.expr)
			if !ok {
				t.Fatalf("refused %q", tt.expr)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("cond %d: got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}

	refuse := []struct {
		name string
		expr string
	}{
		// The int-column trap: exec's -if would ParseInt("15.5"), fail, and
		// silently drop every row — canonicalizing would change results.
		{"float literal", "pop > 15.5"},
		{"or needs clause splitting", `pop > 5 || city == "x"`},
		{"bool ordering", "active > false"},
		{"field-to-field", "pop > qty"},
		{"arithmetic", "pop + 1 > 5"},
		{"function call", "len(city) > 3"},
		{"matches left alone", `city matches "^A"`},
		{"negation inside", `!(pop > 5)`},
	}
	for _, tt := range refuse {
		t.Run("refuse_"+tt.name, func(t *testing.T) {
			if conds, ok := exprToFlagConds(tt.expr); ok {
				t.Errorf("canonicalized %q to %+v — must refuse", tt.expr, conds)
			}
		})
	}
}
