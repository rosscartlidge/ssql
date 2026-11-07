package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// stampSinkFile says on a sink's fragment which file it writes, so the
// backends that do not run its generated code (generate sql, generate
// rust) ask the command rather than guess from its arguments. A sink
// writing to stdout stamps nothing. opts carries the flags that change
// the file; an empty value is left out.
func stampSinkFile(frag *lib.CodeFragment, file string, opts map[string]any) {
	if frag.Op == nil || file == "" {
		return
	}
	if frag.Op.Args == nil {
		frag.Op.Args = map[string]any{}
	}
	frag.Op.Args["file"] = file
	for k, v := range opts {
		if v != "" && v != false {
			frag.Op.Args[k] = v
		}
	}
}

// sinkFile is the file the pipeline's `to` stage writes, "" for stdout.
func (q *sqlQuery) sinkFile() string {
	if q.sinkOp == nil {
		return ""
	}
	file, _ := q.sinkOp.Str("file")
	return file
}

// sqlTimestampCopyFormat spells a time in a delimited file as RFC 3339.
// The Z is literal: the statement pins the session to UTC (sqlCopySink).
const sqlTimestampCopyFormat = "'%Y-%m-%dT%H:%M:%S.%fZ'"

// sqlCopySink turns a sink that writes a file into the COPY the renderer
// wraps round the query. Without it a generated statement printed the rows
// and wrote nothing, exit 0 (Ross, 2026-11-07). A file the statement cannot
// write is refused: the rows must not quietly go somewhere else.
func sqlCopySink(q *sqlQuery) error {
	file := q.sinkFile()
	if file == "" {
		return nil
	}
	kind := q.sink[0]
	what := "to " + kind + " " + file
	opts, err := sqlCopyOptions(kind, what, q.sinkOp)
	if err != nil {
		return err
	}
	if sqlDialectCur != dialectDuckDB {
		return dialectRefuse(what, "only DuckDB's COPY is generated so far")
	}
	sqlNeedsZone = true
	q.copyTo = "TO " + quoteSQLString(file) + " (" + strings.Join(opts, ", ") + ")"
	return nil
}

// sqlCopyOptions is the option list of DuckDB's COPY for a sink.
func sqlCopyOptions(kind, what string, op *lib.Op) ([]string, error) {
	if _, ok := op.Str("time_format"); ok {
		return nil, fmt.Errorf("%s -time-format has no SQL translation — use generate go", what)
	}
	if _, ok := op.Args["sidecar"]; ok {
		return nil, fmt.Errorf("%s -sidecar has no SQL translation — use generate go", what)
	}
	switch kind {
	case "csv":
		return []string{"FORMAT CSV", "HEADER", "TIMESTAMPFORMAT " + sqlTimestampCopyFormat}, nil
	case "tsv":
		// `to tsv` quotes nothing.
		sep, ok := op.Str("separator")
		if !ok {
			sep = "\t"
		}
		delim := quoteSQLString(sep)
		if sep == "\t" {
			delim = `E'\t'`
		}
		return []string{"FORMAT CSV", "HEADER", "DELIMITER " + delim, "QUOTE ''", "ESCAPE ''", "TIMESTAMPFORMAT " + sqlTimestampCopyFormat}, nil
	case "jsonl":
		return []string{"FORMAT JSON"}, nil
	case "json":
		return []string{"FORMAT JSON", "ARRAY true"}, nil
	case "parquet":
		opts := []string{"FORMAT PARQUET"}
		if c, ok := op.Str("compression"); ok {
			if c == "none" {
				c = "uncompressed"
			}
			opts = append(opts, "COMPRESSION "+quoteSQLString(c))
		}
		if n, ok := op.Int64("row_group_size"); ok && n > 0 {
			opts = append(opts, "ROW_GROUP_SIZE "+strconv.FormatInt(n, 10))
		}
		return opts, nil
	}
	return nil, fmt.Errorf("%s has no SQL translation: generate sql writes csv, tsv, json, jsonl and parquet files — use generate go", what)
}

// rustRefuseSink refuses a file the generated main would not write.
func rustRefuseSink(q *sqlQuery) error {
	if file := q.sinkFile(); file != "" && q.sink[0] != "parquet" {
		return fmt.Errorf("to %s %s has no generate rust form: the program writes only to parquet FILE — use generate sql or generate go", q.sink[0], file)
	}
	return nil
}
