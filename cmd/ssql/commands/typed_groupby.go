package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// emitTypedGroupBy produces three pieces of code for a typed group-by:
//
//  1. A key type (single field's Go type, or a struct of multiple
//     fields if grouping by more than one column)
//  2. An accumulator type (one field per aggregation, plus running
//     state) with Add() and Result() methods that satisfy the
//     typed.Aggregator[T, R] interface
//  3. A result struct (group key fields + aggregation result fields)
//
// Plus the typed.GroupBy call wiring them together.
//
// Single-field group key: the key type is the field's Go type
// directly. Multi-field: a tuple struct named <Input>GroupKey is
// emitted alongside.
//
// Aggregations: count/sum/avg/min/max, plus -stream-expr folds lowered
// to accumulator fields by lowerStreamAgg (expr-transpiler Phase 2). No
// collect, no -expr (Phase 3). The caller has validated this before
// invoking us.
//
// Returns handled=false with a reason — WITHOUT writing a fragment — when a
// -stream-expr spec can't be held in a typed struct (non-literal init,
// every keys ≠ init keys, non-numeric state/final, untranspilable
// sub-expression); the caller falls back to record codegen. Unknown fields
// stay loud.
//
// Stream-expr state is NOT generally mergeable (the fold isn't
// necessarily associative), so any -stream-expr forces the serial
// single-template emission (SerialOnly — the planner inserts the
// Stream.Serial() boundary upstream); otherwise dual templates are
// emitted and the planner picks.
func emitTypedGroupBy(inputVar string, in *lib.TypedSchema, groupFields []string, specs []aggSpec, exprSpecs []exprSpec, streamSpecs []streamExprSpec, presorted, parallel bool) (bool, string, error) {
	// Validate group fields exist in the input schema.
	groupSchemaFields := make([]lib.TypedSchemaField, 0, len(groupFields))
	for _, name := range groupFields {
		f, ok := lookupSchemaField(in, name)
		if !ok {
			return true, "", lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'group-by': unknown field %q", name))
		}
		groupSchemaFields = append(groupSchemaFields, f)
	}

	// Validate each aggregation's source field exists and is numeric
	// where required.
	for _, s := range specs {
		if s.function == "count" {
			continue // takes no field
		}
		f, ok := lookupSchemaField(in, s.field)
		if !ok {
			return true, "", lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: aggregation %q references unknown field %q", s.function, s.field))
		}
		if needsNumeric(s.function) && !isNumericGoType(f.GoType) {
			return true, "", lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: aggregation %q on field %q requires a numeric type, got %s", s.function, s.field, f.GoType))
		}
	}

	// Lower each -expr aggregation via the patcher normal form. Unlike
	// -stream-expr folds, these accumulators are MERGEABLE (sums and
	// counts add across shards), so they keep the parallel group-by.
	var exprPlans []exprAggPlan
	var planNotes []string
	for i, es := range exprSpecs {
		plan, err := lowerExprAgg(es, in, i)
		if err != nil {
			if exprIsLoud(err) {
				return true, "", lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: 'group-by -expr': %w", err))
			}
			return false, fmt.Sprintf("-expr %s: %v", es.result, err), nil
		}
		exprPlans = append(exprPlans, plan)
		planNotes = append(planNotes, fmt.Sprintf("expr %q: native mergeable accumulator (%d term(s))", es.expression, len(plan.Terms)))
	}

	// Lower each -stream-expr fold. Unknown record fields inside are loud;
	// any other refusal falls back to record codegen with the reason.
	var streamPlans []streamAggPlan
	for i, ss := range streamSpecs {
		plan, err := lowerStreamAgg(ss, in, i)
		if err != nil {
			var unknownField *exprUnknownFieldError
			if errors.As(err, &unknownField) {
				return true, "", lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: 'group-by -stream-expr': %w", err))
			}
			return false, fmt.Sprintf("-stream-expr %s: %v", ss.result, err), nil
		}
		streamPlans = append(streamPlans, plan)
		planNotes = append(planNotes, fmt.Sprintf("stream-expr %q: native accumulator (serial group-by — fold state is not mergeable)", ss.result))
	}

	// ---- key type ----
	keyType, keyExpr, keyStructDef := buildGroupKeyType(in, groupSchemaFields)

	// ---- result struct ----
	resultName := in.TypeName + "Group"
	var resultFields []lib.TypedSchemaField
	for _, f := range groupSchemaFields {
		resultFields = append(resultFields, f)
	}
	for _, s := range specs {
		resultGoName := lib.GoNameFromColumn(s.result)
		resultFields = append(resultFields, lib.TypedSchemaField{
			Name:   s.result,
			GoName: resultGoName,
			GoType: aggResultGoType(s, in),
		})
	}
	for _, p := range exprPlans {
		// Expression-aggregation results are always float64 (mustAggFloat64
		// parity), like stream folds below.
		resultFields = append(resultFields, lib.TypedSchemaField{
			Name:   p.Spec.result,
			GoName: lib.GoNameFromColumn(p.Spec.result),
			GoType: "float64",
		})
	}
	for _, p := range streamPlans {
		// Stream-expr results are always float64 (mustAggFloat64 parity).
		resultFields = append(resultFields, lib.TypedSchemaField{
			Name:   p.Spec.result,
			GoName: lib.GoNameFromColumn(p.Spec.result),
			GoType: "float64",
		})
	}
	resultSchema := &lib.TypedSchema{TypeName: resultName, Fields: resultFields}
	resultDef := renderResultStructDef(resultSchema)

	// ---- aggregator type ----
	aggTypeName := in.TypeName + "Aggregator"
	aggResultName := aggTypeName + "Result"
	// Without stream folds: emit the parallel-flavoured aggregator (Add +
	// Result + Merge). ParallelAggregator extends Aggregator, so the same
	// struct satisfies both interfaces — ONE definition serves both the
	// parallel call site and the serial alternative. With stream folds:
	// no Merge (not mergeable) — the emission below is SerialOnly anyway.
	aggDef, _, _ := buildTypedAggregator(aggTypeName, in, specs, exprPlans, streamPlans, groupSchemaFields, resultName, len(streamPlans) == 0)

	// The aggregator constructor: stream-expr states carry their INIT
	// values (the plain &T{} zero value is wrong for `{s: 1}`).
	var ctorInits []string
	for _, p := range streamPlans {
		for _, st := range p.States {
			ctorInits = append(ctorInits, fmt.Sprintf("%s: %s", st.GoName, st.Init))
		}
	}
	aggCtor := fmt.Sprintf("&%s{%s}", aggTypeName, strings.Join(ctorInits, ", "))

	// Assemble all three struct/type definitions.
	defs := []string{aggDef}
	if keyStructDef != "" {
		defs = append(defs, keyStructDef)
	}
	defs = append(defs, resultDef)

	// ---- the typed.GroupBy / typed.GroupByParallel call itself ----
	// Build both forms so the planner can swap to the serial
	// alternative if no downstream consumes Stream input.
	makeGroupByCode := func(groupFn, aggIface string) string {
		return fmt.Sprintf(`grouped := %s(%s,
		func(r %s) %s { return %s },
		func() %s[%s, %s] { return %s },
		func(k %s, agg %s) %s {
			return %s{
				%s,
			}
		})`,
			groupFn, inputVar,
			in.TypeName, keyType, keyExpr,
			aggIface, in.TypeName, aggResultName, aggCtor,
			keyType, aggResultName, resultName,
			resultName,
			buildResultCtor(groupSchemaFields, specs, exprPlans, streamPlans, aggResultName),
		)
	}

	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	for _, p := range exprPlans {
		imports = append(imports, p.Imports...)
	}
	for _, p := range streamPlans {
		imports = append(imports, p.Imports...)
	}
	if schemaUsesTime(resultSchema) || schemaUsesTime(in) {
		imports = append(imports, "time")
	}
	imports = dedupeImports(imports)
	var hoisted []string
	for _, p := range exprPlans {
		hoisted = append(hoisted, p.Hoisted...)
	}
	for _, p := range streamPlans {
		hoisted = append(hoisted, p.Hoisted...)
	}

	// -presorted: GroupByOrdered requires contiguous keys → SerialOnly.
	// Any -stream-expr also forces SerialOnly: the fold state is not
	// mergeable across shards. Single-template emission; the planner
	// inserts the Stream.Serial() boundary upstream when needed.
	if presorted || len(streamPlans) > 0 {
		groupFn := "typed.GroupBy"
		if presorted {
			groupFn = "typed.GroupByOrdered"
		}
		code := makeGroupByCode(groupFn, "typed.Aggregator")
		frag := lib.NewStmtFragment("grouped", inputVar, code, imports, getCommandString())
		frag.InputTypedSchema = in
		frag.OutputTypedSchema = resultSchema
		frag.StructDefs = append(defs, hoisted...)
		frag.PlanNotes = planNotes
		frag.Capabilities = &lib.Capabilities{
			Accepts:    lib.ShapeSeqTyped,
			Produces:   lib.ShapeSeqTyped,
			SerialOnly: true,
		}
		return true, "", lib.WriteCodeFragment(frag)
	}

	// Non-presorted: emit both templates. Default is
	// typed.GroupByParallel (Stream[T] → iter.Seq[O], fans in after
	// Combine); planner swaps to typed.GroupBy when source is
	// downgraded.
	parallelCode := makeGroupByCode("typed.GroupByParallel", "typed.ParallelAggregator")
	serialCode := makeGroupByCode("typed.GroupBy", "typed.Aggregator")

	frag := lib.NewStmtFragment("grouped", inputVar, parallelCode, imports, getCommandString())
	frag.InputTypedSchema = in
	frag.OutputTypedSchema = resultSchema
	frag.StructDefs = append(defs, hoisted...)
	frag.PlanNotes = planNotes
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeSeqTyped}
	frag.AltCodeIfSeq = serialCode
	frag.AltImportsIfSeq = imports
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	_ = parallel // intentionally unused — the planner picks now
	return true, "", lib.WriteCodeFragment(frag)
}

