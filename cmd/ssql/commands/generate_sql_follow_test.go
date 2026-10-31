package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFollowSQL pins join -follow's recursive CTE per dialect (DFC161):
// -all-paths everywhere, the keyed modes on DuckDB only (USING KEY with
// the in-round QUALIFY that carries the tie key), and the refusals by
// name elsewhere. Results are checked by the equivalence lanes.
func TestFollowSQL(t *testing.T) {
	dir := t.TempDir()
	seeds := filepath.Join(dir, "seeds.csv")
	flights := filepath.Join(dir, "flights.csv")
	os.WriteFile(seeds, []byte("city,label\nSYD,home\n"), 0o644)
	os.WriteFile(flights, []byte("src,dst,mins\nSYD,MEL,90\nMEL,ADL,75\n"), 0o644)
	from := "ssql from csv " + seeds
	join := "ssql join " + flights + " -on city src -follow dst "

	t.Run("duckdb_keyed", func(t *testing.T) {
		sql := mustDialect(t, dialectDuckDB, from, join+"-depth hops -lowest-sum mins total -path p")
		wantAll(t, sql, "WITH RECURSIVE", "USING KEY (__sid, __node)", "LEFT JOIN recurring.__walk r",
			"QUALIFY row_number() OVER (PARTITION BY w.__sid, e.dst", "error('join -follow -lowest-sum",
			"CAST(0 AS HUGEINT)", "w.__node AS city, s.label, w.__depth AS hops, w.__t1 AS total, w.__path AS p")
	})
	t.Run("duckdb_all_paths_both", func(t *testing.T) {
		sql := mustDialect(t, dialectDuckDB, from, join+"-all-paths -both -max-depth 3")
		wantAll(t, sql, "UNION\n", "dst AS src, src AS dst", "NOT list_contains(w.__path, e.dst)", "w.__depth < 3")
		if strings.Contains(sql, "USING KEY") {
			t.Errorf("-all-paths is not keyed:\n%s", sql)
		}
	})
	t.Run("postgres_all_paths", func(t *testing.T) {
		sql := mustDialect(t, dialectPostgres, from, join+"-all-paths -sum mins m")
		wantAll(t, sql, `ARRAY["city"]`, `NOT e."dst" = ANY(w.__path)`, "CAST(0 AS NUMERIC(39,0))")
	})
	t.Run("datafusion_all_paths", func(t *testing.T) {
		sql := mustDialect(t, dialectDataFusion, from, join+"-all-paths -depth d")
		wantAll(t, sql, `make_array("city")`, `NOT array_has(w.__path, e."dst")`)
	})
	t.Run("refusals", func(t *testing.T) {
		for _, d := range []sqlDialect{dialectPostgres, dialectDataFusion} {
			for flags, want := range map[string]string{
				"":                   "-fewest-hops (the default mode) has no " + string(d) + " translation",
				"-fewest-hops":       "-fewest-hops (the default mode) has no " + string(d) + " translation",
				"-lowest-sum mins t": "join -follow -lowest-sum has no " + string(d) + " translation",
				"-all-paths -path p": "join -follow -path has no " + string(d) + " translation",
			} {
				if _, err := assembleDialect(t, d, from, join+flags); err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("%s %q: want %q, got %v", d, flags, want, err)
				}
			}
		}
	})
}
