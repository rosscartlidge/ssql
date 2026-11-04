package commands

import (
	"fmt"
	"strings"

	"github.com/expr-lang/expr/ast"

	"github.com/rosscartlidge/ssql/v4/exprfn"
)

// The UUID functions in SQL (DFC162 §3), each as text, as the other
// lanes hold it.
//
// uuid4() is every engine's own random UUID. uuid7() is DuckDB's
// uuidv7(); Postgres has it only from 18 and DataFusion not at all.
// uuid5(ns, name) has no function in DuckDB: it is built from sha1 over
// the namespace's bytes and the name's, by slicing the hex and setting
// the version and variant nibbles (verified against Python's uuid). The
// digest is repeated once per slice, as hash's arithmetic repeats its
// argument. Postgres has v5 only through the uuid-ossp extension and
// DataFusion has no sha1, so both refuse.

func uuidFuncToSQL(name string, args []ast.Node) (string, bool, error) {
	switch name {
	case "uuid4", "uuid7":
		if len(args) != 0 {
			return "", true, fmt.Errorf("%s() takes no arguments", name)
		}
		switch {
		case name == "uuid4" && sqlDialectCur == dialectPostgres:
			return "gen_random_uuid()::text", true, nil
		case name == "uuid4" && sqlDialectCur == dialectDataFusion:
			return "uuid()", true, nil // Utf8 already
		case name == "uuid4":
			return "CAST(uuid() AS VARCHAR)", true, nil
		case sqlDialectCur == dialectPostgres:
			return "", true, dialectRefuse("uuid7()", "Postgres has uuidv7() only from version 18")
		case sqlDialectCur == dialectDataFusion:
			return "", true, dialectRefuse("uuid7()", "DataFusion has only the random uuid()")
		}
		return "CAST(uuidv7() AS VARCHAR)", true, nil
	case "uuid5":
		sql, err := uuid5ToSQL(args)
		return sql, true, err
	}
	return "", false, nil
}

func uuid5ToSQL(args []ast.Node) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("uuid5(namespace, name) takes two arguments")
	}
	switch sqlDialectCur {
	case dialectPostgres:
		return "", dialectRefuse("uuid5()", "Postgres has uuid_generate_v5 only through the uuid-ossp extension")
	case dialectDataFusion:
		return "", dialectRefuse("uuid5()", "DataFusion has no sha1")
	}
	// The namespace as 32 hex digits: a literal resolves here (and a bad
	// one is an error now, not per row); a column maps the RFC names.
	var nsHex string
	if lit, ok := args[0].(*ast.StringNode); ok {
		b, err := exprfn.ParseUUIDNamespace(lit.Value)
		if err != nil {
			return "", err
		}
		nsHex = fmt.Sprintf("'%x'", b)
	} else {
		ns, err := exprNodeToSQL(args[0])
		if err != nil {
			return "", err
		}
		var when strings.Builder
		for _, n := range []string{"dns", "url", "oid", "x500"} {
			fmt.Fprintf(&when, " WHEN '%s' THEN '%s'", n, strings.ReplaceAll(exprfn.UUIDNamespaces[n], "-", ""))
		}
		nsHex = "(CASE lower(" + ns + ")" + when.String() + " ELSE replace(lower(" + ns + "), '-', '') END)"
	}
	name, err := exprNodeToSQL(args[1])
	if err != nil {
		return "", err
	}
	h := "sha1(unhex(" + nsHex + ") || encode(" + name + "))"
	part := func(from, n int) string { return fmt.Sprintf("substr(%s, %d, %d)", h, from, n) }
	return "(" + part(1, 8) + " || '-' || " + part(9, 4) + " || '-5' || " + part(14, 3) + " || '-' || " +
		"substr('89ab89ab89ab89ab', strpos('0123456789abcdef', " + part(17, 1) + "), 1) || " +
		part(18, 3) + " || '-' || " + part(21, 12) + ")", nil
}
