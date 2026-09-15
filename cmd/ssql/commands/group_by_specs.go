package commands

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
)

// aggDef is the single description of one built-in aggregation flag
// (DFC129 §6). Every lane derives what it needs from this table —
// flag decoding (parseAggSpecs), the exec AggregateFunc
// (buildAggregator), the record-codegen call (generateAggregatorCode),
// the result's wire type (aggWireType), the SQL function
// (generate_sql.go), and the flag arity the optimiser and the schema
// walker use to step over a group-by stage. Adding an aggregate is one
// entry here plus its typed accumulator kind; before this table a flag
// touched nine files and could exist in four lanes but not the fifth.
type aggDef struct {
	flag     string // "-sum"
	fn       string // aggSpec.function: "sum"
	hasField bool   // false for -count, which takes only a result name
	extraArg string // name of a third argument between field and result ("sep" for -string-agg), or ""
	// sql renders the aggregate for `generate sql` from the quoted field
	// identifier and the extra argument (DuckDB dialect).
	sql func(quotedField, extra string) string
	// wireType is the `_schema` type of the result given the input
	// field's wire type ("" when unknown or fieldless).
	wireType func(fieldType string) string
	build    func(field, extra string) ssql.AggregateFunc // exec
	code     func(field, extra string) string            // record codegen expression
	// typedKind names the accumulator shape the typed lane emits
	// (typed_groupby.go): "" = not typed (falls back to record codegen).
	typedKind string
}

// arity is the number of arguments the flag takes on the command line.
func (d aggDef) arity() int {
	n := 1 // result name
	if d.hasField {
		n++
	}
	if d.extraArg != "" {
		n++
	}
	return n
}

func wireFixed(t string) func(string) string { return func(string) string { return t } }

// sqlCall renders FN("field") for the plain one-field aggregates.
func sqlCall(fn string) func(string, string) string {
	return func(qf, _ string) string { return fmt.Sprintf("%s(%s)", fn, qf) }
}

// sqlStringLiteral quotes a Go string as a SQL string literal.
func sqlStringLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Typed accumulator kinds (DFC129 §5).
const (
	typedKindCount      = "count"        // int64 counter
	typedKindSum        = "sum"          // running sum in the field's type
	typedKindAvg        = "avg"          // sum + count
	typedKindExtreme    = "extreme"      // best value + have flag, ordered compare
	typedKindPositional = "positional"   // first/last: value + have flag, shard-ordered merge
	typedKindSet        = "set"          // map[T]struct{} → count
	typedKindStringList = "string-list"  // []string → join
)

// wireOfField keeps the input field's type (min/max of a string is a
// string); "float" when the input schema does not know the field.
func wireOfField(fieldType string) string {
	if fieldType == "" {
		return "float"
	}
	return fieldType
}

