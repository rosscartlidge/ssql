package commands

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime"
)

// Expression parameters, `-param NAME TYPE VALUE` (DFC134 §5.3).
//
// An expression argument is the one place ssql has a string grammar, so
// splicing a value into it is SQL injection with another grammar. A
// parameter binds NAME as a variable of the expression's environment: the
// program's author fixes the text (`price * rate`), the value arrives in a
// flag slot bound by arity, and nothing untrusted ever passes through the
// expression parser. TYPE is declared, never inferred from the value's
// spelling (that is the zero-padded-number bug of DFC133); a VALUE not of
// TYPE is an error at parse time.
//
// Scope is the clause: every expression in the clause sees every -param
// written in it, wherever in the clause it appears (the scope autocli gives
// .Local() flags). A parameter named like a field of the input, or one no
// expression in the clause uses, is an error.
//
// Lowering: exec puts the value in the env; generated Go lifts it into a
// flag of the binary (-param-NAME) so the program is a prepared statement,
// re-runnable with new values; generate sql renders a literal by TYPE.

// ExprParam is one parsed -param.
type ExprParam struct {
	Name  string
	Type  ssql.FieldType
	Value any // int64 / float64 / string / bool / time.Time, by Type
	raw   string
}

var exprParamName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// exprParamReserved are expr-lang keywords a parameter cannot be called.
var exprParamReserved = map[string]bool{
	"and": true, "or": true, "not": true, "in": true, "nil": true, "true": true, "false": true,
	"let": true, "if": true, "else": true, "matches": true, "contains": true, "startsWith": true, "endsWith": true,
}

// exprParamFlag registers -param on a subcommand that takes expressions.
func exprParamFlag(sb *cf.SubcommandBuilder) *cf.SubcommandBuilder {
	return sb.
		Flag("-param", "-p").
		Arg("name").
		Completer(cf.NoCompleter{Hint: "<identifier>"}).
		Done().
		Arg("type").
		Completer(&cf.StaticCompleter{Options: []string{"string", "int", "float", "bool", "time"}}).
		Done().
		Arg("value").
		Completer(cf.NoCompleter{Hint: "<value>"}).
		Done().
		Accumulate().
		Local().
		Help("Bind NAME as a variable of this clause's expressions: -param <name> <type> <value>; the value is data, never expression text").
		Done()
}

// parseExprParams reads a clause's -param entries.
func parseExprParams(flagValue any) ([]ExprParam, error) {
	list, ok := flagValue.([]any)
	if !ok {
		return nil, nil
	}
	var out []ExprParam
	seen := map[string]bool{}
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		typ, _ := m["type"].(string)
		value, _ := m["value"].(string)
		if !exprParamName.MatchString(name) || exprParamReserved[name] {
			return nil, fmt.Errorf("-param: %q is not a valid parameter name (letters, digits and _, not starting with a digit, not an expression keyword)", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("-param %s: declared twice in one clause", name)
		}
		seen[name] = true
		ft, err := ssql.ParseFieldType(typ)
		if err != nil || ft == ssql.FieldTypeAuto {
			return nil, fmt.Errorf("-param %s: type must be one of string, int, float, bool, time (got %q)", name, typ)
		}
		v, ok := ssql.CastValue(value, ft)
		if !ok {
			return nil, fmt.Errorf("-param %s: %q is not a valid %s", name, value, ft)
		}
		out = append(out, ExprParam{Name: name, Type: ft, Value: v, raw: value})
	}
	return out, nil
}

