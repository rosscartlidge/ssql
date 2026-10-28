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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
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
	prev, prevLoads := sqlDialectCur, pgLoads
	sqlDialectCur, pgLoads = dialectDataFusion, nil
	defer func() { sqlDialectCur, pgLoads = prev, prevLoads }()
	q, err := assembleQuery(input)
	if err != nil {
		return "", err
	}
	return renderRust(q, funcName), nil
}

// rustGen accumulates the pipeline function's body.
type rustGen struct {
	lines   []string
	views   int      // escape-hatch views registered (__q1, __q2, …)
	sides   int      // side DataFrames bound (s1, s2, …)
	exprs   int      // expression bindings (e1, e2, …)
	escapes []string // the reasons layers ran as SQL text, for the header
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
	g.layer(q, "df")
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
		sb.WriteString(rustImports)
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

const rustImports = `use datafusion::arrow::datatypes::DataType;
use datafusion::error::Result;
use datafusion::functions_aggregate::expr_fn as agg;
use datafusion::functions_window::expr_fn as win;
use datafusion::prelude::*;
`

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
		fmt.Fprintf(&sb, "    df.write_parquet(%s, DataFrameWriteOptions::new().with_single_file_output(true), None).await?;\n", rustStr(file))
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
func (g *rustGen) layer(q *sqlQuery, name string) {
	switch {
	case q.setOp != nil:
		g.setOp(q, name)
	case q.sqlOnly != "" || !g.plain(q):
		g.escape(q, name)
		return
	case q.inner != nil:
		g.layer(q.inner, name)
	case q.source != nil:
		g.source(q.source, name)
	default:
		// No tree below this layer (a from the translator left
		// unstructured): read it as the SQL does.
		g.escape(q, name)
		return
	}
	g.clauses(q, name)
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
	switch format {
	case "parquet":
		g.line("let %s = ctx.read_parquet(%s, ParquetReadOptions::default()).await?;", name, paths)
	case "json", "jsonl":
		g.line("let %s = ctx.read_json(%s, JsonReadOptions::default()).await?;", name, paths)
	default:
		g.line("let %s = ctx.read_csv(%s, CsvReadOptions::new().has_header(true)).await?;", name, paths)
	}
	if len(s.columns) > 0 {
		g.line("let %s = %s.select_columns(&[%s])?;", name, name, rustStrList(s.columns))
	}
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
		g.layer(q.inner, name)
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
		g.layer(side, v)
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
func (g *rustGen) clauses(q *sqlQuery, name string) {
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
	if len(q.orderBy) > 0 {
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
		g.layer(j.rightQuery, r)
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

// agg renders a group-by aggregate: builders for the common flags, the
// registry's SQL text (parsed by DataFusion) for the rest.
func (g *rustGen) agg(a sqlAgg, name string) string {
	alias := rustStr(a.name)
	switch a.fn {
	case "count":
		return "agg::count(lit(1i64)).alias(" + alias + ")"
	case "min", "max":
		return fmt.Sprintf("agg::%s(%s).alias(%s)", a.fn, rustCol(a.field), alias)
	case "count-distinct":
		return fmt.Sprintf("agg::count_distinct(%s).alias(%s)", rustCol(a.field), alias)
	case "avg":
		// A float in every lane (sqlAggAvg's rule).
		return fmt.Sprintf("cast(agg::avg(%s), DataType::Float64).alias(%s)", rustCol(a.field), alias)
	case "sum":
		// The empty sum is 0 (sqlSumOrZero); an integer column sums as
		// DECIMAL(38,0) so it cannot wrap (sqlSumArg) — the text says
		// which it rendered.
		arg := rustCol(a.field)
		if strings.Contains(a.expr, "AS DECIMAL(38,0)") {
			arg = "cast(" + arg + ", DataType::Decimal128(38, 0))"
		}
		return fmt.Sprintf("coalesce(vec![agg::sum(%s), lit(0i64)]).alias(%s)", arg, alias)
	}
	return g.bind(name, rustParse(name, a.expr+" AS "+quoteIdent(a.name)))
}

// window renders a window function: builders for the ranking and offset
// functions by flag, the translator's text for the rest (an aggregate
// over a frame needs DataFusion's WindowFunction constructor).
func (g *rustGen) window(w sqlWin, name string) string {
	fn := ""
	switch w.flag {
	case "-row-number":
		fn = "win::row_number()"
	case "-rank":
		fn = "win::rank()"
	case "-dense-rank":
		fn = "win::dense_rank()"
	case "-percent-rank":
		fn = "win::percent_rank()"
	case "-cume-dist":
		fn = "win::cume_dist()"
	case "-ntile":
		if len(w.args) == 2 {
			if _, err := strconv.ParseInt(w.args[0], 10, 64); err == nil {
				fn = "win::ntile(lit(" + w.args[0] + "i64))"
			}
		}
	case "-lag", "-lead":
		if len(w.args) == 3 {
			if _, err := strconv.ParseInt(w.args[1], 10, 64); err == nil {
				fn = fmt.Sprintf("win::%s(%s, Some(%s), None)", w.flag[1:], rustCol(w.args[0]), w.args[1])
			}
		}
	}
	// The ranking and offset functions ignore the frame (it is in the
	// text for the clause's aggregates), so a frame does not stop the
	// builder form.
	if fn == "" {
		text := strings.TrimSuffix(w.text, " AS "+quoteIdent(w.alias))
		return rustParse(name, text)
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
				text := strings.TrimSuffix(w.text, " AS "+quoteIdent(w.alias))
				return rustParse(name, text)
			}
			sorts = append(sorts, fmt.Sprintf("%s.sort(%v, %v)", e, !desc, desc))
		}
		parts = append(parts, ".order_by(vec!["+strings.Join(sorts, ", ")+"])")
	}
	return fn + strings.Join(parts, "") + ".build()?"
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
