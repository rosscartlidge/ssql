package commands

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerGenerateSQL registers the "generate sql" subcommand
func registerGenerateSQL(cmd *cf.SubcommandBuilder) {
	sub := cmd.Subcommand("sql").
		Description("Generate SQL (DuckDB, PostgreSQL or DataFusion dialect) from an ssql CLI pipeline").
		Example("(export SSQL_MODE=record; ssql from data.csv | ssql where -if age gt 25 | ssql to table) | ssql generate sql", "Generate SQL from pipeline").
		Example("(export SSQL_MODE=record; ssql from data.parquet | ssql group-by dept -sum salary total | ssql to table) | ssql generate sql", "Parquet aggregation query").
		Example("(export SSQL_MODE=record; ssql from data.csv | ssql where -if age gt 25 | ssql to table) | ssql generate sql -run", "Generate and execute with DuckDB").
		Example("ssql generate sql -run -pipeline 'ssql from data.csv | ssql group-by dept -sum salary total | ssql to table'", "One-shot: translate the quoted pipeline and execute with DuckDB").
		Example("ssql generate sql -dialect postgres -pipeline 'ssql from data.csv | ssql group-by dept -median salary med | ssql to table'", "Postgres spellings (percentile_cont WITHIN GROUP) plus a CREATE TABLE + \\copy prologue for the source").
		Flag("-run", "-r").
		Bool().
		Global().
		Default(false).
		Help("Execute the generated SQL with the dialect's CLI: duckdb, psql (honours PGHOST/PGDATABASE/…, loads the sources into TEMP tables first) or datafusion-cli").
		Done().
		Flag("-dialect", "-d").
		String().
		Global().
		Default("duckdb").
		Completer(&cf.StaticCompleter{Options: sqlDialects}).
		Help("Target engine: duckdb (default), postgres or datafusion. Same pipeline semantics; engine-specific spellings, and stages with no translation in that engine are refused loudly").
		Done().
		Flag("-pipeline", "-p").
		String().
		Global().
		Default("").
		Help("Run PIPELINE (a quoted ssql pipeline string) in record mode and translate its fragments — no export/subshell ceremony needed.").
		Done()
	jsonDocFlag(sub, "translate").
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
			dialect := dialectDuckDB
			if v, ok := ctx.GlobalFlags["-dialect"]; ok {
				var err error
				if dialect, err = parseSQLDialect(v.(string)); err != nil {
					return err
				}
			}

			// SQL translation reads record-mode fragments, so the mode
			// is fixed; -pipeline (shell) or -json (no shell) name the
			// pipeline, else the fragments arrive on stdin.
			fragSrc, err := generateFragmentSource(ctx, "record", "sql")
			if err != nil {
				return err
			}
			// The dialect stays set through -run so the engine command
			// and the Postgres load script see it.
			prevDialect, prevLoads := sqlDialectCur, pgLoads
			sqlDialectCur, pgLoads = dialect, nil
			defer func() { sqlDialectCur, pgLoads = prevDialect, prevLoads }()
			sql, err := assembleSQL(fragSrc)
			if err != nil {
				return fmt.Errorf("assembling SQL: %w", err)
			}

			if run {
				name, args, stdin := sqlRunCommand(sql)
				if _, err := exec.LookPath(name); err != nil {
					return fmt.Errorf("generate sql -run -dialect %s needs %s on PATH: %w", dialect, name, err)
				}
				cmd := exec.Command(name, args...)
				if stdin != "" {
					cmd.Stdin = strings.NewReader(stdin)
				}
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
	csvSource    string   // DuckDB: the single CSV/TSV file FROM reads, for from -last's ordered re-read
	csvDelim     byte

	// columns tracks the current output field names, seeded from the source
	// CSV/TSV header and advanced by each stage's schemaOp (the same rules
	// pipeline-aware completion uses). nil = unknown; translation then falls
	// back to assuming referenced columns exist.
	columns []string
}

// hasClauses reports whether anything beyond the bare FROM has accumulated
// — i.e. whether a stage that rebuilds the query around its source must
// materialise the current query as that source first.
func (q *sqlQuery) hasClauses() bool {
	return len(q.whereClauses) > 0 || len(q.joins) > 0 || len(q.selectExprs) > 0 ||
		len(q.groupBy) > 0 || len(q.orderBy) > 0 || q.limit != "" || q.offset != "" || q.distinct || q.sampled
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
		fromClause: sqlSubquery("(\n" + indentLines(sub, "  ") + "\n)"),
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
	sqlTimeColumns = map[string]bool{} // per assembly
	sqlColumnKinds = map[string]string{}

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

// collapseArgFlag rewrites the flag form of a positional (`-arg VALUE`,
// autocli's ArgFlag, DFC134 §5.2) to the bare form the translators below
// understand. They tell positionals from flags by a leading dash, which is
// exactly the reading -arg exists to prevent, so a VALUE that would be
// misread (leading - or +, or a clause separator) is REFUSED rather than
// silently dropped from the query. Expressing those needs the translators
// to take parsed positionals from the Op instead of re-reading argv (DFC115
// legacy exception; TODO.md).
func collapseArgFlag(args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] != cf.ArgFlag || i+1 >= len(args) {
			out = append(out, args[i])
			continue
		}
		v := args[i+1]
		if strings.HasPrefix(v, "-") || strings.HasPrefix(v, "+") {
			return nil, fmt.Errorf("generate sql cannot yet express the argument %q given with %s (a name beginning with - or +); "+
				"generate go and direct execution handle it", v, cf.ArgFlag)
		}
		out = append(out, v)
		i++
	}
	return out, nil
}

