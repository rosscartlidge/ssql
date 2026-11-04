package commands

import (
	"fmt"
	"slices"
	"strings"

	"github.com/expr-lang/expr/ast"

	"github.com/rosscartlidge/ssql/v4"
)

// The inet type and the IP functions in SQL (DFC162 §4 I2).
//
// A column is INET inside the query — DuckDB's inet extension, Postgres's
// inet — so sort, compare, group, join, min and max are the engine's, in
// the same order as ssql's (measured). At the output it is text:
// `duckdb -json` prints an INET as a struct that is not JSON, so the
// DuckDB query's last SELECT casts each inet column to VARCHAR
// (sqlInetOutput); Postgres prints the abbreviated text already, and
// abbrev() is its explicit form (::text adds /32). DataFusion has no IP
// type: a query with an inet column is refused whole, and generate rust
// with it.
//
// The functions: ip() as the text; isip() a TRY_CAST (DuckDB) or
// pg_input_is_valid (Postgres 16); ipfamily() family(); ipnet() network()
// of the address at the new length; ipin() the <<= both engines have;
// ipkey() and ipint() from DuckDB's INET → STRUCT cast (address is the
// value for IPv4, the value − 2^127 for IPv6); ipkey() on Postgres from
// inet_send's bytes, ipint() refused there (no inet-to-integer form).

// sqlRefuseInet refuses an inet column on DataFusion (and so in generate
// rust, which lowers to it).
func sqlRefuseInet() error {
	if sqlDialectCur != dialectDataFusion {
		return nil
	}
	for _, k := range sqlColumnKinds {
		if k == "inet" {
			return dialectRefuse("an inet column", "DataFusion has no IP address type")
		}
	}
	return nil
}

// sqlInetOutput casts each inet column of the result to text on DuckDB:
// the query so far becomes a subquery, whose order a projection keeps
// (an outer ORDER BY would sort the text).
func sqlInetOutput(q *sqlQuery) {
	if sqlDialectCur != dialectDuckDB {
		return
	}
	var cols []string
	if q.columns != nil {
		for _, c := range q.columns {
			if sqlColumnKinds[c] == "inet" {
				cols = append(cols, c)
			}
		}
	} else {
		for c, k := range sqlColumnKinds {
			if k == "inet" {
				cols = append(cols, c)
			}
		}
		slices.Sort(cols)
	}
	if len(cols) == 0 {
		return
	}
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = sqlInetText(quoteIdent(c)) + " AS " + quoteIdent(c)
	}
	wrapAsSubquery(q)
	q.selectExprs = []string{"* REPLACE (" + strings.Join(parts, ", ") + ")"}
}

// sqlInetRange guards an inet ordering comparison on DuckDB: its optimiser
// folds `x >= a AND x < b` into BETWEEN, which INET does not implement
// ("Invalid type for BETWEEN", DuckDB 1.5); `(…) IS TRUE` is not folded
// and is the same condition in a WHERE (NULL is not true either way).
func sqlInetRange(cmp, op string) string {
	if sqlDialectCur != dialectDuckDB {
		return cmp
	}
	switch op {
	case "<", "<=", ">", ">=":
		return "(" + cmp + ") IS TRUE"
	}
	return cmp
}

// sqlInetOperand reports whether node is known to be an inet: a column of
// that kind, or inet()/ipnet().
func sqlInetOperand(node ast.Node) bool {
	switch n := node.(type) {
	case *ast.IdentifierNode:
		if _, ok := sqlExprParams[n.Value]; ok || sqlIdentResolver != nil {
			return false
		}
		return sqlColumnKinds[n.Value] == "inet"
	case *ast.CallNode:
		ident, ok := n.Callee.(*ast.IdentifierNode)
		return ok && (ident.Value == "inet" || ident.Value == "ipnet")
	}
	return false
}

// sqlAsInet renders an operand as an inet: an inet as it is, a string
// literal parsed now (a bad one an error here, not in the engine), any
// other value CAST.
func sqlAsInet(node ast.Node) (string, error) {
	if lit, ok := node.(*ast.StringNode); ok {
		if _, err := ssql.ParseInet(lit.Value); err != nil {
			return "", err
		}
		return "CAST('" + escapeSQL(lit.Value) + "' AS " + sqlInetType() + ")", nil
	}
	s, err := exprNodeToSQL(node)
	if err != nil {
		return "", err
	}
	if sqlInetOperand(node) {
		return s, nil
	}
	return "CAST(" + s + " AS " + sqlInetType() + ")", nil
}