// buildGroupKeyType decides on the key type for the group-by.
// Empty fields → key is `struct{}{}` (single group, all rows
// collapse to one). Single field → the field's Go type directly.
// Multiple fields → an emitted anonymous struct.
//
// Returns: (Go type expression, key extraction expression from "r",
// optional struct definition string).
func buildGroupKeyType(in *lib.TypedSchema, fields []lib.TypedSchemaField) (string, string, string) {
	if len(fields) == 0 {
		// SQL `GROUP BY ()` semantics: one group, no key fields.
		// struct{} is comparable, can be used as map key.
		return "struct{}", "struct{}{}", ""
	}
	if len(fields) == 1 {
		return fields[0].GoType, "r." + fields[0].GoName, ""
	}
	// Multi-field: emit a key struct.
	keyName := in.TypeName + "GroupKey"
	var b strings.Builder
	fmt.Fprintf(&b, "// %s is the composite group key for the typed group-by.\n", keyName)
	fmt.Fprintf(&b, "type %s struct {\n", keyName)
	maxName := 0
	maxType := 0
	for _, f := range fields {
		if len(f.GoName) > maxName {
			maxName = len(f.GoName)
		}
		if len(f.GoType) > maxType {
			maxType = len(f.GoType)
		}
	}
	for _, f := range fields {
		fmt.Fprintf(&b, "\t%-*s %s\n", maxName, f.GoName, f.GoType)
	}
	b.WriteString("}\n")

	var ctorParts []string
	for _, f := range fields {
		ctorParts = append(ctorParts, fmt.Sprintf("%s: r.%s", f.GoName, f.GoName))
	}
	keyExpr := fmt.Sprintf("%s{%s}", keyName, strings.Join(ctorParts, ", "))
	return keyName, keyExpr, b.String()
}

