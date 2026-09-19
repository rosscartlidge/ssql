package commands

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// sqlDialect selects the engine `generate sql` renders for. The pipeline
// semantics never change — only the spelling of the constructs where the
// engines differ (source clause, `SELECT *` modifiers, a dozen aggregate
// names, regex matching) and the list of stages that have no translation
// in that engine and are refused loudly (DFC132 §4).
type sqlDialect string

const (
	dialectDuckDB     sqlDialect = "duckdb"
	dialectPostgres   sqlDialect = "postgres"
	dialectDataFusion sqlDialect = "datafusion"
)

var sqlDialects = []string{string(dialectDuckDB), string(dialectPostgres), string(dialectDataFusion)}

// sqlDialectCur is the dialect the assembler is rendering for. `generate
// sql` is a one-shot translation, so this is set once per assembleSQL call
// (assembleSQLDialect) and read by the renderers; the DuckDB default keeps
// every existing caller and test unchanged.
var sqlDialectCur = dialectDuckDB

// pgLoad is one source file the Postgres dialect turned into a table
// reference: the generated SQL reads the table, and the prologue tells the
// reader how to create and load it (renderPGPrologue).
type pgLoad struct {
	table   string   // quoted identifier used in the query
	file    string   // the CSV/TSV path from the pipeline
	delim   rune     // ',' or '\t'
	columns []string // header names (nil when the file was unreadable)
	types   []string // SQL column types parallel to columns
}

var pgLoads []pgLoad

// sqlTimeColumns names the columns a `cast -type F time` stage turned into
// TIMESTAMPs during this assembly. The translator does not track column
// types in general; this is the one fact a later bucket() needs to choose
// the engine's time bucketing over the numeric-epoch arithmetic.
var sqlTimeColumns = map[string]bool{}

func parseSQLDialect(s string) (sqlDialect, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "duckdb":
		return dialectDuckDB, nil
	case "postgres", "postgresql", "pg":
		return dialectPostgres, nil
	case "datafusion", "df":
		return dialectDataFusion, nil
	}
	return "", fmt.Errorf("generate sql: unknown -dialect %q (choose %s)", s, strings.Join(sqlDialects, ", "))
}

// assembleSQLDialect is assembleSQL for a given dialect.
func assembleSQLDialect(input io.Reader, d sqlDialect) (string, error) {
	prev, prevLoads := sqlDialectCur, pgLoads
	sqlDialectCur, pgLoads = d, nil
	defer func() { sqlDialectCur, pgLoads = prev, prevLoads }()
	return assembleSQL(input)
}

// dialectRefuse is the loud refusal for a stage with no translation in the
// current dialect. The phrase "has no <dialect> translation" is what the
// dialect oracle lanes in the equivalence gate recognise as by-design.
func dialectRefuse(what, why string) error {
	if why != "" {
		why = " (" + why + ")"
	}
	return fmt.Errorf("%s has no %s translation%s — use -dialect duckdb or generate go", what, sqlDialectCur, why)
}

// sqlSubquery renders a derived table for the FROM clause. Postgres
// requires an alias on every subquery in FROM; DuckDB and DataFusion do
// not. Nested scopes may reuse the alias, so one fixed name is enough.
func sqlSubquery(body string) string {
	if sqlDialectCur == dialectPostgres {
		return body + " AS __q"
	}
	return body
}

// dialectSource renders the FROM reference for one data file: a quoted
// path for DuckDB and DataFusion (both read a file by name), a table for
// Postgres, which cannot read files in a query — the table is named after
// the file and the prologue carries the CREATE TABLE + \copy to load it.
func dialectSource(file string) (string, error) {
	lower := strings.ToLower(file)
	if sqlDialectCur == dialectDataFusion && (strings.HasSuffix(lower, ".tsv") || strings.HasSuffix(lower, ".jsonl")) {
		return "", dialectRefuse("from "+file, "DataFusion's file table reads .csv, .json and .parquet by extension")
	}
	if sqlDialectCur != dialectPostgres {
		return quoteFile(file), nil
	}
	delim := ','
	switch {
	case strings.HasSuffix(lower, ".csv"):
	case strings.HasSuffix(lower, ".tsv"):
		delim = '\t'
	default:
		return "", dialectRefuse(fmt.Sprintf("from %s", file), "\\copy loads CSV/TSV only; convert with `ssql to csv` first")
	}
	table := pgTableName(file)
	for _, l := range pgLoads {
		if l.table == table && l.file != file {
			return "", fmt.Errorf("from %s: its table name %s is already used by %s — rename one of the files", file, table, l.file)
		}
		if l.file == file {
			return table, nil
		}
	}
	cols, types := pgInferColumns(file, delim)
	pgLoads = append(pgLoads, pgLoad{table: table, file: file, delim: delim, columns: cols, types: types})
	return table, nil
}

