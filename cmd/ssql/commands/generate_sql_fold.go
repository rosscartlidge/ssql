package commands

import (
	"fmt"
	"slices"
	"strings"

	"github.com/expr-lang/expr/ast"

	"github.com/rosscartlidge/ssql/v4"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// A fold (`-stream-expr INIT EVERY FINAL NAME`) in SQL is a recursive CTE
// (DFC156): each partition is aggregated once into an ordered list of its
// rows, the anchor is INIT at index 0, each recursive step applies EVERY
// to the state columns and the next row of the list, and FINAL is read
// per row (window, over the default frame) or at the last index
// (group-by). The shape is linear in the partition's length; the engine
// still pays 50–200 µs per step, so it serves correctness and short
// partitions, and a long recurrence belongs in ssql (§4). DuckDB's list
// form only, for now (§3).
//
//	(WITH RECURSIVE __src AS (SELECT * FROM <query so far>),
//	 __parts AS (SELECT s."dept" AS __p0, list(s ORDER BY s."salary") AS __xs FROM __src s GROUP BY s."dept"),
//	 __fold(__p0, __i, __xs, __st0_s) AS (
//	   SELECT __p0, 0, __xs, CAST(0 AS BIGINT) FROM __parts
//	   UNION ALL
//	   SELECT __p0, __i + 1, __xs, __st0_s + __xs[__i + 1]."salary" FROM __fold WHERE __i < len(__xs))
//	 SELECT unnest(__xs[__i]), __st0_s AS "run" FROM __fold WHERE __i >= 1)

// sqlFoldState is one key of a fold's state as a CTE column.
type sqlFoldState struct {
	key, col, sqlType string
}

// sqlFoldPlan is one fold lowered for SQL.
type sqlFoldPlan struct {
	spec   streamExprSpec
	states []sqlFoldState
	init   []string // INIT value per state, cast to the state's type
	every  []string // EVERY value per state, over state columns and the next row
	final  string   // FINAL over state columns
	kind   string   // the result column's kind for sqlColumnKinds
}

// foldSQLType is the SQL type of a state the typed lowering typed.
func foldSQLType(t exprGoType, scale int) (string, error) {
	switch t {
	case exprGoDecimal:
		return sqlDecimalType(scale), nil
	case exprGoInt:
		return "BIGINT", nil
	case exprGoFloat:
		return sqlFloatType(), nil
	case exprGoInt128:
		return sqlInt128Type(), nil
	case exprGoString:
		return sqlStringType(), nil
	case exprGoBool:
		return "BOOLEAN", nil
	}
	return "", fmt.Errorf("a %s state has no SQL form", t)
}

// foldKind is the sqlColumnKinds kind of a fold's result type.
func foldKind(t exprGoType, scale int) string {
	switch t {
	case exprGoDecimal:
		return ssql.DecimalType(scale).String()
	case exprGoInt:
		return "int"
	case exprGoFloat:
		return "float"
	case exprGoInt128:
		return "int128"
	case exprGoBool:
		return "bool"
	}
	return "string"
}

// kindGoType is the typed-lowering Go type of a sqlColumnKinds kind.
func kindGoType(kind string) string {
	if base, _ := ssql.ParseWireType(kind); base == "decimal" {
		return "ssql.Decimal" // the scale: kindScale
	}
	switch kind {
	case "int":
		return "int64"
	case "int128":
		return "ssql.Int128"
	case "float":
		return "float64"
	case "bool":
		return "bool"
	case "time":
		return "time.Time"
	}
	return "string"
}

// foldSchema is the typed lowering's view of the rows a fold reads: the
// columns EVERY references (an identifier that is not a state key), each
// typed from sqlColumnKinds. Only those: a row's other columns — a
// `-set-expr` result the assembler could not type, say — are no concern
// of the fold's. A referenced column with no known kind is a refusal:
// the state's type would be a guess.
func foldSchema(q *sqlQuery, specs []streamExprSpec) (*lib.TypedSchema, error) {
	var cols []string
	for _, spec := range specs {
		keys := map[string]bool{}
		if pairs, err := exprMapPairs(spec.initExpr); err == nil {
			for _, p := range pairs {
				keys[p.key] = true
			}
		}
		pairs, err := exprMapPairs(spec.everyExpr)
		if err != nil {
			return nil, err
		}
		for _, p := range pairs {
			node := p.value
			ast.Walk(&node, &identCollector{seen: func(name string) {
				// A name that is both a column and a state key is the
				// column (the row shadows the state); a state key that is
				// not a known column is the state.
				if keys[name] && (q.columns == nil || !slices.Contains(q.columns, name)) {
					return
				}
				if !slices.Contains(cols, name) {
					cols = append(cols, name)
				}
			}})
		}
	}
	s := &lib.TypedSchema{TypeName: "Row"}
	for _, c := range cols {
		kind := sqlColumnKinds[c]
		if kind == "" {
			return nil, fmt.Errorf("column %q has no known type here (cast -type %s TYPE before the fold, or declare it with -type on the from stage)", c, c)
		}
		_, scale := ssql.ParseWireType(kind)
		s.Fields = append(s.Fields, lib.TypedSchemaField{Name: c, GoName: lib.GoNameFromColumn(c), GoType: kindGoType(kind), Scale: max(scale, 0)})
	}
	return s, nil
}

// identCollector reports every identifier in an expression tree.
type identCollector struct{ seen func(string) }

func (c *identCollector) Visit(node *ast.Node) {
	if id, ok := (*node).(*ast.IdentifierNode); ok {
		c.seen(id.Value)
	}
}

// lowerSQLFold types the fold through the typed lowering (INIT's literal
// types widened by EVERY, DFC129/expr_stream.go) and renders INIT, EVERY
// and FINAL as SQL over the CTE's state columns and the next list row.
func lowerSQLFold(q *sqlQuery, spec streamExprSpec, idx int) (sqlFoldPlan, error) {
	plan := sqlFoldPlan{spec: spec}
	schema, err := foldSchema(q, []streamExprSpec{spec})
	if err != nil {
		return plan, fmt.Errorf("-stream-expr %s: %w", spec.result, err)
	}
	typed, err := lowerStreamAggAnyFinal(spec, schema, idx) // a string or bool FINAL is a column like any other in SQL
	if err != nil {
		return plan, fmt.Errorf("-stream-expr %s: %w (no SQL translation)", spec.result, err)
	}
	stateCol := map[string]string{}
	for _, st := range typed.States {
		t, err := foldSQLType(st.Type, st.Scale)
		if err != nil {
			return plan, fmt.Errorf("-stream-expr %s: state %q: %w", spec.result, st.Key, err)
		}
		col := fmt.Sprintf("__st%d_%s", idx, st.Key)
		stateCol[st.Key] = col
		plan.states = append(plan.states, sqlFoldState{key: st.Key, col: col, sqlType: t})
	}
	isColumn := func(name string) bool {
		if q.columns != nil {
			return slices.Contains(q.columns, name)
		}
		return stateCol[name] == "" // unknown columns: a name that is not a state key is a column
	}
	// INIT: literals (the lowering refused anything else), cast to the
	// state's type so the recursion's UNION ALL agrees on it.
	initPairs, _ := exprMapPairs(spec.initExpr)
	initByKey := map[string]ast.Node{}
	for _, p := range initPairs {
		initByKey[p.key] = p.value
	}
	everyPairs, _ := exprMapPairs(spec.everyExpr)
	everyByKey := map[string]ast.Node{}
	for _, p := range everyPairs {
		everyByKey[p.key] = p.value
	}
	const nextRow = "__xs[__i + 1]"
	for _, st := range plan.states {
		initSQL, err := exprNodeToSQLWith(initByKey[st.key], func(name string) (string, error) {
			return "", fmt.Errorf("init value of %q names %q, which is not a literal", st.key, name)
		})
		if err != nil {
			return plan, fmt.Errorf("-stream-expr %s: %w", spec.result, err)
		}
		if strings.HasPrefix(initSQL, "CAST(") && strings.HasSuffix(initSQL, " AS "+st.sqlType+")") {
			plan.init = append(plan.init, initSQL) // int128(0) is already the cast
		} else {
			plan.init = append(plan.init, "CAST("+initSQL+" AS "+st.sqlType+")")
		}
		everySQL, err := exprNodeToSQLWith(everyByKey[st.key], func(name string) (string, error) {
			if isColumn(name) { // the row shadows the state, as the VM's env does
				return nextRow + "." + quoteIdent(name), nil
			}
			if col := stateCol[name]; col != "" {
				return col, nil
			}
			return "", fmt.Errorf("every value of %q names %q, which is neither a column nor a state key", st.key, name)
		})
		if err != nil {
			return plan, fmt.Errorf("-stream-expr %s: %w", spec.result, err)
		}
		plan.every = append(plan.every, everySQL)
	}
	finalSQL, err := exprToSQLWith(spec.finalExpr, func(name string) (string, error) {
		if col := stateCol[name]; col != "" {
			return col, nil
		}
		return "", fmt.Errorf("final expression names %q, which is not a state key", name)
	})
	if err != nil {
		return plan, fmt.Errorf("-stream-expr %s: %w", spec.result, err)
	}
	plan.final = finalSQL
	plan.kind = foldKind(typed.FinalType, typed.FinalScale)
	return plan, nil
}

// foldOrderTerms is the ORDER BY of the partition's list: the window's
// -order (with -desc), else a range source's column, else none — SQL has
// no arrival order.
func foldOrderTerms(q *sqlQuery, orderBy []string, desc bool) []string {
	var terms []string
	for _, f := range orderBy {
		t := "s." + quoteIdent(f)
		if desc {
			t += " DESC"
		}
		terms = append(terms, t)
	}
	if len(terms) == 0 && q.rangeColumn != "" && (q.columns == nil || slices.Contains(q.columns, q.rangeColumn)) {
		terms = append(terms, "s."+quoteIdent(q.rangeColumn))
	}
	return terms
}

// buildFoldCTE renders the recursive CTE over src. partition names the
// GROUP BY columns of the lists; order the list's ORDER BY terms (over
// alias s); carried are per-partition aggregates computed beside the list
// and carried through the recursion (group-by's other -flags). perRow
// emits one output row per source row with every column (window), else one
// per partition with the keys, the carried aggregates and the folds'
// FINAL at the last index (group-by).
func buildFoldCTE(src string, partition []string, order []string, carried []sqlAgg, plans []sqlFoldPlan, perRow bool) string {
	var pcols, pdefs, carry, cdefs, scols, sinit, sstep []string
	for i, f := range partition {
		pcols = append(pcols, fmt.Sprintf("__p%d", i))
		pdefs = append(pdefs, fmt.Sprintf("s.%s AS __p%d", quoteIdent(f), i))
	}
	for i, a := range carried {
		carry = append(carry, fmt.Sprintf("__a%d", i))
		cdefs = append(cdefs, fmt.Sprintf("%s AS __a%d", a.expr, i))
	}
	for _, p := range plans {
		for j, st := range p.states {
			scols = append(scols, st.col)
			sinit = append(sinit, p.init[j])
			sstep = append(sstep, p.every[j])
		}
	}
	listExpr := "list(s)"
	if len(order) > 0 {
		listExpr = "list(s ORDER BY " + strings.Join(order, ", ") + ")"
	}
	partsSel := append(append(slices.Clone(pdefs), listExpr+" AS __xs"), cdefs...)
	parts := "SELECT " + strings.Join(partsSel, ", ") + " FROM __src s"
	if len(partition) > 0 {
		var by []string
		for _, f := range partition {
			by = append(by, "s."+quoteIdent(f))
		}
		parts += " GROUP BY " + strings.Join(by, ", ")
	}
	fixed := append(slices.Clone(pcols), "__i", "__xs")
	fixed = append(fixed, carry...)
	anchorSel := append(append(slices.Clone(pcols), "0", "__xs"), carry...)
	stepSel := append(append(slices.Clone(pcols), "__i + 1", "__xs"), carry...)
	var sb strings.Builder
	sb.WriteString("(WITH RECURSIVE __src AS (SELECT * FROM " + src + "),\n")
	sb.WriteString(" __parts AS (" + parts + "),\n")
	sb.WriteString(" __fold(" + strings.Join(append(fixed, scols...), ", ") + ") AS (\n")
	sb.WriteString("   SELECT " + strings.Join(append(anchorSel, sinit...), ", ") + " FROM __parts\n")
	sb.WriteString("   UNION ALL\n")
	sb.WriteString("   SELECT " + strings.Join(append(stepSel, sstep...), ", ") + " FROM __fold WHERE __i < len(__xs))\n")
	var out []string
	if perRow {
		out = append(out, "unnest(__xs[__i])")
	} else {
		for i, f := range partition {
			out = append(out, fmt.Sprintf("__p%d AS %s", i, quoteIdent(f)))
		}
		for i, a := range carried {
			out = append(out, fmt.Sprintf("__a%d AS %s", i, quoteIdent(a.name)))
		}
	}
	for _, p := range plans {
		out = append(out, p.final+" AS "+quoteIdent(p.spec.result))
	}
	where := "__i >= 1"
	if !perRow {
		where = "__i = len(__xs)"
	}
	sb.WriteString(" SELECT " + strings.Join(out, ", ") + " FROM __fold WHERE " + where + ")")
	return sb.String()
}

// translateWindowFolds replaces the query with the fold CTE for one window
// clause over the default frame (the caller has checked the frame).
func translateWindowFolds(q *sqlQuery, partition, orderBy []string, desc bool, specs []streamExprSpec) error {
	if sqlDialectCur != dialectDuckDB {
		return dialectRefuse("window -stream-expr", "the fold's recursive CTE is DuckDB's list form for now (DFC156 §3)")
	}
	order := foldOrderTerms(q, orderBy, desc)
	if len(order) == 0 {
		return windowNeedsOrder("-stream-expr", "the fold runs over the rows as they arrive")
	}
	var plans []sqlFoldPlan
	for i, spec := range specs {
		p, err := lowerSQLFold(q, spec, i)
		if err != nil {
			return fmt.Errorf("window %w", err)
		}
		plans = append(plans, p)
	}
	if q.hasClauses() {
		wrapAsSubquery(q)
	}
	if q.fromClause == "" {
		return fmt.Errorf("window -stream-expr: no source to translate")
	}
	cte := buildFoldCTE(q.fromClause, partition, order, nil, plans, true)
	q.fromClause = sqlSubquery(cte)
	for _, p := range plans {
		sqlColumnKinds[p.spec.result] = p.kind
	}
	return nil
}

// translateGroupByFolds renders group-by with folds: the group keys are
// the partition, the registry aggregates ride beside the list, FINAL is
// read at the last index. The fold's row order is the input order, which
// SQL sees only as an earlier sort's ORDER BY or a range source's column.
func translateGroupByFolds(q *sqlQuery, fields []string, aggs []sqlAgg, specs []streamExprSpec) error {
	if sqlDialectCur != dialectDuckDB {
		return dialectRefuse("group-by -stream-expr", "the fold's recursive CTE is DuckDB's list form for now (DFC156 §3)")
	}
	var order []string
	pending := q.orderBy // an earlier sort: the rows reach the fold in that order
	if len(pending) == 0 {
		pending = q.wrappedOrder // the same sort, already materialised behind a projection or limit
	}
	for _, t := range pending {
		if strings.HasPrefix(t, `"`) {
			t = "s." + t
		}
		order = append(order, t)
	}
	if len(order) == 0 {
		order = foldOrderTerms(q, nil, false)
	}
	if len(order) == 0 {
		return fmt.Errorf("group-by -stream-expr folds each group's rows in their input order, which SQL cannot see: sort first (ssql sort FIELD), or use generate go")
	}
	var plans []sqlFoldPlan
	for i, spec := range specs {
		p, err := lowerSQLFold(q, spec, i)
		if err != nil {
			return fmt.Errorf("group-by %w", err)
		}
		plans = append(plans, p)
	}
	if q.hasClauses() {
		wrapAsSubquery(q)
	}
	if q.fromClause == "" {
		return fmt.Errorf("group-by -stream-expr: no source to translate")
	}
	cte := buildFoldCTE(q.fromClause, fields, order, aggs, plans, false)
	q.fromClause = sqlSubquery(cte)
	for _, p := range plans {
		sqlColumnKinds[p.spec.result] = p.kind
	}
	return nil
}
