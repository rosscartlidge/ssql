package commands

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerGenerateSQL registers the "generate sql" subcommand
func registerGenerateSQL(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("sql").
		Description("Generate DuckDB SQL from ssql CLI pipeline").
		Example("(export SSQL_MODE=record; ssql from data.csv | ssql where -if age gt 25 | ssql to table) | ssql generate sql", "Generate SQL from pipeline").
		Example("(export SSQL_MODE=record; ssql from data.parquet | ssql group-by dept -sum salary total | ssql to table) | ssql generate sql", "Parquet aggregation query").
		Example("(export SSQL_MODE=record; ssql from data.csv | ssql where -if age gt 25 | ssql to table) | ssql generate sql -run", "Generate and execute with DuckDB").
		Example("ssql generate sql -run -pipeline 'ssql from data.csv | ssql group-by dept -sum salary total | ssql to table'", "One-shot: translate the quoted pipeline and execute with DuckDB").
		Flag("-run", "-r").
		Bool().
		Global().
		Default(false).
		Help("Execute the generated SQL with duckdb").
		Done().
		Flag("-pipeline", "-p").
		String().
		Global().
		Default("").
		Help("Run PIPELINE (a quoted ssql pipeline string) in record mode and translate its fragments — no export/subshell ceremony needed.").
		Done().
		Flag("OUTPUT").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.sql"}).
		Global().
		Default("").
		Help("Output SQL file (or stdout if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var run bool
			if outVal, ok := ctx.GlobalFlags["OUTPUT"]; ok {
				outputFile = outVal.(string)
			}
			if runVal, ok := ctx.GlobalFlags["-run"]; ok {
				run = runVal.(bool)
			}

			var fragSrc io.Reader = ctx.Stdin()
			if v, ok := ctx.GlobalFlags["-pipeline"]; ok && v.(string) != "" {
				// SQL translation reads record-mode fragments (the
				// assembler parses their Command strings), so the
				// mode is fixed — not a user knob here.
				fragments, err := runPipelineForFragments(v.(string), "record", "sql -pipeline")
				if err != nil {
					return err
				}
				fragSrc = bytes.NewReader(fragments)
			}
			sql, err := assembleSQL(fragSrc)
			if err != nil {
				return fmt.Errorf("assembling SQL: %w", err)
			}

			if run {
				cmd := exec.Command("duckdb", "-c", sql)
				cmd.Stdout = ctx.Stdout()
				cmd.Stderr = ctx.Stderr()
				return cmd.Run()
			}

			if outputFile != "" {
				if err := os.WriteFile(outputFile, []byte(sql), 0644); err != nil {
					return fmt.Errorf("writing output file: %w", err)
				}
				fmt.Fprintf(ctx.Stderr(), "Generated SQL written to %s\n", outputFile)
			} else {
				fmt.Print(sql)
			}

			return nil
		}).
		Done()
}

// sqlQuery accumulates SQL clauses from pipeline fragments.
type sqlQuery struct {
	fromClause   string
	joins        []string
	whereClauses []string // AND groups; multiple groups joined with OR
	selectExprs  []string
	distinct     bool
	groupBy      []string
	orderBy      []string
	limit        string
	offset       string
	sampled      bool     // FROM already carries a USING SAMPLE clause
	comments     []string // original ssql commands

	// columns tracks the current output field names, seeded from the source
	// CSV/TSV header and advanced by each stage's schemaOp (the same rules
	// pipeline-aware completion uses). nil = unknown; translation then falls
	// back to assuming referenced columns exist.
	columns []string
}

// needsWrap reports whether translating cmd into q would violate the
// pipeline's stage order. A single SELECT evaluates its clauses in a FIXED
// order (FROM→JOIN→WHERE→GROUP BY→SELECT→ORDER BY→LIMIT/OFFSET) regardless of
// the order clauses were added — so a stage arriving after a clause that SQL
// would run LATER (e.g. group-by after limit, a second projection, join after
// group-by) must instead apply to the RESULT of everything so far. Flattening
// anyway silently computes a different pipeline (e.g. `limit 10 | group-by`
// became "group everything, then keep 10 groups").
func needsWrap(q *sqlQuery, cmd string) bool {
	projected := len(q.selectExprs) > 0
	limited := q.limit != "" || q.offset != ""
	switch cmd {
	case "where":
		// WHERE runs before GROUP BY and before the SELECT list.
		return projected || len(q.groupBy) > 0 || limited
	case "group-by":
		// group-by owns the SELECT list and grouping; anything already
		// projected/grouped/ordered/limited must be materialised first.
		return projected || len(q.groupBy) > 0 || len(q.orderBy) > 0 || limited || q.distinct
	case "update", "rename", "cast", "include", "exclude":
		// One SELECT holds one projection spec.
		return projected || limited
	case "window":
		return projected || len(q.groupBy) > 0 || limited
	case "sort":
		return limited // ORDER BY runs before LIMIT; `limit | sort` is not `sort | limit`
	case "top":
		return len(q.orderBy) > 0 || limited // top imposes its own order + limit
	case "limit":
		return q.limit != ""
	case "sample":
		// USING SAMPLE binds to the FROM clause, i.e. BEFORE every other
		// clause of the same SELECT — so anything already accumulated
		// must be materialised first for pipeline order to hold.
		return q.sampled || len(q.whereClauses) > 0 || len(q.selectExprs) > 0 ||
			len(q.groupBy) > 0 || len(q.orderBy) > 0 || limited || q.distinct || len(q.joins) > 0
	case "offset":
		return limited // `limit 10 | offset 5` ≠ LIMIT 10 OFFSET 5 (which skips first)
	case "join":
		// JOIN runs first; joining the grouped/projected/limited result
		// needs that result as a subquery.
		return projected || len(q.groupBy) > 0 || len(q.orderBy) > 0 || limited
	case "distinct":
		return limited
	case "resample":
		// resample rebuilds the query around a grid + ASOF joins; any
		// accumulated state must be materialised as its source.
		return projected || len(q.whereClauses) > 0 || len(q.groupBy) > 0 ||
			len(q.orderBy) > 0 || limited || q.distinct || len(q.joins) > 0 || q.sampled
	}
	return false
}

// wrapAsSubquery folds everything accumulated so far into a FROM (subquery),
// so subsequent stages apply to its result — preserving pipeline order.
func wrapAsSubquery(q *sqlQuery) {
	sub := renderSelect(q)
	*q = sqlQuery{
		fromClause: "(\n" + indentLines(sub, "  ") + "\n)",
		comments:   q.comments,
		columns:    q.columns, // wrapping doesn't change the output schema
	}
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = prefix + ln
		}
	}
	return strings.Join(lines, "\n")
}

