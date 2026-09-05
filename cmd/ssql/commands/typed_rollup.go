package commands

import (
	"fmt"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// emitTypedRollup is the typed form of `group-by FIELDS aggs -rollup|-cube`
// (DFC048 semantics: one row per detail group, enriched with every
// grouping set's aggregates as prefixed columns). Two fragments:
//
//  1. The DETAIL group-by — typed.GroupByParallel (serial alternative
//     typed.GroupBy) over the raw rows, keyed on all fields, whose
//     per-group result is the aggregator's mergeable STATE rather than
//     its final values. A thin wrapper type gives the generated
//     aggregator that Result(); its Add and Merge are unchanged.
//  2. typed.RollupEnrich over the detail rows: for each grouping set
//     (ssql.RollupGroupingSets — the SAME sets exec uses) it merges the
//     detail states sharing the set's key and builds the output struct
//     with ssql.RollupFieldPrefix names.
//
// The raw rows are read once, in parallel; everything else works on
// #groups × #sets. On a 14.6M-row, 161-group cube this replaced a 36 s
// record fallback (ssql.Rollup re-keys every row once per set) with the
// cost of the plain typed group-by.
//
// Only -count/-sum/-avg/-min/-max qualify: they are the aggregations
// with a Merge. -collect and expression aggregations return
// (false, reason) and the caller keeps the record fallback.
func emitTypedRollup(inputVar string, in *lib.TypedSchema, groupFields []string, specs []aggSpec, exprSpecs []exprSpec, streamSpecs []streamExprSpec, mode ssql.RollupMode) (bool, string, error) {
	if len(exprSpecs) > 0 || len(streamSpecs) > 0 {
		return false, "-rollup/-cube with -expr/-stream-expr has no typed form", nil
	}
	for _, s := range specs {
		if s.function == "collect" {
			return false, "-rollup/-cube with -collect has no typed form (collect has no mergeable state)", nil
		}
	}

	// Validate fields the same way the plain typed group-by does.
	groupSchemaFields := make([]lib.TypedSchemaField, 0, len(groupFields))
	for _, name := range groupFields {
		f, ok := lookupSchemaField(in, name)
		if !ok {
			return true, "", lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'group-by': unknown field %q", name))
		}
		groupSchemaFields = append(groupSchemaFields, f)
	}
	for _, s := range specs {
		if s.function == "count" {
			continue
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

	aggTypeName := in.TypeName + "Aggregator"
	wrapName := in.TypeName + "RollupAgg"
	detailName := in.TypeName + "Detail"
	resultName := in.TypeName + "Group"
	keyType, keyExpr, keyDef := buildGroupKeyType(in, groupSchemaFields)
	aggDef, _, _ := buildTypedAggregator(aggTypeName, in, specs, nil, nil, groupSchemaFields, resultName, true)

	// The wrapper: the generated aggregator's Result() yields final
	// values; for rollup the detail stage must hand over the STATE, so
	// RollupEnrich can merge detail groups into their parents.
	var w strings.Builder
	fmt.Fprintf(&w, "// %s makes the aggregator's mergeable state the per-group result,\n", wrapName)
	w.WriteString("// so typed.RollupEnrich can fold detail groups into every grouping set.\n")
	fmt.Fprintf(&w, "type %s struct{ %s }\n\n", wrapName, aggTypeName)
	fmt.Fprintf(&w, "func (a *%s) Result() *%s { return &a.%s }\n\n", wrapName, aggTypeName, aggTypeName)
	fmt.Fprintf(&w, "func (a *%s) Merge(other typed.Aggregator[%s, *%s]) {\n", wrapName, in.TypeName, aggTypeName)
	fmt.Fprintf(&w, "\tif o, ok := other.(*%s); ok {\n\t\ta.%s.Merge(&o.%s)\n\t}\n}\n", wrapName, aggTypeName, aggTypeName)

	// The detail row: the full key plus the state.
	var d strings.Builder
	fmt.Fprintf(&d, "// %s is one full-key group with its aggregation state (the rollup's detail level).\n", detailName)
	fmt.Fprintf(&d, "type %s struct {\n", detailName)
	for _, f := range groupSchemaFields {
		fmt.Fprintf(&d, "\t%s %s `ssql:%q`\n", f.GoName, f.GoType, f.Name)
	}
	fmt.Fprintf(&d, "\tState *%s\n}\n", aggTypeName)
	detailSchema := &lib.TypedSchema{TypeName: detailName, Fields: append(append([]lib.TypedSchemaField{}, groupSchemaFields...),
		lib.TypedSchemaField{Name: "_state", GoName: "State", GoType: "*" + aggTypeName})}

	// The output row: fields, then per grouping set (exec's order) each
	// aggregation under its prefixed name.
	sets := ssql.RollupGroupingSets(groupFields, mode)
	outFields := append([]lib.TypedSchemaField{}, groupSchemaFields...)
	usedGo := make(map[string]int, len(outFields))
	for _, f := range outFields {
		usedGo[f.GoName]++
	}
	type outAgg struct {
		set  int
		spec aggSpec
		goNm string
	}
	var outAggs []outAgg
	for i, set := range sets {
		prefix := ssql.RollupFieldPrefix(set)
		for _, s := range specs {
			name := prefix + s.result
			gn := lib.GoNameFromColumn(name)
			if usedGo[gn] > 0 {
				usedGo[gn]++
				gn = fmt.Sprintf("%s%d", gn, usedGo[gn])
			} else {
				usedGo[gn] = 1
			}
			outFields = append(outFields, lib.TypedSchemaField{Name: name, GoName: gn, GoType: aggResultGoType(s, in)})
			outAggs = append(outAggs, outAgg{i, s, gn})
		}
	}
	outSchema := &lib.TypedSchema{TypeName: resultName, Fields: outFields}
	outDef := renderResultStructDef(outSchema)

	// ---- fragment 1: the detail group-by (dual templates) ----
	var keyCtor []string
	if len(groupSchemaFields) == 1 {
		keyCtor = append(keyCtor, fmt.Sprintf("%s: k", groupSchemaFields[0].GoName))
	} else {
		for _, f := range groupSchemaFields {
			keyCtor = append(keyCtor, fmt.Sprintf("%s: k.%s", f.GoName, f.GoName))
		}
	}
	makeDetail := func(groupFn, aggIface string) string {
		return fmt.Sprintf(`detail := %s(%s,
		func(r %s) %s { return %s },
		func() %s[%s, *%s] { return &%s{} },
		func(k %s, st *%s) %s {
			return %s{%s, State: st}
		})`,
			groupFn, inputVar,
			in.TypeName, keyType, keyExpr,
			aggIface, in.TypeName, aggTypeName, wrapName,
			keyType, aggTypeName, detailName,
			detailName, strings.Join(keyCtor, ", "))
	}
	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if schemaUsesTime(in) {
		imports = append(imports, "time")
	}
	defs := []string{aggDef}
	if keyDef != "" {
		defs = append(defs, keyDef)
	}
	defs = append(defs, w.String(), d.String())

	detailFrag := lib.NewStmtFragment("detail", inputVar, makeDetail("typed.GroupByParallel", "typed.ParallelAggregator"), imports, getCommandString())
	detailFrag.InputTypedSchema = in
	detailFrag.OutputTypedSchema = detailSchema
	detailFrag.StructDefs = defs
	detailFrag.PlanNotes = []string{fmt.Sprintf("native typed rollup: parallel detail group-by + typed.RollupEnrich over %d grouping sets", len(sets))}
	detailFrag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeSeqTyped}
	detailFrag.AltCodeIfSeq = makeDetail("typed.GroupBy", "typed.Aggregator")
	detailFrag.AltImportsIfSeq = imports
	detailFrag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	if err := lib.WriteCodeFragment(detailFrag); err != nil {
		return true, "", err
	}

	// ---- fragment 2: the enrichment ----
	var setLits []string
	needFmt := false
	for _, set := range sets {
		key := `""`
		if len(set) > 0 {
			var verbs, args []string
			for _, f := range set {
				sf, _ := lookupSchemaField(in, f)
				verbs = append(verbs, "%v")
				args = append(args, "d."+sf.GoName)
			}
			key = fmt.Sprintf("fmt.Sprintf(%q, %s)", strings.Join(verbs, "\x00"), strings.Join(args, ", "))
			needFmt = true
		}
		setLits = append(setLits, fmt.Sprintf(`{Key: func(d %s) string { return %s },
				New: func() *%s { return &%s{} }, Merge: func(acc, part *%s) { acc.Merge(part) }}`,
			detailName, key, aggTypeName, aggTypeName, aggTypeName))
	}
	var outCtor []string
	for _, f := range groupSchemaFields {
		outCtor = append(outCtor, fmt.Sprintf("%s: d.%s", f.GoName, f.GoName))
	}
	for _, oa := range outAggs {
		outCtor = append(outCtor, fmt.Sprintf("%s: sets[%d].Result().%s", oa.goNm, oa.set, lib.GoNameFromColumn(oa.spec.result)))
	}
	code := fmt.Sprintf(`grouped := typed.RollupEnrich(detail,
		[]typed.RollupSet[%s, *%s]{
			%s,
		},
		func(d %s) *%s { return d.State },
		func(d %s, sets []*%s) %s {
			return %s{
				%s,
			}
		})`,
		detailName, aggTypeName,
		strings.Join(setLits, ",\n\t\t\t"),
		detailName, aggTypeName,
		detailName, aggTypeName, resultName,
		resultName, strings.Join(outCtor, ",\n\t\t\t\t"))
	imports2 := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if needFmt {
		imports2 = append(imports2, "fmt")
	}
	if schemaUsesTime(outSchema) {
		imports2 = append(imports2, "time")
	}
	// Empty Command: same source command as the detail fragment (the
	// assembler's pipeline comment must list it once).
	enrichFrag := lib.NewStmtFragment("grouped", "detail", code, imports2, "")
	enrichFrag.InputTypedSchema = detailSchema
	enrichFrag.OutputTypedSchema = outSchema
	enrichFrag.StructDefs = []string{outDef}
	enrichFrag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	return true, "", lib.WriteCodeFragment(enrichFrag)
}
