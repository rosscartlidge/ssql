package commands

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// exprToGo transpiles an ssql expression (expr-lang, as used by -if-expr and
// -set-expr) into native Go source against a typed schema — the sibling of
// exprToSQL, targeting the typed codegen path instead of DuckDB.
//
// It covers the curated subset whose Go emission reproduces expr-lang
// semantics exactly (doc/research/expr-transpiler-implementation-plan.md §2 —
// notably int/int division is ALWAYS float64, len() counts runes, and ** is
// math.Pow). A non-nil error means "cannot transpile natively"; the error
// names the construct and the CALLER decides which fallback tier applies —
// unlike exprToSQL's callers, an error here is usually not fatal.

// exprGoType is the transpiler's type lattice. MVP: the four scalar types
// typed schemas produce from CSV. time.Time, pointers and sequences are
// deliberately outside — identifiers of those types refuse to transpile.
type exprGoType string

const (
	exprGoInt    exprGoType = "int64"
	exprGoFloat  exprGoType = "float64"
	exprGoString exprGoType = "string"
	exprGoBool   exprGoType = "bool"
)

func (t exprGoType) numeric() bool { return t == exprGoInt || t == exprGoFloat }

// exprGo is one transpiled (sub)expression.
type exprGo struct {
	Src     string     // Go expression source, parenthesized as needed
	Type    exprGoType // result type per the §2 semantics tables
	Imports []string   // e.g. "strings", "math", ".../exprfn"
	Hoisted []string   // package-level decls (regexp vars), deduped by the assembler

	// lit marks an integer/float literal: as a Go untyped constant it adapts
	// to a float64 context without an explicit float64(...) wrap, which keeps
	// emissions like `r.Price > 15` readable.
	lit bool
}

const exprfnImport = "github.com/rosscartlidge/ssql/v4/exprfn"

// exprToGo transpiles expression against schema; recv is the row variable
// name in the enclosing closure (the emitters use "r").
func exprToGo(expression string, schema *lib.TypedSchema, recv string) (exprGo, error) {
	tree, err := parser.Parse(expression)
	if err != nil {
		return exprGo{}, fmt.Errorf("expression %q: %w", expression, err)
	}
	env := newExprGoEnv(schema, recv)
	res, err := env.node(tree.Node)
	if err != nil {
		return exprGo{}, fmt.Errorf("expression %q: %w", expression, err)
	}
	return res, nil
}

// exprToGoBool is exprToGo + "result must be bool" (for -if-expr / +if-expr).
func exprToGoBool(expression string, schema *lib.TypedSchema, recv string) (exprGo, error) {
	res, err := exprToGo(expression, schema, recv)
	if err != nil {
		return exprGo{}, err
	}
	if res.Type != exprGoBool {
		return exprGo{}, fmt.Errorf("expression %q: result is %s, not a boolean predicate", expression, res.Type)
	}
	return res, nil
}

// exprGoEnv resolves identifiers against the schema, case-insensitively
// (matching lookupSchemaField's convention).
type exprGoEnv struct {
	fields map[string]lib.TypedSchemaField
	names  []string // schema order, for deterministic error messages
	recv   string
}

func newExprGoEnv(schema *lib.TypedSchema, recv string) *exprGoEnv {
	env := &exprGoEnv{fields: make(map[string]lib.TypedSchemaField), recv: recv}
	if schema != nil {
		for _, f := range schema.Fields {
			env.fields[strings.ToLower(f.Name)] = f
			env.names = append(env.names, f.Name)
		}
	}
	return env
}

func (e *exprGoEnv) fieldNames() string {
	return strings.Join(e.names, ", ")
}

// exprUnknownFieldError marks a reference to a field the schema doesn't
// have. Callers treat it as a USER error (loud, like `where -if` unknown-field
// validation) rather than a transpile refusal (silent fallback to record
// mode) — a typo'd field would fail in every mode, just later and worse.
type exprUnknownFieldError struct{ msg string }

func (e *exprUnknownFieldError) Error() string { return e.msg }

