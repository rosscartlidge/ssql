package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterOffset registers the offset subcommand
func RegisterOffset(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("offset").
		Description("Skip first N records (SQL OFFSET)").
		Example("ssql from data.csv | ssql offset 10", "Skip first 10 records").
		Example("ssql from data.csv | ssql offset 100 | ssql limit 10", "Get records 101-110 (pagination)").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("N").
			Int().
			Required().
			Global().
			Help("Number of records to skip").
			Done().

		Handler(func(ctx *cf.Context) error {
			var n int
			var generate bool

			if nVal, ok := ctx.GlobalFlags["N"]; ok {
				n = nVal.(int)
			} else {
				return fmt.Errorf("N argument is required")
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if n < 0 {
				return fmt.Errorf("offset must be non-negative, got %d", n)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateOffsetCode(n)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Apply offset
			offsetted := ssql.Offset[ssql.Record](n)(records)

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), schemaAndRecords.Schema, offsetted); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateOffsetCode generates Go code for the offset command
func generateOffsetCode(n int) error {
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
	outputVar := "skipped"
	params := []lib.CodeParam{
		{Name: "offset", Default: fmt.Sprintf("%d", n), Help: "number of records to skip", VarName: "flagOffset", Type: "int"},
	}

	// Phase B fall-through: prevSchema==nil → Record-mode upstream.
	if typedMode() && prevSchema != nil {
		// offset is SerialOnly — planner inserts Stream.Serial()
		// upstream automatically when input is a Stream.
		code := fmt.Sprintf("%s := typed.Skip[%s](*flagOffset)(%s)", outputVar, prevSchema.TypeName, inputVar)
		frag := lib.NewStmtFragment(outputVar, inputVar, code, []string{"github.com/rosscartlidge/ssql/v4/typed"}, getCommandString())
		frag.Params = params
		frag.InputTypedSchema = prevSchema
		frag.OutputTypedSchema = prevSchema
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped, SerialOnly: true}
		return lib.WriteCodeFragment(frag)
	}

	code := fmt.Sprintf("%s := ssql.Offset[ssql.Record](*flagOffset)(%s)", outputVar, inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
