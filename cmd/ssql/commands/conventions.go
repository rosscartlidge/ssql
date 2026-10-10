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
		Example("ssql conventions -category nested", "Lists and objects in fields: json type, dotted paths, explode/flatten").
		Flag("-category", "-c").
		String().
		Completer(&cf.StaticCompleter{Options: []string{"evaluation", "data", "pipeline", "nested", "codegen"}}).
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
  - time: a "time" field is an instant (RFC 3339 on the wire, keeping the
    offset it came with; compared and sorted as instants). Reading its
    calendar — hour, day, week, month truncation, formatting — uses a zone
    chosen ONCE where the stream starts (from … -tz ZONE, else TZ, else
    /etc/localtime, as date(1) and DuckDB do) and carried in the _schema
    header; later commands read the header, never the environment. TZ=UTC
    pins a run; tz(ts, "Australia/Sydney") picks a zone for one expression
    (DFC147). bucket and resample snap to the Unix epoch, zone-free.

Pipeline:
  - every data command reads stdin and writes stdout (Unix pipelines).
  - process substitution feeds a second source: join <(ssql from FILE) … .

Structured values (lists and objects inside a field):
  - ONE representation: a nested value is a "json" field holding its JSON text;
    it passes through every command untouched and prints as it came in.
  - dotted paths name what is inside wherever a field name goes: addr.city,
    tags.0, tags.-1 (where, sort, group-by, include, to table …). A literal
    field whose name contains a dot wins over the path reading.
  - in expressions a nested value IS its list or object: len(tags), tags[0],
    "go" in tags, addr.city, sort/filter/map/uniq/join; a list or object
    result is stored as json. A missing object is nil: test with addr != nil.
  - explode FIELD makes one row per list element (empty/missing → no rows;
    -keep-empty → one null row); flatten FIELD makes an object's keys columns
    FIELD.key; group-by -collect is the way back to a list per group.
  - aggregates do not nest: in group-by -expr the outer sum/max/avg/count is
    over the group and inside it the same names are list functions over the
    row's own list — sum(sum(scores)) totals every list; sum(scores) adds
    lists and errors. Outside an aggregate a bare list field is the group's
    array of lists (len(flatten(scores))).

Code generation:
  - SSQL_MODE selects the codegen path: record | typed | schema.
  - generate go | sql | ssql all consume the same fragment stream and read NO
    data — they operate on the pipeline structure, so they're instant.

Use: ssql conventions -category <name>   # evaluation | data | pipeline | nested | codegen

Full reference: doc/EXPRESSIONS.md, doc/cli-codelab.md, doc/cli-nested-data.md, doc/api-reference.md
`

func printConventionCategory(ctx *cf.Context, category string) error {
	switch strings.ToLower(category) {
	case "evaluation", "eval":
		fmt.Fprint(ctx.Stdout(), conventionEvaluation)
	case "data", "data-model", "schema":
		fmt.Fprintf(ctx.Stdout(), "%s", conventionData) // the text has a strftime %d
	case "pipeline":
		fmt.Fprint(ctx.Stdout(), conventionPipeline)
	case "nested", "structured", "json":
		fmt.Fprint(ctx.Stdout(), conventionNested)
	case "codegen", "code":
		fmt.Fprint(ctx.Stdout(), conventionCodegen)
	default:
		fmt.Fprintf(ctx.Stdout(), "Unknown category: %s\n\n", category)
		fmt.Fprintln(ctx.Stdout(), "Available categories: evaluation, data, pipeline, nested, codegen")
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
  A CSV or TSV column's type is inferred from the first 1000 rows (the
  narrowest of int, float, bool, string that every non-empty value fits);
  a later cell that does not fit is an ERROR naming the row and column —
  never a silent 0 — in exec AND in generated code (typed readers fail
  the same way). Override with: from csv|tsv FILE -type COLUMN TYPE.
  JSONL lines type themselves; a column that is int on one line and float
  on another is fixed with: from jsonl FILE -type FIELD TYPE.

field ordering
  Record mode emits fields alphabetically; typed mode (generate go) keeps the
  struct field order. Both contain the same fields — only the column order of
  some sinks differs.

time and zones
  A "time" field is an instant. On the wire it is RFC 3339 with the offset
  it came with (2026-01-05T10:30:00+11:00 and 2026-01-04T23:30:00Z are the
  SAME value: equal, and sorted together). It comes from cast -type F time,
  from csv -type F time, a schema sidecar, or a _schema header; the forms
  read are RFC 3339, "2026-01-05 10:30:00", a bare date (midnight UTC) and
  Unix seconds.

  Reading the calendar of an instant — hour(ts), day(ts), week(ts),
  trunc(ts, "month"), format(ts, "%Y-%m-%d"), hour(now()) — needs a zone.
  The zone is chosen ONCE, where the stream starts, and travels with it:
  the source command (from, from ssh, a catalog) takes -tz ZONE, else the
  TZ environment variable, else /etc/localtime — exactly as date(1), Go
  and DuckDB resolve it — and writes the name into the _schema header.
  Every later command reads the header, never its own environment, so a
  pipeline has one zone from end to end, tee keeps it, and a remote or
  server-side stage cannot leak its machine's zone in. So:

    ssql from data.csv -tz Australia/Sydney | …   # say it in the pipeline
    TZ=UTC ssql from data.csv | …                  # pin a run (CI)
    hour(tz(ts, "Australia/Sydney"))              # one zone for one expression

  Everything that serialises a pipeline freezes the zone into text:
  generate sql writes SET TimeZone = 'Australia/Sydney' by name, generate
  go a time.LoadLocation literal, generate json/ssql a -tz on the from
  stage — so the output gives the same answer wherever it runs and says
  what it assumed. Environments without a shell (the WASM playground,
  serve, a JSON pipeline document) supply -tz the same way. The rule
  behind it: an environment variable that changes RESULTS must have a flag
  and travel in the stream; one that changes what runs (SSQL_MODE) may stay
  in the environment. The date functions are DFC147
  (doc/research/dfc147_date_time_functions.md).

  bucket(ts, "5m"), -set-bucket and resample snap to the Unix epoch and do
  not depend on the zone; trunc(ts, "day") does (local midnight).
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

const conventionNested = `STRUCTURED VALUES (lists and objects inside a field):