var pgIdentClean = regexp.MustCompile(`[^a-z0-9_]+`)

// pgTableName derives a Postgres table identifier from a file path:
// the base name without extension, lowercased, runs of other characters
// collapsed to "_", always quoted so it can never collide with a keyword.
func pgTableName(file string) string {
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	name := pgIdentClean.ReplaceAllString(strings.ToLower(base), "_")
	name = strings.Trim(name, "_")
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		name = "t_" + name
	}
	return `"` + name + `"`
}

// pgInferColumns reads the header and up to pgSampleRows rows of a
// delimited file and picks a Postgres type per column the way ssql's own
// reader types cells: BIGINT if every non-empty cell parses as an integer,
// DOUBLE PRECISION if every one is numeric, BOOLEAN for true/false, DATE
// for ISO YYYY-MM-DD (so a RANGE frame with an INTERVAL bound can use the
// column; it renders and compares exactly as the string did), else TEXT.
// Unreadable file → nil, and the prologue says so instead of guessing.
const pgSampleRows = 1000

func pgInferColumns(path string, delim rune) ([]string, []string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = delim
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil || len(header) == 0 {
		return nil, nil
	}
	const (
		kInt = iota
		kFloat
		kBool
		kDate
		kText
		kEmpty
	)
	kinds := make([]int, len(header))
	for i := range kinds {
		kinds[i] = kEmpty
	}
	for n := 0; n < pgSampleRows; n++ {
		row, err := r.Read()
		if err != nil {
			break
		}
		for i := 0; i < len(row) && i < len(header); i++ {
			cell := strings.TrimSpace(row[i])
			if cell == "" {
				continue
			}
			var k int
			switch {
			case isIntToken(cell):
				k = kInt
			case isFloatToken(cell):
				k = kFloat
			case strings.EqualFold(cell, "true"), strings.EqualFold(cell, "false"):
				k = kBool
			case isoDateRe.MatchString(cell):
				k = kDate
			default:
				k = kText
			}
			switch {
			case kinds[i] == kEmpty:
				kinds[i] = k
			case kinds[i] == k:
			case (kinds[i] == kInt || kinds[i] == kFloat) && (k == kInt || k == kFloat):
				kinds[i] = kFloat
			default:
				kinds[i] = kText
			}
		}
	}
	types := make([]string, len(header))
	for i, k := range kinds {
		switch k {
		case kInt:
			types[i] = "BIGINT"
		case kFloat:
			types[i] = "DOUBLE PRECISION"
		case kBool:
			types[i] = "BOOLEAN"
		case kDate:
			types[i] = "DATE"
		default:
			types[i] = "TEXT"
		}
	}
	return header, types
}

var isoDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func isIntToken(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func isFloatToken(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil && !strings.EqualFold(s, "nan") && !strings.Contains(strings.ToLower(s), "inf")
}

// renderPGPrologue is the Postgres header: for every source file, the
// CREATE TABLE and the psql \copy that load it, as SQL comments so the
// statement itself stays portable (a driver would reject the
// meta-command). `-run -dialect postgres` executes the same block for
// real (pgLoadScript).
func renderPGPrologue(sb *strings.Builder) {
	if len(pgLoads) == 0 {
		return
	}
	sb.WriteString("-- Postgres cannot read a file inside a query: load each source once with psql,\n")
	sb.WriteString("-- then run the statement below against the same database.\n")
	for _, ln := range strings.Split(strings.TrimRight(pgLoadScript(false), "\n"), "\n") {
		sb.WriteString("--   " + ln + "\n")
	}
	sb.WriteString("\n")
}

// pgLoadScript renders the psql script that creates and loads the source
// tables. temp=true makes them session-scoped TEMP tables (the oracle lane
// and -run use this so nothing is left behind and parallel runs cannot
// collide); false renders CREATE TABLE IF NOT EXISTS for a one-off load.
func pgLoadScript(temp bool) string {
	var sb strings.Builder
	for _, l := range pgLoads {
		if l.columns == nil {
			fmt.Fprintf(&sb, "-- %s was not readable at generation time: CREATE TABLE %s with its header's column names, then\n", l.file, l.table)
		} else {
			cols := make([]string, len(l.columns))
			for i, c := range l.columns {
				cols[i] = `"` + strings.ReplaceAll(c, `"`, `""`) + `" ` + l.types[i]
			}
			if temp {
				fmt.Fprintf(&sb, "CREATE TEMP TABLE %s (%s);\n", l.table, strings.Join(cols, ", "))
			} else {
				fmt.Fprintf(&sb, "CREATE TABLE IF NOT EXISTS %s (%s);\n", l.table, strings.Join(cols, ", "))
			}
		}
		delim := ""
		if l.delim == '\t' {
			delim = " DELIMITER E'\\t'"
		}
		fmt.Fprintf(&sb, "\\copy %s FROM '%s' CSV HEADER%s\n", l.table, escapeSQL(l.file), delim)
	}
	return sb.String()
}

// starProjection renders "every column, with these changes" for a stage
// that modifies the `SELECT *` (exclude, rename, cast, update, fill).
// DuckDB has the modifiers (`* EXCLUDE`, `* RENAME`, `* REPLACE`);
// DataFusion has EXCLUDE and REPLACE but not RENAME; Postgres has none, so
// there the column list must be spelled out — possible only while the
// translator knows the source's columns, otherwise a loud refusal.
// replace pairs a column with its new expression; rename pairs old with
// new; exclude drops. cols is the stage's input column list (nil = unknown).
type sqlPair struct{ col, val string }

func starProjection(cols []string, exclude []string, replace, rename []sqlPair, stage string) (string, error) {
	quotedList := func(items []string) string {
		out := make([]string, len(items))
		for i, c := range items {
			out[i] = quoteIdent(c)
		}
		return strings.Join(out, ", ")
	}
	native := sqlDialectCur == dialectDuckDB || (sqlDialectCur == dialectDataFusion && len(rename) == 0)
	if native {
		switch {
		case len(exclude) > 0:
			return "* EXCLUDE (" + quotedList(exclude) + ")", nil
		case len(rename) > 0:
			var parts []string
			for _, p := range rename {
				parts = append(parts, quoteIdent(p.col)+" AS "+quoteIdent(p.val))
			}
			return "* RENAME (" + strings.Join(parts, ", ") + ")", nil
		default:
			var parts []string
			for _, p := range replace {
				parts = append(parts, p.val+" AS "+quoteIdent(p.col))
			}
			return "* REPLACE (" + strings.Join(parts, ", ") + ")", nil
		}
	}
	lookup := func(pairs []sqlPair, c string) string {
		for _, p := range pairs {
			if p.col == c {
				return p.val
			}
		}
		return ""
	}
	if cols == nil {
		return "", dialectRefuse(stage, "it rewrites `SELECT *` and the translator does not know this source's columns — name the fields with `include`, or use a CSV/TSV source")
	}
	drop := map[string]bool{}
	for _, c := range exclude {
		drop[c] = true
	}
	var parts []string
	for _, c := range cols {
		switch {
		case drop[c]:
		case lookup(replace, c) != "":
			parts = append(parts, lookup(replace, c)+" AS "+quoteIdent(c))
		case lookup(rename, c) != "":
			parts = append(parts, quoteIdent(c)+" AS "+quoteIdent(lookup(rename, c)))
		default:
			parts = append(parts, quoteIdent(c))
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("%s: no columns left to select", stage)
	}
	return strings.Join(parts, ", "), nil
}

// --- dialect-aware spellings ---

// sqlRegexMatch renders "field matches regex pattern" (pattern already a
// SQL string literal or expression).
func sqlRegexMatch(field, pattern string) string {
	switch sqlDialectCur {
	case dialectPostgres:
		return fmt.Sprintf("(%s ~ %s)", field, pattern)
	case dialectDataFusion:
		return fmt.Sprintf("regexp_like(%s, %s)", field, pattern)
	}
	return fmt.Sprintf("regexp_matches(%s, %s)", field, pattern)
}

// sqlFloatType is the double-precision type name.
func sqlFloatType() string {
	if sqlDialectCur == dialectPostgres {
		return "DOUBLE PRECISION"
	}
	return "DOUBLE"
}

// sqlStringType is the text type name.
func sqlStringType() string {
	if sqlDialectCur == dialectPostgres {
		return "TEXT"
	}
	return "VARCHAR"
}

// Aggregate spellings that differ between engines (DFC132 §3 table). Each
// takes the quoted field and renders the whole aggregate expression.

func sqlAggCollect(qf string) string {
	if sqlDialectCur == dialectDuckDB {
		return "LIST(" + qf + ")"
	}
	return "array_agg(" + qf + ")"
}

func sqlAggFirst(qf string) string {
	switch sqlDialectCur {
	case dialectPostgres:
		return "(array_agg(" + qf + "))[1]"
	case dialectDataFusion:
		return "first_value(" + qf + ")"
	}
	return "first(" + qf + ")"
}

func sqlAggLast(qf string) string {
	switch sqlDialectCur {
	case dialectPostgres:
		return "(array_agg(" + qf + "))[cardinality(array_agg(" + qf + "))]"
	case dialectDataFusion:
		return "last_value(" + qf + ")"
	}
	return "last(" + qf + ")"
}

func sqlAggAny(qf string) string {
	if sqlDialectCur == dialectDataFusion {
		return "first_value(" + qf + ")"
	}
	return "any_value(" + qf + ")"
}

// sqlAggMode: DataFusion has no mode aggregate; the registry's check
// refuses it there (sqlAggUnsupported) before rendering.
func sqlAggMode(qf string) string {
	if sqlDialectCur == dialectPostgres {
		return "mode() WITHIN GROUP (ORDER BY " + qf + ")"
	}
	return "mode(" + qf + ")"
}

// sqlAggMedian: DuckDB's and DataFusion's median are exact and return a
// double for integer input (DataFusion 54 averages the two middle values);
// Postgres has only the ordered-set form.
func sqlAggMedian(qf string) string {
	if sqlDialectCur == dialectPostgres {
		return "percentile_cont(0.5) WITHIN GROUP (ORDER BY " + qf + ")"
	}
	return "median(" + qf + ")"
}

func sqlAggPercentile(qf, p string) string {
	p = strings.TrimSpace(p)
	if sqlDialectCur == dialectDuckDB {
		return fmt.Sprintf("quantile_cont(%s, %s)", qf, p)
	}
	return fmt.Sprintf("percentile_cont(%s) WITHIN GROUP (ORDER BY %s)", p, qf)
}

func sqlAggStringAgg(qf, sep string) string {
	if sqlDialectCur == dialectPostgres {
		// string_agg(bigint, text) does not exist in Postgres.
		qf = "CAST(" + qf + " AS TEXT)"
	}
	return fmt.Sprintf("string_agg(%s, %s)", qf, sqlStringLiteral(sep))
}

// sqlAggArgExtreme renders arg_max (desc=true) / arg_min: the value of qf
// on the row where qby is largest/smallest.
func sqlAggArgExtreme(qf, qby string, desc bool) string {
	dir := "ASC"
	name := "arg_min"
	if desc {
		dir, name = "DESC", "arg_max"
	}
	switch sqlDialectCur {
	case dialectPostgres:
		return fmt.Sprintf("(array_agg(%s ORDER BY %s %s))[1]", qf, qby, dir)
	case dialectDataFusion:
		return fmt.Sprintf("first_value(%s ORDER BY %s %s)", qf, qby, dir)
	}
	return fmt.Sprintf("%s(%s, %s)", name, qf, qby)
}

// aggDialectRefusal reports why an aggregate flag cannot be rendered in
// the current dialect, or nil when it can. Consulted by the group-by and
// window translators before rendering. Over a window frame Postgres lacks
// ordered-set aggregates and DISTINCT; DataFusion 54 lacks an aggregate
// ORDER BY inside a window function, an exact percentile over a frame,
// and a sliding (bounded-start) accumulator for string_agg.
func aggDialectRefusal(fn string, windowed, bounded bool) error {
	switch sqlDialectCur {
	case dialectDataFusion:
		if fn == "mode" {
			return dialectRefuse("-mode", "DataFusion has no mode aggregate")
		}
		if windowed {
			switch fn {
			case "arg-max", "arg-min":
				return dialectRefuse("window -"+fn, "DataFusion has no aggregate ORDER BY inside a window function")
			case "percentile":
				return dialectRefuse("window -percentile", "DataFusion has no exact percentile over a window frame")
			case "string-agg":
				if bounded {
					return dialectRefuse("window -string-agg with a bounded frame", "DataFusion cannot slide string_agg")
				}
			}
		}
	case dialectPostgres:
		if windowed {
			switch fn {
			case "median", "percentile", "mode":
				return dialectRefuse("window -"+fn, "Postgres cannot apply an ordered-set aggregate over a window frame")
			case "count-distinct":
				return dialectRefuse("window -count-distinct", "Postgres does not allow DISTINCT in a window function")
			case "first", "last", "any", "arg-max", "arg-min":
				return dialectRefuse("window -"+fn, "Postgres has no positional aggregate over a frame")
			}
		}
	}
	return nil
}

// sqlRunCommand is the engine command line `-run` executes for the
// dialect: DuckDB's and DataFusion's CLIs take the statement with -c;
// psql takes the load script and the statement on stdin (the \copy
// meta-command only works there) and honours PGHOST/PGDATABASE/… .
func sqlRunCommand(sql string) (name string, args []string, stdin string) {
	switch sqlDialectCur {
	case dialectPostgres:
		return "psql", []string{"-v", "ON_ERROR_STOP=1", "-X"}, pgLoadScript(true) + sql
	case dialectDataFusion:
		return "datafusion-cli", []string{"-c", sql}, ""
	}
	return "duckdb", []string{"-c", sql}, ""
}

// sqlSampleRows renders "N random rows of src": DuckDB's reservoir USING
// SAMPLE on the source; elsewhere a random-order LIMIT over it. Both are
// unseeded statistical samples of N rows (the equivalence lanes assert
// cardinality, not membership).
func sqlSampleRows(src, n string) string {
	if sqlDialectCur == dialectDuckDB {
		return src + " USING SAMPLE " + n + " ROWS (reservoir)"
	}
	return sqlSubquery("(SELECT * FROM " + src + " ORDER BY random() LIMIT " + n + ")")
}

// sqlSamplePercent renders a Bernoulli percent sample of src.
func sqlSamplePercent(src, percent string) string {
	if sqlDialectCur == dialectDuckDB {
		return src + " USING SAMPLE " + percent + "% (bernoulli)"
	}
	return sqlSubquery("(SELECT * FROM " + src + " WHERE random() < " + percent + " / 100.0)")
}

// String predicates for the expression translator. DuckDB and DataFusion
// share contains/starts_with/ends_with; Postgres has starts_with only.
func sqlContains(s, sub string) string {
	if sqlDialectCur == dialectPostgres {
		return "(strpos(" + s + ", " + sub + ") > 0)"
	}
	return "contains(" + s + ", " + sub + ")"
}

func sqlStartsWith(s, prefix string) string {
	return "starts_with(" + s + ", " + prefix + ")"
}

func sqlEndsWith(s, suffix string) string {
	if sqlDialectCur == dialectPostgres {
		return "(right(" + s + ", length(" + suffix + ")) = " + suffix + ")"
	}
	return "ends_with(" + s + ", " + suffix + ")"
}

// sqlUnpivotUnion is the portable UNPIVOT: one SELECT per folded column,
// UNION ALL, every output column aliased explicitly (DataFusion refuses a
// union that mixes qualified and unqualified names), NULL values dropped
// as UnpivotRecords drops absent fields and DuckDB's UNPIVOT drops NULLs.
// The engine unifies the value column's type across branches as DuckDB's
// UNPIVOT does.
func sqlUnpivotUnion(src string, ids, values []string, col, val string) string {
	var branches []string
	for _, v := range values {
		var proj []string
		for _, id := range ids {
			proj = append(proj, quoteIdent(id)+" AS "+quoteIdent(id))
		}
		proj = append(proj, sqlStringLiteral(v)+" AS "+quoteIdent(col), quoteIdent(v)+" AS "+quoteIdent(val))
		branches = append(branches, "    SELECT "+strings.Join(proj, ", ")+" FROM "+src+" WHERE "+quoteIdent(v)+" IS NOT NULL")
	}
	return "(\n" + strings.Join(branches, "\n    UNION ALL\n") + "\n)"
}

// sqlDivide renders expr-lang's `/`, which is always a float division
// (7 / 2 = 3.5). DuckDB's `/` is too; Postgres and DataFusion truncate
// integer operands, so the left operand is cast up there.
func sqlDivide(left, right string) string {
	if sqlDialectCur == dialectDuckDB {
		return "(" + left + " / " + right + ")"
	}
	return "(CAST(" + left + " AS " + sqlFloatType() + ") / " + right + ")"
}
