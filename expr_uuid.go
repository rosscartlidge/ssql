package ssql

import (
	"fmt"

	"github.com/expr-lang/expr"

	"github.com/rosscartlidge/ssql/v4/exprfn"
)

// ExprUUIDFunctionNames are the UUID functions (DFC162 §3): uuid4() and
// uuid7() are random, a new id per call; uuid5(ns, name) is the
// name-based UUID, the same in every lane and in Python's uuid module.
// Each is the canonical lower-case text: a UUID needs no type.
var ExprUUIDFunctionNames = []string{"uuid4", "uuid5", "uuid7"}

func ExprUUIDFunctions() []expr.Option {
	var opts []expr.Option
	bind := func(name string, fn func(args ...any) (any, error), sigs ...any) {
		opts = append(opts, expr.Function(name, fn, sigs...), expr.Function(exprFnAlias(name), fn, sigs...))
	}
	for name, gen := range map[string]func() string{"uuid4": exprfn.UUID4, "uuid7": exprfn.UUID7} {
		bind(name, func(args ...any) (any, error) {
			if len(args) != 0 {
				return nil, fmt.Errorf("%s() takes no arguments", name)
			}
			return gen(), nil
		}, new(func() string))
	}
	bind("uuid5", func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("uuid5(namespace, name) takes two arguments")
		}
		ns, err := exprString("uuid5", "namespace", args[0])
		if err != nil {
			return nil, err
		}
		name, err := exprString("uuid5", "name", args[1])
		if err != nil {
			return nil, err
		}
		return exprfn.TryUUID5(ns, name)
	}, new(func(any, any) string))
	return opts
}
