package commands

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime"
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

// exprGoType is the transpiler's type lattice: the four scalar types typed
// schemas produce from CSV, and time.Time (DFC147: a `time` column, read
// through the calendar functions; arithmetic on times stays outside —
// the VM's result is a time.Duration, which no column holds). Pointers
// and sequences are outside — identifiers of those types refuse.
type exprGoType string

const (
	exprGoInt    exprGoType = "int64"
	exprGoFloat  exprGoType = "float64"
	exprGoString exprGoType = "string"
	exprGoBool   exprGoType = "bool"
	exprGoTime   exprGoType = "time.Time"
)

// ssqlImport is the root package, for the calendar helpers transpiled
// calendar functions call.
const ssqlImport = "github.com/rosscartlidge/ssql/v4"

// zoneSrc is the Go expression for the zone a calendar function reads in:
// the location a tz() gave the value (hoisted), else the pipeline's.
func zoneSrc(v exprGo) string {
	if v.zoneVar != "" {
		return v.zoneVar
	}
	return "ssql.StreamLocation()"
}

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

	// field names the record field an operand reads, for messages about the
	// literal it is compared with ("" for a computed operand).
	field string

	// zoneVar names the hoisted *time.Location a tz() put on a time value:
	// a calendar function over it reads that zone, as the VM does for a
	// zoned time (ssql.TimeIsZoned). "" is the pipeline zone.
	zoneVar string
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

// exprToGoWith is exprToGo with extra variable bindings (e.g. -stream-expr
// state fields), resolved after schema fields — record shadows state. A nil
// schema means identifiers resolve against vars only.
func exprToGoWith(expression string, schema *lib.TypedSchema, recv string, vars map[string]exprGo) (exprGo, error) {
	tree, err := parser.Parse(expression)
	if err != nil {
		return exprGo{}, fmt.Errorf("expression %q: %w", expression, err)
	}
	env := newExprGoEnv(schema, recv)
	env.vars = vars
	res, err := env.node(tree.Node)
	if err != nil {
		return exprGo{}, fmt.Errorf("expression %q: %w", expression, err)
	}
	return res, nil
}

// exprNodeToGoWith runs the walker directly on an already-parsed AST node
// (used by the -stream-expr lowering, which dissects map literals).
func exprNodeToGoWith(n ast.Node, schema *lib.TypedSchema, recv string, vars map[string]exprGo) (exprGo, error) {
	env := newExprGoEnv(schema, recv)
	env.vars = vars
	return env.node(n)
}

// exprToGoRecord transpiles an expression for RECORD mode: identifiers emit
// typed ssql.GetOr calls against recv (a Record), with types from the
// advisory map (CSV name → Go type, sampled by the source). Same walker,
// same semantics tables — only the field emission differs.
func exprToGoRecord(expression string, advisory map[string]string, recv string) (exprGo, error) {
	return exprToGoRecordParams(expression, advisory, recv, nil)
}

// exprToGoRecordParams is exprToGoRecord with -param bindings.
func exprToGoRecordParams(expression string, advisory map[string]string, recv string, params map[string]exprGo) (exprGo, error) {
	tree, err := parser.Parse(expression)
	if err != nil {
		return exprGo{}, fmt.Errorf("expression %q: %w", expression, err)
	}
	env := &exprGoEnv{recv: recv, advisory: make(map[string]lib.TypedSchemaField, len(advisory)), params: params, closed: closedOver(expression, params)}
	for name, goType := range advisory {
		env.advisory[strings.ToLower(name)] = lib.TypedSchemaField{Name: name, GoType: goType}
		env.names = append(env.names, name)
	}
	sort.Strings(env.names) // map order — sorted for deterministic errors
	res, err := env.node(tree.Node)
	if err != nil {
		return exprGo{}, fmt.Errorf("expression %q: %w", expression, err)
	}
	return res, nil
}

// exprToGoParams is exprToGo with -param bindings.
func exprToGoParams(expression string, schema *lib.TypedSchema, recv string, params map[string]exprGo) (exprGo, error) {
	tree, err := parser.Parse(expression)
	if err != nil {
		return exprGo{}, fmt.Errorf("expression %q: %w", expression, err)
	}
	env := newExprGoEnv(schema, recv)
	env.params = params
	env.closed = closedOver(expression, params)
	res, err := env.node(tree.Node)
	if err != nil {
		return exprGo{}, fmt.Errorf("expression %q: %w", expression, err)
	}
	return res, nil
}

