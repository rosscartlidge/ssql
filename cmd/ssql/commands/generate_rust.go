package commands

// generate rust (DFC157): the pipeline as a Rust program on Apache
// DataFusion. The fragment stream is assembled into the same query tree
// `generate sql -dialect datafusion` renders as text (assembleQuery under
// dialectDataFusion), and the tree is walked into DataFrame-API calls —
// one `let df = df.<op>(…)?;` per clause, a Rust program that reads as
// the pipeline does. The hybrid rule settled with Ross (2026-10-27):
// everything that came from a FLAG renders as DataFusion builders
// (col, lit, comparisons, aggregates, sorts, joins, ranking windows);
// the expression-language stages (-if-expr, -set-expr, -expr) hand the
// SQL text the datafusion-dialect translator already produced to
// DataFusion's own expression parser (parse_sql_expr) — one expression
// translator, verified by the oracle lane, not a third one. A layer whose
// form has no DataFrame rendering (the rollup/cube emulation, a keyed
// except/intersect, unpivot, sample, `from range`) runs as one ctx.sql
// step over the DataFrame so far (the escape hatch), so coverage is the
// datafusion dialect's from day one. Builder when a renderer exists, text
// otherwise: coverage never shrinks for want of a builder form.

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
)

// rustDataFusionCrate is the DataFusion crate the generated program
// compiles against: the same major as the Python bindings the datafusion
// oracle lane runs (55), bumped deliberately with it.
const rustDataFusionCrate = "55"

func registerGenerateRust(cmd *cf.SubcommandBuilder) {
	sub := cmd.Subcommand("rust").
		Description("Generate a Rust program (Apache DataFusion's DataFrame API) from an ssql CLI pipeline").
		Example("ssql generate rust -pipeline 'ssql from data.csv | ssql where -if age gt 25 | ssql group-by dept -avg salary avg | ssql to table' pipeline.rs", "The pipeline as a Rust program: one DataFrame call per stage, expressions as DataFusion's own SQL expressions").
		Example("ssql generate rust -run -pipeline 'ssql from data.parquet | ssql group-by dept -count n | ssql to table'", "Compile with cargo (the first build compiles DataFusion: minutes, once) and run").
		Example("(export SSQL_MODE=record; ssql from data.csv | ssql where -if age gt 25 | ssql to csv) | ssql generate rust -build ./report", "Compile to a standalone binary (about 170 MB, DataFusion statically linked)").
		Flag("-run", "-r").
		Bool().
		Global().
		Default(false).
		Help("Compile the program with cargo and run it (mutually exclusive with OUTPUT and -build). The crate lives under $XDG_CACHE_HOME/ssql/rust (~/.cache/ssql/rust): the first build compiles DataFusion itself — several minutes and about 1.3 GB — later builds relink in seconds. cargo is found on PATH, then at ~/.cargo/bin/cargo, or named by SSQL_CARGO").
		Done().
		Flag("-build", "-b").
		String().
		Completer(&cf.FileCompleter{Pattern: "*"}).
		Global().
		Default("").
		Help("Compile to the named binary and exit (mutually exclusive with OUTPUT and -run)").
		Done().
		Flag("-time").
		Bool().
		Global().
		Default(false).
		Help("With -run: print the compile and run wall-clock times to stderr").
		Done().
		Flag("-func").
		String().
		Global().
		Default("").
		Help("Emit only `pub async fn NAME(ctx: &SessionContext) -> Result<DataFrame>` — no main, no imports of its own beyond the prelude — so several pipelines can share one crate (the equivalence lane builds all its cases into one binary this way)").
		Done().
		Flag("-optimise", "-O").
		Bool().
		Global().
		Default(true).
		Help("Apply the pipeline optimiser (the same rewrites as 'generate ssql', 'generate go' and 'generate sql') before translating. ON by default; +O / +optimise translates the pipeline as typed. The program's header comment records both").
		Done().
		Flag("-explain", "-e").
		Bool().
		Global().
		Default(false).
		Help("Print the optimiser's applied rules to stderr").
		Done()
	pipelineSourceFlags(sub, "translate", "record").
		Flag("OUTPUT").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.rs"}).
		Global().
		Default("").
		Help("Output Rust source file (or stdout if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			outputFile, _ := ctx.GlobalFlags["OUTPUT"].(string)
			run := ctx.GetBool("-run", false)
			buildOut, _ := ctx.GlobalFlags["-build"].(string)
			funcName, _ := ctx.GlobalFlags["-func"].(string)
			timeRun := ctx.GetBool("-time", false)
			set := 0
			for _, on := range []bool{run, buildOut != "", outputFile != ""} {
				if on {
					set++
				}
			}
			if set > 1 {
				return fmt.Errorf("ssql generate rust: -run, -build and OUTPUT are mutually exclusive")
			}
			if funcName != "" && (run || buildOut != "") {
				return fmt.Errorf("ssql generate rust: -func emits a function without a main, which has nothing to run or build")
			}
			if funcName != "" && !rustIdentRe.MatchString(funcName) {
				return fmt.Errorf("ssql generate rust: -func %q is not a Rust identifier", funcName)
			}

			fragSrc, err := generateFragmentSource(ctx, "record", "rust")
			if err != nil {
				return err
			}
			buf, err := io.ReadAll(fragSrc)
			if err != nil {
				return fmt.Errorf("reading fragments: %w", err)
			}
			if ctx.GetBool("-optimise", true) && runtime.GOOS != "js" {
				inner := "generate rust +O"
				if funcName != "" {
					inner += " -func " + funcName
				}
				if timeRun {
					inner += " -time"
				}
				if buildOut != "" {
					// -build rides on the inner command (as -func and -time do;
					// -run and OUTPUT are optimiseThenRegen's): without it a
					// pruned pipeline printed the program and built nothing,
					// exit 0 (found by scripts/bench-engines.sh, 2026-10-28).
					inner += " -build " + shellQuote(buildOut)
				}
				if done, err := optimiseThenRegen(buf, ctx.GetBool("-explain", false), "rust", inner, run, outputFile, ctx.Stdout(), ctx.Stderr()); done || err != nil {
					return err
				}
			}
			code, err := assembleRust(bytes.NewReader(buf), funcName)
			if err != nil {
				return fmt.Errorf("assembling Rust: %w", err)
			}
			switch {
			case run:
				return runRustSource(code, timeRun)
			case buildOut != "":
				return buildRustSource(code, buildOut)
			case outputFile != "":
				if err := os.WriteFile(outputFile, []byte(code), 0o644); err != nil {
					return fmt.Errorf("writing output file: %w", err)
				}
				fmt.Fprintf(ctx.Stderr(), "Generated Rust written to %s\n", outputFile)
				return nil
			}
			fmt.Fprint(ctx.Stdout(), code)
			return nil
		}).
		Done()
}