func assembleSQL(input io.Reader) (string, error) {
	var fragments []*lib.CodeFragment
	decoder := json.NewDecoder(input)

	for {
		var frag lib.CodeFragment
		if err := decoder.Decode(&frag); err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("decoding fragment: %w", err)
		}
		if frag.Type == "error" {
			return "", fmt.Errorf("code generation failed in %s: %s", frag.Command, frag.Error)
		}
		fragments = append(fragments, &frag)
	}

	if len(fragments) == 0 {
		return "", fmt.Errorf("no code fragments received")
	}

	q := &sqlQuery{}

	// Collect func fragments (subprocess sources for joins) and build subqueries
	var funcFrags []*lib.CodeFragment
	for _, frag := range fragments {
		if frag.Type == "func" {
			funcFrags = append(funcFrags, frag)
			continue // don't add to comments — shown as part of the join comment
		}
		if frag.Command != "" {
			cmd := frag.Command
			// For join commands referencing /dev/fd/, reconstruct with <(func command)
			if strings.Contains(cmd, "/dev/fd/") && len(funcFrags) > 0 {
				lastFunc := funcFrags[len(funcFrags)-1]
				if lastFunc.Command != "" {
					for i := strings.Index(cmd, "/dev/fd/"); i >= 0; {
						// Find end of /dev/fd/NNN
						end := i + 8
						for end < len(cmd) && cmd[end] >= '0' && cmd[end] <= '9' {
							end++
						}
						cmd = cmd[:i] + "<(" + lastFunc.Command + ")" + cmd[end:]
						break
					}
				}
			}
			q.comments = append(q.comments, cmd)
		}
		if err := translateFragment(q, frag, funcFrags); err != nil {
			return "", err
		}
	}

	return renderSQL(q), nil
}

// stageArgs returns the stage's ["ssql", kind, argv...] view,
// preferring the fragment's structured Op (DFC123 slice 3 — the
// process's own argv, lossless) over re-tokenizing the shell-quoted
// Command string (whose parser cannot represent an embedded single
// quote). Command parsing survives as the fallback for fragments from
// an older ssql across an SSH boundary.
func stageArgs(frag *lib.CodeFragment) []string {
	if frag.Op != nil && frag.Op.Kind != "" {
		return append([]string{"ssql", frag.Op.Kind}, frag.Op.Argv...)
	}
	return parseCommandArgs(frag.Command)
}

func translateFragment(q *sqlQuery, frag *lib.CodeFragment, funcFrags []*lib.CodeFragment) error {
	if frag.Command == "" {
		return nil // skip empty commands (e.g., Aggregate fragment from group-by)
	}

	args := stageArgs(frag)
	if len(args) < 2 {
		return nil
	}

	// args[0] is "ssql", args[1] is the command
	name := args[1]
	if needsWrap(q, name) {
		wrapAsSubquery(q)
	}
	var err error
	switch name {
	case "from":
		err = translateFrom(q, args[2:])
	case "where":
		err = translateWhere(q, args[2:])
	case "group-by":
		err = translateGroupBy(q, args[2:])
	case "sort":
		err = translateSort(q, args[2:])
	case "limit":
		err = translateLimit(q, args[2:])
	case "sample":
		err = translateSample(q, args[2:])
	case "offset":
		err = translateOffset(q, args[2:])
	case "top":
		err = translateTop(q, args[2:])
	case "distinct":
		q.distinct = true
	case "resample":
		err = translateResample(q, frag.Op, args[2:])
	case "join":
		err = translateJoin(q, args[2:], funcFrags)
	case "union":
		err = translateUnion(q, args[2:], funcFrags)
	case "window":
		err = translateWindow(q, args[2:])
	case "rename":
		err = translateRename(q, args[2:])
	case "cast":
		err = translateCast(q, args[2:])
	case "update":
		err = translateUpdate(q, args[2:])
	case "include":
		err = translateInclude(q, args[2:])
	case "exclude":
		err = translateExclude(q, args[2:])
	case "to":
		// Output commands don't affect SQL
		return nil
	default:
		return fmt.Errorf("unsupported command for SQL generation: %s", name)
	}
	if err != nil {
		return err
	}
	advanceColumns(q, name, args[2:])
	return nil
}

// advanceColumns tracks the pipeline's output columns through this stage via
// its schemaOp (the same rules pipeline-aware completion uses), so later
// stages can distinguish existing columns from new ones. Unknown → nil.
func advanceColumns(q *sqlQuery, name string, args []string) {
	switch name {
	case "from":
		// translateFrom seeds columns from the source header itself.
	case "join", "union":
		// Adds/merges columns from another source the ops can't see.
		q.columns = nil
	default:
		if q.columns == nil {
			return
		}
		if out, ok := lookupSchemaOp(name)(nil, q.columns, args); ok {
			q.columns = out
		} else {
			q.columns = nil
		}
	}
}

func translateFrom(q *sqlQuery, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("from requires a file argument")
	}

	// -sample at the source (DFC110 amendment): unseeded translates to
	// DuckDB's approximate system sampling — an honest match, both are
	// probability-proportional-to-storage; a given seed is refused like
	// the sample stage's (no cross-engine deterministic equivalent).
	var sampleN string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-sample-seed":
			return fmt.Errorf("from -sample-seed has no SQL equivalent — DuckDB cannot reproduce ssql's seeded byte-offset selection; drop -sample-seed or use generate go")
		case "-sample":
			if i+1 < len(args) {
				sampleN = args[i+1]
			}
		}
	}

	// Handle format subcommands: from csv FILE, from parquet FILE, etc.
	switch args[0] {
	case "csv", "tsv", "json", "jsonl", "arrow", "parquet", "xlsx":
		if len(args) < 2 {
			return fmt.Errorf("from %s requires a file argument", args[0])
		}
		// Collect file paths — skip flags AND the values of
		// value-taking flags (a bare `-sample 5` used to read '5' as a
		// second file).
		valueFlags := map[string]bool{
			"-sample": true, "-sample-seed": true, "-type": true, "-t": true,
			"-default-type": true, "-dt": true, "-source": true,
		}
		var files []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			if a == "--" {
				break
			}
			if strings.HasPrefix(a, "-") {
				if valueFlags[a] {
					i++
				}
				continue
			}
			files = append(files, a)
		}
		if len(files) == 1 {
			q.fromClause = quoteFile(files[0])
			// Seed column tracking from the header (generation runs where
			// the file lives). Non-delimited formats stay unknown.
			switch args[0] {
			case "csv":
				q.columns = delimHeader(files[0], ',')
			case "tsv":
				q.columns = delimHeader(files[0], '\t')
			}
		} else {
			// DuckDB: read_csv_auto(['file1.csv', 'file2.csv'])
			quoted := make([]string, len(files))
			for i, f := range files {
				quoted[i] = quoteFile(f)
			}
			q.fromClause = fmt.Sprintf("read_csv_auto([%s])", strings.Join(quoted, ", "))
		}
	case "ssh":
		return fmt.Errorf("from ssh has no SQL equivalent — it is an ssql-specific distributed feature")
	case "catalog":
		return fmt.Errorf("from catalog has no SQL equivalent — it is an ssql-specific distributed feature")
	default:
		// Bare: from FILE
		q.fromClause = quoteFile(args[0])
	}
	if sampleN != "" && sampleN != "0" {
		// reservoir, not system: DuckDB's system sampling is
		// percentage-only. Both sides are unseeded statistical samples
		// of N rows; the duckdb equivalence lane asserts cardinality.
		q.fromClause += " USING SAMPLE " + sampleN + " ROWS (reservoir)"
		q.sampled = true
	}
	return nil
}

