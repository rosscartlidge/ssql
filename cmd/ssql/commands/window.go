package commands

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterWindow registers the window subcommand
func RegisterWindow(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("window").
		Description("Apply SQL-style window functions without collapsing rows").
		Example("ssql from data.csv | ssql window -row-number rn -order salary -desc", "Number rows by salary descending").
		Example("ssql from sales.csv | ssql window -sum revenue running_total -partition dept -order date", "Running total per department").
		Example("ssql from data.csv | ssql window -lag price 1 prev_price -order date", "Previous row's value").
		ClauseDescription("Each clause defines a separate window (partition/order/frame)").

		// Global flag
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().

		// Local flags (clause-scoped)
		Flag("-partition").
		String().
		FieldsFromFlag("").
		Accumulate().
		Local().
		Help("Partition field (repeatable, like SQL PARTITION BY)").
		Done().

		Flag("-order").
		String().
		FieldsFromFlag("").
		Accumulate().
		Local().
		Help("Order field (repeatable, like SQL ORDER BY)").
		Done().

		Flag("-desc").
		Bool().
		Local().
		Help("Sort descending (applies to all -order fields in this clause)").
		Done().

		Flag("-rows").
		String().
		Local().
		Help("Frame spec: P,F where * means unbounded (default: *,0)").
		Done().

		// Ranking functions
		Flag("-row-number").
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("ROW_NUMBER() → result field").
		Done().

		Flag("-rank").
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("RANK() → result field").
		Done().

		Flag("-dense-rank").
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("DENSE_RANK() → result field").
		Done().

		Flag("-ntile").
		Arg("n").Completer(&cf.NoCompleter{Hint: "<n>"}).Done().
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("NTILE(n) → result field").
		Done().

		Flag("-percent-rank").
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("PERCENT_RANK() → result field").
		Done().

		// Offset functions
		Flag("-lag").
		Arg("field").FieldsFromFlag("").Done().
		Arg("n").Completer(&cf.NoCompleter{Hint: "<offset>"}).Done().
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("LAG(field, n) → result field").
		Done().

		Flag("-lead").
		Arg("field").FieldsFromFlag("").Done().
		Arg("n").Completer(&cf.NoCompleter{Hint: "<offset>"}).Done().
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("LEAD(field, n) → result field").
		Done().

		Flag("-first").
		Arg("field").FieldsFromFlag("").Done().
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("FIRST_VALUE(field) → result field").
		Done().

		Flag("-last").
		Arg("field").FieldsFromFlag("").Done().
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("LAST_VALUE(field) → result field").
		Done().

		// Aggregate window functions
		Flag("-sum").
		Arg("field").FieldsFromFlag("").Done().
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("Windowed SUM(field) → result field").
		Done().

		Flag("-avg").
		Arg("field").FieldsFromFlag("").Done().
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("Windowed AVG(field) → result field").
		Done().

		Flag("-count").
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("Windowed COUNT(*) → result field").
		Done().

		Flag("-min").
		Arg("field").FieldsFromFlag("").Done().
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("Windowed MIN(field) → result field").
		Done().

		Flag("-max").
		Arg("field").FieldsFromFlag("").Done().
		Arg("result").Completer(&cf.NoCompleter{Hint: "<result-field>"}).Done().
		Accumulate().
		Local().
		Help("Windowed MAX(field) → result field").
		Done().

		Handler(func(ctx *cf.Context) error {
			var generate bool
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Parse all clauses into WindowConfigs
			configs, err := parseWindowClauses(ctx.Clauses)
			if err != nil {
				return err
			}

			// Validate: need at least one window function spec
			totalSpecs := 0
			for _, cfg := range configs {
				totalSpecs += len(cfg.Specs)
			}
			if totalSpecs == 0 {
				return fmt.Errorf("window requires at least one window function (e.g., -row-number rn, -sum field result)")
			}

			if shouldGenerate(generate) {
				return generateWindowCode(configs)
			}

			// Read JSONL from stdin
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records
			inputSchema := schemaAndRecords.Schema

			// Apply window function
			windowed := ssql.Window(configs)(records)

			// Materialize to build output schema
			results := slices.Collect(windowed)

			// Build output schema
			var outSchema *lib.Schema
			if inputSchema != nil {
				outSchema = inputSchema.Clone()
			} else if len(results) > 0 {
				outSchema = lib.InferFromRecord(results[0])
			}

			// Add result fields to schema
			if outSchema != nil {
				for _, cfg := range configs {
					for _, spec := range cfg.Specs {
						if !outSchema.HasField(spec.ResultName) {
							outSchema.AddField(spec.ResultName, inferWindowResultType(spec.Function, inputSchema))
						}
					}
				}
			}

			// Write output
			resultIter := slices.Values(results)
			if err := lib.WriteJSONLWithSchema(os.Stdout, outSchema, resultIter); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// parseWindowClauses parses autocli clauses into WindowConfigs.
func parseWindowClauses(clauses []cf.Clause) ([]ssql.WindowConfig, error) {
	var configs []ssql.WindowConfig

	for _, clause := range clauses {
		cfg := ssql.WindowConfig{
			Frame: ssql.WindowFrame{Preceding: -1, Following: 0}, // default: UNBOUNDED PRECEDING to CURRENT ROW
		}

		// Parse -partition flags
		if raw, ok := clause.Flags["-partition"]; ok {
			if arr, ok := raw.([]any); ok {
				for _, v := range arr {
					if s, ok := v.(string); ok && s != "" {
						cfg.PartitionBy = append(cfg.PartitionBy, s)
					}
				}
			}
		}

		// Parse -order flags
		desc := false
		if descRaw, ok := clause.Flags["-desc"]; ok {
			if b, ok := descRaw.(bool); ok {
				desc = b
			}
		}
		if raw, ok := clause.Flags["-order"]; ok {
			if arr, ok := raw.([]any); ok {
				for _, v := range arr {
					if s, ok := v.(string); ok && s != "" {
						cfg.OrderBy = append(cfg.OrderBy, ssql.OrderField{Field: s, Desc: desc})
					}
				}
			}
		}

		// Parse -rows frame
		if raw, ok := clause.Flags["-rows"]; ok {
			if s, ok := raw.(string); ok && s != "" {
				frame, err := parseRowsFrame(s)
				if err != nil {
					return nil, fmt.Errorf("invalid -rows %q: %w", s, err)
				}
				cfg.Frame = frame
			}
		}

		// Parse window function specs
		var specs []ssql.WindowSpec

		// Single-arg functions: -row-number result, -rank result, -dense-rank result, -percent-rank result, -count result
		specs = append(specs, parseSingleArgSpecs(clause.Flags, "-row-number", func(result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WRowNumber(), ResultName: result}
		})...)
		specs = append(specs, parseSingleArgSpecs(clause.Flags, "-rank", func(result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WRank(), ResultName: result}
		})...)
		specs = append(specs, parseSingleArgSpecs(clause.Flags, "-dense-rank", func(result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WDenseRank(), ResultName: result}
		})...)
		specs = append(specs, parseSingleArgSpecs(clause.Flags, "-percent-rank", func(result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WPercentRank(), ResultName: result}
		})...)
		specs = append(specs, parseSingleArgSpecs(clause.Flags, "-count", func(result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WCount(), ResultName: result}
		})...)

		// Two-arg functions: -first field result, -last field result, -sum field result, -avg field result, -min field result, -max field result
		specs = append(specs, parseTwoArgSpecs(clause.Flags, "-first", "field", "result", func(field, result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WFirst(field), ResultName: result}
		})...)
		specs = append(specs, parseTwoArgSpecs(clause.Flags, "-last", "field", "result", func(field, result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WLast(field), ResultName: result}
		})...)
		specs = append(specs, parseTwoArgSpecs(clause.Flags, "-sum", "field", "result", func(field, result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WSum(field), ResultName: result}
		})...)
		specs = append(specs, parseTwoArgSpecs(clause.Flags, "-avg", "field", "result", func(field, result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WAvg(field), ResultName: result}
		})...)
		specs = append(specs, parseTwoArgSpecs(clause.Flags, "-min", "field", "result", func(field, result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WMin(field), ResultName: result}
		})...)
		specs = append(specs, parseTwoArgSpecs(clause.Flags, "-max", "field", "result", func(field, result string) ssql.WindowSpec {
			return ssql.WindowSpec{Function: ssql.WMax(field), ResultName: result}
		})...)

		// Three-arg functions: -ntile n result, -lag field n result, -lead field n result
		specs = append(specs, parseNtileSpecs(clause.Flags)...)
		specs = append(specs, parseLagLeadSpecs(clause.Flags, "-lag", true)...)
		specs = append(specs, parseLagLeadSpecs(clause.Flags, "-lead", false)...)

		cfg.Specs = specs
		configs = append(configs, cfg)
	}

	return configs, nil
}

