package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterGenerateSQL registers the generate-sql subcommand
func RegisterGenerateSQL(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("generate-sql").
		Description("Generate DuckDB SQL from ssql CLI pipeline").
		Example("(export SSQLGO=1; ssql from data.csv | ssql where -if age gt 25 | ssql to table) | ssql generate-sql", "Generate SQL from pipeline").
		Example("(export SSQLGO=1; ssql from data.parquet | ssql group-by dept -sum salary total | ssql to table) | ssql generate-sql", "Parquet aggregation query").
		Example("(export SSQLGO=1; ssql from data.csv | ssql where -if age gt 25 | ssql to table) | ssql generate-sql -run", "Generate and execute with DuckDB").
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
	return cmd
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

	for _, frag := range fragments {
		if frag.Command != "" {
			q.comments = append(q.comments, frag.Command)
		}
		if err := translateFragment(q, frag); err != nil {
			return "", err
		}
	}

	return renderSQL(q), nil
}

func translateFragment(q *sqlQuery, frag *lib.CodeFragment) error {
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
		return translateJoin(q, args[2:])
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
		q.fromClause = quoteFile(args[1])
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
	desc := false
	for _, arg := range args {
		switch arg {
		case "-desc", "-d":
			desc = true
		case "-asc", "-a":
			desc = false
		default:
			if strings.HasPrefix(arg, "-") {
				continue
			}
			entry := quoteIdent(arg)
			if desc {
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

func translateJoin(q *sqlQuery, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("join requires a file argument")
	}

	file := quoteFile(args[0])
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
		joinCond = "ON TRUE" // shouldn't happen but safe fallback
	}

	q.joins = append(q.joins, fmt.Sprintf("JOIN %s %s", file, joinCond))
	return nil
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
		sb.WriteString("-- Generated by ssql generate-sql\n")
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
