package commands

import (
	"fmt"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterInclude registers the include subcommand
func RegisterInclude(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("include").
		Description("Include only specified fields").
		Example("ssql from data.csv | ssql include name age", "Select only name and age columns").
		Example("ssql from users.json | ssql include email status | ssql to csv out.csv", "Extract email and status to CSV").
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
		Help("Fields to include").
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
				return generateIncludeCode(fields)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Build included fields map
			includedMap := make(map[string]bool)
			for _, field := range fields {
				includedMap[field] = true
			}

			// Build inclusion function - delete fields not in the included list
			includer := func(r ssql.Record) ssql.Record {
				mut := r.ToMutable()
				for k := range r.All() {
					if !includedMap[k] {
						mut = mut.Delete(k)
					}
				}
				return mut.Freeze()
			}

			// Apply inclusion
			included := ssql.Select(includer)(records)

			// Update schema to only include specified fields (in specified order)
			var outputSchema *lib.Schema
			if schemaAndRecords.Schema != nil {
				outputSchema = lib.NewSchema()
				for _, field := range fields {
					if schemaAndRecords.Schema.HasField(field) {
						outputSchema.AddField(field, schemaAndRecords.Schema.TypeOf(field))
					}
				}
			}

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(os.Stdout, outputSchema, included); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateIncludeCode generates Go code for the include command
func generateIncludeCode(fields []string) error {
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

	// Generate field list
	// Build included fields map
	var includedMap strings.Builder
	includedMap.WriteString("map[string]bool{")
	for i, field := range fields {
		if i > 0 {
			includedMap.WriteString(", ")
		}
		includedMap.WriteString(fmt.Sprintf("%q: true", field))
	}
	includedMap.WriteString("}")

	// Generate code
	outputVar := "included"
	code := fmt.Sprintf(`%s := ssql.Select(func(r ssql.Record) ssql.Record {
		includedMap := %s
		mut := r.ToMutable()
		for k := range r.All() {
			if !includedMap[k] {
				mut = mut.Delete(k)
			}
		}
		return mut.Freeze()
	})(%s)`, outputVar, includedMap.String(), inputVar)

	// Create stmt fragment
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
