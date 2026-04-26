package commands

import (
	"fmt"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterExclude registers the exclude subcommand
func RegisterExclude(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("exclude").
		Description("Exclude specified fields").
		Example("ssql from data.csv | ssql exclude id created_at updated_at", "Remove metadata fields").
		Example("ssql from api.json | ssql exclude password token secret_key", "Remove sensitive fields").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FIELDS").
			String().
			Variadic().
			FieldsFromFlag("").
			Global().
			Help("Fields to exclude").
			Done().

		Handler(func(ctx *cf.Context) error {
			var generate bool
			var fields []string

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if fieldsVal, ok := ctx.GlobalFlags["FIELDS"]; ok {
				switch v := fieldsVal.(type) {
				case []string:
					fields = v
				case []any:
					for _, item := range v {
						if s, ok := item.(string); ok {
							fields = append(fields, s)
						}
					}
				case string:
					fields = []string{v}
				}
			}

			if len(fields) == 0 {
				return fmt.Errorf("no fields specified")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateExcludeCode(fields)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Build exclusion function - delete excluded fields
			excluder := func(r ssql.Record) ssql.Record {
				mut := r.ToMutable()
				for _, field := range fields {
					mut = mut.Delete(field)
				}
				return mut.Freeze()
			}

			// Apply exclusion
			excludedRecords := ssql.Select(excluder)(records)

			// Update schema to remove excluded fields
			var outputSchema *lib.Schema
			if schemaAndRecords.Schema != nil {
				outputSchema = schemaAndRecords.Schema.Clone()
				for _, field := range fields {
					outputSchema.RemoveField(field)
				}
			}

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(os.Stdout, outputSchema, excludedRecords); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateExcludeCode generates Go code for the exclude command
func generateExcludeCode(fields []string) error {
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
	var prevSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	} else {
		inputVar = "records"
	}

	if typedMode() {
		if prevSchema == nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'exclude' has no typed input; %s does not yet support typed mode", lastNamedCommand(fragments)))
		}
		return emitTypedProjection("exclude", "Subset", inputVar, prevSchema, fields, true, nil)
	}

	// Generate delete statements
	var deleteStmts strings.Builder
	for _, field := range fields {
		deleteStmts.WriteString(fmt.Sprintf("\n\t\tmut = mut.Delete(%q)", field))
	}

	// Generate code
	outputVar := "excluded"
	code := fmt.Sprintf(`%s := ssql.Select(func(r ssql.Record) ssql.Record {
		mut := r.ToMutable()%s
		return mut.Freeze()
	})(%s)`, outputVar, deleteStmts.String(), inputVar)

	// Create stmt fragment
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
