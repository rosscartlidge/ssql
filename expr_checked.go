package ssql

import (
	"fmt"
	"reflect"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/builtin"
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
//
// The same patch carries the 128-bit integer (DFC155 C). An Int128 is a
// struct the VM cannot add, divide or order on its own — runtime.Less
// panics on it, and runtime.Equal falls back to a deep equality that
// calls an Int128 unequal to the int literal of the same value — so `/`,
// `%`, `**`, the six comparisons and the builtins int, float, abs, min
// and max are patched too, each fast-pathing to expr's own function for
// every pair that holds no Int128. The widening rule: int64 beside
// Int128 is Int128, Int128 beside float64 is float64, never on overflow.

const (
	exprCheckedAdd   = "_ssql_add"
	exprCheckedSub   = "_ssql_sub"
	exprCheckedMul   = "_ssql_mul"
	exprCheckedNeg   = "_ssql_neg"
	exprCheckedSum   = "_ssql_sum"
	exprCheckedDiv   = "_ssql_div"
	exprCheckedMod   = "_ssql_mod"
	exprCheckedPow   = "_ssql_pow"
	exprCheckedEq    = "_ssql_eq"
	exprCheckedNe    = "_ssql_ne"
	exprCheckedLt    = "_ssql_lt"
	exprCheckedLe    = "_ssql_le"
	exprCheckedGt    = "_ssql_gt"
	exprCheckedGe    = "_ssql_ge"
	exprCheckedInt   = "_ssql_int"
	exprCheckedFloat = "_ssql_float"
	exprCheckedAbs   = "_ssql_abs"
	exprCheckedMin   = "_ssql_min"
	exprCheckedMax   = "_ssql_max"
)

// exprCheckedOperators maps a binary operator to its patched call, and
// exprCheckedBuiltins a builtin to its; exprCheckedNames is every call
// target the patch produces.
var exprCheckedOperators = map[string]string{
	"+": exprCheckedAdd, "-": exprCheckedSub, "*": exprCheckedMul,
	"/": exprCheckedDiv, "%": exprCheckedMod, "**": exprCheckedPow, "^": exprCheckedPow,
	"==": exprCheckedEq, "!=": exprCheckedNe,
	"<": exprCheckedLt, "<=": exprCheckedLe, ">": exprCheckedGt, ">=": exprCheckedGe,
}

var exprCheckedBuiltins = map[string]string{
	"int": exprCheckedInt, "float": exprCheckedFloat, "abs": exprCheckedAbs,
	"min": exprCheckedMin, "max": exprCheckedMax,
}

var exprCheckedNames = func() map[string]bool {
	m := map[string]bool{exprCheckedNeg: true, exprCheckedSum: true}
	for _, n := range exprCheckedOperators {
		m[n] = true
	}
	for _, n := range exprCheckedBuiltins {
		m[n] = true
	}
	return m
}()

// ExprCheckedIntArithmetic is the expr.Compile option set that makes an
// int64 overflow an error and gives the VM its Int128 arithmetic and
// comparisons; ExprStandardOptions carries it, so every compile site
// (exec, generated record code, window folds, aggregation expressions)
// has it.
func ExprCheckedIntArithmetic() []expr.Option {
	boolSig := new(func(any, any) bool)
	return []expr.Option{
		expr.Function(exprCheckedAdd, checkedArith(exprfn.CheckedAdd, Int128.CheckedAdd, runtime.Add)),
		expr.Function(exprCheckedSub, checkedArith(exprfn.CheckedSub, Int128.CheckedSub, runtime.Subtract)),
		expr.Function(exprCheckedMul, checkedArith(exprfn.CheckedMul, Int128.CheckedMul, runtime.Multiply)),
		expr.Function(exprCheckedNeg, checkedNegate),
		expr.Function(exprCheckedSum, checkedSum),
		expr.Function(exprCheckedDiv, checkedDivide),
		expr.Function(exprCheckedMod, checkedModulo),
		expr.Function(exprCheckedPow, checkedPower),
		expr.Function(exprCheckedEq, checkedCompare("==", runtime.Equal), boolSig),
		expr.Function(exprCheckedNe, checkedCompare("!=", func(a, b any) bool { return !runtime.Equal(a, b) }), boolSig),
		expr.Function(exprCheckedLt, checkedCompare("<", runtime.Less), boolSig),
		expr.Function(exprCheckedLe, checkedCompare("<=", runtime.LessOrEqual), boolSig),
		expr.Function(exprCheckedGt, checkedCompare(">", runtime.More), boolSig),
		expr.Function(exprCheckedGe, checkedCompare(">=", runtime.MoreOrEqual), boolSig),
		expr.Function(exprCheckedInt, wideBuiltin("int", wideInt)),
		expr.Function(exprCheckedFloat, wideBuiltin("float", wideFloat)),
		expr.Function(exprCheckedAbs, wideBuiltin("abs", wideAbs)),
		expr.Function(exprCheckedMin, wideBuiltin("min", wideExtreme(-1))),
		expr.Function(exprCheckedMax, wideBuiltin("max", wideExtreme(1))),
		expr.Patch(checkedArithPatcher{}),
	}
}

// checkedNegate is unary minus: the checked form for an integer (the one
// value that cannot be negated is MinInt64, or MinInt128), expr's own
// for the rest.
func checkedNegate(params ...any) (any, error) {
	a := exprDeref(params[0])
	if x, ok := exprIntOperand(a); ok {
		r, err := exprfn.CheckedNeg(x)
		if err != nil {
			return nil, err
		}
		return int(r), nil
	}
	if w, ok := a.(Int128); ok {
		return w.CheckedNeg()
	}
	return runtime.Negate(a), nil
}

// checkedSum is expr's sum(array) with the widening every integer sum
// has (DFC155 C): the integer elements accumulate exactly in an Int128,
// which is the result when every element was an integer — as `-sum` of
// a column is — and a float among the elements makes the sum a float,
// as the builtin does. sum(array, mapper) reaches here as
// sum(map(array, mapper)). A nil element (an absent field) adds nothing.
func checkedSum(params ...any) (any, error) {
	arr := exprDeref(params[0])
	var ints Int128
	var floats float64
	isFloat := false
	add := func(elem any) error {
		elem = exprDeref(elem)
		if elem == nil {
			return nil
		}
		if w, ok := asInt128(elem); ok {
			if isFloat {
				floats += w.Float64()
				return nil
			}
			var err error
			ints, err = ints.CheckedAdd(w)
			return err
		}
		f, ok := elem.(float64)
		if !ok {
			return fmt.Errorf("invalid argument for sum (type %T)", elem)
		}
		if !isFloat {
			isFloat = true
			floats = ints.Float64()
		}
		floats += f
		return nil
	}
	switch xs := arr.(type) {
	case nil:
	case []any:
		for _, e := range xs {
			if err := add(e); err != nil {
				return nil, err
			}
		}
	case []int64:
		for _, e := range xs {
			if err := add(e); err != nil {
				return nil, err
			}
		}
	case []float64:
		for _, e := range xs {
			if err := add(e); err != nil {
				return nil, err
			}
		}
	case []int:
		for _, e := range xs {
			if err := add(e); err != nil {
				return nil, err
			}
		}
	default:
		rv := reflect.ValueOf(arr)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return nil, fmt.Errorf("invalid argument for sum (type %T)", arr)
		}
		for i := 0; i < rv.Len(); i++ {
			if err := add(rv.Index(i).Interface()); err != nil {
				return nil, err
			}
		}
	}
	if isFloat {
		return floats, nil
	}
	return ints, nil
}

