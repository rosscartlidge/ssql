package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterGroupBy registers the group-by subcommand
func RegisterGroupBy(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("group-by").
		Description("Group records by fields and apply aggregations").
		Example("ssql from sales.csv | ssql group-by region -count total", "Count records by region").
		Example("ssql from sales.csv | ssql group-by region -sum amount total_sales", "Sum sales amount by region").
		Example("ssql from data.csv | ssql group-by dept -count num_employees -avg salary avg_salary -sum hours total_hours", "Multiple aggregations in one command").
		Example("ssql from data.csv | ssql group-by dept -collect name all_names", "Collect all names into array per department").
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
			Help("Fields to group by").
		Done().
		Flag("-count").
			Arg("result-name").Completer(cf.NoCompleter{Hint: "<name>"}).Done().
			Accumulate().
			Global().
			Help("Count records (result field name)").
		Done().
		Flag("-sum").
			Arg("field").FieldsFromFlag("").Done().
			Arg("result-name").Completer(cf.NoCompleter{Hint: "<name>"}).Done().
			Accumulate().
			Global().
			Help("Sum field values (field name, result name)").
		Done().
		Flag("-avg").
			Arg("field").FieldsFromFlag("").Done().
			Arg("result-name").Completer(cf.NoCompleter{Hint: "<name>"}).Done().
			Accumulate().
			Global().
			Help("Average field values (field name, result name)").
		Done().
		Flag("-min").
			Arg("field").FieldsFromFlag("").Done().
			Arg("result-name").Completer(cf.NoCompleter{Hint: "<name>"}).Done().
			Accumulate().
			Global().
			Help("Minimum field value (field name, result name)").
		Done().
		Flag("-max").
			Arg("field").FieldsFromFlag("").Done().
			Arg("result-name").Completer(cf.NoCompleter{Hint: "<name>"}).Done().
			Accumulate().
			Global().
			Help("Maximum field value (field name, result name)").
		Done().
		Flag("-collect").
			Arg("field").FieldsFromFlag("").Done().
			Arg("result-name").Completer(cf.NoCompleter{Hint: "<name>"}).Done().
			Accumulate().
			Global().
			Help("Collect all field values into array (field name, result name)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var groupByFields []string
			var generate bool

			// Extract group-by fields from variadic positional
			if fieldsVal, ok := ctx.GlobalFlags["FIELDS"]; ok {
				switch v := fieldsVal.(type) {
				case []string:
					groupByFields = v
				case []any:
					for _, item := range v {
						if s, ok := item.(string); ok {
							groupByFields = append(groupByFields, s)
						}
					}
				case string:
					groupByFields = []string{v}
				}
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if len(groupByFields) == 0 {
				return fmt.Errorf("no group-by fields specified")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateGroupByCode(ctx, groupByFields)
			}

			// Parse aggregation specifications from new flag format
			type aggSpec struct {
				function string
				field    string
				result   string
			}

			var aggSpecs []aggSpec

			// Parse -count flags (only result name)
			if countVals, ok := ctx.GlobalFlags["-count"]; ok {
				counts, _ := countVals.([]any)
				for _, countVal := range counts {
					// When there's only 1 Arg(), autocli doesn't wrap in a slice
					if resultName, ok := countVal.(string); ok {
						aggSpecs = append(aggSpecs, aggSpec{
							function: "count",
							field:    "",
							result:   resultName,
						})
					}
				}
			}

			// Parse -sum flags (field and result name)
			if sumVals, ok := ctx.GlobalFlags["-sum"]; ok {
				sums, _ := sumVals.([]any)
				for _, sumVal := range sums {
					// When there are 2+ Args(), autocli returns a map with arg names as keys
					if argsMap, ok := sumVal.(map[string]any); ok {
						field, _ := argsMap["field"].(string)
						result, _ := argsMap["result-name"].(string)
						if field != "" && result != "" {
							aggSpecs = append(aggSpecs, aggSpec{
								function: "sum",
								field:    field,
								result:   result,
							})
						}
					}
				}
			}

			// Parse -avg flags (field and result name)
			if avgVals, ok := ctx.GlobalFlags["-avg"]; ok {
				avgs, _ := avgVals.([]any)
				for _, avgVal := range avgs {
					if argsMap, ok := avgVal.(map[string]any); ok {
						field, _ := argsMap["field"].(string)
						result, _ := argsMap["result-name"].(string)
						if field != "" && result != "" {
							aggSpecs = append(aggSpecs, aggSpec{
								function: "avg",
								field:    field,
								result:   result,
							})
						}
					}
				}
			}

			// Parse -min flags (field and result name)
			if minVals, ok := ctx.GlobalFlags["-min"]; ok {
				mins, _ := minVals.([]any)
				for _, minVal := range mins {
					if argsMap, ok := minVal.(map[string]any); ok {
						field, _ := argsMap["field"].(string)
						result, _ := argsMap["result-name"].(string)
						if field != "" && result != "" {
							aggSpecs = append(aggSpecs, aggSpec{
								function: "min",
								field:    field,
								result:   result,
							})
						}
					}
				}
			}

			// Parse -max flags (field and result name)
			if maxVals, ok := ctx.GlobalFlags["-max"]; ok {
				maxs, _ := maxVals.([]any)
				for _, maxVal := range maxs {
					if argsMap, ok := maxVal.(map[string]any); ok {
						field, _ := argsMap["field"].(string)
						result, _ := argsMap["result-name"].(string)
						if field != "" && result != "" {
							aggSpecs = append(aggSpecs, aggSpec{
								function: "max",
								field:    field,
								result:   result,
							})
						}
					}
				}
			}

			// Parse -collect flags (field and result name)
			if collectVals, ok := ctx.GlobalFlags["-collect"]; ok {
				collects, _ := collectVals.([]any)
				for _, collectVal := range collects {
					if argsMap, ok := collectVal.(map[string]any); ok {
						field, _ := argsMap["field"].(string)
						result, _ := argsMap["result-name"].(string)
						if field != "" && result != "" {
							aggSpecs = append(aggSpecs, aggSpec{
								function: "collect",
								field:    field,
								result:   result,
							})
						}
					}
				}
			}

			if len(aggSpecs) == 0 {
				return fmt.Errorf("no aggregations specified (use -count, -sum, -avg, -min, -max, or -collect)")
			}

			// Read JSONL from stdin
			records := lib.ReadJSONL(os.Stdin)

			// Apply GroupByFields
			grouped := ssql.GroupByFields("_group", groupByFields...)(records)

			// Build aggregations map
			aggregations := make(map[string]ssql.AggregateFunc)
			for _, spec := range aggSpecs {
				agg, err := buildAggregator(spec.function, spec.field)
				if err != nil {
					return err
				}
				aggregations[spec.result] = agg
			}

			// Apply Aggregate
			aggregated := ssql.Aggregate("_group", aggregations)(grouped)

			// Write output as JSONL
			if err := lib.WriteJSONL(os.Stdout, aggregated); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateGroupByCode generates Go code for the group-by command
func generateGroupByCode(ctx *cf.Context, groupByFields []string) error {
	// Read all previous code fragments from stdin (if any)
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

	// Validate group-by fields
	if len(groupByFields) == 0 {
		return fmt.Errorf("no group-by field specified (use -by)")
	}

	// Parse aggregation specifications from new flag format
	type aggSpec struct {
		function string
		field    string
		result   string
	}

	var aggSpecs []aggSpec

	// Parse -count flags (only result name)
	if countVals, ok := ctx.GlobalFlags["-count"]; ok {
		counts, _ := countVals.([]any)
		for _, countVal := range counts {
			// When there's only 1 Arg(), autocli doesn't wrap in a slice
			if resultName, ok := countVal.(string); ok {
				aggSpecs = append(aggSpecs, aggSpec{
					function: "count",
					field:    "",
					result:   resultName,
				})
			}
		}
	}

	// Parse -sum flags (field and result name)
	if sumVals, ok := ctx.GlobalFlags["-sum"]; ok {
		sums, _ := sumVals.([]any)
		for _, sumVal := range sums {
			if argsMap, ok := sumVal.(map[string]any); ok {
				field, _ := argsMap["field"].(string)
				result, _ := argsMap["result-name"].(string)
				if field != "" && result != "" {
					aggSpecs = append(aggSpecs, aggSpec{
						function: "sum",
						field:    field,
						result:   result,
					})
				}
			}
		}
	}

	// Parse -avg flags (field and result name)
	if avgVals, ok := ctx.GlobalFlags["-avg"]; ok {
		avgs, _ := avgVals.([]any)
		for _, avgVal := range avgs {
			if argsMap, ok := avgVal.(map[string]any); ok {
				field, _ := argsMap["field"].(string)
				result, _ := argsMap["result-name"].(string)
				if field != "" && result != "" {
					aggSpecs = append(aggSpecs, aggSpec{
						function: "avg",
						field:    field,
						result:   result,
					})
				}
			}
		}
	}

	// Parse -min flags (field and result name)
	if minVals, ok := ctx.GlobalFlags["-min"]; ok {
		mins, _ := minVals.([]any)
		for _, minVal := range mins {
			if argsMap, ok := minVal.(map[string]any); ok {
				field, _ := argsMap["field"].(string)
				result, _ := argsMap["result-name"].(string)
				if field != "" && result != "" {
					aggSpecs = append(aggSpecs, aggSpec{
						function: "min",
						field:    field,
						result:   result,
					})
				}
			}
		}
	}

	// Parse -max flags (field and result name)
	if maxVals, ok := ctx.GlobalFlags["-max"]; ok {
		maxs, _ := maxVals.([]any)
		for _, maxVal := range maxs {
			if argsMap, ok := maxVal.(map[string]any); ok {
				field, _ := argsMap["field"].(string)
				result, _ := argsMap["result-name"].(string)
				if field != "" && result != "" {
					aggSpecs = append(aggSpecs, aggSpec{
						function: "max",
						field:    field,
						result:   result,
					})
				}
			}
		}
	}

	// Parse -collect flags (field and result name)
	if collectVals, ok := ctx.GlobalFlags["-collect"]; ok {
		collects, _ := collectVals.([]any)
		for _, collectVal := range collects {
			if argsMap, ok := collectVal.(map[string]any); ok {
				field, _ := argsMap["field"].(string)
				result, _ := argsMap["result-name"].(string)
				if field != "" && result != "" {
					aggSpecs = append(aggSpecs, aggSpec{
						function: "collect",
						field:    field,
						result:   result,
					})
				}
			}
		}
	}

	if len(aggSpecs) == 0 {
		return fmt.Errorf("no aggregations specified (use -count, -sum, -avg, -min, -max, or -collect)")
	}

	// Generate TWO fragments: one for GroupByFields, one for Aggregate
	// This allows each to be extracted cleanly for Chain()

	// Fragment 1: GroupByFields
	groupCode := "grouped := ssql.GroupByFields(\"_group\""
	for _, field := range groupByFields {
		groupCode += fmt.Sprintf(", %q", field)
	}
	groupCode += fmt.Sprintf(")(%s)", inputVar)

	frag1 := lib.NewStmtFragment("grouped", inputVar, groupCode, nil, getCommandString())
	if err := lib.WriteCodeFragment(frag1); err != nil {
		return fmt.Errorf("writing GroupByFields fragment: %w", err)
	}

	// Fragment 2: Aggregate
	// Note: Empty command string since this is part of the same CLI command as Fragment 1
	aggCode := "aggregated := ssql.Aggregate(\"_group\", map[string]ssql.AggregateFunc{\n"
	for i, spec := range aggSpecs {
		if i > 0 {
			aggCode += ",\n"
		}
		aggCode += fmt.Sprintf("\t\t%q: %s", spec.result, generateAggregatorCode(spec))
	}
	aggCode += ",\n\t})(grouped)"

	frag2 := lib.NewStmtFragment("aggregated", "grouped", aggCode, nil, "")
	return lib.WriteCodeFragment(frag2)
}

// generateAggregatorCode generates code for a single aggregator
func generateAggregatorCode(spec struct {
	function string
	field    string
	result   string
}) string {
	switch spec.function {
	case "count":
		return "ssql.Count()"
	case "sum":
		return fmt.Sprintf("ssql.Sum(%q)", spec.field)
	case "avg":
		return fmt.Sprintf("ssql.Avg(%q)", spec.field)
	case "min":
		return fmt.Sprintf("ssql.Min[float64](%q)", spec.field)
	case "max":
		return fmt.Sprintf("ssql.Max[float64](%q)", spec.field)
	case "collect":
		return fmt.Sprintf("ssql.Collect(%q)", spec.field)
	default:
		return ""
	}
}
