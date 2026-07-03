package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterDistinct registers the distinct subcommand
func RegisterDistinct(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("distinct").
		Description("Remove duplicate records").
		Example("ssql from data.csv | ssql distinct", "Remove duplicate records").
		Example("ssql from users.csv | ssql include email | ssql distinct", "Get unique email addresses").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			var generate bool

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateDistinctCode()
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Dedup by ssql.RecordKey — a value-based canonical key. NOT
			// fmt.Sprintf("%v", r): that prints the internal schema POINTER,
			// so equal-valued records from different schema instances never
			// match (which is exactly what projections and multi-source
			// inputs produce).
			distinct := ssql.DistinctBy(ssql.RecordKey)(records)

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), schemaAndRecords.Schema, distinct); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateDistinctCode generates Go code for the distinct command
func generateDistinctCode() error {
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
	outputVar := uniqueVarName("distinct", fragments)

	// Phase B fall-through: prevSchema==nil → Record-mode upstream.
	if typedMode() && prevSchema != nil {
		// distinct is SerialOnly — planner inserts Stream.Serial()
		// upstream automatically when input is a Stream.
		// All Tier-1/1.5 supported field types are Go-comparable
		// (string/numeric/bool/time.Time/pointer), so we can dedup by
		// the row value itself. Pointer fields compare by identity,
		// not by pointed-to value — note in the doc, but uncommon
		// for typical CSV data.
		code := fmt.Sprintf("%s := typed.Distinct(func(r %s) %s { return r })(%s)",
			outputVar, prevSchema.TypeName, prevSchema.TypeName, inputVar)
		frag := lib.NewStmtFragment(outputVar, inputVar, code, []string{"github.com/rosscartlidge/ssql/v4/typed"}, getCommandString())
		frag.InputTypedSchema = prevSchema
		frag.OutputTypedSchema = prevSchema
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped, SerialOnly: true}
		return lib.WriteCodeFragment(frag)
	}

	code := fmt.Sprintf("%s := ssql.DistinctBy(ssql.RecordKey)(%s)", outputVar, inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
