package runtime

import (
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

// TestFieldShadowsBuiltin: a record field named like an expr builtin
// (date, len, type, max, …) is the field when used bare and the function
// when called — the transpiler's rule, now the VM's too. Until v4.94.0
// `date > "…"` failed to compile ("mismatched types func(...) and string").
func TestFieldShadowsBuiltin(t *testing.T) {
	rec := ssql.MakeMutableRecord().
		String("date", "2026-02-02").
		Int("len", 3).
		String("type", "gadget").
		Int("amount", 1500).
		Freeze()
	cases := []struct {
		expr string
		want any
	}{
		{`date > "2026-02-01"`, true},
		{`date(date) > date("2026-02-01")`, true}, // the call is still the builtin
		{`date[0:7]`, "2026-02"},
		{`len > 2 && len("abc") == 3`, true}, // field bare, function called
		{`type == "gadget" && type(amount) == "int"`, true},
		{`max(amount, len) == 1500`, true},
		{`amount > 1000 && date >= "2026-02-01"`, true},
	}
	for _, c := range cases {
		eval, err := CompileExpr(c.expr)
		if err != nil {
			t.Errorf("%s: compile: %v", c.expr, err)
			continue
		}
		got, err := eval(rec)
		if err != nil || got != c.want {
			t.Errorf("%s: got %#v (%v), want %#v", c.expr, got, err, c.want)
		}
	}
	// The same through the caller-built-map path generated code uses.
	eval, err := CompileExprEnv(`date >= "2026-02-01" && len == 3`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := eval(map[string]any{"date": "2026-02-02", "len": int64(3)}); err != nil || got != true {
		t.Errorf("CompileExprEnv: got %#v (%v)", got, err)
	}
	// A builtin-named field the record does NOT have is still an unknown
	// field, not a silent nil.
	evalRec, err := CompileExpr(`len > 1`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evalRec(ssql.MakeMutableRecord().Int("amount", 1).Freeze()); err == nil || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "len") {
		t.Errorf("missing builtin-named field should be reported as unknown: %v", err)
	}
}
