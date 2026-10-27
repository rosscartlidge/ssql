package commands

import (
	"fmt"
	"strings"

	"github.com/expr-lang/expr/ast"
)

// The numeric functions (DFC153) in SQL. Names are DuckDB's, so most
// translate by name; the differences are per dialect (pow is power on
// Postgres and DataFusion, sign is signum on DataFusion, round(x, n)
// rounds a NUMERIC on Postgres), and a function an engine lacks is
// refused by design (the "has no DIALECT translation" wording the
// equivalence gate's dialect lanes read as a skip).
//
// A result that is not a number is absent in ssql (DFC153 §4.2: the
// JSON writer makes NaN and ±Inf null, DFC133; null reads as absent,
// DFC124). DuckDB and Postgres RAISE on a domain error instead (sqrt of
// a negative, ln(0)) and return inf on overflow, so sqlFinite renders the
// rule: a domain-error function is wrapped in TRY (DuckDB) or a domain
// CASE guard (Postgres), and an infinite result is made NULL. The same
// guard applies to division (sqlDivide), whose inf the DuckDB lane
// printed until 2026-10-20.

// mathFloatFuncs are the functions of one or two numbers with a float
// result, by the name each dialect uses ("" = refuse).
type mathSQLName struct{ duck, pg, df string }

var mathSQLNames = map[string]mathSQLName{
	"sqrt": {"sqrt", "sqrt", "sqrt"}, "cbrt": {"cbrt", "cbrt", "cbrt"}, "exp": {"exp", "exp", "exp"},
	"ln": {"ln", "ln", "ln"}, "log10": {"log10", "log10", "log10"}, "log2": {"log2", "log2", "log2"}, "log": {"log", "log", "log"},
	"pow": {"pow", "power", "power"},
	"sin": {"sin", "sin", "sin"}, "cos": {"cos", "cos", "cos"}, "tan": {"tan", "tan", "tan"},
	"asin": {"asin", "asin", "asin"}, "acos": {"acos", "acos", "acos"}, "atan": {"atan", "atan", "atan"}, "atan2": {"atan2", "atan2", "atan2"},
	"sinh": {"sinh", "sinh", "sinh"}, "cosh": {"cosh", "cosh", "cosh"}, "tanh": {"tanh", "tanh", "tanh"},
	"asinh": {"asinh", "asinh", "asinh"}, "acosh": {"acosh", "acosh", "acosh"}, "atanh": {"atanh", "atanh", "atanh"},
	"degrees": {"degrees", "degrees", "degrees"}, "radians": {"radians", "radians", "radians"},
	"trunc": {"trunc", "trunc", "trunc"}, "sign": {"sign", "sign", "signum"},
	"gamma": {"gamma", "", ""}, "lgamma": {"lgamma", "", ""},
	"gcd": {"gcd", "gcd", "gcd"}, "lcm": {"lcm", "lcm", "lcm"}, "factorial": {"factorial", "", "factorial"},
	"isnan": {"isnan", "", "isnan"}, "isinf": {"isinf", "", ""}, "isfinite": {"isfinite", "", ""},
}

// mathIntResult are the functions whose result is never non-finite.
var mathIntResult = map[string]bool{"sign": true, "gcd": true, "lcm": true, "factorial": true, "isnan": true, "isinf": true, "isfinite": true}

// mathDomain is the Postgres domain guard per function, on its (last)
// argument; the DuckDB form is TRY(...) for every function listed here.
var mathDomain = map[string]string{
	"sqrt": "%s >= 0", "ln": "%s > 0", "log10": "%s > 0", "log2": "%s > 0", "log": "%s > 0",
	"asin": "%s BETWEEN -1 AND 1", "acos": "%s BETWEEN -1 AND 1", "acosh": "%s >= 1", "atanh": "%s > -1 AND %s < 1",
}

