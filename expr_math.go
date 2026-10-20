package ssql

import (
	"fmt"
	"math"

	"github.com/expr-lang/expr"

	"github.com/rosscartlidge/ssql/v4/exprfn"
)

// ExprMathFunctions is the numeric function set of the expression
// language (DFC153): DuckDB's, under DuckDB's names, so a pipeline reads
// the same in `generate sql`. Results are float64, except sign, gcd, lcm
// and factorial (int64) and the isnan/isinf/isfinite predicates (bool);
// an int argument is promoted. A result that is not a number — sqrt of
// a negative, ln(0), overflow — is NaN or ±Inf here and becomes absent
// where the record is written (DFC133, DFC124), in every lane.
//
// No one-argument log: it is base 10 in DuckDB and Postgres and natural
// in Go, Python and C, so ssql has ln(x), log10(x), log2(x) and
// log(b, x) with a base, and log(x) alone says so. round(x) is
// expr-lang's (half away from zero); round(x, n) adds the places.
// ExprMathFunctionNames lists the set, for ExprCompiledFunctions (a call
// is a function, a bare name a field) and the catalog.
var ExprMathFunctionNames = []string{
	"sqrt", "cbrt", "exp", "ln", "log10", "log2", "log", "pow",
	"sin", "cos", "tan", "asin", "acos", "atan", "atan2",
	"sinh", "cosh", "tanh", "asinh", "acosh", "atanh",
	"degrees", "radians", "pi", "sign", "trunc", "round",
	"isnan", "isinf", "isfinite", "gcd", "lcm", "factorial", "gamma", "lgamma",
}

func ExprMathFunctions() []expr.Option {
	var opts []expr.Option
	// Bound under the name and its alias, as the time functions are, so a
	// column of the same name does not make the call "not callable"
	// (ExprFieldShadowing).
	bind := func(name string, fn func(args ...any) (any, error), sigs ...any) {
		opts = append(opts, expr.Function(name, fn, sigs...), expr.Function(exprFnAlias(name), fn, sigs...))
	}
	unary := func(name string, f func(float64) float64) {
		bind(name, func(args ...any) (any, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("%s() takes one argument", name)
			}
			x, err := exprNumber(name, args[0])
			if err != nil {
				return nil, err
			}
			return f(x), nil
		}, new(func(any) float64))
	}
	for name, f := range map[string]func(float64) float64{
		"sqrt": math.Sqrt, "cbrt": math.Cbrt, "exp": math.Exp,
		"ln": math.Log, "log10": math.Log10, "log2": math.Log2,
		"sin": math.Sin, "cos": math.Cos, "tan": math.Tan,
		"asin": math.Asin, "acos": math.Acos, "atan": math.Atan,
		"sinh": math.Sinh, "cosh": math.Cosh, "tanh": math.Tanh,
		"asinh": math.Asinh, "acosh": math.Acosh, "atanh": math.Atanh,
		"degrees": exprfn.Degrees, "radians": exprfn.Radians,
		"trunc": math.Trunc, "gamma": math.Gamma, "lgamma": exprfn.Lgamma,
	} {
		unary(name, f)
	}
	binary := func(name string, f func(a, b float64) float64) {
		bind(name, func(args ...any) (any, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("%s() takes two arguments", name)
			}
			a, err := exprNumber(name, args[0])
			if err != nil {
				return nil, err
			}
			b, err := exprNumber(name, args[1])
			if err != nil {
				return nil, err
			}
			return f(a, b), nil
		}, new(func(any, any) float64))
	}
	binary("atan2", math.Atan2)
	binary("pow", math.Pow)
	bind("log", func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("log takes a base and a value, log(b, x); ln(x) is the natural logarithm, log10(x) base ten")
		}
		b, err := exprNumber("log", args[0])
		if err != nil {
			return nil, err
		}
		x, err := exprNumber("log", args[1])
		if err != nil {
			return nil, err
		}
		return exprfn.Log(b, x), nil
	}, new(func(any, any) float64), new(func(any) float64)) // the one-argument form compiles, to say which log is meant
	bind("pi", func(args ...any) (any, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("pi() takes no argument")
		}
		return math.Pi, nil
	}, new(func() float64))
	bind("sign", func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("sign() takes one argument")
		}
		x, err := exprNumber("sign", args[0])
		if err != nil {
			return nil, err
		}
		return exprfn.Sign(x), nil
	}, new(func(any) int64))
	// round(x) is expr-lang's own; round(x, n) is ssql's, so the built-in
	// gives way to one function that takes one or two arguments.
	opts = append(opts, expr.DisableBuiltin("round"))
	bind("round", func(args ...any) (any, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("round(x) or round(x, places)")
		}
		x, err := exprNumber("round", args[0])
		if err != nil {
			return nil, err
		}
		if len(args) == 1 {
			return math.Round(x), nil
		}
		n, err := exprInteger("round", "places", args[1])
		if err != nil {
			return nil, err
		}
		return exprfn.RoundTo(x, n), nil
	}, new(func(any) float64), new(func(any, any) float64))
	for name, f := range map[string]func(float64) bool{
		"isnan":    math.IsNaN,
		"isinf":    func(x float64) bool { return math.IsInf(x, 0) },
		"isfinite": func(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) },
	} {
		name, f := name, f
		bind(name, func(args ...any) (any, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("%s() takes one argument", name)
			}
			x, err := exprNumber(name, args[0])
			if err != nil {
				return nil, err
			}
			return f(x), nil
		}, new(func(any) bool))
	}
	intBinary := func(name string, f func(a, b int64) int64) {
		bind(name, func(args ...any) (any, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("%s() takes two arguments", name)
			}
			a, err := exprInteger(name, "argument", args[0])
			if err != nil {
				return nil, err
			}
			b, err := exprInteger(name, "argument", args[1])
			if err != nil {
				return nil, err
			}
			return f(a, b), nil
		}, new(func(any, any) int64))
	}
	intBinary("gcd", exprfn.GCD)
	intBinary("lcm", exprfn.LCM)
	bind("factorial", func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("factorial() takes one argument")
		}
		n, err := exprInteger("factorial", "argument", args[0])
		if err != nil {
			return nil, err
		}
		if n > 20 {
			return nil, fmt.Errorf("factorial(%d) overflows an int64 (20! is the largest)", n)
		}
		return exprfn.Factorial(n), nil
	}, new(func(any) int64))
	return opts
}

// exprNumber reads a numeric argument as a float64; a string or other
// value is refused by name.
func exprNumber(fn string, v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	case int:
		return float64(x), nil
	case float32:
		return float64(x), nil
	case int32:
		return float64(x), nil
	}
	return 0, fmt.Errorf("%s() needs a numeric argument, got %T", fn, v)
}

// exprInteger reads an integer argument; a float with no fraction is
// accepted (a literal 2.0), any other float is not.
func exprInteger(fn, what string, v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) {
			return int64(x), nil
		}
	}
	return 0, fmt.Errorf("%s(): %s must be an integer, got %v", fn, what, v)
}
