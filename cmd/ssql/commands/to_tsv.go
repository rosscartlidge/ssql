package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToTSV registers the "to tsv" subcommand
func registerToTSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("tsv").
		Description("Write as TSV file (simpler and faster than CSV)").
		Example("ssql from data.json | ssql to tsv output.tsv", "Convert JSON to TSV").
		Example("ssql from data.csv | ssql to tsv -separator '|' output.psv", "Use pipe separator").
		Example("ssql from data.csv | ssql to tsv", "Write TSV to stdout").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-separator", "-s").
			String().
			Global().
			Default("\t").
			Completer(&cf.StaticCompleter{Options: []string{"\\t", "|", ";", ","}}). // Common separators
			Help("Field separator character (default: tab)").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.tsv"}).
			Global().
			Default("").
			Help("Output TSV file (or stdout if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			if schemaMode() {
				return runSchemaModeTransform(ctx, "to") // a sink passes the schema through
			}
			var outputFile string
			var separator string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if sepVal, ok := ctx.GlobalFlags["-separator"]; ok {
				separator = sepVal.(string)
			}
			if separator == "" || separator == "\\t" {
				separator = "\t"
			}
			sep := rune(separator[0])

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToTSVCode(outputFile, sep)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Write as TSV
			if outputFile == "" {
				return ssql.WriteTSVToWriterWithSeparator(records, ctx.Stdout(), sep)
			} else {
				return ssql.WriteTSVWithSeparator(records, outputFile, sep)
			}
		}).
		Done()
}

func generateToTSVCode(filename string, sep rune) error {
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
	// The writer's error is checked (DFC142 step 0: these were bare
	// calls, and a program whose output file could not be created exited
	// 0 with nothing written — in every mode, since to tsv has only this
	// record-shaped emission).
	imports = append(imports, "fmt", "os")
	if filename == "" {
		code = fmt.Sprintf(`if err := ssql.WriteTSVToWriterWithSeparator(%s, os.Stdout, %q); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar, sep)
	} else {
		params = append(params, lib.CodeParam{Name: "output", Default: filename, Help: "output TSV file", VarName: "flagOutput"})
		code = fmt.Sprintf(`if err := ssql.WriteTSVWithSeparator(%s, *flagOutput, %q); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar, sep)
	}

	frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
