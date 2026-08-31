package commands

import (
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// exprGoTestSchema covers all four MVP scalar types plus a time.Time field
// (which must REFUSE to transpile, forcing fallback).
func exprGoTestSchema() *lib.TypedSchema {
	return &lib.TypedSchema{
		TypeName: "Row",
		Fields: []lib.TypedSchemaField{
			{Name: "pop", GoName: "Pop", GoType: "int64"},
			{Name: "price", GoName: "Price", GoType: "float64"},
			{Name: "qty", GoName: "Qty", GoType: "int64"},
			{Name: "city", GoName: "City", GoType: "string"},
			{Name: "active", GoName: "Active", GoType: "bool"},
			{Name: "when", GoName: "When", GoType: "time.Time"},
		},
	}
}

// TestExprToGo pins the exact emitted source for every semantics row in the
// implementation plan's §2 tables (doc/research/expr-transpiler-implementation-plan.md).
// The single most important row: int/int division is ALWAYS float64.
func TestExprToGo(t *testing.T) {
	schema := exprGoTestSchema()

	tests := []struct {
		name     string
		expr     string
		wantSrc  string
		wantType exprGoType
		wantImp  string // one import that must be present ("" = none required)
	}{
		// §2a arithmetic and numeric promotion
		{"int division is float", "pop / qty", "(float64(r.Pop) / float64(r.Qty))", exprGoFloat, ""},
		{"literal division is float", "7 / 2", "(float64(7) / 2)", exprGoFloat, ""},
		{"float division no double wrap", "price / 2", "(price...)", exprGoFloat, ""}, // checked via prefix below
		{"int modulo stays int", "pop % 3", "(r.Pop % 3)", exprGoInt, ""},
		{"int add stays int", "pop + 1", "(r.Pop + 1)", exprGoInt, ""},
		{"mixed add promotes", "pop + price", "(float64(r.Pop) + r.Price)", exprGoFloat, ""},
		{"int literal adapts in float context", "price * 2", "(r.Price * 2)", exprGoFloat, ""},
		{"power is math.Pow", "pop ** 2", "math.Pow(float64(r.Pop), 2)", exprGoFloat, "math"},
		{"caret is power too", "price ^ 2", "math.Pow(r.Price, 2)", exprGoFloat, "math"},
		{"unary minus preserves type", "-pop", "(-r.Pop)", exprGoInt, ""},
		{"string concat", `city + "!"`, `(r.City + "!")`, exprGoString, ""},

		// bucket() (DFC121)
		{"bucket int keeps int", `bucket(pop, "10s")`, `exprfn.BucketInt64(r.Pop, 10000000000)`, exprGoInt, "github.com/rosscartlidge/ssql/v4/exprfn"},
		{"bucket float keeps float", `bucket(price, "3s")`, `exprfn.BucketFloat64(r.Price, 3000000000)`, exprGoFloat, "github.com/rosscartlidge/ssql/v4/exprfn"},

		// §2b comparison
		{"int literal compare", "pop > 15", "(r.Pop > 15)", exprGoBool, ""},
		{"float field int literal", "price > 15", "(r.Price > 15)", exprGoBool, ""},
		{"mixed compare promotes", "pop > price", "(float64(r.Pop) > r.Price)", exprGoBool, ""},
		{"numeric equality cross-width", "pop == 7.0", "(float64(r.Pop) == 7.0)", exprGoBool, ""},
		{"string compare", `city >= "M"`, `(r.City >= "M")`, exprGoBool, ""},
		{"chained comparison desugars", "1 < pop < 3", "((1 < r.Pop) && (r.Pop < 3))", exprGoBool, ""},

		// §2c missing fields and helpers
		{"has known field folds true", `has("pop")`, "true", exprGoBool, ""},
		{"has unknown field folds false", `has("nope")`, "false", exprGoBool, ""},
		{"getOr known field is the field", `getOr("pop", 0)`, "r.Pop", exprGoInt, ""},
		{"getOr unknown field is the default", `getOr("nope", 5)`, "5", exprGoInt, ""},
		{"coalesce on field is the field", "pop ?? 0", "r.Pop", exprGoInt, ""},
		{"coalesce on unknown is the default", "nope ?? 5", "5", exprGoInt, ""},

		// §2d built-ins
		{"len counts runes", "len(city)", "exprfn.RuneLen(r.City)", exprGoInt, exprfnImport},
		{"abs preserves int", "abs(pop)", "exprfn.Abs(r.Pop)", exprGoInt, exprfnImport},
		{"abs preserves float", "abs(price)", "exprfn.Abs(r.Price)", exprGoFloat, exprfnImport},
		{"round half away from zero", "round(price)", "math.Round(r.Price)", exprGoFloat, "math"},
		{"round coerces int arg", "round(pop)", "math.Round(float64(r.Pop))", exprGoFloat, "math"},
		{"floor", "floor(price)", "math.Floor(r.Price)", exprGoFloat, "math"},
		{"min same type uses builtin", "min(pop, qty)", "min(r.Pop, r.Qty)", exprGoInt, ""},
		{"int of float truncates", "int(price)", "int64(r.Price)", exprGoInt, ""},
		{"float of int", "float(pop)", "float64(r.Pop)", exprGoFloat, ""},
		{"string of int", "string(pop)", "strconv.FormatInt(r.Pop, 10)", exprGoString, "strconv"},
		{"upper", "upper(city)", "strings.ToUpper(r.City)", exprGoString, "strings"},
		{"pipe desugars", "city | upper()", "strings.ToUpper(r.City)", exprGoString, "strings"},
		{"contains operator", `city contains "x"`, `strings.Contains(r.City, "x")`, exprGoBool, "strings"},
		{"startsWith operator", `city startsWith "A"`, `strings.HasPrefix(r.City, "A")`, exprGoBool, "strings"},
		{"hasPrefix call form", `hasPrefix(city, "A")`, `strings.HasPrefix(r.City, "A")`, exprGoBool, "strings"},
		{"in list literal", `city in ["a", "b"]`, `((r.City == "a") || (r.City == "b"))`, exprGoBool, ""},
		{"in numeric list", "pop in [1, 2]", "((r.Pop == 1) || (r.Pop == 2))", exprGoBool, ""},
		{"ternary same type", `active ? "y" : "n"`, `func() string { if r.Active { return "y" }; return "n" }()`, exprGoString, ""},
		{"ternary numeric unifies to float", "active ? 1 : 2.5", "func() float64 { if r.Active { return 1 }; return 2.5 }()", exprGoFloat, ""},

		// logic
		{"and or not", `pop > 5 && !(city == "x") || active`,
			`(((r.Pop > 5) && !((r.City == "x"))) || r.Active)`, exprGoBool, ""},
		{"word operators", `pop > 5 and active`, `((r.Pop > 5) && r.Active)`, exprGoBool, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := exprToGo(tt.expr, schema, "r")
			if err != nil {
				t.Fatalf("exprToGo(%q) error: %v", tt.expr, err)
			}
			if tt.name == "float division no double wrap" {
				// price is already float64: no float64() wrap on the left.
				if strings.Contains(got.Src, "float64(r.Price)") {
					t.Errorf("redundant coercion of float field: %s", got.Src)
				}
				return
			}
			if got.Src != tt.wantSrc {
				t.Errorf("exprToGo(%q)\n  got  %s\n  want %s", tt.expr, got.Src, tt.wantSrc)
			}
			if got.Type != tt.wantType {
				t.Errorf("exprToGo(%q) type = %s, want %s", tt.expr, got.Type, tt.wantType)
			}
			if tt.wantImp != "" {
				found := false
				for _, imp := range got.Imports {
					if imp == tt.wantImp {
						found = true
					}
				}
				if !found {
					t.Errorf("exprToGo(%q) imports %v missing %q", tt.expr, got.Imports, tt.wantImp)
				}
			}
		})
	}
}

