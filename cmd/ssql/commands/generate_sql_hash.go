package commands

import (
	"fmt"
	"strconv"

	"github.com/expr-lang/expr/ast"

	"github.com/rosscartlidge/ssql/v4/exprfn"
)

// hash(n[, seed]) and the digests in SQL.
//
// hash is exprfn.Hash rendered as arithmetic every engine computes
// identically (DFC154 §6): a BIGINT is folded to 32 bits with & and >>,
// then lowbias32's xorshifts and multiplications; each multiplier is
// below 2^31 and each operand below 2^32, so no product overflows the
// signed 64-bit integer DuckDB, Postgres and DataFusion all raise on.
// Only xor is spelled per dialect (xor() on DuckDB, # elsewhere). The
// argument is repeated once per xorshift — sixteen times — so it reads
// best over a column; a literal seed folds to its constant here.
//
// The digests are each engine's own, hex-encoded where an engine returns
// bytes; sha1 is pgcrypto's on Postgres, so it is refused there.

const hashMask32 = "4294967295"

func sqlXor(a, b string) string {
	if sqlDialectCur == dialectDuckDB {
		return "xor(" + a + ", " + b + ")"
	}
	return "(" + a + " # " + b + ")"
}

// sqlFold32 is exprfn's fold32: low word xor high word of the value as
// a BIGINT — a literal or an INTEGER column shifted by 32 is 0 on DuckDB
// (the shift exceeds the width), so the cast is what makes it 64-bit.
func sqlFold32(v string) string {
	v = "CAST(" + v + " AS BIGINT)"
	return sqlXor("("+v+" & "+hashMask32+")", "(("+v+" >> 32) & "+hashMask32+")")
}

// sqlIntLiteral reads an integer literal, negated or not.
func sqlIntLiteral(n ast.Node) (int64, bool) {
	if u, ok := n.(*ast.UnaryNode); ok && u.Operator == "-" {
		if v, ok := sqlIntLiteral(u.Node); ok {
			return -v, true
		}
		return 0, false
	}
	lit, ok := n.(*ast.IntegerNode)
	if !ok {
		return 0, false
	}
	return int64(lit.Value), true
}

func hashToSQL(args []ast.Node) (string, error) {
	if len(args) < 1 || len(args) > 2 {
		return "", fmt.Errorf("hash(n) or hash(n, seed)")
	}
	n, err := exprNodeToSQL(args[0])
	if err != nil {
		return "", err
	}
	// The seed term: (fold32(seed) + 1) * 0x27d4eb2f, masked.
	seedTerm := strconv.FormatInt(int64(0x27d4eb2f), 10) // seed 0
	if len(args) == 2 {
		if lit, ok := sqlIntLiteral(args[1]); ok {
			seedTerm = strconv.FormatInt(int64(exprfn.SeedTerm(lit)), 10)
		} else {
			seed, err := exprNodeToSQL(args[1])
			if err != nil {
				return "", err
			}
			seedTerm = "(((" + sqlFold32(seed) + " + 1) * 668265263) & " + hashMask32 + ")"
		}
	}
	x := sqlXor(sqlFold32(n), seedTerm)
	xorshift := func(x string, s int) string { return sqlXor(x, "("+x+" >> "+strconv.Itoa(s)+")") }
	mul := func(x string, c int64) string { return "((" + x + " * " + strconv.FormatInt(c, 10) + ") & " + hashMask32 + ")" }
	x = xorshift(x, 16)
	x = mul(x, 0x21f0aaad)
	x = xorshift(x, 15)
	x = mul(x, 0x735a2d97)
	x = xorshift(x, 15)
	return x, nil
}

// hashFuncToSQL translates hash() and md5/sha1/sha256; ok=false for any
// other name.
func hashFuncToSQL(name string, args []ast.Node) (string, bool, error) {
	switch name {
	case "hash":
		sql, err := hashToSQL(args)
		return sql, true, err
	case "md5", "sha1", "sha256":
		if len(args) != 1 {
			return "", true, fmt.Errorf("%s() takes one argument", name)
		}
		s, err := exprNodeToSQL(args[0])
		if err != nil {
			return "", true, err
		}
		switch sqlDialectCur {
		case dialectPostgres:
			if name == "sha1" {
				return "", true, dialectRefuse("sha1()", "Postgres has sha1 only through pgcrypto")
			}
			if name == "md5" {
				return "md5(" + s + ")", true, nil
			}
			return "encode(sha256(convert_to(" + s + ", 'UTF8')), 'hex')", true, nil
		case dialectDataFusion:
			if name == "md5" {
				return "md5(" + s + ")", true, nil
			}
			return "encode(" + name + "(" + s + "), 'hex')", true, nil
		}
		return name + "(" + s + ")", true, nil
	}
	return "", false, nil
}
