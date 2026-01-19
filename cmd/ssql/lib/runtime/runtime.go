package runtime

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/expr-lang/expr"
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

// CompileExprFilter compiles a boolean expression once and returns a filter function.
// The returned function can be used repeatedly on different records.
// This is much more efficient than compiling the expression for each record.
func CompileExprFilter(expression string) (func(ssql.Record) bool, error) {
	eval, err := CompileExpr(expression)
	if err != nil {
		return nil, err
	}

	return func(r ssql.Record) bool {
		result, err := eval(r)
		if err != nil {
			return false
		}
		boolResult, ok := result.(bool)
		return ok && boolResult
	}, nil
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
	// Compile the expression once with a sample environment for type inference
	sampleEnv := make(map[string]interface{})
	sampleEnv["has"] = func(field string) bool { return false }
	sampleEnv["getOr"] = func(field string, defaultValue any) any { return defaultValue }
	sampleEnv["sha256"] = hashSHA256
	sampleEnv["sha1"] = hashSHA1
	sampleEnv["md5"] = hashMD5

	program, err := expr.Compile(expression,
		expr.Env(sampleEnv),
		expr.AllowUndefinedVariables(),
	)
	if err != nil {
		return nil, fmt.Errorf("compile expression: %w", err)
	}

	// Return a closure that evaluates the compiled program on a record
	return func(record ssql.Record) (any, error) {
		// Build environment with all record fields
		env := make(map[string]interface{})
		for k, v := range record.All() {
			env[k] = v
		}

		// Add helper functions that close over the record
		env["has"] = func(field string) bool {
			_, exists := ssql.Get[any](record, field)
			return exists
		}

		env["getOr"] = func(field string, defaultValue any) any {
			if val, exists := ssql.Get[any](record, field); exists {
				return val
			}
			return defaultValue
		}

		// Add hash functions
		env["sha256"] = hashSHA256
		env["sha1"] = hashSHA1
		env["md5"] = hashMD5

		// Execute the pre-compiled program with this record's environment
		result, err := expr.Run(program, env)
		if err != nil {
			return nil, fmt.Errorf("execute expression: %w", err)
		}

		return result, nil
	}, nil
}

// CompileExprWithFields compiles an expression with known field names.
// This is useful when you know the field names ahead of time and want to
// ensure they shadow any built-in functions with the same names.
// The returned function can be used repeatedly on different records.
func CompileExprWithFields(expression string, fields []string) (func(ssql.Record) (any, error), error) {
	// Build sample environment with placeholder values for all fields
	// This ensures field names shadow any built-in functions with the same name
	sampleEnv := make(map[string]interface{})

	// Add placeholders for all known fields (using nil as placeholder)
	for _, field := range fields {
		sampleEnv[field] = nil
	}

	// Add helper functions
	sampleEnv["has"] = func(field string) bool { return false }
	sampleEnv["getOr"] = func(field string, defaultValue any) any { return defaultValue }
	sampleEnv["sha256"] = hashSHA256
	sampleEnv["sha1"] = hashSHA1
	sampleEnv["md5"] = hashMD5

	program, err := expr.Compile(expression,
		expr.Env(sampleEnv),
		expr.AllowUndefinedVariables(),
	)
	if err != nil {
		return nil, fmt.Errorf("compile expression: %w", err)
	}

	// Return a closure that evaluates the compiled program on a record
	return func(record ssql.Record) (any, error) {
		// Build environment with all record fields
		env := make(map[string]interface{})
		for k, v := range record.All() {
			env[k] = v
		}

		// Add helper functions that close over the record
		env["has"] = func(field string) bool {
			_, exists := ssql.Get[any](record, field)
			return exists
		}

		env["getOr"] = func(field string, defaultValue any) any {
			if val, exists := ssql.Get[any](record, field); exists {
				return val
			}
			return defaultValue
		}

		// Add hash functions
		env["sha256"] = hashSHA256
		env["sha1"] = hashSHA1
		env["md5"] = hashMD5

		// Execute the pre-compiled program with this record's environment
		result, err := expr.Run(program, env)
		if err != nil {
			return nil, fmt.Errorf("execute expression: %w", err)
		}

		return result, nil
	}, nil
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

