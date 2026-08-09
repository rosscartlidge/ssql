package runtime

import (
	"fmt"
	"os"

	"github.com/expr-lang/expr"
)

// This file is the Tier-V runtime for typed codegen (expr-transpiler Phase
// 1.5): when an expression falls outside exprToGo's native subset, the
// generated TYPED code evaluates it with the expr-lang VM against an env map
// built statically from the struct — so one exotic expression no longer
// drags the rest of the pipeline through a Serial()+toRecord boundary into
// Record mode. These are CompileExpr/CompileExprFilter minus the Record→env
// copy; identifier validation is skipped entirely because typed codegen
// already validated every identifier against the schema.

// CompileExprEnv compiles an expression once for repeated evaluation against
// caller-built field maps. The caller builds a fresh map per row (generated
// code emits a per-schema env constructor); helpers (has/getOr, hash
// functions) are layered on top without mutating the caller's map.
func CompileExprEnv(expression string) (func(map[string]any) (any, error), error) {
	sampleEnv := map[string]any{
		"has":          func(field string) bool { return false },
		"getOr":        func(field string, defaultValue any) any { return defaultValue },
		"sha256":       hashSHA256,
		"sha1":         hashSHA1,
		"md5":          hashMD5,
		"replaceRegex": replaceRegex,
	}
	program, err := expr.Compile(expression,
		expr.Env(sampleEnv),
		expr.AllowUndefinedVariables(),
	)
	if err != nil {
		return nil, fmt.Errorf("compile expression: %w", err)
	}

	return func(fields map[string]any) (any, error) {
		env := make(map[string]any, len(fields)+6)
		for k, v := range fields {
			env[k] = v
		}
		env["has"] = func(field string) bool {
			_, ok := fields[field]
			return ok
		}
		env["getOr"] = func(field string, defaultValue any) any {
			if v, ok := fields[field]; ok {
				return v
			}
			return defaultValue
		}
		env["sha256"] = hashSHA256
		env["sha1"] = hashSHA1
		env["md5"] = hashMD5
		env["replaceRegex"] = replaceRegex

		result, err := expr.Run(program, env)
		if err != nil {
			return nil, fmt.Errorf("execute expression: %w", err)
		}
		return result, nil
	}, nil
}

// MustCompileExprEnv is CompileExprEnv, panicking on compile failure — for
// generated code, where the expression was already validated at codegen time.
func MustCompileExprEnv(expression string) func(map[string]any) (any, error) {
	eval, err := CompileExprEnv(expression)
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return eval
}

// CompileExprFilterEnv is the predicate form: false on eval error and on
// non-bool results, exactly matching CompileExprFilter's semantics.
func CompileExprFilterEnv(expression string) (func(map[string]any) bool, error) {
	eval, err := CompileExprEnv(expression)
	if err != nil {
		return nil, err
	}
	return func(fields map[string]any) bool {
		result, err := eval(fields)
		if err != nil {
			return false
		}
		b, ok := result.(bool)
		return ok && b
	}, nil
}

// MustCompileExprFilterEnv is CompileExprFilterEnv, panicking on compile
// failure.
func MustCompileExprFilterEnv(expression string) func(map[string]any) bool {
	filter, err := CompileExprFilterEnv(expression)
	if err != nil {
		panic(fmt.Sprintf("failed to compile expression %q: %v", expression, err))
	}
	return filter
}

// The MustCoerce* helpers type a Tier-V -set-expr result for assignment to
// a typed struct field. Exact type matches pass through; the one inserted
// conversion is the value-preserving int64→float64 widening. Anything else
// — notably a float result for an int64 column, which in record mode would
// RETYPE the field — fails the pipeline loudly (stderr + exit 1, the same
// contract as -set-expr eval errors since v4.56.1): a typed column cannot
// change type, and truncating silently would be worse.

func MustCoerceInt64(v any, expression string) int64 {
	if x, ok := v.(int64); ok {
		return x
	}
	if x, ok := v.(int); ok {
		return int64(x)
	}
	coerceFail(v, "int64", expression)
	return 0
}

func MustCoerceFloat64(v any, expression string) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	coerceFail(v, "float64", expression)
	return 0
}

func MustCoerceString(v any, expression string) string {
	if x, ok := v.(string); ok {
		return x
	}
	coerceFail(v, "string", expression)
	return ""
}

func MustCoerceBool(v any, expression string) bool {
	if x, ok := v.(bool); ok {
		return x
	}
	coerceFail(v, "bool", expression)
	return false
}

func coerceFail(v any, want, expression string) {
	fmt.Fprintf(os.Stderr, "Error: expression %q produced %T (%v), but the field's type is %s — a typed column cannot change type (use SSQL_MODE=record if the retype is intended)\n",
		expression, v, v, want)
	os.Exit(1)
}
