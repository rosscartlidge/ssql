package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
)

// RegisterWriteCSV registers the write-csv subcommand
func RegisterWriteCSV(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("write-csv").
		Description("Read JSONL stream and write as CSV file").
		Example("ssql read-json data.json | ssql write-csv output.csv", "Convert JSON to CSV").
		Example("ssql read-csv data.csv | ssql where -match status eq active | ssql write-csv active.csv", "Filter and save to CSV").
		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.csv"}).
			Global().
			Default("").
			Help("Output CSV file (or stdout if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateWriteCSVCode(outputFile)
			}

			// Read JSONL from stdin
			records := lib.ReadJSONL(os.Stdin)

			// Write as CSV
			if outputFile == "" {
				return ssql.WriteCSVToWriter(records, os.Stdout)
			} else {
				return ssql.WriteCSV(records, outputFile)
			}
		}).
		Done()
	return cmd
}

// generateWriteCSVCode generates Go code for the write-csv command
func generateWriteCSVCode(filename string) error {
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

	// Get input variable name from last fragment
	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	// Generate WriteCSV call
	var code string
	var imports []string
	if filename == "" {
		code = fmt.Sprintf(`ssql.WriteCSVToWriter(%s, os.Stdout)`, inputVar)
		imports = append(imports, "os")
	} else {
		code = fmt.Sprintf(`ssql.WriteCSV(%s, %q)`, inputVar, filename)
	}

	// Create final fragment (no output variable)
	frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())

	// Write to stdout
	return lib.WriteCodeFragment(frag)
}