// assembleRust translates the fragment stream into a Rust program (or,
// with funcName, the pipeline function alone).
func assembleRust(input io.Reader, funcName string) (string, error) {
	prev, prevLoads, prevRust := sqlDialectCur, pgLoads, sqlForRust
	sqlDialectCur, pgLoads, sqlForRust = dialectDataFusion, nil, true
	defer func() { sqlDialectCur, pgLoads, sqlForRust = prev, prevLoads, prevRust }()
	q, err := assembleQuery(input)
	if err != nil {
		return "", err
	}
	return renderRust(q, funcName), nil
}

// rustGen accumulates the pipeline function's body.
type rustGen struct {
	lines   []string
	views   int               // escape-hatch views registered (__q1, __q2, …)
	sides   int               // side DataFrames bound (s1, s2, …)
	exprs   int               // expression bindings (e1, e2, …)
	frames  map[string]string // window frames bound (frame1, …), by their constructor
	escapes []string          // the reasons layers ran as SQL text, for the header
}

func (g *rustGen) line(format string, a ...any) {
	g.lines = append(g.lines, "    "+fmt.Sprintf(format, a...))
}

// renderRust renders the query tree as a Rust program.
func renderRust(q *sqlQuery, funcName string) string {
	fn := funcName
	if fn == "" {
		fn = "pipeline"
	}
	g := &rustGen{}
	g.layer(q, "df", nil)
	var sb strings.Builder
	if funcName == "" {
		fmt.Fprintf(&sb, "// Generated by ssql generate rust — runs on Apache DataFusion %s\n", rustDataFusionCrate)
		sb.WriteString("// Pipeline:\n")
		for _, c := range q.comments {
			sb.WriteString("//   " + c + "\n")
		}
		if note := os.Getenv(optimiseNoteEnv); note != "" {
			sb.WriteString("//\n")
			for _, line := range strings.Split(note, "\n") {
				sb.WriteString(strings.TrimRight("// "+line, " ") + "\n")
			}
		}
		if len(g.escapes) > 0 {
			sb.WriteString("//\n// Stages run as SQL over the DataFrame so far (no DataFrame form yet): " + strings.Join(g.escapes, ", ") + "\n")
		}
		sb.WriteString("\n#![allow(unused_imports)]\n\n")
		sb.WriteString(RustImports)
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "pub async fn %s(ctx: &SessionContext) -> Result<DataFrame> {\n", fn)
	for _, l := range g.lines {
		sb.WriteString(l + "\n")
	}
	sb.WriteString("    Ok(df)\n}\n")
	if funcName == "" {
		sb.WriteString("\n" + rustMain(fn, q.sink))
	}
	return sb.String()
}

// RustImports is the use block every generated program (and the rust
// lane's one crate) starts with.
const RustImports = `use datafusion::arrow::datatypes::{DataType, Field, Schema};
use datafusion::error::Result;
use datafusion::functions_aggregate::expr_fn as agg;
use datafusion::functions_aggregate::{average::avg_udaf, count::{count_all_window, count_udaf}, min_max::{max_udaf, min_udaf}, string_agg::string_agg, sum::sum_udaf};
use datafusion::functions_window::expr_fn as win;
use datafusion::logical_expr::expr::WindowFunction;
use datafusion::logical_expr::{Operator, WindowFrame, WindowFrameBound, WindowFrameUnits};
use datafusion::prelude::*;
use datafusion::scalar::ScalarValue;
`

// rustArrowType is the Arrow type a declared ssql column kind reads as:
// an int128 is DataFusion's Decimal128(38, 0) (it has no 128-bit
// integer), a decimal:S a Decimal128(38, S); anything else is text (a
// time column reads as text, rustSourceSchema).
func rustArrowType(kind string) string {
	switch {
	case kind == "int":
		return "DataType::Int64"
	case kind == "float":
		return "DataType::Float64"
	case kind == "bool":
		return "DataType::Boolean"
	case kind == "int128":
		return "DataType::Decimal128(38, 0)"
	case strings.HasPrefix(kind, "decimal:"):
		return "DataType::Decimal128(38, " + strings.TrimPrefix(kind, "decimal:") + ")"
	}
	return "DataType::Utf8"
}

// rustMain is the program's main: the session the datafusion oracle
// lane uses (CSV files have a header; a quoted path in an escape-hatch
// SQL is a table), the pipeline, and the sink.
func rustMain(fn string, sink []string) string {
	var sb strings.Builder
	sb.WriteString("#[tokio::main]\nasync fn main() -> Result<()> {\n")
	sb.WriteString("    let config = SessionConfig::new().set_str(\"datafusion.catalog.has_header\", \"true\");\n")
	sb.WriteString("    let ctx = SessionContext::new_with_config(config).enable_url_table();\n")
	fmt.Fprintf(&sb, "    let df = %s(&ctx).await?;\n", fn)
	kind, file := "", ""
	if len(sink) > 0 {
		kind = sink[0]
		for _, a := range sink[1:] {
			if !strings.HasPrefix(a, "-") {
				file = a
				break
			}
		}
	}
	switch {
	case kind == "table":
		sb.WriteString("    df.show().await?;\n")
	case kind == "parquet" && file != "":
		fmt.Fprintf(&sb, "    df.write_parquet(%s, datafusion::dataframe::DataFrameWriteOptions::new().with_single_file_output(true), None).await?;\n", rustStr(file))
	case kind == "csv":
		sb.WriteString(`    let mut w = datafusion::arrow::csv::WriterBuilder::new().with_header(true).build(std::io::BufWriter::new(std::io::stdout().lock()));
    let mut stream = df.execute_stream().await?;
    while let Some(batch) = futures::StreamExt::next(&mut stream).await {
        w.write(&batch?)?;
    }
`)
	default:
		// JSONL on stdout: what `to jsonl` writes and what the
		// equivalence lane reads.
		sb.WriteString(`    let mut w = datafusion::arrow::json::LineDelimitedWriter::new(std::io::BufWriter::new(std::io::stdout().lock()));
    let mut stream = df.execute_stream().await?;
    while let Some(batch) = futures::StreamExt::next(&mut stream).await {
        w.write(&batch?)?;
    }
    w.finish()?;
`)
	}
	sb.WriteString("    Ok(())\n}\n")
	return sb.String()
}

