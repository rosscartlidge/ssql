// Package typedomain is the domain lint for type switches (DFC163 §2.2).
//
// Every type ssql added after the first four found bugs of one shape: a
// type switch, or a table keyed by a type's name, that the new type was
// never added to. A switch over record values declares the types it
// means to handle with a comment on the line above it,
//
//	// ssql:domain numeric
//	switch v := v.(type) {
//
// and the lint checks its cases cover every member of that domain. A
// switch that names a struct scalar (time.Time, ssql.Int128,
// ssql.Decimal, ssql.Inet) without the comment is an error, so a new
// scalar type, added to the domain table, is a list of every switch that
// must handle it.
//
// Three shapes are checked: a Go type switch; a switch on an
// ssql.FieldType; and a string table — a switch on a string, or a
// map[string] literal — whose keys are Go type names ("ssql.Int128") or
// wire names ("int128").
package typedomain

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Analyzer is the domain lint.
var Analyzer = &analysis.Analyzer{
	Name: "typedomain",
	Doc:  "check that switches over record values declare a type domain (// ssql:domain D) and cover it",
	URL:  "https://github.com/rosscartlidge/ssql/blob/main/doc/research/dfc163_type_operation_matrix_and_domain_lint.md",
	Run:  run,
}

// ssqlPath is the root package, whose struct scalars the lint knows.
const ssqlPath = "github.com/rosscartlidge/ssql/v4"

func run(pass *analysis.Pass) (any, error) {
	for _, f := range pass.Files {
		if strings.HasSuffix(pass.Fset.Position(f.Pos()).Filename, "_test.go") {
			continue
		}
		c := &checker{pass: pass, notes: notesOf(pass.Fset, f)}
		c.walk(f)
	}
	return nil, nil
}

type checker struct {
	pass  *analysis.Pass
	notes map[int]string // line → text after the marker
	stack []ast.Node
	funcs []ast.Node // enclosing function bodies, innermost last
}

// notesOf indexes every ssql:domain comment by the line it ends on.
func notesOf(fset *token.FileSet, f *ast.File) map[int]string {
	notes := map[int]string{}
	for _, g := range f.Comments {
		for _, c := range g.List {
			text := strings.TrimPrefix(strings.TrimPrefix(c.Text, "//"), "/*")
			if _, rest, ok := strings.Cut(text, marker); ok {
				notes[fset.Position(g.End()).Line] = strings.TrimSuffix(rest, "*/")
				notes[fset.Position(c.End()).Line] = strings.TrimSuffix(rest, "*/")
			}
		}
	}
	return notes
}

func (c *checker) walk(root ast.Node) {
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			top := c.stack[len(c.stack)-1]
			c.stack = c.stack[:len(c.stack)-1]
			if len(c.funcs) > 0 && c.funcs[len(c.funcs)-1] == top {
				c.funcs = c.funcs[:len(c.funcs)-1]
			}
			return true
		}
		c.stack = append(c.stack, n)
		switch n := n.(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			c.funcs = append(c.funcs, n)
		case *ast.TypeSwitchStmt:
			c.typeSwitch(n)
		case *ast.SwitchStmt:
			c.valueSwitch(n)
		case *ast.CompositeLit:
			c.mapLiteral(n)
		}
		return true
	})
}

// check reports a qualifying node whose annotation is missing, malformed
// or not covered by its cases.
func (c *checker) check(at ast.Node, what string, covered map[string]bool) {
	text, ok := c.annotationFor(at)
	if !ok {
		c.pass.Reportf(at.Pos(), "%s handles %s but declares no domain: add `// ssql:domain D` above it (DFC163)", what, coveredNames(covered))
		return
	}
	a, err := parseAnnotation(text)
	if err != nil {
		c.pass.Reportf(at.Pos(), "%s: %v", what, err)
		return
	}
	if a.fixed {
		return
	}
	if miss := a.missing(covered); len(miss) > 0 {
		c.pass.Reportf(at.Pos(), "%s declares %s; missing %s", what, strings.TrimSpace(firstWord(text)), strings.Join(miss, ", "))
	}
}

