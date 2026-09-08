package ssql

import (
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/builtin"
)

// exprBuiltinNames is the set of expr-lang builtin function names (date,
// len, type, max, …). A record field with one of these names is common
// (`date` above all), and expr's type checker resolves a bare identifier
// to the builtin when the compile environment does not declare it as a
// variable — so `date > "2026-02-01"` failed to compile ("mismatched
// types func(...) and string") while `date(date)` worked.
var exprBuiltinNames = func() map[string]bool {
	m := make(map[string]bool, len(builtin.Names))
	for _, n := range builtin.Names {
		m[n] = true
	}
	return m
}()

// fieldShadowPatcher implements "field when bare, function when called":
// a bare identifier that shares a builtin's name becomes the environment
// lookup $env["name"] — the record field — while a call `date(...)` is
// parsed by expr as a BuiltinNode and is never an IdentifierNode, so it
// keeps meaning the function. This is the rule the Go transpiler already
// applies (every bare identifier is a struct field; only a call position
// is a function), so the VM lane now agrees with it.
type fieldShadowPatcher struct{}

func (fieldShadowPatcher) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IdentifierNode:
		if exprBuiltinNames[n.Value] {
			ast.Patch(node, &ast.MemberNode{
				Node:     &ast.IdentifierNode{Value: "$env"},
				Property: &ast.StringNode{Value: n.Value},
			})
		}
	case *ast.CallNode:
		// Walk is post-order: the callee was visited (and possibly
		// rewritten) before its call. A call to an environment function
		// that shares a builtin's name — the aggregation env's max/min —
		// must keep its identifier callee.
		if name, ok := ExprFieldName(n.Callee); ok && name != "$env" {
			if _, isID := n.Callee.(*ast.IdentifierNode); !isID {
				n.Callee = &ast.IdentifierNode{Value: name}
			}
		}
	}
}

// ExprFieldShadowing is the expr.Compile option that lets record fields
// named like a builtin (date, len, type, max, …) be used as fields. Every
// ssql expression compile site passes it, so exec, generated code and
// aggregation expressions agree.
func ExprFieldShadowing() expr.Option {
	return expr.Patch(fieldShadowPatcher{})
}

// ExprFieldName reports the record field an AST node refers to: a bare
// identifier, or the $env["name"] form that ExprFieldShadowing rewrites
// builtin-named fields into.
func ExprFieldName(node ast.Node) (string, bool) {
	switch n := node.(type) {
	case *ast.IdentifierNode:
		return n.Value, true
	case *ast.MemberNode:
		if env, ok := n.Node.(*ast.IdentifierNode); ok && env.Value == "$env" {
			if s, ok := n.Property.(*ast.StringNode); ok {
				return s.Value, true
			}
		}
	}
	return "", false
}