// mathOverflow is the Postgres overflow guard per function: where DuckDB
// returns Infinity (which sqlFinite makes NULL), Postgres raises `value
// out of range: overflow` and aborts the statement, so the argument is
// tested first and the result is NULL past it — the same absent value.
var mathOverflow = map[string]string{
	"exp": "%s < 709.782712893384", "sinh": "abs(%s) < 710.4758600739439", "cosh": "abs(%s) < 710.4758600739439",
}

// mathFuncToSQL translates a DFC153 call; ok=false for any other name
// (round with one argument stays expr-lang's, in exprFuncs).
func mathFuncToSQL(name string, args []ast.Node) (string, bool, error) {
	if name == "pi" {
		if len(args) != 0 {
			return "", true, fmt.Errorf("pi() takes no argument")
		}
		return "pi()", true, nil
	}
	if name == "round" {
		if len(args) != 2 {
			return "", false, nil
		}
		x, err := exprNodeToSQL(args[0])
		if err != nil {
			return "", true, err
		}
		n, err := exprNodeToSQL(args[1])
		if err != nil {
			return "", true, err
		}
		if sqlDialectCur == dialectPostgres {
			return "round(CAST(" + x + " AS NUMERIC), " + n + ")", true, nil
		}
		return "round(" + x + ", " + n + ")", true, nil
	}
	names, ok := mathSQLNames[name]
	if !ok {
		return "", false, nil
	}
	if name == "log" && len(args) == 1 {
		return "", true, fmt.Errorf("log takes a base and a value, log(b, x); ln(x) is the natural logarithm, log10(x) base ten")
	}
	sqlName := names.duck
	switch sqlDialectCur {
	case dialectPostgres:
		sqlName = names.pg
	case dialectDataFusion:
		sqlName = names.df
	}
	if sqlName == "" {
		return "", true, dialectRefuse(name+"()", "the engine has no such function (DFC153 §3)")
	}
	parts := make([]string, len(args))
	for i, a := range args {
		s, err := exprNodeToSQL(a)
		if err != nil {
			return "", true, err
		}
		parts[i] = s
	}
	if name == "factorial" && sqlDialectCur == dialectDuckDB {
		parts[0] = "CAST(" + parts[0] + " AS INTEGER)" // DuckDB's factorial takes INTEGER, not BIGINT
	}
	call := sqlName + "(" + strings.Join(parts, ", ") + ")"
	if sqlDialectCur == dialectPostgres {
		// Postgres has no log2, and its two-argument log takes NUMERICs:
		// both as a ratio of natural logarithms over doubles.
		switch name {
		case "log2":
			call = "(ln(" + parts[0] + ") / ln(2.0))"
		case "log":
			call = "(ln(" + parts[1] + ") / ln(" + parts[0] + "))"
		}
	}
	if mathIntResult[name] {
		return call, true, nil
	}
	if guard, domain := mathDomain[name]; domain && len(parts) > 0 {
		last := parts[len(parts)-1]
		switch sqlDialectCur {
		case dialectDuckDB:
			call = "TRY(" + call + ")"
		case dialectPostgres:
			call = "CASE WHEN " + strings.ReplaceAll(guard, "%s", last) + " THEN " + call + " END"
		}
	}
	if guard, overflows := mathOverflow[name]; overflows && sqlDialectCur == dialectPostgres && len(parts) == 1 {
		call = "CASE WHEN " + strings.ReplaceAll(guard, "%s", parts[0]) + " THEN " + call + " END"
	}
	return sqlFinite(call), true, nil
}

// sqlFinite makes an infinite (or NaN) double NULL: ssql has no value for
// a number that is not one. The operand is repeated inside the CASE; it is
// a pure expression, so that is exact (the ?? translation does the same).
func sqlFinite(v string) string {
	switch sqlDialectCur {
	case dialectPostgres:
		return "(CASE WHEN " + v + " IN ('Infinity', '-Infinity', 'NaN') THEN NULL ELSE " + v + " END)"
	case dialectDataFusion:
		return "(CASE WHEN isnan(" + v + ") OR abs(" + v + ") = 'inf'::double THEN NULL ELSE " + v + " END)"
	}
	return "(CASE WHEN isfinite(" + v + ") THEN " + v + " END)"
}
