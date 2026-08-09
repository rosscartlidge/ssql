package commands

import (
	"fmt"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// Typed lowering of `group-by -stream-expr INIT EVERY FINAL RESULT`
// (expr-transpiler Phase 2). The exec semantics (evalStreamAggExpr,
// expr_agg.go) are a map-state fold:
//
//	state = eval(INIT)                       // must be an object
//	for each record: state = eval(EVERY, state ∪ record)  // record SHADOWS state
//	result = mustAggFloat64(eval(FINAL, state))            // state fields only
//
// The lowering turns the state map into typed accumulator struct fields, the
// EVERY object into one simultaneous multi-assignment inside Add() (EVERY's
// values all read the OLD state — {s: n, n: s} must swap), and FINAL into
// the Result() expression, coerced to float64 for VM parity.
//
// A refusal (non-nil error, not exprUnknownFieldError) means "this spec
// can't be held in a typed struct" — the caller falls back to record
// codegen. Unknown record fields inside EVERY are loud (they'd fail in
// every mode); FINAL resolves against state fields ONLY, exactly like the
// VM (a record field in FINAL fails in every mode → loud).

// streamAggState is one state field of the lowered fold.
type streamAggState struct {
	Key    string     // state key as written in INIT (e.g. "s")
	GoName string     // accumulator struct field (e.g. "se0_s")
	Type   exprGoType // unified over INIT and EVERY (int64 may widen to float64)
	Init   string     // Go initialisation expression (literal / literal arithmetic)
}

// streamAggPlan is the complete typed lowering of one -stream-expr spec.
type streamAggPlan struct {
	Spec    streamExprSpec
	States  []streamAggState // INIT declaration order
	Every   []string         // new-state Go exprs, parallel to States (all read OLD state)
	Final   string           // Go expr over state fields, already float64
	Imports []string
	Hoisted []string
}

// lowerStreamAgg lowers spec against the input schema. idx namespaces the
// accumulator field names when several -stream-expr specs share the struct.
func lowerStreamAgg(spec streamExprSpec, schema *lib.TypedSchema, idx int) (streamAggPlan, error) {
	plan := streamAggPlan{Spec: spec}

	// INIT: a map literal of literal (or literal-arithmetic) values. Any
	// identifier inside INIT is a refusal — the VM evaluates INIT with an
	// empty env too, but a non-literal shape is exotic enough for record
	// mode.
	initPairs, err := exprMapPairs(spec.initExpr)
	if err != nil {
		return plan, fmt.Errorf("init %w", err)
	}
	for _, p := range initPairs {
		res, err := exprNodeToGoWith(p.value, nil, "", nil)
		if err != nil {
			return plan, fmt.Errorf("init %q value: %w", p.key, err)
		}
		plan.States = append(plan.States, streamAggState{
			Key:    p.key,
			GoName: fmt.Sprintf("se%d_%s", idx, p.key),
			Type:   res.Type,
			Init:   res.Src,
		})
		plan.Imports = append(plan.Imports, res.Imports...)
		plan.Hoisted = append(plan.Hoisted, res.Hoisted...)
	}

	// EVERY: a map literal whose key set must equal INIT's (the VM replaces
	// the WHOLE state with EVERY's result — dropped or added keys change
	// the state shape, which a struct can't do).
	everyPairs, err := exprMapPairs(spec.everyExpr)
	if err != nil {
		return plan, fmt.Errorf("every %w", err)
	}
	everyByKey := make(map[string]ast.Node, len(everyPairs))
	for _, p := range everyPairs {
		everyByKey[p.key] = p.value
	}
	if len(everyPairs) != len(plan.States) {
		return plan, fmt.Errorf("every expression keys %s do not match init keys (the VM replaces the whole state object)", exprPairKeys(everyPairs))
	}
	for _, st := range plan.States {
		if _, ok := everyByKey[st.Key]; !ok {
			return plan, fmt.Errorf("every expression is missing state key %q (the VM replaces the whole state object)", st.Key)
		}
	}

	// Transpile EVERY values with env = schema ∪ state (record shadows
	// state), iterating to a widening fixpoint: `{s: s + salary}` with
	// int init and float salary widens s to float64, which can in turn
	// widen other states. Only int64→float64 widening exists, so this
	// terminates in ≤ len(states) rounds.
	for round := 0; ; round++ {
		if round > len(plan.States)+1 {
			return plan, fmt.Errorf("state type inference did not converge")
		}
		vars := make(map[string]exprGo, len(plan.States))
		for _, st := range plan.States {
			vars[st.Key] = exprGo{Src: "a." + st.GoName, Type: st.Type}
		}
		widened := false
		plan.Every = plan.Every[:0]
		for i, st := range plan.States {
			res, err := exprNodeToGoWith(everyByKey[st.Key], schema, "r", vars)
			if err != nil {
				return plan, fmt.Errorf("every %q value: %w", st.Key, err)
			}
			switch {
			case res.Type == st.Type:
				plan.Every = append(plan.Every, res.Src)
			case st.Type == exprGoInt && res.Type == exprGoFloat:
				plan.States[i].Type = exprGoFloat
				widened = true
			default:
				return plan, fmt.Errorf("every %q value is %s but the state field is %s", st.Key, res.Type, st.Type)
			}
			plan.Imports = append(plan.Imports, res.Imports...)
			plan.Hoisted = append(plan.Hoisted, res.Hoisted...)
		}
		if !widened {
			break
		}
	}

	// Widening may leave an int INIT literal initialising a float64 state
	// field — fine in Go (untyped constants adapt in the struct literal /
	// zero value); nothing to fix up.

	// FINAL: state fields only (matching the VM: env = stateMap). Result is
	// coerced to float64 — mustAggFloat64 parity; non-numeric results panic
	// in exec, so here they are loud at codegen (§6 strictening).
	finalVars := make(map[string]exprGo, len(plan.States))
	for _, st := range plan.States {
		finalVars[st.Key] = exprGo{Src: "a." + st.GoName, Type: st.Type}
	}
	finalRes, err := exprToGoWith(spec.finalExpr, nil, "", finalVars)
	if err != nil {
		return plan, fmt.Errorf("final: %w", err)
	}
	if !finalRes.Type.numeric() {
		return plan, fmt.Errorf("final expression is %s — stream aggregation results must be numeric (the VM coerces via mustAggFloat64, which panics on non-numerics)", finalRes.Type)
	}
	plan.Final = asFloat(finalRes)
	plan.Imports = append(plan.Imports, finalRes.Imports...)
	plan.Hoisted = append(plan.Hoisted, finalRes.Hoisted...)

	return plan, nil
}

type exprMapPair struct {
	key   string
	value ast.Node
}

// exprMapPairs parses an expression that must be a map literal and returns
// its pairs in declaration order.
func exprMapPairs(expression string) ([]exprMapPair, error) {
	tree, err := parser.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("expression %q: %w", expression, err)
	}
	m, ok := tree.Node.(*ast.MapNode)
	if !ok {
		return nil, fmt.Errorf("expression %q is not an object literal", expression)
	}
	var pairs []exprMapPair
	for _, p := range m.Pairs {
		pair, ok := p.(*ast.PairNode)
		if !ok {
			return nil, fmt.Errorf("expression %q: unsupported pair form", expression)
		}
		key, ok := pair.Key.(*ast.StringNode)
		if !ok {
			return nil, fmt.Errorf("expression %q: computed keys are not supported", expression)
		}
		pairs = append(pairs, exprMapPair{key: key.Value, value: pair.Value})
	}
	return pairs, nil
}

func exprPairKeys(pairs []exprMapPair) string {
	keys := make([]string, len(pairs))
	for i, p := range pairs {
		keys[i] = p.key
	}
	return strings.Join(keys, ", ")
}
