package commands

import (
	"slices"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
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

// sqlColumnKinds is what the assembler knows about column TYPES: the kind
// of each source column ("int", "float", "bool", "string", "time"),
// sampled from a CSV/TSV source at generation time and updated by cast. A
// column it does not know is simply absent, and literals fall back to the
// look of the token. It exists so a literal is typed by the column it
// meets, not by its spelling.
var sqlColumnKinds = map[string]string{}

// sqlZone is the pipeline's time zone for this assembly (DFC147 §5.7):
// the from stage's materialised `-tz`, else the generating process's. The
// calendar functions name it explicitly in the SQL they emit; the session
// zone is pinned to UTC (renderZonePrologue) so the engine reads zoneless
// text as ssql does. sqlNeedsZone records that a time type or function
// was emitted, so the pin is written only when it matters.
var sqlZone string
var sqlNeedsZone bool

// sqlTextTimeColumns names the columns a JSON source's `_schema` header
// types as time but the engine reads as text (its JSON reader infers
// VARCHAR for RFC 3339 strings in mixed offsets): a reference to one as
// a time casts it. A cast or -type stage replaces the entry.
var sqlTextTimeColumns = map[string]bool{}

// sqlTimeColumnRef is the SQL for a time column: the identifier, cast to
// the instant type when the engine holds it as text.
func sqlTimeColumnRef(name string) string {
	if sqlTextTimeColumns[name] {
		return "CAST(" + quoteIdent(name) + " AS " + sqlTimestampType() + ")"
	}
	return quoteIdent(name)
}

// sqlTimestampType is the engine type of an ssql `time`: an instant
// (TIMESTAMPTZ) on DuckDB and Postgres — a naive TIMESTAMP drops the
// offset of a `…+11:00` cell (probed 2026-10-10) — and DataFusion's
// TIMESTAMP, which it reads as UTC.
func sqlTimestampType() string {
	sqlNeedsZone = true
	if sqlDialectCur == dialectDataFusion {
		return "TIMESTAMP"
	}
	return "TIMESTAMPTZ"
}

// sqlEpochOrigin is the Unix epoch as a literal of sqlTimestampType, the
// origin bucket() and resample pin their grids to.
func sqlEpochOrigin() string {
	if sqlDialectCur == dialectDataFusion {
		return "TIMESTAMP '1970-01-01 00:00:00'"
	}
	return "TIMESTAMPTZ '1970-01-01 00:00:00+00'"
}

// renderZonePrologue pins the session zone to UTC when the statement
// involves a time, so zoneless text parses as ssql reads it (UTC) and
// every calendar reading is the explicit `AT TIME ZONE` the functions
// emit, never the machine's zone. DataFusion needs no pin (UTC-naive).
func renderZonePrologue(sb *strings.Builder) {
	if !sqlNeedsZone {
		return
	}
	switch sqlDialectCur {
	case dialectDuckDB:
		sb.WriteString("SET TimeZone = 'UTC';\n\n")
	case dialectPostgres:
		sb.WriteString("SET timezone TO 'UTC';\n\n")
	}
}

// seedColumnKinds samples a delimited source (the Postgres prologue's
// sampler — the same int → float → bool → text reading the CSV reader
// does) into sqlColumnKinds.
func seedColumnKinds(path string, delim rune) {
	cols, types := pgInferColumns(path, delim)
	for i, c := range cols {
		switch types[i] {
		case "BIGINT":
			sqlColumnKinds[c] = "int"
		case pgInt128Type:
			sqlColumnKinds[c] = "int128" // a literal past int64 in the sample (DFC155 C)
		case "DOUBLE PRECISION":
			sqlColumnKinds[c] = "float"
		case "BOOLEAN":
			sqlColumnKinds[c] = "bool"
		default: // TEXT, and DATE — a date is text to ssql until it is cast
			sqlColumnKinds[c] = "string"
		}
	}
}

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
// not. Nested scopes may reuse the alias, so one fixed name is enough, except
// across the sides of a join: see sqlJoinSideSubquery.
func sqlSubquery(body string) string {
	if sqlDialectCur == dialectPostgres {
		return body + " AS __q"
	}
	return body
}

// sqlJoinSideSubquery is sqlSubquery for a join's right side, which shares a
// scope with the left side and with the query's other joins, so Postgres needs
// it to have a name of its own: n is the join's position. With __q on both,
// `join <(… | where …) -using k` after a subquery was "table name "__q"
// specified more than once" (found 2026-11-07 by the Postgres lane, when the
// join pushdown rule began writing that shape).
func sqlJoinSideSubquery(body string, n int) string {
	if sqlDialectCur == dialectPostgres {
		return fmt.Sprintf("%s AS __j%d", body, n)
	}
	return body
}

// sqlOrderState follows what a statement may say about row order. An engine
// keeps none unless it promises to: over 3 M rows `-first` answered 954250 where
// the group's first row is 0, on DuckDB and DataFusion, and Postgres plans
// array_agg in parallel by default (2026-11-07; the fixtures had been too small
// to show it on any engine). So a stage that reads its input's order is translated
// only where the answer is guaranteed, and refused with the way out otherwise
// (Ross: fail loudly rather than slow it down). A guarantee is an ORDER BY from an
// earlier sort or a range source's column, or on DuckDB what its Order Preservation
// page documents: single table, WHERE, SELECT, LIMIT, OFFSET, UNION ALL and
// subqueries of those, and row_number() OVER () to make the order a column.
type sqlOrderState struct {
	lost     string // the stage that ended the source's order, "" while it holds
	aggOrder string // " ORDER BY …" for the aggregate being rendered
	rowNum   bool   // the group-by needs the source's row number as a column
}

var sqlOrder sqlOrderState

// sqlRowNumColumn is the column row_number() OVER () is carried in.
const sqlRowNumColumn = "__ssql_row"

// sqlOrderedAggs are the aggregates whose answer is their input's order. The arg
// aggregates are defined except on a tie, and -any promises nothing.
var sqlOrderedAggs = map[string]bool{"first": true, "last": true, "collect": true, "string-agg": true}

// sqlOrderTransparent are the stages that keep the rows in the order they came.
var sqlOrderTransparent = map[string]bool{
	"from": true, "where": true, "include": true, "exclude": true, "rename": true,
	"update": true, "cast": true, "limit": true, "offset": true, "to": true,
}

func (o *sqlOrderState) refuse(what string) error {
	engine := string(sqlDialectCur)
	why := "which " + engine + " does not keep"
	if o.lost != "" {
		why += " across " + o.lost
	}
	way := "add a sort to say which order, or use generate go, which keeps file order"
	if o.lost == "" && sqlDialectCur != dialectDuckDB {
		way = "add a sort to say which order, use -dialect duckdb, or use generate go, which keeps file order"
	}
	return fmt.Errorf("%s with no sort has no %s translation: it reads its input's order, %s; %s", what, engine, why, way)
}

// refuseDropped is the sort that is there but cannot be said again here.
func (o *sqlOrderState) refuseDropped(what string) error {
	return fmt.Errorf("%s has no %s translation: it reads the order of a sort whose key was dropped before it; keep the key until here, or use generate go", what, sqlDialectCur)
}

// orderKeys is the ORDER BY that already defines the rows here. wrapped says it
// sits in a subquery, so it must be said again, which needs its columns.
func (o *sqlOrderState) orderKeys(q *sqlQuery) (keys []string, wrapped bool) {
	if len(q.orderBy) > 0 {
		return q.orderBy, false
	}
	if len(q.wrappedOrder) > 0 {
		return q.wrappedOrder, true
	}
	if q.rangeColumn != "" && (q.columns == nil || slices.Contains(q.columns, q.rangeColumn)) {
		return []string{quoteIdent(q.rangeColumn)}, true
	}
	return nil, false
}

// keysPresent reports that every quoted column of the terms still exists: a
// projection after the sort can drop the key, and a sort there has no meaning.
func keysPresent(q *sqlQuery, keys []string) bool {
	if q.columns == nil {
		return true
	}
	for _, k := range keys {
		name, ok := strings.CutPrefix(k, `"`)
		if ok {
			name, _, _ = strings.Cut(name, `"`)
		} else {
			name, _, _ = strings.Cut(k, " ") // a dialect that quotes only when it must
		}
		if !strings.Contains(name, "(") && !slices.Contains(q.columns, name) {
			return false
		}
	}
	return true
}

// readsRows is limit or offset: which rows they keep is the input's order.
func (o *sqlOrderState) readsRows(what string, q *sqlQuery) error {
	if keys, wrapped := o.orderKeys(q); keys != nil {
		if !wrapped {
			return nil
		}
		if keysPresent(q, keys) {
			q.orderBy = keys
			return nil
		}
		return o.refuseDropped(what)
	}
	if sqlDialectCur == dialectDuckDB && o.lost == "" {
		return nil
	}
	return o.refuse(what)
}

// readsAgg decides the order an aggregate reads, or refuses it.
func (o *sqlOrderState) readsAgg(fn string, q *sqlQuery) error {
	o.aggOrder = ""
	if !sqlOrderedAggs[fn] {
		return nil
	}
	if keys, _ := o.orderKeys(q); keys != nil {
		if !keysPresent(q, keys) {
			return o.refuseDropped("group-by -" + fn)
		}
		o.aggOrder = " ORDER BY " + strings.Join(keys, ", ")
		return nil
	}
	if sqlDialectCur == dialectDuckDB && o.lost == "" {
		o.rowNum = true
		o.aggOrder = " ORDER BY " + quoteIdent(sqlRowNumColumn)
		return nil
	}
	return o.refuse("group-by -" + fn)
}

// numberRows makes the source's order a column, under the group-by that reads it.
func (o *sqlOrderState) numberRows(q *sqlQuery) {
	if !o.rowNum {
		return
	}
	o.rowNum = false
	if q.hasClauses() {
		wrapAsSubquery(q)
	}
	q.fromClause = "(SELECT *, row_number() OVER () AS " + quoteIdent(sqlRowNumColumn) + " FROM " + q.fromClause + ")"
}

// afterStage records what a stage did to the order the next one sees: a sort's
// ORDER BY speaks for itself, and any other stage that is not known to keep the
// order ends it, along with a sort below it.
func (o *sqlOrderState) afterStage(name string, q *sqlQuery) {
	switch {
	case sqlOrderTransparent[name]:
	case name == "sort" || name == "top":
		o.lost = ""
	default:
		o.lost = "`" + name + "`"
		q.wrappedOrder = nil
	}
}

// sqlForRust is set while `generate rust` assembles its tree: the
// DataFrame API's readers take what DataFusion's SQL file table cannot —
// a delimiter and a file extension (.tsv, .jsonl) and a declared schema
// (-type, a schema sidecar, an integer column past int64) — so those
// refusals of the datafusion dialect are lifted there, and the source
// carries what the reader needs (sqlSource.schema). The SQL text the
// dialect renders is unchanged (2026-10-30, DFC157 §6 follow-up 1).
var sqlForRust bool

// dialectSource renders the FROM reference for one data file: a quoted
// path for DuckDB and DataFusion (both read a file by name), a table for
// Postgres, which cannot read files in a query — the table is named after
// the file and the prologue carries the CREATE TABLE + \copy to load it.
func dialectSource(file string) (string, error) {
	return dialectSourceTyped(file, nil)
}

// dialectSourceTyped is dialectSource with the source's declared column
// types (-type and a schema sidecar, DFC146): Postgres's table takes
// them; DataFusion's file table infers and cannot, so they are refused.
func dialectSourceTyped(file string, overrides map[string]string) (string, error) {
	lower := strings.ToLower(file)
	if sqlDialectCur == dialectDataFusion && len(overrides) > 0 && !sqlForRust {
		return "", dialectRefuse("from -type / schema sidecar", "DataFusion's file table infers its column types")
	}
	if sqlDialectCur == dialectDataFusion && (strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")) {
		return "", dialectRefuse("from "+file, "DataFusion's file table reads local paths; an http source needs a registered object store, which the Python bindings' url table does not resolve (55.0)")
	}
	if sqlDialectCur == dialectDataFusion && !sqlForRust && (strings.HasSuffix(lower, ".tsv") || strings.HasSuffix(lower, ".jsonl")) {
		return "", dialectRefuse("from "+file, "DataFusion's file table reads .csv, .json and .parquet by extension")
	}
	if sqlDialectCur == dialectDataFusion && strings.HasSuffix(lower, ".json") && jsonFileIsArray(file) {
		return "", dialectRefuse("from "+file, "DataFusion reads .json as one object per line; this file is one JSON array — write it with `ssql to jsonl`")
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
	for i, c := range cols {
		if t, ok := overrides[c]; ok {
			types[i] = pgColumnType(t, types[i])
		}
	}
	pgLoads = append(pgLoads, pgLoad{table: table, file: file, delim: delim, columns: cols, types: types})
	return table, nil
}

// jsonFileIsArray peeks at a JSON file's first non-blank byte. Unreadable
// at generation time → false: the engine reports it if it matters.
func jsonFileIsArray(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	trimmed := strings.TrimLeft(string(buf[:n]), " \t\r\n\ufeff")
	return strings.HasPrefix(trimmed, "[")
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
		kWide // an integer past int64 that fits int128 (DFC155 C)
		kFloat
		kBool
		kDate
		kText
		kEmpty
	)
	numeric := func(k int) bool { return k == kInt || k == kWide || k == kFloat }
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
			case isWideIntToken(cell):
				k = kWide
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
			case (kinds[i] == kInt || kinds[i] == kWide) && (k == kInt || k == kWide):
				kinds[i] = kWide // int beside int128 is int128
			case numeric(kinds[i]) && numeric(k):
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
		case kWide:
			types[i] = pgInt128Type
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
	if ssql.ZeroPaddedNumber(s) {
		return false
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

// isWideIntToken: an integer literal past int64 that fits int128.
func isWideIntToken(s string) bool {
	if ssql.ZeroPaddedNumber(s) {
		return false
	}
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return false
	}
	_, err := ssql.ParseInt128(s)
	return err == nil
}

func isFloatToken(s string) bool {
	if ssql.ZeroPaddedNumber(s) {
		return false
	}
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
	if sqlNeedsZone {
		sb.WriteString("SET timezone TO 'UTC';\n") // zoneless cells load as UTC, as ssql reads them
	}
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

// pgInt128Type is the Postgres column type that holds an int128: NUMERIC
// has no 128-bit form, so 39 digits of it (DFC155 C).
const pgInt128Type = "NUMERIC(39,0)"

// sqlInt128Type is the 128-bit integer type per dialect: DuckDB's native
// HUGEINT, Postgres NUMERIC(39,0), DataFusion DECIMAL(38,0) (38 digits,
// one short of int128's 39; documented, the lane is not gated here).
func sqlInt128Type() string {
	switch sqlDialectCur {
	case dialectPostgres:
		return pgInt128Type
	case dialectDataFusion:
		return "DECIMAL(38,0)"
	}
	return "HUGEINT"
}

// sqlInetType is the IP address type (DFC162): DuckDB's INET (its inet
// extension), Postgres's inet. DataFusion has none: a query with an inet
// column is refused whole (sqlRefuseInet), so its name is never run there.
func sqlInetType() string {
	if sqlDialectCur == dialectPostgres {
		return "inet"
	}
	return "INET"
}

// sqlInetText renders an inet value as the engines' text: DuckDB's
// VARCHAR cast prints a full-length prefix bare; Postgres's ::text adds
// /32 or /128, abbrev() does not (measured 2026-11-04).
func sqlInetText(x string) string {
	if sqlDialectCur == dialectPostgres {
		return "abbrev(" + x + ")"
	}
	return "CAST(" + x + " AS VARCHAR)"
}

// sqlTimeText renders a time as ssql.TimeText does: RFC 3339 on the pipeline
// zone's clock, a fraction only when there is one and without trailing
// zeros, Z at offset zero. The session zone stays UTC (renderZonePrologue),
// so the wall clock and its offset are spelled out from the zone's name.
func sqlTimeText(x string) string {
	zone := sqlZone
	if zone == "" {
		zone = "UTC"
	}
	z := sqlStringLiteral(zone)
	sqlNeedsZone = true
	switch sqlDialectCur {
	case dialectDataFusion:
		// A TIMESTAMP is UTC-naive: UTC first, or the second cast reads the
		// wall clock as the zone's. %.f prints .500; the regexps need its dot.
		ts := fmt.Sprintf(`arrow_cast(arrow_cast(%s, 'Timestamp(Nanosecond, Some("UTC"))'), 'Timestamp(Nanosecond, Some("%s"))')`, x, escapeSQL(zone))
		s := "to_char(" + ts + ", '%Y-%m-%dT%H:%M:%S%.f%:z')"
		s = `regexp_replace(` + s + `, '(\.[0-9]*?)0+([+-][0-9:]+)$', '$1$2')`
		return `regexp_replace(` + s + `, '[+-]00:00$', 'Z')`
	case dialectPostgres:
		local := "(" + x + " AT TIME ZONE " + z + ")"
		text := `to_char(` + local + `, 'YYYY-MM-DD"T"HH24:MI:SS') || rtrim(rtrim(to_char(` + local + `, '.US'), '0'), '.')`
		if zone == "UTC" {
			return "(" + text + " || 'Z')"
		}
		return "(" + text + " || " + sqlOffsetText("(extract(epoch FROM "+local+" - ("+x+" AT TIME ZONE 'UTC')) / 60)::int") + ")"
	}
	local := "(" + x + " AT TIME ZONE " + z + ")"
	text := `strftime(` + local + `, '%Y-%m-%dT%H:%M:%S') || rtrim(rtrim(strftime(` + local + `, '.%f'), '0'), '.')`
	if zone == "UTC" {
		return "(" + text + " || 'Z')"
	}
	return "(" + text + " || " + sqlOffsetText("date_diff('minute', "+x+" AT TIME ZONE 'UTC', "+local+")") + ")"
}

// sqlOffsetText is RFC 3339's offset from minutes east of UTC.
func sqlOffsetText(m string) string {
	part := func(e string) string { return "lpad(CAST(" + e + " AS VARCHAR), 2, '0')" }
	hours := "abs(" + m + ") // 60"
	if sqlDialectCur == dialectPostgres {
		part = func(e string) string { return "lpad(CAST(" + e + " AS TEXT), 2, '0')" }
		hours = "abs(" + m + ") / 60" // integer division here; DuckDB's / is a float's
	}
	return "CASE WHEN " + m + " = 0 THEN 'Z' ELSE CASE WHEN " + m + " < 0 THEN '-' ELSE '+' END || " +
		part(hours) + " || ':' || " + part("abs("+m+") % 60") + " END"
}

// sqlDecimalType is the fixed-point type of the scale per dialect:
// DECIMAL(38,S) on DuckDB and DataFusion, NUMERIC(38,S) on Postgres
// (DFC155 E; the Postgres and DataFusion lanes are documented, not gated).
func sqlDecimalType(scale int) string {
	if sqlDialectCur == dialectPostgres {
		return fmt.Sprintf("NUMERIC(38,%d)", scale)
	}
	return fmt.Sprintf("DECIMAL(38,%d)", scale)
}

// sqlAggAvg is AVG as a float in every dialect: DuckDB's AVG is a DOUBLE
// already; Postgres (and DataFusion over a decimal) return an exact
// NUMERIC, where ssql, the typed lane and DuckDB all average as a float
// — found running the int128 cases on the rig (2026-10-27).
func sqlAggAvg(qf string) string {
	if sqlDialectCur == dialectDuckDB {
		return "AVG(" + qf + ")"
	}
	return "CAST(AVG(" + qf + ") AS " + sqlFloatType() + ")"
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

// sqlAggCollect lists a group's present values, as exec's Collect does:
// a NULL is skipped (DuckDB's LIST kept it as a null element), a group
// with none is the empty list, and an address is listed as its text
// (DuckDB listed INET structs; DFC163 §7).
func sqlAggCollect(qf string) string {
	elem := qf
	// ssql:domain fixed: the kinds SQL's own list would print unlike ssql's text
	switch sqlColumnKinds[strings.Trim(qf, `"`)] {
	case "inet":
		elem = sqlInetText(qf)
	case "time":
		elem = sqlTimeText(qf)
	}
	present := " FILTER (WHERE " + qf + " IS NOT NULL)"
	switch sqlDialectCur {
	case dialectDuckDB:
		return "COALESCE(LIST(" + elem + sqlOrder.aggOrder + ")" + present + ", [])"
	case dialectPostgres:
		return "COALESCE(array_agg(" + elem + sqlOrder.aggOrder + ")" + present + ", '{}')"
	}
	return "COALESCE(array_agg(" + elem + sqlOrder.aggOrder + ")" + present + ", make_array())"
}

// sqlAggFirst and sqlAggLast skip a missing value, as exec does (Ross,
// DFC163 §7): the engines' first/last take a NULL row's NULL.
func sqlAggFirst(qf string) string {
	present := " FILTER (WHERE " + qf + " IS NOT NULL)"
	switch sqlDialectCur {
	case dialectPostgres:
		return "(array_agg(" + qf + sqlOrder.aggOrder + ")" + present + ")[1]"
	case dialectDataFusion:
		return "first_value(" + qf + sqlOrder.aggOrder + ")" + present
	}
	return "first(" + qf + sqlOrder.aggOrder + ")" + present
}

func sqlAggLast(qf string) string {
	present := " FILTER (WHERE " + qf + " IS NOT NULL)"
	switch sqlDialectCur {
	case dialectPostgres:
		agg := "array_agg(" + qf + sqlOrder.aggOrder + ")" + present
		return "(" + agg + ")[cardinality(" + agg + ")]"
	case dialectDataFusion:
		return "last_value(" + qf + sqlOrder.aggOrder + ")" + present
	}
	return "last(" + qf + sqlOrder.aggOrder + ")" + present
}

func sqlAggAny(qf string) string {
	if sqlDialectCur == dialectDataFusion { // any_value skips a NULL; first_value does not
		return "first_value(" + qf + ") FILTER (WHERE " + qf + " IS NOT NULL)"
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
	qf = sqlQuantileArg(qf)
	if sqlDialectCur == dialectPostgres {
		return "percentile_cont(0.5) WITHIN GROUP (ORDER BY " + qf + ")"
	}
	return "median(" + qf + ")"
}

func sqlAggPercentile(qf, p string) string {
	p = strings.TrimSpace(p)
	qf = sqlQuantileArg(qf)
	if sqlDialectCur == dialectDuckDB {
		return fmt.Sprintf("quantile_cont(%s, %s)", qf, p)
	}
	return fmt.Sprintf("percentile_cont(%s) WITHIN GROUP (ORDER BY %s)", p, qf)
}

func sqlAggStringAgg(qf, sep string) string {
	if sqlColumnKinds[strings.Trim(qf, `"`)] == "inet" {
		// An address's text, as ssql writes it: Postgres's CAST to text
		// adds /32 (DFC162 §9); abbrev does not (DFC163 §6).
		qf = sqlInetText(qf)
	} else if sqlColumnKinds[strings.Trim(qf, `"`)] == "time" {
		qf = sqlTimeText(qf)
	} else if sqlDialectCur == dialectPostgres {
		// string_agg(bigint, text) does not exist in Postgres.
		qf = "CAST(" + qf + " AS TEXT)"
	}
	return fmt.Sprintf("string_agg(%s, %s%s)", qf, sqlStringLiteral(sep), sqlOrder.aggOrder)
}

// sqlAggArgExtreme renders arg_max (desc=true) / arg_min: the value of qf
// on the row where qby is largest/smallest. A row with no qby is no
// candidate, as in exec and DuckDB's arg_max: NULLS LAST in both
// directions, where Postgres's and DataFusion's DESC default put the NULL
// first and arg-max answered that row's value (DFC163 §7).
func sqlAggArgExtreme(qf, qby string, desc bool) string {
	dir := "ASC NULLS LAST"
	name := "arg_min"
	if desc {
		dir, name = "DESC NULLS LAST", "arg_max"
	}
	present := " FILTER (WHERE " + qby + " IS NOT NULL)" // a group with no BY has no value
	switch sqlDialectCur {
	case dialectPostgres:
		return fmt.Sprintf("(array_agg(%s ORDER BY %s %s)%s)[1]", qf, qby, dir, present)
	case dialectDataFusion:
		return fmt.Sprintf("first_value(%s ORDER BY %s %s)%s", qf, qby, dir, present)
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
	// A division by zero is absent in ssql (the writer makes ±Inf null,
	// DFC133): DuckDB's inf is nulled by sqlFinite; Postgres and
	// DataFusion, which raise, divide by NULLIF(right, 0) instead
	// (DFC153 §4.2 — the DuckDB lane printed inf until 2026-10-20).
	if sqlDialectCur == dialectDuckDB {
		return sqlFinite("(" + left + " / " + right + ")")
	}
	return "(CAST(" + left + " AS " + sqlFloatType() + ") / NULLIF(" + right + ", 0))"
}

// sqlDividePlain is the division for a divisor known not to be zero (a
// literal): DuckDB's `/` is already a double division, the others cast.
func sqlDividePlain(left, right string) string {
	if sqlDialectCur == dialectDuckDB {
		return "(" + left + " / " + right + ")"
	}
	return "(CAST(" + left + " AS " + sqlFloatType() + ") / " + right + ")"
}

// pgColumnType is the Postgres column type for a declared ssql type;
// "auto" keeps the inferred one.
func pgColumnType(typeName, inferred string) string {
	if base, scale := ssql.ParseWireType(typeName); base == "decimal" && scale >= 0 {
		return fmt.Sprintf("NUMERIC(38,%d)", scale)
	}
	// ssql:domain scalar
	switch typeName {
	case "int":
		return "BIGINT"
	case "int128":
		return pgInt128Type
	case "float":
		return "DOUBLE PRECISION"
	case "bool":
		return "BOOLEAN"
	case "time":
		return sqlTimestampType()
	case "string":
		return "TEXT"
	case "inet":
		return "inet"
	}
	return inferred
}
