package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
)

// RegisterDistinct registers the distinct subcommand
func RegisterDistinct(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("distinct").
		Description("Remove duplicate records").
		Example("ssql read-csv data.csv | ssql distinct", "Remove duplicate records").
		Example("ssql read-csv users.csv | ssql include email | ssql distinct", "Get unique email addresses").
		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.jsonl"}).
			Global().
			Default("").
			Help("Input JSONL file (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateDistinctCode()
			}

			// Read JSONL from stdin or file
			input, err := lib.OpenInput(inputFile)
			if err != nil {
				return err
			}
			defer input.Close()

			records := lib.ReadJSONL(input)

			// Apply distinct using DistinctBy with JSON serialization for comparison
			distinct := ssql.DistinctBy(func(r ssql.Record) string {
				// Use JSON representation as unique key
				// This is simpler than making Record comparable
				json := fmt.Sprintf("%v", r)
				return json
			})(records)

			// Write output as JSONL
			if err := lib.WriteJSONL(os.Stdout, distinct); err != nil {
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
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}
	outputVar := "distinct"
	code := fmt.Sprintf(`%s := ssql.DistinctBy(func(r ssql.Record) string {
		return fmt.Sprintf("%%v", r)
	})(%s)`, outputVar, inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, []string{"fmt"}, getCommandString())
	return lib.WriteCodeFragment(frag)
}