// aggDefs lists the built-in aggregation flags in the order they are
// declared on the command and decoded from it.
var aggDefs = []aggDef{
	{flag: "-count", fn: "count", wireType: wireFixed("int"), typedKind: typedKindCount,
		sql:   func(string, string) string { return "COUNT(*)" },
		build: func(string, string) ssql.AggregateFunc { return ssql.Count() },
		code:  func(string, string) string { return "ssql.Count()" }},
	{flag: "-sum", fn: "sum", hasField: true, sql: sqlCall("SUM"), wireType: wireFixed("float"), typedKind: typedKindSum,
		build: func(f, _ string) ssql.AggregateFunc { return ssql.Sum(f) },
		code:  func(f, _ string) string { return fmt.Sprintf("ssql.Sum(%q)", f) }},
	{flag: "-avg", fn: "avg", hasField: true, sql: sqlCall("AVG"), wireType: wireFixed("float"), typedKind: typedKindAvg,
		build: func(f, _ string) ssql.AggregateFunc { return ssql.Avg(f) },
		code:  func(f, _ string) string { return fmt.Sprintf("ssql.Avg(%q)", f) }},
	// min/max keep the field's type: MinOf/MaxOf order numbers, strings
	// and times (Min[float64] reported 0 for every string group — DFC129 §2).
	{flag: "-min", fn: "min", hasField: true, sql: sqlCall("MIN"), wireType: wireOfField, typedKind: typedKindExtreme,
		build: func(f, _ string) ssql.AggregateFunc { return ssql.MinOf(f) },
		code:  func(f, _ string) string { return fmt.Sprintf("ssql.MinOf(%q)", f) }},
	{flag: "-max", fn: "max", hasField: true, sql: sqlCall("MAX"), wireType: wireOfField, typedKind: typedKindExtreme,
		build: func(f, _ string) ssql.AggregateFunc { return ssql.MaxOf(f) },
		code:  func(f, _ string) string { return fmt.Sprintf("ssql.MaxOf(%q)", f) }},
	{flag: "-collect", fn: "collect", hasField: true, sql: sqlCall("LIST"), wireType: wireFixed("json"),
		build: func(f, _ string) ssql.AggregateFunc { return ssql.Collect(f) },
		code:  func(f, _ string) string { return fmt.Sprintf("ssql.Collect(%q)", f) }},
	// DFC129 phase 1. first/last are arrival order — file order on a file
	// source in every lane (the typed parallel merge is in shard order);
	// -any is first with SQL's weaker any_value promise.
	{flag: "-first", fn: "first", hasField: true, sql: sqlCall("first"), wireType: wireOfField, typedKind: typedKindPositional,
		build: func(f, _ string) ssql.AggregateFunc { return ssql.FirstOf(f) },
		code:  func(f, _ string) string { return fmt.Sprintf("ssql.FirstOf(%q)", f) }},
	{flag: "-last", fn: "last", hasField: true, sql: sqlCall("last"), wireType: wireOfField, typedKind: typedKindPositional,
		build: func(f, _ string) ssql.AggregateFunc { return ssql.LastOf(f) },
		code:  func(f, _ string) string { return fmt.Sprintf("ssql.LastOf(%q)", f) }},
	{flag: "-any", fn: "any", hasField: true, sql: sqlCall("any_value"), wireType: wireOfField, typedKind: typedKindPositional,
		build: func(f, _ string) ssql.AggregateFunc { return ssql.FirstOf(f) },
		code:  func(f, _ string) string { return fmt.Sprintf("ssql.FirstOf(%q)", f) }},
	{flag: "-count-distinct", fn: "count-distinct", hasField: true, wireType: wireFixed("int"), typedKind: typedKindSet,
		sql:   func(qf, _ string) string { return fmt.Sprintf("COUNT(DISTINCT %s)", qf) },
		build: func(f, _ string) ssql.AggregateFunc { return ssql.CountDistinct(f) },
		code:  func(f, _ string) string { return fmt.Sprintf("ssql.CountDistinct(%q)", f) }},
	{flag: "-string-agg", fn: "string-agg", hasField: true, extraArg: "sep", wireType: wireFixed("string"), typedKind: typedKindStringList,
		sql:   func(qf, sep string) string { return fmt.Sprintf("string_agg(%s, %s)", qf, sqlStringLiteral(sep)) },
		build: func(f, sep string) ssql.AggregateFunc { return ssql.StringAgg(f, sep) },
		code:  func(f, sep string) string { return fmt.Sprintf("ssql.StringAgg(%q, %q)", f, sep) }},
}

// aggDefByFlag / aggDefByFn look an aggregate up by its flag ("-sum") or
// its function name ("sum").
func aggDefByFlag(flag string) (aggDef, bool) {
	for _, d := range aggDefs {
		if d.flag == flag {
			return d, true
		}
	}
	return aggDef{}, false
}

func aggDefByFn(fn string) (aggDef, bool) {
	for _, d := range aggDefs {
		if d.fn == fn {
			return d, true
		}
	}
	return aggDef{}, false
}

// aggFlagArity returns flag → argument count for every built-in
// aggregation flag (1 for -count, 2 for FIELD RESULT flags) — the table
// the optimiser and the schema walker use to step over a stage.
func aggFlagArity() map[string]int {
	m := make(map[string]int, len(aggDefs))
	for _, d := range aggDefs {
		m[d.flag] = d.arity()
	}
	return m
}

// aggWireType is the `_schema` type of an aggregation result, given the
// input schema (nil when the input had no header).
func aggWireType(spec aggSpec, in interface {
	HasField(string) bool
	TypeOf(string) string
}) string {
	d, ok := aggDefByFn(spec.function)
	if !ok {
		return "float"
	}
	ft := ""
	if d.hasField && in != nil && in.HasField(spec.field) {
		ft = in.TypeOf(spec.field)
	}
	return d.wireType(ft)
}

// streamExprSpec represents a streaming custom-aggregation
// specification (-stream-expr init every final result).
type streamExprSpec struct {
	initExpr  string
	everyExpr string
	finalExpr string
	result    string
}