// checkedArith is the operator's replacement: the checked int64 form for
// an integer pair, the Int128 form when either side is an Int128 and the
// other an integer, floats when an Int128 meets a float64, and expr's own
// operator for anything else. The result kind is expr's own for an
// integer pair — int, whatever the operands were (its runtime computes
// `int(x) + int(y)` for every integer pair), so nothing downstream of
// the value can tell the difference.
func checkedArith(checked func(a, b int64) (int64, error), wide func(a, b Int128) (Int128, error), own func(a, b any) any) func(...any) (any, error) {
	return func(params ...any) (any, error) {
		a, b := exprDeref(params[0]), exprDeref(params[1])
		if x, xok := exprIntOperand(a); xok {
			if y, yok := exprIntOperand(b); yok {
				r, err := checked(x, y)
				if err != nil {
					return nil, err
				}
				return int(r), nil
			}
		}
		if x, y, ok := exprWidePair(a, b); ok {
			return wide(x, y)
		}
		if fa, fb, ok := exprWideFloat(a, b); ok {
			return own(fa, fb), nil
		}
		return own(a, b), nil
	}
}

// checkedDivide is `/`: a float result for every numeric pair, as expr's
// own is (int / int is a float64), the Int128 sides as their nearest
// float64.
func checkedDivide(params ...any) (any, error) {
	a, b := exprDeref(params[0]), exprDeref(params[1])
	if x, y, ok := exprWidePair(a, b); ok {
		return x.Float64() / y.Float64(), nil
	}
	if fa, fb, ok := exprWideFloat(a, b); ok {
		return runtime.Divide(fa, fb), nil
	}
	return runtime.Divide(a, b), nil
}

