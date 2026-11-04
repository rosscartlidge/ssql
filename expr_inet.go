package ssql

import (
	"fmt"

	"github.com/expr-lang/expr"
)

// ExprInetFunctionNames are the IP functions (DFC162 §4 I2). Each takes
// an inet or a string that parses as one, and has one result type:
// isip(s) bool (never an error); ip(x) the canonical text; inet(x) the
// inet value (the expression door to the type); ipfamily(x) 4 or 6;
// ipint(x) the address as an int128 (two's complement for IPv6, so not
// a sort key there); ipkey(x) a text key that sorts as inet does;
// ipnet(x, bits) the network, an inet; ipin(x, p) the engines' <<=.
var ExprInetFunctionNames = []string{"isip", "ip", "inet", "ipfamily", "ipint", "ipkey", "ipnet", "ipin"}

// ExprInetArg reads an IP function's argument: an Inet, or text that
// parses as one; anything else is an error naming the function.
func ExprInetArg(fn, what string, v any) (Inet, error) {
	switch x := exprDeref(v).(type) {
	case Inet:
		return x, nil
	case string:
		p, err := ParseInet(x)
		if err != nil {
			return Inet{}, fmt.Errorf("%s(): %w", fn, err)
		}
		return p, nil
	}
	return Inet{}, fmt.Errorf("%s(): the %s must be an IP address or prefix, got %T", fn, what, v)
}

func ExprInetFunctions() []expr.Option {
	var opts []expr.Option
	bind := func(name string, fn func(args ...any) (any, error), sigs ...any) {
		opts = append(opts, expr.Function(name, fn, sigs...), expr.Function(exprFnAlias(name), fn, sigs...))
	}
	one := func(name string, f func(Inet) any, sig any) {
		bind(name, func(args ...any) (any, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("%s() takes one argument", name)
			}
			x, err := ExprInetArg(name, "argument", args[0])
			if err != nil {
				return nil, err
			}
			return f(x), nil
		}, sig)
	}
	bind("isip", func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("isip() takes one argument")
		}
		_, err := ExprInetArg("isip", "argument", args[0])
		return err == nil, nil
	}, new(func(any) bool))
	one("ip", func(x Inet) any { return x.String() }, new(func(any) string))
	one("inet", func(x Inet) any { return x }, new(func(any) Inet))
	one("ipfamily", func(x Inet) any { return x.Family() }, new(func(any) int64))
	one("ipint", func(x Inet) any { return x.Int128() }, new(func(any) Int128))
	one("ipkey", func(x Inet) any { return x.Key() }, new(func(any) string))
	bind("ipnet", func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("ipnet(x, bits) takes two arguments")
		}
		x, err := ExprInetArg("ipnet", "address", args[0])
		if err != nil {
			return nil, err
		}
		bits, err := exprInteger("ipnet", "bits", exprDeref(args[1]))
		if err != nil {
			return nil, err
		}
		return x.Network(bits)
	}, new(func(any, any) Inet))
	bind("ipin", func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("ipin(x, prefix) takes two arguments")
		}
		x, err := ExprInetArg("ipin", "address", args[0])
		if err != nil {
			return nil, err
		}
		p, err := ExprInetArg("ipin", "prefix", args[1])
		if err != nil {
			return nil, err
		}
		return x.Within(p), nil
	}, new(func(any, any) bool))
	return opts
}

// exprInetCmp is a VM comparison with an inet on one side: the other side
// read as an address (ok); an error when it cannot be (a string that does
// not parse, a number) — never a comparison of texts or expr's struct
// panic. ok and err are both false/nil when neither side is an inet.
func exprInetCmp(a, b any) (int, bool, error) {
	_, ai := a.(Inet)
	_, bi := b.(Inet)
	if !ai && !bi {
		return 0, false, nil
	}
	x, xok := asInet(a)
	y, yok := asInet(b)
	if !xok || !yok {
		other := b
		if !xok {
			other = a
		}
		return 0, false, fmt.Errorf("an inet compares only with an address or prefix, and %s is not one", describeValue(other))
	}
	return x.Compare(y), true, nil
}
