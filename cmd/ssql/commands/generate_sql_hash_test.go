package commands

import (
	"strings"
	"testing"
)

// hash() renders as the mixer's arithmetic with xor spelled per dialect
// and every operand cast to BIGINT (an INTEGER literal shifted by 32 is
// 0 on DuckDB); a literal seed, negated or not, folds to its constant;
// the digests are each engine's own, sha1 refused on Postgres.
func TestHashSQL(t *testing.T) {
	defer func() { sqlDialectCur = dialectDuckDB }()
	got, err := exprToSQL("hash(n)")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"xor(", "CAST(n AS BIGINT)", "& 4294967295", ">> 32", "668265263", "* 569420461", "* 1935289751", ">> 16", ">> 15"} {
		if !strings.Contains(got, want) {
			t.Errorf("duckdb hash(n) lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, " # ") || strings.Count(got, "CAST(n AS BIGINT)") != 16 {
		t.Errorf("duckdb hash(n): %d casts, # present %v", strings.Count(got, "CAST(n AS BIGINT)"), strings.Contains(got, " # "))
	}
	if got, _ := exprToSQL("hash(n, -3)"); !strings.Contains(got, ", 2004795789)") || strings.Contains(got, "-3") {
		t.Errorf("a negated literal seed should fold to 2004795789:\n%s", got)
	}
	if got, _ := exprToSQL("hash(n, 7)"); !strings.Contains(got, ", 1051154808)") {
		t.Errorf("seed 7 should fold to its term:\n%s", got)
	}
	if got, _ := exprToSQL("hash(n, m)"); !strings.Contains(got, "CAST(m AS BIGINT)") || !strings.Contains(got, "+ 1) * 668265263) & 4294967295)") {
		t.Errorf("a column seed folds in SQL:\n%s", got)
	}
	for in, want := range map[string]string{"md5(s)": "md5(s)", "sha1(s)": "sha1(s)", "sha256(string(n))": "sha256(CAST(n AS VARCHAR))"} {
		if got, err := exprToSQL(in); err != nil || got != want {
			t.Errorf("duckdb %s = %s, %v", in, got, err)
		}
	}
	if _, err := exprToSQL("hash(n, 1, 2)"); err == nil || !strings.Contains(err.Error(), "hash(n) or hash(n, seed)") {
		t.Errorf("three arguments: %v", err)
	}

	sqlDialectCur = dialectPostgres
	got, _ = exprToSQL("hash(n)")
	if !strings.Contains(got, ` # `) || strings.Contains(got, "xor(") || !strings.Contains(got, `CAST("n" AS BIGINT)`) {
		t.Errorf("postgres hash(n):\n%s", got)
	}
	for in, want := range map[string]string{"md5(s)": `md5("s")`, "sha256(s)": `encode(sha256(convert_to("s", 'UTF8')), 'hex')`} {
		if got, err := exprToSQL(in); err != nil || got != want {
			t.Errorf("postgres %s = %s, %v", in, got, err)
		}
	}
	if _, err := exprToSQL("sha1(s)"); err == nil || !strings.Contains(err.Error(), "has no postgres translation") {
		t.Errorf("postgres sha1: want the refusal, got %v", err)
	}

	sqlDialectCur = dialectDataFusion
	got, _ = exprToSQL("hash(n)")
	if !strings.Contains(got, ` # `) || strings.Contains(got, "xor(") {
		t.Errorf("datafusion hash(n):\n%s", got)
	}
	for in, want := range map[string]string{"md5(s)": `md5("s")`, "sha1(s)": `encode(sha1("s"), 'hex')`, "sha256(s)": `encode(sha256("s"), 'hex')`} {
		if got, err := exprToSQL(in); err != nil || got != want {
			t.Errorf("datafusion %s = %s, %v", in, got, err)
		}
	}
}