// layer emits the lines that leave the layer's DataFrame in the Rust
// variable name: the inner layer (or source) first, then this layer's
// clauses in SQL evaluation order. A layer whose clauses the renderer
// cannot express as DataFrame calls runs as SQL text over a view of the
// layer below it.
// layer renders q into name; next is the layer that consumes q's rows
// (nil at the top, and for a set-operation side, a join's right side and
// an escape hatch's input, where the renderer makes no claim about what
// the consumer needs).
func (g *rustGen) layer(q *sqlQuery, name string, next *sqlQuery) {
	switch {
	case q.setOp != nil:
		g.setOp(q, name)
	case q.hatch != nil && rustHatchable(q.hatch) && g.plain(hatchRest(q)):
		g.hatch(q, name)
		q = hatchRest(q)
	case q.sqlOnly != "" || !g.plain(q):
		g.escape(q, name)
		return
	case q.inner != nil:
		g.layer(q.inner, name, q)
	case q.source != nil:
		g.source(q.source, name)
	default:
		// No tree below this layer (a from the translator left
		// unstructured): read it as the SQL does.
		g.escape(q, name)
		return
	}
	g.clauses(q, name, next)
}

// orderSensitiveAggs are the aggregates whose result depends on the
// order their input arrives in (the registry's "arrival order": first,
// last, collect, string-agg) or whose ties it breaks (mode, arg-max,
// arg-min). A sort feeding any other aggregate is dead.
var orderSensitiveAggs = map[string]bool{"first": true, "last": true, "collect": true, "string-agg": true, "mode": true, "arg-max": true, "arg-min": true}

// sortIsDead reports whether q's ORDER BY can be left out of the program:
// q selects no rows by it (no LIMIT/OFFSET of its own) and the consumer is
// an aggregate none of whose functions reads arrival order. The SQL
// engines drop the same ORDER BY inside a subquery; the DataFrame API
// keeps a sort it is given — a global merge-sort of 14.6 M rows before a
// single-partition aggregate, 14.4 s where DataFusion's own plan for the
// SQL took 5.9 s (DFC158 §3.6, 2026-10-28).
func sortIsDead(q, next *sqlQuery) bool {
	if q.limit != "" || q.offset != "" || next == nil {
		return false
	}
	if len(next.groupFields) == 0 && len(next.aggs) == 0 {
		return false
	}
	for _, a := range next.aggs {
		if orderSensitiveAggs[a.fn] {
			return false
		}
	}
	return true
}

// plain reports whether every clause of the layer has a structured twin
// the renderer can express; otherwise the layer is an escape hatch.
func (g *rustGen) plain(q *sqlQuery) bool {
	if len(q.conds) != len(q.whereClauses) || len(q.joinNodes) != len(q.joins) {
		return false
	}
	if q.limit != "" {
		if _, err := strconv.ParseInt(q.limit, 10, 64); err != nil {
			return false
		}
	}
	if q.offset != "" {
		if _, err := strconv.ParseInt(q.offset, 10, 64); err != nil {
			return false
		}
	}
	switch {
	case len(q.selectExprs) == 0:
	case len(q.groupFields) > 0 || len(q.aggs) > 0:
		if len(q.selectExprs) != len(q.groupFields)+len(q.aggs) {
			return false
		}
	case q.star != nil:
		if len(q.selectExprs) != 1+len(q.wins)+len(q.assigns) {
			return false
		}
	case q.includeCols != nil:
		if len(q.selectExprs) != len(q.includeCols) {
			return false
		}
	default:
		return false
	}
	return true
}

// source reads a leaf's file(s).
func (g *rustGen) source(s *sqlSource, name string) {
	files := s.files
	format := s.format
	if format == "" && len(files) > 0 {
		switch lower := strings.ToLower(files[0]); {
		case strings.HasSuffix(lower, ".parquet"):
			format = "parquet"
		case strings.HasSuffix(lower, ".json"), strings.HasSuffix(lower, ".jsonl"):
			format = "json"
		default:
			format = "csv"
		}
	}
	paths := rustStr(files[0])
	if len(files) > 1 {
		var quoted []string
		for _, f := range files {
			quoted = append(quoted, rustStr(f))
		}
		paths = "vec![" + strings.Join(quoted, ", ") + "]"
	}
	// The reader's options from the file itself: a .tsv is tab-delimited
	// and a .jsonl is line-delimited JSON, and DataFusion's readers reject
	// a path whose extension is not the one they expect unless told
	// (file_extension) — the SQL file table cannot be told, so the
	// datafusion dialect refuses both (2026-10-30). A declared schema
	// (-type, a sidecar, an integer column past int64) is the reader's
	// whole schema; without one DataFusion infers.
	ext := strings.ToLower(filepath.Ext(files[0]))
	switch format {
	case "parquet":
		g.line("let %s = ctx.read_parquet(%s, ParquetReadOptions::default()).await?;", name, paths)
	case "json", "jsonl":
		opts := "JsonReadOptions::default()"
		if ext != ".json" && ext != "" {
			opts += fmt.Sprintf(".file_extension(%s)", rustStr(ext))
		}
		g.line("let %s = ctx.read_json(%s, %s).await?;", name, paths, opts)
	default:
		opts := "CsvReadOptions::new().has_header(true)"
		if format == "tsv" || ext == ".tsv" {
			opts += ".delimiter(b'\\t')"
		}
		if ext != ".csv" && ext != "" {
			opts += fmt.Sprintf(".file_extension(%s)", rustStr(ext))
		}
		// A declared decimal is read as text and cast after the read: the
		// CSV reader parses a cell with more digits than the scale by
		// TRUNCATING (12.345 → 12.34), where ssql — and DataFusion's own
		// text-to-decimal cast — round half away from zero (12.35). Found
		// by the rust lane's decimal cases on the day the read was lifted
		// (2026-10-30).
		var decimals []sqlPair
		if len(s.schema) > 0 {
			schema := name + "_schema"
			g.line("let %s = Schema::new(vec![", schema)
			for _, c := range s.schema {
				arrow := rustArrowType(c.val)
				if strings.HasPrefix(c.val, "decimal:") {
					decimals = append(decimals, sqlPair{c.col, arrow})
					arrow = "DataType::Utf8"
				}
				g.line("    Field::new(%s, %s, true),", rustStr(c.col), arrow)
			}
			g.line("]);")
			opts += ".schema(&" + schema + ")"
		}
		g.line("let %s = ctx.read_csv(%s, %s).await?;", name, paths, opts)
		for _, d := range decimals {
			g.line("let %s = %s.with_column(%s, cast(col(%s), %s))?;", name, name, rustStr(d.col), rustStr(d.col), d.val)
		}
	}
	if len(s.columns) > 0 {
		g.line("let %s = %s.select_columns(&[%s])?;", name, name, rustStrList(s.columns))
	}
}

