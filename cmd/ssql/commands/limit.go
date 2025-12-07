package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
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

			// Read JSONL from stdin
			records := lib.ReadJSONL(os.Stdin)

			// Apply limit
			limited := ssql.Limit[ssql.Record](n)(records)

			// Write output as JSONL
			if err := lib.WriteJSONL(os.Stdout, limited); err != nil {
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
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}
	outputVar := "limited"
	code := fmt.Sprintf("%s := ssql.Limit[ssql.Record](%d)(%s)", outputVar, n, inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
