package commands

import (
	"fmt"
	"iter"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
)

// RegisterSort registers the sort subcommand
func RegisterSort(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("sort").
		Description("Sort records by field").
		Example("ssql from data.csv | ssql sort age", "Sort by age ascending").
		Example("ssql from sales.csv | ssql sort amount -desc", "Sort by amount descending").
		Flag("FIELD").
			String().
			Required().
			FieldsFromFlag("").
			Global().
			Help("Field to sort by").
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
			var field string
			var desc bool
			var generate bool

			if fieldVal, ok := ctx.GlobalFlags["FIELD"]; ok {
				field = fieldVal.(string)
			}

			if descVal, ok := ctx.GlobalFlags["-desc"]; ok {
				desc = descVal.(bool)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if field == "" {
				return fmt.Errorf("no sort field specified")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateSortCode(field, desc)
			}

			// Read JSONL from stdin
			records := lib.ReadJSONL(os.Stdin)

			// Build sort key extractor and apply sort
			var result iter.Seq[ssql.Record]
			if desc {
				// Descending: negate numeric values
				sorter := ssql.SortBy(func(r ssql.Record) float64 {
					val, _ := ssql.Get[any](r, field)
					return -extractNumeric(val)
				})
				result = sorter(records)
			} else {
				// Ascending
				sorter := ssql.SortBy(func(r ssql.Record) float64 {
					val, _ := ssql.Get[any](r, field)
					return extractNumeric(val)
				})
				result = sorter(records)
			}

			// Write output as JSONL
			if err := lib.WriteJSONL(os.Stdout, result); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateSortCode generates Go code for the sort command
func generateSortCode(field string, desc bool) error {
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
	var sortFunc string
	if desc {
		sortFunc = fmt.Sprintf(`ssql.SortBy(func(r ssql.Record) float64 {
		return -ssql.GetOr(r, %q, 0.0)
	})`, field)
	} else {
		sortFunc = fmt.Sprintf(`ssql.SortBy(func(r ssql.Record) float64 {
		return ssql.GetOr(r, %q, 0.0)
	})`, field)
	}
	code := fmt.Sprintf("%s := %s(%s)", outputVar, sortFunc, inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
