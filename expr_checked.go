package ssql

import (
	"fmt"
	"reflect"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm/runtime"

	"github.com/rosscartlidge/ssql/v4/exprfn"
)

// Integer overflow is an error in every lane (DFC155 B): an int64 `+`,
// `-` or `*` whose exact result does not fit stops the pipeline, as it
// does in DuckDB, Postgres and DataFusion, instead of wrapping. Until
// v4.123.0 three lanes answered wrong where the fourth stopped: the
// Fibonacci fold wrapped at row 92 in exec, record and typed codegen.
//
// The expression VM has no hook for this. expr.Operator overloading
// needs the operand types at compile time, and ssql compiles every
// expression against an untyped map (each field is `any`, see
// cmd/ssql/lib/runtime), so the overload never fires — probed
// 2026-10-22: MaxInt64 + 1 wrapped with it installed. What does work is
// an AST patch: every `+`, `-` and `*` node becomes a call of a function
// that checks an int64 pair and hands any other pair to expr's own
// operator, so no other semantics moves (string concatenation, floats,
// int beside float, time arithmetic, and the VM's own type errors).
// Measured cost: 60 ns per three operators in the VM lanes, 2.5% of a
// per-row update step.

const (
	exprCheckedAdd = "_ssql_add"
	exprCheckedSub = "_ssql_sub"
	exprCheckedMul = "_ssql_mul"
	exprCheckedNeg = "_ssql_neg"
	exprCheckedSum = "_ssql_sum"
)

// ExprCheckedIntArithmetic is the expr.Compile option set that makes an
// int64 overflow an error; ExprStandardOptions carries it, so every
// compile site (exec, generated record code, window folds, aggregation
// expressions) has it.
func ExprCheckedIntArithmetic() []expr.Option {
	return []expr.Option{
		expr.Function(exprCheckedAdd, checkedArith(exprfn.CheckedAdd, runtime.Add)),
		expr.Function(exprCheckedSub, checkedArith(exprfn.CheckedSub, runtime.Subtract)),
		expr.Function(exprCheckedMul, checkedArith(exprfn.CheckedMul, runtime.Multiply)),
		expr.Function(exprCheckedNeg, checkedNegate),
		expr.Function(exprCheckedSum, checkedSum),
		expr.Patch(checkedArithPatcher{}),
	}
}

// checkedNegate is unary minus: the checked form for an integer (the one
// value that cannot be negated is MinInt64), expr's own for the rest.
func checkedNegate(params ...any) (any, error) {
	a := exprDeref(params[0])
	if x, ok := exprIntOperand(a); ok {
		r, err := exprfn.CheckedNeg(x)
		if err != nil {
			return nil, err
		}
		return int(r), nil
	}
	return runtime.Negate(a), nil
}

// checkedSumAdd is the accumulation step of sum(): expr's own loop adds
// each element onto an int 0 with OpAdd, so the checked form of the
// same operator gives sum() the same overflow rule as `+`.
var checkedSumAdd = checkedArith(exprfn.CheckedAdd, runtime.Add)

// checkedSum is expr's sum(array) with the overflow check: the elements
// added in order onto an int 0, so an all-integer list sums to an int
// and a float among the elements makes the sum a float, exactly as the
// builtin does. sum(array, mapper) reaches here as sum(map(array, mapper)).
func checkedSum(params ...any) (any, error) {
	arr := exprDeref(params[0])
	var acc any = 0
	var err error
	add := func(elem any) bool {
		acc, err = checkedSumAdd(elem, acc)
		return err == nil
	}
	switch xs := arr.(type) {
	case nil:
		return acc, nil
	case []any:
		for _, e := range xs {
			if !add(e) {
				return nil, err
			}
		}
	case []int64:
		for _, e := range xs {
			if !add(e) {
				return nil, err
			}
		}
	case []float64:
		for _, e := range xs {
			if !add(e) {
				return nil, err
			}
		}
	case []int:
		for _, e := range xs {
			if !add(e) {
				return nil, err
			}
		}
	default:
		rv := reflect.ValueOf(arr)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return nil, fmt.Errorf("invalid argument for sum (type %T)", arr)
		}
		for i := 0; i < rv.Len(); i++ {
			if !add(rv.Index(i).Interface()) {
				return nil, err
			}
		}
	}
	return acc, nil
}

// checkedArith is the operator's replacement: the checked int64 form for
// an integer pair, expr's own operator for anything else. The result
// kind is expr's own for an integer pair — int, whatever the operands
// were (its runtime computes `int(x) + int(y)` for every integer pair),
// so nothing downstream of the value can tell the difference.
func checkedArith(checked func(a, b int64) (int64, error), own func(a, b any) any) func(...any) (any, error) {
	return func(params ...any) (any, error) {
		a, b := exprDeref(params[0]), exprDeref(params[1])
		x, xok := exprIntOperand(a)
		y, yok := exprIntOperand(b)
		if !xok || !yok {
			return own(a, b), nil
		}
		r, err := checked(x, y)
		if err != nil {
			return nil, err
		}
		return int(r), nil
	}
}

