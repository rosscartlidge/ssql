package commands

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
	"github.com/rosscartlidge/ssql/v4"
)

// AggPatcher transforms natural aggregation syntax to expr-lang predicate form
// e.g., sum(salary * bonus) → sum(_records, .salary * .bonus)
type AggPatcher struct {
	Fields map[string]bool // Known field names
}

// Aggregation functions that support predicate form in expr-lang
var aggFunctions = map[string]string{
	"sum":   "sum",
	"count": "count",
	// avg/mean/median need special handling - they don't support predicate form
	// min/max also need custom handling - expr's builtins take 2 scalars, not arrays
}

// Functions that need sum/len transformation for average
var avgFunctions = map[string]bool{
	"avg":  true,
	"mean": true,
}

func (p *AggPatcher) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.CallNode:
		p.patchCall(node, n)
	case *ast.BuiltinNode:
		p.patchBuiltin(node, n)
	}
}

func (p *AggPatcher) patchCall(node *ast.Node, call *ast.CallNode) {
	// Get function name from callee
	ident, ok := call.Callee.(*ast.IdentifierNode)
	if !ok {
		return
	}

	// Handle avg/mean functions → sum()/len()
	if avgFunctions[ident.Value] {
		if len(call.Arguments) == 0 {
			return // avg() with no args doesn't make sense
		}
		// Transform: avg(expr) → sum(_records, .expr) / len(_records)
		arg := call.Arguments[0]
		p.transformIdentifiers(&arg)

		// sum(_records, .expr)
		sumNode := &ast.BuiltinNode{
			Name: "sum",
			Arguments: []ast.Node{
				&ast.IdentifierNode{Value: "_records"},
				&ast.PredicateNode{Node: arg},
			},
		}

		// len(_records)
		lenNode := &ast.BuiltinNode{
			Name: "len",
			Arguments: []ast.Node{
				&ast.IdentifierNode{Value: "_records"},
			},
		}

		// sum / len
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
			// count() → len(_records)
			ast.Patch(node, &ast.BuiltinNode{
				Name: "len",
				Arguments: []ast.Node{
					&ast.IdentifierNode{Value: "_records"},
				},
			})
		}
		return
	}

	// Transform: sum(expr) → sum(_records, predicate)
	arg := call.Arguments[0]

	// Transform identifiers within the argument to member access
	p.transformIdentifiers(&arg)

	// Create new call with _records as first arg, wrapped in PredicateNode
	ast.Patch(node, &ast.BuiltinNode{
		Name: exprName,
		Arguments: []ast.Node{
			&ast.IdentifierNode{Value: "_records"},
			&ast.PredicateNode{Node: arg},
		},
	})
}

func (p *AggPatcher) patchBuiltin(node *ast.Node, builtin *ast.BuiltinNode) {
	exprName, isAgg := aggFunctions[builtin.Name]
	if !isAgg {
		return
	}

	// Handle count() with no args
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

	// Transform: sum(expr) → sum(_records, predicate)
	arg := builtin.Arguments[0]
	p.transformIdentifiers(&arg)

	ast.Patch(node, &ast.BuiltinNode{
		Name: exprName,
		Arguments: []ast.Node{
			&ast.IdentifierNode{Value: "_records"},
			&ast.PredicateNode{Node: arg},
		},
	})
}

// transformIdentifiers converts field names to member access within a node
func (p *AggPatcher) transformIdentifiers(node *ast.Node) {
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

	// Check if this identifier is a known field
	if !t.Fields[ident.Value] {
		return // Not a field, leave as-is
	}

	// Transform: salary → #.salary (member access on current element)
	// PointerNode with empty Name represents the current element (#)
	ast.Patch(node, &ast.MemberNode{
		Node:     &ast.PointerNode{},
		Property: &ast.StringNode{Value: ident.Value},
	})
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

// CompileAggExpr compiles an aggregation expression with AST patching
func CompileAggExpr(expression string, fields map[string]bool, env map[string]any) (*vm.Program, error) {
	patcher := &AggPatcher{Fields: fields}
	return expr.Compile(expression,
		expr.Env(env),
		expr.Patch(patcher),
	)
}

// EvalBatchExpr evaluates a batch aggregation expression on a group of records
func EvalBatchExpr(expression string, records []ssql.Record) (any, error) {
	env, fields := buildBatchEnv(records)
	program, err := CompileAggExpr(expression, fields, env)
	if err != nil {
		return nil, fmt.Errorf("compiling expression: %w", err)
	}
	result, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("evaluating expression: %w", err)
	}
	return result, nil
}

// StreamExprSpec holds the three expressions for streaming aggregation
type StreamExprSpec struct {
	InitExpr  string // Initialize state: '{s: 0}'
	EveryExpr string // Update state per record: '{s: s + salary}'
	FinalExpr string // Compute final result: 's'
	Result    string // Result field name
}

// EvalStreamExpr evaluates a streaming aggregation on a group of records
func EvalStreamExpr(spec StreamExprSpec, records []ssql.Record) (any, error) {
	if len(records) == 0 {
		return nil, fmt.Errorf("no records to process")
	}

	// 1. Initialize state
	initProgram, err := expr.Compile(spec.InitExpr)
	if err != nil {
		return nil, fmt.Errorf("compiling init expression: %w", err)
	}
	state, err := expr.Run(initProgram, nil)
	if err != nil {
		return nil, fmt.Errorf("evaluating init expression: %w", err)
	}
	stateMap, ok := state.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("init expression must return object, got %T", state)
	}

	// 2. Build a sample environment for compiling (state + first record's fields)
	compileEnv := make(map[string]any)
	for k, v := range stateMap {
		compileEnv[k] = v
	}
	for k, v := range records[0].All() {
		compileEnv[k] = v
	}

	// Compile "every" expression with combined environment
	everyProgram, err := expr.Compile(spec.EveryExpr, expr.Env(compileEnv))
	if err != nil {
		return nil, fmt.Errorf("compiling every expression: %w", err)
	}

	// 3. Process each record
	for _, record := range records {
		// Merge state with record fields
		env := make(map[string]any)
		for k, v := range stateMap {
			env[k] = v
		}
		for k, v := range record.All() {
			env[k] = v
		}

		// Evaluate every expression
		newState, err := expr.Run(everyProgram, env)
		if err != nil {
			return nil, fmt.Errorf("evaluating every expression: %w", err)
		}
		stateMap, ok = newState.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("every expression must return object, got %T", newState)
		}
	}

	// 4. Compute final result
	finalProgram, err := expr.Compile(spec.FinalExpr, expr.Env(stateMap))
	if err != nil {
		return nil, fmt.Errorf("compiling final expression: %w", err)
	}
	result, err := expr.Run(finalProgram, stateMap)
	if err != nil {
		return nil, fmt.Errorf("evaluating final expression: %w", err)
	}

	return result, nil
}
