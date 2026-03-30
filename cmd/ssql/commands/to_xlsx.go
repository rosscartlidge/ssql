package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToXLSX registers the "to xlsx" subcommand
func registerToXLSX(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("xlsx").
		Description("Write as Excel XLSX file").
		Example("ssql from data.csv | ssql to xlsx output.xlsx", "Convert CSV to Excel").
		Example("ssql from data.json | ssql to xlsx -sheet Sales output.xlsx", "Convert JSON to Excel with custom sheet name").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-sheet").
			String().
			Global().
			Default("").
			Help("Sheet name (default: Sheet1)").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.xlsx"}).
			Global().
			Required().
			Help("Output XLSX file (required)").
			Done().

		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var sheet string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if sheetVal, ok := ctx.GlobalFlags["-sheet"]; ok {
				sheet = sheetVal.(string)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if outputFile == "" {
				return fmt.Errorf("output file required")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToXLSXCode(outputFile, sheet)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Build XLSX config
			var xlsxConfig []ssql.XLSXConfig
			if sheet != "" {
				xlsxConfig = append(xlsxConfig, ssql.XLSXConfig{SheetName: sheet})
			}

			// Write as XLSX
			if err := ssql.WriteXLSX(records, outputFile, xlsxConfig...); err != nil {
				return fmt.Errorf("writing XLSX file: %w", err)
			}

			return nil
		}).
		Done()
}

func generateToXLSXCode(filename string, sheet string) error {
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

	params := []lib.CodeParam{
		{Name: "output", Default: filename, Help: "output XLSX file", VarName: "flagOutput"},
	}
	var code string
	if sheet != "" {
		code = fmt.Sprintf(`if err := ssql.WriteXLSX(%s, *flagOutput, ssql.XLSXConfig{SheetName: %q}); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing XLSX: %%v\n", err)
		os.Exit(1)
	}`, inputVar, sheet)
	} else {
		code = fmt.Sprintf(`if err := ssql.WriteXLSX(%s, *flagOutput); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing XLSX: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
	}

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt", "os"}, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
