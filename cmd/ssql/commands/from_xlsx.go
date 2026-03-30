package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func registerFromXLSX(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("xlsx").
		Description("Read Excel spreadsheet").
		Example("ssql from xlsx data.xlsx | ssql to table", "Read Excel spreadsheet").
		Example("ssql from xlsx workbook.xlsx -sheet Sales | ssql to csv", "Read specific sheet").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-sheet").
			String().
			Global().
			Default("").
			Help("Sheet name to read (default: first sheet)").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.xlsx"}).
			Global().
			Help("Input XLSX file (required — XLSX cannot be read from stdin)").
			Done().

		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool
			var sheet string

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if sheetVal, ok := ctx.GlobalFlags["-sheet"]; ok {
				sheet = sheetVal.(string)
			}

			if inputFile == "" {
				return fmt.Errorf("XLSX format cannot be read from stdin (it requires random file access); use a file path")
			}

			return executeFromXLSX(inputFile, sheet, generate)
		}).
		Done()
}

// executeFromXLSX handles XLSX reading for both the subcommand and bare form.
func executeFromXLSX(inputFile string, sheet string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromXLSXCode(inputFile, sheet)
	}

	var xlsxConfig []ssql.XLSXConfig
	if sheet != "" {
		xlsxConfig = append(xlsxConfig, ssql.XLSXConfig{SheetName: sheet})
	}

	records, err := ssql.ReadXLSX(inputFile, xlsxConfig...)
	if err != nil {
		return fmt.Errorf("reading XLSX file: %w", err)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// generateFromXLSXCode generates Go code for reading XLSX.
func generateFromXLSXCode(filename string, sheet string) error {
	var code string
	var params []lib.CodeParam

	params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input XLSX file", VarName: "flagInput"})
	if sheet != "" {
		code = fmt.Sprintf(`records, err := ssql.ReadXLSX(*flagInput, ssql.XLSXConfig{SheetName: %q})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading XLSX: %%w", err))
		os.Exit(1)
	}`, sheet)
	} else {
		code = `records, err := ssql.ReadXLSX(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading XLSX: %w", err))
		os.Exit(1)
	}`
	}

	imports := []string{"fmt", "os"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