func translateWhere(q *sqlQuery, args []string) error {
	// Parse -if and -if-expr conditions
	// Multiple -if within one clause are AND; clauses separated by + are OR
	var orGroups []string
	var currentAnd []string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "-if", "-i", "+if", "+i":
			if i+3 >= len(args) {
				return fmt.Errorf("incomplete -if condition")
			}
			field, op, value := args[i+1], args[i+2], args[i+3]
			cond := translateCondition(field, op, value)
			if args[i][0] == '+' {
				cond = "NOT (" + cond + ")"
			}
			currentAnd = append(currentAnd, cond)
			i += 4
		case "-if-expr", "-x", "+if-expr", "+x":
			if i+1 >= len(args) {
				return fmt.Errorf("incomplete -if-expr")
			}
			cond, err := exprToSQL(args[i+1])
			if err != nil {
				return fmt.Errorf("where -if-expr: %w", err)
			}
			if args[i][0] == '+' {
				cond = "NOT (" + cond + ")"
			}
			currentAnd = append(currentAnd, cond)
			i += 2
		case "+":
			// OR separator between clauses
			if len(currentAnd) > 0 {
				orGroups = append(orGroups, strings.Join(currentAnd, " AND "))
				currentAnd = nil
			}
			i++
		default:
			i++
		}
	}

	if len(currentAnd) > 0 {
		orGroups = append(orGroups, strings.Join(currentAnd, " AND "))
	}

	if len(orGroups) == 1 {
		q.whereClauses = append(q.whereClauses, orGroups[0])
	} else if len(orGroups) > 1 {
		wrapped := make([]string, len(orGroups))
		for i, g := range orGroups {
			wrapped[i] = "(" + g + ")"
		}
		q.whereClauses = append(q.whereClauses, strings.Join(wrapped, " OR "))
	}

	return nil
}

func translateCondition(field, op, value string) string {
	sqlOp := sqlOperator(op)
	if sqlOp == "LIKE" {
		// contains, startswith, endswith
		switch op {
		case "contains":
			return fmt.Sprintf("%s LIKE '%%%s%%'", quoteIdent(field), escapeLike(value))
		case "startswith":
			return fmt.Sprintf("%s LIKE '%s%%'", quoteIdent(field), escapeLike(value))
		case "endswith":
			return fmt.Sprintf("%s LIKE '%%%s'", quoteIdent(field), escapeLike(value))
		}
	}
	if op == "regex" {
		return fmt.Sprintf("regexp_matches(%s, '%s')", quoteIdent(field), escapeSQL(value))
	}
	return fmt.Sprintf("%s %s %s", quoteIdent(field), sqlOp, sqlLiteral(value))
}

// sqlLiteral renders a CLI value token as a SQL literal. Numeric and boolean
// tokens stay bare — quoting them as strings is semantically fragile ('9' >
// '15' is true as strings, false as numbers; DuckDB happens to coerce by
// column type but stricter engines don't) — everything else is a
// single-quoted string.
func sqlLiteral(value string) string {
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return value
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
		return value
	}
	switch strings.ToLower(value) {
	case "true":
		return "TRUE"
	case "false":
		return "FALSE"
	}
	return "'" + escapeSQL(value) + "'"
}

func translateGroupBy(q *sqlQuery, args []string) error {
	// Parse: group-by FIELD [FIELD...] [-count name] [-sum field name] [-avg field name] etc.
	i := 0

	// Collect group-by fields (positional args before first flag)
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		q.groupBy = append(q.groupBy, quoteIdent(args[i]))
		q.selectExprs = append(q.selectExprs, quoteIdent(args[i]))
		i++
	}

	// Parse aggregation flags
	for i < len(args) {
		switch args[i] {
		case "-count":
			if i+1 < len(args) {
				q.selectExprs = append(q.selectExprs, fmt.Sprintf("COUNT(*) AS %s", quoteIdent(args[i+1])))
				i += 2
			} else {
				i++
			}
		case "-sum":
			if i+2 < len(args) {
				q.selectExprs = append(q.selectExprs, fmt.Sprintf("SUM(%s) AS %s", quoteIdent(args[i+1]), quoteIdent(args[i+2])))
				i += 3
			} else {
				i++
			}
		case "-avg":
			if i+2 < len(args) {
				q.selectExprs = append(q.selectExprs, fmt.Sprintf("AVG(%s) AS %s", quoteIdent(args[i+1]), quoteIdent(args[i+2])))
				i += 3
			} else {
				i++
			}
		case "-min":
			if i+2 < len(args) {
				q.selectExprs = append(q.selectExprs, fmt.Sprintf("MIN(%s) AS %s", quoteIdent(args[i+1]), quoteIdent(args[i+2])))
				i += 3
			} else {
				i++
			}
		case "-max":
			if i+2 < len(args) {
				q.selectExprs = append(q.selectExprs, fmt.Sprintf("MAX(%s) AS %s", quoteIdent(args[i+1]), quoteIdent(args[i+2])))
				i += 3
			} else {
				i++
			}
		case "-collect":
			if i+2 < len(args) {
				q.selectExprs = append(q.selectExprs, fmt.Sprintf("LIST(%s) AS %s", quoteIdent(args[i+1]), quoteIdent(args[i+2])))
				i += 3
			} else {
				i++
			}
		// Silently dropping an aggregation would produce wrong results —
		// fail loudly on the forms with no SQL translation (yet).
		case "-expr", "-e":
			return fmt.Errorf("group-by -expr has no SQL translation (expression aggregations are ssql-specific)")
		case "-stream-expr":
			return fmt.Errorf("group-by -stream-expr has no SQL translation (expression aggregations are ssql-specific)")
		case "-rollup":
			return fmt.Errorf("group-by -rollup is not yet translated to SQL (GROUP BY ROLLUP)")
		case "-cube":
			return fmt.Errorf("group-by -cube is not yet translated to SQL (GROUP BY CUBE)")
		default:
			i++
		}
	}

	return nil
}

func translateSort(q *sqlQuery, args []string) error {
	// Parse fields with per-clause direction: "dept - salary -desc"
	// The "-" is a clause separator; -desc/-asc apply to all fields in their clause.
	// We collect fields per clause, then apply the clause's direction.
	type sortClause struct {
		fields []string
		desc   bool
	}
	var clauses []sortClause
	current := sortClause{}

	for _, arg := range args {
		switch arg {
		case "-desc", "-d":
			current.desc = true
		case "-asc", "-a":
			current.desc = false
		case "-":
			if len(current.fields) > 0 {
				clauses = append(clauses, current)
			}
			current = sortClause{}
		default:
			if strings.HasPrefix(arg, "-") {
				continue
			}
			current.fields = append(current.fields, arg)
		}
	}
	if len(current.fields) > 0 {
		clauses = append(clauses, current)
	}

	var entries []string
	for _, c := range clauses {
		for _, f := range c.fields {
			entry := quoteIdent(f)
			if c.desc {
				entry += " DESC"
			}
			entries = append(entries, entry)
		}
	}
	// A later `sort` re-sorts stably: its keys take precedence and any
	// existing order becomes the tie-break — so PREPEND (appending would
	// leave the earlier sort as the primary key).
	q.orderBy = append(entries, q.orderBy...)
	return nil
}

