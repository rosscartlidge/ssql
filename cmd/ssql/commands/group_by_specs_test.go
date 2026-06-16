package commands

import (
	"reflect"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

// TestParseGroupBySpecs feeds parseGroupBySpecs the exact flag shapes
// autocli produces (single-Arg flags as a bare string; multi-Arg flags
// as a map keyed by Arg() names, wrapped in []any by Accumulate()) and
// asserts the decode. This guards the single decode point shared by the
// exec handler, the codegen path, and (slice 5) the schema rule.
func TestParseGroupBySpecs(t *testing.T) {
	ctx := &cf.Context{
		GlobalFlags: map[string]any{
			// -count: single Arg() -> bare string, accumulated.
			"-count": []any{"n"},
			// 2-Arg field+result flags -> map, accumulated.
			"-sum":     []any{map[string]any{"field": "salary", "result-name": "total"}},
			"-avg":     []any{map[string]any{"field": "hours", "result-name": "avg_hrs"}},
			"-min":     []any{map[string]any{"field": "salary", "result-name": "lo"}},
			"-max":     []any{map[string]any{"field": "salary", "result-name": "hi"}},
			"-collect": []any{map[string]any{"field": "name", "result-name": "names"}},
			// -expr: (expression, result-name).
			"-expr": []any{map[string]any{"expression": "sum(salary * bonus)", "result-name": "comp"}},
			// -stream-expr: (init, every, final, result-name).
			"-stream-expr": []any{map[string]any{
				"init": "{s:0}", "every": "{s:s+salary}", "final": "s", "result-name": "sx",
			}},
			"-rollup":    true,
			"-presorted": true,
		},
	}

	got := parseGroupBySpecs(ctx)

	wantAggs := []aggSpec{
		{function: "count", result: "n"},
		{function: "sum", field: "salary", result: "total"},
		{function: "avg", field: "hours", result: "avg_hrs"},
		{function: "min", field: "salary", result: "lo"},
		{function: "max", field: "salary", result: "hi"},
		{function: "collect", field: "name", result: "names"},
	}
	if !reflect.DeepEqual(got.aggs, wantAggs) {
		t.Errorf("aggs:\n got %+v\nwant %+v", got.aggs, wantAggs)
	}

	wantExprs := []exprSpec{{expression: "sum(salary * bonus)", result: "comp"}}
	if !reflect.DeepEqual(got.exprs, wantExprs) {
		t.Errorf("exprs:\n got %+v\nwant %+v", got.exprs, wantExprs)
	}

	wantStream := []streamExprSpec{{initExpr: "{s:0}", everyExpr: "{s:s+salary}", finalExpr: "s", result: "sx"}}
	if !reflect.DeepEqual(got.streamExprs, wantStream) {
		t.Errorf("streamExprs:\n got %+v\nwant %+v", got.streamExprs, wantStream)
	}

	if !got.rollup || got.cube || !got.presorted {
		t.Errorf("modifiers: got rollup=%v cube=%v presorted=%v, want true/false/true",
			got.rollup, got.cube, got.presorted)
	}
}

// TestParseGroupBySpecs_Empty confirms an empty Context decodes to all
// zero values (no panics, no spurious specs).
func TestParseGroupBySpecs_Empty(t *testing.T) {
	got := parseGroupBySpecs(&cf.Context{GlobalFlags: map[string]any{}})
	if len(got.aggs) != 0 || len(got.exprs) != 0 || len(got.streamExprs) != 0 {
		t.Errorf("expected no specs, got %+v", got)
	}
	if got.rollup || got.cube || got.presorted {
		t.Errorf("expected no modifiers, got %+v", got)
	}
}

// TestParseGroupBySpecs_IncompleteDropped confirms malformed flag
// entries (missing field or result) are dropped, not panicked on.
func TestParseGroupBySpecs_IncompleteDropped(t *testing.T) {
	ctx := &cf.Context{
		GlobalFlags: map[string]any{
			"-sum": []any{
				map[string]any{"field": "salary"},                // missing result-name -> dropped
				map[string]any{"result-name": "total"},           // missing field -> dropped
				map[string]any{"field": "x", "result-name": "y"}, // kept
			},
		},
	}
	got := parseGroupBySpecs(ctx)
	want := []aggSpec{{function: "sum", field: "x", result: "y"}}
	if !reflect.DeepEqual(got.aggs, want) {
		t.Errorf("aggs:\n got %+v\nwant %+v", got.aggs, want)
	}
}
