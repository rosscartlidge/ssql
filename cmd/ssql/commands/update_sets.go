package commands

import (
	cf "github.com/rosscartlidge/autocli/v4"
)

// The assignments of one update clause, in the order the user wrote them.
//
// Until 2026-10-20 each backend read the clause's -set, -set-expr,
// -set-bucket and -set-field lists in its own order (exec: set, field,
// expr; record: set, expr, field; typed: field, set, expr; SQL: argv), so
// the new columns of `update -set-expr pay … -set-field who name -set tag
// 1` landed in a different order per lane. The rule since the 2026-09-23
// column-order work is that a column lands where the stage put it, and
// the stage puts it where the user wrote it: argv order. autocli records
// that order per clause (Clause.Order, v4.23.0), and this is the one
// reader of it — every lane applies the sets in this sequence.

type clauseSetKind int

const (
	clauseSetLiteral clauseSetKind = iota // -set FIELD VALUE
	clauseSetExpr                         // -set-expr FIELD EXPR, or -set-bucket desugared
	clauseSetCopy                         // -set-field FIELD SOURCE
)

type clauseSet struct {
	kind       clauseSetKind
	field      string
	value      string // literal
	expression string // expression text
	source     string // the field copied
}

// clauseSets lists a clause's assignments in argv order. A clause built
// by hand without Order (tests) falls back to list order: -set,
// -set-expr, -set-bucket, -set-field.
func clauseSets(clause cf.Clause) ([]clauseSet, error) {
	lists := map[string][]clauseSet{}
	for _, item := range flagEntries(clause, "-set") {
		if field, _ := item["field"].(string); field != "" {
			value, _ := item["value"].(string)
			lists["-set"] = append(lists["-set"], clauseSet{kind: clauseSetLiteral, field: field, value: value})
		}
	}
	for _, item := range flagEntries(clause, "-set-expr") {
		field, _ := item["field"].(string)
		expression, _ := item["expression"].(string)
		if field != "" && expression != "" {
			lists["-set-expr"] = append(lists["-set-expr"], clauseSet{kind: clauseSetExpr, field: field, expression: expression})
		}
	}
	for _, item := range flagEntries(clause, "-set-bucket") {
		field, _ := item["field"].(string)
		source, _ := item["source"].(string)
		width, _ := item["width"].(string)
		if field == "" || source == "" || width == "" {
			continue
		}
		se, err := bucketSetExpr(field, source, width)
		if err != nil {
			return nil, err
		}
		lists["-set-bucket"] = append(lists["-set-bucket"], clauseSet{kind: clauseSetExpr, field: se.field, expression: se.expression})
	}
	for _, sf := range parseSetFields(clause.Flags["-set-field"]) {
		lists["-set-field"] = append(lists["-set-field"], clauseSet{kind: clauseSetCopy, field: sf.target, source: sf.source})
	}
	order := clause.Order
	if order == nil {
		for _, flag := range []string{"-set", "-set-expr", "-set-bucket", "-set-field"} {
			for range lists[flag] {
				order = append(order, flag)
			}
		}
	}
	var out []clauseSet
	next := map[string]int{}
	for _, flag := range order {
		list, known := lists[flag]
		if !known {
			continue
		}
		// An entry the lists skipped (empty field) is not in them, so a
		// later occurrence of the same flag may run past the list's end.
		if i := next[flag]; i < len(list) {
			out = append(out, list[i])
			next[flag]++
		}
	}
	return out, nil
}

// flagEntries is the map-per-occurrence list of a multi-arg Accumulate
// flag (nil-safe).
func flagEntries(clause cf.Clause, flag string) []map[string]any {
	raw, _ := clause.Flags[flag].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// setExprsOf is the expression assignments of a clause's sets, for the
// parameter checks that read expression text (clauseExprParams).
func setExprsOf(sets []clauseSet) []setExpr {
	var out []setExpr
	for _, s := range sets {
		if s.kind == clauseSetExpr {
			out = append(out, setExpr{s.field, s.expression})
		}
	}
	return out
}