func translateFragment(q *sqlQuery, frag *lib.CodeFragment, funcFrags []*lib.CodeFragment) error {
	if frag.Command == "" {
		return nil // skip empty commands (e.g., Aggregate fragment from group-by)
	}

	args, err := collapseArgFlag(stageArgs(frag))
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return nil
	}

	// args[0] is "ssql", args[1] is the command
	name := args[1]
	if needsWrap(q, name) {
		wrapAsSubquery(q)
	}
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
	case "describe":
		err = translateDescribe(q, frag.Op, args[2:])
	case "unpivot":
		err = translateUnpivot(q, frag.Op, args[2:])
	case "fill":
		err = translateFill(q, frag.Op, args[2:])
	case "extract":
		err = translateExtract(q, frag.Op, args[2:])
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
	var sampleN, lastN string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-sample-seed":
			return fmt.Errorf("from -sample-seed has no SQL equivalent — DuckDB cannot reproduce ssql's seeded byte-offset selection; drop -sample-seed or use generate go")
		case "-sample":
			if i+1 < len(args) {
				sampleN = args[i+1]
			}
		case "-last":
			if i+1 < len(args) {
				lastN = args[i+1]
			}
		}
	}

	// Handle format subcommands: from csv FILE, from parquet FILE, etc.
	switch args[0] {
	case "lines":
		// One VARCHAR column per line, numbered in file order
		// (parallel=false keeps read_csv's row order deterministic).
		var file string
		for _, a := range args[1:] {
			if !strings.HasPrefix(a, "-") {
				file = a
				break
			}
		}
		if file == "" {
			return fmt.Errorf("from lines: SQL translation needs a file (stdin has no SQL equivalent)")
		}
		if sqlDialectCur != dialectDuckDB {
			return dialectRefuse("from lines", "needs DuckDB's read_csv to number lines in file order")
		}
		q.fromClause = fmt.Sprintf("(SELECT row_number() OVER () AS line_number, line FROM read_csv(%s, columns={'line': 'VARCHAR'}, header=false, delim='\\x01', quote='', escape='', parallel=false))", quoteFile(file))
		q.columns = []string{"line_number", "line"}
		return nil
	case "csv", "tsv", "json", "jsonl", "arrow", "parquet", "xlsx":
		if len(args) < 2 {
			return fmt.Errorf("from %s requires a file argument", args[0])
		}
		// Collect file paths — skip flags AND the arguments of
		// argument-taking flags, by arity (a bare `-sample 5` used to read
		// '5' as a second file; `-type date time` read 'time' as one until
		// 2026-09-22, because this table said -type takes one argument).
		// This is a copy of from's grammar (DFC115 legacy exception).
		flagArity := map[string]int{
			"-sample": 1, "-sample-seed": 1, "-last": 1, "-type": 2, "-t": 2,
			"-default-type": 1, "-dt": 1, "-source": 1, cf.ArgFlag: 1,
		}
		var files []string
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			if a == "--" {
				break
			}
			if a == cf.ArgFlag && i+1 < len(rest) {
				files = append(files, rest[i+1])
				i++
				continue
			}
			if strings.HasPrefix(a, "-") {
				i += flagArity[a]
				continue
			}
			files = append(files, a)
		}
		if len(files) == 1 {
			src, err := dialectSource(files[0])
			if err != nil {
				return err
			}
			q.fromClause = src
			// Seed column tracking from the header (generation runs where
			// the file lives). Non-delimited formats stay unknown.
			switch {
			case args[0] == "csv", args[0] != "tsv" && strings.HasSuffix(strings.ToLower(files[0]), ".csv"):
				q.columns = delimHeader(files[0], ',')
				seedColumnKinds(files[0], ',')
				q.csvSource, q.csvDelim = files[0], ','
			case args[0] == "tsv", strings.HasSuffix(strings.ToLower(files[0]), ".tsv"):
				q.columns = delimHeader(files[0], '\t')
				seedColumnKinds(files[0], '\t')
				q.csvSource, q.csvDelim = files[0], '\t'
			}
			if q.csvSource != "" && sqlDialectCur == dialectDuckDB {
				q.fromClause = duckReadCSV([]string{q.csvSource}, q.csvDelim, "")
			}
		} else if sqlDialectCur == dialectDuckDB {
			delim := byte(',')
			if args[0] == "tsv" || strings.HasSuffix(strings.ToLower(files[0]), ".tsv") {
				delim = '\t'
			}
			q.fromClause = duckReadCSV(files, delim, "")
		} else {
			// Postgres/DataFusion: one source per file, UNION ALL (the
			// files must share a header, as ssql's own multi-file read
			// assumes).
			var parts []string
			for _, f := range files {
				src, err := dialectSource(f)
				if err != nil {
					return err
				}
				parts = append(parts, "SELECT * FROM "+src)
			}
			q.fromClause = sqlSubquery("(" + strings.Join(parts, " UNION ALL ") + ")")
		}
	case "ssh":
		return fmt.Errorf("from ssh has no SQL equivalent — it is an ssql-specific distributed feature")
	case "catalog":
		return fmt.Errorf("from catalog has no SQL equivalent — it is an ssql-specific distributed feature")
	default:
		// Bare: from FILE — seed column tracking from the header as the
		// explicit forms do, so `update -set-expr NEW …` becomes an added
		// column, not a REPLACE of one that does not exist.
		src, err := dialectSource(args[0])
		if err != nil {
			return err
		}
		q.fromClause = src
		switch lower := strings.ToLower(args[0]); {
		case strings.HasSuffix(lower, ".csv"):
			q.columns = delimHeader(args[0], ',')
			seedColumnKinds(args[0], ',')
		case strings.HasSuffix(lower, ".tsv"):
			q.columns = delimHeader(args[0], '\t')
			seedColumnKinds(args[0], '\t')
		}
	}
	if sampleN != "" && sampleN != "0" {
		// reservoir, not system: DuckDB's system sampling is
		// percentage-only. Both sides are unseeded statistical samples
		// of N rows; the duckdb equivalence lane asserts cardinality.
		q.fromClause = sqlSampleRows(q.fromClause, sampleN)
		q.sampled = true
	}
	if lastN != "" && lastN != "0" {
		if sqlDialectCur != dialectDuckDB {
			return dialectRefuse("from -last", "needs DuckDB's ordered read_csv(parallel=false); other engines define no file order")
		}
		// -last N: the last N rows in FILE order. SQL cannot seek, so
		// this is the ordered full read + reversed LIMIT, re-ordered —
		// correct, not fast (the speed win is a Go-lane property).
		// Needs a deterministic row order: read_csv(parallel=false) for
		// a single CSV/TSV file; anything else has no ordered read.
		if q.csvSource == "" {
			return fmt.Errorf("from -last has a SQL translation only for a single CSV/TSV file (file order is undefined for other sources); use generate go")
		}
		src := duckReadCSV([]string{q.csvSource}, q.csvDelim, "parallel=false")
		q.fromClause = fmt.Sprintf("(SELECT * EXCLUDE (__rn) FROM (SELECT * FROM (SELECT *, row_number() OVER () AS __rn FROM %s) ORDER BY __rn DESC LIMIT %s) ORDER BY __rn)", src, lastN)
	}
	return nil
}

func translateWhere(q *sqlQuery, args []string) error {
	// Parse -if and -if-expr conditions
	// Multiple -if within one clause are AND; clauses separated by + are OR
	var orGroups []string
	var currentAnd []string
	currentNot, invert := false, false
	closeGroup := func() {
		if len(currentAnd) == 0 {
			return
		}
		group := strings.Join(currentAnd, " AND ")
		if currentNot {
			group = sqlNot(group)
		}
		orGroups = append(orGroups, group)
		currentAnd = nil
		currentNot = false
	}

	clauseParams, err := sqlClauseParams(args, "+")
	if err != nil {
		return fmt.Errorf("where: %w", err)
	}
	clauseIdx := 0
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-not":
			currentNot = true
			i++
		case "-invert":
			invert = true
			i++
		case "-param", "-p":
			i += 4 // read by sqlClauseParams
		case "-if", "-i", "+if", "+i":
			if i+3 >= len(args) {
				return fmt.Errorf("incomplete -if condition")
			}
			field, op, value := args[i+1], args[i+2], args[i+3]
			cond := translateCondition(field, op, value)
			if args[i][0] == '+' {
				cond = sqlNot(cond)
			}
			currentAnd = append(currentAnd, cond)
			i += 4
		case "-if-expr", "-x", "+if-expr", "+x":
			if i+1 >= len(args) {
				return fmt.Errorf("incomplete -if-expr")
			}
			cond, err := exprToSQLParams(args[i+1], clauseParams[clauseIdx])
			if err != nil {
				return fmt.Errorf("where -if-expr: %w", err)
			}
			if args[i][0] == '+' {
				cond = sqlNot(cond)
			}
			currentAnd = append(currentAnd, cond)
			i += 2
		case "+":
			// OR separator between clauses
			closeGroup()
			clauseIdx++
			i++
		default:
			i++
		}
	}
	closeGroup()

	if invert {
		// -invert/-v: the complement of the whole filter (grep -v). No
		// clauses = match everything, so the complement matches nothing.
		if len(orGroups) == 0 {
			q.whereClauses = append(q.whereClauses, "FALSE")
			return nil
		}
		wrapped := make([]string, len(orGroups))
		for k, g := range orGroups {
			wrapped[k] = "(" + g + ")"
		}
		q.whereClauses = append(q.whereClauses, sqlNot(strings.Join(wrapped, " OR ")))
		return nil
	}

	if len(orGroups) == 1 {
		q.whereClauses = append(q.whereClauses, orGroups[0])
	} else if len(orGroups) > 1 {
		wrapped := make([]string, len(orGroups))
		for i, g := range orGroups {
			wrapped[i] = "(" + g + ")"
		}
		// whereClauses are ANDed at render time (one entry per where
		// stage), so an OR of clauses must be one parenthesised term:
		// `where A + B | where C` is (A OR B) AND C, not A OR (B AND C).
		q.whereClauses = append(q.whereClauses, "("+strings.Join(wrapped, " OR ")+")")
	}

	return nil
}

