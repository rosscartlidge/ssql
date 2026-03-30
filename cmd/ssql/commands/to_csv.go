package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToCSV registers the "to csv" subcommand
func registerToCSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("csv").
		Description("Write as CSV file").
		Example("ssql from data.json | ssql to csv output.csv", "Convert JSON to CSV").
		Example("ssql from data.csv | ssql where -if status eq active | ssql to csv active.csv", "Filter and save to CSV").

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
				return generateToCSVCode(outputFile)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Build CSV config with field order from schema if present
			config := ssql.DefaultCSVConfig()
			if schemaAndRecords.Schema != nil {
				config.Fields = schemaAndRecords.Schema.Fields
			}

			// Write as CSV with field order
			if outputFile == "" {
				return ssql.WriteCSVToWriter(records, os.Stdout, config)
			} else {
				return ssql.WriteCSV(records, outputFile, config)
			}
		}).
		Done()
}

func generateToCSVCode(filename string) error {
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

	var code string
	var imports []string
	var params []lib.CodeParam
	if filename == "" {
		code = fmt.Sprintf(`ssql.WriteCSVToWriter(%s, os.Stdout)`, inputVar)
		imports = append(imports, "os")
	} else {
		params = append(params, lib.CodeParam{Name: "output", Default: filename, Help: "output CSV file", VarName: "flagOutput"})
		code = fmt.Sprintf(`ssql.WriteCSV(%s, *flagOutput)`, inputVar)
	}

	frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