func inetFuncToSQL(name string, args []ast.Node) (string, bool, error) {
	want := map[string]int{"isip": 1, "ip": 1, "inet": 1, "ipfamily": 1, "ipint": 1, "ipkey": 1, "ipnet": 2, "ipin": 2}[name]
	if want == 0 {
		return "", false, nil
	}
	if len(args) != want {
		return "", true, fmt.Errorf("%s() takes %d argument(s)", name, want)
	}
	if sqlDialectCur == dialectDataFusion {
		return "", true, dialectRefuse(name+"()", "DataFusion has no IP address type")
	}
	if name == "isip" {
		s, err := exprNodeToSQL(args[0])
		if err != nil {
			return "", true, err
		}
		if sqlDialectCur == dialectPostgres {
			return "pg_input_is_valid(CAST(" + s + " AS TEXT), 'inet')", true, nil
		}
		return "(TRY_CAST(" + s + " AS INET) IS NOT NULL)", true, nil
	}
	x, err := sqlAsInet(args[0])
	if err != nil {
		return "", true, fmt.Errorf("%s(): %w", name, err)
	}
	switch name {
	case "ip":
		return sqlInetText(x), true, nil
	case "inet":
		return x, true, nil
	case "ipfamily":
		return "CAST(family(" + x + ") AS BIGINT)", true, nil
	case "ipnet":
		bits, err := exprNodeToSQL(args[1])
		if err != nil {
			return "", true, err
		}
		if sqlDialectCur == dialectPostgres {
			return "network(set_masklen(" + x + ", " + bits + "))", true, nil
		}
		return "network(CAST(host(" + x + ") || '/' || CAST(" + bits + " AS VARCHAR) AS INET))", true, nil
	case "ipin":
		p, err := sqlAsInet(args[1])
		if err != nil {
			return "", true, fmt.Errorf("ipin(): %w", err)
		}
		return "(" + x + " <<= " + p + ")", true, nil
	}
	if sqlDialectCur == dialectPostgres {
		if name == "ipkey" {
			// inet_send's hex is family, bits, flag and length (2 digits
			// each), then the address bytes: the key from its tail.
			addr := func(v string) string { return "substr(encode(inet_send(" + v + "), 'hex'), 9)" }
			return "((CASE WHEN family(" + x + ") = 4 THEN '4' ELSE '6' END) || " + addr("network("+x+")::inet") +
				" || lpad(to_hex(masklen(" + x + ")), 2, '0') || " + addr(x) + ")", true, nil
		}
		return "", true, dialectRefuse(name+"()", "Postgres has no inet-to-integer conversion; the inet column sorts and compares natively")
	}
	if name == "ipint" {
		return duckInetInt(x), true, nil
	}
	// ipkey: the family digit, the network's address, the length and the
	// address in fixed-width hex — exec's Inet.Key, character for character.
	net := "network(" + x + ")"
	return "(CASE WHEN " + duckInetStruct(x) + ".ip_type = 1 THEN '4' || " +
		duckHex(duckInetStruct(net)+".address", 8) + " || " + duckHex(duckInetStruct(x)+".mask", 2) + " || " + duckHex(duckInetStruct(x)+".address", 8) +
		" ELSE '6' || " + duckHex(duckInetTwos(net), 32) + " || " + duckHex(duckInetStruct(x)+".mask", 2) + " || " + duckHex(duckInetTwos(x), 32) + " END)", true, nil
}

// duckInetStruct is DuckDB's INET as its struct (ip_type, address, mask):
// address is the value for IPv4 and the value − 2^127 for IPv6.
func duckInetStruct(x string) string {
	return "CAST(" + x + " AS STRUCT(ip_type UTINYINT, address HUGEINT, mask USMALLINT))"
}

// duckInetTwos is an IPv6 address's 128 bits in two's complement from the
// struct's offset form: a negative address field is a value below 2^127
// (+2^127), a non-negative one at or above it (−2^127). −2^127 is spelled
// (−(2^127 − 1) − 1), the one form that fits: a + 2^127 written directly
// folded 2^127 − 1 + 1 first and overflowed.
func duckInetTwos(x string) string {
	const minHalf = "(-170141183460469231731687303715884105727 - 1)"
	a := duckInetStruct(x) + ".address"
	return "(CASE WHEN " + a + " < 0 THEN " + a + " - " + minHalf + " ELSE " + a + " + " + minHalf + " END)"
}

// duckInetInt is ipint: an IPv4 address's value, an IPv6 one's two's
// complement (Inet.Int128).
func duckInetInt(x string) string {
	return "(CASE WHEN " + duckInetStruct(x) + ".ip_type = 1 THEN " + duckInetStruct(x) + ".address ELSE " + duckInetTwos(x) + " END)"
}

// duckHex is n lower-case hex digits: hex() of a negative HUGEINT is its
// 32-digit two's complement, of anything else the shortest form.
func duckHex(v string, n int) string {
	return fmt.Sprintf("lpad(lower(hex(%s)), %d, '0')", v, n)
}