// sqlNot negates a condition the way ssql does. In ssql a condition on an
// absent value is simply false, so its negation is TRUE: `where +if n ge
// 0` keeps a row with no n. SQL's NOT over a NULL comparison is NULL and
// the row is dropped. COALESCE(…, FALSE) first makes the inner condition
// two-valued — "false when unknown", which is exactly exec's `exists &&
// op` — and then NOT means what it says (DFC128 §6g).
func sqlNot(cond string) string {
	return "NOT COALESCE((" + cond + "), FALSE)"
}

func translateCondition(field, op, value string) string {
	sqlOp := sqlOperator(op)
	if sqlOp == "LIKE" {
		// contains, startswith, endswith
		switch op {
		case "contains":
			return fmt.Sprintf("%s LIKE '%%%s%%' ESCAPE '\\'", quoteIdent(field), escapeLike(value))
		case "startswith":
			return fmt.Sprintf("%s LIKE '%s%%' ESCAPE '\\'", quoteIdent(field), escapeLike(value))
		case "endswith":
			return fmt.Sprintf("%s LIKE '%%%s' ESCAPE '\\'", quoteIdent(field), escapeLike(value))
		}
	}
	if op == "regex" {
		return sqlRegexMatch(quoteIdent(field), "'"+escapeSQL(value)+"'")
	}
	return fmt.Sprintf("%s %s %s", quoteIdent(field), sqlOp, sqlLiteralFor(field, value))
}

// sqlLiteralFor renders a value token for comparison with, or assignment
// to, FIELD. When the assembler knows the column's kind (sampled from the
// source CSV/TSV, updated by cast) the COLUMN decides: a text column gets
// a quoted literal whatever the token looks like — `where -if zip eq
// 02134`, or `s eq 12` on a column that also holds "abc", rendered `s =
// 12` and DuckDB refused to cast the column (DFC133 random differential).
// Unknown column → the token's own look, as before.
func sqlLiteralFor(field, value string) string {
	if sqlColumnKinds[field] == "string" {
		return "'" + escapeSQL(value) + "'"
	}
	return sqlLiteral(value)
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

// sqlAgg is one group-by aggregation: the SQL expression over the raw
// rows and the ssql result name.
type sqlAgg struct {
	expr string // e.g. COUNT(*), SUM("amount")
	name string // unquoted ssql field name
}

func translateGroupBy(q *sqlQuery, args []string) error {
	// Parse: group-by FIELD [FIELD...] [-count name] [-sum field name] [-avg field name] etc.
	i := 0

	// Collect group-by fields (positional args before first flag)
	var fields []string
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		fields = append(fields, args[i])
		i++
	}

	// Parse aggregation flags
	var aggs []sqlAgg
	rollup, cube := false, false
	for i < len(args) {
		// Built-in aggregates come from the registry (aggDefs, DFC129 §6):
		// the def renders its own SQL from the quoted field and the extra
		// argument; the result name is always the flag's last argument.
		if d, ok := aggDefByFlag(args[i]); ok {
			n := d.arity()
			if i+n < len(args) {
				if err := aggDialectRefusal(d.fn, false, false); err != nil {
					return err
				}
				qf, extra := "", ""
				if d.hasField {
					qf = quoteIdent(args[i+1])
				}
				if d.extraArg != "" {
					extra = args[i+2]
				}
				aggs = append(aggs, sqlAgg{d.sql(qf, extra), args[i+n]})
				// The result column's kind, from the registry's own wire-type
				// rule (an extreme of a text column is text; a count is an
				// int) — so a later literal against it is typed by the column
				// (DFC133). Unknown input kind on a type-preserving aggregate
				// → leave the result unknown rather than guess.
				in := ""
				if d.hasField {
					in = sqlColumnKinds[args[i+1]]
				}
				if wt := d.wireType(in); !(d.hasField && in == "" && wt != d.wireType("string")) {
					sqlColumnKinds[args[i+n]] = wt
				} else {
					delete(sqlColumnKinds, args[i+n])
				}
				i += n + 1
			} else {
				i++
			}
			continue
		}
		switch args[i] {
		// Silently dropping an aggregation would produce wrong results —
		// fail loudly on the forms with no SQL translation (yet).
		case "-expr", "-e":
			return fmt.Errorf("group-by -expr has no SQL translation (expression aggregations are ssql-specific)")
		case "-stream-expr":
			return fmt.Errorf("group-by -stream-expr has no SQL translation (expression aggregations are ssql-specific)")
		case "-rollup":
			rollup = true
			i++
		case "-cube":
			cube = true
			i++
		default:
			i++
		}
	}

	if rollup || cube {
		if rollup && cube {
			return fmt.Errorf("cannot use both -rollup and -cube; choose one")
		}
		if len(fields) == 0 || len(aggs) == 0 {
			return fmt.Errorf("-rollup/-cube requires at least one group-by field and one aggregation")
		}
		mode := ssql.RollupHierarchical
		if cube {
			mode = ssql.RollupCube
		}
		return translateGroupByRollup(q, fields, aggs, mode)
	}

	for _, f := range fields {
		q.groupBy = append(q.groupBy, quoteIdent(f))
		q.selectExprs = append(q.selectExprs, quoteIdent(f))
	}
	for _, a := range aggs {
		q.selectExprs = append(q.selectExprs, fmt.Sprintf("%s AS %s", a.expr, quoteIdent(a.name)))
	}
	return nil
}