func translateLimit(q *sqlQuery, args []string) error {
	var n string
	last := false
	for _, a := range args {
		switch a {
		case "-last":
			last = true
		case "-generate", "-g":
		default:
			if !strings.HasPrefix(a, "-") && n == "" {
				n = a
			}
		}
	}
	if !last {
		if n != "" {
			q.limit = n
		}
		return nil
	}
	// limit -last N: the LAST N in the pipeline's current order. SQL
	// has no arrival order, so this is only translatable when the
	// query carries an ORDER BY: take N under the REVERSED order,
	// then restore the original order outside.
	if len(q.orderBy) == 0 {
		return fmt.Errorf("limit -last needs a preceding sort for SQL — arrival order is undefined in SQL; add `ssql sort FIELD` before it, or use generate go")
	}
	if n == "" {
		return fmt.Errorf("limit -last: need N")
	}
	original := append([]string(nil), q.orderBy...)
	reversed := make([]string, len(original))
	for i, e := range original {
		switch {
		case strings.HasSuffix(e, " DESC"):
			reversed[i] = strings.TrimSuffix(e, " DESC") + " ASC"
		case strings.HasSuffix(e, " ASC"):
			reversed[i] = strings.TrimSuffix(e, " ASC") + " DESC"
		default:
			reversed[i] = e + " DESC" // bare entry sorts ASC by default
		}
	}
	q.orderBy = reversed
	q.limit = n
	wrapAsSubquery(q)
	q.orderBy = original
	return nil
}

// translateSample renders DuckDB's USING SAMPLE on the FROM clause.
// Seeded sampling is REFUSED loudly: DuckDB's RNG cannot match ssql's
// spec-stable generator, so a seeded sample has no cross-engine
// deterministic equivalent (DFC110) — the Go lanes stay byte-identical
// under -seed; the SQL lane covers unseeded, statistically-equivalent
// sampling only.
func translateSample(q *sqlQuery, args []string) error {
	var n, percent string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-seed":
			return fmt.Errorf("sample -seed has no SQL equivalent — DuckDB's RNG cannot reproduce ssql's seeded selection; drop -seed for a statistical sample, or use generate go")
		case "-percent", "-p":
			if i+1 < len(args) {
				percent = args[i+1]
				i++
			}
		case "-generate", "-g":
		default:
			if !strings.HasPrefix(args[i], "-") && n == "" {
				n = args[i]
			}
		}
	}
	if n == "0" {
		return nil // pass-through dial: stage vanishes (matches Go codegen)
	}
	switch {
	case percent != "":
		q.fromClause += " USING SAMPLE " + percent + "% (bernoulli)"
	case n != "":
		q.fromClause += " USING SAMPLE " + n + " ROWS (reservoir)"
	default:
		return fmt.Errorf("sample: need N or -percent")
	}
	q.sampled = true
	return nil
}

func translateOffset(q *sqlQuery, args []string) error {
	if len(args) > 0 {
		q.offset = args[0]
	}
	return nil
}

func translateTop(q *sqlQuery, args []string) error {
	// top [-asc] N -field FIELD → ORDER BY FIELD DESC|ASC LIMIT N.
	// Default is descending (largest first); -asc selects the smallest.
	// N is the first bare positional; the field comes from -field/-f (NOT
	// the long-removed -by). Order-independent so `-asc` before N parses.
	var field, limit string
	asc := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-asc":
			asc = true
		case "-field", "-f":
			if i+1 < len(args) {
				field = args[i+1]
				i++
			}
		default:
			if limit == "" && !strings.HasPrefix(args[i], "-") && !strings.HasPrefix(args[i], "+") {
				limit = args[i]
			}
		}
	}
	if limit != "" {
		q.limit = limit
	}
	if field != "" {
		dir := "DESC"
		if asc {
			dir = "ASC"
		}
		q.orderBy = append(q.orderBy, quoteIdent(field)+" "+dir)
	}
	return nil
}

func translateJoin(q *sqlQuery, args []string, funcFrags []*lib.CodeFragment) error {
	if len(args) == 0 {
		return fmt.Errorf("join requires a file argument")
	}

	filePath := args[0]
	var joinCond string

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-using", "-u":
			if i+1 < len(args) {
				joinCond = fmt.Sprintf("USING (%s)", quoteIdent(args[i+1]))
				i++
			}
		case "-on", "-o":
			if i+2 < len(args) {
				joinCond = fmt.Sprintf("ON t1.%s = t2.%s", quoteIdent(args[i+1]), quoteIdent(args[i+2]))
				i += 2
			}
		}
	}

	if joinCond == "" {
		joinCond = "ON TRUE"
	}

	// Check if the join file is a process substitution — build a SQL subquery
	// from the func fragment's body commands
	if strings.HasPrefix(filePath, "/dev/fd/") && len(funcFrags) > 0 {
		subquery := buildJoinSubquery(funcFrags[len(funcFrags)-1])
		if subquery != "" {
			q.joins = append(q.joins, fmt.Sprintf("JOIN (%s) %s", subquery, joinCond))
			return nil
		}
	}

	q.joins = append(q.joins, fmt.Sprintf("JOIN %s %s", quoteFile(filePath), joinCond))
	return nil
}

// translateUnion translates `union [-all] -file <(…)…` to a SQL set
// operation: the accumulated query UNION [ALL] each source subquery, wrapped
// as the new FROM. Bare UNION deduplicates — exactly `union` without -all.
// Before v4.56 union was "unsupported"; silently dropping it would return a
// fraction of the rows.
func translateUnion(q *sqlQuery, args []string, funcFrags []*lib.CodeFragment) error {
	if q.fromClause == "" {
		return fmt.Errorf("union requires an upstream source")
	}
	unionAll := false
	var files []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-all", "-a":
			unionAll = true
		case "-file", "-f":
			if i+1 < len(args) {
				files = append(files, args[i+1])
				i++
			}
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("union requires at least one -file source")
	}

	// Process-substitution sources arrive as unionSourceN func fragments, in
	// -file order, emitted just before this fragment.
	var procs []*lib.CodeFragment
	for _, f := range funcFrags {
		if strings.HasPrefix(f.FuncName, "unionSource") {
			procs = append(procs, f)
		}
	}

	op := "UNION"
	if unionAll {
		op = "UNION ALL"
	}
	parts := []string{renderSelect(q)}
	pi := 0
	for _, f := range files {
		if !strings.HasPrefix(f, "/dev/fd/") {
			return fmt.Errorf("union -file %s: schema-headed JSONL files have no SQL translation — use <(ssql from csv FILE)", f)
		}
		if pi >= len(procs) {
			return fmt.Errorf("union: missing source fragment for %s", f)
		}
		sub := buildJoinSubquery(procs[pi])
		pi++
		if sub == "" {
			return fmt.Errorf("union: source pipeline is too complex to translate (only from/where are supported inside <(…)>)")
		}
		parts = append(parts, sub)
	}

	*q = sqlQuery{
		fromClause: "(\n" + indentLines(strings.Join(parts, "\n"+op+"\n"), "  ") + "\n)",
		comments:   q.comments,
	}
	return nil
}

