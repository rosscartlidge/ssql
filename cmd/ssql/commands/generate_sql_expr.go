package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/rosscartlidge/ssql/v4"
)

// exprToSQL translates an ssql expression (expr-lang, as used by -if-expr and
// -set-expr) into an equivalent DuckDB SQL expression.
//
// Verbatim passthrough is NOT safe: expr-lang and SQL disagree on more than
// function names — `&&` is a SQL parse error, `||` means string concatenation,
// and "double quotes" quote identifiers, not strings. So this translates the
// subset of the language with a faithful SQL equivalent and fails loudly on
// anything else; silently emitting broken SQL is worse than refusing.
func exprToSQL(expression string) (string, error) {
	return exprToSQLParams(expression, nil)
}

// sqlExprParams are the -param bindings of the clause whose expression is
// being translated: an identifier that names one renders as a literal of
// its declared type instead of a column (DFC134 §5.3). Set for the duration
// of one exprToSQLParams call; the translator is a tree of plain functions.
var sqlExprParams map[string]ExprParam

// exprToSQLParams is exprToSQL with a clause's -param bindings.
func exprToSQLParams(expression string, params map[string]ExprParam) (string, error) {
	tree, err := parser.Parse(expression)
	if err != nil {
		return "", fmt.Errorf("expression %q: %w", expression, err)
	}
	sqlExprParams = params
	defer func() { sqlExprParams = nil }()
	sql, err := exprNodeToSQL(tree.Node)
	if err != nil {
		return "", fmt.Errorf("expression %q: %w — rewrite with SQL-translatable constructs, or use -if", expression, err)
	}
	return sql, nil
}

// exprBinaryOps maps expr-lang binary operators with a direct SQL spelling.
// Note `!=`→`<>` (portable), `&&`/`||`→AND/OR (SQL `||` is concat), and
// `^`/`**`→`**` (DuckDB power operator).
var exprBinaryOps = map[string]string{
	"==": "=", "!=": "<>",
	"<": "<", "<=": "<=", ">": ">", ">=": ">=",
	"and": "AND", "&&": "AND", "or": "OR", "||": "OR",
	"+": "+", "-": "-", "*": "*", "/": "/", "%": "%",
	"**": "**", "^": "**",
}

// exprFuncs maps expr-lang functions with a direct DuckDB equivalent.
var exprFuncs = map[string]string{
	"upper": "upper", "lower": "lower", "trim": "trim",
	"abs": "abs", "round": "round", "floor": "floor", "ceil": "ceil",
	"len":      "length",
	"contains": "contains", "hasPrefix": "starts_with", "hasSuffix": "ends_with",
	"min": "least", "max": "greatest",
}

// exprListFuncs maps expr-lang list functions to DuckDB's list_* family
// (DFC144 Level 1); argument order matches in every entry.
var exprListFuncs = map[string]string{
	"sort": "list_sort", "uniq": "list_distinct", "flatten": "flatten",
	"join": "array_to_string", "reverse": "list_reverse",
}

// exprCasts maps expr-lang conversion functions to SQL cast types.
var exprCasts = map[string]string{
	"int": "BIGINT", "float": "DOUBLE", "string": "VARCHAR",
}

func exprNodeToSQL(node ast.Node) (string, error) {
	switch n := node.(type) {
	case *ast.NilNode:
		return "NULL", nil
	case *ast.IdentifierNode:
		if p, ok := sqlExprParams[n.Value]; ok {
			return p.sqlLiteral(), nil
		}
		return quoteIdent(n.Value), nil
	case *ast.IntegerNode:
		return strconv.Itoa(n.Value), nil
	case *ast.FloatNode:
		return strconv.FormatFloat(n.Value, 'g', -1, 64), nil
	case *ast.BoolNode:
		if n.Value {
			return "TRUE", nil
		}
		return "FALSE", nil
	case *ast.StringNode:
		return "'" + escapeSQL(n.Value) + "'", nil
	case *ast.UnaryNode:
		return exprUnaryToSQL(n)
	case *ast.BinaryNode:
		return exprBinaryToSQL(n)
	case *ast.ConditionalNode:
		cond, err := exprNodeToSQL(n.Cond)
		if err != nil {
			return "", err
		}
		then, err := exprNodeToSQL(n.Exp1)
		if err != nil {
			return "", err
		}
		els, err := exprNodeToSQL(n.Exp2)
		if err != nil {
			return "", err
		}
		return "(CASE WHEN " + cond + " THEN " + then + " ELSE " + els + " END)", nil
	case *ast.ArrayNode:
		parts := make([]string, len(n.Nodes))
		for i, el := range n.Nodes {
			s, err := exprNodeToSQL(el)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "(" + strings.Join(parts, ", ") + ")", nil
	case *ast.CallNode:
		ident, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			return "", fmt.Errorf("%s has no SQL translation", exprNodeDesc(n.Callee))
		}
		return exprCallToSQL(ident.Value, n.Arguments)
	case *ast.MemberNode:
		return exprMemberToSQL(n)
	case *ast.BuiltinNode:
		return exprCallToSQL(n.Name, n.Arguments)
	}
	return "", fmt.Errorf("%s has no SQL translation", exprNodeDesc(node))
}

