package ssql

import (
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
		expr.Patch(checkedArithPatcher{}),
	}
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

// checkedArithPatcher rewrites `a + b` into `_ssql_add(a, b)` (and - , *).
// Walk is post-order, so the operands have already been rewritten; the
// call it builds holds no BinaryNode, so the visitor pass converges.
type checkedArithPatcher struct{}

func (checkedArithPatcher) Visit(node *ast.Node) {
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
	return name == exprCheckedAdd || name == exprCheckedSub || name == exprCheckedMul
}
