package runtime

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/rosscartlidge/ssql/v4"
)

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

// EvalBatchAgg evaluates a batch aggregation expression on a group of records.
// It supports aggregation functions: sum(field), count(), avg(field), min(field), max(field)
// The expression can reference fields directly (e.g., sum(salary)) which is automatically
// transformed to work over the array of records.
// This function compiles and evaluates the expression - for repeated calls with the same
// expression, consider using CompileBatchAgg instead.
func EvalBatchAgg(expression string, records []ssql.Record) (any, error) {
	if len(records) == 0 {
		return nil, nil
	}

	// Build environment with record data
	env, fields := buildBatchEnv(records)

	// Compile with AST patching
	patcher := &aggPatcher{Fields: fields}
	program, err := expr.Compile(expression,
		expr.Env(env),
		expr.Patch(patcher),
	)
	if err != nil {
		return nil, fmt.Errorf("compiling expression: %w", err)
	}

	// Evaluate
	result, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("evaluating expression: %w", err)
	}
	return result, nil
}

// buildBatchEnv builds the environment for batch expression evaluation
func buildBatchEnv(records []ssql.Record) (map[string]any, map[string]bool) {
	env := make(map[string]any)
	fields := make(map[string]bool)

	// Collect all field names and values
	fieldValues := make(map[string][]any)
	recordMaps := make([]map[string]any, 0, len(records))

	for _, r := range records {
		recMap := make(map[string]any)
		for k, v := range r.All() {
			fieldValues[k] = append(fieldValues[k], v)
			recMap[k] = v
			fields[k] = true
		}
		recordMaps = append(recordMaps, recMap)
	}

	// Add field arrays to env
	for field, values := range fieldValues {
		env[field] = values
	}

	// Add _records and _count
	env["_records"] = recordMaps
	env["_count"] = len(records)

	// Dummy functions to satisfy type-checker before patching
	env["count"] = func() int { return 0 }
	env["avg"] = func(arr []float64) float64 { return 0 }

	return env, fields
}

// aggPatcher transforms natural aggregation syntax to expr-lang predicate form
// e.g., sum(salary * bonus) → sum(_records, .salary * .bonus)
type aggPatcher struct {
	Fields map[string]bool // Known field names
}

// Aggregation functions that support predicate form in expr-lang
var aggFunctions = map[string]string{
	"sum":   "sum",
	"count": "count",
}

// Functions that need sum/len transformation for average
var avgFunctions = map[string]bool{
	"avg":  true,
	"mean": true,
}

func (p *aggPatcher) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.CallNode:
		p.patchCall(node, n)
	case *ast.BuiltinNode:
		p.patchBuiltin(node, n)
	}
}

func (p *aggPatcher) patchCall(node *ast.Node, call *ast.CallNode) {
	ident, ok := call.Callee.(*ast.IdentifierNode)
	if !ok {
		return
	}

	// Handle avg/mean functions → sum()/len()
	if avgFunctions[ident.Value] {
		if len(call.Arguments) == 0 {
			return
		}
		arg := call.Arguments[0]
		p.transformIdentifiers(&arg)

		sumNode := &ast.BuiltinNode{
			Name: "sum",
			Arguments: []ast.Node{
				&ast.IdentifierNode{Value: "_records"},
				&ast.PredicateNode{Node: arg},
			},
		}
		lenNode := &ast.BuiltinNode{
			Name: "len",
			Arguments: []ast.Node{
				&ast.IdentifierNode{Value: "_records"},
			},
		}
		ast.Patch(node, &ast.BinaryNode{
			Operator: "/",
			Left:     sumNode,
			Right:    lenNode,
		})
		return
	}

	exprName, isAgg := aggFunctions[ident.Value]
	if !isAgg {
		return
	}

	// Handle count() with no args
	if len(call.Arguments) == 0 {
		if ident.Value == "count" {
			ast.Patch(node, &ast.BuiltinNode{
				Name: "len",
				Arguments: []ast.Node{
					&ast.IdentifierNode{Value: "_records"},
				},
			})
		}
		return
	}

	// Transform aggregation: sum(expr) → sum(_records, .expr)
	arg := call.Arguments[0]
	p.transformIdentifiers(&arg)

	ast.Patch(node, &ast.BuiltinNode{
		Name: exprName,
		Arguments: []ast.Node{
			&ast.IdentifierNode{Value: "_records"},
			&ast.PredicateNode{Node: arg},
		},
	})
}