one representation: the json type
  A nested value — a list or an object in a JSON field, a -collect result, a
  list-valued expression — is a field of type "json" holding its JSON text,
  from every reader (from json, from jsonl, cast -type F json) alike. It
  passes through every command untouched and 'to json' writes it back as
  JSON, not as a quoted string. The schema header says json; typed codegen
  reads it as a Go string of the text.

dotted paths wherever a field name goes
  addr.city, tags.0, tags.-1, a.b.c name what is inside a nested value in
  any field position: where -if, sort, group-by, include, join -on, window,
  describe, to table … A path is READ anywhere but WRITTEN nowhere: as a
  target of cast -type, rename -as, exclude, fill or update -set it is
  refused by name (flatten first, or rebuild the object with -set-expr).
  A literal field whose name contains a dot wins over the path reading.
  Schema mode and Ctrl-O list an object's keys as completable paths (one
  level); a list index is typed by hand.

    ssql from events.jsonl | ssql where -if addr.city eq NYC | ssql group-by addr.city -count n

expressions see the list or object
  Inside -if-expr / -set-expr / -expr a nested value IS its list or map, so
  the expression language applies: len(tags), tags[0], "go" in tags,
  addr.city, sort(scores)[-1], filter(scores, # >= 9), map, uniq, join.
  A result that is a list or an object is stored as a json value, ready for
  the next stage to open or explode. A missing object is nil (addr != nil
  is the "has an address" test); indexing an empty list is an error, so
  filter first — ssql names the expression rather than inventing a value.

explode and flatten: changing shape on purpose
  explode FIELD        one row per list element; the other fields repeat.
                       An empty or missing list gives NO rows; -keep-empty
                       gives one row with a null.
  flatten FIELD        an object's keys become sibling columns FIELD.key
                       (-depth N for nested objects, -keep keeps FIELD); the
                       keys are fixed by the first row.
  group-by -collect    the way back: a list per group, stored as json.
  Explode or flatten early when you will WORK on the values (joins, windows,
  aggregates and sorts are built for flat rows); leave a value that is only
  passing through alone.

aggregates do not nest (group-by -expr)
  The outer sum/count/avg/min/max/first/last aggregates over the group and
  its argument is per-row code: inside it the same names are the ordinary
  list functions over that row's own list, and a closure's # is its own.

    sum(sum(scores))                      total of every row's list
    max(len(tags))                        the widest list
    avg(sum(map(items, #.qty * #.price))) mean order value, no explode needed
    sum(scores)                           ERROR: adds lists — nest a sum

  Outside an aggregate a bare list field is the group's ARRAY of lists:
  len(flatten(scores)) counts every score, len(filter(scores, sum(#) > 10))
  counts rows.

what generate sql translates
  DuckDB: explode (UNNEST), member access, in, len and the list functions.
  Refused by name: flatten, explode -keep-empty, a dotted path in a field
  position, group-by -expr over nested values. Postgres and DataFusion
  refuse nested values. The Go lanes (record, typed, parallel) run all of it.

Codelab: doc/cli-nested-data.md   Design: doc/research/dfc144_non_scalar_values.md
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
