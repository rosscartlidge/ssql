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
	sampleEnv["replaceRegex"] = replaceRegex

	program, err := expr.Compile(expression,
		expr.Env(sampleEnv),
		expr.AllowUndefinedVariables(),
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
		env["replaceRegex"] = replaceRegex

		// On first record, check that expression identifiers exist as fields or known functions
		if !validated {
			validated = true
			var missing []string
			for _, id := range identifiers {
				if _, ok := env[id]; !ok {
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
	seen := make(map[string]bool)
	ast.Walk(&node, &identifierVisitor{ids: &ids, seen: seen})
	return ids
}

type identifierVisitor struct {
	ids  *[]string
	seen map[string]bool
}

func (v *identifierVisitor) Visit(node *ast.Node) {
	if n, ok := (*node).(*ast.IdentifierNode); ok {
		if !v.seen[n.Value] {
			v.seen[n.Value] = true
			*v.ids = append(*v.ids, n.Value)
		}
	}
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
	sampleEnv["replaceRegex"] = replaceRegex

	program, err := expr.Compile(expression,
		expr.Env(sampleEnv),
		expr.AllowUndefinedVariables(),
	)
	if err != nil {
		return nil, fmt.Errorf("compile expression: %w", err)
	}

	// Validate identifiers against known fields at compile time
	identifiers := extractIdentifiers(program.Node())
	var missing []string
	for _, id := range identifiers {
		if _, ok := sampleEnv[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("expression references unknown field(s): %s", strings.Join(missing, ", "))
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
		env["replaceRegex"] = replaceRegex

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
