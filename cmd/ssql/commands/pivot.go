package commands

import (
	"fmt"
	"slices"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterPivot registers the pivot subcommand
func RegisterPivot(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	aggFuncs := []string{"count", "sum", "avg", "min", "max"}

	cmd.Subcommand("pivot").
		Description("Create a pivot table (cross-tabulation)").
		Example("ssql from sales.csv | ssql pivot -row dept -col quarter -val revenue", "Pivot revenue by department and quarter").
		Example("ssql from data.csv | ssql pivot -row region -col product -val units -func count", "Count units by region and product").

		Flag("-row").
			String().
			Global().
			Help("Row field (groups become rows)").
			FieldsFromFlag("").
			Done().

		Flag("-col").
			String().
			Global().
			Help("Column field (unique values become columns)").
			FieldsFromFlag("").
			Done().

		Flag("-val").
			String().
			Global().
			Help("Value field to aggregate").
			FieldsFromFlag("").
			Done().

		Flag("-func").
			String().
			Global().
			Help("Aggregation function: count, sum, avg, min, max (default: sum)").
			Completer(&cf.StaticCompleter{Options: aggFuncs}).
			Default("sum").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			var rowField, colField, valField, aggFunc string
			var generate bool

			if val, ok := ctx.GlobalFlags["-row"]; ok {
				rowField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-col"]; ok {
				colField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-val"]; ok {
				valField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-func"]; ok {
				aggFunc = val.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if rowField == "" || colField == "" || valField == "" {
				return fmt.Errorf("pivot requires -row, -col, and -val flags")
			}

			// Validate aggregation function
			validPivotFuncs := map[string]bool{"count": true, "sum": true, "avg": true, "min": true, "max": true}
			if !validPivotFuncs[aggFunc] {
				return fmt.Errorf("unknown pivot function %q (valid: count, sum, avg, min, max)", aggFunc)
			}

			if shouldGenerate(generate) {
				return generatePivotCode(rowField, colField, valField, aggFunc)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Validate field names against schema
			if err := validateFieldsSchema(schemaAndRecords.Schema, []string{rowField, colField, valField}, "pivot"); err != nil {
				return err
			}

			// Apply pivot
			pivoted := ssql.Pivot(rowField, colField, valField, aggFunc)(records)

			// Materialize to build output schema (columns are data-dependent)
			results := slices.Collect(pivoted)

			// Build output schema from first result record
			var outSchema *lib.Schema
			if len(results) > 0 {
				outSchema = lib.InferFromRecord(results[0])
			}

			// Write output as JSONL with schema
			resultIter := slices.Values(results)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outSchema, resultIter); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generatePivotCode generates Go code for the pivot command
func generatePivotCode(rowField, colField, valField, aggFunc string) error {
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
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	outputVar := "pivoted"
	code := fmt.Sprintf(`%s := ssql.Pivot(%q, %q, %q, %q)(%s)`,
		outputVar, rowField, colField, valField, aggFunc, inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
