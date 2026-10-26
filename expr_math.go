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
// hash(n[, seed]) is the reproducible integer mix (exprfn.Hash, DFC154
// §6): an int64 in [0, 2^32), the same in every lane, for spread
// without a file. ExprMathFunctionNames lists the set, for
// ExprCompiledFunctions (a call is a function, a bare name a field) and
// the catalog.
var ExprMathFunctionNames = []string{
	"sqrt", "cbrt", "exp", "ln", "log10", "log2", "log", "pow",
	"sin", "cos", "tan", "asin", "acos", "atan", "atan2",
	"sinh", "cosh", "tanh", "asinh", "acosh", "atanh",
	"degrees", "radians", "pi", "sign", "trunc", "round",
	"isnan", "isinf", "isfinite", "gcd", "lcm", "factorial", "gamma", "lgamma",
	"hash", "int128", "decimal",
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
		"gamma": math.Gamma, "lgamma": exprfn.Lgamma,
	} {
		unary(name, f)
	}
	// trunc(x): toward zero; a decimal truncates to a decimal at scale 0
	// (DuckDB's trunc(DECIMAL) is DECIMAL(·,0); DFC155 E), a number to a float.
	bind("trunc", func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("trunc() takes one argument")
		}
		if d, ok := exprDeref(args[0]).(Decimal); ok {
			return NewDecimal(d.Trunc(), 0)
		}
		x, err := exprNumber("trunc", args[0])
		if err != nil {
			return nil, err
		}
		return math.Trunc(x), nil
	}, new(func(any) any))
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
	// int128(x): the widening conversion (DFC155 C) — an integer, a whole
	// float, a digit string (the way to write a literal past int64, which
	// expr refuses at compile), or an int128 as it is; nil stays nil.
	bind("int128", func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("int128() takes one argument")
		}
		switch x := exprDeref(args[0]).(type) {
		case nil:
			return nil, nil
		case Int128:
			return x, nil
		case int64:
			return Int128From(x), nil
		case int:
			return Int128From(int64(x)), nil
		case float64:
			if w, ok := Int128FromFloat(x); ok {
				return w, nil
			}
			return nil, fmt.Errorf("int128(): %v is not a whole number that fits an int128", x)
		case string:
			w, err := ParseInt128(x)
			if err != nil {
				return nil, fmt.Errorf("int128(): %q is not an integer that fits an int128", x)
			}
			return w, nil
		}
		return nil, fmt.Errorf("int128() needs a number or a digit string, got %T", args[0])
	}, new(func(any) any))
	// decimal(x) and decimal(x, scale): the fixed-point conversion (DFC155
	// E) — a decimal string takes its scale from the text (the way to
	// write 0.10 exactly), an integer is exact at the scale (0 without
	// one), a float by its shortest decimal form, a decimal is brought to
	// the scale; every rescale rounds half away from zero. nil stays nil.
	bind("decimal", func(args ...any) (any, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("decimal(x) or decimal(x, scale)")
		}
		scale, hasScale := -1, len(args) == 2
		if hasScale {
			n, err := exprInteger("decimal", "scale", exprDeref(args[1]))
			if err != nil {
				return nil, err
			}
			if n < 0 || n > MaxDecimalScale {
				return nil, fmt.Errorf("decimal(): scale %d is outside 0..%d", n, MaxDecimalScale)
			}
			scale = int(n)
		}
		at := func(d Decimal) (any, error) {
			if !hasScale {
				return d, nil
			}
			return d.Rescale(scale)
		}
		switch x := exprDeref(args[0]).(type) {
		case nil:
			return nil, nil
		case Decimal:
			return at(x)
		case int64, int, Int128:
			d, ok := asDecimal(x)
			if !ok {
				return nil, fmt.Errorf("decimal(): %v does not fit DECIMAL(38)", x)
			}
			return at(d)
		case float64:
			if hasScale {
				d, ok := DecimalFromFloat(x, scale)
				if !ok {
					return nil, fmt.Errorf("decimal(): %v is not a decimal of scale %d", x, scale)
				}
				return d, nil
			}
			d, ok := decimalFromFloatExact(x)
			if !ok {
				return nil, fmt.Errorf("decimal(): %v is not a decimal", x)
			}
			return d, nil
		case string:
			d, err := ParseDecimal(x)
			if err != nil {
				return nil, fmt.Errorf("decimal(): %q is not a decimal that fits 38 digits", x)
			}
			return at(d)
		}
		return nil, fmt.Errorf("decimal() needs a number or a decimal string, got %T", args[0])
	}, new(func(any) any), new(func(any, any) any))
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
		if d, ok := exprDeref(args[0]).(Decimal); ok {
			// A decimal rounds to a decimal of the places, half away from
			// zero (DuckDB's round(DECIMAL, n) is DECIMAL(·,n); DFC155 E).
			n := 0
			if len(args) == 2 {
				p, err := exprInteger("round", "places", args[1])
				if err != nil {
					return nil, err
				}
				n = int(p)
			}
			if n < 0 || n > MaxDecimalScale {
				return nil, fmt.Errorf("round(): places %d is outside 0..%d for a decimal", n, MaxDecimalScale)
			}
			return d.Round(n)
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
	}, new(func(any) any), new(func(any, any) any))
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
	bind("hash", func(args ...any) (any, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("hash(n) or hash(n, seed)")
		}
		n, err := exprInteger("hash", "argument", args[0])
		if err != nil {
			return nil, err
		}
		var seed int64
		if len(args) == 2 {
			if seed, err = exprInteger("hash", "seed", args[1]); err != nil {
				return nil, err
			}
		}
		return exprfn.Hash(n, seed), nil
	}, new(func(any) int64), new(func(any, any) int64))
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
	case Int128:
		return x.Float64(), nil
	case Decimal:
		return x.Float64(), nil
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
	case Int128:
		if i, ok := x.Int64(); ok {
			return i, nil
		}
		return 0, fmt.Errorf("%s(): %s %s does not fit an int64", fn, what, x)
	case Decimal:
		if i, ok := x.Int64(); ok {
			return i, nil
		}
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) {
			return int64(x), nil
		}
	}
	return 0, fmt.Errorf("%s(): %s must be an integer, got %v", fn, what, v)
}