// delimHeader reads the header row of a delimited file; nil when unreadable
// (column tracking then degrades to unknown rather than failing generation).
func delimHeader(path string, comma rune) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = comma
	header, err := r.Read()
	if err != nil || len(header) == 0 {
		return nil
	}
	return header
}

// buildJoinSubquery builds a SQL subquery from a func fragment's body.
func buildJoinSubquery(funcFrag *lib.CodeFragment) string {
	if funcFrag == nil {
		return ""
	}

	// Build a mini SQL query from the func body's commands
	sub := &sqlQuery{}
	for _, bodyFrag := range funcFrag.FuncBody {
		if bodyFrag.Command == "" {
			continue
		}
		args := stageArgs(bodyFrag)
		if len(args) < 2 {
			continue
		}
		switch args[1] {
		case "from":
			translateFrom(sub, args[2:])
		case "where":
			translateWhere(sub, args[2:])
		}
	}

	// If we didn't get a FROM, try the func fragment's own Command
	if sub.fromClause == "" && funcFrag.Command != "" {
		args := parseCommandArgs(funcFrag.Command)
		if len(args) >= 2 && args[1] == "from" {
			translateFrom(sub, args[2:])
		}
	}

	if sub.fromClause == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("SELECT * FROM " + sub.fromClause)
	if len(sub.whereClauses) > 0 {
		sb.WriteString(" WHERE " + strings.Join(sub.whereClauses, " AND "))
	}
	return sb.String()
}

func translateWindow(q *sqlQuery, args []string) error {
	// Parse window clauses separated by "-"
	// Each clause has: partition, order, desc, preceding, following, and function flags
	type windowClause struct {
		partitionBy []string
		orderBy     []string
		desc        bool
		preceding   int
		following   int
		funcs       []string // pre-built SQL function expressions like "ROW_NUMBER() AS rn"
	}

	clauses := []windowClause{{preceding: -1, following: 0}}
	cur := &clauses[0]

	i := 0
	for i < len(args) {
		switch args[i] {
		case "-":
			// Clause separator
			clauses = append(clauses, windowClause{preceding: -1, following: 0})
			cur = &clauses[len(clauses)-1]
			i++
		case "-partition":
			if i+1 < len(args) {
				cur.partitionBy = append(cur.partitionBy, args[i+1])
				i += 2
			} else {
				i++
			}
		case "-order":
			if i+1 < len(args) {
				cur.orderBy = append(cur.orderBy, args[i+1])
				i += 2
			} else {
				i++
			}
		case "-desc", "-d":
			cur.desc = true
			i++
		case "-preceding":
			if i+1 < len(args) {
				cur.preceding, _ = strconv.Atoi(args[i+1])
				i += 2
			} else {
				i++
			}
		case "-following":
			if i+1 < len(args) {
				cur.following, _ = strconv.Atoi(args[i+1])
				i += 2
			} else {
				i++
			}
		case "-presorted":
			i++ // ignore for SQL
		// Ranking: 1-arg → result
		case "-row-number":
			if i+1 < len(args) {
				cur.funcs = append(cur.funcs, fmt.Sprintf("ROW_NUMBER() AS %s", quoteIdent(args[i+1])))
				i += 2
			} else {
				i++
			}
		case "-rank":
			if i+1 < len(args) {
				cur.funcs = append(cur.funcs, fmt.Sprintf("RANK() AS %s", quoteIdent(args[i+1])))
				i += 2
			} else {
				i++
			}
		case "-dense-rank":
			if i+1 < len(args) {
				cur.funcs = append(cur.funcs, fmt.Sprintf("DENSE_RANK() AS %s", quoteIdent(args[i+1])))
				i += 2
			} else {
				i++
			}
		case "-percent-rank":
			if i+1 < len(args) {
				cur.funcs = append(cur.funcs, fmt.Sprintf("PERCENT_RANK() AS %s", quoteIdent(args[i+1])))
				i += 2
			} else {
				i++
			}
		// 1-arg result: -count result
		case "-count":
			if i+1 < len(args) {
				cur.funcs = append(cur.funcs, fmt.Sprintf("COUNT(*) AS %s", quoteIdent(args[i+1])))
				i += 2
			} else {
				i++
			}
		// 2-arg: -ntile n result
		case "-ntile":
			if i+2 < len(args) {
				cur.funcs = append(cur.funcs, fmt.Sprintf("NTILE(%s) AS %s", args[i+1], quoteIdent(args[i+2])))
				i += 3
			} else {
				i++
			}
		// 2-arg: -func field result
		case "-sum", "-avg", "-min", "-max", "-first", "-last":
			if i+2 < len(args) {
				sqlFunc := windowSQLFunc(args[i])
				cur.funcs = append(cur.funcs, fmt.Sprintf("%s(%s) AS %s", sqlFunc, quoteIdent(args[i+1]), quoteIdent(args[i+2])))
				i += 3
			} else {
				i++
			}
		// 3-arg: -lag/-lead field n result
		case "-lag", "-lead":
			if i+3 < len(args) {
				sqlFunc := strings.ToUpper(args[i][1:])
				cur.funcs = append(cur.funcs, fmt.Sprintf("%s(%s, %s) AS %s", sqlFunc, quoteIdent(args[i+1]), args[i+2], quoteIdent(args[i+3])))
				i += 4
			} else {
				i++
			}
		default:
			i++
		}
	}

	// Window adds new columns — ensure existing columns are preserved
	if len(q.selectExprs) == 0 {
		q.selectExprs = append(q.selectExprs, "*")
	}

	// Build SQL OVER clauses
	for _, c := range clauses {
		overParts := []string{}

		if len(c.partitionBy) > 0 {
			quoted := make([]string, len(c.partitionBy))
			for j, f := range c.partitionBy {
				quoted[j] = quoteIdent(f)
			}
			overParts = append(overParts, "PARTITION BY "+strings.Join(quoted, ", "))
		}

		if len(c.orderBy) > 0 {
			quoted := make([]string, len(c.orderBy))
			for j, f := range c.orderBy {
				quoted[j] = quoteIdent(f)
				if c.desc {
					quoted[j] += " DESC"
				}
			}
			overParts = append(overParts, "ORDER BY "+strings.Join(quoted, ", "))
		}

		// Frame clause — only emit if non-default or if aggregate functions present
		frameSQL := buildFrameSQL(c.preceding, c.following)
		if frameSQL != "" {
			overParts = append(overParts, frameSQL)
		}

		over := strings.Join(overParts, " ")

		for _, f := range c.funcs {
			// f is like "ROW_NUMBER() AS rn" — insert OVER before AS
			asIdx := strings.LastIndex(f, " AS ")
			if asIdx < 0 {
				continue
			}
			funcExpr := f[:asIdx]
			alias := f[asIdx:]
			q.selectExprs = append(q.selectExprs, funcExpr+" OVER ("+over+")"+alias)
		}
	}

	return nil
}