func exprUnaryToSQL(n *ast.UnaryNode) (string, error) {
	operand, err := exprNodeToSQL(n.Node)
	if err != nil {
		return "", err
	}
	switch n.Operator {
	case "not", "!":
		return "(NOT " + operand + ")", nil
	case "-":
		return "(-" + operand + ")", nil
	case "+":
		return operand, nil
	}
	return "", fmt.Errorf("operator %q has no SQL translation", n.Operator)
}

func exprBinaryToSQL(n *ast.BinaryNode) (string, error) {
	// `x == nil` / `x != nil`: SQL's `x = NULL` is never true (NULL, not
	// a boolean), so an absent-field test lowered that way selected no
	// rows while exec, record and typed returned the guarded ones (the
	// codelab's lag guard `prev_temp != nil`; found 2026-09-30). NULL is
	// how a SQL column expresses an absent record field, so the test is
	// IS [NOT] NULL, the same lowering `has(f)` already uses.
	if n.Operator == "==" || n.Operator == "!=" {
		_, leftNil := n.Left.(*ast.NilNode)
		_, rightNil := n.Right.(*ast.NilNode)
		if leftNil != rightNil {
			other := n.Right
			if rightNil {
				other = n.Left
			}
			operand, err := exprNodeToSQL(other)
			if err != nil {
				return "", err
			}
			if n.Operator == "==" {
				return "(" + operand + " IS NULL)", nil
			}
			return "(" + operand + " IS NOT NULL)", nil
		}
	}
	left, err := exprNodeToSQL(n.Left)
	if err != nil {
		return "", err
	}
	if n.Operator == "in" {
		if _, ok := n.Right.(*ast.ArrayNode); !ok {
			// `x in field`: the field is a nested list (DFC144 Level 1).
			if sqlDialectCur != dialectDuckDB {
				return "", dialectRefuse("`in` over a list field", "list_contains is DuckDB's")
			}
			right, err := exprNodeToSQL(n.Right)
			if err != nil {
				return "", err
			}
			return "list_contains(" + right + ", " + left + ")", nil
		}
	}
	right, err := exprNodeToSQL(n.Right)
	if err != nil {
		return "", err
	}
	if n.Operator == "/" {
		return sqlDivide(left, right), nil
	}
	if op, ok := exprBinaryOps[n.Operator]; ok {
		return "(" + left + " " + op + " " + right + ")", nil
	}
	switch n.Operator {
	case "in":
		return "(" + left + " IN " + right + ")", nil
	case "contains":
		return sqlContains(left, right), nil
	case "startsWith":
		return sqlStartsWith(left, right), nil
	case "endsWith":
		return sqlEndsWith(left, right), nil
	case "matches":
		return sqlRegexMatch(left, right), nil
	case "??":
		return "COALESCE(" + left + ", " + right + ")", nil
	}
	return "", fmt.Errorf("operator %q has no SQL translation", n.Operator)
}

