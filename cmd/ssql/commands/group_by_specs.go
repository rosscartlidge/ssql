package commands

import cf "github.com/rosscartlidge/autocli/v4"

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

// parseAggSpecs decodes the built-in aggregation flags (-count, -sum,
// -avg, -min, -max, -collect) into aggSpecs in flag order.
func parseAggSpecs(ctx *cf.Context) []aggSpec {
	var aggs []aggSpec
	// -count has a single Arg() (result name), so autocli delivers a
	// bare string rather than a map.
	if vals, ok := ctx.GlobalFlags["-count"].([]any); ok {
		for _, v := range vals {
			if name, ok := v.(string); ok {
				aggs = append(aggs, aggSpec{function: "count", result: name})
			}
		}
	}
	// The field+result aggregations all share the same 2-Arg shape.
	for _, f := range []struct{ flag, fn string }{
		{"-sum", "sum"}, {"-avg", "avg"}, {"-min", "min"},
		{"-max", "max"}, {"-collect", "collect"},
	} {
		aggs = append(aggs, fieldResultAggSpecs(ctx, f.flag, f.fn)...)
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