func windowSQLFunc(flag string) string {
	switch flag {
	case "-sum":
		return "SUM"
	case "-avg":
		return "AVG"
	case "-min":
		return "MIN"
	case "-max":
		return "MAX"
	case "-first":
		return "FIRST_VALUE"
	case "-last":
		return "LAST_VALUE"
	default:
		return strings.ToUpper(flag[1:])
	}
}

func buildFrameSQL(preceding, following int) string {
	// Default frame (unbounded preceding to current row) matches SQL default
	// for aggregate window functions when ORDER BY is present, so we can often omit it.
	// But be explicit when the user specified non-default values.
	if preceding == -1 && following == 0 {
		return "" // default — let DuckDB use its own default
	}

	var start, end string
	if preceding < 0 {
		start = "UNBOUNDED PRECEDING"
	} else if preceding == 0 {
		start = "CURRENT ROW"
	} else {
		start = fmt.Sprintf("%d PRECEDING", preceding)
	}

	if following < 0 {
		end = "UNBOUNDED FOLLOWING"
	} else if following == 0 {
		end = "CURRENT ROW"
	} else {
		end = fmt.Sprintf("%d FOLLOWING", following)
	}

	return fmt.Sprintf("ROWS BETWEEN %s AND %s", start, end)
}

func translateRename(q *sqlQuery, args []string) error {
	// DuckDB: SELECT * RENAME ("old" AS "new", ...)
	var renames []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-as" && i+2 < len(args) {
			renames = append(renames, fmt.Sprintf("%s AS %s", quoteIdent(args[i+1]), quoteIdent(args[i+2])))
			i += 2
		}
	}
	if len(renames) > 0 {
		q.selectExprs = append(q.selectExprs, "* RENAME ("+strings.Join(renames, ", ")+")")
	}
	return nil
}

func translateCast(q *sqlQuery, args []string) error {
	// DuckDB: SELECT * REPLACE (CAST("field" AS TYPE) AS "field", ...)
	var replacements []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-type" && i+2 < len(args) {
			field, typeName := args[i+1], args[i+2]
			sqlType := mapTypeToSQL(typeName)
			replacements = append(replacements, fmt.Sprintf("CAST(%s AS %s) AS %s", quoteIdent(field), sqlType, quoteIdent(field)))
			i += 2
		}
	}
	if len(replacements) > 0 {
		q.selectExprs = append(q.selectExprs, "* REPLACE ("+strings.Join(replacements, ", ")+")")
	}
	return nil
}

func translateUpdate(q *sqlQuery, args []string) error {
	// DuckDB: SELECT * REPLACE (CASE WHEN cond THEN val ELSE "field" END AS "field")
	// Parse: -if field op val -set field val [-set-expr field expr ...]
	// Multiple -if groups create chained CASE WHEN ... WHEN ... ELSE ... END

	// First pass: collect all target fields and their condition/value pairs.
	// valueSQL is an already-rendered SQL expression (literal or translated
	// -set-expr), inserted verbatim into THEN/ELSE.
	type assignment struct {
		conds    []string // AND conditions for this clause
		field    string
		valueSQL string
	}
	var assignments []assignment
	var currentConds []string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "-if", "-i", "+if", "+i":
			if i+3 >= len(args) {
				return fmt.Errorf("incomplete -if condition in update")
			}
			cond := translateCondition(args[i+1], args[i+2], args[i+3])
			if args[i][0] == '+' {
				cond = "NOT (" + cond + ")"
			}
			currentConds = append(currentConds, cond)
			i += 4
		case "-if-expr", "-x", "+if-expr", "+x":
			if i+1 >= len(args) {
				return fmt.Errorf("incomplete -if-expr in update")
			}
			cond, err := exprToSQL(args[i+1])
			if err != nil {
				return fmt.Errorf("update -if-expr: %w", err)
			}
			if args[i][0] == '+' {
				cond = "NOT (" + cond + ")"
			}
			currentConds = append(currentConds, cond)
			i += 2
		case "-set", "-s":
			if i+2 >= len(args) {
				return fmt.Errorf("incomplete -set in update")
			}
			assignments = append(assignments, assignment{
				conds:    append([]string{}, currentConds...),
				field:    args[i+1],
				valueSQL: sqlLiteral(args[i+2]),
			})
			i += 3
		case "-set-expr", "-e":
			if i+2 >= len(args) {
				return fmt.Errorf("incomplete -set-expr in update")
			}
			valueSQL, err := exprToSQL(args[i+2])
			if err != nil {
				return fmt.Errorf("update -set-expr: %w", err)
			}
			assignments = append(assignments, assignment{
				conds:    append([]string{}, currentConds...),
				field:    args[i+1],
				valueSQL: valueSQL,
			})
			i += 3
		case "-":
			// Clause separator — reset conditions
			currentConds = nil
			i++
		default:
			i++
		}
	}

	// Group assignments by target field to build a single CASE expression per field
	fieldCases := make(map[string][]assignment)
	var fieldOrder []string
	for _, a := range assignments {
		if _, seen := fieldCases[a.field]; !seen {
			fieldOrder = append(fieldOrder, a.field)
		}
		fieldCases[a.field] = append(fieldCases[a.field], a)
	}

	var replacements, additions []string
	for _, field := range fieldOrder {
		cases := fieldCases[field]
		// `* REPLACE` requires the column to exist; a NEW field (exec creates
		// it) must be an added select expression instead. Only decidable when
		// column tracking is live — unknown schema assumes the column exists.
		isNew := q.columns != nil && !slices.Contains(q.columns, field)

		// Conditional sets become WHEN arms; an unconditional set becomes the
		// ELSE (last one wins). No conditionals at all → plain value, since
		// `CASE ELSE x END` (no WHEN) is a SQL syntax error.
		var whens []assignment
		elseSQL := quoteIdent(field) // default: preserve original value
		for _, c := range cases {
			if len(c.conds) > 0 {
				whens = append(whens, c)
			} else {
				elseSQL = c.valueSQL
			}
		}
		if isNew && len(whens) > 0 {
			// exec leaves the field ABSENT on non-matching rows; SQL columns
			// are rectangular, so there is no faithful translation.
			return fmt.Errorf("update: conditional -set on new field %q has no SQL translation (unmatched rows would need a value)", field)
		}
		if isNew && elseSQL == quoteIdent(field) {
			return fmt.Errorf("update: -set on new field %q needs an unconditional value for SQL translation", field)
		}
		exprSQL := elseSQL
		if len(whens) > 0 {
			var sb strings.Builder
			sb.WriteString("CASE")
			for _, c := range whens {
				sb.WriteString(" WHEN " + strings.Join(c.conds, " AND ") + " THEN " + c.valueSQL)
			}
			sb.WriteString(" ELSE " + elseSQL + " END")
			exprSQL = sb.String()
		}
		if isNew {
			additions = append(additions, fmt.Sprintf("%s AS %s", exprSQL, quoteIdent(field)))
		} else {
			replacements = append(replacements, fmt.Sprintf("%s AS %s", exprSQL, quoteIdent(field)))
		}
	}

	switch {
	case len(replacements) > 0:
		q.selectExprs = append(q.selectExprs, "* REPLACE ("+strings.Join(replacements, ", ")+")")
	case len(additions) > 0:
		q.selectExprs = append(q.selectExprs, "*")
	}
	q.selectExprs = append(q.selectExprs, additions...)
	return nil
}