// hatchRest is a hatch layer without the clauses its hatch accounts for
// (a keyed except/intersect's EXISTS tests): what the layer's later
// stages added, rendered as any layer's clauses are.
func hatchRest(q *sqlQuery) *sqlQuery {
	rest := *q
	if e := q.hatch.exists; e != nil {
		rest.whereClauses = q.whereClauses[len(e.sides):]
	}
	return &rest
}

// rustHatchable reports whether the hatch's values have a Rust form.
func rustHatchable(h *sqlHatch) bool {
	if h.sample != "" {
		if _, err := strconv.ParseInt(h.sample, 10, 64); err != nil {
			return false
		}
	}
	if h.percent != "" {
		if _, err := strconv.ParseFloat(h.percent, 64); err != nil {
			return false
		}
	}
	return true
}

// hatch builds a layer the SQL renders as text from its structure: the
// inner layer (from range has none), then what the layer does to its
// rows, as DataFrame calls.
func (g *rustGen) hatch(q *sqlQuery, name string) {
	h := q.hatch
	if q.inner != nil {
		g.layer(q.inner, name, nil)
	}
	switch {
	case h.series:
		// The source alone is SQL: DataFusion's DataFrame API reaches
		// generate_series only through a deprecated call that fails at
		// run time (TableFunction::create_table_provider) or a
		// TableFunctionArgs built over the session state, which reads
		// worse than the one SELECT. The stages after it are calls.
		sql := strings.TrimSuffix(strings.TrimPrefix(q.fromClause, "("), ")")
		g.line("let %s = ctx.sql(%s).await?;", name, rustRaw(sql))
	case h.zero != nil:
		for _, p := range h.zero {
			zero := "lit(0i64)"
			if p.val == "FALSE" {
				zero = "lit(false)"
			}
			g.line("let %s = %s.with_column(%s, coalesce(vec![%s, %s]))?;", name, name, rustStr(p.col), rustCol(p.col), zero)
		}
	case h.sample != "":
		g.line("let %s = %s.sort(vec![random().sort(true, false)])?.limit(0, Some(%s))?;", name, name, h.sample)
	case h.percent != "":
		p, _ := strconv.ParseFloat(h.percent, 64)
		frac, _ := rustLit(strconv.FormatFloat(p/100, 'f', -1, 64))
		g.line("let %s = %s.filter(random().lt(%s))?;", name, name, frac)
	case h.unpivot != nil:
		g.unpivot(h.unpivot, name)
	case h.rollup != nil:
		g.rollup(h.rollup, name)
	case h.exists != nil:
		e := h.exists
		kind := "LeftSemi"
		if e.anti {
			kind = "LeftAnti"
		}
		for _, side := range e.sides {
			g.sides++
			s := fmt.Sprintf("s%d", g.sides)
			g.layer(side, s, nil)
			g.line("let %s = %s.join(%s, JoinType::%s, &[%s], &[%s], None)?;", name, name, s, kind, rustStrList(e.leftKey), rustStrList(e.rightKey))
		}
	}
}

// unpivot is one branch per value column — its non-absent cells beside
// the ids, the column's name in u.col — concatenated.
func (g *rustGen) unpivot(u *sqlUnpivot, name string) {
	var branches []string
	for _, v := range u.values {
		g.sides++
		s := fmt.Sprintf("s%d", g.sides)
		var proj []string
		for _, id := range u.ids {
			proj = append(proj, rustCol(id))
		}
		proj = append(proj, fmt.Sprintf("lit(%s).alias(%s)", rustStr(v), rustStr(u.col)), fmt.Sprintf("%s.alias(%s)", rustCol(v), rustStr(u.val)))
		g.line("let %s = %s.clone().filter(%s.is_not_null())?.select(vec![%s])?;", s, name, rustCol(v), strings.Join(proj, ", "))
		branches = append(branches, s)
	}
	expr := branches[0]
	for _, b := range branches[1:] {
		expr += ".union(" + b + ")?"
	}
	g.line("let %s = %s;", name, expr)
}

// rollup aggregates the rows once per grouping set (the full set last,
// the detail rows) and joins each set's totals to the detail rows on the
// set's keys, absent keys matching (IS NOT DISTINCT FROM) — rollupBody's
// shape.
func (g *rustGen) rollup(r *sqlRollup, name string) {
	detail := len(r.sets) - 1
	alias := func(i int) string {
		if i == detail {
			return "__d"
		}
		return fmt.Sprintf("__s%d", i)
	}
	// Each set's DataFrame is bound to the name of its relation alias:
	// shadowed by a second rollup, never read after the joins.
	for i, set := range r.sets {
		var groups, aggs []string
		for _, f := range set {
			groups = append(groups, rustCol(f))
		}
		prefix := ssql.RollupFieldPrefix(set)
		for _, a := range r.aggs {
			a.name = prefix + a.name
			aggs = append(aggs, g.agg(a, name))
		}
		src := name + ".clone()"
		if i == len(r.sets)-1 {
			src = name
		}
		g.line("let %s = %s.aggregate(vec![%s], vec![%s])?.alias(%s)?;", alias(i), src, strings.Join(groups, ", "), strings.Join(aggs, ", "), rustStr(alias(i)))
	}
	from := alias(detail)
	for i, set := range r.sets {
		if i == detail {
			continue
		}
		on := []string{"lit(true)"}
		if len(set) > 0 {
			on = nil
			for _, f := range set {
				on = append(on, fmt.Sprintf("binary_expr(%s, Operator::IsNotDistinctFrom, %s)", rustQualCol("__d", f), rustQualCol(alias(i), f)))
			}
		}
		g.line("let %s = %s.join_on(%s, JoinType::Inner, [%s])?;", name, from, alias(i), strings.Join(on, ", "))
		from = name
	}
	var sel []string
	for _, f := range r.fields {
		sel = append(sel, fmt.Sprintf("%s.alias(%s)", rustQualCol("__d", f), rustStr(f)))
	}
	for i, set := range r.sets {
		prefix := ssql.RollupFieldPrefix(set)
		for _, a := range r.aggs {
			sel = append(sel, fmt.Sprintf("%s.alias(%s)", rustQualCol(alias(i), prefix+a.name), rustStr(prefix+a.name)))
		}
	}
	g.line("let %s = %s.select(vec![%s])?;", name, name, strings.Join(sel, ", "))
}

