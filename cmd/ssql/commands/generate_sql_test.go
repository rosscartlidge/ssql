package commands

import (
	"slices"
	"testing"
)

// TestTranslateTopSQL guards the `top` → SQL translation. It regressed
// silently once already: it looked for the long-removed -by flag (so no
// ORDER BY was ever emitted) and treated args[0] as N (so `-asc` became the
// LIMIT). N is the first bare positional; the field comes from -field/-f;
// -asc flips DESC→ASC.
func TestTranslateTopSQL(t *testing.T) {
	cases := []struct {
		name      string
		args      []string // args after "ssql top"
		wantOrder string
		wantLimit string
	}{
		// quoteIdent leaves simple identifiers unquoted.
		{"desc", []string{"3", "-field", "name"}, "name DESC", "3"},
		{"asc", []string{"-asc", "3", "-field", "name"}, "name ASC", "3"},
		{"asc-after-n", []string{"3", "-asc", "-field", "name"}, "name ASC", "3"},
		{"short-field", []string{"5", "-f", "salary"}, "salary DESC", "5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &sqlQuery{}
			if err := translateTop(q, c.args); err != nil {
				t.Fatalf("translateTop: %v", err)
			}
			if q.limit != c.wantLimit {
				t.Errorf("limit = %q, want %q", q.limit, c.wantLimit)
			}
			if !slices.Contains(q.orderBy, c.wantOrder) {
				t.Errorf("orderBy = %v, want to contain %q", q.orderBy, c.wantOrder)
			}
		})
	}
}
