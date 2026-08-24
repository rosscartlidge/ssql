package commands

import (
	"fmt"
	"iter"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// aggSpec represents a built-in aggregation specification
type aggSpec struct {
	function string
	field    string
	result   string
}

// exprSpec represents a custom expression aggregation specification
type exprSpec struct {
	expression string
	result     string
}

// RegisterGroupBy registers the group-by subcommand
func RegisterGroupBy(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("group-by").
		Description("Group records by fields and apply aggregations").
		Example("ssql from sales.csv | ssql group-by region", "Get unique values of region field").
		Example("ssql from sales.csv | ssql group-by region -count total", "Count records by region").
		Example("ssql from sales.csv | ssql group-by region -sum amount total_sales", "Sum sales amount by region").
		Example("ssql from data.csv | ssql group-by dept -count num_employees -avg salary avg_salary -sum hours total_hours", "Multiple aggregations in one command").
		Example("ssql from data.csv | ssql group-by dept -collect name all_names", "Collect all names into array per department").
		Example("ssql from data.csv | ssql group-by dept -expr 'sum(salary * bonus)' total_comp", "Custom expression aggregation").
		Example("ssql from huge.csv | ssql group-by dept -stream-expr '{s:0}' '{s:s+salary}' 's' total", "Memory-efficient streaming aggregation").
		Example("ssql from data.csv | ssql group-by a_kind z_kind -count count -rollup", "Hierarchical rollup with parent-level counts").
		Example("ssql from data.csv | ssql group-by a_kind z_kind -count count -cube", "Full cube with all combination counts").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("FIELDS").
		String().
		Variadic().
		FieldsFromFlag("").
		Global().
		Help("Fields to group by").
		Done().
		Flag("-count").
		Arg("result-name").
		Completer(cf.NoCompleter{Hint: "<name>"}).
		Done().
		Accumulate().
		Global().
		Help("Count records (result field name)").
		Done().
		Flag("-sum").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Arg("result-name").
		Completer(cf.NoCompleter{Hint: "<name>"}).
		Done().
		Accumulate().
		Global().
		Help("Sum field values (field name, result name)").
		Done().
		Flag("-avg").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Arg("result-name").
		Completer(cf.NoCompleter{Hint: "<name>"}).
		Done().
		Accumulate().
		Global().
		Help("Average field values (field name, result name)").
		Done().
		Flag("-min").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Arg("result-name").
		Completer(cf.NoCompleter{Hint: "<name>"}).
		Done().
		Accumulate().
		Global().
		Help("Minimum field value (field name, result name)").
		Done().
		Flag("-max").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Arg("result-name").
		Completer(cf.NoCompleter{Hint: "<name>"}).
		Done().
		Accumulate().
		Global().
		Help("Maximum field value (field name, result name)").
		Done().
		Flag("-collect").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Arg("result-name").
		Completer(cf.NoCompleter{Hint: "<name>"}).
		Done().
		Accumulate().
		Global().
		Help("Collect all field values into array (field name, result name)").
		Done().
		Flag("-expr", "-e").
		Arg("expression").
		Completer(cf.NoCompleter{Hint: "<expression>"}).
		Done().
		Arg("result-name").
		Completer(cf.NoCompleter{Hint: "<name>"}).
		Done().
		Accumulate().
		Global().
		Help("Custom aggregation expression: -expr 'sum(salary * bonus)' total").
		Done().
		Flag("-stream-expr").
		Arg("init").
		Completer(cf.NoCompleter{Hint: "<init-expr>"}).
		Done().
		Arg("every").
		Completer(cf.NoCompleter{Hint: "<every-expr>"}).
		Done().
		Arg("final").
		Completer(cf.NoCompleter{Hint: "<final-expr>"}).
		Done().
		Arg("result-name").
		Completer(cf.NoCompleter{Hint: "<name>"}).
		Done().
		Accumulate().
		Global().
		Help("Streaming aggregation: -stream-expr '{s:0}' '{s:s+salary}' 's' total").
		Done().
		Flag("-presorted").
		Bool().
		Global().
		Help("Input is presorted by group fields (streaming, O(1) memory per group)").
		Done().
		Flag("-rollup").
		Bool().
		Global().
		Help("Hierarchical rollup: enrich rows with parent-level aggregations").
		Done().
		Flag("-cube").
		Bool().
		Global().
		Help("Full cube: enrich rows with all combination aggregations").
		Done().
		Handler(func(ctx *cf.Context) error {
			if schemaMode() {
				return runSchemaModeTransform(ctx, "group-by")
			}

			var groupByFields []string
			var generate bool

			// Extract group-by fields from variadic positional
			if fieldsVal, ok := ctx.GlobalFlags["FIELDS"]; ok {
				switch v := fieldsVal.(type) {
				case []string:
					groupByFields = v
				case []any:
					for _, item := range v {
						if s, ok := item.(string); ok {
							groupByFields = append(groupByFields, s)
						}
					}
				case string:
					groupByFields = []string{v}
				}
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Decode all aggregation + modifier flags once. The same
			// helper feeds the codegen path (and, later, the schema
			// rule), so the flag-shape decoding lives in one place.
			specs := parseGroupBySpecs(ctx)
			rollup, cube, presorted := specs.rollup, specs.cube, specs.presorted

			// Empty group-by fields means "single group spanning all rows"
			// (SQL `GROUP BY ()`) — useful for `group-by -count n` to get
			// a single total. Rollup/cube need at least one field
			// because the grouping-set logic depends on it.
			if (rollup || cube) && len(groupByFields) == 0 {
				return fmt.Errorf("-rollup/-cube requires at least one group-by field")
			}

			if rollup && cube {
				return fmt.Errorf("cannot use both -rollup and -cube; choose one")
			}

			if presorted && (rollup || cube) {
				return fmt.Errorf("-presorted cannot be combined with -rollup or -cube")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateGroupByCode(ctx, groupByFields)
			}

			// Aggregation specs were decoded above by parseGroupBySpecs.
			aggSpecs, exprSpecs, streamExprSpecs := specs.aggs, specs.exprs, specs.streamExprs

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records
			inputSchema := schemaAndRecords.Schema

			// Validate field names against schema
			var fieldsToValidate []string
			fieldsToValidate = append(fieldsToValidate, groupByFields...)
			for _, spec := range aggSpecs {
				if spec.field != "" { // count has no field
					fieldsToValidate = append(fieldsToValidate, spec.field)
				}
			}
			if err := validateFieldsSchema(inputSchema, fieldsToValidate, "group-by"); err != nil {
				return err
			}

			// Check if we have any aggregations
			hasAnyAgg := len(aggSpecs) > 0 || len(exprSpecs) > 0 || len(streamExprSpecs) > 0

			// Validate rollup/cube requires aggregations
			if (rollup || cube) && !hasAnyAgg {
				return fmt.Errorf("-rollup/-cube requires at least one aggregation (-count, -sum, etc.)")
			}

			// Rollup/cube path: use ssql.Rollup()
			if (rollup || cube) && hasAnyAgg {
				mode := ssql.RollupHierarchical
				if cube {
					mode = ssql.RollupCube
				}

				aggregations := make(map[string]ssql.AggregateFunc)
				for _, spec := range aggSpecs {
					agg, err := buildAggregator(spec.function, spec.field)
					if err != nil {
						return err
					}
					aggregations[spec.result] = agg
				}
				for _, spec := range exprSpecs {
					aggregations[spec.result] = ssql.ExprAgg(spec.expression)
				}

				config := ssql.RollupConfig{
					Fields:       groupByFields,
					Aggregations: aggregations,
					Mode:         mode,
				}

				result := ssql.Rollup(config)(records)

				// Build output schema
				var outputSchema *lib.Schema
				if inputSchema != nil {
					outputSchema = lib.NewSchema()
					for _, field := range groupByFields {
						if inputSchema.HasField(field) {
							outputSchema.AddField(field, inputSchema.TypeOf(field))
						}
					}
					// Add all prefixed aggregation fields
					sets := computeGroupingSetsForSchema(groupByFields, mode)
					for _, setFields := range sets {
						prefix := groupingSetPrefixForSchema(setFields)
						for _, spec := range aggSpecs {
							outputSchema.AddField(prefix+spec.result, aggResultType(spec.function))
						}
						for _, spec := range exprSpecs {
							outputSchema.AddField(prefix+spec.result, "float")
						}
					}
				}

				if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outputSchema, result); err != nil {
					return fmt.Errorf("writing output: %w", err)
				}
				return nil
			}

			// If no aggregations, output unique groupings (DISTINCT on grouped fields)
			if !hasAnyAgg {
				// Build key function for distinct
				keyFn := func(r ssql.Record) string {
					var parts []string
					for _, field := range groupByFields {
						parts = append(parts, fmt.Sprintf("%v", ssql.GetOr(r, field, "")))
					}
					return strings.Join(parts, "\x00")
				}

				// Build set of fields to keep
				keepFields := make(map[string]bool)
				for _, field := range groupByFields {
					keepFields[field] = true
				}

				// Get distinct records and project only grouped fields
				distinct := ssql.DistinctBy(keyFn)(records)
				projected := ssql.Select(func(r ssql.Record) ssql.Record {
					mut := r.ToMutable()
					for k := range r.All() {
						if !keepFields[k] {
							mut = mut.Delete(k)
						}
					}
					return mut.Freeze()
				})(distinct)

				// Build output schema with only group-by fields
				var outputSchema *lib.Schema
				if inputSchema != nil {
					outputSchema = lib.NewSchema()
					for _, field := range groupByFields {
						if inputSchema.HasField(field) {
							outputSchema.AddField(field, inputSchema.TypeOf(field))
						}
					}
				}

				if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outputSchema, projected); err != nil {
					return fmt.Errorf("writing output: %w", err)
				}
				return nil
			}

			// Standard path: use ssql.GroupByFields + ssql.Aggregate
			// Supports built-in aggregations (-count, -sum, etc.), expressions (-expr),
			// and streaming expressions (-stream-expr) via ssql.StreamExprAgg()
			var grouped iter.Seq[ssql.Record]
			if presorted {
				grouped = ssql.StreamGroupByFields("_group", groupByFields...)(records)
			} else {
				grouped = ssql.GroupByFields("_group", groupByFields...)(records)
			}

			// Build aggregations map
			aggregations := make(map[string]ssql.AggregateFunc)
			for _, spec := range aggSpecs {
				agg, err := buildAggregator(spec.function, spec.field)
				if err != nil {
					return err
				}
				aggregations[spec.result] = agg
			}
			// Add expression aggregations using ssql.ExprAgg()
			for _, spec := range exprSpecs {
				aggregations[spec.result] = ssql.ExprAgg(spec.expression)
			}
			// Add streaming expression aggregations using ssql.StreamExprAgg()
			for _, spec := range streamExprSpecs {
				aggregations[spec.result] = ssql.StreamExprAgg(spec.initExpr, spec.everyExpr, spec.finalExpr)
			}

			// Apply Aggregate
			aggregated := ssql.Aggregate("_group", aggregations)(grouped)

			// Build output schema: group-by fields + aggregation result fields
			var outputSchema *lib.Schema
			if inputSchema != nil {
				outputSchema = lib.NewSchema()
				// Add group-by fields with their input types
				for _, field := range groupByFields {
					if inputSchema.HasField(field) {
						outputSchema.AddField(field, inputSchema.TypeOf(field))
					}
				}
				// Add aggregation result fields
				for _, spec := range aggSpecs {
					outputSchema.AddField(spec.result, aggResultType(spec.function))
				}
				// Add expression result fields
				for _, spec := range exprSpecs {
					outputSchema.AddField(spec.result, "float") // ExprAgg always returns float64
				}
				// Add streaming expression result fields
				for _, spec := range streamExprSpecs {
					outputSchema.AddField(spec.result, "float") // StreamExprAgg always returns float64
				}
			}

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outputSchema, aggregated); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateGroupByCode generates Go code for the group-by command
func generateGroupByCode(ctx *cf.Context, groupByFields []string) error {
	// Read all previous code fragments from stdin (if any)
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	// Pass through all previous fragments
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	// Get input variable from last fragment
	var inputVar string
	var prevSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	} else {
		inputVar = "records"
	}

	// Empty group-by fields means "single group spanning all rows"
	// (SQL `GROUP BY ()`) — generates code that emits one summary row
	// with just the aggregation result fields.

	// Decode all aggregation + modifier flags (shared with the exec
	// handler via parseGroupBySpecs — single decode point).
	specs := parseGroupBySpecs(ctx)
	aggSpecs, exprSpecs, streamExprSpecs := specs.aggs, specs.exprs, specs.streamExprs
	rollup, cube, presorted := specs.rollup, specs.cube, specs.presorted

	if rollup && cube {
		return fmt.Errorf("cannot use both -rollup and -cube; choose one")
	}
	if presorted && (rollup || cube) {
		return fmt.Errorf("-presorted cannot be combined with -rollup or -cube")
	}

	// Typed-mode branch — emits typed.GroupBy (or typed.GroupByParallel
	// in parallel mode) with a synthesized aggregator and a derived
	// result struct. -expr aggregations lower via the patcher normal
	// form to MERGEABLE accumulator fields (expr-transpiler Phase 3;
	// keeps the parallel form); -stream-expr folds lower to accumulator
	// fields too but force the serial form (Phase 2; fold state is not
	// mergeable). No -rollup / -cube, no -collect (deferred).
	// Phase B fall-throughs: prevSchema==nil → Record-mode upstream;
	// an expression shape a typed struct can't hold → record path
	// below, with the reason surfaced under -explain.
	var typedFallbackNotes []string
	if typedMode() && prevSchema != nil {
		if rollup || cube {
			// No typed runtime for rollup/cube (the enriched output
			// carries per-grouping-set prefixed fields). Eject to the
			// record path below — the planner inserts the typed→Record
			// boundary, so the typed (parallel) upstream is kept.
			typedFallbackNotes = append(typedFallbackNotes,
				"record fallback (-rollup / -cube have no typed form)")
		} else if len(aggSpecs) == 0 && len(exprSpecs) == 0 && len(streamExprSpecs) == 0 {
			// 'group-by FIELDS' with no aggregations is semantically
			// equivalent to `include FIELDS | distinct` — project to
			// the kept fields and dedupe. Emit two fragments rather
			// than a third dedicated codepath: the projection
			// (Subset struct + typed.Select) and a typed.Distinct on
			// the projected stream. typed.Distinct is iter.Seq-only,
			// so the planner inserts a Stream.Serial() boundary
			// upstream when needed.
			projVar, perr := emitTypedProjection("include", "Subset", inputVar, prevSchema, groupByFields, false, nil, fragments)
			if perr != nil {
				return perr
			}
			// emitTypedProjection wrote a fragment with Var "included"
			// and OutputTypedSchema = the subset schema. Append the
			// distinct stage referencing it.
			derived, _, derr := buildDerivedSchema(prevSchema, "Subset", groupByFields, false, nil)
			if derr != nil {
				return lib.WriteErrorAndExit(getCommandString(), derr)
			}
			groupedVar := uniqueVarName("grouped", fragments)
			// Dual templates: DistinctParallel keeps the upstream
			// Stream parallel (per-shard dedupe, tiny serial merge —
			// the SerialOnly form cost 6.7s vs 1.5s on 14.6M parquet
			// rows); the planner downgrades to the serial Distinct
			// when the upstream is already iter.Seq.
			parallelCode := fmt.Sprintf("%s := typed.DistinctParallel(%s, func(r %s) %s { return r })",
				groupedVar, projVar, derived.TypeName, derived.TypeName)
			serialCode := fmt.Sprintf("%s := typed.Distinct(func(r %s) %s { return r })(%s)",
				groupedVar, derived.TypeName, derived.TypeName, projVar)
			// Empty Command on the second fragment: both fragments
			// belong to the same source command (`ssql group-by FIELD`),
			// and the assembler builds the pipeline-comment list from
			// each fragment's Command — recording the same command
			// twice would duplicate it in the output header.
			distinctFrag := lib.NewStmtFragment(groupedVar, projVar, parallelCode,
				[]string{"github.com/rosscartlidge/ssql/v4/typed"}, "")
			distinctFrag.InputTypedSchema = derived
			distinctFrag.OutputTypedSchema = derived
			distinctFrag.Capabilities = &lib.Capabilities{
				Accepts: lib.ShapeStream, Produces: lib.ShapeSeqTyped,
			}
			distinctFrag.AltCodeIfSeq = serialCode
			distinctFrag.AltImportsIfSeq = []string{"github.com/rosscartlidge/ssql/v4/typed"}
			distinctFrag.AltCapabilitiesIfSeq = &lib.Capabilities{
				Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped,
			}
			return lib.WriteCodeFragment(distinctFrag)
		} else {
			for _, s := range aggSpecs {
				if s.function == "collect" {
					return lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: -collect not yet supported (would need slice-typed result fields); drop -typed for now"))
				}
			}
			// emitTypedGroupBy now always emits dual templates (parallel
			// + serial) for non-presorted, and the planner picks per
			// pipeline. -presorted and -stream-expr force SerialOnly
			// (GroupByOrdered needs contiguous keys; fold state is not
			// mergeable), single-template emission. The `useParallel` arg
			// is vestigial now — kept for API stability but ignored.
			handled, reason, err := emitTypedGroupBy(inputVar, prevSchema, groupByFields, aggSpecs, exprSpecs, streamExprSpecs, presorted, true)
			if handled || err != nil {
				return err
			}
			typedFallbackNotes = append(typedFallbackNotes, fmt.Sprintf("record fallback (%s)", reason))
		}
	}

	// Rollup/cube code generation path
	if (rollup || cube) && (len(aggSpecs) > 0 || len(exprSpecs) > 0 || len(streamExprSpecs) > 0) {
		modeStr := "ssql.RollupHierarchical"
		if cube {
			modeStr = "ssql.RollupCube"
		}

		// Build fields list
		var fieldsList strings.Builder
		for i, field := range groupByFields {
			if i > 0 {
				fieldsList.WriteString(", ")
			}
			fieldsList.WriteString(fmt.Sprintf("%q", field))
		}

		// Build aggregations map
		var aggLines strings.Builder
		first := true
		for _, spec := range aggSpecs {
			if !first {
				aggLines.WriteString(",\n")
			}
			first = false
			aggLines.WriteString(fmt.Sprintf("\t\t\t%q: %s", spec.result, generateAggregatorCode(spec)))
		}
		for _, spec := range exprSpecs {
			if !first {
				aggLines.WriteString(",\n")
			}
			first = false
			aggLines.WriteString(fmt.Sprintf("\t\t\t%q: ssql.ExprAgg(%q)", spec.result, spec.expression))
		}
		for _, spec := range streamExprSpecs {
			if !first {
				aggLines.WriteString(",\n")
			}
			first = false
			aggLines.WriteString(fmt.Sprintf("\t\t\t%q: ssql.StreamExprAgg(%q, %q, %q)", spec.result, spec.initExpr, spec.everyExpr, spec.finalExpr))
		}

		code := fmt.Sprintf(`rollupResult := ssql.Rollup(ssql.RollupConfig{
		Fields: []string{%s},
		Aggregations: map[string]ssql.AggregateFunc{
%s,
		},
		Mode: %s,
	})(%s)`, fieldsList.String(), aggLines.String(), modeStr, inputVar)

		frag := lib.NewStmtFragment("rollupResult", inputVar, code, nil, getCommandString())
		frag.PlanNotes = typedFallbackNotes
		return lib.WriteCodeFragment(frag)
	}

	// If no aggregations at all, generate code for DISTINCT on grouped
	// fields. Must emit a SINGLE Filter expression (the record-mode
	// assembler runs extractFilter() over each stmt's Code, which only
	// understands `var := filter(input)`); ssql.Chain glues the
	// DistinctBy + Select projection into one filter.
	if len(aggSpecs) == 0 && len(exprSpecs) == 0 && len(streamExprSpecs) == 0 {
		// Inline the distinct-key extraction. Each field becomes one
		// %v slot in a fmt.Sprintf — short, no closure-captured slice.
		var keyParts []string
		for _, field := range groupByFields {
			keyParts = append(keyParts, fmt.Sprintf("ssql.GetOr(r, %q, \"\")", field))
		}
		var keyFmt strings.Builder
		for i := range groupByFields {
			if i > 0 {
				keyFmt.WriteString("\\x00")
			}
			keyFmt.WriteString("%v")
		}
		// Field-keep check: a small switch instead of a per-record map
		// allocation. Cheap and inline.
		var keepCases strings.Builder
		for _, field := range groupByFields {
			keepCases.WriteString(fmt.Sprintf("\n\t\t\tcase %q:\n\t\t\t\tcontinue", field))
		}

		code := fmt.Sprintf(`grouped := ssql.Chain(
		ssql.DistinctBy(func(r ssql.Record) string {
			return fmt.Sprintf("%s", %s)
		}),
		ssql.Select(func(r ssql.Record) ssql.Record {
			mut := r.ToMutable()
			for k := range r.All() {
				switch k {%s
				}
				mut = mut.Delete(k)
			}
			return mut.Freeze()
		}),
	)(%s)`,
			keyFmt.String(),
			strings.Join(keyParts, ", "),
			keepCases.String(),
			inputVar,
		)

		frag := lib.NewStmtFragment("grouped", inputVar, code, []string{"fmt"}, getCommandString())
		return lib.WriteCodeFragment(frag)
	}

	// Standard path: Generate TWO fragments for GroupByFields + Aggregate
	// Both built-in aggregations (-count, -sum, etc.) and expressions (-expr) use this path

	// Fragment 1: GroupByFields (or StreamGroupByFields if presorted)
	groupFunc := "ssql.GroupByFields"
	if presorted {
		groupFunc = "ssql.StreamGroupByFields"
	}
	var groupCode strings.Builder
	groupCode.WriteString(fmt.Sprintf("grouped := %s(\"_group\"", groupFunc))
	for _, field := range groupByFields {
		groupCode.WriteString(fmt.Sprintf(", %q", field))
	}
	groupCode.WriteString(fmt.Sprintf(")(%s)", inputVar))

	frag1 := lib.NewStmtFragment("grouped", inputVar, groupCode.String(), nil, getCommandString())
	frag1.PlanNotes = typedFallbackNotes
	if err := lib.WriteCodeFragment(frag1); err != nil {
		return fmt.Errorf("writing GroupByFields fragment: %w", err)
	}

	// Fragment 2: Aggregate
	// Note: Empty command string since this is part of the same CLI command as Fragment 1
	var aggCode strings.Builder
	aggCode.WriteString("aggregated := ssql.Aggregate(\"_group\", map[string]ssql.AggregateFunc{\n")
	first := true
	for _, spec := range aggSpecs {
		if !first {
			aggCode.WriteString(",\n")
		}
		first = false
		aggCode.WriteString(fmt.Sprintf("\t\t%q: %s", spec.result, generateAggregatorCode(spec)))
	}
	// Add expression aggregations using ssql.ExprAgg()
	for _, spec := range exprSpecs {
		if !first {
			aggCode.WriteString(",\n")
		}
		first = false
		aggCode.WriteString(fmt.Sprintf("\t\t%q: ssql.ExprAgg(%q)", spec.result, spec.expression))
	}
	// Add streaming expression aggregations using ssql.StreamExprAgg()
	for _, spec := range streamExprSpecs {
		if !first {
			aggCode.WriteString(",\n")
		}
		first = false
		aggCode.WriteString(fmt.Sprintf("\t\t%q: ssql.StreamExprAgg(%q, %q, %q)", spec.result, spec.initExpr, spec.everyExpr, spec.finalExpr))
	}
	aggCode.WriteString(",\n\t})(grouped)")

	frag2 := lib.NewStmtFragment("aggregated", "grouped", aggCode.String(), nil, "")
	// Populate OutputRecordFields so downstream `to table` codegen
	// can match the column order the CLI pipeline produces (group-by
	// fields first, then aggregations in the order they were
	// declared on the command line). Without this, the generated
	// program goes through ssql.GroupByFields → MutableRecord →
	// alphabetical-sorted schema, and the table renders columns in
	// alphabetical order — different from the CLI pipeline's
	// JSONL-schema-driven order.
	outputFields := append([]string{}, groupByFields...)
	for _, spec := range aggSpecs {
		outputFields = append(outputFields, spec.result)
	}
	for _, spec := range exprSpecs {
		outputFields = append(outputFields, spec.result)
	}
	for _, spec := range streamExprSpecs {
		outputFields = append(outputFields, spec.result)
	}
	frag2.OutputRecordFields = outputFields
	return lib.WriteCodeFragment(frag2)
}

// generateAggregatorCode generates code for a single aggregator
func generateAggregatorCode(spec aggSpec) string {
	switch spec.function {
	case "count":
		return "ssql.Count()"
	case "sum":
		return fmt.Sprintf("ssql.Sum(%q)", spec.field)
	case "avg":
		return fmt.Sprintf("ssql.Avg(%q)", spec.field)
	case "min":
		return fmt.Sprintf("ssql.Min[float64](%q)", spec.field)
	case "max":
		return fmt.Sprintf("ssql.Max[float64](%q)", spec.field)
	case "collect":
		return fmt.Sprintf("ssql.Collect(%q)", spec.field)
	default:
		return ""
	}
}

// buildMapLiteral builds a Go map literal string like: "field1": true, "field2": true
func buildMapLiteral(fields []string) string {
	var parts []string
	for _, field := range fields {
		parts = append(parts, fmt.Sprintf("%q: true", field))
	}
	return strings.Join(parts, ", ")
}

// computeGroupingSetsForSchema mirrors ssql.computeGroupingSets for schema building.
func computeGroupingSetsForSchema(fields []string, mode ssql.RollupMode) [][]string {
	switch mode {
	case ssql.RollupCube:
		n := len(fields)
		sets := make([][]string, 0, 1<<n)
		for mask := range 1 << n {
			var subset []string
			for i := range n {
				if mask&(1<<i) != 0 {
					subset = append(subset, fields[i])
				}
			}
			sets = append(sets, subset)
		}
		return sets
	default: // RollupHierarchical
		sets := make([][]string, len(fields)+1)
		sets[0] = []string{}
		for i := 1; i <= len(fields); i++ {
			sets[i] = make([]string, i)
			copy(sets[i], fields[:i])
		}
		return sets
	}
}

// groupingSetPrefixForSchema mirrors ssql.groupingSetPrefix for schema building.
func groupingSetPrefixForSchema(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, "_") + "_"
}

// aggResultType returns the schema type for an aggregation function result
func aggResultType(function string) string {
	switch function {
	case "count":
		return "int"
	case "sum", "avg", "min", "max":
		return "float"
	case "collect":
		return "json"
	default:
		return "float"
	}
}
