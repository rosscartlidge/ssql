package ssql

import (
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
)

// ExprAgg creates an AggregateFunc that evaluates a custom expression.
// Supports aggregation functions: sum(expr), count(), avg(expr)/mean(expr).
//
// Panics if the expression is invalid or evaluation fails.
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "total_comp":   ssql.ExprAgg("sum(salary * bonus)"),
//	    "avg_rate":     ssql.ExprAgg("avg(amount / quantity)"),
//	    "record_count": ssql.ExprAgg("count()"),
//	}
func ExprAgg(expression string) AggregateFunc {
	return func(records []Record) AggregateResult {
		result, err := evalBatchAggExpr(expression, records)
		if err != nil {
			panic(fmt.Errorf("ExprAgg(%q): %w", expression, err))
		}
		return aggResult(fmt.Sprintf("ExprAgg(%q)", expression), result)
	}
}

// StreamExprAgg creates an AggregateFunc using streaming (init/every/final) expressions.
// This processes records one at a time with mutable state, useful for memory-efficient
// aggregations or custom accumulation logic.
//
// The three expressions are:
//   - initExpr: initializes state as an object, e.g. "{s: 0}"
//   - everyExpr: updates state for each record, e.g. "{s: s + salary}"
//   - finalExpr: extracts the final result from state, e.g. "s"
//
// Panics if any expression is invalid or evaluation fails.
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "total": ssql.StreamExprAgg("{s: 0}", "{s: s + salary}", "s"),
//	}
func StreamExprAgg(initExpr, everyExpr, finalExpr string) AggregateFunc {
	return func(records []Record) AggregateResult {
		result, err := evalStreamAggExpr(initExpr, everyExpr, finalExpr, records)
		if err != nil {
			panic(fmt.Errorf("StreamExprAgg: %w", err))
		}
		return aggResult("StreamExprAgg", result)
	}
}