// renderResultStructDef builds the Go source for the result struct.
// Group-key fields keep their original ssql tags (so a downstream
// `to csv` writes them under the right column names); aggregation
// result fields use the user-supplied result name as the tag.
func renderResultStructDef(s *lib.TypedSchema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s is the per-group result type produced by typed.GroupBy.\n", s.TypeName)
	fmt.Fprintf(&b, "type %s struct {\n", s.TypeName)
	maxName := 0
	maxType := 0
	for _, f := range s.Fields {
		if len(f.GoName) > maxName {
			maxName = len(f.GoName)
		}
		if len(f.GoType) > maxType {
			maxType = len(f.GoType)
		}
	}
	for _, f := range s.Fields {
		fmt.Fprintf(&b, "\t%-*s %-*s `ssql:%q`\n", maxName, f.GoName, maxType, f.GoType, f.Name)
	}
	b.WriteString("}\n")
	return b.String()
}

// buildTypedAggregator emits a struct that implements typed.Aggregator
// for the given aggregation specs. Returns the struct definition,
// plus separate Add and Result body strings (the Add body is already
// embedded into the returned definition).
//
// When parallel is true, also emits a Merge method so the struct
// satisfies typed.ParallelAggregator and can be used with
// typed.GroupByParallel. Merge rules per aggregation function are
// the trivially associative ones (count: N1+N2; sum: s1+s2; avg:
// sum1+sum2 + n1+n2; min/max: pick the better while honouring the
// "have value" flag).
func buildTypedAggregator(aggTypeName string, in *lib.TypedSchema, specs []aggSpec, exprPlans []exprAggPlan, streamPlans []streamAggPlan, groupFields []lib.TypedSchemaField, resultName string, parallel bool) (def, addBody, resultBody string) {
	// Decide what state to hold per spec.
	type aggState struct {
		spec      aggSpec
		stateName string
		fieldGo   string // input field's GoName (empty for count)
		fieldGoT  string // input field's GoType (empty for count)
		stateType string // "int64", "float64", "MinMaxState[int64]", etc.
		needsN    bool   // avg also tracks count
	}

	// Look up source fields once.
	byCSV := make(map[string]lib.TypedSchemaField, len(in.Fields))
	for _, f := range in.Fields {
		byCSV[strings.ToLower(f.Name)] = f
	}

	var states []aggState
	for i, s := range specs {
		st := aggState{spec: s, stateName: fmt.Sprintf("agg%d", i)}
		if s.function == "count" {
			st.stateType = "int64"
			states = append(states, st)
			continue
		}
		f := byCSV[strings.ToLower(s.field)]
		st.fieldGo = f.GoName
		st.fieldGoT = f.GoType
		switch s.function {
		case "sum":
			st.stateType = f.GoType
		case "avg":
			// avg uses two states: running sum (in same numeric type)
			// plus running count.
			st.stateType = f.GoType
			st.needsN = true
		case "min", "max":
			// Track value + a "have any" flag.
			st.stateType = f.GoType
			st.needsN = true // reuses needsN as "haveValue bool"
		}
		states = append(states, st)
	}

	// Build struct definition.
	var d strings.Builder
	fmt.Fprintf(&d, "// %s is the typed.Aggregator[T, R] implementation generated for this group-by.\n", aggTypeName)
	fmt.Fprintf(&d, "type %s struct {\n", aggTypeName)
	for _, s := range states {
		fmt.Fprintf(&d, "\t%s %s\n", s.stateName, s.stateType)
		if s.spec.function == "avg" {
			fmt.Fprintf(&d, "\t%s_n int64\n", s.stateName)
		}
		if s.spec.function == "min" || s.spec.function == "max" {
			fmt.Fprintf(&d, "\t%s_have bool\n", s.stateName)
		}
	}
	for _, p := range exprPlans {
		for _, t := range p.Terms {
			fmt.Fprintf(&d, "\t%s %s // -expr %q term: %s\n", t.GoName, t.Type, p.Spec.result, t.ElemSrc)
		}
		if p.UsesCount {
			fmt.Fprintf(&d, "\t%s int64 // -expr %q count\n", p.CountName, p.Spec.result)
		}
	}
	for _, p := range streamPlans {
		for _, st := range p.States {
			fmt.Fprintf(&d, "\t%s %s // -stream-expr %q state %q\n", st.GoName, st.Type, p.Spec.result, st.Key)
		}
	}
	d.WriteString("}\n\n")

	// Add() method.
	fmt.Fprintf(&d, "func (a *%s) Add(r %s) {\n", aggTypeName, in.TypeName)
	for _, s := range states {
		switch s.spec.function {
		case "count":
			fmt.Fprintf(&d, "\ta.%s++\n", s.stateName)
		case "sum":
			fmt.Fprintf(&d, "\ta.%s += r.%s\n", s.stateName, s.fieldGo)
		case "avg":
			fmt.Fprintf(&d, "\ta.%s += r.%s\n", s.stateName, s.fieldGo)
			fmt.Fprintf(&d, "\ta.%s_n++\n", s.stateName)
		case "min":
			fmt.Fprintf(&d, "\tif !a.%s_have || r.%s < a.%s {\n", s.stateName, s.fieldGo, s.stateName)
			fmt.Fprintf(&d, "\t\ta.%s = r.%s\n", s.stateName, s.fieldGo)
			fmt.Fprintf(&d, "\t\ta.%s_have = true\n", s.stateName)
			d.WriteString("\t}\n")
		case "max":
			fmt.Fprintf(&d, "\tif !a.%s_have || r.%s > a.%s {\n", s.stateName, s.fieldGo, s.stateName)
			fmt.Fprintf(&d, "\t\ta.%s = r.%s\n", s.stateName, s.fieldGo)
			fmt.Fprintf(&d, "\t\ta.%s_have = true\n", s.stateName)
			d.WriteString("\t}\n")
		}
	}
	for _, p := range exprPlans {
		for _, t := range p.Terms {
			fmt.Fprintf(&d, "\ta.%s += %s\n", t.GoName, t.ElemSrc)
		}
		if p.UsesCount {
			fmt.Fprintf(&d, "\ta.%s++\n", p.CountName)
		}
	}
	for _, p := range streamPlans {
		// One SIMULTANEOUS multi-assignment per fold: the VM computes the
		// whole new state object from the OLD state, then replaces it —
		// {s: n, n: s} must swap, so every RHS reads pre-assignment values.
		var lhs, rhs []string
		for i, st := range p.States {
			lhs = append(lhs, "a."+st.GoName)
			rhs = append(rhs, p.Every[i])
		}
		fmt.Fprintf(&d, "\t%s = %s\n", strings.Join(lhs, ", "), strings.Join(rhs, ", "))
	}
	d.WriteString("}\n\n")

	// Result() method — returns a struct with a field per aggregation
	// in the order they were declared. We use a small inner type so
	// the build function in the GroupBy call can pull the values out
	// by name.
	fmt.Fprintf(&d, "func (a *%s) Result() %sResult {\n", aggTypeName, aggTypeName)
	fmt.Fprintf(&d, "\treturn %sResult{\n", aggTypeName)
	for _, s := range states {
		switch s.spec.function {
		case "count":
			fmt.Fprintf(&d, "\t\t%s: a.%s,\n", lib.GoNameFromColumn(s.spec.result), s.stateName)
		case "sum", "min", "max":
			fmt.Fprintf(&d, "\t\t%s: a.%s,\n", lib.GoNameFromColumn(s.spec.result), s.stateName)
		case "avg":
			fmt.Fprintf(&d, "\t\t%s: func() float64 { if a.%s_n == 0 { return 0 }; return float64(a.%s) / float64(a.%s_n) }(),\n",
				lib.GoNameFromColumn(s.spec.result), s.stateName, s.stateName, s.stateName)
		}
	}
	for _, p := range exprPlans {
		fmt.Fprintf(&d, "\t\t%s: %s,\n", lib.GoNameFromColumn(p.Spec.result), p.Result)
	}
	for _, p := range streamPlans {
		fmt.Fprintf(&d, "\t\t%s: %s,\n", lib.GoNameFromColumn(p.Spec.result), p.Final)
	}
	d.WriteString("\t}\n}\n\n")

	// Merge() — only emitted in parallel mode. Each shard's partial
	// aggregator gets folded into the surviving one. The peer is
	// recovered via type assertion; mismatched concrete types are a
	// programmer error and we no-op rather than panic.
	if parallel {
		fmt.Fprintf(&d, "func (a *%s) Merge(other typed.Aggregator[%s, %sResult]) {\n", aggTypeName, in.TypeName, aggTypeName)
		fmt.Fprintf(&d, "\to, ok := other.(*%s)\n", aggTypeName)
		d.WriteString("\tif !ok {\n\t\treturn\n\t}\n")
		for _, s := range states {
			switch s.spec.function {
			case "count", "sum":
				fmt.Fprintf(&d, "\ta.%s += o.%s\n", s.stateName, s.stateName)
			case "avg":
				fmt.Fprintf(&d, "\ta.%s += o.%s\n", s.stateName, s.stateName)
				fmt.Fprintf(&d, "\ta.%s_n += o.%s_n\n", s.stateName, s.stateName)
			case "min":
				fmt.Fprintf(&d, "\tif o.%s_have && (!a.%s_have || o.%s < a.%s) {\n", s.stateName, s.stateName, s.stateName, s.stateName)
				fmt.Fprintf(&d, "\t\ta.%s = o.%s\n", s.stateName, s.stateName)
				fmt.Fprintf(&d, "\t\ta.%s_have = true\n", s.stateName)
				d.WriteString("\t}\n")
			case "max":
				fmt.Fprintf(&d, "\tif o.%s_have && (!a.%s_have || o.%s > a.%s) {\n", s.stateName, s.stateName, s.stateName, s.stateName)
				fmt.Fprintf(&d, "\t\ta.%s = o.%s\n", s.stateName, s.stateName)
				fmt.Fprintf(&d, "\t\ta.%s_have = true\n", s.stateName)
				d.WriteString("\t}\n")
			}
		}
		// -expr accumulators merge by addition — sums and counts are
		// associative, which is exactly why they keep GroupByParallel.
		for _, p := range exprPlans {
			for _, t := range p.Terms {
				fmt.Fprintf(&d, "\ta.%s += o.%s\n", t.GoName, t.GoName)
			}
			if p.UsesCount {
				fmt.Fprintf(&d, "\ta.%s += o.%s\n", p.CountName, p.CountName)
			}
		}
		d.WriteString("}\n\n")
	}

	// The Result struct itself.
	fmt.Fprintf(&d, "// %sResult is the per-group result of the synthesized aggregator.\n", aggTypeName)
	fmt.Fprintf(&d, "type %sResult struct {\n", aggTypeName)
	maxName := 0
	for _, s := range states {
		gn := lib.GoNameFromColumn(s.spec.result)
		if len(gn) > maxName {
			maxName = len(gn)
		}
	}
	for _, p := range exprPlans {
		if gn := lib.GoNameFromColumn(p.Spec.result); len(gn) > maxName {
			maxName = len(gn)
		}
	}
	for _, p := range streamPlans {
		if gn := lib.GoNameFromColumn(p.Spec.result); len(gn) > maxName {
			maxName = len(gn)
		}
	}
	for _, s := range states {
		gn := lib.GoNameFromColumn(s.spec.result)
		fmt.Fprintf(&d, "\t%-*s %s\n", maxName, gn, aggResultGoTypeForState(s.spec.function, s.fieldGoT))
	}
	for _, p := range exprPlans {
		fmt.Fprintf(&d, "\t%-*s float64\n", maxName, lib.GoNameFromColumn(p.Spec.result))
	}
	for _, p := range streamPlans {
		fmt.Fprintf(&d, "\t%-*s float64\n", maxName, lib.GoNameFromColumn(p.Spec.result))
	}
	d.WriteString("}\n")

	return d.String(), "", aggTypeName
}