// parseSingleArgSpecs parses single-arg accumulated flags (result field only).
// Autocli stores single-arg accumulated flags as []any{string} (not maps).
func parseSingleArgSpecs(flags map[string]any, flagName string, mkSpec func(string) ssql.WindowSpec) []ssql.WindowSpec {
	raw, ok := flags[flagName]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	var specs []ssql.WindowSpec
	for _, v := range arr {
		switch val := v.(type) {
		case string:
			if val != "" {
				specs = append(specs, mkSpec(val))
			}
		case map[string]any:
			if result, ok := val["result"].(string); ok && result != "" {
				specs = append(specs, mkSpec(result))
			}
		}
	}
	return specs
}

// parseTwoArgSpecs parses two-arg accumulated flags (field + result).
func parseTwoArgSpecs(flags map[string]any, flagName, arg1Name, arg2Name string, mkSpec func(string, string) ssql.WindowSpec) []ssql.WindowSpec {
	raw, ok := flags[flagName]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	var specs []ssql.WindowSpec
	for _, v := range arr {
		if m, ok := v.(map[string]any); ok {
			a1, _ := m[arg1Name].(string)
			a2, _ := m[arg2Name].(string)
			if a1 != "" && a2 != "" {
				specs = append(specs, mkSpec(a1, a2))
			}
		}
	}
	return specs
}

