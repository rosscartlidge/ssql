package commands

import (
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

func TestClauseSetsArgvOrder(t *testing.T) {
	clause := cf.Clause{
		Flags: map[string]any{
			"-set":        []any{map[string]any{"field": "tag", "value": "1"}, map[string]any{"field": "k", "value": "2"}},
			"-set-expr":   []any{map[string]any{"field": "pay", "expression": "salary * 2"}},
			"-set-bucket": []any{map[string]any{"field": "m", "source": "ts", "width": "5m"}},
			"-set-field":  []any{map[string]any{"field": "who", "source": "name"}},
		},
		Order: []string{"-set-expr", "-set-field", "-if", "-set", "-set-bucket", "-set"},
	}
	sets, err := clauseSets(clause)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range sets {
		got = append(got, s.field)
	}
	if want := "pay who tag m k"; join(got) != want {
		t.Errorf("order = %q, want %q", join(got), want)
	}
	if sets[0].kind != clauseSetExpr || sets[1].kind != clauseSetCopy || sets[1].source != "name" || sets[2].kind != clauseSetLiteral || sets[2].value != "1" {
		t.Errorf("kinds: %+v", sets)
	}
	if sets[3].kind != clauseSetExpr || sets[3].expression != `bucket(ts, "5m")` {
		t.Errorf("bucket desugar: %+v", sets[3])
	}
	exprs := setExprsOf(sets)
	if len(exprs) != 2 || exprs[0].field != "pay" || exprs[1].field != "m" {
		t.Errorf("setExprsOf = %+v", exprs)
	}

	// No Order (a hand-built clause): list order, -set first, -set-field last.
	clause.Order = nil
	sets, _ = clauseSets(clause)
	got = nil
	for _, s := range sets {
		got = append(got, s.field)
	}
	if want := "tag k pay m who"; join(got) != want {
		t.Errorf("fallback order = %q, want %q", join(got), want)
	}
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
