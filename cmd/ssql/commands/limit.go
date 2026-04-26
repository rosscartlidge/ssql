package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterLimit registers the limit subcommand
func RegisterLimit(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("limit").
		Description("Take first N records (SQL LIMIT)").
		Example("ssql from data.csv | ssql limit 10", "Show first 10 records").
		Example("ssql from large.csv | ssql limit 100 | ssql to table", "Preview first 100 records").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("N").
			Int().
			Required().
			Global().
			Help("Number of records to take").
			Done().

		Handler(func(ctx *cf.Context) error {
			var n int
			var generate bool

			// Get flags from context
			if nVal, ok := ctx.GlobalFlags["N"]; ok {
				n = nVal.(int)
			} else {
				return fmt.Errorf("N argument is required")
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if n <= 0 {
				return fmt.Errorf("limit must be positive, got %d", n)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateLimitCode(n)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Apply limit
			limited := ssql.Limit[ssql.Record](n)(records)

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(os.Stdout, schemaAndRecords.Schema, limited); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateLimitCode generates Go code for the limit command
func generateLimitCode(n int) error {
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
	outputVar := "limited"
	params := []lib.CodeParam{
		{Name: "limit", Default: fmt.Sprintf("%d", n), Help: "maximum number of records", VarName: "flagLimit", Type: "int"},
	}

	if typedMode() {
		if prevSchema == nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'limit' has no typed input; %s does not yet support typed mode", lastNamedCommand(fragments)))
		}
		code := fmt.Sprintf("%s := typed.Limit[%s](*flagLimit)(%s)", outputVar, prevSchema.TypeName, inputVar)
		frag := lib.NewStmtFragment(outputVar, inputVar, code, []string{"github.com/rosscartlidge/ssql/v4/typed"}, getCommandString())
		frag.Params = params
		frag.InputTypedSchema = prevSchema
		frag.OutputTypedSchema = prevSchema
		return lib.WriteCodeFragment(frag)
	}

	code := fmt.Sprintf("%s := ssql.Limit[ssql.Record](*flagLimit)(%s)", outputVar, inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