// field resolves a schema field to its Go reference, admitting only the MVP
// scalar types.
func (e *exprGoEnv) field(name string) (exprGo, error) {
	f, ok := e.fields[strings.ToLower(name)]
	if !ok {
		return exprGo{}, &exprUnknownFieldError{
			msg: fmt.Sprintf("unknown field %q (schema has %s)", name, e.fieldNames())}
	}
	switch f.GoType {
	case "int64":
		return exprGo{Src: e.recv + "." + f.GoName, Type: exprGoInt}, nil
	case "float64":
		return exprGo{Src: e.recv + "." + f.GoName, Type: exprGoFloat}, nil
	case "string":
		return exprGo{Src: e.recv + "." + f.GoName, Type: exprGoString}, nil
	case "bool":
		return exprGo{Src: e.recv + "." + f.GoName, Type: exprGoBool}, nil
	}
	return exprGo{}, fmt.Errorf("field %q has type %s, which has no native Go emission", name, f.GoType)
}

// mergeMeta combines child imports/hoisted decls into a parent result.
func mergeMeta(parent exprGo, children ...exprGo) exprGo {
	for _, c := range children {
		parent.Imports = append(parent.Imports, c.Imports...)
		parent.Hoisted = append(parent.Hoisted, c.Hoisted...)
	}
	return parent
}

// asFloat renders v in a float64 context. Int literals stay bare (Go untyped
// constants adapt — `r.Price > 15` beats `r.Price > float64(15)`); other int
// expressions get an explicit conversion.
func asFloat(v exprGo) string {
	if v.Type == exprGoFloat || v.lit {
		return v.Src
	}
	return "float64(" + v.Src + ")"
}

func (e *exprGoEnv) node(n ast.Node) (exprGo, error) {
	switch n := n.(type) {
	case *ast.IntegerNode:
		return exprGo{Src: strconv.Itoa(n.Value), Type: exprGoInt, lit: true}, nil
	case *ast.FloatNode:
		src := strconv.FormatFloat(n.Value, 'g', -1, 64)
		if !strings.ContainsAny(src, ".eE") {
			src += ".0" // keep it a float literal in Go source
		}
		return exprGo{Src: src, Type: exprGoFloat, lit: true}, nil
	case *ast.StringNode:
		return exprGo{Src: strconv.Quote(n.Value), Type: exprGoString}, nil
	case *ast.BoolNode:
		return exprGo{Src: strconv.FormatBool(n.Value), Type: exprGoBool}, nil
	case *ast.IdentifierNode:
		return e.field(n.Value)
	case *ast.UnaryNode:
		return e.unary(n)
	case *ast.BinaryNode:
		return e.binary(n)
	case *ast.ConditionalNode:
		return e.conditional(n)
	case *ast.BuiltinNode:
		return e.call(n.Name, n.Arguments)
	case *ast.CallNode:
		ident, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			return exprGo{}, fmt.Errorf("%s has no native Go emission", exprNodeDesc(n.Callee))
		}
		return e.call(ident.Value, n.Arguments)
	case *ast.NilNode:
		return exprGo{}, fmt.Errorf("nil has no native Go emission in typed mode")
	}
	return exprGo{}, fmt.Errorf("%s has no native Go emission", exprNodeDesc(n))
}

func (e *exprGoEnv) unary(n *ast.UnaryNode) (exprGo, error) {
	operand, err := e.node(n.Node)
	if err != nil {
		return exprGo{}, err
	}
	switch n.Operator {
	case "not", "!":
		if operand.Type != exprGoBool {
			return exprGo{}, fmt.Errorf("operator %q needs a boolean operand, got %s", n.Operator, operand.Type)
		}
		return mergeMeta(exprGo{Src: "!(" + operand.Src + ")", Type: exprGoBool}, operand), nil
	case "-":
		if !operand.Type.numeric() {
			return exprGo{}, fmt.Errorf("unary minus needs a numeric operand, got %s", operand.Type)
		}
		return mergeMeta(exprGo{Src: "(-" + operand.Src + ")", Type: operand.Type, lit: operand.lit}, operand), nil
	case "+":
		if !operand.Type.numeric() {
			return exprGo{}, fmt.Errorf("unary plus needs a numeric operand, got %s", operand.Type)
		}
		return operand, nil
	}
	return exprGo{}, fmt.Errorf("operator %q has no native Go emission", n.Operator)
}