// checkExprParamsUsed rejects a parameter that none of the clause's
// expressions refers to. A mistyped parameter name must not pass quietly.
func checkExprParamsUsed(params []ExprParam, expressions []string) error {
	if len(params) == 0 {
		return nil
	}
	used := map[string]bool{}
	for _, e := range expressions {
		ids, err := runtime.ExprIdentifiers(e)
		if err != nil {
			return err
		}
		for _, id := range ids {
			used[id] = true
		}
	}
	var unused []string
	for _, p := range params {
		if !used[p.Name] {
			unused = append(unused, p.Name)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		return fmt.Errorf("-param %s: no expression in the clause uses it", strings.Join(unused, ", "))
	}
	return nil
}

// exprParamsEnv is the interpreter's binding.
func exprParamsEnv(params []ExprParam) runtime.Params {
	if len(params) == 0 {
		return nil
	}
	m := make(map[string]any, len(params))
	for _, p := range params {
		m[p.Name] = p.Value
	}
	return runtime.StaticParams(m)
}

// exprParamsExpressions lists the expression texts of a clause for the
// unused check: -if-expr / +if-expr and the -set-expr family.
func exprParamsExpressions(conds []ExprCond, sets []setExpr) []string {
	var out []string
	for _, c := range conds {
		out = append(out, c.Expression)
	}
	for _, s := range sets {
		out = append(out, s.expression)
	}
	return out
}

// Generated code. Each parameter becomes a flag of the binary, -param-NAME,
// with the pipeline's value as its default (the same lifting `where -if`
// and `limit` do), so the compiled program is re-runnable with new values.

func (p ExprParam) flagName() string { return "param-" + strings.ReplaceAll(p.Name, "_", "-") }
func (p ExprParam) varName() string  { return "flagParam" + flagVarName(p.Name) }

// codeParam is the flag declaration. Time has no flag.Duration-like
// parser, so it travels as a string and is parsed at first use.
func (p ExprParam) codeParam() lib.CodeParam {
	cp := lib.CodeParam{
		Name:    p.flagName(),
		Default: p.raw,
		Help:    fmt.Sprintf("expression parameter %s (%s)", p.Name, p.Type),
		VarName: p.varName(),
	}
	// The default is the CAST value's canonical spelling, so it is a valid
	// Go literal of the flag's kind (a bool written "yes" becomes true).
	switch v := p.Value.(type) {
	case int64:
		cp.Type, cp.Default = "int", strconv.FormatInt(v, 10)
	case float64:
		cp.Type, cp.Default = "float", strconv.FormatFloat(v, 'g', -1, 64)
	case bool:
		cp.Type, cp.Default = "bool", strconv.FormatBool(v)
	}
	return cp
}

// goValue is the Go expression reading the parameter after flag.Parse.
func (p ExprParam) goValue() string {
	switch p.Type {
	case ssql.FieldTypeInt:
		return "int64(*" + p.varName() + ")"
	case ssql.FieldTypeTime:
		return "ssql.MustParseTime(*" + p.varName() + ", " + strconv.Quote("-"+p.flagName()) + ")"
	}
	return "*" + p.varName()
}

// exprParamsCodeParams collects the flag declarations.
func exprParamsCodeParams(params []ExprParam) []lib.CodeParam {
	var out []lib.CodeParam
	for _, p := range params {
		out = append(out, p.codeParam())
	}
	return out
}

// exprParamsGoVars gives the native transpiler a binding per parameter.
// Time is outside the transpiler's type lattice; an expression that uses a
// time parameter refuses natively and takes the VM path.
func exprParamsGoVars(params []ExprParam) map[string]exprGo {
	if len(params) == 0 {
		return nil
	}
	vars := make(map[string]exprGo, len(params))
	for _, p := range params {
		var t exprGoType
		switch p.Type {
		case ssql.FieldTypeInt:
			t = exprGoInt
		case ssql.FieldTypeFloat:
			t = exprGoFloat
		case ssql.FieldTypeString:
			t = exprGoString
		case ssql.FieldTypeBool:
			t = exprGoBool
		default:
			// Type "" is the transpiler's cue to refuse quietly (VM tier).
		}
		vars[p.Name] = exprGo{Src: p.goValue(), Type: t}
	}
	return vars
}

// exprParamsGoThunk renders the runtime.Params argument for the VM forms:
// a func literal so the flags are read after flag.Parse. Empty when there
// are no parameters (callers then emit the plain compile call).
func exprParamsGoThunk(params []ExprParam) string {
	if len(params) == 0 {
		return ""
	}
	sorted := append([]ExprParam(nil), params...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	parts := make([]string, len(sorted))
	for i, p := range sorted {
		parts[i] = strconv.Quote(p.Name) + ": " + p.goValue()
	}
	return "func() map[string]any { return map[string]any{" + strings.Join(parts, ", ") + "} }"
}

// exprParamsNeedSSQL reports whether the thunk references the ssql package
// (time parsing), so the emitter can add the import.
func exprParamsNeedSSQL(params []ExprParam) bool {
	for _, p := range params {
		if p.Type == ssql.FieldTypeTime {
			return true
		}
	}
	return false
}

// SQL. A parameter renders as a literal of its declared type; the value
// goes through the same quoting every string literal does.
func (p ExprParam) sqlLiteral() string {
	switch v := p.Value.(type) {
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case bool:
		if v {
			return "TRUE"
		}
		return "FALSE"
	case time.Time:
		return "TIMESTAMP '" + v.UTC().Format("2006-01-02 15:04:05.999999") + "'"
	}
	return "'" + escapeSQL(p.raw) + "'"
}

// sqlClauseParams reads the -param triples of a stage's argv, one map per
// clause (clauses split on sep, the command's separator), for the SQL
// translators, which walk argv themselves. Values are validated exactly as
// the interpreter validates them; a clause's expressions are checked for
// use of every parameter by the interpreter, not here.
func sqlClauseParams(args []string, sep string) ([]map[string]ExprParam, error) {
	var out []map[string]ExprParam
	var list []any
	flush := func() error {
		var m map[string]ExprParam
		if list != nil {
			params, err := parseExprParams(list)
			if err != nil {
				return err
			}
			m = make(map[string]ExprParam, len(params))
			for _, p := range params {
				m[p.Name] = p
			}
		}
		out = append(out, m)
		list = nil
		return nil
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-param", "-p":
			if i+3 >= len(args) {
				return nil, fmt.Errorf("incomplete -param")
			}
			list = append(list, map[string]any{"name": args[i+1], "type": args[i+2], "value": args[i+3]})
			i += 3
		case sep:
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// clauseExprParams parses a clause's -param flags and checks each is used
// by one of the clause's expressions.
func clauseExprParams(clause cf.Clause, conds []ExprCond, sets []setExpr) ([]ExprParam, error) {
	params, err := parseExprParams(clause.Flags["-param"])
	if err != nil {
		return nil, err
	}
	if err := checkExprParamsUsed(params, exprParamsExpressions(conds, sets)); err != nil {
		return nil, err
	}
	return params, nil
}

// exprVMCompileDecl renders the record-mode pre-compiled VM var: the plain
// compile call, or the Params form when the clause has parameters.
func exprVMCompileDecl(varName, compileFn, expression, thunk string) string {
	if thunk == "" {
		return fmt.Sprintf("var %s = runtime.%s(%q)", varName, compileFn, expression)
	}
	return fmt.Sprintf("var %s = runtime.%sParams(%q, %s)", varName, compileFn, expression, thunk)
}

// isUnknownField reports the transpiler's unknown-field refusal, which
// record mode treats as "let the VM validate against the real first record"
// rather than a loud error.
func isUnknownField(err error) bool {
	var unknownField *exprUnknownFieldError
	return errors.As(err, &unknownField)
}
