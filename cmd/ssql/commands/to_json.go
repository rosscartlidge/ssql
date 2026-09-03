package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToJSON registers the "to json" subcommand (pretty-printed JSON array)
func registerToJSON(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("json").
		Description("Write as pretty-printed JSON array").
		Example("ssql from data.csv | ssql to json", "Convert CSV to pretty JSON").
		Example("ssql from data.csv | ssql to json output.json", "Write pretty JSON to file").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.json"}).
			Global().
			Default("").
			Help("Output JSON file (or stdout if not specified)").
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

			if shouldGenerate(generate) {
				return generateToJSONCode(outputFile, true)
			}

			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			var fieldOrder []string
			if schemaAndRecords.Schema != nil {
				fieldOrder = schemaAndRecords.Schema.Fields
			}

			if outputFile == "" {
				return lib.WriteJSONWithFieldOrder(ctx.Stdout(), records, true, fieldOrder)
			}
			output, err := lib.OpenOutput(outputFile)
			if err != nil {
				return err
			}
			defer output.Close()
			return lib.WriteJSONWithFieldOrder(output, records, true, fieldOrder)
		}).
		Done()
}

// registerToJSONL registers the "to jsonl" subcommand (one JSON object per line)
func registerToJSONL(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("jsonl").
		Description("Write as JSONL (one JSON object per line)").
		Example("ssql from data.csv | ssql to jsonl", "Convert CSV to JSONL").
		Example("ssql from data.csv | ssql to jsonl output.jsonl", "Write JSONL to file").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.jsonl"}).
			Global().
			Default("").
			Help("Output JSONL file (or stdout if not specified)").
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

			if shouldGenerate(generate) {
				return generateToJSONCode(outputFile, false)
			}

			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			var fieldOrder []string
			if schemaAndRecords.Schema != nil {
				fieldOrder = schemaAndRecords.Schema.Fields
			}

			if outputFile == "" {
				return lib.WriteJSONWithFieldOrder(ctx.Stdout(), records, false, fieldOrder)
			}
			output, err := lib.OpenOutput(outputFile)
			if err != nil {
				return err
			}
			defer output.Close()
			return lib.WriteJSONWithFieldOrder(output, records, false, fieldOrder)
		}).
		Done()
}

func generateToJSONCode(filename string, pretty bool) error {
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
	if filename == "" {
		// Write to stdout
		if pretty {
			code = fmt.Sprintf(`	// Collect and pretty-print records to stdout
	var recordMaps []map[string]interface{}
	for record := range %s {
		data := make(map[string]interface{})
		for k, v := range record.All() {
			data[k] = v
		}
		recordMaps = append(recordMaps, data)
	}
	jsonBytes, err := json.MarshalIndent(recordMaps, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %%v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(jsonBytes); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write([]byte("\n"))`, inputVar)
		} else {
			code = fmt.Sprintf(`	if err := ssql.WriteJSONFastToWriter(%s, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
		}
	} else {
		// Write to file
		if pretty {
			code = fmt.Sprintf(`	if err := ssql.WriteJSONPretty(%s, *flagOutput); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
		} else {
			code = fmt.Sprintf(`	if err := ssql.WriteJSON(%s, *flagOutput); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
		}
	}

	var params []lib.CodeParam
	if filename != "" {
		params = append(params, lib.CodeParam{Name: "output", Default: filename, Help: "output JSON file", VarName: "flagOutput"})
	}

	imports := []string{"fmt", "os"}
	if filename == "" && pretty {
		imports = append(imports, "encoding/json")
	}
	frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