// exprToGoBool is exprToGo + "result must be bool" (for -if-expr / +if-expr).
func exprToGoBool(expression string, schema *lib.TypedSchema, recv string) (exprGo, error) {
	return exprToGoBoolParams(expression, schema, recv, nil)
}

func exprToGoBoolParams(expression string, schema *lib.TypedSchema, recv string, params map[string]exprGo) (exprGo, error) {
	res, err := exprToGoParams(expression, schema, recv, params)
	if err != nil {
		return exprGo{}, err
	}
	if res.Type != exprGoBool {
		return exprGo{}, fmt.Errorf("expression %q: result is %s, not a boolean predicate", expression, res.Type)
	}
	return res, nil
}

// exprGoEnv resolves identifiers against the schema, case-insensitively
// (matching lookupSchemaField's convention). vars holds extra bindings
// (group-by -stream-expr state fields) consulted AFTER schema fields —
// record shadows state, matching evalStreamAggExpr's env build order
// (maps.Copy(state) then maps.Insert(record): record wins).
//
// In RECORD mode (expr-transpiler Phase 4) there is no struct: advisory
// holds sampled column types and identifiers emit typed ssql.GetOr calls
// instead of struct field access.
type exprGoEnv struct {
	fields map[string]lib.TypedSchemaField
	names  []string // schema order, for deterministic error messages
	recv   string
	vars   map[string]exprGo // extra bindings, e.g. "s" → {Src: "a.se0_s", Type: float64}

	advisory map[string]lib.TypedSchemaField // record mode: lowercase name → {Name, GoType}

	// params are -param bindings (DFC134 §5.3), resolved BEFORE fields: a
	// name that is both is a loud error (silent precedence would let a new
	// upstream column change an expression's meaning) — except a FIELD
	// parameter (field set) in a CLOSED expression, whose every identifier
	// is a parameter (closed): the columns are not in play and the
	// parameter may be named like the column it views (a macro body,
	// DFC152). A parameter whose type is outside the lattice (time)
	// refuses quietly → the VM tier, where it is an ordinary env variable.
	params map[string]exprGo
	closed bool
}

// closedOver reports whether every identifier of the expression is one of
// params (the runtime's paramBinding.markClosed, at transpile time).
func closedOver(expression string, params map[string]exprGo) bool {
	if len(params) == 0 {
		return false
	}
	ids, err := runtime.ExprIdentifiers(expression)
	if err != nil || len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if _, ok := params[id]; !ok {
			return false
		}
	}
	return true
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

// exprLoudError marks a codegen-time failure that is a user error in EVERY
// mode (e.g. an aggregation expression the VM's own compiler rejects) —
// callers error out instead of falling back.
type exprLoudError struct{ err error }

func (e *exprLoudError) Error() string { return e.err.Error() }

// exprIsLoud reports whether err demands a loud codegen error rather than a
// record fallback.
func exprIsLoud(err error) bool {
	var unknownField *exprUnknownFieldError
	var loud *exprLoudError
	return errors.As(err, &unknownField) || errors.As(err, &loud)
}

// known reports whether name resolves to a schema field or an extra binding
// (used by has/getOr/?? which fold existence at codegen time).
func (e *exprGoEnv) known(name string) bool {
	if _, ok := e.fields[strings.ToLower(name)]; ok {
		return true
	}
	if _, ok := e.advisory[strings.ToLower(name)]; ok {
		return true
	}
	if _, ok := e.params[name]; ok {
		return true
	}
	_, ok := e.vars[name]
	return ok
}

// param resolves a -param binding; handled=false means name is not one.
func (e *exprGoEnv) param(name string) (res exprGo, handled bool, err error) {
	p, ok := e.params[name]
	if !ok {
		return exprGo{}, false, nil
	}
	_, isField := e.fields[strings.ToLower(name)]
	if !isField {
		_, isField = e.advisory[strings.ToLower(name)]
	}
	if isField && !(e.closed && p.field != "") {
		return exprGo{}, true, &exprLoudError{err: fmt.Errorf("parameter %s has the same name as a field of the input; rename the parameter", name)}
	}
	if p.Type == "" {
		return exprGo{}, true, fmt.Errorf("parameter %q has a type outside the native subset", name)
	}
	return p, true, nil
}

