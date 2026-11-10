package commands

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// isSQLLookupJoin reports whether a join's argv is a lookup: an -as, or
// several -on/-using clauses (separated by `-`). Until 2026-11-02 the
// first was refused and the second translated only its LAST clause.
func isSQLLookupJoin(args []string) bool {
	keys := 0
	for _, a := range args {
		switch a {
		case "-as":
			return true
		case "-on", "-o", "-using", "-u":
			keys++
		}
	}
	return keys > 1
}

// translateLookupJoin renders a lookup join (ssql.InnerLookupJoin, or
// LookupJoin under -type left) as one JOIN of the side per clause, each
// aliased __rN, selecting the left row and each clause's renamed fields:
//
//	SELECT __l.*, __r0."kind_name" AS "a_kind_name", __r1."kind_name" AS "z_kind_name"
//	FROM (…) AS __l
//	JOIN 'kind.csv' AS __r0 ON __l."a_kind" = __r0."kind"
//	JOIN 'kind.csv' AS __r1 ON __l."z_kind" = __r1."kind"
//
// The matches multiply as the record lookup's do, and a LEFT JOIN keeps
// an unmatched row with the clause's fields NULL — absent, as LookupJoin
// leaves them.
func translateLookupJoin(q *sqlQuery, args []string, funcFrags []*lib.CodeFragment) error {
	filePath := args[0]
	kind := "JOIN"
	var clauses []ssql.LookupClause
	var renameOrder [][]string // each clause's -as targets' sources, as written
	cur := func() *ssql.LookupClause {
		if len(clauses) == 0 {
			clauses = append(clauses, ssql.LookupClause{})
			renameOrder = append(renameOrder, nil)
		}
		return &clauses[len(clauses)-1]
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-":
			clauses = append(clauses, ssql.LookupClause{})
			renameOrder = append(renameOrder, nil)
		case "-using", "-u":
			if i+1 < len(args) {
				cur().LeftField, cur().RightField = args[i+1], args[i+1]
				i++
			}
		case "-on", "-o":
			if i+2 < len(args) {
				cur().LeftField, cur().RightField = args[i+1], args[i+2]
				i += 2
			}
		case "-as":
			if i+2 < len(args) {
				c := cur()
				if c.FieldRenames == nil {
					c.FieldRenames = make(map[string]string)
				}
				c.FieldRenames[args[i+1]] = args[i+2]
				renameOrder[len(renameOrder)-1] = append(renameOrder[len(renameOrder)-1], args[i+1])
				i += 2
			}
		case "-type", "-t":
			if i+1 < len(args) {
				if _, err := lookupJoinFunc(args[i+1]); err != nil {
					return err
				}
				if args[i+1] == "left" {
					kind = "LEFT JOIN"
				}
				i++
			}
		case "-suffix", "-exclude-left", "-exclude-right":
			return fmt.Errorf("join %s has no SQL translation yet — rename or drop the columns with `rename` / `exclude` after the join, or use generate go", args[i])
		}
	}
	// the clauses the join kept: one with a key (parseJoinClauses drops
	// the rest)
	clauses = slices.DeleteFunc(clauses, func(c ssql.LookupClause) bool { return c.LeftField == "" })
	for _, c := range clauses {
		if c.FieldRenames == nil {
			return fmt.Errorf("join with several -on clauses copies every right field once per clause, which has no SQL translation — name what each clause brings with -as, or use generate go")
		}
	}

	var rightFields []string
	if strings.HasPrefix(filePath, "/dev/fd/") && len(funcFrags) > 0 {
		rightFields = fragmentFields(funcFrags[len(funcFrags)-1].FuncBody)
	} else {
		rightFields = sideFileFields(filePath)
	}
	if _, err := resolveJoin(q.columns, rightFields, clauses, joinOptions{}); err != nil {
		return err
	}

	var src string
	if strings.HasPrefix(filePath, "/dev/fd/") && len(funcFrags) > 0 {
		side := buildJoinQuery(funcFrags[len(funcFrags)-1])
		if side == nil {
			return sqlSideError("join")
		}
		src = "(" + renderSelect(side) + ")"
	} else {
		var err error
		if src, _, err = sqlSideFileSource(filePath); err != nil {
			return err
		}
	}

	sel := []string{"__l.*"}
	var joins []string
	columns := slices.Clone(q.columns)
	for i, c := range clauses {
		alias := fmt.Sprintf("__r%d", i)
		joins = append(joins, fmt.Sprintf("%s %s AS %s ON __l.%s = %s.%s", kind, src, alias, quoteIdent(c.LeftField), alias, quoteIdent(c.RightField)))
		// the renamed fields in the right side's order when it is known
		// (the record lookup's), else as written
		order := renameOrder[i]
		if rightFields != nil {
			order = slices.DeleteFunc(slices.Clone(rightFields), func(f string) bool { _, ok := c.FieldRenames[f]; return !ok })
		}
		for _, f := range order {
			sel = append(sel, fmt.Sprintf("%s.%s AS %s", alias, quoteIdent(f), quoteIdent(c.FieldRenames[f])))
			if columns != nil {
				columns = append(columns, c.FieldRenames[f])
			}
		}
	}
	// The Rust renderer runs this layer as SQL text over the inner layer
	// (the template keeps the alias), as it does except's.
	prev := *q
	*q = sqlQuery{
		fromClause:   "(\n" + indentLines(renderSelect(&prev), "  ") + "\n) AS __l",
		fromTemplate: "(\n" + indentLines(sqlFromPlaceholder, "  ") + "\n) AS __l",
		comments:     prev.comments,
		columns:      columns,
		inner:        &prev,
		sqlOnly:      "join -as (a lookup)",
		selectExprs:  sel,
		joins:        joins,
	}
	return nil
}