func (e *exprGoEnv) binary(n *ast.BinaryNode) (exprGo, error) {
	// `in` needs its RHS handled as a literal list, not a value.
	if n.Operator == "in" {
		return e.inList(n)
	}

	// `x ?? d`: typed struct fields always exist and are never nil, so a
	// field LHS IS the result (§2c). Unknown-identifier LHS means the VM
	// would always take the default — emit the default (before the general
	// walk, which treats unknown identifiers as errors). Anything else has
	// nil-ness the type system can't see: refuse.
	if n.Operator == "??" {
		if ident, ok := n.Left.(*ast.IdentifierNode); ok {
			if _, known := e.fields[strings.ToLower(ident.Value)]; !known {
				return e.node(n.Right)
			}
			return e.field(ident.Value)
		}
		return exprGo{}, fmt.Errorf("?? needs a field on the left for native Go emission")
	}

	left, err := e.node(n.Left)
	if err != nil {
		return exprGo{}, err
	}

	right, err := e.node(n.Right)
	if err != nil {
		return exprGo{}, err
	}

	switch n.Operator {
	case "+":
		if left.Type == exprGoString && right.Type == exprGoString {
			return mergeMeta(exprGo{Src: "(" + left.Src + " + " + right.Src + ")", Type: exprGoString}, left, right), nil
		}
		return e.arith(n.Operator, left, right)
	case "-", "*":
		return e.arith(n.Operator, left, right)
	case "/":
		// expr-lang division is ALWAYS float64 — 7/2 is 3.5. NEVER emit Go
		// integer division.
		if !left.Type.numeric() || !right.Type.numeric() {
			return exprGo{}, fmt.Errorf("operator / needs numeric operands, got %s and %s", left.Type, right.Type)
		}
		ls, rs := asFloat(left), asFloat(right)
		if left.Type != exprGoFloat && right.Type != exprGoFloat {
			// Both int: at least one side must be a typed float64 expression
			// or the emission is Go integer division again.
			ls = "float64(" + left.Src + ")"
		}
		return mergeMeta(exprGo{Src: "(" + ls + " / " + rs + ")", Type: exprGoFloat}, left, right), nil
	case "%":
		if left.Type != exprGoInt || right.Type != exprGoInt {
			return exprGo{}, fmt.Errorf("operator %% needs integer operands, got %s and %s", left.Type, right.Type)
		}
		return mergeMeta(exprGo{Src: "(" + left.Src + " % " + right.Src + ")", Type: exprGoInt}, left, right), nil
	case "**", "^":
		if !left.Type.numeric() || !right.Type.numeric() {
			return exprGo{}, fmt.Errorf("power operator needs numeric operands, got %s and %s", left.Type, right.Type)
		}
		res := exprGo{Src: "math.Pow(" + asFloat(left) + ", " + asFloat(right) + ")", Type: exprGoFloat,
			Imports: []string{"math"}}
		return mergeMeta(res, left, right), nil
	case "<", "<=", ">", ">=":
		return e.compare(n.Operator, left, right, false)
	case "==", "!=":
		return e.compare(n.Operator, left, right, true)
	case "and", "&&":
		return e.logical("&&", left, right)
	case "or", "||":
		return e.logical("||", left, right)
	case "contains", "startsWith", "endsWith":
		fn := map[string]string{"contains": "strings.Contains", "startsWith": "strings.HasPrefix", "endsWith": "strings.HasSuffix"}[n.Operator]
		if left.Type != exprGoString || right.Type != exprGoString {
			return exprGo{}, fmt.Errorf("operator %q needs string operands, got %s and %s", n.Operator, left.Type, right.Type)
		}
		res := exprGo{Src: fn + "(" + left.Src + ", " + right.Src + ")", Type: exprGoBool, Imports: []string{"strings"}}
		return mergeMeta(res, left, right), nil
	case "matches":
		if left.Type != exprGoString {
			return exprGo{}, fmt.Errorf("matches needs a string on the left, got %s", left.Type)
		}
		pat, ok := n.Right.(*ast.StringNode)
		if !ok {
			return exprGo{}, fmt.Errorf("matches needs a literal pattern for native Go emission")
		}
		// Hoist the compiled pattern to a package-level var; the name is
		// content-addressed so identical patterns dedupe to one decl.
		h := fnv.New32a()
		h.Write([]byte(pat.Value))
		varName := fmt.Sprintf("exprRe%08x", h.Sum32())
		decl := fmt.Sprintf("var %s = regexp.MustCompile(%s)", varName, strconv.Quote(pat.Value))
		res := exprGo{Src: varName + ".MatchString(" + left.Src + ")", Type: exprGoBool,
			Imports: []string{"regexp"}, Hoisted: []string{decl}}
		return mergeMeta(res, left), nil
	}
	return exprGo{}, fmt.Errorf("operator %q has no native Go emission", n.Operator)
}

