package commands

import (
	"fmt"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
)

// RegisterReadJSON registers the read-json subcommand
func RegisterReadJSON(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("read-json").
		Description("Read JSON array or JSONL file (auto-detects format)").
		Example("ssql read-json data.jsonl | ssql table", "Read JSONL file and display as table").
		Example("ssql read-json array.json | ssql where -match status eq active", "Read JSON array and filter records").
		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.{json,jsonl}"}).
			Global().
			Required().
			Help("Input JSON/JSONL file").
		Done().
		CacheFieldsFrom("FILE").
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			} else {
				return fmt.Errorf("FILE is required")
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateReadJSONCode(inputFile)
			}

			// Open and read JSON file
			input, err := lib.OpenInput(inputFile)
			if err != nil {
				return err
			}
			defer input.Close()

			originalRecords := lib.ReadJSON(input)

			// Cache field names for completion in pipelines
			// We peek at the first record to extract fields, then reconstruct the iterator
			var firstRecord *ssql.Record
			var fieldNames []string
			records := func(yield func(ssql.Record) bool) {
				for r := range originalRecords {
					if firstRecord == nil {
						// Capture first record and extract field names
						firstRecord = &r
						for k := range r.All() {
							fieldNames = append(fieldNames, k)
						}
						// Set environment variable for completion
						os.Setenv("AUTOCLI_FIELDS", strings.Join(fieldNames, ","))
						if inputFile != "" {
							// Also set file-specific cache
							cleanName := strings.ReplaceAll(inputFile, ".", "_")
							cleanName = strings.ReplaceAll(cleanName, "/", "_")
							os.Setenv("AUTOCLI_FIELDS_"+cleanName, strings.Join(fieldNames, ","))
						}
					}
					if !yield(r) {
						return
					}
				}
			}

			// Write as JSONL to stdout
			if err := lib.WriteJSONL(os.Stdout, records); err != nil {
				return fmt.Errorf("writing JSONL: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateReadJSONCode generates Go code for the read-json command
func generateReadJSONCode(filename string) error {
	// No previous fragments for init command
	outputVar := "records"
	imports := []string{"fmt", "os"}

	code := fmt.Sprintf(`records, err := ssql.ReadJSONAuto(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading JSON: %%w", err))
		os.Exit(1)
	}`, filename)

	frag := lib.NewInitFragment(outputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