// translateGroupByRollup renders ssql's -rollup/-cube: NOT SQL's GROUP BY
// ROLLUP (which adds subtotal ROWS) but exec's enrichment — one row per
// detail group (all fields) carrying every grouping set's aggregates as
// prefixed COLUMNS (ssql.RollupFieldPrefix: "" for the grand total,
// "a_" for (a), "a_b_" for (a, b)). Each set is aggregated over the raw
// rows exactly as exec does, so every aggregate kind (AVG, MIN, LIST…)
// is exact — a window-function rewrite would only be right for
// COUNT/SUM/MIN/MAX. The sets and the naming rule come from the root
// package (RollupGroupingSets / RollupFieldPrefix), never a second copy.
//
// Shape (rollup over a, b with -count n):
//
//	(WITH __src AS (SELECT * FROM <source>)
//	 SELECT __d."a", __d."b", __s0."n", __s1."a_n", __d."a_b_n"
//	 FROM   (SELECT "a", "b", COUNT(*) AS "a_b_n" FROM __src GROUP BY "a", "b") AS __d
//	 JOIN   (SELECT COUNT(*) AS "n" FROM __src) AS __s0 ON TRUE
//	 JOIN   (SELECT "a", COUNT(*) AS "a_n" FROM __src GROUP BY "a") AS __s1
//	          ON (__d."a" IS NOT DISTINCT FROM __s1."a"))
//
// IS NOT DISTINCT FROM keeps NULL group keys matched, as exec's %v-keyed
// grouping does. The result replaces the query as its FROM, so later
// stages apply to the enriched rows.
func translateGroupByRollup(q *sqlQuery, fields []string, aggs []sqlAgg, mode ssql.RollupMode) error {
	// Materialise whatever is accumulated (a WHERE, a join, a sample…)
	// as the source: the sets must all aggregate the same rows.
	if q.hasClauses() {
		wrapAsSubquery(q)
	}
	src := q.fromClause
	if src == "" {
		return fmt.Errorf("group-by -rollup/-cube: no source to translate")
	}

	sets := ssql.RollupGroupingSets(fields, mode)
	detail := len(sets) - 1 // the full field set is always last
	alias := func(i int) string {
		if i == detail {
			return "__d"
		}
		return fmt.Sprintf("__s%d", i)
	}

	var sb strings.Builder
	sb.WriteString("(\n  WITH __src AS (SELECT * FROM " + src + ")\n  SELECT ")
	var sel []string
	for _, f := range fields {
		sel = append(sel, "__d."+quoteIdent(f))
	}
	for i, set := range sets {
		prefix := ssql.RollupFieldPrefix(set)
		for _, a := range aggs {
			sel = append(sel, alias(i)+"."+quoteIdent(prefix+a.name))
		}
	}
	sb.WriteString(strings.Join(sel, ", ") + "\n")

	setSelect := func(set []string) string {
		var cols []string
		for _, f := range set {
			cols = append(cols, quoteIdent(f))
		}
		prefix := ssql.RollupFieldPrefix(set)
		for _, a := range aggs {
			cols = append(cols, fmt.Sprintf("%s AS %s", a.expr, quoteIdent(prefix+a.name)))
		}
		sql := "SELECT " + strings.Join(cols, ", ") + " FROM __src"
		if len(set) > 0 {
			var g []string
			for _, f := range set {
				g = append(g, quoteIdent(f))
			}
			sql += " GROUP BY " + strings.Join(g, ", ")
		}
		return sql
	}

	sb.WriteString("  FROM (" + setSelect(sets[detail]) + ") AS __d\n")
	for i, set := range sets {
		if i == detail {
			continue
		}
		on := "TRUE"
		if len(set) > 0 {
			var conds []string
			for _, f := range set {
				// Parenthesised: DataFusion parses `a IS NOT DISTINCT FROM b AND c`
				// as `a IS NOT DISTINCT FROM (b AND c)` and fails to type it
				// (DFC132); DuckDB and Postgres accept either form.
				conds = append(conds, fmt.Sprintf("(__d.%s IS NOT DISTINCT FROM %s.%s)", quoteIdent(f), alias(i), quoteIdent(f)))
			}
			on = strings.Join(conds, " AND ")
		}
		sb.WriteString("  JOIN (" + setSelect(set) + ") AS " + alias(i) + " ON " + on + "\n")
	}
	sb.WriteString(")")

	*q = sqlQuery{fromClause: sqlSubquery(sb.String()), comments: q.comments}
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
	// A bare `sample` (pass-through) emits no fragment, so it never
	// reaches here; `sample 0` is a real stage: USING SAMPLE 0 ROWS.
	switch {
	case percent != "":
		q.fromClause = sqlSamplePercent(q.fromClause, percent)
	case n != "":
		q.fromClause = sqlSampleRows(q.fromClause, n)
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
			q.joins = append(q.joins, fmt.Sprintf("JOIN %s %s", sqlSubquery("("+subquery+")"), joinCond))
			return nil
		}
	}

	src, err := dialectSource(filePath)
	if err != nil {
		return err
	}
	q.joins = append(q.joins, fmt.Sprintf("JOIN %s %s", src, joinCond))
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
		fromClause: sqlSubquery("(\n" + indentLines(strings.Join(parts, "\n"+op+"\n"), "  ") + "\n)"),
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
		args, err := collapseArgFlag(stageArgs(bodyFrag))
		if err != nil {
			return "" // the caller reports a side it cannot build
		}
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
		rangeP      string // RANGE frame bounds as typed (DFC130 unit 3); "" = ROWS frame
		rangeF      string
		funcs       []string // pre-built SQL function expressions like "ROW_NUMBER() AS rn"
		registryFns []string // registry aggregate names used, for the dialect check once the frame is known
	}

	clauses := []windowClause{{preceding: -1, following: 0}}
	cur := &clauses[0]

	i := 0
	for i < len(args) {
		// Registry aggregates over the frame (DFC130 unit 2): FIELD [EXTRA] RESULT.
		if d, ok := isWindowRegistryFlag(args[i]); ok {
			n := d.arity()
			if i+n < len(args) {
				extra := ""
				if d.extraArg != "" {
					extra = args[i+2]
				}
				cur.registryFns = append(cur.registryFns, d.fn)
				cur.funcs = append(cur.funcs, fmt.Sprintf("%s AS %s", d.sql(quoteIdent(args[i+1]), extra), quoteIdent(args[i+n])))
				i += n + 1
			} else {
				i++
			}
			continue
		}
		switch args[i] {
		case "+", "-":
			// "+" is autocli's clause separator (a bare "-" is accepted for
			// older pipelines). Until v4.100.0 only "-" was recognised, so a
			// two-clause window collapsed into one and the second clause's
			// -desc reordered the first (found by the DFC130 unit-0 gate).
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
		case "+desc", "+d":
			cur.desc = false // autocli's negated bool: ascending
			i++
		case "-preceding":
			if i+1 < len(args) {
				cur.preceding, _ = strconv.Atoi(args[i+1])
				i += 2
			} else {
				i++
			}
		case "-range-preceding":
			if i+1 < len(args) {
				cur.rangeP = args[i+1]
				i += 2
			} else {
				i++
			}
		case "-range-following":
			if i+1 < len(args) {
				cur.rangeF = args[i+1]
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
		case "-cume-dist":
			if i+1 < len(args) {
				cur.funcs = append(cur.funcs, fmt.Sprintf("CUME_DIST() AS %s", quoteIdent(args[i+1])))
				i += 2
			} else {
				i++
			}
		case "-count-field":
			if i+2 < len(args) {
				cur.funcs = append(cur.funcs, fmt.Sprintf("COUNT(%s) AS %s", quoteIdent(args[i+1]), quoteIdent(args[i+2])))
				i += 3
			} else {
				i++
			}
		case "-nth-value":
			if i+3 < len(args) {
				cur.funcs = append(cur.funcs, fmt.Sprintf("NTH_VALUE(%s, %s) AS %s", quoteIdent(args[i+1]), args[i+2], quoteIdent(args[i+3])))
				i += 4
			} else {
				i++
			}
		// 4-arg: -lag-default/-lead-default field n default result
		case "-lag-default", "-lead-default":
			if i+4 < len(args) {
				sqlFunc := strings.ToUpper(strings.TrimSuffix(args[i][1:], "-default"))
				cur.funcs = append(cur.funcs, fmt.Sprintf("%s(%s, %s, %s) AS %s", sqlFunc, quoteIdent(args[i+1]), args[i+2], sqlTypedLiteral(windowDefaultLiteral(args[i+3])), quoteIdent(args[i+4])))
				i += 5
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
		// The frame is known only now: a bounded start (N PRECEDING or a
		// finite RANGE bound) is what some engines cannot slide over.
		bounded := c.preceding >= 0 || (c.rangeP != "" && c.rangeP != "unbounded")
		for _, fn := range c.registryFns {
			if err := aggDialectRefusal(fn, true, bounded); err != nil {
				return err
			}
		}
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
		if c.rangeP != "" || c.rangeF != "" {
			var err error
			if frameSQL, err = buildRangeFrameSQL(c.rangeP, c.rangeF); err != nil {
				return err
			}
		}
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

// sqlTypedLiteral renders a typed CLI literal (windowDefaultLiteral) as
// SQL: numbers and booleans bare, strings quoted.
func sqlTypedLiteral(v any) string {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strings.ToUpper(strconv.FormatBool(x))
	case string:
		return sqlStringLiteral(x)
	}
	return fmt.Sprint(v)
}

// buildRangeFrameSQL renders a RANGE frame from the CLI's bounds: numbers
// bare, durations as INTERVAL 'N seconds' (DuckDB and Postgres both accept
// it against a TIMESTAMP or DATE order column), "unbounded" and 0 as
// UNBOUNDED / CURRENT ROW.
func buildRangeFrameSQL(rangeP, rangeF string) (string, error) {
	bound := func(s, side string) (string, error) {
		if s == "" {
			return "CURRENT ROW", nil
		}
		v, isTime, err := parseRangeBound(s)
		if err != nil {
			return "", fmt.Errorf("window -range-%s: %w", side, err)
		}
		switch {
		case v < 0:
			return "UNBOUNDED " + strings.ToUpper(side), nil
		case v == 0:
			return "CURRENT ROW", nil
		case isTime:
			return fmt.Sprintf("INTERVAL '%s seconds' %s", strconv.FormatFloat(v, 'f', -1, 64), strings.ToUpper(side)), nil
		}
		return fmt.Sprintf("%s %s", strconv.FormatFloat(v, 'f', -1, 64), strings.ToUpper(side)), nil
	}
	start, err := bound(rangeP, "preceding")
	if err != nil {
		return "", err
	}
	end, err := bound(rangeF, "following")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("RANGE BETWEEN %s AND %s", start, end), nil
}

func buildFrameSQL(preceding, following int) string {
	// ssql's default frame is ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT
	// ROW. SQL's default with an ORDER BY is RANGE … CURRENT ROW, which
	// includes the current row's PEERS (rows tied on the order key) — so a
	// running sum or LAST_VALUE over tied order values differed between exec
	// and DuckDB until v4.100.0 (found by the DFC130 unit-0 gate). Always
	// render the frame explicitly.

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
	// DuckDB: SELECT * RENAME ("old" AS "new", ...); explicit column list
	// where the engine lacks the modifier (starProjection).
	var renames []sqlPair
	for i := 0; i < len(args); i++ {
		if args[i] == "-as" && i+2 < len(args) {
			renames = append(renames, sqlPair{args[i+1], args[i+2]})
			i += 2
		}
	}
	if len(renames) > 0 {
		sel, err := starProjection(q.columns, nil, nil, renames, "rename")
		if err != nil {
			return err
		}
		q.selectExprs = append(q.selectExprs, sel)
	}
	return nil
}

func translateCast(q *sqlQuery, args []string) error {
	// DuckDB: SELECT * REPLACE (CAST("field" AS TYPE) AS "field", ...)
	// -invalid missing: a value that cannot be converted becomes NULL —
	// TRY_CAST, where the engine has it. Plain CAST is already strict.
	castFn := "CAST"
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-invalid" && args[i+1] == "missing" {
			switch sqlDialectCur {
			case dialectDuckDB:
				castFn = "TRY_CAST"
			case dialectDataFusion:
				castFn = "try_cast"
			default:
				return dialectRefuse("cast -invalid missing", "Postgres has no TRY_CAST")
			}
		}
	}
	var replacements []sqlPair
	for i := 0; i < len(args); i++ {
		if args[i] == "-type" && i+2 < len(args) {
			field, typeName := args[i+1], args[i+2]
			sqlType := mapTypeToSQL(typeName)
			kindBefore := sqlColumnKinds[field] // the cast below updates it
			if ft, err := ssql.ParseFieldType(typeName); err == nil {
				sqlColumnKinds[field] = ft.String()
				if ft == ssql.FieldTypeTime {
					sqlTimeColumns[field] = true
				} else {
					delete(sqlTimeColumns, field)
				}
			}
			expr := fmt.Sprintf("%s(%s AS %s)", castFn, quoteIdent(field), sqlType)
			if ft, err := ssql.ParseFieldType(typeName); err == nil && ft == ssql.FieldTypeInt && kindBefore != "int" {
				// ssql's float → int TRUNCATES (2.9 → 2, as Go and pandas do);
				// SQL's CAST rounds (DuckDB and Postgres give 3). Truncate
				// explicitly — except from a column already known to be an
				// int, where a detour through DOUBLE would corrupt values
				// past 2^53 (DFC133: found re-testing cast).
				expr = fmt.Sprintf("%s(trunc(%s(%s AS %s)) AS %s)", castFn, castFn, quoteIdent(field), sqlFloatType(), sqlType)
			}
			replacements = append(replacements, sqlPair{field, expr})
			i += 2
		}
	}
	if len(replacements) > 0 {
		sel, err := starProjection(q.columns, nil, replacements, nil, "cast")
		if err != nil {
			return err
		}
		q.selectExprs = append(q.selectExprs, sel)
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
		conds    []string // AND conditions of the clause itself (own; no guards)
		clause   int      // clause index, for the first-match-wins guards
		field    string
		valueSQL string
	}
	var assignments []assignment
	var currentConds []string
	currentNot := false
	// clauseConds is the clause's condition group as the CASE arm sees
	// it: the AND of its conditions, or NOT (…) of that under -not.
	// update is first-match-wins: a clause applies only to rows no EARLIER
	// clause matched. So every arm carries NOT(earlier clause) for each
	// preceding conditional clause; an unconditional clause after those
	// (the else) becomes conditional on none of them having matched. The
	// CASE built per field below cannot express this on its own, because a
	// field set only in a later clause has no WHEN arm for the earlier ones.
	var clauseGroups []string // per clause index: its own condition group ANDed ("" = unconditional)
	clauseIdx := 0
	clauseConds := func() []string {
		if !currentNot || len(currentConds) == 0 {
			return append([]string{}, currentConds...)
		}
		return []string{sqlNot(strings.Join(currentConds, " AND "))}
	}
	closeClause := func() {
		clauseGroups = append(clauseGroups, strings.Join(clauseConds(), " AND "))
		currentConds = nil
		currentNot = false
		clauseIdx++
	}

	clauseParams, err := sqlClauseParams(args, "-")
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-not":
			currentNot = true
			i++
		case "-param", "-p":
			i += 4 // read by sqlClauseParams
		case "-if", "-i", "+if", "+i":
			if i+3 >= len(args) {
				return fmt.Errorf("incomplete -if condition in update")
			}
			cond := translateCondition(args[i+1], args[i+2], args[i+3])
			if args[i][0] == '+' {
				cond = sqlNot(cond) // ssql negation: true for an absent value (DFC128 §6g; update missed it — DFC133)
			}
			currentConds = append(currentConds, cond)
			i += 4
		case "-if-expr", "-x", "+if-expr", "+x":
			if i+1 >= len(args) {
				return fmt.Errorf("incomplete -if-expr in update")
			}
			cond, err := exprToSQLParams(args[i+1], clauseParams[clauseIdx])
			if err != nil {
				return fmt.Errorf("update -if-expr: %w", err)
			}
			if args[i][0] == '+' {
				cond = sqlNot(cond) // ssql negation: true for an absent value (DFC128 §6g; update missed it — DFC133)
			}
			currentConds = append(currentConds, cond)
			i += 2
		case "-set", "-s":
			if i+2 >= len(args) {
				return fmt.Errorf("incomplete -set in update")
			}
			assignments = append(assignments, assignment{
				conds:    clauseConds(),
				clause:   clauseIdx,
				field:    args[i+1],
				valueSQL: sqlLiteralFor(args[i+1], args[i+2]),
			})
			i += 3
		case "-set-expr", "-e":
			if i+2 >= len(args) {
				return fmt.Errorf("incomplete -set-expr in update")
			}
			valueSQL, err := exprToSQLParams(args[i+2], clauseParams[clauseIdx])
			if err != nil {
				return fmt.Errorf("update -set-expr: %w", err)
			}
			assignments = append(assignments, assignment{
				conds:    clauseConds(),
				clause:   clauseIdx,
				field:    args[i+1],
				valueSQL: valueSQL,
			})
			i += 3
		case "-set-bucket", "-b":
			// The flag spelling of -set-expr FIELD 'bucket(SOURCE, "WIDTH")'.
			if i+3 >= len(args) {
				return fmt.Errorf("incomplete -set-bucket in update")
			}
			se, err := bucketSetExpr(args[i+1], args[i+2], args[i+3])
			if err != nil {
				return err
			}
			valueSQL, err := exprToSQLParams(se.expression, clauseParams[clauseIdx])
			if err != nil {
				return fmt.Errorf("update -set-bucket: %w", err)
			}
			assignments = append(assignments, assignment{
				conds:    clauseConds(),
				clause:   clauseIdx,
				field:    se.field,
				valueSQL: valueSQL,
			})
			i += 4
		case "-":
			// Clause separator: the next clause applies only where this one did not
			closeClause()
			i++
		default:
			i++
		}
	}
	closeClause()

	// Group assignments by target field to build a single CASE expression per field
	fieldCases := make(map[string][]assignment)
	var fieldOrder []string
	for _, a := range assignments {
		if _, seen := fieldCases[a.field]; !seen {
			fieldOrder = append(fieldOrder, a.field)
		}
		fieldCases[a.field] = append(fieldCases[a.field], a)
	}

	var replacements []sqlPair
	var additions []string
	for _, field := range fieldOrder {
		cases := fieldCases[field]
		// `* REPLACE` requires the column to exist; a NEW field (exec creates
		// it) must be an added select expression instead. Only decidable when
		// column tracking is live — unknown schema assumes the column exists.
		isNew := q.columns != nil && !slices.Contains(q.columns, field)

		// Conditional sets become WHEN arms; an unconditional set becomes the
		// ELSE (last one wins). No conditionals at all → plain value, since
		// `CASE ELSE x END` (no WHEN) is a SQL syntax error.
		// A clause applies only to rows no EARLIER clause matched. CASE
		// gives that for free when every earlier clause has an arm for this
		// field; for an earlier conditional clause that does NOT set the
		// field, the arm carries NOT(that clause) explicitly, so a field set
		// only in a later or else clause is not set on rows an earlier
		// clause claimed.
		var whens []assignment
		elseSQL := quoteIdent(field) // default: preserve original value
		covered := map[int]bool{}
		for _, c := range cases {
			conds := append([]string{}, c.conds...)
			for k := 0; k < c.clause && k < len(clauseGroups); k++ {
				if !covered[k] && clauseGroups[k] != "" {
					conds = append(conds, sqlNot(clauseGroups[k]))
				}
			}
			covered[c.clause] = true
			c.conds = conds
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
			replacements = append(replacements, sqlPair{field, exprSQL})
		}
	}

	switch {
	case len(replacements) > 0:
		sel, err := starProjection(q.columns, nil, replacements, nil, "update")
		if err != nil {
			return err
		}
		q.selectExprs = append(q.selectExprs, sel)
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
		return sqlFloatType()
	case "string", "str", "text":
		return sqlStringType()
	case "bool", "boolean":
		return "BOOLEAN"
	case "time", "timestamp", "datetime":
		return "TIMESTAMP"
	case "date":
		return "DATE"
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
	// DuckDB and DataFusion: SELECT * EXCLUDE (col1, col2); Postgres
	// needs the surviving columns spelled out (starProjection).
	var cols []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			cols = append(cols, arg)
		}
	}
	if len(cols) > 0 {
		sel, err := starProjection(q.columns, cols, nil, nil, "exclude")
		if err != nil {
			return err
		}
		q.selectExprs = append(q.selectExprs, sel)
	}
	return nil
}

