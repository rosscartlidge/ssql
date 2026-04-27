package commands

import (
	"fmt"
	"os"
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
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			keyFn := func(r ssql.Record) float64 {
				val, _ := ssql.Get[any](r, field)
				return extractNumeric(val)
			}

			var result = records
			if asc {
				result = ssql.BottomBy(n, keyFn)(records)
			} else {
				result = ssql.TopBy(n, keyFn)(records)
			}

			if err := lib.WriteJSONLWithSchema(os.Stdout, schemaAndRecords.Schema, result); err != nil {
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

	outputVar := "topRecords"
	params := []lib.CodeParam{
		{Name: "top", Default: fmt.Sprintf("%d", n), Help: "number of top records", VarName: "flagTop", Type: "int"},
	}

	if typedMode() {
		if err := rejectParallelMode("top"); err != nil {
			return err
		}
		if prevSchema == nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'top' has no typed input; %s does not yet support typed mode", lastNamedCommand(fragments)))
		}
		f, ok := lookupSchemaField(prevSchema, field)
		if !ok {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'top' references unknown field %q", field))
		}
		if !isSortableGoType(f.GoType) {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'top' on field %q (type %s) not supported (need int/float/string)", field, f.GoType))
		}
		// `top` is desugared as sort + limit. Default order is descending
		// (largest first); -asc reverses to smallest first.
		sortFn := "typed.SortByDesc"
		if asc {
			sortFn = "typed.SortBy"
		}
		code := fmt.Sprintf(`%s := typed.Limit[%s](*flagTop)(
		%s(func(r %s) %s { return r.%s })(%s),
	)`, outputVar, prevSchema.TypeName, sortFn, prevSchema.TypeName, f.GoType, f.GoName, inputVar)
		frag := lib.NewStmtFragment(outputVar, inputVar, code, []string{"github.com/rosscartlidge/ssql/v4/typed"}, getCommandString())
		frag.Params = params
		frag.InputTypedSchema = prevSchema
		frag.OutputTypedSchema = prevSchema
		return lib.WriteCodeFragment(frag)
	}

	funcName := "ssql.TopBy"
	if asc {
		funcName = "ssql.BottomBy"
	}
	code := fmt.Sprintf(`%s := %s(*flagTop, func(r ssql.Record) float64 {
		return ssql.GetOr(r, %q, 0.0)
	})(%s)`, outputVar, funcName, field, inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