// annotationFor finds the ssql:domain comment for a node: on the line
// above it or trailing on its own line, or the same for each enclosing
// node up to the statement or declaration that holds it (a map literal
// is usually the value of a var).
func (c *checker) annotationFor(at ast.Node) (string, bool) {
	for i := len(c.stack) - 1; i >= 0; i-- {
		n := c.stack[i]
		line := c.pass.Fset.Position(n.Pos()).Line
		if t, ok := c.notes[line-1]; ok {
			return t, true
		}
		if t, ok := c.notes[line]; ok {
			return t, true
		}
		switch n.(type) {
		case ast.Stmt, ast.Decl, *ast.ValueSpec:
			if n != at {
				return "", false
			}
		}
	}
	return "", false
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

func coveredNames(covered map[string]bool) string {
	var out []string
	for _, m := range members {
		if covered[m] {
			out = append(out, m)
		}
	}
	return strings.Join(out, ", ")
}

func qualifies(covered map[string]bool) bool {
	for m := range covered {
		if structScalars[m] {
			return true
		}
	}
	return false
}

// typeSwitch: `switch v := x.(type)` naming a struct scalar.
func (c *checker) typeSwitch(s *ast.TypeSwitchStmt) {
	covered := map[string]bool{}
	for _, cl := range s.Body.List {
		for _, e := range cl.(*ast.CaseClause).List {
			if m := memberOfType(c.pass.TypesInfo.TypeOf(e)); m != "" {
				covered[m] = true
			}
		}
	}
	if qualifies(covered) {
		c.check(s, "type switch", covered)
	}
}

// memberOfType is the member a case type is, or "".
func memberOfType(t types.Type) string {
	if t == nil {
		return ""
	}
	if b, ok := t.(*types.Basic); ok {
		switch b.Kind() {
		case types.Int64:
			return mInt64
		case types.Float64:
			return mFloat64
		case types.String:
			return mString
		case types.Bool:
			return mBool
		}
		return ""
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return ""
	}
	switch path, name := n.Obj().Pkg().Path(), n.Obj().Name(); {
	case path == "time" && name == "Time":
		return mTime
	case path == ssqlPath && name == "Int128":
		return mInt128
	case path == ssqlPath && name == "Decimal":
		return mDecimal
	case path == ssqlPath && name == "Inet":
		return mInet
	}
	return ""
}

// fieldTypeMembers maps the FieldType constants to members. Decimal is
// never a case label (its scale rides in the value); a FieldType switch
// covers it when its function tests IsDecimal.
var fieldTypeMembers = map[string]string{
	"FieldTypeString": mString,
	"FieldTypeInt":    mInt64,
	"FieldTypeFloat":  mFloat64,
	"FieldTypeBool":   mBool,
	"FieldTypeTime":   mTime,
	"FieldTypeInt128": mInt128,
	"FieldTypeInet":   mInet,
}

// valueSwitch: a switch on an ssql.FieldType, or on a string whose
// cases are type names.
func (c *checker) valueSwitch(s *ast.SwitchStmt) {
	if s.Tag == nil {
		return
	}
	t := c.pass.TypesInfo.TypeOf(s.Tag)
	if t == nil {
		return
	}
	if isFieldType(t) {
		covered := map[string]bool{}
		for _, cl := range s.Body.List {
			for _, e := range cl.(*ast.CaseClause).List {
				if obj := c.constObject(e); obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == ssqlPath {
					if m, ok := fieldTypeMembers[obj.Name()]; ok {
						covered[m] = true
					}
				}
			}
		}
		if qualifies(covered) {
			if c.funcHandlesDecimal() {
				covered[mDecimal] = true
			}
			c.check(s, "FieldType switch", covered)
		}
		return
	}
	if b, ok := t.Underlying().(*types.Basic); !ok || b.Info()&types.IsString == 0 {
		return
	}
	var keys []ast.Expr
	for _, cl := range s.Body.List {
		keys = append(keys, cl.(*ast.CaseClause).List...)
	}
	if covered, ok := c.nameTable(keys); ok {
		if c.funcHandlesDecimal() {
			covered[mDecimal] = true
		}
		c.check(s, "type-name switch", covered)
	}
}

// mapLiteral: a map[string]… literal whose keys are type names.
func (c *checker) mapLiteral(lit *ast.CompositeLit) {
	t := c.pass.TypesInfo.TypeOf(lit)
	if t == nil {
		return
	}
	m, ok := t.Underlying().(*types.Map)
	if !ok {
		return
	}
	if b, ok := m.Key().Underlying().(*types.Basic); !ok || b.Info()&types.IsString == 0 {
		return
	}
	var keys []ast.Expr
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			keys = append(keys, kv.Key)
		}
	}
	if covered, ok := c.nameTable(keys); ok {
		c.check(lit, "type-name table", covered)
	}
}

