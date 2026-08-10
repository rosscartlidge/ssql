package commands

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// The ONE lowering for `FIELD OP VALUE` flag conditions (convergence Phase
// B, doc/research/flag-expr-convergence.md). Before this file the operator
// semantics were implemented independently in typedWhereCondition (typed
// where + update), generateCondition (record where) and
// generateConditionCode (record update) — the duplication class behind the
// six-site +if negation bug and the three divergences Phase A's metamorphic
// gate caught on its first run. Those three functions are now thin wrappers:
// each resolves the FIELD for its backend (that part legitimately differs —
// struct access vs typed GetOr vs heuristic GetOr) and delegates the
// OPERATOR to condOpToExprGo, which reuses the expression walker's own
// emissions (exprCompare, the strings.* forms, the hoisted-regex machinery).
// exec's applyOperator is deliberately NOT converged: it is the differential
// oracle the metamorphic gate compares everything against.
//
// condOpToExprGo lowers OP against an already-resolved left-hand side.
// rhsParam, when non-empty (record `where`), is the CodeParam variable name:
// the VALUE is emitted as a runtime-flag dereference (`*flagPopGt`) so
// generated programs keep their adjustable filter flags; otherwise the VALUE
// is emitted as a validated literal.
func condOpToExprGo(lhs exprGo, op, value, rhsParam string) (exprGo, error) {
	switch op {
	case "eq", "ne", "gt", "ge", "lt", "le":
		sym := map[string]string{"eq": "==", "ne": "!=", "gt": ">", "ge": ">=", "lt": "<", "le": "<="}[op]
		rhs, err := condRHS(lhs, op, value, rhsParam)
		if err != nil {
			return exprGo{}, err
		}
		return exprCompare(sym, lhs, rhs, op == "eq" || op == "ne")

	case "contains", "startswith", "endswith":
		if lhs.Type != exprGoString {
			return exprGo{}, fmt.Errorf("operator %q requires a string field, got %s", op, lhs.Type)
		}
		fn := map[string]string{"contains": "strings.Contains", "startswith": "strings.HasPrefix", "endswith": "strings.HasSuffix"}[op]
		rhs := condStringRHS(value, rhsParam)
		res := exprGo{Src: fn + "(" + lhs.Src + ", " + rhs + ")", Type: exprGoBool, Imports: []string{"strings"}}
		return mergeMeta(res, lhs), nil

	case "regex":
		if lhs.Type != exprGoString {
			return exprGo{}, fmt.Errorf("operator \"regex\" requires a string field, got %s", lhs.Type)
		}
		if rhsParam != "" {
			// Parameterized pattern: it isn't known until flag.Parse, so it
			// cannot be hoisted to a package var — compiled at the call site
			// (pre-existing record behaviour, kept).
			res := exprGo{Src: "regexp.MustCompile(*" + rhsParam + ").MatchString(" + lhs.Src + ")",
				Type: exprGoBool, Imports: []string{"regexp"}}
			return mergeMeta(res, lhs), nil
		}
		if _, err := regexp.Compile(value); err != nil {
			// Validate at codegen — the hoisted MustCompile would otherwise
			// panic at generated-program startup.
			return exprGo{}, fmt.Errorf("invalid regex %q: %v", value, err)
		}
		// Literal pattern: hoist a content-addressed compiled var — the same
		// machinery the expression form's `matches` uses.
		varName := "exprRe" + exprGoHash(value)
		decl := fmt.Sprintf("var %s = regexp.MustCompile(%s)", varName, strconv.Quote(value))
		res := exprGo{Src: varName + ".MatchString(" + lhs.Src + ")", Type: exprGoBool,
			Imports: []string{"regexp"}, Hoisted: []string{decl}}
		return mergeMeta(res, lhs), nil
	}
	return exprGo{}, fmt.Errorf("unknown where operator %q (valid: eq/ne/gt/ge/lt/le/contains/startswith/endswith/regex)", op)
}

// condRHS builds the right-hand side of a comparison, typed to match the
// field (exec's applyOperator branches the comparison on the FIELD's runtime
// type — so must we).
func condRHS(lhs exprGo, op, value, rhsParam string) (exprGo, error) {
	switch lhs.Type {
	case exprGoInt, exprGoFloat:
		if rhsParam != "" {
			return exprGo{Src: "ssql.ParseFloat64(*" + rhsParam + ")", Type: exprGoFloat}, nil
		}
		if _, err := strconv.ParseInt(value, 10, 64); err == nil {
			return exprGo{Src: value, Type: exprGoInt, lit: true}, nil
		}
		if _, err := strconv.ParseFloat(value, 64); err == nil {
			return exprGo{Src: value, Type: exprGoFloat, lit: true}, nil
		}
		return exprGo{}, fmt.Errorf("invalid numeric literal %q for a %s field", value, lhs.Type)
	case exprGoString:
		return exprGo{Src: condStringRHS(value, rhsParam), Type: exprGoString}, nil
	case exprGoBool:
		if op != "eq" && op != "ne" {
			return exprGo{}, fmt.Errorf("operator %q is not defined for bool fields", op)
		}
		if rhsParam != "" {
			return exprGo{Src: "(*" + rhsParam + " == \"true\")", Type: exprGoBool}, nil
		}
		b, err := strconv.ParseBool(value)
		if err != nil {
			return exprGo{}, fmt.Errorf("invalid bool literal %q", value)
		}
		return exprGo{Src: strconv.FormatBool(b), Type: exprGoBool}, nil
	}
	return exprGo{}, fmt.Errorf("field type %s has no flag-condition emission", lhs.Type)
}

