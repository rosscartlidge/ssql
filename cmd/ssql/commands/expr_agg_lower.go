package commands

import (
	"errors"
	"fmt"

	"github.com/expr-lang/expr/ast"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// Typed lowering of `group-by -expr EXPRESSION RESULT` (expr-transpiler
// Phase 3). The expression is first rewritten to the SAME normal form the
// VM evaluates (ssql.PatchAggExpr): sum(salary*bonus) →
// sum(_records, #.salary*#.bonus), count() → len(_records), avg(e) →
// sum/len. The patched tree then contains exactly two aggregation shapes —
// sum-over-records and len-of-records — connected by ordinary arithmetic.
//
// Each DISTINCT sum term becomes an accumulator field with a per-row
// `+= <elem>`; len(_records) becomes a shared counter; the outer expression
// becomes Result(), computed over the accumulated values. Sums and counts
// add across shards, so — unlike -stream-expr folds — these accumulators
// are MERGEABLE and the group-by keeps its parallel form.
//
// Accumulators keep the element expression's own type (int64 sums stay
// int64 — expr-lang's sum of ints is an int, and int wrap/`%` semantics
// must survive into the outer arithmetic); the FINAL result is coerced to
// float64 like the VM's mustAggFloat64. A non-numeric outer expression is
// a refusal: record fallback reproduces the VM's loud runtime panic.

// exprAggTerm is one distinct sum(_records, elem) accumulator.
type exprAggTerm struct {
	GoName  string     // accumulator struct field, e.g. "ea0_t0"
	Type    exprGoType // the element expression's type (int64 or float64)
	ElemSrc string     // per-row Go expression added into the accumulator
}

// exprAggPlan is the complete typed lowering of one -expr aggregation.
type exprAggPlan struct {
	Spec      exprSpec
	Terms     []exprAggTerm
	UsesCount bool
	CountName string // shared counter field, e.g. "ea0_cnt"
	Result    string // outer Go expression over accumulators, float64-coerced
	Imports   []string
	Hoisted   []string
}

// lowerExprAgg lowers spec against the input schema. idx namespaces the
// accumulator field names when several -expr specs share the struct.
func lowerExprAgg(spec exprSpec, schema *lib.TypedSchema, idx int) (exprAggPlan, error) {
	plan := exprAggPlan{Spec: spec, CountName: fmt.Sprintf("ea%d_cnt", idx)}

	fieldNames := make([]string, len(schema.Fields))
	for i, f := range schema.Fields {
		fieldNames[i] = f.Name
	}
	root, err := ssql.CompileAggExprPatched(spec.expression, fieldNames)
	if err != nil {
		// This is exec's OWN compile (same env dummies, same patcher) — a
		// failure here fails in every mode. Loud at codegen.
		return plan, &exprLoudError{err: fmt.Errorf("expression %q: %w", spec.expression, err)}
	}

	// Replace every aggregation-shaped subtree with a placeholder
	// identifier bound to its accumulator, collecting terms as we go.
	// Identical element sources share one accumulator.
	bySrc := make(map[string]string) // elem Go source → placeholder name
	vars := make(map[string]exprGo)
	var walkErr error
	sub := &aggSubtreeReplacer{
		replace: func(node *ast.Node) {
			if walkErr != nil {
				return
			}
			switch shape := aggShape(*node); shape {
			case aggShapeSum:
				elem := (*node).(*ast.BuiltinNode).Arguments[1].(*ast.PredicateNode).Node
				res, err := exprNodeToGoWith(elem, schema, "r", nil)
				if err != nil {
					walkErr = fmt.Errorf("sum() element: %w", err)
					return
				}
				if !res.Type.numeric() {
					walkErr = fmt.Errorf("sum() element is %s — sums need numeric elements", res.Type)
					return
				}
				name, ok := bySrc[res.Src]
				if !ok {
					name = fmt.Sprintf("__aggterm%d", len(plan.Terms))
					goName := fmt.Sprintf("ea%d_t%d", idx, len(plan.Terms))
					plan.Terms = append(plan.Terms, exprAggTerm{GoName: goName, Type: res.Type, ElemSrc: res.Src})
					vars[name] = exprGo{Src: "a." + goName, Type: res.Type}
					bySrc[res.Src] = name
					plan.Imports = append(plan.Imports, res.Imports...)
					plan.Hoisted = append(plan.Hoisted, res.Hoisted...)
				}
				ast.Patch(node, &ast.IdentifierNode{Value: name})
			case aggShapeCount:
				plan.UsesCount = true
				vars["__aggcount"] = exprGo{Src: "a." + plan.CountName, Type: exprGoInt}
				ast.Patch(node, &ast.IdentifierNode{Value: "__aggcount"})
			}
		},
	}
	ast.Walk(&root, sub)
	if walkErr != nil {
		return plan, walkErr
	}
	if len(plan.Terms) == 0 && !plan.UsesCount {
		return plan, fmt.Errorf("expression %q contains no aggregation (sum/count/avg) — a per-group constant is better written as update", spec.expression)
	}

	// The outer expression: ordinary arithmetic over the placeholders.
	// NB field references OUTSIDE an aggregation are legal in the VM — the
	// batch env binds each field to the ARRAY of its group values (e.g.
	// `sum(salary)/len(salary)` works, len of the array = group size).
	// Arrays have no typed lowering, so an unknown identifier here is a
	// REFUSAL (record fallback preserves the VM behaviour), deliberately
	// NOT the loud exprUnknownFieldError path — unlike inside sum()
	// elements, where scope is per-record and an unknown name is a typo.
	outer, err := exprNodeToGoWith(root, nil, "", vars)
	if err != nil {
		var unknownField *exprUnknownFieldError
		if errors.As(err, &unknownField) {
			return plan, fmt.Errorf("aggregation expression references a field outside sum()/avg() — the VM binds it to the group's value ARRAY, which has no typed lowering: %v", err)
		}
		return plan, fmt.Errorf("aggregation expression: %w", err)
	}
	if !outer.Type.numeric() {
		return plan, fmt.Errorf("aggregation result is %s — the VM coerces via mustAggFloat64, which panics on non-numerics", outer.Type)
	}
	plan.Result = asFloat(outer)
	plan.Imports = append(plan.Imports, outer.Imports...)
	plan.Hoisted = append(plan.Hoisted, outer.Hoisted...)
	return plan, nil
}

type aggShapeKind int

const (
	aggShapeNone aggShapeKind = iota
	aggShapeSum
	aggShapeCount
)

// aggShape recognises the two normal-form aggregation shapes PatchAggExpr
// produces: sum(_records, <predicate>) and len(_records).
func aggShape(n ast.Node) aggShapeKind {
	b, ok := n.(*ast.BuiltinNode)
	if !ok {
		return aggShapeNone
	}
	switch b.Name {
	case "sum":
		if len(b.Arguments) == 2 {
			if id, ok := b.Arguments[0].(*ast.IdentifierNode); ok && id.Value == "_records" {
				if _, ok := b.Arguments[1].(*ast.PredicateNode); ok {
					return aggShapeSum
				}
			}
		}
	case "len":
		if len(b.Arguments) == 1 {
			if id, ok := b.Arguments[0].(*ast.IdentifierNode); ok && id.Value == "_records" {
				return aggShapeCount
			}
		}
	}
	return aggShapeNone
}

// aggSubtreeReplacer applies replace to every node (ast.Walk visitor).
type aggSubtreeReplacer struct {
	replace func(node *ast.Node)
}

func (r *aggSubtreeReplacer) Visit(node *ast.Node) {
	r.replace(node)
}