// evalStreamAggExpr evaluates a streaming aggregation on a group of records.
func evalStreamAggExpr(initExpr, everyExpr, finalExpr string, records []Record) (any, error) {
	if len(records) == 0 {
		return nil, fmt.Errorf("no records to process")
	}

	// 1. Initialize state
	initProgram, err := expr.Compile(initExpr)
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
	maps.Copy(compileEnv, stateMap)
	maps.Insert(compileEnv, records[0].All())

	// Compile "every" expression with combined environment
	everyProgram, err := expr.Compile(everyExpr, expr.Env(compileEnv), ExprFieldShadowing())
	if err != nil {
		return nil, fmt.Errorf("compiling every expression: %w", err)
	}

	// 3. Process each record
	for _, record := range records {
		env := make(map[string]any)
		maps.Copy(env, stateMap)
		maps.Insert(env, record.All())

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
	finalProgram, err := expr.Compile(finalExpr, expr.Env(stateMap), ExprFieldShadowing())
	if err != nil {
		return nil, fmt.Errorf("compiling final expression: %w", err)
	}
	result, err := expr.Run(finalProgram, stateMap)
	if err != nil {
		return nil, fmt.Errorf("evaluating final expression: %w", err)
	}

	return result, nil
}

// aggResult types an aggregation expression's result: every numeric
// result is float64 (as it always was), and a string, bool or time.Time
// result is kept as is — `max(date)` over ISO dates, a `-stream-expr`
// that carries a name, a latest timestamp. Anything else (a map, a list,
// nil) is a wrong expression and panics with a clear message; the old
// silent coercion to 0 turned that into corrupted-looking data, and until
// v4.94.0 a string result panicked too ("need a numeric result").
func aggResult(context string, v any) AggregateResult {
	switch x := v.(type) {
	case float64, float32, int, int64, int32, int16, int8,
		uint, uint64, uint32, uint16, uint8:
		return AggResult[float64]{val: toFloat64(v)}
	case string:
		return AggResult[string]{val: x}
	case bool:
		return AggResult[bool]{val: x}
	case time.Time:
		return AggResult[time.Time]{val: x}
	}
	panic(fmt.Errorf("%s: expression returned %T (%v), need a number, string, bool or time", context, v, v))
}

// aggMax and aggMin are the aggregation environment's max/min: they take
// the field arrays the batch env provides (`max(date)` sees every date in
// the group) or scalars, and order numbers, strings and times — expr's own
// builtins are numeric-only, which is why `max(date)` used to fail with
// "invalid argument for max (type string)".
func aggMax(args ...any) (any, error) { return aggExtreme("max", args, 1) }
func aggMin(args ...any) (any, error) { return aggExtreme("min", args, -1) }

// aggFirst and aggLast are the aggregation environment's first/last: the
// first or last value of the group in arrival order (records without the
// field contribute nothing, as ssql.First/Last), of any type.
func aggFirst(args ...any) (any, error) {
	vals, err := aggValues("first", args)
	if err != nil {
		return nil, err
	}
	return vals[0], nil
}

func aggLast(args ...any) (any, error) {
	vals, err := aggValues("last", args)
	if err != nil {
		return nil, err
	}
	return vals[len(vals)-1], nil
}

// aggValues flattens an aggregation function's arguments — the field
// arrays the batch env provides, a mapped expression, or scalars — into
// one list, erroring on an empty group.
func aggValues(name string, args []any) ([]any, error) {
	var vals []any
	for _, a := range args {
		switch arr := a.(type) {
		case []any:
			vals = append(vals, arr...)
		case []float64:
			for _, v := range arr {
				vals = append(vals, v)
			}
		case []int64:
			for _, v := range arr {
				vals = append(vals, v)
			}
		case []string:
			for _, v := range arr {
				vals = append(vals, v)
			}
		default:
			vals = append(vals, a)
		}
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("%s: no values", name)
	}
	return vals, nil
}

func aggExtreme(name string, args []any, sign int) (any, error) {
	vals, err := aggValues(name, args)
	if err != nil {
		return nil, err
	}
	best := vals[0]
	for _, v := range vals[1:] {
		c, err := aggCompare(name, v, best)
		if err != nil {
			return nil, err
		}
		if c*sign > 0 {
			best = v
		}
	}
	return best, nil
}

// aggCompare orders two aggregation values of the same kind: numbers
// (any width, compared as float64), strings, or times.
func aggCompare(name string, a, b any) (int, error) {
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		if !ok {
			return 0, fmt.Errorf("%s: mixed types %T and %T", name, a, b)
		}
		return strings.Compare(x, y), nil
	case time.Time:
		y, ok := b.(time.Time)
		if !ok {
			return 0, fmt.Errorf("%s: mixed types %T and %T", name, a, b)
		}
		switch {
		case x.After(y):
			return 1, nil
		case x.Before(y):
			return -1, nil
		}
		return 0, nil
	}
	if !isNumeric(a) || !isNumeric(b) {
		return 0, fmt.Errorf("%s: cannot order %T and %T", name, a, b)
	}
	fa, fb := toFloat64(a), toFloat64(b)
	switch {
	case fa > fb:
		return 1, nil
	case fa < fb:
		return -1, nil
	}
	return 0, nil
}

func isNumeric(v any) bool {
	switch v.(type) {
	case float64, float32, int, int64, int32, int16, int8,
		uint, uint64, uint32, uint16, uint8:
		return true
	}
	return false
}

// toFloat64 converts various numeric types to float64
func toFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case int32:
		return float64(val)
	case int16:
		return float64(val)
	case int8:
		return float64(val)
	case uint:
		return float64(val)
	case uint64:
		return float64(val)
	case uint32:
		return float64(val)
	case uint16:
		return float64(val)
	case uint8:
		return float64(val)
	default:
		return 0
	}
}

// evalBatchAggExpr evaluates a batch aggregation expression on a group of records
func evalBatchAggExpr(expression string, records []Record) (any, error) {
	env, fields := buildAggBatchEnv(records)
	program, err := compileAggExpr(expression, fields, env)
	if err != nil {
		return nil, fmt.Errorf("compiling expression: %w", err)
	}
	result, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("evaluating expression: %w", err)
	}
	return result, nil
}