// condStringRHS renders a string-valued RHS: flag dereference or quoted
// literal.
func condStringRHS(value, rhsParam string) string {
	if rhsParam != "" {
		return "*" + rhsParam
	}
	return strconv.Quote(value)
}

// ---- expression canonicalization (convergence Phase C) ----
//
// exprToFlagConds recognizes a TRIVIAL expression — a conjunction of
// `field OP literal` comparisons — and returns the equivalent structured
// conditions. The `generate ssql` optimizer uses it to rewrite such
// -if-expr predicates into -if form, which the downstream rules (range
// tightening, contradiction detection, reorder, catalog extraction, join
// pushdown) can reason about; opaque expressions defeat all of them.
//
// Deliberately conservative:
//   - int, string and (for eq/ne) bool literals only. FLOAT literals are
//     REFUSED: `-if f gt 15.5` on an int column is silently
//     false-for-every-row in exec (ParseInt fails), while the expression
//     compares numerically — canonicalizing would change results (the
//     residual divergence documented by convergence Phase A).
//   - conjunctions (&&/and) only; OR needs clause splitting and is left
//     for a later phase.
//   - flipped operand order (`5 < pop`) is normalized (`pop gt 5`).
//   - contains/startsWith/endsWith map to their flag operators. `matches`
//     is left alone (regex values in shell round-trips deserve their own
//     soak).
func exprToFlagConds(expression string) ([]whereCondition, bool) {
	tree, err := parser.Parse(expression)
	if err != nil {
		return nil, false
	}
	return exprNodeToFlagConds(tree.Node)
}

func exprNodeToFlagConds(n ast.Node) ([]whereCondition, bool) {
	b, ok := n.(*ast.BinaryNode)
	if !ok {
		return nil, false
	}
	switch b.Operator {
	case "&&", "and":
		left, ok := exprNodeToFlagConds(b.Left)
		if !ok {
			return nil, false
		}
		right, ok := exprNodeToFlagConds(b.Right)
		if !ok {
			return nil, false
		}
		return append(left, right...), true

	case "==", "!=", "<", "<=", ">", ">=":
		field, value, isBool, flipped, ok := condCompareOperands(b.Left, b.Right)
		if !ok {
			return nil, false
		}
		op := map[string]string{"==": "eq", "!=": "ne", "<": "lt", "<=": "le", ">": "gt", ">=": "ge"}[b.Operator]
		if flipped {
			op = map[string]string{"eq": "eq", "ne": "ne", "lt": "gt", "le": "ge", "gt": "lt", "ge": "le"}[op]
		}
		if isBool && op != "eq" && op != "ne" {
			return nil, false
		}
		return []whereCondition{{Field: field, Operator: op, Value: value}}, true

	case "contains", "startsWith", "endsWith":
		ident, ok := b.Left.(*ast.IdentifierNode)
		if !ok {
			return nil, false
		}
		str, ok := b.Right.(*ast.StringNode)
		if !ok {
			return nil, false
		}
		op := map[string]string{"contains": "contains", "startsWith": "startswith", "endsWith": "endswith"}[b.Operator]
		return []whereCondition{{Field: ident.Value, Operator: op, Value: str.Value}}, true
	}
	return nil, false
}

// condCompareOperands extracts (field, literal) from a comparison's sides,
// accepting either order. Float literals refuse (see exprToFlagConds).
func condCompareOperands(left, right ast.Node) (field, value string, isBool, flipped, ok bool) {
	if ident, isIdent := left.(*ast.IdentifierNode); isIdent {
		v, b, lok := condLiteral(right)
		return ident.Value, v, b, false, lok
	}
	if ident, isIdent := right.(*ast.IdentifierNode); isIdent {
		v, b, lok := condLiteral(left)
		return ident.Value, v, b, true, lok
	}
	return "", "", false, false, false
}

func condLiteral(n ast.Node) (value string, isBool, ok bool) {
	switch l := n.(type) {
	case *ast.IntegerNode:
		return strconv.Itoa(l.Value), false, true
	case *ast.StringNode:
		return l.Value, false, true
	case *ast.BoolNode:
		return strconv.FormatBool(l.Value), true, true
	}
	return "", false, false
}
