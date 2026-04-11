package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerGenerateSQL registers the "generate sql" subcommand
func registerGenerateSQL(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("sql").
		Description("Generate DuckDB SQL from ssql CLI pipeline").
		Example("(export SSQLGO=1; ssql from data.csv | ssql where -if age gt 25 | ssql to table) | ssql generate sql", "Generate SQL from pipeline").
		Example("(export SSQLGO=1; ssql from data.parquet | ssql group-by dept -sum salary total | ssql to table) | ssql generate sql", "Parquet aggregation query").
		Example("(export SSQLGO=1; ssql from data.csv | ssql where -if age gt 25 | ssql to table) | ssql generate sql -run", "Generate and execute with DuckDB").
		Flag("-run", "-r").
		Bool().
		Global().
		Default(false).
		Help("Execute the generated SQL with duckdb").
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

			sql, err := assembleSQL(os.Stdin)
			if err != nil {
				return fmt.Errorf("assembling SQL: %w", err)
			}

			if run {
				cmd := exec.Command("duckdb", "-c", sql)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				return cmd.Run()
			}

			if outputFile != "" {
				if err := os.WriteFile(outputFile, []byte(sql), 0644); err != nil {
					return fmt.Errorf("writing output file: %w", err)
				}
				fmt.Fprintf(os.Stderr, "Generated SQL written to %s\n", outputFile)
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
	groupBy      []string
	orderBy      []string
	limit        string
	offset       string
	comments     []string // original ssql commands
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

func translateFragment(q *sqlQuery, frag *lib.CodeFragment, funcFrags []*lib.CodeFragment) error {
	cmd := frag.Command
	if cmd == "" {
		return nil // skip empty commands (e.g., Aggregate fragment from group-by)
	}

	args := parseCommandArgs(cmd)
	if len(args) < 2 {
		return nil
	}

	// args[0] is "ssql", args[1] is the command
	switch args[1] {
	case "from":
		return translateFrom(q, args[2:])
	case "where":
		return translateWhere(q, args[2:])
	case "group-by":
		return translateGroupBy(q, args[2:])
	case "sort":
		return translateSort(q, args[2:])
	case "limit":
		return translateLimit(q, args[2:])
	case "offset":
		return translateOffset(q, args[2:])
	case "top":
		return translateTop(q, args[2:])
	case "distinct":
		q.selectExprs = append([]string{"DISTINCT"}, q.selectExprs...)
		return nil
	case "join":
		return translateJoin(q, args[2:], funcFrags)
	case "window":
		return translateWindow(q, args[2:])
	case "rename":
		return translateRename(q, args[2:])
	case "cast":
		return translateCast(q, args[2:])
	case "update":
		return translateUpdate(q, args[2:])
	case "include":
		return translateInclude(q, args[2:])
	case "exclude":
		return translateExclude(q, args[2:])
	case "to":
		// Output commands don't affect SQL
		return nil
	default:
		return fmt.Errorf("unsupported command for SQL generation: %s", args[1])
	}
}

func translateFrom(q *sqlQuery, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("from requires a file argument")
	}

	// Handle format subcommands: from csv FILE, from parquet FILE, etc.
	switch args[0] {
	case "csv", "tsv", "json", "jsonl", "arrow", "parquet", "xlsx":
		if len(args) < 2 {
			return fmt.Errorf("from %s requires a file argument", args[0])
		}
		// Collect file paths (skip flags starting with -)
		var files []string
		for _, a := range args[1:] {
			if a == "--" {
				break
			}
			if !strings.HasPrefix(a, "-") {
				files = append(files, a)
			}
		}
		if len(files) == 1 {
			q.fromClause = quoteFile(files[0])
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
		case "-if", "-i":
			if i+3 >= len(args) {
				return fmt.Errorf("incomplete -if condition")
			}
			field, op, value := args[i+1], args[i+2], args[i+3]
			cond := translateCondition(field, op, value)
			currentAnd = append(currentAnd, cond)
			i += 4
		case "-if-expr", "-x":
			if i+1 >= len(args) {
				return fmt.Errorf("incomplete -if-expr")
			}
			// Pass expression through as-is — most expr-lang syntax is valid SQL
			currentAnd = append(currentAnd, args[i+1])
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
	return fmt.Sprintf("%s %s '%s'", quoteIdent(field), sqlOp, escapeSQL(value))
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

	for _, c := range clauses {
		for _, f := range c.fields {
			entry := quoteIdent(f)
			if c.desc {
				entry += " DESC"
			}
			q.orderBy = append(q.orderBy, entry)
		}
	}
	return nil
}

func translateLimit(q *sqlQuery, args []string) error {
	if len(args) > 0 {
		q.limit = args[0]
	}
	return nil
}

func translateOffset(q *sqlQuery, args []string) error {
	if len(args) > 0 {
		q.offset = args[0]
	}
	return nil
}

func translateTop(q *sqlQuery, args []string) error {
	// top N -by field → ORDER BY field DESC LIMIT N
	if len(args) == 0 {
		return nil
	}
	q.limit = args[0]
	for i := 1; i < len(args); i++ {
		if (args[i] == "-by" || args[i] == "-b") && i+1 < len(args) {
			q.orderBy = append(q.orderBy, quoteIdent(args[i+1])+" DESC")
			i++
		}
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
		args := parseCommandArgs(bodyFrag.Command)
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
	// Parse: -if field op val -set field val [-set field val ...]
	// Multiple -if groups create chained CASE WHEN ... WHEN ... ELSE ... END

	// First pass: collect all target fields and their condition/value pairs
	type assignment struct {
		conds []string // AND conditions for this clause
		field string
		value string
	}
	var assignments []assignment
	var currentConds []string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "-if", "-i":
			if i+3 >= len(args) {
				return fmt.Errorf("incomplete -if condition in update")
			}
			cond := translateCondition(args[i+1], args[i+2], args[i+3])
			currentConds = append(currentConds, cond)
			i += 4
		case "-set", "-s":
			if i+2 >= len(args) {
				return fmt.Errorf("incomplete -set in update")
			}
			assignments = append(assignments, assignment{
				conds: append([]string{}, currentConds...),
				field: args[i+1],
				value: args[i+2],
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

	var replacements []string
	for _, field := range fieldOrder {
		cases := fieldCases[field]
		var sb strings.Builder
		sb.WriteString("CASE")
		for _, c := range cases {
			if len(c.conds) > 0 {
				sb.WriteString(" WHEN " + strings.Join(c.conds, " AND ") + " THEN '" + escapeSQL(c.value) + "'")
			} else {
				// Unconditional set
				sb.WriteString(" ELSE '" + escapeSQL(c.value) + "'")
			}
		}
		// If all cases are conditional, preserve original value as ELSE
		hasUnconditional := false
		for _, c := range cases {
			if len(c.conds) == 0 {
				hasUnconditional = true
				break
			}
		}
		if !hasUnconditional {
			sb.WriteString(" ELSE " + quoteIdent(field))
		}
		sb.WriteString(" END")
		replacements = append(replacements, fmt.Sprintf("%s AS %s", sb.String(), quoteIdent(field)))
	}

	if len(replacements) > 0 {
		q.selectExprs = append(q.selectExprs, "* REPLACE ("+strings.Join(replacements, ", ")+")")
	}
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

	// SELECT
	if len(q.selectExprs) > 0 {
		sb.WriteString("SELECT " + strings.Join(q.selectExprs, ", ") + "\n")
	} else {
		sb.WriteString("SELECT *\n")
	}

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

	sb.WriteString(";\n")
	return sb.String()
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
