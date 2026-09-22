package runtime

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/expr-lang/expr/parser"
	"github.com/rosscartlidge/ssql/v4"
)

// Expression parameters (DFC134 §5.3): `-param NAME TYPE VALUE` binds NAME
// as a VARIABLE of the expression's environment, exactly as a field is
// bound. The value never passes through the expression grammar, so a
// program can put untrusted text in it the way a prepared statement takes a
// bound value; splicing the same text into the expression source is SQL
// injection with another grammar.
//
// Params is a thunk rather than a map because generated programs lift each
// parameter into a flag of the binary and package-level vars are
// initialised before flag.Parse runs: the values are read on first
// evaluation, once, after main has parsed the flags.
type Params func() map[string]any

// StaticParams wraps values known at compile time (the interpreter's case).
func StaticParams(m map[string]any) Params {
	if len(m) == 0 {
		return nil
	}
	return func() map[string]any { return m }
}

// ErrAbsent: a -param-field's column is absent on this row, so the
// expression has no value. Callers treat it as "false" for a condition and
// "assign nothing" for a set, quietly; it is not a failure.
var ErrAbsent = errors.New("expression has no value: a -param-field column is absent on this row")

// ParamCollisionError: a parameter shares its name with a field of the
// input. It is wrong for the whole run, not for the row, so commands stop
// on it rather than treating it as a per-row evaluation failure.
type ParamCollisionError struct{ Names []string }

func (e *ParamCollisionError) Error() string {
	return fmt.Sprintf("parameter %s has the same name as a field of the input; rename the parameter", strings.Join(e.Names, ", "))
}

// FieldParam is `-param-field NAME TYPE FIELD` (DFC135): NAME is bound per
// row to FIELD's value cast to Type. An absent FIELD leaves NAME unbound on
// that row (a condition on it is then false, its negation true); a value
// not of Type is loud, as `cast` is.
type FieldParam struct {
	Field string
	Type  ssql.FieldType
}

// FieldParams maps parameter names to the field each views.
type FieldParams map[string]FieldParam

// paramBinding resolves a Params thunk once and checks, on the first record
// seen, that no parameter shares a name with a field of the input. Silent
// precedence either way would let data (a new upstream column) change what
// an expression means; the collision is an error naming both. Field
// parameters are re-bound on every record.
type paramBinding struct {
	thunk   Params
	fields  FieldParams
	once    sync.Once
	values  map[string]any
	checked bool
}

// names lists every parameter the binding declares, static and field.
func (b *paramBinding) names() []string {
	if b == nil {
		return nil
	}
	var out []string
	for n := range b.resolve() {
		out = append(out, n)
	}
	for n := range b.fields {
		out = append(out, n)
	}
	return out
}

func (b *paramBinding) resolve() map[string]any {
	if b == nil || b.thunk == nil {
		return nil
	}
	b.once.Do(func() { b.values = b.thunk() })
	return b.values
}

// apply adds the parameters to env and, once, checks for a field collision
// using has (true when the input record carries that name); get reads a
// field for the field parameters.
func (b *paramBinding) apply(env map[string]any, has func(string) bool, get func(string) (any, bool)) error {
	if b == nil {
		return nil
	}
	values := b.resolve()
	if len(values) == 0 && len(b.fields) == 0 {
		return nil
	}
	if !b.checked {
		b.checked = true
		var clash []string
		for _, name := range b.names() {
			if has(name) {
				clash = append(clash, name)
			}
		}
		if len(clash) > 0 {
			sort.Strings(clash)
			return &ParamCollisionError{Names: clash}
		}
	}
	for k, v := range values {
		env[k] = v
	}
	for name, fp := range b.fields {
		v, ok := get(fp.Field)
		if !ok || v == nil {
			// The absent-value rule (DFC124/DFC128): an expression over a
			// missing value has no value. A condition is then false, a
			// -set-expr assigns nothing. (Binding nothing and letting the
			// VM see nil made `l != r` TRUE on an absent r while SQL's
			// NULL made it false; found by the random tester.)
			return ErrAbsent
		}
		cast, ok := ssql.CastValue(v, fp.Type)
		if !ok {
			return &ssql.CastError{Field: fp.Field, Value: v, Target: fp.Type}
		}
		env[name] = cast
	}
	return nil
}