// escape runs the layer as SQL text over a view of what is below it
// (the escape hatch): the inner layer or source is bound and registered
// as __qN, and the layer's SELECT is rendered with that view as its FROM.
func (g *rustGen) escape(q *sqlQuery, name string) {
	reason := q.sqlOnly
	if reason == "" {
		reason = "a " + g.layerKind(q) + " layer"
	}
	g.escapes = append(g.escapes, reason)
	var sql string
	switch {
	case q.inner != nil:
		g.layer(q.inner, name, nil)
		view := g.view(name)
		copy := *q
		if q.fromTemplate != "" {
			copy.fromClause = strings.ReplaceAll(q.fromTemplate, sqlFromPlaceholder, "SELECT * FROM "+view)
		} else {
			copy.fromClause = view
		}
		sql = renderSelect(&copy)
	case q.source != nil:
		g.source(q.source, name)
		view := g.view(name)
		copy := *q
		copy.fromClause = view
		sql = renderSelect(&copy)
	default:
		// A leaf with no file (from range): the text names its own source.
		sql = renderSelect(q)
	}
	g.line("let %s = ctx.sql(%s).await?;", name, rustRaw(sql))
}

// view registers the DataFrame in name as the next __qN view and returns
// the view's name.
func (g *rustGen) view(name string) string {
	g.views++
	view := fmt.Sprintf("__q%d", g.views)
	g.line("ctx.register_table(%q, %s.into_view())?;", view, name)
	return view
}

func (g *rustGen) layerKind(q *sqlQuery) string {
	switch {
	case len(q.conds) != len(q.whereClauses):
		return "where"
	case len(q.joinNodes) != len(q.joins):
		return "join"
	case len(q.selectExprs) > 0:
		return "projection"
	}
	return "limit"
}

// setOp binds each side and combines them.
func (g *rustGen) setOp(q *sqlQuery, name string) {
	op := q.setOp
	var vars []string
	for _, side := range op.sides {
		g.sides++
		v := fmt.Sprintf("s%d", g.sides)
		g.layer(side, v, nil)
		if op.projectCols != nil {
			g.line("let %s = %s.select_columns(&[%s])?;", v, v, rustStrList(op.projectCols))
		}
		vars = append(vars, v)
	}
	method := map[string]string{"UNION": "union_distinct", "EXCEPT": "except_distinct", "INTERSECT": "intersect_distinct"}[op.op]
	if op.all {
		method = map[string]string{"UNION": "union", "EXCEPT": "except", "INTERSECT": "intersect"}[op.op]
	}
	expr := vars[0]
	for _, v := range vars[1:] {
		expr = fmt.Sprintf("%s.%s(%s)?", expr, method, v)
	}
	g.line("let %s = %s;", name, expr)
}

// clauses emits the layer's clauses in SQL evaluation order.
func (g *rustGen) clauses(q *sqlQuery, name string, next *sqlQuery) {
	for _, j := range q.joinNodes {
		g.join(j, name)
	}
	for _, c := range q.conds {
		e := g.bind(name, g.cond(c, name))
		g.line("let %s = %s.filter(%s)?;", name, name, e)
	}
	switch {
	case len(q.groupFields) > 0 || len(q.aggs) > 0:
		var groups, aggs []string
		for _, f := range q.groupFields {
			groups = append(groups, rustCol(f))
		}
		for _, a := range q.aggs {
			aggs = append(aggs, g.agg(a, name))
		}
		g.line("let %s = %s.aggregate(vec![%s], vec![%s])?;", name, name, strings.Join(groups, ", "), strings.Join(aggs, ", "))
	case q.star != nil:
		if len(q.star.exclude) > 0 {
			g.line("let %s = %s.drop_columns(&[%s])?;", name, name, rustStrList(q.star.exclude))
		}
		for _, p := range q.star.replace {
			e := g.bind(name, rustParse(name, p.val))
			g.line("let %s = %s.with_column(%s, %s)?;", name, name, rustStr(p.col), e)
		}
		for _, p := range q.star.rename {
			g.line("let %s = %s.with_column_renamed(%s, %s)?;", name, name, rustStr(p.col), rustStr(p.val))
		}
		for _, w := range q.wins {
			e := g.bind(name, g.window(w, name))
			g.line("let %s = %s.with_column(%s, %s)?;", name, name, rustStr(w.alias), e)
		}
		for _, a := range q.assigns {
			e := g.bind(name, rustParse(name, a.text))
			g.line("let %s = %s.with_column(%s, %s)?;", name, name, rustStr(a.name), e)
		}
	case q.includeCols != nil:
		g.line("let %s = %s.select_columns(&[%s])?;", name, name, rustStrList(q.includeCols))
	}
	if q.distinct {
		g.line("let %s = %s.distinct()?;", name, name)
	}
	if len(q.orderBy) > 0 && !sortIsDead(q, next) {
		var sorts []string
		for _, o := range q.orderBy {
			term, desc := splitSortTerm(o)
			e := g.bind(name, rustTerm(name, term))
			sorts = append(sorts, fmt.Sprintf("%s.sort(%v, %v)", e, !desc, desc))
		}
		g.line("let %s = %s.sort(vec![%s])?;", name, name, strings.Join(sorts, ", "))
	}
	if q.limit != "" || q.offset != "" {
		skip := "0"
		if q.offset != "" {
			skip = q.offset
		}
		fetch := "None"
		if q.limit != "" {
			fetch = "Some(" + q.limit + ")"
		}
		g.line("let %s = %s.limit(%s, %s)?;", name, name, skip, fetch)
	}
}

// bind binds an expression to a fresh name when it needs the DataFrame's
// schema (parse_sql_expr borrows the DataFrame the next call consumes).
func (g *rustGen) bind(name, expr string) string {
	if !strings.Contains(expr, name+".parse_sql_expr(") {
		return expr
	}
	g.exprs++
	e := fmt.Sprintf("e%d", g.exprs)
	g.line("let %s = %s;", e, expr)
	return e
}

// join binds the right side and joins it. SQL's USING yields one key
// column and DataFusion's join keeps both, so the right side's key is
// dropped; the sides are aliased l and r so the keys can be told apart.
func (g *rustGen) join(j sqlJoin, name string) {
	g.sides++
	r := fmt.Sprintf("s%d", g.sides)
	if j.rightQuery != nil {
		g.layer(j.rightQuery, r, nil)
	} else {
		g.source(&sqlSource{files: []string{j.rightFile}}, r)
	}
	kind := map[string]string{"JOIN": "Inner", "LEFT JOIN": "Left", "RIGHT JOIN": "Right", "FULL JOIN": "Full"}[j.kind]
	if kind == "" {
		kind = "Inner"
	}
	if j.leftKey == "" {
		g.line("let %s = %s.join_on(%s, JoinType::%s, [lit(true)])?;", name, name, r, kind)
		return
	}
	g.line("let %s = %s.alias(\"l\")?;", name, name)
	g.line("let %s = %s.alias(\"r\")?;", r, r)
	g.line("let %s = %s.join(%s, JoinType::%s, &[%s], &[%s], None)?;", name, name, r, kind, rustStr(j.leftKey), rustStr(j.rightKey))
	if j.using {
		g.line("let %s = %s.drop_columns(&[%s])?;", name, name, rustStr("r."+j.rightKey))
	}
}

