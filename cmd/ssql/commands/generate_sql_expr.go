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
		interval := fmt.Sprintf("INTERVAL '%d microseconds'", int64(every/time.Microsecond))
		fn := "time_bucket" // DuckDB
		if sqlDialectCur != dialectDuckDB {
			fn = "date_bin" // Postgres, DataFusion
		}
		return fmt.Sprintf("%s(%s, %s, TIMESTAMP '1970-01-01 00:00:00')", fn, interval, ts), nil
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
