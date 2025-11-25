package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v2/cmd/ssql/lib"
)

// RegisterWriteJSON registers the write-json subcommand
func RegisterWriteJSON(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("write-json").
		Description("Write as JSONL (default) or pretty JSON array (-pretty)").
		Example("ssql read-csv data.csv | ssql write-json", "Convert CSV to JSONL").
		Example("ssql read-csv data.csv | ssql write-json -pretty > output.json", "Convert CSV to pretty JSON array").
		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
		Done().
		Flag("-pretty", "-p").
			Bool().
			Global().
			Help("Pretty-print as JSON array (default: JSONL)").
		Done().
		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.{json,jsonl}"}).
			Global().
			Default("").
			Help("Output JSON/JSONL file (or stdout if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var pretty bool
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if prettyVal, ok := ctx.GlobalFlags["-pretty"]; ok {
				pretty = prettyVal.(bool)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateWriteJSONCode(outputFile, pretty)
			}

			// Read JSONL from stdin
			records := lib.ReadJSONL(os.Stdin)

			// Write to stdout or file
			if outputFile == "" {
				return lib.WriteJSON(os.Stdout, records, pretty)
			} else {
				output, err := lib.OpenOutput(outputFile)
				if err != nil {
					return err
				}
				defer output.Close()
				return lib.WriteJSON(output, records, pretty)
			}
		}).
		Done()
	return cmd
}

// generateWriteJSONCode generates Go code for the write-json command
func generateWriteJSONCode(filename string, pretty bool) error {
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

	// Get input variable from last fragment
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
			code = fmt.Sprintf(`	if err := ssql.WriteJSONToWriter(%s, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
		}
	} else {
		// Write to file
		if pretty {
			code = fmt.Sprintf(`	if err := ssql.WriteJSONPretty(%s, %q); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}`, inputVar, filename)
		} else {
			code = fmt.Sprintf(`	if err := ssql.WriteJSON(%s, %q); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}`, inputVar, filename)
		}
	}

	imports := []string{"fmt", "os"}
	// Add encoding/json import if pretty printing to stdout
	if filename == "" && pretty {
		imports = append(imports, "encoding/json")
	}
	frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