// cond renders a where stage: builders when every atom has one (a
// positive -if with a plain literal, or -if-field), the stage's SQL text
// otherwise.
func (g *rustGen) cond(c sqlCond, name string) string {
	if c.invert {
		return rustParse(name, c.text)
	}
	var groups []string
	for gi, grp := range c.groups {
		if c.notGrp[gi] {
			return rustParse(name, c.text)
		}
		var atoms []string
		for _, a := range grp {
			e, ok := rustAtom(a)
			if !ok {
				return rustParse(name, c.text)
			}
			atoms = append(atoms, e)
		}
		expr := atoms[0]
		for _, a := range atoms[1:] {
			expr = expr + ".and(" + a + ")"
		}
		if len(atoms) > 1 && len(c.groups) > 1 {
			expr = "(" + expr + ")"
		}
		groups = append(groups, expr)
	}
	expr := groups[0]
	for _, gr := range groups[1:] {
		expr = expr + ".or(" + gr + ")"
	}
	return expr
}

// rustAtom renders one flag condition as builders; false when the atom
// has no builder form (an expression, a negation, a pattern, a regex).
func rustAtom(a sqlAtom) (string, bool) {
	if a.neg || (a.field == "" && !a.isField) {
		return "", false
	}
	method := map[string]string{"eq": "eq", "ne": "not_eq", "gt": "gt", "ge": "gt_eq", "lt": "lt", "le": "lt_eq"}[a.op]
	if method == "" {
		return "", false
	}
	rhs := ""
	if a.isField {
		rhs = rustCol(a.value)
	} else {
		var ok bool
		if rhs, ok = rustLit(a.lit); !ok {
			return "", false
		}
	}
	return fmt.Sprintf("%s.%s(%s)", rustCol(a.field), method, rhs), true
}

// rustLit renders a SQL literal the translator produced as a DataFusion
// literal: a quoted string, TRUE/FALSE, an integer, a float.
func rustLit(sql string) (string, bool) {
	switch {
	case sql == "TRUE":
		return "lit(true)", true
	case sql == "FALSE":
		return "lit(false)", true
	case strings.HasPrefix(sql, "'") && strings.HasSuffix(sql, "'") && len(sql) >= 2:
		return "lit(" + rustStr(strings.ReplaceAll(sql[1:len(sql)-1], "''", "'")) + ")", true
	}
	if _, err := strconv.ParseInt(sql, 10, 64); err == nil {
		return "lit(" + sql + "i64)", true
	}
	if _, err := strconv.ParseFloat(sql, 64); err == nil {
		f := sql
		if !strings.ContainsAny(f, ".eE") {
			f += ".0"
		}
		return "lit(" + f + "f64)", true
	}
	return "", false
}

// agg renders a group-by aggregate: a builder for every registry
// aggregate the datafusion dialect renders as one plain call (the
// registry renders a.expr from the quoted field, so the builder and the
// text name the same function on the same column); the text, parsed by
// DataFusion, for an -expr term.
func (g *rustGen) agg(a sqlAgg, name string) string {
	alias := rustStr(a.name)
	c := rustCol(a.field)
	e := ""
	switch a.fn {
	case "count":
		e = "agg::count(lit(1i64))"
	case "min", "max", "median":
		e = fmt.Sprintf("agg::%s(%s)", a.fn, c)
	case "count-distinct":
		e = "agg::count_distinct(" + c + ")"
	case "avg":
		// A float in every lane (sqlAggAvg's rule).
		e = "cast(agg::avg(" + c + "), DataType::Float64)"
	case "sum":
		// The empty sum is 0 (sqlSumOrZero); an integer column sums as
		// DECIMAL(38,0) so it cannot wrap (sqlSumArg) — the text says
		// which it rendered.
		if strings.Contains(a.expr, "AS DECIMAL(38,0)") {
			c = "cast(" + c + ", DataType::Decimal128(38, 0))"
		}
		e = "coalesce(vec![agg::sum(" + c + "), lit(0i64)])"
	case "collect":
		e = "agg::array_agg(" + c + ")"
	case "first", "any":
		e = "agg::first_value(" + c + ", vec![])"
	case "last":
		e = "agg::last_value(" + c + ", vec![])"
	case "string-agg":
		e = "string_agg(" + c + ", lit(" + rustStr(a.extra) + "))"
	case "stddev":
		e = "agg::stddev(" + c + ")"
	case "variance":
		e = "agg::var_sample(" + c + ")"
	case "percentile":
		// WITHIN GROUP (ORDER BY field): ascending, nulls last.
		if p, err := strconv.ParseFloat(strings.TrimSpace(a.extra), 64); err == nil {
			e = fmt.Sprintf("agg::percentile_cont(%s.sort(true, false), lit(%sf64))", c, strconv.FormatFloat(p, 'f', -1, 64))
		}
	case "arg-max", "arg-min":
		// first_value(field ORDER BY by DESC|ASC), the dialect's form;
		// nulls sort as the largest value, as in the SQL.
		desc := a.fn == "arg-max"
		e = fmt.Sprintf("agg::first_value(%s, vec![%s.sort(%v, %v)])", c, rustCol(a.extra), !desc, desc)
	}
	if e == "" {
		return g.bind(name, rustParse(name, a.expr+" AS "+quoteIdent(a.name)))
	}
	return e + ".alias(" + alias + ")"
}