// parseNtileSpecs parses -ntile n result flags.
func parseNtileSpecs(flags map[string]any) []ssql.WindowSpec {
	raw, ok := flags["-ntile"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	var specs []ssql.WindowSpec
	for _, v := range arr {
		if m, ok := v.(map[string]any); ok {
			nStr, _ := m["n"].(string)
			result, _ := m["result"].(string)
			if nStr != "" && result != "" {
				n, err := strconv.Atoi(nStr)
				if err != nil {
					continue
				}
				specs = append(specs, ssql.WindowSpec{Function: ssql.WNtile(n), ResultName: result})
			}
		}
	}
	return specs
}

// parseLagLeadSpecs parses -lag/-lead field n result flags.
func parseLagLeadSpecs(flags map[string]any, flagName string, isLag bool) []ssql.WindowSpec {
	raw, ok := flags[flagName]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	var specs []ssql.WindowSpec
	for _, v := range arr {
		if m, ok := v.(map[string]any); ok {
			field, _ := m["field"].(string)
			nStr, _ := m["n"].(string)
			result, _ := m["result"].(string)
			if field != "" && nStr != "" && result != "" {
				n, err := strconv.Atoi(nStr)
				if err != nil {
					continue
				}
				if isLag {
					specs = append(specs, ssql.WindowSpec{Function: ssql.WLag(field, n), ResultName: result})
				} else {
					specs = append(specs, ssql.WindowSpec{Function: ssql.WLead(field, n), ResultName: result})
				}
			}
		}
	}
	return specs
}

// parseRowsFrame parses a frame spec like "2,0" or "*,0" or "*,*".
func parseRowsFrame(spec string) (ssql.WindowFrame, error) {
	parts := strings.SplitN(spec, ",", 2)
	if len(parts) != 2 {
		return ssql.WindowFrame{}, fmt.Errorf("expected format P,F (e.g., 2,0 or *,0)")
	}

	preceding, err := parseFrameValue(strings.TrimSpace(parts[0]))
	if err != nil {
		return ssql.WindowFrame{}, fmt.Errorf("preceding: %w", err)
	}
	following, err := parseFrameValue(strings.TrimSpace(parts[1]))
	if err != nil {
		return ssql.WindowFrame{}, fmt.Errorf("following: %w", err)
	}

	return ssql.WindowFrame{Preceding: preceding, Following: following}, nil
}

func parseFrameValue(s string) (int, error) {
	if s == "*" {
		return -1, nil // UNBOUNDED
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("expected integer or * for unbounded, got %q", s)
	}
	if n < 0 {
		return 0, fmt.Errorf("frame value must be non-negative, got %d", n)
	}
	return n, nil
}

// inferWindowResultType returns the schema type string for a window function result.
func inferWindowResultType(fn ssql.WindowFunc, inputSchema *lib.Schema) string {
	// Use %T to determine the concrete type since the types are unexported
	typeName := fmt.Sprintf("%T", fn)
	switch {
	case strings.HasSuffix(typeName, "wRowNumber"),
		strings.HasSuffix(typeName, "wRank"),
		strings.HasSuffix(typeName, "wDenseRank"),
		strings.HasSuffix(typeName, "wNtile"),
		strings.HasSuffix(typeName, "wCount"):
		return "int"
	case strings.HasSuffix(typeName, "wPercentRank"),
		strings.HasSuffix(typeName, "wSum"),
		strings.HasSuffix(typeName, "wAvg"):
		return "float"
	case strings.HasSuffix(typeName, "wLag"),
		strings.HasSuffix(typeName, "wLead"),
		strings.HasSuffix(typeName, "wFirst"),
		strings.HasSuffix(typeName, "wLast"),
		strings.HasSuffix(typeName, "wMin"),
		strings.HasSuffix(typeName, "wMax"):
		// Infer from source field if schema available
		field := extractStructField(fmt.Sprintf("%+v", fn), "Field")
		if inputSchema != nil && inputSchema.HasField(field) {
			return inputSchema.TypeOf(field)
		}
		return "string"
	default:
		return "string"
	}
}

// generateWindowCode generates Go code for the window command.
func generateWindowCode(configs []ssql.WindowConfig) error {
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

	outputVar := "windowed"
	code := fmt.Sprintf("%s := ssql.Window([]ssql.WindowConfig{\n", outputVar)

	for i, cfg := range configs {
		if i > 0 {
			code += ",\n"
		}
		code += "\t{"

		// PartitionBy
		if len(cfg.PartitionBy) > 0 {
			code += fmt.Sprintf("\n\t\tPartitionBy: []string{%s},", formatStringSlice(cfg.PartitionBy))
		}

		// OrderBy
		if len(cfg.OrderBy) > 0 {
			code += "\n\t\tOrderBy: []ssql.OrderField{"
			for j, of := range cfg.OrderBy {
				if j > 0 {
					code += ", "
				}
				if of.Desc {
					code += fmt.Sprintf("{Field: %q, Desc: true}", of.Field)
				} else {
					code += fmt.Sprintf("{Field: %q}", of.Field)
				}
			}
			code += "},"
		}

		// Frame
		code += fmt.Sprintf("\n\t\tFrame: ssql.WindowFrame{Preceding: %d, Following: %d},", cfg.Frame.Preceding, cfg.Frame.Following)

		// Specs
		if len(cfg.Specs) > 0 {
			code += "\n\t\tSpecs: []ssql.WindowSpec{"
			for j, spec := range cfg.Specs {
				if j > 0 {
					code += ", "
				}
				code += fmt.Sprintf("{Function: %s, ResultName: %q}", formatWindowFunc(spec.Function), spec.ResultName)
			}
			code += "},"
		}

		code += "\n\t}"
	}

	code += fmt.Sprintf(",\n})(%s)", inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// formatStringSlice formats a string slice for Go code.
func formatStringSlice(ss []string) string {
	var parts []string
	for _, s := range ss {
		parts = append(parts, fmt.Sprintf("%q", s))
	}
	return strings.Join(parts, ", ")
}

// formatWindowFunc formats a WindowFunc constructor call for generated Go code.
func formatWindowFunc(fn ssql.WindowFunc) string {
	return windowFuncToCode(fn)
}

// windowFuncToCode converts a WindowFunc to its Go constructor call string.
// Since the concrete types are unexported, we compare against known instances.
func windowFuncToCode(fn ssql.WindowFunc) string {
	// Use fmt.Sprintf(%T) to get the type name
	typeName := fmt.Sprintf("%T", fn)

	switch {
	case strings.HasSuffix(typeName, "wRowNumber"):
		return "ssql.WRowNumber()"
	case strings.HasSuffix(typeName, "wRank"):
		return "ssql.WRank()"
	case strings.HasSuffix(typeName, "wDenseRank"):
		return "ssql.WDenseRank()"
	case strings.HasSuffix(typeName, "wPercentRank"):
		return "ssql.WPercentRank()"
	case strings.HasSuffix(typeName, "wCount"):
		return "ssql.WCount()"
	case strings.HasSuffix(typeName, "wNtile"):
		// Extract N from the struct
		n := fmt.Sprintf("%v", fn)
		// Parse {N:3} or similar
		return fmt.Sprintf("ssql.WNtile(%s)", extractStructField(n, "N"))
	case strings.HasSuffix(typeName, "wLag"):
		v := fmt.Sprintf("%+v", fn)
		return fmt.Sprintf("ssql.WLag(%q, %s)", extractStructField(v, "Field"), extractStructField(v, "Offset"))
	case strings.HasSuffix(typeName, "wLead"):
		v := fmt.Sprintf("%+v", fn)
		return fmt.Sprintf("ssql.WLead(%q, %s)", extractStructField(v, "Field"), extractStructField(v, "Offset"))
	case strings.HasSuffix(typeName, "wFirst"):
		v := fmt.Sprintf("%+v", fn)
		return fmt.Sprintf("ssql.WFirst(%q)", extractStructField(v, "Field"))
	case strings.HasSuffix(typeName, "wLast"):
		v := fmt.Sprintf("%+v", fn)
		return fmt.Sprintf("ssql.WLast(%q)", extractStructField(v, "Field"))
	case strings.HasSuffix(typeName, "wSum"):
		v := fmt.Sprintf("%+v", fn)
		return fmt.Sprintf("ssql.WSum(%q)", extractStructField(v, "Field"))
	case strings.HasSuffix(typeName, "wAvg"):
		v := fmt.Sprintf("%+v", fn)
		return fmt.Sprintf("ssql.WAvg(%q)", extractStructField(v, "Field"))
	case strings.HasSuffix(typeName, "wMin"):
		v := fmt.Sprintf("%+v", fn)
		return fmt.Sprintf("ssql.WMin(%q)", extractStructField(v, "Field"))
	case strings.HasSuffix(typeName, "wMax"):
		v := fmt.Sprintf("%+v", fn)
		return fmt.Sprintf("ssql.WMax(%q)", extractStructField(v, "Field"))
	default:
		return fmt.Sprintf("/* unknown window func: %T */", fn)
	}
}

// extractStructField extracts a named field from fmt.Sprintf("%+v", struct) output.
// Input format: "{Field:salary Offset:1}" — extracts value by field name.
func extractStructField(s, fieldName string) string {
	key := fieldName + ":"
	idx := strings.Index(s, key)
	if idx == -1 {
		return "0"
	}
	start := idx + len(key)
	end := start
	for end < len(s) && s[end] != ' ' && s[end] != '}' {
		end++
	}
	return s[start:end]
}