// field resolves an identifier: schema field first (record shadows state),
// then extra bindings. Schema fields admit only the MVP scalar types. In
// record mode (advisory set), fields emit typed ssql.GetOr calls.
func (e *exprGoEnv) field(name string) (exprGo, error) {
	if res, handled, err := e.param(name); handled {
		return res, err
	}
	if e.advisory != nil {
		f, ok := e.advisory[strings.ToLower(name)]
		if !ok {
			if v, ok := e.vars[name]; ok {
				return v, nil
			}
			return exprGo{}, &exprUnknownFieldError{
				msg: fmt.Sprintf("unknown field %q (schema has %s)", name, e.fieldNames())}
		}
		switch f.GoType {
		case "int64":
			return exprGo{Src: fmt.Sprintf("ssql.GetOr(%s, %q, int64(0))", e.recv, f.Name), Type: exprGoInt}, nil
		case "float64":
			return exprGo{Src: fmt.Sprintf("ssql.GetOr(%s, %q, float64(0))", e.recv, f.Name), Type: exprGoFloat}, nil
		case "string":
			return exprGo{Src: fmt.Sprintf("ssql.GetOr(%s, %q, \"\")", e.recv, f.Name), Type: exprGoString}, nil
		case "bool":
			return exprGo{Src: fmt.Sprintf("ssql.GetOr(%s, %q, false)", e.recv, f.Name), Type: exprGoBool}, nil
		case "time.Time":
			return exprGo{Src: fmt.Sprintf("ssql.GetOr(%s, %q, time.Time{})", e.recv, f.Name), Type: exprGoTime, Imports: []string{"time"}}, nil
		}
		return exprGo{}, fmt.Errorf("field %q has advisory type %s, which has no native Go emission", name, f.GoType)
	}

	f, ok := e.fields[strings.ToLower(name)]
	if !ok {
		if v, ok := e.vars[name]; ok {
			return v, nil
		}
		return exprGo{}, &exprUnknownFieldError{
			msg: fmt.Sprintf("unknown field %q (schema has %s)", name, e.fieldNames())}
	}
	if f.JSON {
		// The struct field is the nested value's TEXT; len(), indexing and
		// member access mean the list or map, which only the VM does.
		return exprGo{}, fmt.Errorf("field %q is a nested value (json), which has no native Go emission", name)
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
	case "time.Time":
		return exprGo{Src: e.recv + "." + f.GoName, Type: exprGoTime}, nil
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
	case *ast.MemberNode:
		// #.field — the aggregation patcher's element reference (PatchAggExpr
		// rewrites `salary` to `#.salary` inside sum() predicates). Resolves
		// exactly like the bare identifier.
		if _, ok := n.Node.(*ast.PointerNode); ok {
			if prop, ok := n.Property.(*ast.StringNode); ok {
				return e.field(prop.Value)
			}
		}
		return exprGo{}, fmt.Errorf("%s has no native Go emission", exprNodeDesc(n))
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
			if !e.known(ident.Value) {
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
	return exprCompare(op, left, right, equality)
}

// exprCompare is the one comparison emission — shared by the expression
// walker and the flag-condition lowering (condOpToExprGo), so `-if pop gt X`
// and `pop > X` cannot drift.
func exprCompare(op string, left, right exprGo, equality bool) (exprGo, error) {
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
	case left.Type == exprGoTime && right.Type == exprGoTime:
		// Instants: Equal/Compare, never ==, which also compares the
		// Location (a tz() value would differ from the same instant).
		var src string
		switch op {
		case "==":
			src = left.Src + ".Equal(" + right.Src + ")"
		case "!=":
			src = "!" + left.Src + ".Equal(" + right.Src + ")"
		default:
			src = "(" + left.Src + ".Compare(" + right.Src + ") " + op + " 0)"
		}
		return mergeMeta(exprGo{Src: src, Type: exprGoBool}, left, right), nil
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
		return exprGo{Src: strconv.FormatBool(e.known(fieldName)), Type: exprGoBool}, nil
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
		if !e.known(fieldName) {
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
	case "bucket":
		// bucket(ts, "5m") — DFC121. The duration must be a literal so
		// it can be parsed at CODEGEN time (a dynamic duration would
		// need runtime parsing; VM fallback handles it). Numeric
		// timestamps only in transpiled form; string timestamps fall
		// back to the VM (layout probing is stateful there).
		if len(args) != 2 {
			return exprGo{}, fmt.Errorf("bucket() takes (timestamp, duration)")
		}
		durLit, ok := args[1].(*ast.StringNode)
		if !ok {
			return exprGo{}, fmt.Errorf("bucket() duration must be a string literal for typed codegen")
		}
		d, derr := time.ParseDuration(durLit.Value)
		if derr != nil || d <= 0 {
			return exprGo{}, fmt.Errorf("bucket() duration %q invalid: %v", durLit.Value, derr)
		}
		arg, err := e.node(args[0])
		if err != nil {
			return exprGo{}, err
		}
		switch arg.Type {
		case exprGoInt:
			res := exprGo{Src: fmt.Sprintf("exprfn.BucketInt64(%s, %d)", arg.Src, int64(d)), Type: exprGoInt, Imports: []string{exprfnImport}}
			return mergeMeta(res, arg), nil
		case exprGoFloat:
			res := exprGo{Src: fmt.Sprintf("exprfn.BucketFloat64(%s, %d)", arg.Src, int64(d)), Type: exprGoFloat, Imports: []string{exprfnImport}}
			return mergeMeta(res, arg), nil
		case exprGoTime:
			res := exprGo{Src: fmt.Sprintf("ssql.BucketTime(%s, %d)", arg.Src, int64(d)), Type: exprGoTime, Imports: []string{ssqlImport}}
			return mergeMeta(res, arg), nil
		}
		return exprGo{}, fmt.Errorf("bucket() on %s not transpilable (string timestamps use the VM fallback)", arg.Type)
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
		if name == "round" && len(args) == 2 {
			// round(x, n): half away from zero at n places (DFC153).
			x, err := e.node(args[0])
			if err != nil {
				return exprGo{}, err
			}
			n, err := e.node(args[1])
			if err != nil {
				return exprGo{}, err
			}
			if !x.Type.numeric() || n.Type != exprGoInt {
				return exprGo{}, fmt.Errorf("round(x, places) needs a number and an integer, got %s and %s", x.Type, n.Type)
			}
			res := exprGo{Src: "exprfn.RoundTo(" + asFloat(x) + ", " + n.Src + ")", Type: exprGoFloat, Imports: []string{exprfnImport}}
			return mergeMeta(res, x, n), nil
		}
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
	case "sqrt", "cbrt", "exp", "ln", "log10", "log2",
		"sin", "cos", "tan", "asin", "acos", "atan",
		"sinh", "cosh", "tanh", "asinh", "acosh", "atanh",
		"trunc", "gamma", "lgamma", "degrees", "radians":
		// DFC153: a float of a float; an int argument is promoted. A result
		// that is not a number (sqrt of a negative, ln(0), overflow) is NaN
		// or ±Inf, which the record writer makes absent, as in exec.
		arg, err := one()
		if err != nil {
			return exprGo{}, err
		}
		if !arg.Type.numeric() {
			return exprGo{}, fmt.Errorf("%s() needs a numeric argument, got %s", name, arg.Type)
		}
		fn := map[string]string{
			"sqrt": "math.Sqrt", "cbrt": "math.Cbrt", "exp": "math.Exp",
			"ln": "math.Log", "log10": "math.Log10", "log2": "math.Log2",
			"sin": "math.Sin", "cos": "math.Cos", "tan": "math.Tan",
			"asin": "math.Asin", "acos": "math.Acos", "atan": "math.Atan",
			"sinh": "math.Sinh", "cosh": "math.Cosh", "tanh": "math.Tanh",
			"asinh": "math.Asinh", "acosh": "math.Acosh", "atanh": "math.Atanh",
			"trunc": "math.Trunc", "gamma": "math.Gamma",
			"lgamma": "exprfn.Lgamma", "degrees": "exprfn.Degrees", "radians": "exprfn.Radians",
		}[name]
		imp := "math"
		if strings.HasPrefix(fn, "exprfn.") {
			imp = exprfnImport
		}
		res := exprGo{Src: fn + "(" + asFloat(arg) + ")", Type: exprGoFloat, Imports: []string{imp}}
		return mergeMeta(res, arg), nil
	case "atan2", "pow", "log":
		if name == "log" && len(args) == 1 {
			return exprGo{}, fmt.Errorf("log takes a base and a value, log(b, x); ln(x) is the natural logarithm, log10(x) base ten")
		}
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
		fn, imp := map[string]string{"atan2": "math.Atan2", "pow": "math.Pow", "log": "exprfn.Log"}[name], "math"
		if name == "log" {
			imp = exprfnImport
		}
		res := exprGo{Src: fn + "(" + asFloat(a) + ", " + asFloat(b) + ")", Type: exprGoFloat, Imports: []string{imp}}
		return mergeMeta(res, a, b), nil
	case "pi":
		if len(args) != 0 {
			return exprGo{}, fmt.Errorf("pi() takes no argument")
		}
		return exprGo{Src: "math.Pi", Type: exprGoFloat, Imports: []string{"math"}}, nil
	case "sign":
		arg, err := one()
		if err != nil {
			return exprGo{}, err
		}
		if !arg.Type.numeric() {
			return exprGo{}, fmt.Errorf("sign() needs a numeric argument, got %s", arg.Type)
		}
		res := exprGo{Src: "exprfn.Sign(" + arg.Src + ")", Type: exprGoInt, Imports: []string{exprfnImport}}
		return mergeMeta(res, arg), nil
	case "isnan", "isinf", "isfinite":
		arg, err := one()
		if err != nil {
			return exprGo{}, err
		}
		if !arg.Type.numeric() {
			return exprGo{}, fmt.Errorf("%s() needs a numeric argument, got %s", name, arg.Type)
		}
		var src string
		switch name {
		case "isnan":
			src = "math.IsNaN(" + asFloat(arg) + ")"
		case "isinf":
			src = "math.IsInf(" + asFloat(arg) + ", 0)"
		default:
			src = "(!math.IsNaN(" + asFloat(arg) + ") && !math.IsInf(" + asFloat(arg) + ", 0))"
		}
		res := exprGo{Src: src, Type: exprGoBool, Imports: []string{"math"}}
		return mergeMeta(res, arg), nil
	case "gcd", "lcm", "factorial":
		want := 2
		if name == "factorial" {
			want = 1
		}
		if len(args) != want {
			return exprGo{}, fmt.Errorf("%s() takes exactly %d argument(s)", name, want)
		}
		var parts []string
		var kids []exprGo
		for _, a := range args {
			v, err := e.node(a)
			if err != nil {
				return exprGo{}, err
			}
			if v.Type != exprGoInt {
				return exprGo{}, fmt.Errorf("%s() needs integer arguments, got %s", name, v.Type)
			}
			parts = append(parts, v.Src)
			kids = append(kids, v)
		}
		fn := map[string]string{"gcd": "exprfn.GCD", "lcm": "exprfn.LCM", "factorial": "exprfn.Factorial"}[name]
		res := exprGo{Src: fn + "(" + strings.Join(parts, ", ") + ")", Type: exprGoInt, Imports: []string{exprfnImport}}
		return mergeMeta(res, kids...), nil
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
	if res, handled, err := e.timeCall(name, args); handled {
		return res, err
	}
	return exprGo{}, fmt.Errorf("function %q has no native Go emission", name)
}

// timeCall transpiles the calendar functions (DFC147 §4) over a time
// operand: parts, date_trunc, date_add, date_diff, tz, now, and bucket
// over a time. Units and zones must be literals (validated here, so the
// Must* helpers cannot fail at run time). A string operand is refused —
// the VM parses it per row; a typed program casts the column.
func (e *exprGoEnv) timeCall(name string, args []ast.Node) (exprGo, bool, error) {
	isPart := false
	for _, p := range ssql.TimeParts {
		if p == name {
			isPart = true
		}
	}
	lit := func(what string, n ast.Node) (string, error) {
		s, ok := n.(*ast.StringNode)
		if !ok {
			return "", fmt.Errorf("%s() needs a literal %s for native Go emission", name, what)
		}
		return s.Value, nil
	}
	timeArg := func(n ast.Node) (exprGo, error) {
		v, err := e.node(n)
		if err != nil {
			return exprGo{}, err
		}
		if v.Type != exprGoTime {
			return exprGo{}, fmt.Errorf("%s() over %s: a time column is needed (cast -type F time); the VM reads strings", name, v.Type)
		}
		return v, nil
	}
	switch {
	case isPart:
		if len(args) != 1 {
			return exprGo{}, true, fmt.Errorf("%s() takes one argument", name)
		}
		v, err := timeArg(args[0])
		if err != nil {
			return exprGo{}, true, err
		}
		res := exprGo{Src: fmt.Sprintf("ssql.MustTimePart(%s, %q, %s)", v.Src, name, zoneSrc(v)), Type: exprGoInt, Imports: []string{ssqlImport}}
		return mergeMeta(res, v), true, nil
	case name == "date_trunc":
		if len(args) != 2 {
			return exprGo{}, true, fmt.Errorf("date_trunc(ts, unit) takes two arguments")
		}
		unit, err := lit("unit", args[1])
		if err != nil {
			return exprGo{}, true, err
		}
		if !ssql.ValidCalendarUnit(unit) {
			return exprGo{}, true, fmt.Errorf("date_trunc: unit %q is not one of %s", unit, strings.Join(ssql.CalendarUnits, ", "))
		}
		v, err := timeArg(args[0])
		if err != nil {
			return exprGo{}, true, err
		}
		res := exprGo{Src: fmt.Sprintf("ssql.MustTruncTime(%s, %q, %s)", v.Src, unit, zoneSrc(v)), Type: exprGoTime, Imports: []string{ssqlImport}, zoneVar: v.zoneVar}
		return mergeMeta(res, v), true, nil
	case name == "date_add":
		if len(args) != 3 {
			return exprGo{}, true, fmt.Errorf("date_add(ts, n, unit) takes three arguments")
		}
		unit, err := lit("unit", args[2])
		if err != nil {
			return exprGo{}, true, err
		}
		if !ssql.ValidCalendarUnit(unit) {
			return exprGo{}, true, fmt.Errorf("date_add: unit %q is not one of %s", unit, strings.Join(ssql.CalendarUnits, ", "))
		}
		v, err := timeArg(args[0])
		if err != nil {
			return exprGo{}, true, err
		}
		n, err := e.node(args[1])
		if err != nil {
			return exprGo{}, true, err
		}
		if n.Type != exprGoInt {
			return exprGo{}, true, fmt.Errorf("date_add: n must be an integer, got %s", n.Type)
		}
		res := exprGo{Src: fmt.Sprintf("ssql.MustAddCalendar(%s, int64(%s), %q, %s)", v.Src, n.Src, unit, zoneSrc(v)), Type: exprGoTime, Imports: []string{ssqlImport}, zoneVar: v.zoneVar}
		return mergeMeta(res, v, n), true, nil
	case name == "date_diff":
		if len(args) != 3 {
			return exprGo{}, true, fmt.Errorf("date_diff(unit, start, end) takes three arguments")
		}
		unit, err := lit("unit", args[0])
		if err != nil {
			return exprGo{}, true, err
		}
		if !ssql.ValidCalendarUnit(unit) {
			return exprGo{}, true, fmt.Errorf("date_diff: unit %q is not one of %s", unit, strings.Join(ssql.CalendarUnits, ", "))
		}
		a, err := timeArg(args[1])
		if err != nil {
			return exprGo{}, true, err
		}
		b, err := timeArg(args[2])
		if err != nil {
			return exprGo{}, true, err
		}
		res := exprGo{Src: fmt.Sprintf("ssql.MustDiffCalendar(%q, %s, %s, %s)", unit, a.Src, b.Src, zoneSrc(a)), Type: exprGoInt, Imports: []string{ssqlImport}}
		return mergeMeta(res, a, b), true, nil
	case name == "tz":
		if len(args) != 2 {
			return exprGo{}, true, fmt.Errorf("tz(ts, zone) takes two arguments")
		}
		zone, err := lit("zone", args[1])
		if err != nil {
			return exprGo{}, true, err
		}
		if _, err := time.LoadLocation(zone); err != nil {
			return exprGo{}, true, fmt.Errorf("tz: %v", err)
		}
		v, err := timeArg(args[0])
		if err != nil {
			return exprGo{}, true, err
		}
		varName := "exprLoc" + exprGoHash(zone)
		decl := fmt.Sprintf("var %s = ssql.MustLoadZone(%s)", varName, strconv.Quote(zone))
		res := exprGo{Src: v.Src + ".In(" + varName + ")", Type: exprGoTime, Imports: []string{ssqlImport}, Hoisted: []string{decl}, zoneVar: varName}
		return mergeMeta(res, v), true, nil
	case name == "now":
		if len(args) != 0 {
			return exprGo{}, true, fmt.Errorf("now() takes no arguments")
		}
		return exprGo{Src: "time.Now().In(ssql.StreamLocation())", Type: exprGoTime, Imports: []string{"time", ssqlImport}}, true, nil
	case name == "strftime":
		if len(args) != 2 {
			return exprGo{}, true, fmt.Errorf("strftime(ts, pattern) takes two arguments")
		}
		pattern, err := lit("pattern", args[1])
		if err != nil {
			return exprGo{}, true, err
		}
		if _, _, err := ssql.StrftimeLayout(pattern); err != nil {
			return exprGo{}, true, err
		}
		v, err := timeArg(args[0])
		if err != nil {
			return exprGo{}, true, err
		}
		res := exprGo{Src: fmt.Sprintf("ssql.MustFormatStrftime(%s, %s, %s)", v.Src, strconv.Quote(pattern), zoneSrc(v)), Type: exprGoString, Imports: []string{ssqlImport}}
		return mergeMeta(res, v), true, nil
	case name == "strptime", name == "date" && len(args) == 2:
		pattern, err := lit("pattern", args[1])
		if err != nil {
			return exprGo{}, true, err
		}
		if _, err := ssql.TimeLayout(pattern); err != nil {
			return exprGo{}, true, err
		}
		v, err := e.node(args[0])
		if err != nil {
			return exprGo{}, true, err
		}
		if v.Type != exprGoString {
			return exprGo{}, true, fmt.Errorf("%s(): the text must be a string, got %s", name, v.Type)
		}
		fieldName, _ := ssql.ExprFieldName(args[0]) // names the column in the error a bad value raises
		res := exprGo{Src: fmt.Sprintf("ssql.MustParseTimeLayout(%s, %s, %q)", v.Src, strconv.Quote(pattern), fieldName), Type: exprGoTime, Imports: []string{ssqlImport}}
		return mergeMeta(res, v), true, nil
	}
	return exprGo{}, false, nil
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

// ---- Tier V: VM with a generated static env (expr-transpiler Phase 1.5) ----
//
// When exprToGo refuses an expression, typed emission doesn't have to eject
// the whole stage to Record mode: the generated code evaluates the expression
// with the expr-lang VM against an env map built from the struct. The stage
// stays Stream[T]/Seq[T] — one exotic expression no longer downgrades every
// downstream stage. Costs the map alloc + VM dispatch that Tier N avoids.

// Aliased because parallel-mode programs also import Go's stdlib "runtime"
// (GOMAXPROCS). The "alias path" form (no quotes) is rendered by the
// assemblers' renderImport as `alias "path"`.
const exprRuntimeImport = "exprvm github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime"

// exprGoHash is the content-addressing suffix for hoisted expression vars —
// identical expressions dedupe to one package-level decl.
func exprGoHash(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// exprEnvConstructor renders the per-schema env-builder function the Tier-V
// call sites feed: one map literal with every schema field. Content-addressed
// by type name; the assembler dedupes the identical decls.
func exprEnvConstructor(schema *lib.TypedSchema) (name string, decl string) {
	name = "exprEnv" + schema.TypeName
	var b strings.Builder
	fmt.Fprintf(&b, "// %s builds the expr-lang env for Tier-V (VM) expressions over %s.\n", name, schema.TypeName)
	fmt.Fprintf(&b, "func %s(r %s) map[string]any {\n\treturn map[string]any{\n", name, schema.TypeName)
	for _, f := range schema.Fields {
		if f.JSON {
			// A nested value: the expression sees the list or map, not
			// the text (DFC144 Level 1), as exec's environment does.
			fmt.Fprintf(&b, "\t\t%q: ssql.ExprValue(ssql.JSONString(r.%s)),\n", f.Name, f.GoName)
			continue
		}
		fmt.Fprintf(&b, "\t\t%q: r.%s,\n", f.Name, f.GoName)
	}
	b.WriteString("\t}\n}")
	return name, b.String()
}

// exprTierVValidate confirms the expression at least compiles in the VM.
// When it doesn't, the expression is invalid in EVERY mode — the caller
// should error loudly at codegen instead of emitting a program that panics
// at startup.
func exprTierVValidate(expression string) error {
	_, err := runtime.CompileExprEnv(expression)
	return err
}

// exprTierVFilter builds the Tier-V predicate call for -if-expr: a hoisted
// MustCompileExprFilterEnv var (false on eval error / non-bool, matching the
// record path) applied to the schema's env constructor.
func exprTierVFilter(expression string, schema *lib.TypedSchema) (call string, imports, hoisted []string, err error) {
	return exprTierVFilterParams(expression, schema, nil)
}

// exprTierVFilterParams is exprTierVFilter with -param bindings: the
// parameters ride in a thunk read after flag.Parse (runtime.Params).
func exprTierVFilterParams(expression string, schema *lib.TypedSchema, params []ExprParam) (call string, imports, hoisted []string, err error) {
	return exprTierV("exprFilterEnv", "MustCompileExprFilterEnv", expression, schema, params)
}

// exprTierVEval builds the Tier-V evaluation pieces for -set-expr: a hoisted
// MustCompileExprEnv var and the call source producing (any, error).
func exprTierVEval(expression string, schema *lib.TypedSchema) (call string, imports, hoisted []string, err error) {
	return exprTierVEvalParams(expression, schema, nil)
}

func exprTierVEvalParams(expression string, schema *lib.TypedSchema, params []ExprParam) (call string, imports, hoisted []string, err error) {
	return exprTierV("exprEvalEnv", "MustCompileExprEnv", expression, schema, params)
}

func exprTierV(varPrefix, compileFn, expression string, schema *lib.TypedSchema, params []ExprParam) (call string, imports, hoisted []string, err error) {
	if err := exprTierVValidate(expression); err != nil {
		return "", nil, nil, err
	}
	envFn, envDecl := exprEnvConstructor(schema)
	imports = []string{exprRuntimeImport}
	thunk := exprParamsGoThunk(params)
	// The typed runtime import is aliased exprvm; the FieldParams literal
	// names the package as runtime, so spell it for the alias.
	fields := strings.Replace(exprParamsGoFields(params), "runtime.FieldParams", "exprvm.FieldParams", 1)
	// The hoisted var is content-addressed; the bindings are part of the
	// content (the same text with other parameters is another predicate).
	v := varPrefix + exprGoHash(expression+thunk+fields)
	var decl string
	switch {
	case fields != "":
		if thunk == "" {
			thunk = "nil"
		}
		decl = fmt.Sprintf("var %s = exprvm.%sParamsFields(%q, %s, %s)", v, compileFn, expression, thunk, fields)
	case thunk != "":
		decl = fmt.Sprintf("var %s = exprvm.%sParams(%q, %s)", v, compileFn, expression, thunk)
	default:
		decl = fmt.Sprintf("var %s = exprvm.%s(%q)", v, compileFn, expression)
	}
	if exprParamsNeedSSQL(params) {
		imports = append(imports, "github.com/rosscartlidge/ssql/v4")
	}
	return v + "(" + envFn + "(r))", imports, []string{envDecl, decl}, nil
}

// exprTierReason renders an exprToGo refusal for a -explain plan note,
// trimming the redundant "expression …:" prefix.
func exprTierReason(expression string, err error) string {
	return strings.TrimPrefix(err.Error(), fmt.Sprintf("expression %q: ", expression))
}

// exprCoerceFunc maps a typed column's Go type to the runtime coercion
// helper that types a Tier-V result for assignment (loud exit on mismatch —
// a typed column cannot retype). ok=false for column types with no coercion
// (time.Time etc.), which forces record fallback.
func exprCoerceFunc(f lib.TypedSchemaField) (string, bool) {
	if f.JSON {
		return "exprvm.MustCoerceJSON", true
	}
	switch f.GoType {
	case "int64":
		return "exprvm.MustCoerceInt64", true
	case "float64":
		return "exprvm.MustCoerceFloat64", true
	case "string":
		return "exprvm.MustCoerceString", true
	case "bool":
		return "exprvm.MustCoerceBool", true
	case "time.Time":
		return "exprvm.MustCoerceTime", true
	}
	return "", false
}