// TestExprToGoRecord pins the record-mode emission (expr-transpiler Phase
// 4): identifiers become typed ssql.GetOr calls with the type from the
// advisory map; all §2 semantics (division!, promotion) carry over.
func TestExprToGoRecord(t *testing.T) {
	advisory := map[string]string{
		"pop": "int64", "price": "float64", "city": "string", "active": "bool", "when": "time.Time",
	}

	tests := []struct {
		name     string
		expr     string
		wantSrc  string
		wantType exprGoType
	}{
		{"int field compare", "pop > 15", `(ssql.GetOr(r, "pop", int64(0)) > 15)`, exprGoBool},
		{"string field", `city == "Oslo"`, `(ssql.GetOr(r, "city", "") == "Oslo")`, exprGoBool},
		{"division is float", "pop / 2", `(float64(ssql.GetOr(r, "pop", int64(0))) / 2)`, exprGoFloat},
		{"int arithmetic stays int", "pop + 1", `(ssql.GetOr(r, "pop", int64(0)) + 1)`, exprGoInt},
		{"mixed promotes", "pop + price", `(float64(ssql.GetOr(r, "pop", int64(0))) + ssql.GetOr(r, "price", float64(0)))`, exprGoFloat},
		{"bool field", "active", `ssql.GetOr(r, "active", false)`, exprGoBool},
		{"has folds against advisory", `has("pop") && has("nope")`, "(true && false)", exprGoBool},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := exprToGoRecord(tt.expr, advisory, "r")
			if err != nil {
				t.Fatalf("exprToGoRecord(%q) error: %v", tt.expr, err)
			}
			if got.Src != tt.wantSrc {
				t.Errorf("exprToGoRecord(%q)\n  got  %s\n  want %s", tt.expr, got.Src, tt.wantSrc)
			}
			if got.Type != tt.wantType {
				t.Errorf("type = %s, want %s", got.Type, tt.wantType)
			}
		})
	}

	t.Run("advisory-untypeable field refuses", func(t *testing.T) {
		if _, err := exprToGoRecord("when > when", advisory, "r"); err == nil {
			t.Errorf("time.Time advisory field must refuse")
		}
	})
	t.Run("unknown field errors", func(t *testing.T) {
		_, err := exprToGoRecord("missing > 5", advisory, "r")
		if err == nil {
			t.Fatalf("unknown field accepted")
		}
	})
}

