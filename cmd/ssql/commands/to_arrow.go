package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToArrow registers the "to arrow" subcommand
func registerToArrow(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("arrow").
		Description("Write as Apache Arrow IPC file (10-20x faster I/O)").
		Example("ssql from data.csv | ssql to arrow output.arrow", "Convert CSV to Arrow for faster subsequent reads").
		Example("ssql from large.json | ssql to arrow data.arrow", "Convert JSON to Arrow format").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.arrow"}).
			Global().
			Required().
			Help("Output Arrow file (required)").
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

			if outputFile == "" {
				return fmt.Errorf("output file required")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToArrowCode(outputFile)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Write as Arrow
			if err := ssql.WriteArrow(records, outputFile); err != nil {
				return fmt.Errorf("writing Arrow file: %w", err)
			}

			return nil
		}).
		Done()
}

func generateToArrowCode(filename string) error {
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
		{Name: "output", Default: filename, Help: "output Arrow file", VarName: "flagOutput"},
	}
	code := fmt.Sprintf(`if err := ssql.WriteArrow(%s, *flagOutput); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing Arrow: %%v\n", err)
		os.Exit(1)
	}`, inputVar)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt", "os"}, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
