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
// writing to stdout stamps no file. opts carries the flags that change
// the output, stamped for stdout too, so a backend that cannot honour one
// refuses it instead of writing its own format (`generate rust` ignored
// `to csv -time-format` until 2026-11-08); an empty value is left out.
func stampSinkFile(frag *lib.CodeFragment, file string, opts map[string]any) {
	if frag.Op == nil {
		return
	}
	if frag.Op.Args == nil {
		frag.Op.Args = map[string]any{}
	}
	if file != "" {
		frag.Op.Args["file"] = file
	}
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

// rustRefuseSink refuses a sink the generated main would not write as
// asked: a file other than csv, tsv and parquet, and an option of a csv or
// tsv sink that Arrow's CSV writer cannot honour.
func rustRefuseSink(q *sqlQuery) error {
	if len(q.sink) == 0 {
		return nil
	}
	kind, file := q.sink[0], q.sinkFile()
	what := strings.TrimSpace("to " + kind + " " + file)
	switch kind {
	case "csv", "tsv":
		if q.sinkOp == nil {
			return nil
		}
		if _, ok := q.sinkOp.Str("time_format"); ok {
			return fmt.Errorf("%s -time-format has no generate rust form (the program writes a time as an RFC 3339 instant) — use generate go", what)
		}
		if _, ok := q.sinkOp.Args["sidecar"]; ok {
			return fmt.Errorf("%s -sidecar has no generate rust form — use generate go", what)
		}
		if sep, ok := q.sinkOp.Str("separator"); ok && len(sep) != 1 {
			return fmt.Errorf("%s -separator %q has no generate rust form: Arrow's CSV writer takes a one-byte delimiter — use generate go", what, sep)
		}
		return nil
	case "parquet":
		return nil
	}
	if file != "" {
		return fmt.Errorf("%s has no generate rust form: the program writes csv, tsv and parquet files — use generate sql or generate go", what)
	}
	return nil
}

// rustCSVSink is the Rust that writes the DataFrame as CSV, or as TSV
// (tab or -separator, nothing quoted, as `to tsv` writes), to file or to
// stdout. A time is an RFC 3339 instant, which reads back as the same
// time; the cell's own offset is gone, as in every SQL lane. The header
// goes out before the first batch, so a result with no rows is a header,
// as exec writes it (Arrow's writer emitted nothing until 2026-11-08). The
// writer is flushed explicitly: a write error is the program's exit code.
func rustCSVSink(kind, file string, op *lib.Op) string {
	var b strings.Builder
	b.WriteString("    let schema = df.schema().inner().clone();\n")
	b.WriteString("    let builder = datafusion::arrow::csv::WriterBuilder::new()\n")
	b.WriteString("        .with_header(true)\n")
	b.WriteString("        .with_timestamp_format(\"%Y-%m-%dT%H:%M:%S%.fZ\".to_string())\n")
	b.WriteString("        .with_timestamp_tz_format(\"%Y-%m-%dT%H:%M:%S%.fZ\".to_string())")
	if kind == "tsv" {
		sep := "\t"
		if op != nil {
			if s, ok := op.Str("separator"); ok {
				sep = s
			}
		}
		fmt.Fprintf(&b, "\n        .with_delimiter(%s)\n", rustByte(sep[0]))
		b.WriteString("        .with_quote_style(datafusion::arrow::csv::QuoteStyle::Never)")
	}
	b.WriteString(";\n")
	if file == "" {
		b.WriteString("    let mut w = builder.build(std::io::BufWriter::new(std::io::stdout().lock()));\n")
	} else {
		fmt.Fprintf(&b, "    let path = %s;\n", rustStr(file))
		b.WriteString("    let out = std::fs::File::create(path).map_err(|e| datafusion::error::DataFusionError::Execution(format!(\"{path}: {e}\")))?;\n")
		b.WriteString("    let mut w = builder.build(std::io::BufWriter::new(out));\n")
	}
	b.WriteString(`    w.write(&datafusion::arrow::record_batch::RecordBatch::new_empty(schema))?;
    let mut stream = df.execute_stream().await?;
    while let Some(batch) = futures::StreamExt::next(&mut stream).await {
        w.write(&batch?)?;
    }
    std::io::Write::flush(&mut w.into_inner())?;
`)
	return b.String()
}

// rustByte spells a byte as a Rust byte literal.
func rustByte(c byte) string {
	switch c {
	case '\t':
		return `b'\t'`
	case '\'':
		return `b'\''`
	case '\\':
		return `b'\\'`
	}
	if c >= 0x20 && c < 0x7f {
		return "b'" + string(c) + "'"
	}
	return fmt.Sprintf(`b'\x%02x'`, c)
}
