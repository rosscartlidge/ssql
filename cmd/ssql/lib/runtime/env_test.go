package runtime

import (
	"testing"
)

// TestCompileExprEnv covers the Tier-V env-map evaluator: field access,
// helpers (has/getOr close over the FIELD map, not the merged env — so
// has("sha256") must be false), and the hash builtins.
func TestCompileExprEnv(t *testing.T) {
	fields := map[string]any{"pop": int64(7), "city": "Oslo"}

	tests := []struct {
		expr string
		want any
	}{
		{"pop > 5", true},
		{"pop / 2", 3.5},
		{`city + "!"`, "Oslo!"},
		{`has("pop")`, true},
		{`has("nope")`, false},
		{`has("sha256")`, false}, // helper names are NOT fields
		{`getOr("pop", 0)`, int64(7)},
		{`getOr("nope", 5)`, 5},
		{`sha256("x") != ""`, true},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			eval, err := CompileExprEnv(tt.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, err := eval(fields)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %#v (%T), want %#v (%T)", got, got, tt.want, tt.want)
			}
		})
	}

	if _, err := CompileExprEnv("pop >"); err == nil {
		t.Errorf("invalid expression compiled")
	}
}

// TestCompileExprFilterEnv locks the predicate contract shared with the
// record path's CompileExprFilter: false on eval error and non-bool results.
func TestCompileExprFilterEnv(t *testing.T) {
	fields := map[string]any{"pop": int64(7), "city": "Oslo"}

	filter, err := CompileExprFilterEnv("pop > 5")
	if err != nil {
		t.Fatal(err)
	}
	if !filter(fields) {
		t.Errorf("pop > 5 with pop=7 should pass")
	}

	nonBool, err := CompileExprFilterEnv("pop + 1")
	if err != nil {
		t.Fatal(err)
	}
	if nonBool(fields) {
		t.Errorf("non-bool result must filter false")
	}

	evalErr, err := CompileExprFilterEnv("missing + 1")
	if err != nil {
		t.Fatal(err)
	}
	if evalErr(fields) {
		t.Errorf("eval error must filter false")
	}
}

// TestMustCoerce covers the assignment typing helpers' accepting paths (the
// rejecting path calls os.Exit — its message is asserted at the emission
// level, not here).
func TestMustCoerce(t *testing.T) {
	if got := MustCoerceInt64(int64(3), "e"); got != 3 {
		t.Errorf("int64: %v", got)
	}
	if got := MustCoerceInt64(3, "e"); got != 3 {
		t.Errorf("int: %v", got)
	}
	if got := MustCoerceFloat64(2.5, "e"); got != 2.5 {
		t.Errorf("float64: %v", got)
	}
	if got := MustCoerceFloat64(int64(2), "e"); got != 2.0 {
		t.Errorf("int64→float64 widening: %v", got)
	}
	if got := MustCoerceString("x", "e"); got != "x" {
		t.Errorf("string: %v", got)
	}
	if got := MustCoerceBool(true, "e"); got != true {
		t.Errorf("bool: %v", got)
	}
}