// ExprIdentifiers returns the bare identifiers an expression refers to
// (fields and parameters; not calls, not member properties), each once, in
// order of first appearance. Commands use it to reject a parameter no
// expression in the clause uses: a mistyped name must not pass quietly.
func ExprIdentifiers(expression string) ([]string, error) {
	tree, err := parser.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("expression %q: %w", expression, err)
	}
	return extractIdentifiers(tree.Node), nil
}

// CompileExprParams is CompileExpr with parameters bound into the
// environment (see Params).
func CompileExprParams(expression string, params Params) (func(ssql.Record) (any, error), error) {
	return compileExpr(expression, &paramBinding{thunk: params})
}

// CompileExprParamsFields is CompileExprParams with per-row field
// parameters (see FieldParam).
func CompileExprParamsFields(expression string, params Params, fields FieldParams) (func(ssql.Record) (any, error), error) {
	return compileExpr(expression, &paramBinding{thunk: params, fields: fields})
}

func MustCompileExprParamsFields(expression string, params Params, fields FieldParams) func(ssql.Record) (any, error) {
	eval, err := CompileExprParamsFields(expression, params, fields)
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return eval
}

func MustCompileExprFilterParamsFields(expression string, params Params, fields FieldParams) func(ssql.Record) bool {
	eval, err := CompileExprParamsFields(expression, params, fields)
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return exprFilter(eval)
}

// The Tier-V (typed codegen) forms with field parameters: the field is
// read from the caller's env map.
func MustCompileExprEnvParamsFields(expression string, params Params, fields FieldParams) func(map[string]any) (any, error) {
	eval, err := compileExprEnv(expression, &paramBinding{thunk: params, fields: fields})
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return eval
}

func MustCompileExprFilterEnvParamsFields(expression string, params Params, fields FieldParams) func(map[string]any) bool {
	eval := MustCompileExprEnvParamsFields(expression, params, fields)
	return func(f map[string]any) bool {
		result, err := eval(f)
		if err != nil {
			return false
		}
		b, ok := result.(bool)
		return ok && b
	}
}

// CompileExprFilterParams is CompileExprFilter with parameters.
func CompileExprFilterParams(expression string, params Params) (func(ssql.Record) bool, error) {
	eval, err := CompileExprParams(expression, params)
	if err != nil {
		return nil, err
	}
	return exprFilter(eval), nil
}

// MustCompileExprParams and MustCompileExprFilterParams are the generated
// code forms: the expression was validated at codegen, so a compile failure
// is a bug and panics.
func MustCompileExprParams(expression string, params Params) func(ssql.Record) (any, error) {
	eval, err := CompileExprParams(expression, params)
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return eval
}

func MustCompileExprFilterParams(expression string, params Params) func(ssql.Record) bool {
	filter, err := CompileExprFilterParams(expression, params)
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return filter
}

// CompileExprEnvParams and CompileExprFilterEnvParams are the Tier-V
// (typed codegen) forms: the caller's field map plus the parameters.
func CompileExprEnvParams(expression string, params Params) (func(map[string]any) (any, error), error) {
	return compileExprEnv(expression, &paramBinding{thunk: params})
}

func MustCompileExprEnvParams(expression string, params Params) func(map[string]any) (any, error) {
	eval, err := CompileExprEnvParams(expression, params)
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return eval
}

func MustCompileExprFilterEnvParams(expression string, params Params) func(map[string]any) bool {
	eval, err := CompileExprEnvParams(expression, params)
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return func(fields map[string]any) bool {
		result, err := eval(fields)
		if err != nil {
			return false
		}
		b, ok := result.(bool)
		return ok && b
	}
}