// TestExprToGoMatches pins the hoisted-regex emission for `matches`.
func TestExprToGoMatches(t *testing.T) {
	got, err := exprToGo(`city matches "^A"`, exprGoTestSchema(), "r")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(got.Src, ".MatchString(r.City)") {
		t.Errorf("src = %s, want a .MatchString(r.City) call", got.Src)
	}
	if len(got.Hoisted) != 1 || !strings.Contains(got.Hoisted[0], `regexp.MustCompile("^A")`) {
		t.Errorf("hoisted = %v, want one regexp.MustCompile var", got.Hoisted)
	}
	// Content-addressed name: the same pattern must produce the identical
	// declaration (so the assembler's dedup collapses them).
	again, err := exprToGo(`city matches "^A"`, exprGoTestSchema(), "r")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if again.Hoisted[0] != got.Hoisted[0] {
		t.Errorf("same pattern produced different hoisted decls: %q vs %q", got.Hoisted[0], again.Hoisted[0])
	}
}

// TestExprToGoErrors: every refusal must be loud and name the construct —
// the caller turns these into fallback decisions, and -explain (Phase 1.5)
// will surface the text.
func TestExprToGoErrors(t *testing.T) {
	schema := exprGoTestSchema()

	tests := []struct {
		name    string
		expr    string
		wantErr string // substring the error must contain
	}{
		{"unknown identifier", "missing > 5", `unknown field "missing"`},
		{"unknown identifier lists fields", "missing > 5", "pop, price, qty, city, active, when"},
		{"string plus int", `city + 1`, "numeric"},
		{"string compared to int", `city > 5`, "cannot compare"},
		{"cross-type equality", `city == 7`, "cannot compare"},
		{"time field refuses", `when > 5`, "time.Time"},
		{"float modulo", "price % 2", "integer"},
		{"dynamic matches pattern", "city matches city", "literal pattern"},
		// The VM returns the winning operand with its OWN type (min(-3, -2.5)
		// is int64 -3) — caught by the differential harness on its first run;
		// no static Go type can express it.
		{"mixed min keeps winner type", "min(pop, price)", "winner's type"},
		{"in non-literal list", "pop in pop", "list literal"},
		{"unsupported function", `fromJSON(city)`, `function "fromJSON"`},
		{"coalesce non-field", `upper(city) ?? "x"`, "field on the left"},
		{"int of string", `int(city)`, "int() of a string"},
		{"ternary mixed branches", `active ? 1 : "x"`, "incompatible types"},
		{"map literal", `{a: 1}`, "no native Go emission"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := exprToGo(tt.expr, schema, "r")
			if err == nil {
				t.Fatalf("exprToGo(%q) succeeded, want error containing %q", tt.expr, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("exprToGo(%q) error %q does not contain %q", tt.expr, err.Error(), tt.wantErr)
			}
		})
	}
}

// TestExprToGoBool enforces the boolean-result contract for -if-expr.
func TestExprToGoBool(t *testing.T) {
	schema := exprGoTestSchema()
	if _, err := exprToGoBool("pop > 5", schema, "r"); err != nil {
		t.Errorf("bool predicate rejected: %v", err)
	}
	if _, err := exprToGoBool("pop + 5", schema, "r"); err == nil {
		t.Errorf("non-bool expression accepted as predicate")
	} else if !strings.Contains(err.Error(), "not a boolean predicate") {
		t.Errorf("error %q should name the boolean contract", err)
	}
}
