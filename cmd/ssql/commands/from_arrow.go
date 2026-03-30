package commands

import (
	"fmt"
	"iter"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func registerFromArrow(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("arrow").
		Description("Read Arrow file or stdin").
		Example("ssql from arrow data.arrow | ssql to table", "Read Arrow file (10-20x faster than CSV)").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.arrow"}).
			Global().
			Default("").
			Help("Input Arrow file (or stdin if not specified)").
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

			return executeFromArrow(inputFile, generate)
		}).
		Done()
}

// executeFromArrow handles Arrow reading for both the subcommand and bare form.
func executeFromArrow(inputFile string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromArrowCode(inputFile)
	}

	var records iter.Seq[ssql.Record]
	if inputFile == "" {
		records = ssql.ReadArrowFromReader(os.Stdin)
	} else {
		var err error
		records, err = ssql.ReadArrow(inputFile)
		if err != nil {
			return fmt.Errorf("reading Arrow file: %w", err)
		}
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// generateFromArrowCode generates Go code for reading Arrow.
func generateFromArrowCode(filename string) error {
	var code string
	var imports []string
	var params []lib.CodeParam

	if filename == "" {
		code = `records := ssql.ReadArrowFromReader(os.Stdin)`
		imports = []string{"os"}
	} else {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input Arrow file", VarName: "flagInput"})
		code = `records, err := ssql.ReadArrow(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading Arrow: %w", err))
		os.Exit(1)
	}`
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