// checkedModulo is `%`: the Int128 remainder (the sign of the dividend,
// a zero divisor an error) when either side is an Int128, expr's own
// otherwise.
func checkedModulo(params ...any) (any, error) {
	a, b := exprDeref(params[0]), exprDeref(params[1])
	if x, y, ok := exprWidePair(a, b); ok {
		return x.CheckedRem(y)
	}
	return runtime.Modulo(a, b), nil
}

// checkedPower is `**`: a float, as expr's own is; an Int128 side as its
// nearest float64.
func checkedPower(params ...any) (any, error) {
	a, b := exprDeref(params[0]), exprDeref(params[1])
	if w, ok := a.(Int128); ok {
		a = w.Float64()
	}
	if w, ok := b.(Int128); ok {
		b = w.Float64()
	}
	return runtime.Exponent(a, b), nil
}

// checkedCompare is a comparison operator's replacement: two integers of
// either width compare exactly (an int64 beside an Int128 included), an
// Int128 beside a float64 as floats, and every other pair by expr's own
// comparison — strings, bools, times, nil, and its own type errors.
func checkedCompare(op string, own func(a, b any) bool) func(...any) (any, error) {
	return func(params ...any) (any, error) {
		a, b := exprDeref(params[0]), exprDeref(params[1])
		if c, ok := exactIntCmp(a, b); ok {
			switch op {
			case "==":
				return c == 0, nil
			case "!=":
				return c != 0, nil
			case "<":
				return c < 0, nil
			case "<=":
				return c <= 0, nil
			case ">":
				return c > 0, nil
			}
			return c >= 0, nil
		}
		if fa, fb, ok := exprWideFloat(a, b); ok {
			return own(fa, fb), nil
		}
		return own(a, b), nil
	}
}

// exprBuiltinFuncs is expr's own builtins by name, for the patched
// builtins to delegate every non-Int128 call to.
var exprBuiltinFuncs = func() map[string]func(...any) (any, error) {
	m := make(map[string]func(...any) (any, error), len(builtin.Builtins))
	for _, f := range builtin.Builtins {
		switch {
		case f.Func != nil:
			m[f.Name] = f.Func
		case f.Fast != nil: // int, float, abs: the one-argument fast form
			fast, name := f.Fast, f.Name
			m[name] = func(args ...any) (any, error) {
				if len(args) != 1 {
					return nil, fmt.Errorf("invalid number of arguments (expected 1, got %d) for %s", len(args), name)
				}
				return fast(args[0]), nil
			}
		}
	}
	return m
}()

// wideBuiltin is a patched builtin: wide handles the Int128 cases
// (handled=false means none applied) and expr's own builtin takes the
// rest unchanged.
func wideBuiltin(name string, wide func(args []any) (v any, handled bool, err error)) func(...any) (any, error) {
	own := exprBuiltinFuncs[name]
	return func(params ...any) (any, error) {
		args := make([]any, len(params))
		for i := range params {
			args[i] = exprDeref(params[i])
		}
		if v, handled, err := wide(args); handled {
			return v, err
		}
		return own(args...)
	}
}

// wideInt is int(x) of an Int128: the int when it fits, else an error
// — the narrowing never wraps.
func wideInt(args []any) (any, bool, error) {
	if len(args) != 1 {
		return nil, false, nil
	}
	w, ok := args[0].(Int128)
	if !ok {
		return nil, false, nil
	}
	i, fits := w.Int64()
	if !fits {
		return nil, true, fmt.Errorf("int(): %s does not fit an int64 (keep it int128, or cast the column)", w)
	}
	return int(i), true, nil
}

// wideFloat is float(x) of an Int128: the nearest float64.
func wideFloat(args []any) (any, bool, error) {
	if len(args) != 1 {
		return nil, false, nil
	}
	if w, ok := args[0].(Int128); ok {
		return w.Float64(), true, nil
	}
	return nil, false, nil
}

// wideAbs is abs(x) of an Int128 (MinInt128 overflows).
func wideAbs(args []any) (any, bool, error) {
	if len(args) != 1 {
		return nil, false, nil
	}
	if w, ok := args[0].(Int128); ok {
		v, err := w.CheckedAbs()
		return v, true, err
	}
	return nil, false, nil
}

