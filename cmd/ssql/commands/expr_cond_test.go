package commands

import (
	"strings"
	"testing"
)

// TestCondOpToExprGo pins the ONE flag-condition operator lowering
// (convergence Phase B) across the lhs shapes the three wrappers feed it:
// typed struct fields, record advisory GetOr, and record parameterized
// values.
func TestCondOpToExprGo(t *testing.T) {
	typedInt := exprGo{Src: "r.Pop", Type: exprGoInt}
	typedStr := exprGo{Src: "r.City", Type: exprGoString}
	recInt := exprGo{Src: `ssql.GetOr(r, "pop", float64(0))`, Type: exprGoFloat}
	recStr := exprGo{Src: `ssql.GetOr(r, "city", "")`, Type: exprGoString}

	tests := []struct {
		name     string
		lhs      exprGo
		op, val  string
		rhsParam string
		wantSrc  string
		wantImp  string
	}{
		{"typed int gt", typedInt, "gt", "15", "", "(r.Pop > 15)", ""},
		{"typed int eq float literal", typedInt, "eq", "7.5", "", "(float64(r.Pop) == 7.5)", ""},
		{"typed string lexicographic", typedStr, "gt", "Lima", "", `(r.City > "Lima")`, ""},
		{"typed contains", typedStr, "contains", "an", "", `strings.Contains(r.City, "an")`, "strings"},
		{"record param numeric", recInt, "ge", "14", "flagPopGe", `(ssql.GetOr(r, "pop", float64(0)) >= ssql.ParseFloat64(*flagPopGe))`, ""},
		{"record param string", recStr, "ne", "Oslo", "flagCityNe", `(ssql.GetOr(r, "city", "") != *flagCityNe)`, ""},
		{"record param contains", recStr, "contains", "an", "flagCityContains", `strings.Contains(ssql.GetOr(r, "city", ""), *flagCityContains)`, "strings"},
		{"record param regex compiles at call site", recStr, "regex", "^[A-M]", "flagCityRegex", `regexp.MustCompile(*flagCityRegex).MatchString(ssql.GetOr(r, "city", ""))`, "regexp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := condOpToExprGo(tt.lhs, tt.op, tt.val, tt.rhsParam)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got.Src != tt.wantSrc {
				t.Errorf("src\n  got  %s\n  want %s", got.Src, tt.wantSrc)
			}
			if got.Type != exprGoBool {
				t.Errorf("type = %s, want bool", got.Type)
			}
			if tt.wantImp != "" && !contains(strings.Join(got.Imports, ","), tt.wantImp) {
				t.Errorf("imports %v missing %q", got.Imports, tt.wantImp)
			}
		})
	}

	t.Run("literal regex hoists a compiled pattern", func(t *testing.T) {
		got, err := condOpToExprGo(typedStr, "regex", "^[A-M]", "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got.Src, ".MatchString(r.City)") {
			t.Errorf("src: %s", got.Src)
		}
		if len(got.Hoisted) != 1 || !strings.Contains(got.Hoisted[0], `regexp.MustCompile("^[A-M]")`) {
			t.Errorf("hoisted: %v", got.Hoisted)
		}
	})

	t.Run("errors are loud and specific", func(t *testing.T) {
		cases := []struct {
			name    string
			lhs     exprGo
			op, val string
			wantErr string
		}{
			{"invalid numeric literal", typedInt, "gt", "banana", "invalid numeric literal"},
			{"contains on numeric", typedInt, "contains", "x", "requires a string field"},
			{"invalid regex at codegen", typedStr, "regex", "([", "invalid regex"},
			{"unknown operator", typedInt, "frobnicate", "1", "unknown where operator"},
			{"ordering on bool", exprGo{Src: "r.Active", Type: exprGoBool}, "gt", "true", "not defined for bool"},
		}
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				_, err := condOpToExprGo(tt.lhs, tt.op, tt.val, "")
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("got %v, want error containing %q", err, tt.wantErr)
				}
			})
		}
	})
}
