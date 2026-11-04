package commands

import (
	"strings"
	"testing"
)

// The IP functions and inet comparisons per dialect (DFC162): DuckDB's
// INET and Postgres's inet, a literal beside an inet cast (a bad one an
// error here), DuckDB's range comparisons guarded against its BETWEEN
// folding, arithmetic refused, DataFusion refused by name.
func TestInetSQL(t *testing.T) {
	defer func() { sqlDialectCur = dialectDuckDB; sqlColumnKinds = map[string]string{} }()
	sqlColumnKinds = map[string]string{"ip": "inet"}
	for in, want := range map[string]string{
		`ip < "10.0.0.1"`:        "((ip < CAST('10.0.0.1' AS INET))) IS TRUE",
		`ip == "::1"`:            "(ip = CAST('::1' AS INET))",
		`ipin(ip, "10.0.0.0/8")`: "(ip <<= CAST('10.0.0.0/8' AS INET))",
		"ip(s)":                  "CAST(CAST(s AS INET) AS VARCHAR)",
		"ipfamily(ip)":           "CAST(family(ip) AS BIGINT)",
		"isip(s)":                "(TRY_CAST(s AS INET) IS NOT NULL)",
		"ipnet(ip, 8)":           "network(CAST(host(ip) || '/' || CAST(8 AS VARCHAR) AS INET))",
	} {
		if got, err := exprToSQL(in); err != nil || got != want {
			t.Errorf("duckdb %s = %s, %v; want %s", in, got, err, want)
		}
	}
	if got, _ := exprToSQL("ipint(ip)"); !strings.Contains(got, "AS STRUCT(ip_type UTINYINT, address HUGEINT, mask USMALLINT)") || !strings.Contains(got, "(-170141183460469231731687303715884105727 - 1)") {
		t.Errorf("duckdb ipint: %s", got)
	}
	for in, want := range map[string]string{`ip < "nope"`: "is not an IP address", "ip + 1": "has no arithmetic", `ipin(ip, "10.0.0.0/33")`: "is not an IP address"} {
		if _, err := exprToSQL(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want an error with %q, got %v", in, want, err)
		}
	}
	if got := translateCondition("ip", "ge", "10.0.0.0/8"); got != "(ip >= CAST('10.0.0.0/8' AS INET)) IS TRUE" {
		t.Errorf("duckdb -if ip ge: %s", got)
	}

	sqlDialectCur = dialectPostgres
	for in, want := range map[string]string{
		`ip < "10.0.0.1"`: `("ip" < CAST('10.0.0.1' AS inet))`,
		"ip(s)":           `abbrev(CAST("s" AS inet))`,
		"isip(s)":         `pg_input_is_valid(CAST("s" AS TEXT), 'inet')`,
		"ipnet(ip, 8)":    `network(set_masklen("ip", 8))`,
	} {
		if got, err := exprToSQL(in); err != nil || got != want {
			t.Errorf("postgres %s = %s, %v; want %s", in, got, err, want)
		}
	}
	if got, _ := exprToSQL("ipkey(ip)"); !strings.Contains(got, `inet_send(network("ip")::inet)`) {
		t.Errorf("postgres ipkey: %s", got)
	}
	if _, err := exprToSQL("ipint(ip)"); err == nil || !strings.Contains(err.Error(), "has no postgres translation") {
		t.Errorf("postgres ipint: want the refusal, got %v", err)
	}

	sqlDialectCur = dialectDataFusion
	if _, err := exprToSQL("ipfamily(s)"); err == nil || !strings.Contains(err.Error(), "has no datafusion translation") {
		t.Errorf("datafusion ipfamily: want the refusal, got %v", err)
	}
	if err := sqlRefuseInet(); err == nil || !strings.Contains(err.Error(), "an inet column has no datafusion translation") {
		t.Errorf("datafusion inet column: want the refusal, got %v", err)
	}
}
