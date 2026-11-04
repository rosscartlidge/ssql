package commands

import (
	"strings"
	"testing"
)

// The UUID functions per dialect (DFC162): each engine's random uuid as
// text; uuid7 and uuid5 on DuckDB only, a literal namespace resolved at
// translation (a bad one an error there), a column one through a CASE.
func TestUUIDSQL(t *testing.T) {
	defer func() { sqlDialectCur = dialectDuckDB }()
	for in, want := range map[string]string{"uuid4()": "CAST(uuid() AS VARCHAR)", "uuid7()": "CAST(uuidv7() AS VARCHAR)"} {
		if got, err := exprToSQL(in); err != nil || got != want {
			t.Errorf("duckdb %s = %s, %v", in, got, err)
		}
	}
	got, err := exprToSQL(`uuid5("dns", s)`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "sha1(unhex('6ba7b8109dad11d180b400c04fd430c8') || encode(s))") || !strings.Contains(got, "'-5'") || !strings.Contains(got, "'89ab89ab89ab89ab'") {
		t.Errorf("duckdb uuid5 with a literal namespace:\n%s", got)
	}
	if got, _ := exprToSQL("uuid5(ns, s)"); !strings.Contains(got, "CASE lower(ns) WHEN 'dns' THEN '6ba7b8109dad11d180b400c04fd430c8'") || !strings.Contains(got, "ELSE replace(lower(ns), '-', '') END") {
		t.Errorf("duckdb uuid5 with a column namespace:\n%s", got)
	}
	if _, err := exprToSQL(`uuid5("dnsx", s)`); err == nil || !strings.Contains(err.Error(), "not a UUID or one of dns") {
		t.Errorf("a bad literal namespace: %v", err)
	}
	if _, err := exprToSQL("uuid4(n)"); err == nil || !strings.Contains(err.Error(), "no arguments") {
		t.Errorf("uuid4 with an argument: %v", err)
	}

	sqlDialectCur = dialectPostgres
	if got, err := exprToSQL("uuid4()"); err != nil || got != "gen_random_uuid()::text" {
		t.Errorf("postgres uuid4() = %s, %v", got, err)
	}
	for _, in := range []string{"uuid7()", "uuid5('dns', s)"} {
		if _, err := exprToSQL(in); err == nil || !strings.Contains(err.Error(), "has no postgres translation") {
			t.Errorf("postgres %s: want the refusal, got %v", in, err)
		}
	}

	sqlDialectCur = dialectDataFusion
	if got, err := exprToSQL("uuid4()"); err != nil || got != "uuid()" {
		t.Errorf("datafusion uuid4() = %s, %v", got, err)
	}
	for _, in := range []string{"uuid7()", "uuid5('dns', s)"} {
		if _, err := exprToSQL(in); err == nil || !strings.Contains(err.Error(), "has no datafusion translation") {
			t.Errorf("datafusion %s: want the refusal, got %v", in, err)
		}
	}
}
