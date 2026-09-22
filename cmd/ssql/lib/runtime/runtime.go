package runtime

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/rosscartlidge/ssql/v4"
)

// Hash functions for expressions
func hashSHA256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func hashSHA1(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func hashMD5(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// replaceRegex replaces all matches of a regex pattern in a string.
func replaceRegex(s, pattern, replacement string) string {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return s
	}
	return re.ReplaceAllString(s, replacement)
}

// CompileExprFilter compiles a boolean expression once and returns a filter function.
// The returned function can be used repeatedly on different records.
// This is much more efficient than compiling the expression for each record.
func CompileExprFilter(expression string) (func(ssql.Record) bool, error) {
	eval, err := CompileExpr(expression)
	if err != nil {
		return nil, err
	}
	return exprFilter(eval), nil
}

// exprFilter is the predicate view of an evaluator: false on an evaluation
// error and on a non-boolean result.
func exprFilter(eval func(ssql.Record) (any, error)) func(ssql.Record) bool {
	return func(r ssql.Record) bool {
		result, err := eval(r)
		if err != nil {
			return false
		}
		boolResult, ok := result.(bool)
		return ok && boolResult
	}
}

// MustCompileExprFilter is like CompileExprFilter but panics on error.
// Use this in generated code to fail fast at program startup if expressions are invalid.
func MustCompileExprFilter(expression string) func(ssql.Record) bool {
	filter, err := CompileExprFilter(expression)
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return filter
}

// CompileExpr compiles an expression once and returns an evaluator function.
// The returned function can be used repeatedly on different records.
// The result can be any type (int, float, bool, string, etc.)
func CompileExpr(expression string) (func(ssql.Record) (any, error), error) {
	return compileExpr(expression, nil)
}

// exprHelpers are the functions every expression environment carries.
func exprHelpers(env map[string]any, has func(string) bool, get func(string) (any, bool)) {
	env["has"] = has
	env["getOr"] = func(field string, defaultValue any) any {
		if val, exists := get(field); exists {
			return val
		}
		return defaultValue
	}
	env["sha256"] = hashSHA256
	env["bucket"] = bucketFn
	env["sha1"] = hashSHA1
	env["md5"] = hashMD5
	env["replaceRegex"] = replaceRegex
}

func compileExpr(expression string, params *paramBinding) (func(ssql.Record) (any, error), error) {
	// Compile the expression once with a sample environment for type inference
	sampleEnv := make(map[string]interface{})
	exprHelpers(sampleEnv, func(string) bool { return false }, func(string) (any, bool) { return nil, false })

	program, err := expr.Compile(expression,
		expr.Env(sampleEnv),
		expr.AllowUndefinedVariables(),
		ssql.ExprFieldShadowing(),
		ssql.ExprDate(),
	)
	if err != nil {
		return nil, fmt.Errorf("compile expression: %w", err)
	}

	// Extract identifiers from the AST so we can validate fields on first record.
	// Skip validation if the expression handles missing fields explicitly via ??, has(), or getOr().
	handlesMissing := strings.Contains(expression, "??") ||
		strings.Contains(expression, "has(") ||
		strings.Contains(expression, "getOr(")
	var identifiers []string
	if !handlesMissing {
		identifiers = extractIdentifiers(program.Node())
	}

	// Return a closure that evaluates the compiled program on a record
	validated := false
	return func(record ssql.Record) (any, error) {
		// Build environment with all record fields
		env := make(map[string]interface{})
		for k, v := range record.All() {
			env[k] = v
		}
		has := func(field string) bool {
			_, exists := ssql.Get[any](record, field)
			return exists
		}
		get := func(field string) (any, bool) { return ssql.Get[any](record, field) }
		if err := params.apply(env, has, get); err != nil {
			return nil, err
		}

		// Add helper functions that close over the record
		exprHelpers(env, has, get)

		// On first record, check that expression identifiers exist as fields
		// or known functions. A declared parameter is known even when its
		// field is absent on this row.
		if !validated {
			validated = true
			declared := map[string]bool{}
			for _, n := range params.names() {
				declared[n] = true
			}
			var missing []string
			for _, id := range identifiers {
				if _, ok := env[id]; !ok && !declared[id] {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				return nil, fmt.Errorf("expression references unknown field(s): %s", strings.Join(missing, ", "))
			}
		}

		// Execute the pre-compiled program with this record's environment
		result, err := expr.Run(program, env)
		if err != nil {
			return nil, fmt.Errorf("execute expression: %w", err)
		}

		return result, nil
	}, nil
}

// extractIdentifiers walks the AST and returns all identifier names.
func extractIdentifiers(node ast.Node) []string {
	var ids []string
	v := &identifierVisitor{ids: &ids, seen: make(map[string]bool), uses: map[string]int{}, calls: map[string]int{}}
	ast.Walk(&node, v)
	fields := ids[:0]
	for _, name := range ids {
		if v.uses[name] > v.calls[name] {
			fields = append(fields, name)
		}
	}
	return fields
}

type identifierVisitor struct {
	ids  *[]string
	seen map[string]bool
	// uses counts identifier occurrences; calls counts those that were the
	// callee of a compile-time function (date(x)). Walk is post-order, so
	// the callee identifier is seen before its CallNode: a name is a field
	// reference only if it occurs somewhere OTHER than as such a callee —
	// `date(date)` references the field, `date(ts)` does not.
	uses  map[string]int
	calls map[string]int
}

func (v *identifierVisitor) Visit(node *ast.Node) {
	// A bare identifier, or the $env["name"] form ExprFieldShadowing
	// rewrites builtin-named fields (date, len, …) into — both are field
	// references to validate against the first record.
	if call, ok := (*node).(*ast.CallNode); ok {
		if name, ok := ssql.ExprFieldName(call.Callee); ok && ssql.ExprCompiledFunctions[name] {
			v.calls[name]++
		}
	}
	if name, ok := ssql.ExprFieldName(*node); ok && name != "$env" {
		v.uses[name]++
		if !v.seen[name] {
			v.seen[name] = true
			*v.ids = append(*v.ids, name)
		}
	}
}

// MustCompileExpr is like CompileExpr but panics on error.
// Use this in generated code to fail fast at program startup if expressions are invalid.
func MustCompileExpr(expression string) func(ssql.Record) (any, error) {
	eval, err := CompileExpr(expression)
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return eval
}