func renderSQL(q *sqlQuery) string {
	var sb strings.Builder

	// Comment with original pipeline
	if len(q.comments) > 0 {
		sb.WriteString(fmt.Sprintf("-- Generated by ssql generate sql (dialect: %s)\n", sqlDialectCur))
		sb.WriteString("-- Pipeline:\n")
		for _, c := range q.comments {
			sb.WriteString("--   " + c + "\n")
		}
		sb.WriteString("\n")
	}
	renderPGPrologue(&sb)

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

// duckReadCSV renders DuckDB's read_csv for files ssql wrote or read as
// RFC 4180 delimited text, with the dialect PINNED: quote and escape are
// the double quote and the delimiter is known. Left to its sniffer, DuckDB
// took an apostrophe in a cell (`'; DROP TABLE t; --`) as the quote
// character and merged two rows into one (found by the injection fuzz,
// 2026-09-22). Types are still inferred. extra is appended verbatim
// (`parallel=false` for an ordered read).
func duckReadCSV(files []string, delim byte, extra string) string {
	quoted := make([]string, len(files))
	for i, f := range files {
		quoted[i] = quoteFile(f)
	}
	src := quoted[0]
	if len(quoted) > 1 {
		src = "[" + strings.Join(quoted, ", ") + "]"
	}
	opts := `header=true, quote='"', escape='"', delim=','`
	if delim == '\t' {
		opts = `header=true, quote='"', escape='"', delim='\t'`
	}
	if extra != "" {
		opts += ", " + extra
	}
	return fmt.Sprintf("read_csv(%s, %s)", src, opts)
}

// sqlPlainIdent is a name every engine reads bare: ASCII letters, digits
// and _, not starting with a digit. Anything else is quoted.
var sqlPlainIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func quoteIdent(name string) string {
	// DuckDB: quote names that are not plain identifiers (a space, a dash,
	// a dot, a leading digit — `1st` bare is the literal 1 aliased st — a
	// non-ASCII letter) and reserved words. Postgres and DataFusion fold
	// unquoted identifiers to lower case, so a header like "Name" would
	// not resolve — quote everything there.
	if sqlDialectCur != dialectDuckDB || !sqlPlainIdent.MatchString(name) || isSQLReserved(name) {
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
	return name
}

func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// escapeLike renders a value for a LIKE pattern with ESCAPE '\': the
// backslash first (a literal one in the value must not escape what
// follows), then the two metacharacters, then SQL quoting. The ESCAPE
// clause is required: DuckDB has no default escape character, so without
// it `contains '%'` matched a literal backslash and nothing else (found
// by the injection fuzz, 2026-09-22).
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
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
	var current []byte
	inQuote, quoted := false, false

	// Bytes, not runes: the three characters that matter are ASCII, and a
	// rune loop rewrote any byte that is not valid UTF-8 (a Latin-1 file
	// name) as U+FFFD. `quoted` keeps an explicitly empty argument — `''`
	// used to vanish, shifting every argument after it (DFC133 fuzzing).
	for k := 0; k < len(cmd); k++ {
		c := cmd[k]
		switch {
		case c == '\'' && !inQuote:
			inQuote, quoted = true, true
		case c == '\'' && inQuote:
			inQuote = false
		case c == ' ' && !inQuote:
			if len(current) > 0 || quoted {
				args = append(args, string(current))
				current, quoted = current[:0], false
			}
		default:
			current = append(current, c)
		}
	}
	if len(current) > 0 || quoted {
		args = append(args, string(current))
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
	if sqlDialectCur != dialectDuckDB {
		return dialectRefuse("resample", "the grid + ASOF JOIN rewrite is DuckDB-specific")
	}
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
	// tsNum is the timestamp as the integer the grid arithmetic runs on;
	// gridOut turns a grid point back into the column's own family. A
	// numeric epoch column is its own integer. A column an upstream `cast
	// -type F time` made a TIMESTAMP (DFC128 D1) goes through epoch
	// MICROSECONDS — the engine's resolution — with the unit pinned, and
	// comes back as a TIMESTAMP: time in, time out, same epoch grid as
	// every other lane. (-time-unit describes numeric epochs; it has no
	// meaning for a time, in exec as here.)
	tsNum, gridOut := tsCol, "__grid.__g"
	if sqlTimeColumns[timeField] {
		tsNum, gridOut = "epoch_us("+tsCol+")", "make_timestamp(__grid.__g)"
		unitExpr, timeUnit = "1000", "us"
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
			unitExpr, tsNum))
	}
	sb.WriteString(fmt.Sprintf("  __step AS (SELECT CASE WHEN %d %% u != 0 THEN CAST(error('resample: -every is finer than the epoch unit — use generate go') AS BIGINT) ELSE %d // u END AS s FROM __unit),\n",
		everyNs, everyNs))
	sb.WriteString(fmt.Sprintf("  __mm AS (SELECT min(%s) AS mn, max(%s) AS mx FROM __base),\n", tsNum, tsNum))
	sb.WriteString("  __bounds AS (SELECT CAST(floor(mn * 1.0 / s) * s AS BIGINT) AS lo, CAST(floor(mx * 1.0 / s) * s AS BIGINT) AS hi, s FROM __mm, __step),\n")
	sb.WriteString("  __grid AS (SELECT __g FROM __bounds, generate_series(lo, hi, s) __t(__g)),\n")
	for i, v := range values {
		sb.WriteString(fmt.Sprintf("  __s%d AS (SELECT CAST(%s AS BIGINT) AS __ts, max(CAST(%s AS DOUBLE)) AS v FROM __base WHERE %s IS NOT NULL GROUP BY __ts),\n",
			i, tsNum, quoteIdent(v), quoteIdent(v)))
	}
	sb.WriteString("  __out AS (\n    SELECT " + gridOut + " AS " + tsCol)
	for i, v := range values {
		vc := quoteIdent(v)
		switch fill {
		case "previous":
			sb.WriteString(fmt.Sprintf(",\n      COALESCE(p%d.v, (SELECT v FROM __s%d ORDER BY __ts LIMIT 1)) AS %s", i, i, vc))
		case "next":
			sb.WriteString(fmt.Sprintf(",\n      COALESCE(n%d.v, (SELECT v FROM __s%d ORDER BY __ts DESC LIMIT 1)) AS %s", i, i, vc))
		case "linear":
			// frac first, then scale — the association ResampleRecords uses
			// (p + ((g-p)/(n-p))·Δv). Multiplying before dividing is the
			// same real number and a different float64 in the last place.
			sb.WriteString(fmt.Sprintf(`,
      CASE
        WHEN p%d.__ts = __grid.__g THEN p%d.v
        WHEN p%d.__ts IS NOT NULL AND n%d.__ts IS NOT NULL THEN p%d.v + (CAST(__grid.__g - p%d.__ts AS DOUBLE) / CAST(n%d.__ts - p%d.__ts AS DOUBLE)) * (n%d.v - p%d.v)
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


// translateDescribe (DFC122 Tier 1): one row per field with the SAME
// definitions as ssql.DescribeRecords — exact distinct, missing =
// NULL or empty string, median = quantile_cont(0.5), numeric stats
// NULL (absent in ssql) for non-numeric fields, type names mapped to
// ssql's int/float/string/bool vocabulary. Needs the source column
// list (tracked from CSV/TSV headers); refuses loudly when unknown.
func translateDescribe(q *sqlQuery, op *lib.Op, args []string) error {
	if sqlDialectCur != dialectDuckDB {
		return dialectRefuse("describe", "the per-column TRY_CAST/median profile is DuckDB-specific")
	}
	fields, ok := op.StrList("fields")
	if !ok {
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				fields = append(fields, a)
			}
		}
	}
	if q.columns == nil {
		return fmt.Errorf("describe: the SQL translator does not know this source's columns (non-CSV/TSV source or an opaque stage upstream) — use generate go")
	}
	if len(fields) == 0 {
		// Same contract as ssql.DescribeRecords: unrestricted → sorted
		// by field name.
		fields = append([]string(nil), q.columns...)
		slices.Sort(fields)
	} else {
		for _, f := range fields {
			if !slices.Contains(q.columns, f) {
				return fmt.Errorf("describe: unknown field %q (available: %s)", f, strings.Join(q.columns, ", "))
			}
		}
	}
	if q.fromClause == "" {
		return fmt.Errorf("describe: no source to translate")
	}
	wrapAsSubquery(q)
	src := q.fromClause

	const numericTypes = "('TINYINT','SMALLINT','INTEGER','BIGINT','HUGEINT','UTINYINT','USMALLINT','UINTEGER','UBIGINT','UHUGEINT','FLOAT','DOUBLE')"
	var sb strings.Builder
	sb.WriteString("(\n  WITH __d AS (SELECT * FROM " + src + ")")
	for i, f := range fields {
		c := quoteIdent(f)
		present := "nullif(CAST(" + c + " AS VARCHAR), '')"
		ty := "typeof(min(" + c + "))"
		isNum := "(" + ty + " IN " + numericTypes + " OR " + ty + " LIKE 'DECIMAL%')"
		isInt := "(" + ty + " IN ('TINYINT','SMALLINT','INTEGER','BIGINT','HUGEINT','UTINYINT','USMALLINT','UINTEGER','UBIGINT','UHUGEINT'))"
		if i == 0 {
			sb.WriteString("\n  SELECT ")
		} else {
			sb.WriteString("\n  UNION ALL SELECT ")
		}
		fmt.Fprintf(&sb, "%d AS __ord, '%s' AS \"field\",\n", i, escapeSQL(f))
		fmt.Fprintf(&sb, "    CASE WHEN %s THEN 'int' WHEN %s THEN 'float' WHEN %s = 'BOOLEAN' THEN 'bool' ELSE 'string' END AS \"type\",\n", isInt, isNum, ty)
		fmt.Fprintf(&sb, "    count(%s) AS \"count\", count(*) - count(%s) AS \"missing\",\n", present, present)
		fmt.Fprintf(&sb, "    count(DISTINCT %s) FILTER (WHERE %s IS NOT NULL) AS \"distinct\",\n", c, present)
		fmt.Fprintf(&sb, "    CASE WHEN %s THEN CAST(min(%s) AS DOUBLE) END AS \"min\", CASE WHEN %s THEN CAST(max(%s) AS DOUBLE) END AS \"max\",\n", isNum, c, isNum, c)
		fmt.Fprintf(&sb, "    CASE WHEN %s THEN avg(TRY_CAST(%s AS DOUBLE)) END AS \"mean\", CASE WHEN %s THEN median(TRY_CAST(%s AS DOUBLE)) END AS \"median\"\n", isNum, c, isNum, c)
		sb.WriteString("  FROM __d")
	}
	sb.WriteString("\n  ORDER BY __ord\n)")
	*q = sqlQuery{
		fromClause:  sb.String(),
		selectExprs: []string{`"field"`, `"type"`, `"count"`, `"missing"`, `"distinct"`, `"min"`, `"max"`, `"mean"`, `"median"`},
		comments:    q.comments,
		columns:     append([]string(nil), describeColumns...),
	}
	return nil
}


// translateUnpivot lowers to DuckDB's native UNPIVOT (DFC122 Tier 1):
// `UNPIVOT src ON v1, v2 INTO NAME col VALUE val`, projected to
// ids + col + val. Default value list (all non-id columns, sorted)
// needs the tracked column list; refuses loudly when unknown. NULLs are
// excluded by UNPIVOT's default — matching UnpivotRecords, which emits
// no row for an absent value. Caveat, documented: DuckDB coerces mixed
// value-column types to a common type (int + varchar → varchar), where
// ssql keeps each row's own type.
func translateUnpivot(q *sqlQuery, op *lib.Op, args []string) error {
	var ids, values []string
	col, val := "name", "value"
	if s, ok := op.StrList("ids"); ok {
		ids = s
		values, _ = op.StrList("values")
		if c, ok := op.Str("col"); ok && c != "" {
			col = c
		}
		if v, ok := op.Str("val"); ok && v != "" {
			val = v
		}
	} else {
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "-id":
				if i+1 < len(args) {
					ids = append(ids, args[i+1])
					i++
				}
			case "-value":
				if i+1 < len(args) {
					values = append(values, args[i+1])
					i++
				}
			case "-col":
				if i+1 < len(args) {
					col = args[i+1]
					i++
				}
			case "-val":
				if i+1 < len(args) {
					val = args[i+1]
					i++
				}
			}
		}
	}
	if len(values) == 0 {
		if q.columns == nil {
			return fmt.Errorf("unpivot: no -value fields and the SQL translator does not know this source's columns — name the -value fields, or use generate go")
		}
		isID := map[string]bool{}
		for _, id := range ids {
			isID[id] = true
		}
		for _, c := range q.columns {
			if !isID[c] {
				values = append(values, c)
			}
		}
		slices.Sort(values)
	}
	if len(values) == 0 {
		return fmt.Errorf("unpivot: nothing to fold (every column is an -id)")
	}
	if q.fromClause == "" {
		return fmt.Errorf("unpivot: no source to translate")
	}
	if q.hasClauses() {
		wrapAsSubquery(q)
	}
	src := q.fromClause

	var on []string
	for _, v := range values {
		on = append(on, quoteIdent(v))
	}
	var proj []string
	for _, id := range ids {
		proj = append(proj, quoteIdent(id))
	}
	proj = append(proj, quoteIdent(col), quoteIdent(val))
	body := fmt.Sprintf("(\n  SELECT %s FROM (\n    UNPIVOT %s ON %s INTO NAME %s VALUE %s\n  )\n)",
		strings.Join(proj, ", "), src, strings.Join(on, ", "), quoteIdent(col), quoteIdent(val))
	if sqlDialectCur != dialectDuckDB {
		body = sqlUnpivotUnion(src, ids, values, col, val)
	}
	*q = sqlQuery{
		fromClause: sqlSubquery(body),
		comments:   q.comments,
		columns:    append(append([]string(nil), ids...), col, val),
	}
	return nil
}


// translateFill (DFC122 Tier 1): -default → COALESCE(x, v); -down →
// LAST_VALUE(x IGNORE NULLS) OVER (ORDER BY <the query's ORDER BY>
// ROWS UNBOUNDED PRECEDING), both applied with DuckDB's
// `SELECT * REPLACE (...)` so the projection needs no column list.
// -down needs an order: with no preceding sort it refuses loudly (the
// limit -last contract — arrival order is undefined in SQL). DuckDB's
// CSV reader yields NULL for empty cells, matching ssql's missing
// (DFC124); JSON sources with explicit "" strings would differ — the
// documented caveat.
func translateFill(q *sqlQuery, op *lib.Op, args []string) error {
	var down []string
	type dflt struct{ field, value string }
	var defaults []dflt
	if d, ok := op.StrList("down"); ok || (op != nil && op.Args != nil) {
		down = d
		if pairs, ok := op.Args["defaults"].([]any); ok {
			for _, p := range pairs {
				if m, ok := p.(map[string]any); ok {
					f, _ := m["field"].(string)
					v := fmt.Sprintf("%v", m["value"])
					defaults = append(defaults, dflt{f, v})
				}
			}
		}
	} else {
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "-down":
				if i+1 < len(args) {
					down = append(down, args[i+1])
					i++
				}
			case "-default":
				if i+2 < len(args) {
					defaults = append(defaults, dflt{args[i+1], args[i+2]})
					i += 2
				}
			}
		}
	}
	if len(down) == 0 && len(defaults) == 0 {
		return fmt.Errorf("fill: nothing to do (need -down FIELD and/or -default FIELD VALUE)")
	}
	if len(down) > 0 && len(q.orderBy) == 0 {
		return fmt.Errorf("fill -down needs a preceding sort for SQL — carry-forward order is undefined in SQL; add `ssql sort FIELD` before it, or use generate go")
	}
	if q.fromClause == "" {
		return fmt.Errorf("fill: no source to translate")
	}
	order := append([]string(nil), q.orderBy...)
	wrapAsSubquery(q)
	q.orderBy = order // the output keeps the pipeline's order
	if len(down) > 0 && sqlDialectCur == dialectPostgres {
		return dialectRefuse("fill -down", "Postgres has no IGNORE NULLS for LAST_VALUE")
	}
	var repl []sqlPair
	for _, f := range down {
		c := quoteIdent(f)
		repl = append(repl, sqlPair{f, fmt.Sprintf("LAST_VALUE(%s IGNORE NULLS) OVER (ORDER BY %s ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)",
			c, strings.Join(order, ", "))})
	}
	for _, d := range defaults {
		c := quoteIdent(d.field)
		lit := d.value
		if _, err := strconv.ParseFloat(lit, 64); err != nil && lit != "true" && lit != "false" {
			lit = "'" + escapeSQL(lit) + "'"
		}
		// A field defaulted AND carried: default applies after the carry.
		if slices.Contains(down, d.field) {
			for i, r := range repl {
				if r.col == d.field {
					repl[i].val = fmt.Sprintf("COALESCE(%s, %s)", r.val, lit)
				}
			}
			continue
		}
		repl = append(repl, sqlPair{d.field, fmt.Sprintf("COALESCE(%s, %s)", c, lit)})
	}
	sel, err := starProjection(q.columns, nil, repl, nil, "fill")
	if err != nil {
		return err
	}
	q.selectExprs = []string{sel}
	return nil
}


// translateExtract (DFC122 Tier 1): regexp_extract with the named-group
// list → a STRUCT, projected to fields; the source field dropped unless
// -keep. SQL cannot fail per row, so without -skip the translation is
// REFUSED (a non-match would silently yield empty strings where ssql
// stops loudly); with -skip, WHERE regexp_matches drops non-matches —
// the same rows ssql drops.
func translateExtract(q *sqlQuery, op *lib.Op, args []string) error {
	if sqlDialectCur != dialectDuckDB {
		return dialectRefuse("extract", "named-group regexp_extract is DuckDB-specific")
	}
	var field, re string
	var names []string
	skip, keep := false, false
	if f, ok := op.Str("field"); ok {
		field = f
		re, _ = op.Str("re")
		names, _ = op.StrList("names")
		if b, ok := op.Args["skip"].(bool); ok {
			skip = b
		}
		if b, ok := op.Args["keep"].(bool); ok {
			keep = b
		}
	} else {
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "-field":
				if i+1 < len(args) {
					field = args[i+1]
					i++
				}
			case "-re":
				if i+1 < len(args) {
					re = args[i+1]
					i++
				}
			case "-skip":
				skip = true
			case "-keep":
				keep = true
			}
		}
	}
	if field == "" || re == "" {
		return fmt.Errorf("extract: -field and -re are required")
	}
	if len(names) == 0 {
		_, ns, err := ssql.CompileExtract(ssql.ExtractConfig{Pattern: re})
		if err != nil {
			return err
		}
		names = ns
	}
	if !skip {
		return fmt.Errorf("extract without -skip has no SQL translation — SQL cannot fail on a non-matching row the way ssql does; add -skip, or use generate go")
	}
	if q.fromClause == "" {
		return fmt.Errorf("extract: no source to translate")
	}
	wrapAsSubquery(q)
	src := q.fromClause
	c := quoteIdent(field)
	lit := "'" + escapeSQL(re) + "'"
	var quotedNames []string
	for _, n := range names {
		quotedNames = append(quotedNames, "'"+escapeSQL(n)+"'")
	}
	exclude := "__m"
	if !keep {
		exclude = c + ", __m"
	}
	var proj []string
	for _, n := range names {
		proj = append(proj, fmt.Sprintf("__m.%s AS %s", quoteIdent(n), quoteIdent(n)))
	}
	body := fmt.Sprintf("(\n  SELECT * EXCLUDE (%s), %s\n  FROM (SELECT *, regexp_extract(%s, %s, [%s]) AS __m FROM %s WHERE regexp_matches(%s, %s))\n)",
		exclude, strings.Join(proj, ", "), c, lit, strings.Join(quotedNames, ", "), src, c, lit)
	var cols []string
	if q.columns != nil {
		for _, col := range q.columns {
			if col != field || keep {
				cols = append(cols, col)
			}
		}
		cols = append(cols, names...)
	}
	*q = sqlQuery{fromClause: body, comments: q.comments, columns: cols}
	return nil
}
