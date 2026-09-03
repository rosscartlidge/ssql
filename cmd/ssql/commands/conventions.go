package commands

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
)

// RegisterConventions registers the `conventions` subcommand — an in-binary
// reference for cross-cutting system semantics that span commands and tend to
// surprise people (and that no single command's -help would cover). Sibling of
// `ssql functions` (which documents the expression language).
func RegisterConventions(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("conventions").
		Description("Document system-wide conventions and semantics that span commands").
		Example("ssql conventions", "List all convention categories").
		Example("ssql conventions -category evaluation", "How update/filter expressions are evaluated").
		Example("ssql conventions -category data", "Schema header, numeric types, field ordering").
		Flag("-category", "-c").
		String().
		Completer(&cf.StaticCompleter{Options: []string{"evaluation", "data", "pipeline", "codegen"}}).
		Global().
		Default("").
		Help("Show detailed help for a category").
		Done().
		Handler(func(ctx *cf.Context) error {
			category := ""
			if cat, ok := ctx.GlobalFlags["-category"]; ok {
				category = cat.(string)
			}
			if category == "" {
				fmt.Fprint(ctx.Stdout(), ConventionsReference)
				return nil
			}
			return printConventionCategory(ctx, category)
		}).
		Done()
	return cmd
}

// ConventionsReference is the concise overview printed by `ssql conventions`
// (no args). Single source of truth; keep in sync with the per-category detail
// below and the deeper docs (doc/EXPRESSIONS.md, CLAUDE.md). Scope: cross-command
// behaviors that surprise people — NOT per-command help (use -help / Alt-h) and
// NOT the expression language (use `ssql functions`).
const ConventionsReference = `SSQL CONVENTIONS (system-wide semantics):

Evaluation:
  - update: every -set / -set-expr / -if in ONE update sees the ORIGINAL row
    (a snapshot), like SQL "UPDATE … SET" — assignments do NOT see each other.
    Pipe to sequence:  … | ssql update -set t 100 | ssql update -set-expr u 't+1'
  - where: -if clauses are AND within a clause, OR across +/- separators.
  - missing fields: reads use GetOr defaults; in expressions use has()/getOr()/??.

Data model:
  - JSONL carries a "_schema" header (field names + types). Commands that take
    file inputs (join/merge/union) need schema-header JSONL — wrap plain files
    as <(ssql from jsonl FILE) to add it.
  - canonical numeric types are int64 and float64 (CSV auto-parses to these).
  - field order: record mode is alphabetical; typed mode keeps struct order.

Pipeline:
  - every data command reads stdin and writes stdout (Unix pipelines).
  - process substitution feeds a second source: join <(ssql from FILE) … .

Code generation:
  - SSQL_MODE selects the codegen path: record | typed | schema.
  - generate go | sql | ssql all consume the same fragment stream and read NO
    data — they operate on the pipeline structure, so they're instant.

Use: ssql conventions -category <name>   # evaluation | data | pipeline | codegen

Full reference: doc/EXPRESSIONS.md, doc/cli-codelab.md, doc/api-reference.md
`

func printConventionCategory(ctx *cf.Context, category string) error {
	switch strings.ToLower(category) {
	case "evaluation", "eval":
		fmt.Fprint(ctx.Stdout(), conventionEvaluation)
	case "data", "data-model", "schema":
		fmt.Fprint(ctx.Stdout(), conventionData)
	case "pipeline":
		fmt.Fprint(ctx.Stdout(), conventionPipeline)
	case "codegen", "code":
		fmt.Fprint(ctx.Stdout(), conventionCodegen)
	default:
		fmt.Fprintf(ctx.Stdout(), "Unknown category: %s\n\n", category)
		fmt.Fprintln(ctx.Stdout(), "Available categories: evaluation, data, pipeline, codegen")
	}
	return nil
}

const conventionEvaluation = `EVALUATION SEMANTICS:

update — all assignments see the ORIGINAL row (SQL SET semantics)
  Every -set / -set-expr / -if in a single 'update' evaluates against the row
  as it entered the command — a snapshot taken once. Assignments do NOT see one
  another, and there is no left-to-right dependency.

    ssql update -set x 12 -set-expr x 'x * 2'     # x = (original x) * 2, NOT 24
    ssql update -set t 100 -set-expr u 't + 1'    # ERROR: 't' is unknown here

  This matches SQL: UPDATE t SET x = 12, y = x*2  uses the OLD x for y.
  (If the same field is set by both a literal and an expression, the expression
  wins regardless of order — so don't set a field twice in one update.)

  To make a value visible to later work, pipe into a SECOND update — the pipe
  boundary is where the new value becomes available:

    … | ssql update -set t 100 | ssql update -set-expr u 't + 1'   # u = 101

where — clauses combine AND within, OR across separators
  Flags within one clause are ANDed; clauses split by + / - are ORed.

    ssql where -if age ge 18 -if age le 65          # age>=18 AND age<=65
    ssql where -if dept eq sales + -if dept eq eng  # dept=sales OR dept=eng

missing fields
  Reads use a default when a field is absent (GetOr). In expressions, guard with
  has("field"), getOr("field", default), or the ?? nil-coalescing operator.
`

const conventionData = `DATA MODEL:

JSONL "_schema" header
  ssql JSONL pipelines carry a leading {"_schema": …} line recording field
  names and types. Commands that read FILES (join, merge, union) require
  schema-header JSONL so they don't silently lose field info — wrap a plain
  file as a process substitution to add the header:

    ssql join <(ssql from jsonl plain.jsonl) -using id      # adds the _schema
    ssql from jsonl plain.jsonl                             # only 'from' takes plain JSONL

canonical numeric types
  Scalars are int64 and float64 (never int/int32/float32). CSV auto-parsing
  produces int64 / float64; use int64(0) / float64(0) as GetOr defaults.

field ordering
  Record mode emits fields alphabetically; typed mode (generate go) keeps the
  struct field order. Both contain the same fields — only the column order of
  some sinks differs.
`

const conventionPipeline = `PIPELINE:

stdin / stdout
  Every data command reads stdin and writes stdout, so commands compose with
  ordinary Unix pipes:  ssql from data.csv | ssql where … | ssql to table

process substitution
  A second data source is fed with <( … ):

    ssql from a.csv | ssql join <(ssql from b.csv) -on a_id id

  (Ctrl-O completes the join's right-side fields from the procsub — see the
  shell integration: eval "$(ssql -shell-init)".)
`

const conventionCodegen = `CODE GENERATION:

SSQL_MODE selects the codegen path
  record   — map[string]any rows (ssql.Record).
  typed    — the planner picks Stream[T] + parallel primitives where reachable,
             else the serial iter.Seq[T] form.
  schema   — transforms the schema header only (no data) — powers completion
             and 'generate schema'.

generate go | sql | ssql
  All three consume the same codegen FRAGMENT stream and read NO data — they
  operate on the pipeline structure, so they are instant even on huge files:

    (export SSQL_MODE=record; ssql from x.csv | ssql where -if a gt 1 | ssql to table) \
      | ssql generate sql | duckdb

  Authoring help: Alt-g shows the generated Go; Alt-r compiles and runs it;
  Ctrl-T optimises the pipeline in place (all via eval "$(ssql -shell-init)").
`
