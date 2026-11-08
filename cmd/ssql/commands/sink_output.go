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
// asked: anything but a table, csv, tsv, json or jsonl (on stdout or to a
// file) and a parquet file, and an option of a csv or tsv sink that
// Arrow's CSV writer cannot honour. Until 2026-11-09 a sink the main did
// not know printed JSONL on stdout, exit 0 (`to json` and `to markdown`
// among them).
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
	case "table", "json", "jsonl":
		return nil
	case "parquet":
		if file != "" {
			return nil
		}
	}
	return fmt.Errorf("%s has no generate rust form: the program writes a table, csv, tsv, json and jsonl, and parquet files — use generate go", what)
}

// rustTimeFormat spells a time in a written file as an RFC 3339 instant
// in UTC, which reads back as the same time in ssql and in any other
// reader; the cell's own offset is gone, as in every SQL lane. Arrow's
// default has no Z, which another reader takes as local time.
const rustTimeFormat = "\"%Y-%m-%dT%H:%M:%S%.fZ\".to_string()"

// rustSinkWriter is the Rust that builds the writer w from builder over a
// buffered stdout or file; a file that cannot be created is an error that
// names it. typ annotates w where build is generic over the format.
func rustSinkWriter(b *strings.Builder, file, typ string) {
	if file == "" {
		fmt.Fprintf(b, "    let mut w%s = builder.build(std::io::BufWriter::new(std::io::stdout().lock()));\n", typ)
		return
	}
	fmt.Fprintf(b, "    let path = %s;\n", rustStr(file))
	b.WriteString("    let out = std::fs::File::create(path).map_err(|e| datafusion::error::DataFusionError::Execution(format!(\"{path}: {e}\")))?;\n")
	fmt.Fprintf(b, "    let mut w%s = builder.build(std::io::BufWriter::new(out));\n", typ)
}

// rustWriteBatches is the Rust that writes the DataFrame's batches to w
// as they stream.
const rustWriteBatches = `    let mut stream = df.execute_stream().await?;
    while let Some(batch) = futures::StreamExt::next(&mut stream).await {
        w.write(&batch?)?;
    }
`

// rustCSVSink is the Rust that writes the DataFrame as CSV, or as TSV
// (tab or -separator, nothing quoted, as `to tsv` writes), to file or to
// stdout. The header goes out before the first batch, so a result with no
// rows is a header, as exec writes it (Arrow's writer emitted nothing
// until 2026-11-08). The writer is flushed explicitly: a write error is
// the program's exit code.
func rustCSVSink(kind, file string, op *lib.Op) string {
	var b strings.Builder
	b.WriteString("    let schema = df.schema().inner().clone();\n")
	b.WriteString("    let builder = datafusion::arrow::csv::WriterBuilder::new()\n")
	b.WriteString("        .with_header(true)\n")
	b.WriteString("        .with_timestamp_format(" + rustTimeFormat + ")\n")
	b.WriteString("        .with_timestamp_tz_format(" + rustTimeFormat + ")")
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
	rustSinkWriter(&b, file, "")
	b.WriteString("    w.write(&datafusion::arrow::record_batch::RecordBatch::new_empty(schema))?;\n")
	b.WriteString(rustWriteBatches)
	b.WriteString("    std::io::Write::flush(&mut w.into_inner())?;\n")
	return b.String()
}

// rustJSONSink is the Rust that writes the DataFrame as JSONL (one object
// per line, a null left out, as `to jsonl` writes; what the equivalence
// lanes read on stdout) or as a JSON array (every key in every object, a
// null spelled null, as `to json` writes; `[]` for no rows), to file or
// to stdout. Arrow's array has no indentation where exec's is
// pretty-printed: the same JSON, other bytes.
func rustJSONSink(kind, file string) string {
	var b strings.Builder
	b.WriteString("    let builder = datafusion::arrow::json::WriterBuilder::new()\n")
	if kind == "json" {
		b.WriteString("        .with_explicit_nulls(true)\n")
	}
	b.WriteString("        .with_timestamp_format(" + rustTimeFormat + ")\n")
	b.WriteString("        .with_timestamp_tz_format(" + rustTimeFormat + ");\n")
	typ := ": datafusion::arrow::json::LineDelimitedWriter<_>"
	if kind == "json" {
		typ = ": datafusion::arrow::json::ArrayWriter<_>"
	}
	rustSinkWriter(&b, file, typ)
	b.WriteString(rustWriteBatches)
	b.WriteString("    w.finish()?;\n")
	b.WriteString("    let mut out = w.into_inner();\n")
	if kind == "json" {
		b.WriteString("    std::io::Write::write_all(&mut out, b\"\\n\")?;\n")
	}
	b.WriteString("    std::io::Write::flush(&mut out)?;\n")
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
