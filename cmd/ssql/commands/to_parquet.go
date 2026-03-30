package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToParquet registers the "to parquet" subcommand
func registerToParquet(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("parquet").
		Description("Write as Parquet file (Snappy compression, DuckDB compatible)").
		Example("ssql from data.csv | ssql to parquet output.parquet", "Convert CSV to Parquet").
		Example("ssql from large.json | ssql to parquet data.parquet", "Convert JSON to Parquet").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.parquet"}).
			Global().
			Required().
			Help("Output Parquet file (required)").
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

			if shouldGenerate(generate) {
				return generateToParquetCode(outputFile)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			if err := ssql.WriteParquet(records, outputFile); err != nil {
				return fmt.Errorf("writing Parquet file: %w", err)
			}

			return nil
		}).
		Done()
}

func generateToParquetCode(filename string) error {
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
		{Name: "output", Default: filename, Help: "output Parquet file", VarName: "flagOutput"},
	}
	code := fmt.Sprintf(`if err := ssql.WriteParquet(%s, *flagOutput); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing Parquet: %%v\n", err)
		os.Exit(1)
	}`, inputVar)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt", "os"}, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