// window renders a window function as DataFusion's builders: a ranking,
// offset or value function from functions_window, an aggregate over a
// frame as WindowFunction::new(<aggregate>_udaf(), …) — the definition
// DataFusion's SQL planner resolves the same name to (an aggregate
// first, except first_value, last_value and nth_value, which are window
// functions). The translator's text is the fallback: a registry
// aggregate, an ORDER BY over an expression, a time-width RANGE frame.
func (g *rustGen) window(w sqlWin, name string) string {
	text := func() string { return rustParse(name, strings.TrimSuffix(w.text, " AS "+quoteIdent(w.alias))) }
	fn, framed := rustWindowFunc(w)
	if fn == "" {
		return text()
	}
	var parts []string
	if len(w.partition) > 0 {
		var cols []string
		for _, p := range w.partition {
			cols = append(cols, rustCol(p))
		}
		parts = append(parts, ".partition_by(vec!["+strings.Join(cols, ", ")+"])")
	}
	if len(w.orderTerms) > 0 {
		var sorts []string
		for _, o := range w.orderTerms {
			term, desc := splitSortTerm(o)
			e := rustTerm(name, term)
			if strings.Contains(e, "parse_sql_expr") {
				return text()
			}
			sorts = append(sorts, fmt.Sprintf("%s.sort(%v, %v)", e, !desc, desc))
		}
		parts = append(parts, ".order_by(vec!["+strings.Join(sorts, ", ")+"])")
	}
	// The ranking and offset functions ignore the frame, so it is set
	// only where it decides the value.
	if framed {
		frame, ok := rustFrame(w)
		if !ok {
			return text()
		}
		parts = append(parts, ".window_frame("+g.frame(frame)+".clone())")
	}
	e := fn + strings.Join(parts, "") + ".build()?"
	if w.flag == "-avg" && strings.HasPrefix(w.text, "CAST(") {
		// A window average is a float in every lane (sqlAggAvg's rule).
		e = "cast(" + e + ", DataType::Float64)"
	}
	return e
}

// frame binds a window frame once (a clause's functions share it) and
// returns its name.
func (g *rustGen) frame(ctor string) string {
	if v, ok := g.frames[ctor]; ok {
		return v
	}
	if g.frames == nil {
		g.frames = map[string]string{}
	}
	v := fmt.Sprintf("frame%d", len(g.frames)+1)
	g.frames[ctor] = v
	g.line("let %s = %s;", v, ctor)
	return v
}

// rustWindowFunc renders the function a window flag names, before its
// OVER clause, and whether its value depends on the frame; "" when the
// flag has no builder form.
func rustWindowFunc(w sqlWin) (fn string, framed bool) {
	n := len(w.args)
	field := ""
	if n > 1 {
		field = rustCol(w.args[0])
	}
	udaf := func(def, arg string) string {
		return "Expr::from(WindowFunction::new(" + def + "(), vec![" + arg + "]))"
	}
	switch w.flag {
	case "-row-number":
		return "win::row_number()", false
	case "-rank":
		return "win::rank()", false
	case "-dense-rank":
		return "win::dense_rank()", false
	case "-percent-rank":
		return "win::percent_rank()", false
	case "-cume-dist":
		return "win::cume_dist()", false
	case "-ntile":
		if _, err := strconv.ParseInt(w.args[0], 10, 64); n == 2 && err == nil {
			return "win::ntile(lit(" + w.args[0] + "i64))", false
		}
	case "-lag", "-lead":
		if _, err := strconv.ParseInt(w.args[1], 10, 64); n == 3 && err == nil {
			return fmt.Sprintf("win::%s(%s, Some(%s), None)", w.flag[1:], field, w.args[1]), false
		}
	case "-lag-default", "-lead-default":
		if _, err := strconv.ParseInt(w.args[1], 10, 64); n == 4 && err == nil {
			if d, ok := rustScalar(windowDefaultLiteral(w.args[2])); ok {
				return fmt.Sprintf("win::%s(%s, Some(%s), Some(%s))", strings.TrimSuffix(w.flag[1:], "-default"), field, w.args[1], d), false
			}
		}
	case "-first":
		return "win::first_value(" + field + ")", true
	case "-last":
		return "win::last_value(" + field + ")", true
	case "-nth-value":
		if _, err := strconv.ParseInt(w.args[1], 10, 64); n == 3 && err == nil {
			return "win::nth_value(" + field + ", " + w.args[1] + ")", true
		}
	case "-count":
		return "count_all_window()", true
	case "-count-field":
		return udaf("count_udaf", field), true
	case "-sum":
		// An integer column sums as DECIMAL(38,0) (sqlSumArg).
		if strings.Contains(w.fn, "AS DECIMAL(38,0)") {
			field = "cast(" + field + ", DataType::Decimal128(38, 0))"
		}
		return udaf("sum_udaf", field), true
	case "-avg":
		return udaf("avg_udaf", field), true
	case "-min":
		return udaf("min_udaf", field), true
	case "-max":
		return udaf("max_udaf", field), true
	}
	return "", false
}

// rustFrame renders the clause's frame as DataFusion's SQL planner
// builds it: ROWS offsets as UInt64, RANGE offsets as text the analyzer
// casts to the ORDER BY column's type, an unbounded side as a null. A
// time-width RANGE frame stays text (its ORDER BY is a cast and its
// offset an interval).
func rustFrame(w sqlWin) (string, bool) {
	units := "Rows"
	bound := func(n int, side string) string {
		switch {
		case n < 0:
			return "WindowFrameBound::" + side + "(ScalarValue::UInt64(None))"
		case n == 0:
			return "WindowFrameBound::CurrentRow"
		}
		return fmt.Sprintf("WindowFrameBound::%s(ScalarValue::UInt64(Some(%d)))", side, n)
	}
	start, end := bound(w.preceding, "Preceding"), bound(w.following, "Following")
	if w.rangeP != "" || w.rangeF != "" {
		units = "Range"
		rangeBound := func(s, side string) (string, bool) {
			if s == "" {
				return "WindowFrameBound::CurrentRow", true
			}
			v, isTime, err := parseRangeBound(s)
			switch {
			case err != nil || isTime:
				return "", false
			case v < 0:
				return "WindowFrameBound::" + side + "(ScalarValue::UInt64(None))", true
			case v == 0:
				return "WindowFrameBound::CurrentRow", true
			}
			return fmt.Sprintf("WindowFrameBound::%s(ScalarValue::from(%q))", side, strconv.FormatFloat(v, 'f', -1, 64)), true
		}
		var ok1, ok2 bool
		start, ok1 = rangeBound(w.rangeP, "Preceding")
		end, ok2 = rangeBound(w.rangeF, "Following")
		if !ok1 || !ok2 {
			return "", false
		}
	}
	return fmt.Sprintf("WindowFrame::new_bounds(WindowFrameUnits::%s, %s, %s)", units, start, end), true
}

// rustScalar renders a typed CLI literal (windowDefaultLiteral) as a
// ScalarValue.
func rustScalar(v any) (string, bool) {
	switch x := v.(type) {
	case int64:
		return fmt.Sprintf("ScalarValue::from(%di64)", x), true
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return "", false
		}
		f := strconv.FormatFloat(x, 'f', -1, 64)
		if !strings.Contains(f, ".") {
			f += ".0"
		}
		return "ScalarValue::from(" + f + "f64)", true
	case bool:
		return fmt.Sprintf("ScalarValue::from(%v)", x), true
	case string:
		return "ScalarValue::from(" + rustStr(x) + ")", true
	}
	return "", false
}

