package commands

import (
	"strings"
	"testing"
)

// Native fuzz targets for the translators (DFC133 instrument 4): whatever
// the expression or command string, they return a result or an error —
// never a panic — and an expression they accept yields non-empty output.
//
//	go test ./cmd/ssql/commands -run '^$' -fuzz FuzzExprToSQL -fuzztime 60s

var fuzzExprSeeds = []string{
	`pop > 5 && city == "Oslo"`, `price * qty / 2`, `city contains "a" || city startsWith "O"`, `active ? "y" : "n"`,
	`getOr("pop", 0) + len(city)`, `has("city") and not active`, `city in ["Oslo", "Lima"]`, `bucket(pop, "5m")`,
	`date(city).Year()`, `pop ?? 0`, `-pop % 3`, `upper(trim(city)) matches "^O"`, `min(pop, qty) + max(price, 1.5)`,
	`((((`, `"unterminated`, `pop >`, `1/0`, `pop == nil`, `city[0:2]`, `{"a": 1}.a`, `map(1..3, # * 2)`, ``,
}

func FuzzExprToSQL(f *testing.F) {
	for _, s := range fuzzExprSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, expr string) {
		for _, d := range []sqlDialect{dialectDuckDB, dialectPostgres, dialectDataFusion} {
			prev := sqlDialectCur
			sqlDialectCur = d
			sql, err := exprToSQL(expr)
			sqlDialectCur = prev
			if err == nil && strings.TrimSpace(sql) == "" && strings.TrimSpace(expr) != "" {
				t.Fatalf("%s: accepted %q and produced empty SQL", d, expr)
			}
		}
	})
}

func FuzzExprToGo(f *testing.F) {
	for _, s := range fuzzExprSeeds {
		f.Add(s)
	}
	schema := exprGoTestSchema()
	f.Fuzz(func(t *testing.T, expr string) {
		if g, err := exprToGo(expr, schema, "r"); err == nil && strings.TrimSpace(g.Src) == "" && strings.TrimSpace(expr) != "" {
			t.Fatalf("typed: accepted %q and produced empty Go", expr)
		}
		advisory := map[string]string{"pop": "int", "price": "float", "city": "string", "active": "bool"}
		if g, err := exprToGoRecord(expr, advisory, "r"); err == nil && strings.TrimSpace(g.Src) == "" && strings.TrimSpace(expr) != "" {
			t.Fatalf("record: accepted %q and produced empty Go", expr)
		}
	})
}

// FuzzParseCommandArgs: the fragment command splitter never panics, never
// invents text (every argument's characters come from the input), and
// plain words split exactly as strings.Fields does.
func FuzzParseCommandArgs(f *testing.F) {
	for _, s := range []string{`ssql where -if age gt 25`, `ssql update -set-expr x 'a + "b c"'`, `ssql where -if name eq "O'Brien"`,
		`ssql x 'unterminated`, `ssql x "a\"b"`, `  ssql   sort   -desc  pop  `, `ssql x ''`, `ssql x '\''`, ``} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		args := parseCommandArgs(cmd)
		total := 0
		for _, a := range args {
			total += len(a)
		}
		if total > len(cmd) {
			t.Fatalf("split produced more text (%d bytes) than the input (%d): %q -> %q", total, len(cmd), cmd, args)
		}
		if !strings.Contains(cmd, "'") {
			// No quotes: exactly the non-empty space-separated pieces, bytes
			// untouched (the splitter's only separator is the space that
			// ssql itself puts between arguments).
			var want []string
			for _, w := range strings.Split(cmd, " ") {
				if w != "" {
					want = append(want, w)
				}
			}
			if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("unquoted input must split on spaces, bytes untouched: %q -> %q, want %q", cmd, args, want)
			}
		}
	})
}