// buildResultCtor emits the field assignments inside the result-struct
// constructor passed to typed.GroupBy's build function. The build
// function receives:
//   - k: the group key (single value or composite-key struct)
//   - agg: the aggregator's Result() value (the synthesized inner
//     result struct of typed Aggregator[T, S], where S = aggResultName)
func buildResultCtor(groupFields []lib.TypedSchemaField, specs []aggSpec, exprPlans []exprAggPlan, streamPlans []streamAggPlan, aggResultName string) string {
	var b strings.Builder
	// Group-key fields come from k.
	if len(groupFields) == 1 {
		// Key is a single value, not a struct — copy directly.
		fmt.Fprintf(&b, "%s: k,\n", groupFields[0].GoName)
	} else {
		for _, f := range groupFields {
			fmt.Fprintf(&b, "\t\t\t\t%s: k.%s,\n", f.GoName, f.GoName)
		}
	}
	// Aggregation results come from agg directly (it's the Result()
	// value, not the aggregator itself).
	for _, s := range specs {
		gn := lib.GoNameFromColumn(s.result)
		fmt.Fprintf(&b, "\t\t\t\t%s: agg.%s,\n", gn, gn)
	}
	for _, p := range exprPlans {
		gn := lib.GoNameFromColumn(p.Spec.result)
		fmt.Fprintf(&b, "\t\t\t\t%s: agg.%s,\n", gn, gn)
	}
	for _, p := range streamPlans {
		gn := lib.GoNameFromColumn(p.Spec.result)
		fmt.Fprintf(&b, "\t\t\t\t%s: agg.%s,\n", gn, gn)
	}
	return strings.TrimRight(b.String(), ",\n")
}

func needsNumeric(fn string) bool {
	switch fn {
	case "sum", "avg", "min", "max":
		return true
	}
	return false
}

func isNumericGoType(t string) bool {
	switch t {
	case "int", "int32", "int64", "uint64", "float32", "float64":
		return true
	}
	return false
}

// aggResultGoType returns the Go type of an aggregation's output
// field, given the input schema. count → int64; avg → float64; sum,
// min, max → same as the source field's type.
func aggResultGoType(s aggSpec, in *lib.TypedSchema) string {
	if s.function == "count" {
		return "int64"
	}
	if s.function == "avg" {
		return "float64"
	}
	if f, ok := lookupSchemaField(in, s.field); ok {
		return f.GoType
	}
	return "string"
}

// aggResultGoTypeForState mirrors aggResultGoType for the inner result
// struct (where we already have the source field's Go type, not the
// full schema).
func aggResultGoTypeForState(fn, fieldGoType string) string {
	switch fn {
	case "count":
		return "int64"
	case "avg":
		return "float64"
	default:
		return fieldGoType
	}
}