func exprCallToSQL(name string, args []ast.Node) (string, error) {
	if sqlType, ok := exprCasts[name]; ok && len(args) == 1 {
		arg, err := exprNodeToSQL(args[0])
		if err != nil {
			return "", err
		}
		return "CAST(" + arg + " AS " + sqlType + ")", nil
	}
	switch name {
	case "has":
		// SQL columns always exist; record-field absence maps to NULL.
		if len(args) == 1 {
			field, err := exprFieldArg(args[0])
			if err != nil {
				return "", err
			}
			return "(" + field + " IS NOT NULL)", nil
		}
	case "getOr":
		if len(args) == 2 {
			field, err := exprFieldArg(args[0])
			if err != nil {
				return "", err
			}
			def, err := exprNodeToSQL(args[1])
			if err != nil {
				return "", err
			}
			return "COALESCE(" + field + ", " + def + ")", nil
		}
	}
	if name == "bucket" && len(args) == 2 {
		return bucketToSQL(args[0], args[1])
	}
	if sql, ok, err := timeFuncToSQL(name, args); ok {
		return sql, err
	}
	if sqlFunc, ok := exprListFuncs[name]; ok {
		// List functions over a nested value (DFC144 Level 1): DuckDB's
		// list_* family; the other dialects have no form for a value whose
		// type the translator does not know.
		if sqlDialectCur != dialectDuckDB {
			return "", dialectRefuse(fmt.Sprintf("%s() over a list", name), "list functions are DuckDB's")
		}
		parts := make([]string, len(args))
		for i, a := range args {
			s, err := exprNodeToSQL(a)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return sqlFunc + "(" + strings.Join(parts, ", ") + ")", nil
	}
	if sqlFunc, ok := exprFuncs[name]; ok {
		parts := make([]string, len(args))
		for i, a := range args {
			s, err := exprNodeToSQL(a)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return sqlFunc + "(" + strings.Join(parts, ", ") + ")", nil
	}
	return "", fmt.Errorf("function %q has no SQL translation", name)
}

// exprFieldArg renders a has("field")/getOr("field", …) field argument — a
// string literal naming a record field — as a SQL column reference.
func exprFieldArg(node ast.Node) (string, error) {
	switch n := node.(type) {
	case *ast.StringNode:
		return quoteIdent(n.Value), nil
	case *ast.IdentifierNode:
		return quoteIdent(n.Value), nil
	}
	return "", fmt.Errorf("field argument must be a literal field name")
}

func exprNodeDesc(node ast.Node) string {
	switch node.(type) {
	case *ast.MemberNode, *ast.ChainNode:
		return "member access"
	case *ast.PredicateNode:
		return "a predicate closure"
	case *ast.MapNode:
		return "a map literal"
	case *ast.SliceNode:
		return "slicing"
	case *ast.PointerNode:
		return "the # placeholder"
	}
	return fmt.Sprintf("syntax (%T)", node)
}

// bucketToSQL translates bucket(ts, "WIDTH") over a NUMERIC epoch column
// exactly as exprfn.BucketInt64 computes it: the unit is read from the
// value's magnitude (≥1e17 ns, ≥1e14 µs, ≥1e11 ms, else s — the same
// thresholds as DetectEpochUnitNanos), and the value is snapped down to a
// multiple of the width expressed in that unit. Per row in SQL where Go
// detects once per value — identical on any column of one unit. String
// timestamps (RFC 3339) have no translation here, as for resample: the
// Go lanes format them back into the input layout, which has no faithful
// SQL counterpart.
func bucketToSQL(tsNode, widthNode ast.Node) (string, error) {
	w, ok := widthNode.(*ast.StringNode)
	if !ok {
		return "", fmt.Errorf("bucket(): the width must be a duration literal like \"5m\"")
	}
	every, err := time.ParseDuration(w.Value)
	if err != nil || every <= 0 {
		return "", fmt.Errorf("bucket(): bad duration %q", w.Value)
	}
	ts, err := exprNodeToSQL(tsNode)
	if err != nil {
		return "", err
	}
	// A column an upstream `cast -type F time` made a TIMESTAMP (DFC128
	// D1): the engine's own bucketing, pinned to the Unix epoch so it lands
	// on the grid exprfn.SnapToBucket uses (the engines default to
	// 2000-01-03, which only coincides for widths that divide 10959 days).
	if name, ok := ssql.ExprFieldName(tsNode); ok && sqlTimeColumns[name] {
		if int64(every)%int64(time.Microsecond) != 0 {
			return "", fmt.Errorf("bucket(): a width finer than a microsecond has no SQL translation over a time column")
		}
		ts = sqlTimeColumnRef(name)
		interval := fmt.Sprintf("INTERVAL '%d microseconds'", int64(every/time.Microsecond))
		fn := "time_bucket" // DuckDB
		if sqlDialectCur != dialectDuckDB {
			fn = "date_bin" // Postgres, DataFusion
		}
		return fmt.Sprintf("%s(%s, %s, %s)", fn, interval, ts, sqlEpochOrigin()), nil
	}
	// width in each unit; an integer literal when it divides evenly so an
	// integer column stays integer (DuckDB's % keeps the operand type).
	unit := func(nanosPerUnit int64) string {
		if int64(every)%nanosPerUnit == 0 {
			return fmt.Sprintf("%d", int64(every)/nanosPerUnit)
		}
		return fmt.Sprintf("%g", float64(every)/float64(nanosPerUnit))
	}
	snap := func(n string) string { return "(" + ts + " - (" + ts + " % " + n + "))" }
	return "(CASE WHEN abs(" + ts + ") >= 1e17 THEN " + snap(unit(1)) +
		" WHEN abs(" + ts + ") >= 1e14 THEN " + snap(unit(1_000)) +
		" WHEN abs(" + ts + ") >= 1e11 THEN " + snap(unit(1_000_000)) +
		" ELSE " + snap(unit(1_000_000_000)) + " END)", nil
}

// exprMemberToSQL translates member access on a nested value (DFC144
// Level 1): `addr.city` on an object is struct_extract, `tags[0]` on a
// list is list_extract (expr-lang indexes from 0, DuckDB from 1; a
// negative index counts from the end in both). DuckDB reads a JSON file's
// nested values as STRUCT/LIST natively; the other dialects have no
// matching form for a value whose type the translator does not know, so
// they refuse.
func exprMemberToSQL(n *ast.MemberNode) (string, error) {
	if sqlDialectCur != dialectDuckDB {
		return "", dialectRefuse("member access on a nested value", "struct_extract/list_extract are DuckDB's")
	}
	base, err := exprNodeToSQL(n.Node)
	if err != nil {
		return "", err
	}
	switch p := n.Property.(type) {
	case *ast.StringNode:
		return "struct_extract(" + base + ", '" + strings.ReplaceAll(p.Value, "'", "''") + "')", nil
	case *ast.IntegerNode:
		return fmt.Sprintf("list_extract(%s, %d)", base, p.Value+1), nil
	case *ast.UnaryNode:
		// tags[-1]: expr-lang parses the index as a unary minus on a literal.
		if lit, ok := p.Node.(*ast.IntegerNode); ok && p.Operator == "-" {
			return fmt.Sprintf("list_extract(%s, %d)", base, -lit.Value), nil
		}
	}
	return "", fmt.Errorf("member access with a computed key has no SQL translation")
}

// --- calendar functions (DFC147 §4, §8.3) ----------------------------
//
// A time in SQL is an instant (sqlTimestampType). Reading its calendar
// needs a zone, named EXPLICITLY in the emitted text — the pipeline's
// (sqlZone) or the one a tz() gave the value — never the session's:
// `(e AT TIME ZONE 'Z')` is the local wall clock as a naive TIMESTAMP on
// DuckDB and Postgres (the same construct the other way turns a wall
// clock back into an instant), so parts, truncation and calendar
// addition are read on the wall clock and the result comes back as an
// instant. DataFusion reads a TIMESTAMP as UTC and has no portable
// zone conversion here: it serves a UTC pipeline and refuses otherwise.

// timeFuncToSQL translates the calendar functions; ok=false when name is
// not one of them.
func timeFuncToSQL(name string, args []ast.Node) (string, bool, error) {
	isPart := false
	for _, p := range ssql.TimeParts {
		if p == name {
			isPart = true
		}
	}
	switch {
	case isPart, name == "date_trunc", name == "date_add", name == "date_diff", name == "tz", name == "now", name == "date" && len(args) == 1,
		name == "strftime", name == "strptime", name == "date" && len(args) == 2:
	default:
		return "", false, nil
	}
	sqlNeedsZone = true
	sql, err := timeFuncSQL(name, args)
	return sql, true, err
}

// sqlTimeExpr is a translated time-valued expression: the instant and the
// zone its calendar is read in.
type sqlTimeExpr struct {
	instant string
	zone    string
}

func (t sqlTimeExpr) wall() string { return "(" + t.instant + " AT TIME ZONE '" + escapeSQL(t.zone) + "')" }

func sqlInstantOfWall(wall, zone string) string {
	return "(" + wall + " AT TIME ZONE '" + escapeSQL(zone) + "')"
}

// sqlTimeArg translates a time operand: a column the assembly knows is a
// time, a tz(), or another calendar function; anything else is refused
// with the cast remedy (a VARCHAR column has no calendar).
func sqlTimeArg(fn string, node ast.Node) (sqlTimeExpr, error) {
	zone := sqlZone
	if zone == "" {
		return sqlTimeExpr{}, fmt.Errorf("%s(): the pipeline's time zone is unknown — set -tz on the source or TZ", fn)
	}
	if name, ok := ssql.ExprFieldName(node); ok {
		if _, isParam := sqlExprParams[name]; !isParam {
			if !sqlTimeColumns[name] {
				return sqlTimeExpr{}, fmt.Errorf("%s(%s): a time column is needed — cast -type %s time (or -type on the source)", fn, name, name)
			}
			return sqlTimeExpr{instant: sqlTimeColumnRef(name), zone: zone}, nil
		}
		if p := sqlExprParams[name]; p.Type == ssql.FieldTypeTime {
			return sqlTimeExpr{instant: p.sqlLiteral(), zone: zone}, nil
		}
		return sqlTimeExpr{}, fmt.Errorf("%s(%s): the parameter is not a time", fn, name)
	}
	var callee string
	var cargs []ast.Node
	switch n := node.(type) {
	case *ast.CallNode:
		if id, ok := n.Callee.(*ast.IdentifierNode); ok {
			callee, cargs = id.Value, n.Arguments
		}
	case *ast.BuiltinNode:
		callee, cargs = n.Name, n.Arguments
	}
	switch callee {
	case "tz":
		if len(cargs) != 2 {
			return sqlTimeExpr{}, fmt.Errorf("tz(ts, zone) takes two arguments")
		}
		z, ok := cargs[1].(*ast.StringNode)
		if !ok {
			return sqlTimeExpr{}, fmt.Errorf("tz(): the zone must be a string literal")
		}
		if _, err := time.LoadLocation(z.Value); err != nil {
			return sqlTimeExpr{}, fmt.Errorf("tz: %v", err)
		}
		inner, err := sqlTimeArg("tz", cargs[0])
		if err != nil {
			return sqlTimeExpr{}, err
		}
		return sqlTimeExpr{instant: inner.instant, zone: z.Value}, nil
	case "date_trunc", "date_add", "now", "date", "bucket":
		sql, err := timeFuncSQLZoned(callee, cargs)
		if err != nil {
			return sqlTimeExpr{}, err
		}
		return sql, nil
	}
	return sqlTimeExpr{}, fmt.Errorf("%s(): the argument is not a time column or time expression (cast -type F time)", fn)
}

// timeFuncSQL renders one calendar function call as SQL text.
func timeFuncSQL(name string, args []ast.Node) (string, error) {
	switch name {
	case "date_trunc", "date_add", "now", "date", "strptime":
		t, err := timeFuncSQLZoned(name, args)
		if err != nil {
			return "", err
		}
		return t.instant, nil
	case "strftime":
		if len(args) != 2 {
			return "", fmt.Errorf("strftime(ts, pattern) takes two arguments")
		}
		pat, ok := args[1].(*ast.StringNode)
		if !ok {
			return "", fmt.Errorf("strftime(): the pattern must be a string literal")
		}
		if _, _, err := ssql.StrftimeLayout(pat.Value); err != nil {
			return "", err
		}
		t, err := sqlTimeArg("strftime", args[0])
		if err != nil {
			return "", err
		}
		switch sqlDialectCur {
		case dialectPostgres:
			pg, err := ssql.PgToChar(pat.Value)
			if err != nil {
				return "", err
			}
			return "to_char(" + t.wall() + ", '" + escapeSQL(pg) + "')", nil
		case dialectDataFusion:
			if t.zone != "UTC" {
				return "", dialectRefuse("strftime() in zone "+t.zone, "DataFusion reads timestamps as UTC; run the pipeline with -tz UTC")
			}
			return "to_char(" + t.instant + ", '" + escapeSQL(pat.Value) + "')", nil
		}
		return "strftime(" + t.wall() + ", '" + escapeSQL(pat.Value) + "')", nil
	case "tz":
		t, err := sqlTimeArg("tz", &ast.CallNode{Callee: &ast.IdentifierNode{Value: "tz"}, Arguments: args})
		if err != nil {
			return "", err
		}
		return t.instant, nil // the same instant; the zone travels to the enclosing function
	case "date_diff":
		return dateDiffSQL(args)
	}
	// a part
	if len(args) != 1 {
		return "", fmt.Errorf("%s() takes one argument", name)
	}
	t, err := sqlTimeArg(name, args[0])
	if err != nil {
		return "", err
	}
	if name == "epoch" {
		switch sqlDialectCur {
		case dialectPostgres:
			return "CAST(EXTRACT(EPOCH FROM " + t.instant + ") AS BIGINT)", nil
		case dialectDataFusion:
			return "CAST(date_part('epoch', " + t.instant + ") AS BIGINT)", nil
		}
		return "CAST(epoch(" + t.instant + ") AS BIGINT)", nil
	}
	switch sqlDialectCur {
	case dialectDuckDB:
		fn := map[string]string{"dow": "dayofweek", "doy": "dayofyear"}[name]
		if fn == "" {
			fn = name
		}
		return fn + "(" + t.wall() + ")", nil
	case dialectPostgres:
		if name == "second" {
			return "CAST(FLOOR(EXTRACT(second FROM " + t.wall() + ")) AS BIGINT)", nil
		}
		return "CAST(EXTRACT(" + name + " FROM " + t.wall() + ") AS BIGINT)", nil
	default: // DataFusion: UTC pipelines only
		if t.zone != "UTC" {
			return "", dialectRefuse(name+"() in zone "+t.zone, "DataFusion reads timestamps as UTC; run the pipeline with -tz UTC")
		}
		return "CAST(date_part('" + name + "', " + t.instant + ") AS BIGINT)", nil
	}
}

// timeFuncSQLZoned renders the time-valued functions, keeping the zone the
// calendar was read in so an enclosing part reads the same one (as the VM
// does: TruncTime returns a time in that location).
func timeFuncSQLZoned(name string, args []ast.Node) (sqlTimeExpr, error) {
	switch name {
	case "now":
		if len(args) != 0 {
			return sqlTimeExpr{}, fmt.Errorf("now() takes no arguments")
		}
		return sqlTimeExpr{instant: "now()", zone: sqlZone}, nil
	case "date":
		if len(args) == 2 {
			return timeFuncSQLZoned("strptime", args)
		}
		arg, err := exprNodeToSQL(args[0])
		if err != nil {
			return sqlTimeExpr{}, err
		}
		return sqlTimeExpr{instant: "CAST(" + arg + " AS " + sqlTimestampType() + ")", zone: sqlZone}, nil
	case "strptime":
		if len(args) != 2 {
			return sqlTimeExpr{}, fmt.Errorf("strptime(text, pattern) takes two arguments")
		}
		pat, ok := args[1].(*ast.StringNode)
		if !ok {
			return sqlTimeExpr{}, fmt.Errorf("strptime(): the pattern must be a string literal")
		}
		if _, err := ssql.TimeLayout(pat.Value); err != nil {
			return sqlTimeExpr{}, err
		}
		if !ssql.IsStrftime(pat.Value) {
			return sqlTimeExpr{}, fmt.Errorf("strptime(): a Go layout has no SQL form — write the pattern as strftime (%%d/%%m/%%Y)")
		}
		arg, err := exprNodeToSQL(args[0])
		if err != nil {
			return sqlTimeExpr{}, err
		}
		return sqlTimeExpr{instant: sqlStrptime(arg, pat.Value), zone: sqlZone}, nil
	case "bucket":
		if len(args) != 2 {
			return sqlTimeExpr{}, fmt.Errorf("bucket() takes (timestamp, duration)")
		}
		t, err := sqlTimeArg("bucket", args[0])
		if err != nil {
			return sqlTimeExpr{}, err
		}
		sql, err := bucketToSQL(args[0], args[1])
		if err != nil {
			return sqlTimeExpr{}, err
		}
		return sqlTimeExpr{instant: sql, zone: t.zone}, nil
	case "date_trunc":
		if len(args) != 2 {
			return sqlTimeExpr{}, fmt.Errorf("date_trunc(ts, unit) takes two arguments")
		}
		unit, err := sqlCalendarUnit("date_trunc", args[1])
		if err != nil {
			return sqlTimeExpr{}, err
		}
		t, err := sqlTimeArg("date_trunc", args[0])
		if err != nil {
			return sqlTimeExpr{}, err
		}
		if sqlDialectCur == dialectDataFusion {
			if t.zone != "UTC" {
				return sqlTimeExpr{}, dialectRefuse("date_trunc() in zone "+t.zone, "DataFusion reads timestamps as UTC; run the pipeline with -tz UTC")
			}
			return sqlTimeExpr{instant: "date_trunc('" + unit + "', " + t.instant + ")", zone: t.zone}, nil
		}
		return sqlTimeExpr{instant: sqlInstantOfWall("date_trunc('"+unit+"', "+t.wall()+")", t.zone), zone: t.zone}, nil
	case "date_add":
		if len(args) != 3 {
			return sqlTimeExpr{}, fmt.Errorf("date_add(ts, n, unit) takes three arguments")
		}
		unit, err := sqlCalendarUnit("date_add", args[2])
		if err != nil {
			return sqlTimeExpr{}, err
		}
		t, err := sqlTimeArg("date_add", args[0])
		if err != nil {
			return sqlTimeExpr{}, err
		}
		n, err := exprNodeToSQL(args[1])
		if err != nil {
			return sqlTimeExpr{}, err
		}
		interval, err := sqlInterval(n, unit)
		if err != nil {
			return sqlTimeExpr{}, err
		}
		switch unit {
		case "hour", "minute", "second": // absolute: on the instant
			return sqlTimeExpr{instant: "(" + t.instant + " + " + interval + ")", zone: t.zone}, nil
		}
		if sqlDialectCur == dialectDataFusion {
			if t.zone != "UTC" {
				return sqlTimeExpr{}, dialectRefuse("date_add() in zone "+t.zone, "DataFusion reads timestamps as UTC; run the pipeline with -tz UTC")
			}
			return sqlTimeExpr{instant: "(" + t.instant + " + " + interval + ")", zone: t.zone}, nil
		}
		return sqlTimeExpr{instant: sqlInstantOfWall("("+t.wall()+" + "+interval+")", t.zone), zone: t.zone}, nil
	}
	return sqlTimeExpr{}, fmt.Errorf("%s has no SQL translation", name)
}

// sqlStrptime parses text with a strftime pattern as an instant: the text
// is a wall clock in the pipeline zone (ssql.ParseStrftime), so the naive
// result is placed there with AT TIME ZONE.
func sqlStrptime(text, pattern string) string {
	switch sqlDialectCur {
	case dialectPostgres:
		pg, err := ssql.PgToChar(pattern)
		if err != nil {
			pg = pattern // PgToChar refuses only %z and a literal quote; to_timestamp will say so
		}
		// to_timestamp reads the text in the session zone (UTC): take the
		// wall clock back and place it in the pipeline zone.
		return sqlInstantOfWall("(to_timestamp("+text+", '"+escapeSQL(pg)+"') AT TIME ZONE 'UTC')", sqlZone)
	case dialectDataFusion:
		return "to_timestamp(" + text + ", '" + escapeSQL(pattern) + "')" // UTC pipelines only (callers refuse otherwise)
	}
	return sqlInstantOfWall("strptime("+text+", '"+escapeSQL(pattern)+"')", sqlZone)
}

// sqlCalendarUnit reads a literal unit argument.
func sqlCalendarUnit(fn string, node ast.Node) (string, error) {
	s, ok := node.(*ast.StringNode)
	if !ok {
		return "", fmt.Errorf("%s(): the unit must be a string literal", fn)
	}
	if !ssql.ValidCalendarUnit(s.Value) {
		return "", fmt.Errorf("%s: unit %q is not one of %s", fn, s.Value, strings.Join(ssql.CalendarUnits, ", "))
	}
	return s.Value, nil
}

// sqlInterval is n units as an interval: a literal count inline, an
// expression through the engine's constructor.
func sqlInterval(n, unit string) (string, error) {
	plural := map[string]string{"year": "years", "quarter": "months", "month": "months", "week": "weeks", "day": "days", "hour": "hours", "minute": "mins", "second": "secs"}[unit]
	if unit == "quarter" {
		n = "(" + n + ") * 3"
	}
	switch sqlDialectCur {
	case dialectPostgres:
		return "make_interval(" + plural + " => " + n + ")", nil
	case dialectDataFusion:
		if _, err := strconv.Atoi(n); err != nil {
			return "", dialectRefuse("date_add() with a computed count", "DataFusion intervals are literals")
		}
		return "INTERVAL '" + n + " " + unit + "'", nil
	}
	du := strings.ToUpper(unit)
	if unit == "quarter" {
		du = "MONTH"
	}
	return "INTERVAL (" + n + ") " + du, nil
}

// dateDiffSQL counts unit boundaries crossed (ssql.DiffCalendar): DuckDB's
// date_diff over the wall clocks; Postgres by the unit's own arithmetic.
func dateDiffSQL(args []ast.Node) (string, error) {
	if len(args) != 3 {
		return "", fmt.Errorf("date_diff(unit, start, end) takes three arguments")
	}
	unit, err := sqlCalendarUnit("date_diff", args[0])
	if err != nil {
		return "", err
	}
	a, err := sqlTimeArg("date_diff", args[1])
	if err != nil {
		return "", err
	}
	b, err := sqlTimeArg("date_diff", args[2])
	if err != nil {
		return "", err
	}
	// Both read in the start's zone, as DiffCalendar does.
	la, lb := a.wall(), sqlTimeExpr{instant: b.instant, zone: a.zone}.wall()
	switch sqlDialectCur {
	case dialectDuckDB:
		return "date_diff('" + unit + "', " + la + ", " + lb + ")", nil
	case dialectPostgres:
		ex := func(part, w string) string { return "EXTRACT(" + part + " FROM " + w + ")" }
		switch unit {
		case "year":
			return "CAST(" + ex("year", lb) + " - " + ex("year", la) + " AS BIGINT)", nil
		case "quarter":
			return "CAST((" + ex("year", lb) + "*4 + " + ex("quarter", lb) + ") - (" + ex("year", la) + "*4 + " + ex("quarter", la) + ") AS BIGINT)", nil
		case "month":
			return "CAST((" + ex("year", lb) + "*12 + " + ex("month", lb) + ") - (" + ex("year", la) + "*12 + " + ex("month", la) + ") AS BIGINT)", nil
		case "week":
			return "CAST((CAST(" + lb + " AS DATE) - CAST(" + la + " AS DATE)) / 7 AS BIGINT)", nil
		case "day":
			return "CAST(CAST(" + lb + " AS DATE) - CAST(" + la + " AS DATE) AS BIGINT)", nil
		}
		secs := map[string]string{"hour": "3600", "minute": "60", "second": "1"}[unit]
		return "CAST(FLOOR(EXTRACT(EPOCH FROM (date_trunc('" + unit + "', " + lb + ") - date_trunc('" + unit + "', " + la + "))) / " + secs + ") AS BIGINT)", nil
	}
	return "", dialectRefuse("date_diff()", "DataFusion has no date_diff")
}

// exprResultIsTime reports whether an expression's value is a time: a
// time column, a parameter of type time, or a time-valued calendar
// function at the top — so a -set-expr column can be typed for later
// stages.
func exprResultIsTime(expression string) bool {
	tree, err := parser.Parse(expression)
	if err != nil {
		return false
	}
	if name, ok := ssql.ExprFieldName(tree.Node); ok {
		return sqlTimeColumns[name]
	}
	var callee string
	switch n := tree.Node.(type) {
	case *ast.CallNode:
		if id, ok := n.Callee.(*ast.IdentifierNode); ok {
			callee = id.Value
		}
		if callee == "date" && len(n.Arguments) != 1 {
			return false
		}
	case *ast.BuiltinNode:
		callee = n.Name
		if callee == "date" && len(n.Arguments) != 1 {
			return false
		}
	}
	switch callee {
	case "date_trunc", "date_add", "tz", "now", "date", "strptime":
		return true
	case "bucket": // a time in, a time out
		if n, ok := tree.Node.(*ast.CallNode); ok && len(n.Arguments) == 2 {
			if name, ok := ssql.ExprFieldName(n.Arguments[0]); ok {
				return sqlTimeColumns[name]
			}
		}
	}
	return false
}
