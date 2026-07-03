package commands

import (
	"fmt"
	"strconv"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterTop registers the top subcommand
func RegisterTop(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("top").
		Description("Select top N records by field value (heap-based, O(K) memory)").
		Example("ssql from data.csv | ssql top 10 -field salary", "Top 10 by salary (descending)").
		Example("ssql from data.csv | ssql top 5 -field age -asc", "Bottom 5 by age (ascending)").
		Example("ssql from sales.csv | ssql top 3 -field revenue | ssql to table", "Top 3 revenue records as table").

		Flag("N").
			String().
			Required().
			Global().
			Help("Number of records to return").
			Done().

		Flag("-field", "-f").
			String().
			Required().
			FieldsFromFlag("").
			Global().
			Help("Field to rank by").
			Done().

		Flag("-asc").
			Bool().
			Global().
			Help("Return bottom N instead (ascending, use +asc for top N)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			var nStr string
			var field string
			var asc bool
			var generate bool

			if val, ok := ctx.GlobalFlags["N"]; ok {
				nStr = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-field"]; ok {
				field = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-asc"]; ok {
				asc = val.(bool)
			}
			if val, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = val.(bool)
			}

			n, err := strconv.Atoi(nStr)
			if err != nil || n < 0 {
				return fmt.Errorf("N must be a non-negative integer, got %q", nStr)
			}
			if field == "" {
				return fmt.Errorf("no field specified (use -field)")
			}

			if shouldGenerate(generate) {
				return generateTopCode(n, field, asc)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Rank by the field's natural type — numeric when both values
			// are numbers, lexicographic otherwise — via the same
			// type-aware comparator `sort` uses (ssql.CompareAny). This
			// matches the typed codegen (which keys by the field's Go type)
			// and fixes string fields: the previous numeric-only key coerced
			// every string to 0, so `top -field <string>` silently returned
			// arbitrary rows instead of the lexicographic top-N.
			cmp := func(a, b ssql.Record) int {
				av, _ := ssql.Get[any](a, field)
				bv, _ := ssql.Get[any](b, field)
				return ssql.CompareAny(av, bv)
			}

			var result = records
			if asc {
				result = ssql.BottomByFunc(n, cmp)(records)
			} else {
				result = ssql.TopByFunc(n, cmp)(records)
			}

			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), schemaAndRecords.Schema, result); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateTopCode generates Go code for the top command
func generateTopCode(n int, field string, asc bool) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	var inputVar string
	var prevSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	} else {
		inputVar = "records"
	}

	outputVar := uniqueVarName("topRecords", fragments)
	params := []lib.CodeParam{
		{Name: "top", Default: fmt.Sprintf("%d", n), Help: "number of top records", VarName: "flagTop", Type: "int"},
	}

	// Phase B fall-through: prevSchema==nil → Record-mode upstream.
	if typedMode() && prevSchema != nil {
		f, ok := lookupSchemaField(prevSchema, field)
		if !ok {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'top' references unknown field %q", field))
		}
		if !isSortableGoType(f.GoType) {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'top' on field %q (type %s) not supported (need int/float/string)", field, f.GoType))
		}
		// `top` is a bounded heap select (O(N·log K), O(K) memory), NOT a
		// full sort + limit. It is an associative reduction, so it has a
		// parallel form: typed.TopByParallel keeps a per-shard heap over a
		// Stream[T] and merges the survivors. Emit BOTH templates — the
		// planner keeps the parallel form when the upstream is a Stream and
		// swaps to the serial iter.Seq form (typed.TopBy) otherwise. Either
		// way the output is an iter.Seq[T] of the ≤ K winners. Default order
		// is descending (largest first); -asc selects the smallest instead.
		serialFn, parallelFn := "typed.TopBy", "typed.TopByParallel"
		if asc {
			serialFn, parallelFn = "typed.BottomBy", "typed.BottomByParallel"
		}
		keyFn := fmt.Sprintf("func(r %s) %s { return r.%s }", prevSchema.TypeName, f.GoType, f.GoName)
		parallelCode := fmt.Sprintf("%s := %s(%s, *flagTop, %s)", outputVar, parallelFn, inputVar, keyFn)
		serialCode := fmt.Sprintf("%s := %s(*flagTop, %s)(%s)", outputVar, serialFn, keyFn, inputVar)
		imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}

		frag := lib.NewStmtFragment(outputVar, inputVar, parallelCode, imports, getCommandString())
		frag.Params = params
		frag.InputTypedSchema = prevSchema
		frag.OutputTypedSchema = prevSchema
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeSeqTyped}
		frag.AltCodeIfSeq = serialCode
		frag.AltImportsIfSeq = imports
		frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
		return lib.WriteCodeFragment(frag)
	}

	// Rank by the field's natural type via ssql.CompareAny (numeric when
	// both values are numbers, lexicographic otherwise) — the same
	// comparator the CLI execution path and the typed codegen use. The old
	// float64 key coerced every string to 0.0, so `top -field <string>`
	// generated code returned arbitrary rows.
	funcName := "ssql.TopByFunc"
	if asc {
		funcName = "ssql.BottomByFunc"
	}
	code := fmt.Sprintf(`%s := %s(*flagTop, func(a, b ssql.Record) int {
		av, _ := ssql.Get[any](a, %q)
		bv, _ := ssql.Get[any](b, %q)
		return ssql.CompareAny(av, bv)
	})(%s)`, outputVar, funcName, field, field, inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
