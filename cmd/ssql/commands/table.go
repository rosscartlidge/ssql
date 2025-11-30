package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
)

// RegisterTable registers the table subcommand
func RegisterTable(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("table").
		Description("Display records as a formatted table").
		Example("ssql read-csv data.csv | ssql table", "Display CSV as formatted table").
		Example("ssql read-csv data.csv | ssql where -match age gt 21 | ssql table -max-width 30", "Filter and display with custom column width").
		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
		Done().
		Flag("-max-width").
			Int().
			Global().
			Default(50).
			Help("Maximum column width (truncate longer values)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var generate bool
			var maxWidth int

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if widthVal, ok := ctx.GlobalFlags["-max-width"]; ok {
				maxWidth = widthVal.(int)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateTableCode(maxWidth)
			}

			// Read all records from stdin and display as table
			records := lib.ReadJSONL(os.Stdin)
			ssql.DisplayTable(records, maxWidth)
			return nil
		}).
		Done()
	return cmd
}

// generateTableCode generates Go code for the table command
func generateTableCode(maxWidth int) error {
	// Read all previous code fragments from stdin
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
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	// Generate simple call to DisplayTable
	code := fmt.Sprintf("\tssql.DisplayTable(%s, %d)\n", inputVar, maxWidth)

	// Create final fragment (table is a sink - no output variable)
	frag := lib.NewFinalFragment(inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