func (p *aggPatcher) patchBuiltin(node *ast.Node, builtin *ast.BuiltinNode) {
	// Handle avg/mean builtins
	if avgFunctions[builtin.Name] {
		if len(builtin.Arguments) == 0 {
			return
		}
		arg := builtin.Arguments[0]
		p.transformIdentifiers(&arg)

		sumNode := &ast.BuiltinNode{
			Name: "sum",
			Arguments: []ast.Node{
				&ast.IdentifierNode{Value: "_records"},
				&ast.PredicateNode{Node: arg},
			},
		}
		lenNode := &ast.BuiltinNode{
			Name: "len",
			Arguments: []ast.Node{
				&ast.IdentifierNode{Value: "_records"},
			},
		}
		ast.Patch(node, &ast.BinaryNode{
			Operator: "/",
			Left:     sumNode,
			Right:    lenNode,
		})
		return
	}

	_, isAgg := aggFunctions[builtin.Name]
	if !isAgg {
		return
	}

	if len(builtin.Arguments) == 0 {
		if builtin.Name == "count" {
			ast.Patch(node, &ast.BuiltinNode{
				Name: "len",
				Arguments: []ast.Node{
					&ast.IdentifierNode{Value: "_records"},
				},
			})
		}
		return
	}

	arg := builtin.Arguments[0]
	p.transformIdentifiers(&arg)

	ast.Patch(node, &ast.BuiltinNode{
		Name: builtin.Name,
		Arguments: []ast.Node{
			&ast.IdentifierNode{Value: "_records"},
			&ast.PredicateNode{Node: arg},
		},
	})
}

func (p *aggPatcher) transformIdentifiers(node *ast.Node) {
	ast.Walk(node, &identifierTransformer{Fields: p.Fields})
}

type identifierTransformer struct {
	Fields map[string]bool
}

func (t *identifierTransformer) Visit(node *ast.Node) {
	ident, ok := (*node).(*ast.IdentifierNode)
	if !ok {
		return
	}
	if !t.Fields[ident.Value] {
		return
	}
	ast.Patch(node, &ast.MemberNode{
		Node:     &ast.PointerNode{},
		Property: &ast.StringNode{Value: ident.Value},
	})
}

// ApplyValue applies a value of any type to a mutable record.
// This handles the type conversion needed because expression evaluation returns `any`.
// Handles type conversions (int→int64, float32→float64) and defaults unknown types to string.
func ApplyValue(mut ssql.MutableRecord, field string, value any) ssql.MutableRecord {
	switch v := value.(type) {
	case int64:
		return mut.Int(field, v)
	case float64:
		return mut.Float(field, v)
	case bool:
		return mut.Bool(field, v)
	case string:
		return mut.String(field, v)
	case int:
		// expr might return int instead of int64
		return mut.Int(field, int64(v))
	case int32:
		return mut.Int(field, int64(v))
	case int16:
		return mut.Int(field, int64(v))
	case int8:
		return mut.Int(field, int64(v))
	case uint:
		return mut.Int(field, int64(v))
	case uint64:
		return mut.Int(field, int64(v))
	case uint32:
		return mut.Int(field, int64(v))
	case uint16:
		return mut.Int(field, int64(v))
	case uint8:
		return mut.Int(field, int64(v))
	case float32:
		return mut.Float(field, float64(v))
	case nil:
		return mut.String(field, "")
	default:
		// Convert unknown types to string
		return mut.String(field, fmt.Sprintf("%v", v))
	}
}