func mapTypeToSQL(typeName string) string {
	switch strings.ToLower(typeName) {
	case "int", "integer", "int64":
		return "BIGINT"
	case "float", "float64", "double", "number":
		return "DOUBLE"
	case "string", "str", "text":
		return "VARCHAR"
	case "bool", "boolean":
		return "BOOLEAN"
	case "date":
		return "DATE"
	case "timestamp", "datetime":
		return "TIMESTAMP"
	default:
		return strings.ToUpper(typeName)
	}
}

func translateInclude(q *sqlQuery, args []string) error {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			q.selectExprs = append(q.selectExprs, quoteIdent(arg))
		}
	}
	return nil
}

func translateExclude(q *sqlQuery, args []string) error {
	// DuckDB supports SELECT * EXCLUDE (col1, col2)
	var cols []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			cols = append(cols, quoteIdent(arg))
		}
	}
	if len(cols) > 0 {
		q.selectExprs = append(q.selectExprs, "* EXCLUDE ("+strings.Join(cols, ", ")+")")
	}
	return nil
}

func renderSQL(q *sqlQuery) string {
	var sb strings.Builder

	// Comment with original pipeline
	if len(q.comments) > 0 {
		sb.WriteString("-- Generated by ssql generate sql\n")
		sb.WriteString("-- Pipeline:\n")
		for _, c := range q.comments {
			sb.WriteString("--   " + c + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString(renderSelect(q))
	sb.WriteString("\n;\n")
	return sb.String()
}

// renderSelect renders the accumulated query as a SELECT statement (no
// pipeline comments, no trailing semicolon) so it can also serve as a
// subquery body for wrapAsSubquery.
func renderSelect(q *sqlQuery) string {
	var sb strings.Builder

	// SELECT
	sel := "*"
	if len(q.selectExprs) > 0 {
		sel = strings.Join(q.selectExprs, ", ")
	}
	if q.distinct {
		sel = "DISTINCT " + sel
	}
	sb.WriteString("SELECT " + sel + "\n")

	// FROM
	if q.fromClause != "" {
		sb.WriteString("FROM " + q.fromClause + "\n")
	}

	// JOINs
	for _, j := range q.joins {
		sb.WriteString(j + "\n")
	}

	// WHERE
	if len(q.whereClauses) > 0 {
		sb.WriteString("WHERE " + strings.Join(q.whereClauses, " AND ") + "\n")
	}

	// GROUP BY
	if len(q.groupBy) > 0 {
		sb.WriteString("GROUP BY " + strings.Join(q.groupBy, ", ") + "\n")
	}

	// ORDER BY
	if len(q.orderBy) > 0 {
		sb.WriteString("ORDER BY " + strings.Join(q.orderBy, ", ") + "\n")
	}

	// LIMIT
	if q.limit != "" {
		sb.WriteString("LIMIT " + q.limit + "\n")
	}

	// OFFSET
	if q.offset != "" {
		sb.WriteString("OFFSET " + q.offset + "\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

// --- SQL helpers ---

func sqlOperator(op string) string {
	switch op {
	case "eq":
		return "="
	case "ne":
		return "!="
	case "gt":
		return ">"
	case "ge":
		return ">="
	case "lt":
		return "<"
	case "le":
		return "<="
	case "contains", "startswith", "endswith":
		return "LIKE"
	case "regex":
		return "REGEXP"
	default:
		return "="
	}
}

func quoteFile(path string) string {
	return "'" + escapeSQL(path) + "'"
}

func quoteIdent(name string) string {
	// Only quote if the name contains special characters or is a reserved word
	// For simplicity, quote everything with double quotes (DuckDB standard)
	if strings.ContainsAny(name, " -./") || isSQLReserved(name) {
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
	return name
}

func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return escapeSQL(s)
}

func isSQLReserved(name string) bool {
	switch strings.ToUpper(name) {
	case "SELECT", "FROM", "WHERE", "GROUP", "ORDER", "BY", "LIMIT", "OFFSET",
		"JOIN", "ON", "AND", "OR", "NOT", "IN", "BETWEEN", "LIKE", "AS",
		"INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER", "TABLE",
		"INDEX", "VIEW", "DISTINCT", "HAVING", "UNION", "ALL", "EXISTS",
		"CASE", "WHEN", "THEN", "ELSE", "END", "NULL", "TRUE", "FALSE",
		"ASC", "DESC", "COUNT", "SUM", "AVG", "MIN", "MAX", "DATE", "TIME",
		"TIMESTAMP", "INT", "INTEGER", "FLOAT", "DOUBLE", "VARCHAR", "TEXT",
		"BOOLEAN", "PRIMARY", "KEY", "FOREIGN", "REFERENCES", "DEFAULT",
		"CHECK", "UNIQUE", "SET", "VALUES", "INTO":
		return true
	}
	return false
}

// parseCommandArgs splits a command string into args, respecting single quotes.
func parseCommandArgs(cmd string) []string {
	var args []string
	var current strings.Builder
	inQuote := false

	for _, c := range cmd {
		switch {
		case c == '\'' && !inQuote:
			inQuote = true
		case c == '\'' && inQuote:
			inQuote = false
		case c == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(c)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// translateResample (DFC121 resolution #4): the DuckDB translation —
// epoch-aligned generate_series grid + ASOF joins. Semantics mirror
// ssql.ResampleRecords exactly (the equivalence harness arbitrates):
// per-field series skip NULLs (raggedness), duplicate timestamps keep
// the highest value, edges clamp to the nearest observation, and the
// epoch unit is detected by magnitude (max |ts|, matching Go's
// thresholds) unless -time-unit pins it.
//
// v1 scope, loud refusals otherwise: numeric epoch timestamps only
// (string timestamps and -time-format need TIMESTAMP-typed handling —
// use generate go), no -from/-to bounds yet.
func translateResample(q *sqlQuery, op *lib.Op, args []string) error {
	// Structured path (DFC123 slice 3): the command recorded its own
	// parsed config on the Op — no second implementation of its flag
	// grammar here. Defaults, aliases, and accumulation were already
	// applied by the command itself.
	if t, ok := op.Str("time"); ok {
		if _, refused := op.Str("time_format"); refused {
			return fmt.Errorf("resample over string timestamps has no SQL translation — numeric epochs only; use generate go")
		}
		if _, refused := op.Str("from"); refused {
			return fmt.Errorf("resample -from/-to has no SQL translation yet — use generate go")
		}
		if _, refused := op.Str("to"); refused {
			return fmt.Errorf("resample -from/-to has no SQL translation yet — use generate go")
		}
		everyNs, ok := op.Int64("every")
		if !ok || everyNs <= 0 {
			return fmt.Errorf("resample: bad every in op descriptor")
		}
		values, ok := op.StrList("values")
		if !ok || len(values) == 0 {
			return fmt.Errorf("resample: no values in op descriptor")
		}
		fill, _ := op.Str("fill")
		timeUnit, _ := op.Str("time_unit")
		return buildResampleSQL(q, t, everyNs, values, fill, timeUnit)
	}

	// Fallback: parse the stage's argv (fragments from an older ssql).
	var timeField, everyStr, fill, timeUnit string
	var values []string
	fill = "previous"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-time":
			if i+1 < len(args) {
				timeField = args[i+1]
				i++
			}
		case "-every":
			if i+1 < len(args) {
				everyStr = args[i+1]
				i++
			}
		case "-value":
			if i+1 < len(args) {
				values = append(values, args[i+1])
				i++
			}
		case "-fill":
			if i+1 < len(args) {
				fill = args[i+1]
				i++
			}
		case "-time-unit":
			if i+1 < len(args) {
				timeUnit = args[i+1]
				i++
			}
		case "-from", "-to":
			return fmt.Errorf("resample -from/-to has no SQL translation yet — use generate go")
		case "-time-format":
			return fmt.Errorf("resample over string timestamps has no SQL translation — numeric epochs only; use generate go")
		case "-generate", "-g":
		}
	}
	if timeField == "" || everyStr == "" || len(values) == 0 {
		return fmt.Errorf("resample: -time, -every and at least one -value are required")
	}
	every, err := time.ParseDuration(everyStr)
	if err != nil || every <= 0 {
		return fmt.Errorf("resample: bad -every %q", everyStr)
	}
	return buildResampleSQL(q, timeField, int64(every), values, fill, timeUnit)
}

// buildResampleSQL is the DuckDB lowering of resample's SEMANTIC
// config (epoch grid + ASOF joins) — shared by the structured-Op path
// and the argv fallback, so both produce byte-identical SQL.
func buildResampleSQL(q *sqlQuery, timeField string, everyNs int64, values []string, fill, timeUnit string) error {
	if fill == "" {
		fill = "previous"
	}

	// The epoch unit (in ns): pinned by -time-unit, else detected from
	// the data by magnitude — Go's exact thresholds.
	unitExpr := ""
	switch timeUnit {
	case "ns":
		unitExpr = "1"
	case "us":
		unitExpr = "1000"
	case "ms":
		unitExpr = "1000000"
	case "s":
		unitExpr = "1000000000"
	case "":
		unitExpr = "(CASE WHEN max(abs(__ts)) >= 1e17 THEN 1 WHEN max(abs(__ts)) >= 1e14 THEN 1000 WHEN max(abs(__ts)) >= 1e11 THEN 1000000 ELSE 1000000000 END)"
	default:
		return fmt.Errorf("resample: unknown -time-unit %q (ns|us|ms|s)", timeUnit)
	}

	tsCol := quoteIdent(timeField)
	src := q.fromClause
	if src == "" {
		return fmt.Errorf("resample: no source to translate")
	}

	var sb strings.Builder
	sb.WriteString("(\n  WITH __base AS (SELECT * FROM " + src + " WHERE " + tsCol + " IS NOT NULL),\n")
	// step in SOURCE units; error() if -every is finer than the unit
	// (sub-unit grids are unrepresentable in source-unit integers).
	if timeUnit != "" {
		// Pinned unit: a bare one-row CTE. (Deriving it FROM __base
		// without an aggregate yields one row PER BASE ROW and the
		// cross-join multiplies the grid — caught by the linear
		// equivalence case.)
		sb.WriteString("  __unit AS (SELECT " + unitExpr + " AS u),\n")
	} else {
		sb.WriteString(fmt.Sprintf("  __unit AS (SELECT %s AS u FROM (SELECT CAST(%s AS BIGINT) AS __ts FROM __base)),\n",
			unitExpr, tsCol))
	}
	sb.WriteString(fmt.Sprintf("  __step AS (SELECT CASE WHEN %d %% u != 0 THEN CAST(error('resample: -every is finer than the epoch unit — use generate go') AS BIGINT) ELSE %d // u END AS s FROM __unit),\n",
		everyNs, everyNs))
	sb.WriteString(fmt.Sprintf("  __mm AS (SELECT min(%s) AS mn, max(%s) AS mx FROM __base),\n", tsCol, tsCol))
	sb.WriteString("  __bounds AS (SELECT CAST(floor(mn * 1.0 / s) * s AS BIGINT) AS lo, CAST(floor(mx * 1.0 / s) * s AS BIGINT) AS hi, s FROM __mm, __step),\n")
	sb.WriteString("  __grid AS (SELECT __g FROM __bounds, generate_series(lo, hi, s) __t(__g)),\n")
	for i, v := range values {
		sb.WriteString(fmt.Sprintf("  __s%d AS (SELECT CAST(%s AS BIGINT) AS __ts, max(CAST(%s AS DOUBLE)) AS v FROM __base WHERE %s IS NOT NULL GROUP BY __ts),\n",
			i, tsCol, quoteIdent(v), quoteIdent(v)))
	}
	sb.WriteString("  __out AS (\n    SELECT __grid.__g AS " + tsCol)
	for i, v := range values {
		vc := quoteIdent(v)
		switch fill {
		case "previous":
			sb.WriteString(fmt.Sprintf(",\n      COALESCE(p%d.v, (SELECT v FROM __s%d ORDER BY __ts LIMIT 1)) AS %s", i, i, vc))
		case "next":
			sb.WriteString(fmt.Sprintf(",\n      COALESCE(n%d.v, (SELECT v FROM __s%d ORDER BY __ts DESC LIMIT 1)) AS %s", i, i, vc))
		case "linear":
			sb.WriteString(fmt.Sprintf(`,
      CASE
        WHEN p%d.__ts = __grid.__g THEN p%d.v
        WHEN p%d.__ts IS NOT NULL AND n%d.__ts IS NOT NULL THEN p%d.v + (__grid.__g - p%d.__ts) * (n%d.v - p%d.v) / CAST(n%d.__ts - p%d.__ts AS DOUBLE)
        WHEN p%d.__ts IS NULL THEN n%d.v
        ELSE p%d.v
      END AS %s`, i, i, i, i, i, i, i, i, i, i, i, i, i, vc))
		default:
			return fmt.Errorf("resample: unknown -fill %q (previous|next|linear)", fill)
		}
	}
	sb.WriteString("\n    FROM __grid")
	for i := range values {
		switch fill {
		case "previous":
			sb.WriteString(fmt.Sprintf("\n    ASOF LEFT JOIN __s%d p%d ON __grid.__g >= p%d.__ts", i, i, i))
		case "next":
			sb.WriteString(fmt.Sprintf("\n    ASOF LEFT JOIN __s%d n%d ON __grid.__g <= n%d.__ts", i, i, i))
		case "linear":
			sb.WriteString(fmt.Sprintf("\n    ASOF LEFT JOIN __s%d p%d ON __grid.__g >= p%d.__ts", i, i, i))
			sb.WriteString(fmt.Sprintf("\n    ASOF LEFT JOIN __s%d n%d ON __grid.__g <= n%d.__ts", i, i, i))
		}
	}
	sb.WriteString("\n  )\n  SELECT * FROM __out ORDER BY " + tsCol + "\n)")

	newCols := append([]string{timeField}, values...)
	*q = sqlQuery{
		fromClause: sb.String(),
		comments:   q.comments,
		columns:    newCols,
	}
	return nil
}