// arith handles + - * over numbers: int OP int stays int64 (Go wrap matches
// the VM), any float operand promotes the result to float64.
func (e *exprGoEnv) arith(op string, left, right exprGo) (exprGo, error) {
	if !left.Type.numeric() || !right.Type.numeric() {
		return exprGo{}, fmt.Errorf("operator %s needs numeric operands, got %s and %s", op, left.Type, right.Type)
	}
	if left.Type == exprGoFloat || right.Type == exprGoFloat {
		res := exprGo{Src: "(" + asFloat(left) + " " + op + " " + asFloat(right) + ")", Type: exprGoFloat}
		return mergeMeta(res, left, right), nil
	}
	return mergeMeta(exprGo{Src: "(" + left.Src + " " + op + " " + right.Src + ")", Type: exprGoInt}, left, right), nil
}

// compare handles < <= > >= == !=. Cross-type comparisons (string vs number,
// number vs bool) are codegen errors: the VM either errors per row or — for
// `==` — silently returns false, and both are bugs worth catching early (§6).
func (e *exprGoEnv) compare(op string, left, right exprGo, equality bool) (exprGo, error) {
	switch {
	case left.Type.numeric() && right.Type.numeric():
		if left.Type != right.Type {
			left.Src = asFloat(left)
			right.Src = asFloat(right)
		}
	case left.Type == exprGoString && right.Type == exprGoString:
		// lexicographic — Go string compare matches
	case equality && left.Type == exprGoBool && right.Type == exprGoBool:
		// bool == bool is fine
	default:
		return exprGo{}, fmt.Errorf("cannot compare %s with %s (%s)", left.Type, right.Type, op)
	}
	return mergeMeta(exprGo{Src: "(" + left.Src + " " + op + " " + right.Src + ")", Type: exprGoBool}, left, right), nil
}

func (e *exprGoEnv) logical(op string, left, right exprGo) (exprGo, error) {
	if left.Type != exprGoBool || right.Type != exprGoBool {
		return exprGo{}, fmt.Errorf("operator %s needs boolean operands, got %s and %s", op, left.Type, right.Type)
	}
	return mergeMeta(exprGo{Src: "(" + left.Src + " " + op + " " + right.Src + ")", Type: exprGoBool}, left, right), nil
}

// inList lowers `x in [a, b, c]` (literal list) to (x == a || x == b || x == c).
func (e *exprGoEnv) inList(n *ast.BinaryNode) (exprGo, error) {
	arr, ok := n.Right.(*ast.ArrayNode)
	if !ok {
		return exprGo{}, fmt.Errorf("`in` needs a list literal for native Go emission")
	}
	left, err := e.node(n.Left)
	if err != nil {
		return exprGo{}, err
	}
	if len(arr.Nodes) == 0 {
		return exprGo{Src: "false", Type: exprGoBool}, nil
	}
	var parts []string
	res := exprGo{Type: exprGoBool}
	for _, el := range arr.Nodes {
		ev, err := e.node(el)
		if err != nil {
			return exprGo{}, err
		}
		eq, err := e.compare("==", left, ev, true)
		if err != nil {
			return exprGo{}, err
		}
		parts = append(parts, eq.Src)
		res = mergeMeta(res, eq)
	}
	if len(parts) == 1 {
		res.Src = parts[0]
	} else {
		res.Src = "(" + strings.Join(parts, " || ") + ")"
	}
	return res, nil
}

