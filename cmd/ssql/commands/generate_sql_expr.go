package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
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
	tree, err := parser.Parse(expression)
	if err != nil {
		return "", fmt.Errorf("expression %q: %w", expression, err)
	}
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

// exprCasts maps expr-lang conversion functions to SQL cast types.
var exprCasts = map[string]string{
	"int": "BIGINT", "float": "DOUBLE", "string": "VARCHAR",
}

func exprNodeToSQL(node ast.Node) (string, error) {
	switch n := node.(type) {
	case *ast.NilNode:
		return "NULL", nil
	case *ast.IdentifierNode:
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
	left, err := exprNodeToSQL(n.Left)
	if err != nil {
		return "", err
	}
	if n.Operator == "in" {
		if _, ok := n.Right.(*ast.ArrayNode); !ok {
			return "", fmt.Errorf("`in` needs a list literal for SQL translation")
		}
	}
	right, err := exprNodeToSQL(n.Right)
	if err != nil {
		return "", err
	}
	if op, ok := exprBinaryOps[n.Operator]; ok {
		return "(" + left + " " + op + " " + right + ")", nil
	}
	switch n.Operator {
	case "in":
		return "(" + left + " IN " + right + ")", nil
	case "contains":
		return "contains(" + left + ", " + right + ")", nil
	case "startsWith":
		return "starts_with(" + left + ", " + right + ")", nil
	case "endsWith":
		return "ends_with(" + left + ", " + right + ")", nil
	case "matches":
		return "regexp_matches(" + left + ", " + right + ")", nil
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
