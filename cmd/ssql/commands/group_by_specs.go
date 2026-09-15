package commands

import (
	"fmt"

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
	sqlFn    string // "SUM" → SUM("field"); -count carries the whole expression "COUNT(*)"
	// wireType is the `_schema` type of the result given the input
	// field's wire type ("" when unknown or fieldless).
	wireType func(fieldType string) string
	build    func(field string) ssql.AggregateFunc // exec
	code     func(field string) string            // record codegen expression
}

func wireFixed(t string) func(string) string { return func(string) string { return t } }

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
	{flag: "-count", fn: "count", sqlFn: "COUNT(*)", wireType: wireFixed("int"),
		build: func(string) ssql.AggregateFunc { return ssql.Count() },
		code:  func(string) string { return "ssql.Count()" }},
	{flag: "-sum", fn: "sum", hasField: true, sqlFn: "SUM", wireType: wireFixed("float"),
		build: ssql.Sum,
		code:  func(f string) string { return fmt.Sprintf("ssql.Sum(%q)", f) }},
	{flag: "-avg", fn: "avg", hasField: true, sqlFn: "AVG", wireType: wireFixed("float"),
		build: ssql.Avg,
		code:  func(f string) string { return fmt.Sprintf("ssql.Avg(%q)", f) }},
	// min/max keep the field's type: MinOf/MaxOf order numbers, strings
	// and times (Min[float64] reported 0 for every string group — DFC129 §2).
	{flag: "-min", fn: "min", hasField: true, sqlFn: "MIN", wireType: wireOfField,
		build: ssql.MinOf,
		code:  func(f string) string { return fmt.Sprintf("ssql.MinOf(%q)", f) }},
	{flag: "-max", fn: "max", hasField: true, sqlFn: "MAX", wireType: wireOfField,
		build: ssql.MaxOf,
		code:  func(f string) string { return fmt.Sprintf("ssql.MaxOf(%q)", f) }},
	{flag: "-collect", fn: "collect", hasField: true, sqlFn: "LIST", wireType: wireFixed("json"),
		build: ssql.Collect,
		code:  func(f string) string { return fmt.Sprintf("ssql.Collect(%q)", f) }},
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
		if d.hasField {
			m[d.flag] = 2
		} else {
			m[d.flag] = 1
		}
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
		aggs = append(aggs, fieldResultAggSpecs(ctx, d.flag, d.fn)...)
	}
	return aggs
}

// fieldResultAggSpecs decodes a two-argument aggregation flag whose
// args are ("field", "result-name") into aggSpecs tagged with fn.
func fieldResultAggSpecs(ctx *cf.Context, flag, fn string) []aggSpec {
	vals, ok := ctx.GlobalFlags[flag].([]any)
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
		if field != "" && result != "" {
			out = append(out, aggSpec{function: fn, field: field, result: result})
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