// groupBySpecs holds everything decoded from group-by's aggregation
// and modifier flags. A single decode point shared by the exec
// handler, the codegen path (generateGroupByCode), and — for slice 5
// of the schema-aware-completion work — the schema rule. Keeping the
// autocli flag-shape quirks (a single-Arg flag arrives as a bare
// string; a multi-Arg flag as a map keyed by its Arg() names) in one
// place means the three consumers can never drift.
// See doc/research/schema-aware-completion.md §0.
type groupBySpecs struct {
	aggs        []aggSpec
	exprs       []exprSpec
	streamExprs []streamExprSpec
	rollup      bool
	cube        bool
	presorted   bool
}

// parseGroupBySpecs decodes all aggregation and modifier flags from a
// parsed group-by Context. It is total and side-effect-free.
func parseGroupBySpecs(ctx *cf.Context) groupBySpecs {
	return groupBySpecs{
		aggs:        parseAggSpecs(ctx),
		exprs:       parseExprSpecs(ctx),
		streamExprs: parseStreamExprSpecs(ctx),
		rollup:      groupByBoolFlag(ctx, "-rollup"),
		cube:        groupByBoolFlag(ctx, "-cube"),
		presorted:   groupByBoolFlag(ctx, "-presorted"),
	}
}

// parseAggSpecs decodes the built-in aggregation flags (aggDefs) into
// aggSpecs in registry order.
func parseAggSpecs(ctx *cf.Context) []aggSpec {
	var aggs []aggSpec
	for _, d := range aggDefs {
		if !d.hasField {
			// A single-Arg() flag (result name only) arrives from autocli
			// as a bare string rather than a map.
			if vals, ok := ctx.GlobalFlags[d.flag].([]any); ok {
				for _, v := range vals {
					if name, ok := v.(string); ok {
						aggs = append(aggs, aggSpec{function: d.fn, result: name})
					}
				}
			}
			continue
		}
		aggs = append(aggs, fieldResultAggSpecs(ctx, d)...)
	}
	return aggs
}

// fieldResultAggSpecs decodes a multi-argument aggregation flag whose
// args are ("field", [extra,] "result-name") into aggSpecs tagged with
// the def's function name.
func fieldResultAggSpecs(ctx *cf.Context, d aggDef) []aggSpec {
	vals, ok := ctx.GlobalFlags[d.flag].([]any)
	if !ok {
		return nil
	}
	var out []aggSpec
	for _, v := range vals {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		field, _ := m["field"].(string)
		result, _ := m["result-name"].(string)
		extra := ""
		if d.extraArg != "" {
			extra, _ = m[d.extraArg].(string)
		}
		if field != "" && result != "" {
			out = append(out, aggSpec{function: d.fn, field: field, result: result, extra: extra})
		}
	}
	return out
}

// parseExprSpecs decodes -expr (expression, result-name) flags.
func parseExprSpecs(ctx *cf.Context) []exprSpec {
	vals, ok := ctx.GlobalFlags["-expr"].([]any)
	if !ok {
		return nil
	}
	var out []exprSpec
	for _, v := range vals {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		expr, _ := m["expression"].(string)
		result, _ := m["result-name"].(string)
		if expr != "" && result != "" {
			out = append(out, exprSpec{expression: expr, result: result})
		}
	}
	return out
}

// parseStreamExprSpecs decodes -stream-expr (init, every, final,
// result-name) flags.
func parseStreamExprSpecs(ctx *cf.Context) []streamExprSpec {
	vals, ok := ctx.GlobalFlags["-stream-expr"].([]any)
	if !ok {
		return nil
	}
	var out []streamExprSpec
	for _, v := range vals {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		initExpr, _ := m["init"].(string)
		everyExpr, _ := m["every"].(string)
		finalExpr, _ := m["final"].(string)
		result, _ := m["result-name"].(string)
		if initExpr != "" && everyExpr != "" && finalExpr != "" && result != "" {
			out = append(out, streamExprSpec{
				initExpr:  initExpr,
				everyExpr: everyExpr,
				finalExpr: finalExpr,
				result:    result,
			})
		}
	}
	return out
}

// groupByBoolFlag reads a boolean global flag, defaulting to false when
// absent or of an unexpected type.
func groupByBoolFlag(ctx *cf.Context, name string) bool {
	v, _ := ctx.GlobalFlags[name].(bool)
	return v
}