// wideExtreme is min (dir −1) or max (dir 1) over scalar arguments when
// one of them is an Int128: integers compare exactly and the extreme is
// an Int128; with a float among them the comparison is as floats and the
// winner keeps its own type, as expr's own min/max do.
func wideExtreme(dir int) func(args []any) (any, bool, error) {
	name := map[int]string{-1: "min", 1: "max"}[dir]
	return func(args []any) (any, bool, error) {
		hasWide := false
		for _, a := range args {
			if _, ok := a.(Int128); ok {
				hasWide = true
			}
		}
		if !hasWide {
			return nil, false, nil
		}
		allInt := true
		for _, a := range args {
			if _, ok := asInt128(a); !ok {
				allInt = false
			}
		}
		if allInt {
			best, _ := asInt128(args[0])
			for _, a := range args[1:] {
				w, _ := asInt128(a)
				if w.Cmp(best) == dir {
					best = w
				}
			}
			return best, true, nil
		}
		best := args[0]
		bf, ok := toFloat(best)
		if !ok {
			return nil, true, fmt.Errorf("invalid argument for %s (type %T)", name, best)
		}
		for _, a := range args[1:] {
			f, ok := toFloat(a)
			if !ok {
				return nil, true, fmt.Errorf("invalid argument for %s (type %T)", name, a)
			}
			if (dir < 0 && f < bf) || (dir > 0 && f > bf) {
				best, bf = a, f
			}
		}
		return best, true, nil
	}
}

// exprWidePair reports an integer pair with an Int128 on at least one
// side, both widened; false for any other pair.
func exprWidePair(a, b any) (Int128, Int128, bool) {
	_, aw := a.(Int128)
	_, bw := b.(Int128)
	if !aw && !bw {
		return Int128{}, Int128{}, false
	}
	x, ok := asInt128(a)
	if !ok {
		return Int128{}, Int128{}, false
	}
	y, ok := asInt128(b)
	if !ok {
		return Int128{}, Int128{}, false
	}
	return x, y, true
}

// exprWideFloat reports an Int128 beside a float64, the Int128 widened to
// its nearest float64 (the rule int beside float follows).
func exprWideFloat(a, b any) (any, any, bool) {
	if w, ok := a.(Int128); ok {
		if f, ok := b.(float64); ok {
			return w.Float64(), f, true
		}
	}
	if w, ok := b.(Int128); ok {
		if f, ok := a.(float64); ok {
			return f, w.Float64(), true
		}
	}
	return a, b, false
}

// exprDeref is what expr's compiler emits (OpDeref) before its own binary
// operator for an operand of unknown type — every ssql field is one — and
// what a call's variadic parameters do not get: a pointer or interface is
// unwrapped to the value, a nil pointer to nil (a nullable window value,
// the lag before the first row, reaches an expression as a *int64).
func exprDeref(v any) any {
	switch v.(type) {
	case nil, int64, int, float64, string, bool, Int128:
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

// checkedArithPatcher rewrites `a + b` into `_ssql_add(a, b)` (and every
// operator in exprCheckedOperators), `-a` into `_ssql_neg(a)`, `sum(xs)`
// into `_ssql_sum(xs)`, `sum(xs, f)` into `_ssql_sum(map(xs, f))` and the
// builtins of exprCheckedBuiltins into their calls. Walk is post-order,
// so the operands have already been rewritten; the calls it builds hold
// none of the shapes it matches, so the visitor pass converges. The
// aggregation patcher (expr_agg.go) runs before it — visitors run in
// registration order, and every aggregation compile registers it first —
// so the group-level sum(_records, f) it produces is rewritten here too;
// the typed lowering recognises both spellings (aggSumPredicate).
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
		if n.Name == "sum" && len(n.Arguments) >= 1 && len(n.Arguments) <= 2 {
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
		if name, ok := exprCheckedBuiltins[n.Name]; ok {
			ast.Patch(node, &ast.CallNode{
				Callee:    &ast.IdentifierNode{Value: name},
				Arguments: n.Arguments,
			})
		}
		return
	case *ast.BinaryNode:
		if name, ok := exprCheckedOperators[n.Operator]; ok {
			ast.Patch(node, &ast.CallNode{
				Callee:    &ast.IdentifierNode{Value: name},
				Arguments: []ast.Node{n.Left, n.Right},
			})
		}
	}
}

// ExprCheckedArithFunction reports whether name is one of the call
// targets the overflow patch rewrites operators and builtins into, for
// the walkers that tell a field reference from a function call (the
// identifier validation in cmd/ssql/lib/runtime) and for the transpiler.
func ExprCheckedArithFunction(name string) bool { return exprCheckedNames[name] }

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
