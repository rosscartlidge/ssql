package commands

import (
	"fmt"
	"os"
	"strings"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterUpdate registers the update subcommand
func RegisterUpdate(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("update").
		Description("Conditionally update record fields with new values").
		Example("ssql from users.csv | ssql update -where status eq pending -set status approved", "Update status from pending to approved").
		Example("ssql from sales.csv | ssql update -set-expr total 'price * qty'", "Calculate total using expression").
		Example("ssql from data.csv | ssql update -where age lt 18 -set category minor + -where age ge 18 -set category adult", "Categorize by age using if-else logic").
		Example("ssql from sales.csv | ssql update -where-expr 'total > 1000' -set-expr discount 'total * 0.1'", "Apply conditional discount").
		Example("ssql from users.csv | ssql update -set-expr email 'lower(trim(email))'", "Normalize email addresses").
		Example("ssql from data.csv | ssql update -set-expr tier 'revenue > 10000 ? \"gold\" : (revenue > 5000 ? \"silver\" : \"bronze\")'", "Multi-tier categorization").
		ClauseDescription("Clauses are evaluated in order using if-then-else logic.\nSeparators: +, -\nThe FIRST matching clause applies its updates, then processing stops (first-match-wins).\nThis is different from 'where' which uses OR logic - all clauses are evaluated.").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-where", "-w").
		Arg("field").FieldsFromFlag("").Done().
		Arg("operator").Completer(&cf.StaticCompleter{Options: []string{"eq", "ne", "gt", "ge", "lt", "le", "contains", "startswith", "endswith", "regex"}}).Done().
		Arg("value").FieldValuesFrom("", "field").Done().
		Accumulate().
		Local().
		Help("Condition to check: -where <field> <operator> <value>").
		Done().
		Flag("-where-expr", "-x").
		Arg("expression").Completer(cf.NoCompleter{Hint: "<boolean-expression>"}).Done().
		Accumulate().
		Local().
		Help("Condition using boolean expression: -where-expr <expression>").
		Done().
		Flag("-set", "-s").
		Arg("field").FieldsFromFlag("").Done().
		Arg("value").FieldValuesFrom("", "field").Done().
		Accumulate().
		Local().
		Help("Set field to literal value: -set <field> <value>").
		Done().
		Flag("-set-expr", "-e").
		Arg("field").FieldsFromFlag("").Done().
		Arg("expression").Completer(cf.NoCompleter{Hint: "<expression>"}).Done().
		Accumulate().
		Local().
		Help("Set field to expression result: -set-expr <field> <expression>").
		Done().
		Handler(func(ctx *cf.Context) error {
			var generate bool

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateUpdateCode(ctx)
			}

			// Parse clauses - each clause has optional -where/-where-expr conditions and required -set/-set-expr operations
			type updateClause struct {
				conditions []struct {
					field string
					op    string
					value string
				}
				whereExprEvals []func(ssql.Record) (any, error) // For -where-expr (pre-compiled)
				updates        []struct {
					field    string
					literal  string                         // For -set
					exprEval func(ssql.Record) (any, error) // For -set-expr (pre-compiled)
					isExpr   bool
				}
			}

			var clauses []updateClause

			for _, clause := range ctx.Clauses {
				uc := updateClause{}

				// Parse -match conditions (optional)
				if matchesRaw, ok := clause.Flags["-where"]; ok && matchesRaw != nil {
					matches, ok := matchesRaw.([]any)
					if ok {
						for _, matchRaw := range matches {
							matchMap, ok := matchRaw.(map[string]any)
							if !ok {
								continue
							}

							field, _ := matchMap["field"].(string)
							op, _ := matchMap["operator"].(string)
							value, _ := matchMap["value"].(string)

							if field != "" && op != "" {
								uc.conditions = append(uc.conditions, struct {
									field string
									op    string
									value string
								}{field, op, value})
							}
						}
					}
				}

				// Parse -where-expr conditions and compile expressions ONCE
				if exprsRaw, ok := clause.Flags["-where-expr"]; ok && exprsRaw != nil {
					exprs, ok := exprsRaw.([]any)
					if ok {
						for _, exprRaw := range exprs {
							expression, ok := exprRaw.(string)
							if !ok || expression == "" {
								continue
							}

							// Compile the expression ONCE
							eval, err := compileExpression(expression)
							if err != nil {
								return fmt.Errorf("compiling where-expr %q: %w", expression, err)
							}
							uc.whereExprEvals = append(uc.whereExprEvals, eval)
						}
					}
				}

				// Parse -set operations (literal values)
				if setOpsRaw, ok := clause.Flags["-set"]; ok && setOpsRaw != nil {
					setList, ok := setOpsRaw.([]any)
					if ok {
						for _, setRaw := range setList {
							setMap, ok := setRaw.(map[string]any)
							if !ok {
								continue
							}

							field, _ := setMap["field"].(string)
							value, _ := setMap["value"].(string)

							if field != "" {
								uc.updates = append(uc.updates, struct {
									field    string
									literal  string
									exprEval func(ssql.Record) (any, error)
									isExpr   bool
								}{field: field, literal: value, isExpr: false})
							}
						}
					}
				}

				// Parse -set-expr operations and compile expressions ONCE
				if setExprRaw, ok := clause.Flags["-set-expr"]; ok && setExprRaw != nil {
					setList, ok := setExprRaw.([]any)
					if ok {
						for _, setRaw := range setList {
							setMap, ok := setRaw.(map[string]any)
							if !ok {
								continue
							}

							field, _ := setMap["field"].(string)
							expression, _ := setMap["expression"].(string)

							if field != "" && expression != "" {
								// Compile the expression ONCE
								eval, err := compileExpression(expression)
								if err != nil {
									return fmt.Errorf("compiling expression %q: %w", expression, err)
								}
								uc.updates = append(uc.updates, struct {
									field    string
									literal  string
									exprEval func(ssql.Record) (any, error)
									isExpr   bool
								}{field: field, exprEval: eval, isExpr: true})
							}
						}
					}
				}

				if len(uc.updates) > 0 {
					clauses = append(clauses, uc)
				}
			}

			if len(clauses) == 0 {
				return fmt.Errorf("no -set or -set-expr operations specified")
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Track schema from first record
			var schemaFields map[string]any // field -> sample value (for type inference)
			var isFirstRecord = true

			// Track which fields have already been warned about type coercion
			warnedFields := make(map[string]bool)

			// Track new fields and their types (determined from first assignment)
			newFieldTypes := make(map[string]any) // field -> default value for type

			// Collect all fields being set across all clauses
			allSetFields := make(map[string]bool)
			for _, clause := range clauses {
				for _, upd := range clause.updates {
					allSetFields[upd.field] = true
				}
			}

			// Build update filter with first-match-wins clause evaluation (using pre-compiled expressions)
			updateFilter := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
				frozen := mut.Freeze()

				// On first record, capture the schema
				if isFirstRecord {
					schemaFields = make(map[string]any)
					for k, v := range frozen.All() {
						schemaFields[k] = v
					}
					isFirstRecord = false
				}

				// Track which new fields get set in this record
				newFieldsSetThisRecord := make(map[string]bool)

				// Evaluate clauses in order - first match wins
				clauseMatched := false
				for _, clause := range clauses {
					// Check all conditions in this clause (AND logic)
					allMatch := true
					for _, cond := range clause.conditions {
						fieldValue, exists := ssql.Get[any](frozen, cond.field)
						if !exists || !applyOperator(fieldValue, cond.op, cond.value) {
							allMatch = false
							break
						}
					}

					// Check where-expr conditions
					if allMatch {
						for _, eval := range clause.whereExprEvals {
							result, err := eval(frozen)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error evaluating where-expr: %v\n", err)
								allMatch = false
								break
							}

							boolResult, ok := result.(bool)
							if !ok {
								fmt.Fprintf(os.Stderr, "Where-expr must return boolean, got %T\n", result)
								allMatch = false
								break
							}

							if !boolResult {
								allMatch = false
								break
							}
						}
					}

					// If clause matches (or has no conditions), apply updates and stop
					if allMatch {
						clauseMatched = true
						for _, upd := range clause.updates {
							var parsedValue any

							// Check if this is an expression or a literal
							if upd.isExpr {
								// Evaluate using pre-compiled expression
								result, err := upd.exprEval(frozen)
								if err != nil {
									// Log error and set field to empty string
									fmt.Fprintf(os.Stderr, "Error evaluating expression: %v\n", err)
									parsedValue = ""
								} else {
									parsedValue = result
								}
							} else {
								// Parse as literal
								parsedValue = parseValue(upd.literal)
							}

							// Check if this is a new field (not in original schema)
							_, existsInSchema := schemaFields[upd.field]
							existingValue, existsInRecord := ssql.Get[any](frozen, upd.field)

							if !existsInSchema {
								// New field - track its type for defaults
								if _, typeKnown := newFieldTypes[upd.field]; !typeKnown {
									newFieldTypes[upd.field] = getDefaultForValue(parsedValue)
								}
								newFieldsSetThisRecord[upd.field] = true
								mut = applyValueToRecord(mut, upd.field, parsedValue)
							} else {
								// Existing field - coerce to existing type if needed
								var coerced bool
								mut, coerced = applyValueToRecordWithTypeCheck(mut, upd.field, parsedValue, existingValue, existsInRecord)
								if coerced && !warnedFields[upd.field] {
									fmt.Fprintf(os.Stderr, "Warning: field %q value coerced to match existing type (use 'ssql cast' for explicit type conversion)\n", upd.field)
									warnedFields[upd.field] = true
								}
							}
						}
						break // First match wins
					}
				}

				// For new fields that weren't set in this record, apply defaults
				for field, defaultVal := range newFieldTypes {
					if !newFieldsSetThisRecord[field] {
						// Check if field already exists in record (set by previous records processing)
						if _, exists := ssql.Get[any](frozen, field); !exists {
							mut = applyValueToRecord(mut, field, defaultVal)
						}
					}
				}

				// If no clause matched but we have new fields being introduced,
				// we still need to add defaults for new fields
				if !clauseMatched {
					for field := range allSetFields {
						if _, existsInSchema := schemaFields[field]; !existsInSchema {
							// This is a new field - if we have a type for it, add default
							if defaultVal, hasType := newFieldTypes[field]; hasType {
								if _, exists := ssql.Get[any](frozen, field); !exists {
									mut = applyValueToRecord(mut, field, defaultVal)
								}
							}
						}
					}
				}

				return mut
			})

			// Apply update
			updated := updateFilter(records)

			// Update schema to include any new fields added by -set/-set-expr
			outputSchema := schemaAndRecords.Schema
			if outputSchema != nil {
				hasNewFields := false
				for field := range allSetFields {
					if !outputSchema.HasField(field) {
						hasNewFields = true
						break
					}
				}
				if hasNewFields {
					outputSchema = outputSchema.Clone()
					for _, clause := range clauses {
						for _, upd := range clause.updates {
							if !outputSchema.HasField(upd.field) {
								if upd.isExpr {
									outputSchema.AddField(upd.field, lib.TypeString)
								} else {
									outputSchema.AddField(upd.field, lib.InferTypeString(parseValue(upd.literal)))
								}
							}
						}
					}
				}
			}

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(os.Stdout, outputSchema, updated); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateUpdateCode generates Go code for the update command with conditional clauses
func generateUpdateCode(ctx *cf.Context) error {
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

	// Get input variable from last fragment (or default to "records")
	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	// Parse clauses - each clause has optional -match conditions and required -set/-set-expr operations
	type updateClause struct {
		conditions []struct {
			field string
			op    string
			value string
		}
		whereExprs []string // For -where-expr expressions
		updates    []struct {
			field  string
			value  string
			isExpr bool
		}
	}

	var clauses []updateClause

	for _, clause := range ctx.Clauses {
		uc := updateClause{}

		// Parse -match conditions (optional)
		if matchesRaw, ok := clause.Flags["-where"]; ok && matchesRaw != nil {
			matches, ok := matchesRaw.([]any)
			if ok {
				for _, matchRaw := range matches {
					matchMap, ok := matchRaw.(map[string]any)
					if !ok {
						continue
					}

					field, _ := matchMap["field"].(string)
					op, _ := matchMap["operator"].(string)
					value, _ := matchMap["value"].(string)

					if field != "" && op != "" {
						uc.conditions = append(uc.conditions, struct {
							field string
							op    string
							value string
						}{field, op, value})
					}
				}
			}

			// Parse -where-expr conditions (boolean expressions)
			if exprsRaw, ok := clause.Flags["-where-expr"]; ok && exprsRaw != nil {
				exprs, ok := exprsRaw.([]any)
				if ok {
					for _, exprRaw := range exprs {
						expression, ok := exprRaw.(string)
						if ok && expression != "" {
							uc.whereExprs = append(uc.whereExprs, expression)
						}
					}
				}
			}
		}

		// Parse -set operations (literal values)
		if setOpsRaw, ok := clause.Flags["-set"]; ok && setOpsRaw != nil {
			setList, ok := setOpsRaw.([]any)
			if ok {
				for _, setRaw := range setList {
					setMap, ok := setRaw.(map[string]any)
					if !ok {
						continue
					}

					field, _ := setMap["field"].(string)
					value, _ := setMap["value"].(string)

					if field != "" {
						uc.updates = append(uc.updates, struct {
							field  string
							value  string
							isExpr bool
						}{field, value, false})
					}
				}
			}
		}

		// Parse -set-expr operations (expressions)
		if setExprRaw, ok := clause.Flags["-set-expr"]; ok && setExprRaw != nil {
			setList, ok := setExprRaw.([]any)
			if ok {
				for _, setRaw := range setList {
					setMap, ok := setRaw.(map[string]any)
					if !ok {
						continue
					}

					field, _ := setMap["field"].(string)
					expr, _ := setMap["expression"].(string)

					if field != "" {
						uc.updates = append(uc.updates, struct {
							field  string
							value  string
							isExpr bool
						}{field, expr, true})
					}
				}
			}
		}

		if len(uc.updates) > 0 {
			clauses = append(clauses, uc)
		}
	}

	if len(clauses) == 0 {
		return fmt.Errorf("no -set or -set-expr operations specified")
	}

	// Generate Update code with conditional clauses
	var codeBody strings.Builder
	var preCompileVars []string
	exprCounter := 0
	needsTime := false
	needsStrings := false
	needsRegexp := false
	needsRuntime := false

	// Check if we need frozen (for reading in conditions or evaluating expressions)
	needsFrozen := false
	for _, clause := range clauses {
		// Need frozen if there are conditions or where-exprs
		if len(clause.conditions) > 0 || len(clause.whereExprs) > 0 {
			needsFrozen = true
			break
		}
		// Need frozen if there are expressions
		for _, upd := range clause.updates {
			if upd.isExpr {
				needsFrozen = true
				break
			}
		}
		if needsFrozen {
			break
		}
	}

	if needsFrozen {
		codeBody.WriteString("\t\tfrozen := mut.Freeze()\n\n")
	}

	// Generate clause evaluation (first-match-wins)
	for i, clause := range clauses {
		indent := "\t\t"

		// Generate condition check for this clause
		if len(clause.conditions) > 0 || len(clause.whereExprs) > 0 {
			if i == 0 {
				codeBody.WriteString(indent + "if ")
			} else {
				codeBody.WriteString(indent + "} else if ")
			}

			// Generate all conditions with AND logic
			condCount := 0
			for _, cond := range clause.conditions {
				if condCount > 0 {
					codeBody.WriteString(" && ")
				}
				codeBody.WriteString(generateConditionCode(cond.field, cond.op, cond.value))
				condCount++

				// Track which imports are needed
				switch cond.op {
				case "contains", "startswith", "endswith":
					needsStrings = true
				case "regex":
					needsRegexp = true
				}
			}

			// Generate where-expr conditions
			for _, whereExpr := range clause.whereExprs {
				if condCount > 0 {
					codeBody.WriteString(" && ")
				}
				needsRuntime = true
				exprCounter++
				varName := fmt.Sprintf("exprFilter%d", exprCounter)
				preCompileVars = append(preCompileVars,
					fmt.Sprintf("var %s = runtime.MustCompileExprFilter(%q)", varName, whereExpr))
				codeBody.WriteString(varName + "(frozen)")
				condCount++
			}

			codeBody.WriteString(" {\n")
			indent = "\t\t\t"
		} else if i > 0 {
			// Default case (no conditions) - use else
			codeBody.WriteString("\t\t} else {\n")
			indent = "\t\t\t"
		}

		// Generate update statements
		for _, upd := range clause.updates {
			var stmt string

			// Check if this is an expression
			if upd.isExpr {
				// Pre-compile expression and use runtime function
				needsRuntime = true
				exprCounter++
				varName := fmt.Sprintf("exprEval%d", exprCounter)
				preCompileVars = append(preCompileVars,
					fmt.Sprintf("var %s = runtime.MustCompileExpr(%q)", varName, upd.value))

				// Generate code to use pre-compiled expression
				var stmtBuilder strings.Builder
				stmtBuilder.WriteString(indent + "{\n")
				stmtBuilder.WriteString(indent + "\tresult, err := " + varName + "(frozen)\n")
				stmtBuilder.WriteString(indent + "\tif err != nil {\n")
				stmtBuilder.WriteString(fmt.Sprintf("%s\t\tmut = mut.String(%q, \"\")\n", indent, upd.field))
				stmtBuilder.WriteString(indent + "\t} else {\n")
				stmtBuilder.WriteString(indent + "\t\tswitch v := result.(type) {\n")
				stmtBuilder.WriteString(indent + "\t\tcase int64:\n")
				stmtBuilder.WriteString(fmt.Sprintf("%s\t\t\tmut = mut.Int(%q, v)\n", indent, upd.field))
				stmtBuilder.WriteString(indent + "\t\tcase float64:\n")
				stmtBuilder.WriteString(fmt.Sprintf("%s\t\t\tmut = mut.Float(%q, v)\n", indent, upd.field))
				stmtBuilder.WriteString(indent + "\t\tcase bool:\n")
				stmtBuilder.WriteString(fmt.Sprintf("%s\t\t\tmut = mut.Bool(%q, v)\n", indent, upd.field))
				stmtBuilder.WriteString(indent + "\t\tcase string:\n")
				stmtBuilder.WriteString(fmt.Sprintf("%s\t\t\tmut = mut.String(%q, v)\n", indent, upd.field))
				stmtBuilder.WriteString(indent + "\t\tcase int:\n")
				stmtBuilder.WriteString(fmt.Sprintf("%s\t\t\tmut = mut.Int(%q, int64(v))\n", indent, upd.field))
				stmtBuilder.WriteString(indent + "\t\tcase float32:\n")
				stmtBuilder.WriteString(fmt.Sprintf("%s\t\t\tmut = mut.Float(%q, float64(v))\n", indent, upd.field))
				stmtBuilder.WriteString(indent + "\t\tdefault:\n")
				stmtBuilder.WriteString(fmt.Sprintf("%s\t\t\tmut = mut.String(%q, fmt.Sprintf(\"%%v\", v))\n", indent, upd.field))
				stmtBuilder.WriteString(indent + "\t\t}\n")
				stmtBuilder.WriteString(indent + "\t}\n")
				stmtBuilder.WriteString(indent + "}")
				stmt = stmtBuilder.String()
			} else {
				// Generate literal value code
				parsedValue := parseValue(upd.value)

				switch v := parsedValue.(type) {
				case int64:
					stmt = fmt.Sprintf("%smut = mut.Int(%q, int64(%d))", indent, upd.field, v)
				case float64:
					stmt = fmt.Sprintf("%smut = mut.Float(%q, %f)", indent, upd.field, v)
				case bool:
					stmt = fmt.Sprintf("%smut = mut.Bool(%q, %t)", indent, upd.field, v)
				case time.Time:
					timeExpr := fmt.Sprintf("time.Date(%d, %d, %d, %d, %d, %d, %d, time.UTC)",
						v.Year(), v.Month(), v.Day(), v.Hour(), v.Minute(), v.Second(), v.Nanosecond())
					stmt = fmt.Sprintf("%smut = ssql.Set(mut, %q, %s)", indent, upd.field, timeExpr)
					needsTime = true
				case string:
					stmt = fmt.Sprintf("%smut = mut.String(%q, %q)", indent, upd.field, v)
				default:
					stmt = fmt.Sprintf("%smut = mut.String(%q, %q)", indent, upd.field, upd.value)
				}
			}

			codeBody.WriteString(stmt + "\n")
		}
	}

	// Close the if-else chain if we had conditions
	if len(clauses) > 0 && (len(clauses[0].conditions) > 0 || len(clauses) > 1) {
		codeBody.WriteString("\t\t}")
	}

	// Build complete code with pre-compile vars
	var codeLines []string
	for _, preVar := range preCompileVars {
		codeLines = append(codeLines, preVar)
	}

	outputVar := "updated"
	updateCode := fmt.Sprintf(`%s := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
%s
		return mut
	})(%s)`, outputVar, codeBody.String(), inputVar)
	codeLines = append(codeLines, updateCode)
	code := strings.Join(codeLines, "\n")

	// Determine imports needed
	imports := []string{}
	if needsTime {
		imports = append(imports, "time")
	}
	if needsStrings {
		imports = append(imports, "strings")
	}
	if needsRegexp {
		imports = append(imports, "regexp")
	}
	if needsRuntime {
		imports = append(imports, "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime")
	}

	// Create and write fragment
	frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// generateConditionCode generates the Go code for a single condition check
func generateConditionCode(field, op, value string) string {
	switch op {
	case "eq":
		return fmt.Sprintf("ssql.GetOr(frozen, %q, %s) == %s",
			field, getDefaultValueForComparison(value), getComparisonValue(value))
	case "ne":
		return fmt.Sprintf("ssql.GetOr(frozen, %q, %s) != %s",
			field, getDefaultValueForComparison(value), getComparisonValue(value))
	case "gt", "ge", "lt", "le":
		// Numeric comparisons
		return fmt.Sprintf("ssql.GetOr(frozen, %q, float64(0)) %s %s",
			field, getOperatorCode(op), getComparisonValue(value))
	case "contains":
		return fmt.Sprintf("strings.Contains(ssql.GetOr(frozen, %q, \"\"), %s)",
			field, getComparisonValue(value))
	case "startswith":
		return fmt.Sprintf("strings.HasPrefix(ssql.GetOr(frozen, %q, \"\"), %s)",
			field, getComparisonValue(value))
	case "endswith":
		return fmt.Sprintf("strings.HasSuffix(ssql.GetOr(frozen, %q, \"\"), %s)",
			field, getComparisonValue(value))
	case "regex":
		// For regexp, we need to compile the pattern
		return fmt.Sprintf("regexp.MustCompile(%s).MatchString(ssql.GetOr(frozen, %q, \"\"))",
			getComparisonValue(value), field)
	default:
		// Fallback to equality
		return fmt.Sprintf("ssql.GetOr(frozen, %q, %s) == %s",
			field, getDefaultValueForComparison(value), getComparisonValue(value))
	}
}

// getDefaultValueForComparison returns the default value for GetOr based on the comparison value's type
func getDefaultValueForComparison(value string) string {
	parsedValue := parseValue(value)
	switch parsedValue.(type) {
	case int64, float64:
		return "float64(0)"
	case bool:
		return "false"
	case time.Time:
		return "time.Time{}"
	default:
		return `""`
	}
}

// getOperatorCode converts operator string to Go comparison operator
func getOperatorCode(op string) string {
	switch op {
	case "eq":
		return "=="
	case "ne":
		return "!="
	case "gt":
		return ">"
	case "ge":
		return ">="
	case "lt":
		return "<"
	case "le":
		return "<="
	default:
		return "=="
	}
}

// getComparisonValue formats a value for comparison in generated code
func getComparisonValue(value string) string {
	parsedValue := parseValue(value)
	switch v := parsedValue.(type) {
	case int64:
		return fmt.Sprintf("float64(%d)", v)
	case float64:
		return fmt.Sprintf("%f", v)
	case bool:
		return fmt.Sprintf("%t", v)
	case string:
		return fmt.Sprintf("%q", v)
	default:
		return fmt.Sprintf("%q", value)
	}
}