// splitSortTerm splits an ORDER BY term into its expression and direction.
func splitSortTerm(o string) (term string, desc bool) {
	switch {
	case strings.HasSuffix(o, " DESC"):
		return strings.TrimSuffix(o, " DESC"), true
	case strings.HasSuffix(o, " ASC"):
		return strings.TrimSuffix(o, " ASC"), false
	}
	return o, false
}

// rustTerm renders a SQL term that is either a quoted column or an
// expression.
func rustTerm(name, term string) string {
	if col, ok := unquoteIdent(term); ok {
		return rustCol(col)
	}
	return rustParse(name, term)
}

// unquoteIdent returns the column a quoted identifier names.
func unquoteIdent(s string) (string, bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	inner := s[1 : len(s)-1]
	if strings.Contains(strings.ReplaceAll(inner, `""`, ""), `"`) {
		return "", false
	}
	return strings.ReplaceAll(inner, `""`, `"`), true
}

// rustParse hands a SQL expression to DataFusion's parser against the
// DataFrame's schema.
func rustParse(name, sql string) string {
	return name + ".parse_sql_expr(" + rustRaw(sql) + ")?"
}

// rustCol renders a column reference. col("a.b") would parse as table
// a's column b (the path_literal_dot_wins case); a name with a dot or a
// quote is given to Column::from_name, which takes it whole.
func rustCol(field string) string {
	if strings.ContainsAny(field, ".\"") {
		return "col(Column::from_name(" + rustStr(field) + "))"
	}
	return "col(" + rustStr(field) + ")"
}

// rustQualCol renders relation rel's column field.
func rustQualCol(rel, field string) string {
	if strings.ContainsAny(field, ".\"") {
		return fmt.Sprintf("col(Column::new(Some(%s), %s))", rustStr(rel), rustStr(field))
	}
	return "col(" + rustStr(rel+"."+field) + ")"
}

// rustStr renders a Go string as a Rust string literal.
func rustStr(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\t':
			sb.WriteString(`\t`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&sb, `\u{%x}`, r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// rustStrList renders a &[&str] body.
func rustStrList(items []string) string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = rustStr(s)
	}
	return strings.Join(out, ", ")
}

// rustRaw renders a Rust raw string literal, with as many # as the text
// needs so no `"#…` inside it ends the literal.
func rustRaw(s string) string {
	hashes := "#"
	for strings.Contains(s, `"`+hashes) {
		hashes += "#"
	}
	return "r" + hashes + `"` + s + `"` + hashes
}

var rustIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// --- cargo: -run and -build ---

// rustCargo finds cargo: PATH, then the rustup default location, or
// SSQL_CARGO.
func rustCargo() (string, error) {
	if c := os.Getenv("SSQL_CARGO"); c != "" {
		return c, nil
	}
	if c, err := exec.LookPath("cargo"); err == nil {
		return c, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		c := filepath.Join(home, ".cargo", "bin", "cargo")
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("ssql generate rust: cargo not found on PATH or at ~/.cargo/bin/cargo (install Rust: https://rustup.rs) — or name it with SSQL_CARGO")
}

// rustCacheDir is where the dependency build persists between runs:
// $XDG_CACHE_HOME/ssql/rust or ~/.cache/ssql/rust.
func rustCacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "ssql", "rust")
	return dir, os.MkdirAll(dir, 0o755)
}

// rustCargoToml is the generated program's crate manifest.
func rustCargoToml() string {
	return fmt.Sprintf(`[package]
name = "ssqlgen"
version = "0.1.0"
edition = "2021"

[dependencies]
datafusion = "%s"
tokio = { version = "1", features = ["rt-multi-thread", "macros"] }
futures = "0.3"

[profile.release]
opt-level = 3
`, rustDataFusionCrate)
}

// compileRustSource writes the program into a crate of its own (so two
// invocations never overwrite each other's main.rs) built under the
// shared cache target directory (so DataFusion compiles once), and
// returns the crate directory and the binary's path.
func compileRustSource(code, outPath string) (crateDir, binPath string, err error) {
	cargo, err := rustCargo()
	if err != nil {
		return "", "", err
	}
	cache, err := rustCacheDir()
	if err != nil {
		return "", "", fmt.Errorf("ssql generate rust: cache dir: %w", err)
	}
	target := filepath.Join(cache, "target")
	dir, err := os.MkdirTemp(cache, "crate-*")
	if err != nil {
		return "", "", fmt.Errorf("creating crate dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(rustCargoToml()), 0o644); err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.rs"), []byte(code), 0o644); err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	if _, err := os.Stat(filepath.Join(target, "release")); err != nil {
		fmt.Fprintf(os.Stderr, "ssql generate rust: first build compiles DataFusion %s itself (several minutes, about 1.3 GB under %s); later builds relink in seconds\n", rustDataFusionCrate, target)
	}
	build := exec.Command(cargo, "build", "--release", "--quiet")
	build.Dir = dir
	build.Env = append(os.Environ(), "CARGO_TARGET_DIR="+target)
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("ssql generate rust: cargo build failed: %w", err)
	}
	built := filepath.Join(target, "release", "ssqlgen")
	if outPath == "" {
		return dir, built, nil
	}
	data, err := os.ReadFile(built)
	if err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	if err := os.WriteFile(outPath, data, 0o755); err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("ssql generate rust -build: %w", err)
	}
	return dir, outPath, nil
}

// runRustSource compiles and runs the program from the user's cwd (so
// relative file paths in the pipeline resolve as they do for the CLI).
func runRustSource(code string, timeRun bool) error {
	compileStart := time.Now()
	dir, bin, err := compileRustSource(code, "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	compileDur := time.Since(compileStart)
	run := exec.Command(bin)
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	runStart := time.Now()
	runErr := run.Run()
	if timeRun {
		fmt.Fprintf(os.Stderr, "[ssql: compiled in %s, ran in %s]\n", roundDur(compileDur), roundDur(time.Since(runStart)))
	}
	return runErr
}

// buildRustSource compiles the program to outPath and exits.
func buildRustSource(code, outPath string) error {
	abs, err := filepath.Abs(outPath)
	if err != nil {
		return fmt.Errorf("ssql generate rust -build: resolving output path: %w", err)
	}
	dir, _, err := compileRustSource(code, abs)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