// exprDeref is what expr's compiler emits (OpDeref) before its own binary
// operator for an operand of unknown type — every ssql field is one — and
// what a call's variadic parameters do not get: a pointer or interface is
// unwrapped to the value, a nil pointer to nil (a nullable window value,
// the lag before the first row, reaches an expression as a *int64).
func exprDeref(v any) any {
	switch v.(type) {
	case nil, int64, int, float64, string, bool:
		return v
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.IsValid() {
		return rv.Interface()
	}
	return v
}

// exprIntOperand reports an integer operand: int64 (every integer
// field) or int (a literal, len(), int(), the result of another
// operator), the two integer kinds a value has inside an ssql
// expression. Any other kind is expr's business.
func exprIntOperand(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	}
	return 0, false
}

// checkedArithPatcher rewrites `a + b` into `_ssql_add(a, b)` (and - , *),
// `-a` into `_ssql_neg(a)`, `sum(xs)` into `_ssql_sum(xs)` and
// `sum(xs, f)` into `_ssql_sum(map(xs, f))`. Walk is post-order, so the
// operands have already been rewritten; the calls it builds hold none of
// the shapes it matches, so the visitor pass converges. The aggregation
// patcher (expr_agg.go) runs before it — visitors run in registration
// order, and every aggregation compile registers it first — so the
// group-level sum(_records, f) it produces is rewritten here too; the
// typed lowering recognises both spellings (aggSumPredicate).
type checkedArithPatcher struct{}

func (checkedArithPatcher) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.UnaryNode:
		if n.Operator == "-" {
			ast.Patch(node, &ast.CallNode{
				Callee:    &ast.IdentifierNode{Value: exprCheckedNeg},
				Arguments: []ast.Node{n.Node},
			})
		}
		return
	case *ast.BuiltinNode:
		if n.Name != "sum" || len(n.Arguments) == 0 || len(n.Arguments) > 2 {
			return
		}
		arg := n.Arguments[0]
		if len(n.Arguments) == 2 {
			arg = &ast.BuiltinNode{Name: "map", Arguments: []ast.Node{n.Arguments[0], n.Arguments[1]}}
		}
		ast.Patch(node, &ast.CallNode{
			Callee:    &ast.IdentifierNode{Value: exprCheckedSum},
			Arguments: []ast.Node{arg},
		})
		return
	}
	bn, ok := (*node).(*ast.BinaryNode)
	if !ok {
		return
	}
	var name string
	switch bn.Operator {
	case "+":
		name = exprCheckedAdd
	case "-":
		name = exprCheckedSub
	case "*":
		name = exprCheckedMul
	default:
		return
	}
	ast.Patch(node, &ast.CallNode{
		Callee:    &ast.IdentifierNode{Value: name},
		Arguments: []ast.Node{bn.Left, bn.Right},
	})
}

// ExprCheckedArithFunction reports whether name is one of the call
// targets the overflow patch rewrites operators into, for the walkers
// that tell a field reference from a function call (the identifier
// validation in cmd/ssql/lib/runtime).
func ExprCheckedArithFunction(name string) bool {
	switch name {
	case exprCheckedAdd, exprCheckedSub, exprCheckedMul, exprCheckedNeg, exprCheckedSum:
		return true
	}
	return false
}

// ExprCheckedSumPredicate recognises the group-level sum the aggregation
// patcher produces in either spelling — sum(_records, <predicate>) as it
// builds it, or _ssql_sum(map(_records, <predicate>)) after the overflow
// patch — and returns the predicate's node. The typed lowering reads a
// compiled program's AST, which holds the second.
func ExprCheckedSumPredicate(n ast.Node) (ast.Node, bool) {
	if b, ok := n.(*ast.BuiltinNode); ok && b.Name == "sum" && len(b.Arguments) == 2 {
		return aggRecordsPredicate(b.Arguments[0], b.Arguments[1])
	}
	if c, ok := n.(*ast.CallNode); ok && len(c.Arguments) == 1 {
		if id, ok := c.Callee.(*ast.IdentifierNode); ok && id.Value == exprCheckedSum {
			if m, ok := c.Arguments[0].(*ast.BuiltinNode); ok && m.Name == "map" && len(m.Arguments) == 2 {
				return aggRecordsPredicate(m.Arguments[0], m.Arguments[1])
			}
		}
	}
	return nil, false
}

func aggRecordsPredicate(records, pred ast.Node) (ast.Node, bool) {
	if id, ok := records.(*ast.IdentifierNode); ok && id.Value == "_records" {
		if p, ok := pred.(*ast.PredicateNode); ok {
			return p.Node, true
		}
	}
	return nil, false
}
