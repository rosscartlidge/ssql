package commands

import (
	"fmt"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterSort registers the sort subcommand
func RegisterSort(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("sort").
		Description("Sort records by one or more fields").
		Example("ssql from data.csv | ssql sort name", "Sort by name ascending").
		Example("ssql from data.csv | ssql sort dept age", "Sort by dept then age").
		Example("ssql from sales.csv | ssql sort amount -desc", "Sort by amount descending").
		Flag("FIELDS").
		String().
		Variadic().
		Required().
		FieldsFromFlag("").
		Global().
		Help("Fields to sort by").
		Done().
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-desc", "-d").
		Bool().
		Global().
		Help("Sort descending").
		Done().
		Handler(func(ctx *cf.Context) error {
			var fields []string
			var desc bool
			var generate bool

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

			if descVal, ok := ctx.GlobalFlags["-desc"]; ok {
				desc = descVal.(bool)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if len(fields) == 0 {
				return fmt.Errorf("no sort fields specified")
			}

			// Build OrderField slice
			orderBy := make([]ssql.OrderField, len(fields))
			for i, f := range fields {
				orderBy[i] = ssql.OrderField{Field: f, Desc: desc}
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateSortCode(orderBy)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Validate sort fields against schema
			if err := validateFieldsSchema(schemaAndRecords.Schema, fields, "sort"); err != nil {
				return err
			}

			// Sort using SortRecords (proper cross-type comparison)
			result := ssql.SortRecords(orderBy)(records)

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(os.Stdout, schemaAndRecords.Schema, result); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateSortCode generates Go code for the sort command
func generateSortCode(orderBy []ssql.OrderField) error {
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
	outputVar := "sorted"

	// Build OrderField slice literal
	var fields []string
	for _, of := range orderBy {
		fields = append(fields, fmt.Sprintf(`{Field: %q, Desc: %v}`, of.Field, of.Desc))
	}
	code := fmt.Sprintf("%s := ssql.SortRecords([]ssql.OrderField{%s})(%s)",
		outputVar, strings.Join(fields, ", "), inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