// buildAggBatchEnv builds the environment for batch expression evaluation
func buildAggBatchEnv(records []Record) (map[string]any, map[string]bool) {
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
	// max/min over the group, for strings and times as well as numbers
	// (an env function shadows expr's numeric-only builtin); first/last in
	// arrival order.
	env["max"] = aggMax
	env["min"] = aggMin
	env["first"] = aggFirst
	env["last"] = aggLast

	// Dummy functions to satisfy type-checker before patching
	env["count"] = func() int { return 0 }
	env["avg"] = func(arr []float64) float64 { return 0 }
	env["max"] = aggMax // the exec env's max/min shadow expr's numeric-only builtins
	env["min"] = aggMin
	env["first"] = aggFirst
	env["last"] = aggLast

	return env, fields
}

// compileAggExpr compiles an aggregation expression with AST patching
func compileAggExpr(expression string, fields map[string]bool, env map[string]any) (*vm.Program, error) {
	patcher := &aggPatcher{Fields: fields}
	return expr.Compile(expression,
		expr.Env(env),
		expr.Patch(patcher),
		ExprFieldShadowing(),
	)
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
	// avg/mean/median need special handling - they don't support predicate form
	// min/max also need custom handling - expr's builtins take 2 scalars, not arrays
}

// Functions provided by the aggregation env (aggMax/aggMin/aggFirst/
// aggLast) that take the group's values: a field array as is, an
// expression mapped across the records.
var valueAggFunctions = map[string]bool{
	"max": true, "min": true, "first": true, "last": true,
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

	// max/min/first/last take the group's values. A bare field is already
	// an array in the batch env; an expression (`max(price * qty)`) is
	// mapped across the records first: max(map(_records, .price * .qty)).
	if valueAggFunctions[ident.Value] && len(call.Arguments) == 1 {
		if _, bare := ExprFieldName(call.Arguments[0]); !bare {
			arg := call.Arguments[0]
			p.transformIdentifiers(&arg)
			call.Arguments[0] = &ast.BuiltinNode{
				Name: "map",
				Arguments: []ast.Node{
					&ast.IdentifierNode{Value: "_records"},
					&ast.PredicateNode{Node: arg},
				},
			}
		}
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

func (p *aggPatcher) patchBuiltin(node *ast.Node, builtin *ast.BuiltinNode) {
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
func (p *aggPatcher) transformIdentifiers(node *ast.Node) {
	ast.Walk(node, &identifierTransformer{Fields: p.Fields})
}

// CompileAggExprPatched compiles an aggregation expression EXACTLY as the
// exec path does — same env dummies (bare `count()` only parses because a
// dummy env function shadows the arity-checked builtin), same AST patcher —
// against a synthetic empty-record env built from the field names, and
// returns the PATCHED tree: sum(salary*bonus) → sum(_records,
// #.salary*#.bonus), count() → len(_records), avg(e) → sum/len. The typed
// code generator lowers this SAME normal form to mergeable accumulators
// instead of re-deriving aggregation recognition (one semantics, two
// consumers).
func CompileAggExprPatched(expression string, fieldNames []string) (ast.Node, error) {
	env := make(map[string]any)
	fields := make(map[string]bool)
	for _, f := range fieldNames {
		env[f] = []any{}
		fields[f] = true
	}
	env["_records"] = []map[string]any{}
	env["_count"] = 0
	env["count"] = func() int { return 0 }
	env["avg"] = func(arr []float64) float64 { return 0 }
	env["max"] = aggMax // the exec env's max/min shadow expr's numeric-only builtins
	env["min"] = aggMin
	env["first"] = aggFirst
	env["last"] = aggLast
	program, err := compileAggExpr(expression, fields, env)
	if err != nil {
		return nil, err
	}
	return program.Node(), nil
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