func (e *exprGoEnv) conditional(n *ast.ConditionalNode) (exprGo, error) {
	cond, err := e.node(n.Cond)
	if err != nil {
		return exprGo{}, err
	}
	if cond.Type != exprGoBool {
		return exprGo{}, fmt.Errorf("ternary condition is %s, not bool", cond.Type)
	}
	then, err := e.node(n.Exp1)
	if err != nil {
		return exprGo{}, err
	}
	els, err := e.node(n.Exp2)
	if err != nil {
		return exprGo{}, err
	}
	typ := then.Type
	thenSrc, elsSrc := then.Src, els.Src
	if then.Type != els.Type {
		if !then.Type.numeric() || !els.Type.numeric() {
			return exprGo{}, fmt.Errorf("ternary branches have incompatible types %s and %s", then.Type, els.Type)
		}
		typ = exprGoFloat
		thenSrc, elsSrc = asFloat(then), asFloat(els)
	}
	src := fmt.Sprintf("func() %s { if %s { return %s }; return %s }()", typ, cond.Src, thenSrc, elsSrc)
	return mergeMeta(exprGo{Src: src, Type: typ}, cond, then, els), nil
}

// call handles builtins and the has/getOr record helpers.
func (e *exprGoEnv) call(name string, args []ast.Node) (exprGo, error) {
	// has/getOr resolve against the schema at codegen time: typed struct
	// fields always exist, so has() is a constant and getOr() is the field
	// itself (or the default, when the field isn't in the schema at all —
	// exactly what the VM would return on every row).
	switch name {
	case "has":
		if len(args) != 1 {
			return exprGo{}, fmt.Errorf("has() takes exactly one field name")
		}
		fieldName, err := exprGoFieldArg(args[0])
		if err != nil {
			return exprGo{}, err
		}
		_, known := e.fields[strings.ToLower(fieldName)]
		return exprGo{Src: strconv.FormatBool(known), Type: exprGoBool}, nil
	case "getOr":
		if len(args) != 2 {
			return exprGo{}, fmt.Errorf("getOr() takes a field name and a default")
		}
		fieldName, err := exprGoFieldArg(args[0])
		if err != nil {
			return exprGo{}, err
		}
		def, err := e.node(args[1])
		if err != nil {
			return exprGo{}, err
		}
		if _, known := e.fields[strings.ToLower(fieldName)]; !known {
			return def, nil
		}
		field, err := e.field(fieldName)
		if err != nil {
			return exprGo{}, err
		}
		if field.Type != def.Type && !(field.Type.numeric() && def.Type.numeric()) {
			return exprGo{}, fmt.Errorf("getOr(%q, …): field is %s but default is %s", fieldName, field.Type, def.Type)
		}
		return field, nil
	}

	// Single-argument builtins.
	one := func() (exprGo, error) {
		if len(args) != 1 {
			return exprGo{}, fmt.Errorf("%s() takes exactly one argument", name)
		}
		return e.node(args[0])
	}

	switch name {
	case "len":
		arg, err := one()
		if err != nil {
			return exprGo{}, err
		}
		if arg.Type != exprGoString {
			return exprGo{}, fmt.Errorf("len() supports strings only, got %s", arg.Type)
		}
		res := exprGo{Src: "exprfn.RuneLen(" + arg.Src + ")", Type: exprGoInt, Imports: []string{exprfnImport}}
		return mergeMeta(res, arg), nil
	case "abs":
		arg, err := one()
		if err != nil {
			return exprGo{}, err
		}
		if !arg.Type.numeric() {
			return exprGo{}, fmt.Errorf("abs() needs a numeric argument, got %s", arg.Type)
		}
		res := exprGo{Src: "exprfn.Abs(" + arg.Src + ")", Type: arg.Type, Imports: []string{exprfnImport}}
		return mergeMeta(res, arg), nil
	case "round", "floor", "ceil":
		arg, err := one()
		if err != nil {
			return exprGo{}, err
		}
		if !arg.Type.numeric() {
			return exprGo{}, fmt.Errorf("%s() needs a numeric argument, got %s", name, arg.Type)
		}
		fn := map[string]string{"round": "math.Round", "floor": "math.Floor", "ceil": "math.Ceil"}[name]
		res := exprGo{Src: fn + "(" + asFloat(arg) + ")", Type: exprGoFloat, Imports: []string{"math"}}
		return mergeMeta(res, arg), nil
	case "min", "max":
		if len(args) != 2 {
			return exprGo{}, fmt.Errorf("%s() takes exactly two arguments", name)
		}
		a, err := e.node(args[0])
		if err != nil {
			return exprGo{}, err
		}
		b, err := e.node(args[1])
		if err != nil {
			return exprGo{}, err
		}
		if !a.Type.numeric() || !b.Type.numeric() {
			return exprGo{}, fmt.Errorf("%s() needs numeric arguments, got %s and %s", name, a.Type, b.Type)
		}
		if a.Type != b.Type {
			// The VM returns the WINNING operand with its own type (probed:
			// min(-3, -2.5) is int64 -3, not -3.0 — the differential harness
			// caught the float64-promotion assumption). No static Go type
			// expresses that; refuse and let the caller fall back.
			return exprGo{}, fmt.Errorf("%s() with mixed int/float arguments keeps the winner's type — no native Go emission", name)
		}
		return mergeMeta(exprGo{Src: name + "(" + a.Src + ", " + b.Src + ")", Type: a.Type}, a, b), nil
	case "int":
		arg, err := one()
		if err != nil {
			return exprGo{}, err
		}
		switch arg.Type {
		case exprGoInt:
			return arg, nil
		case exprGoFloat:
			// expr-lang int() truncates toward zero — Go conversion matches.
			return mergeMeta(exprGo{Src: "int64(" + arg.Src + ")", Type: exprGoInt}, arg), nil
		}
		return exprGo{}, fmt.Errorf("int() of a %s has no native Go emission", arg.Type)
	case "float":
		arg, err := one()
		if err != nil {
			return exprGo{}, err
		}
		switch arg.Type {
		case exprGoFloat:
			return arg, nil
		case exprGoInt:
			return mergeMeta(exprGo{Src: "float64(" + arg.Src + ")", Type: exprGoFloat}, arg), nil
		}
		return exprGo{}, fmt.Errorf("float() of a %s has no native Go emission", arg.Type)
	case "string":
		arg, err := one()
		if err != nil {
			return exprGo{}, err
		}
		switch arg.Type {
		case exprGoString:
			return arg, nil
		case exprGoInt:
			res := exprGo{Src: "strconv.FormatInt(" + arg.Src + ", 10)", Type: exprGoString, Imports: []string{"strconv"}}
			return mergeMeta(res, arg), nil
		case exprGoFloat:
			res := exprGo{Src: "strconv.FormatFloat(" + arg.Src + ", 'g', -1, 64)", Type: exprGoString, Imports: []string{"strconv"}}
			return mergeMeta(res, arg), nil
		}
		return exprGo{}, fmt.Errorf("string() of a %s has no native Go emission", arg.Type)
	case "upper", "lower", "trim":
		arg, err := one()
		if err != nil {
			return exprGo{}, err
		}
		if arg.Type != exprGoString {
			return exprGo{}, fmt.Errorf("%s() needs a string argument, got %s", name, arg.Type)
		}
		fn := map[string]string{"upper": "strings.ToUpper", "lower": "strings.ToLower", "trim": "strings.TrimSpace"}[name]
		res := exprGo{Src: fn + "(" + arg.Src + ")", Type: exprGoString, Imports: []string{"strings"}}
		return mergeMeta(res, arg), nil
	case "hasPrefix", "hasSuffix":
		if len(args) != 2 {
			return exprGo{}, fmt.Errorf("%s() takes exactly two arguments", name)
		}
		a, err := e.node(args[0])
		if err != nil {
			return exprGo{}, err
		}
		b, err := e.node(args[1])
		if err != nil {
			return exprGo{}, err
		}
		if a.Type != exprGoString || b.Type != exprGoString {
			return exprGo{}, fmt.Errorf("%s() needs string arguments, got %s and %s", name, a.Type, b.Type)
		}
		fn := map[string]string{"hasPrefix": "strings.HasPrefix", "hasSuffix": "strings.HasSuffix"}[name]
		res := exprGo{Src: fn + "(" + a.Src + ", " + b.Src + ")", Type: exprGoBool, Imports: []string{"strings"}}
		return mergeMeta(res, a, b), nil
	}
	return exprGo{}, fmt.Errorf("function %q has no native Go emission", name)
}

// exprGoFieldArg extracts the literal field name from a has("field") /
// getOr("field", …) argument.
func exprGoFieldArg(node ast.Node) (string, error) {
	switch n := node.(type) {
	case *ast.StringNode:
		return n.Value, nil
	case *ast.IdentifierNode:
		return n.Value, nil
	}
	return "", fmt.Errorf("field argument must be a literal field name")
}
