package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
)

// RegisterGenerateGo registers the generate-go subcommand
func RegisterGenerateGo(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("generate-go").
		Description("Generate Go code from StreamV3 CLI pipeline").
		Example("ssql read-csv -g data.csv | ssql where -g -match age gt 18 | ssql generate-go", "Generate Go code from pipeline").
		Example("(export SSQLGO=1 && ssql read-csv data.csv | ssql limit 10 | ssql generate-go) > prog.go", "Generate using environment variable").
		Flag("OUTPUT").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.go"}).
			Global().
			Default("").
			Help("Output Go file (or stdout if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string

			if outVal, ok := ctx.GlobalFlags["OUTPUT"]; ok {
				outputFile = outVal.(string)
			}

			// Assemble code fragments from stdin
			code, err := lib.AssembleCodeFragments(os.Stdin)
			if err != nil {
				return fmt.Errorf("assembling code fragments: %w", err)
			}

			// Write to output
			if outputFile != "" {
				if err := os.WriteFile(outputFile, []byte(code), 0644); err != nil {
					return fmt.Errorf("writing output file: %w", err)
				}
				fmt.Fprintf(os.Stderr, "Generated Go code written to %s\n", outputFile)
			} else {
				fmt.Print(code)
			}

			return nil
		}).
		Done()
	return cmd
}