// typeNames maps the Go type names and wire names a codegen or schema
// table is keyed by to members. Only the struct scalars' names make a
// table qualify; the basic names count toward coverage.
// ssql:domain fixed: the lint's own tables
var typeNames = map[string]string{
	"ssql.Int128": mInt128, "ssql.Decimal": mDecimal, "ssql.Inet": mInet, "time.Time": mTime,
	"int64": mInt64, "float64": mFloat64,
	"int128": mInt128, "decimal": mDecimal, "inet": mInet, "time": mTime,
	"int": mInt64, "float": mFloat64, "string": mString, "bool": mBool,
}

// qualifyingNames are the names that make a string table a type table:
// "time" alone names too much else.
// ssql:domain fixed: the lint's own tables
var qualifyingNames = map[string]bool{
	"ssql.Int128": true, "ssql.Decimal": true, "ssql.Inet": true, "time.Time": true,
	"int128": true, "decimal": true, "inet": true,
}

func (c *checker) nameTable(keys []ast.Expr) (map[string]bool, bool) {
	covered := map[string]bool{}
	qualify := false
	for _, k := range keys {
		tv, ok := c.pass.TypesInfo.Types[k]
		if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
			continue
		}
		s := constant.StringVal(tv.Value)
		if m, ok := typeNames[s]; ok {
			covered[m] = true
		}
		if qualifyingNames[s] {
			qualify = true
		}
	}
	return covered, qualify
}

func isFieldType(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == ssqlPath && n.Obj().Name() == "FieldType"
}

func (c *checker) constObject(e ast.Expr) types.Object {
	var id *ast.Ident
	switch e := e.(type) {
	case *ast.Ident:
		id = e
	case *ast.SelectorExpr:
		id = e.Sel
	default:
		return nil
	}
	if obj, ok := c.pass.TypesInfo.Uses[id].(*types.Const); ok {
		return obj
	}
	return nil
}

// funcHandlesDecimal reports whether the innermost enclosing function
// handles a decimal before its switch, the only way one can: a decimal
// is never a case label, since its scale rides in the FieldType and in
// the wire name ("decimal:2"). The function calls IsDecimal or
// ParseWireType, or tests a "decimal" prefix.
func (c *checker) funcHandlesDecimal() bool {
	if len(c.funcs) == 0 {
		return false
	}
	found := false
	ast.Inspect(c.funcs[len(c.funcs)-1], func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.SelectorExpr:
			found = n.Sel.Name == "IsDecimal" || n.Sel.Name == "ParseWireType"
		case *ast.Ident:
			found = n.Name == "ParseWireType"
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "HasPrefix" || sel.Sel.Name == "CutPrefix") {
				for _, a := range n.Args {
					if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.HasPrefix(strings.Trim(lit.Value, "`\""), "decimal") {
						found = true
					}
				}
			}
		}
		return !found
	})
	return found
}
