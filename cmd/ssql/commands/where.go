package commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterWhere registers the where subcommand
func RegisterWhere(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("where").
		Description("Filter records based on field conditions").
		ClauseDescription("Conditions within a clause use AND logic. Separate clauses with + for OR logic.").
		Example("ssql from data.csv | ssql where -if age gt 18", "Filter records where age > 18").
		Example("ssql from sales.csv | ssql where -if-expr 'price * qty > 1000'", "Filter using expression (price * qty > 1000)").
		Example("ssql from users.csv | ssql where -if dept eq Sales + -if dept eq Marketing", "Sales OR Marketing departments").
		Example("ssql from users.csv | ssql where -if-expr 'age >= 18 and status == \"active\"'", "Multiple conditions with AND logic").
		Example("ssql from data.csv | ssql where -if-expr 'has(\"email\") and contains(email, \"@\")'", "Validate email field exists and format").
		Example("ssql from sales.csv | ssql where -if-expr '(age >= 18 and verified) or role == \"admin\"'", "Complex boolean logic").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-if", "-i").
			Arg("field").
				FieldsFromFlag("").
				Done().
			Arg("operator").
				Completer(&cf.StaticCompleter{Options: []string{"eq", "ne", "gt", "ge", "lt", "le", "contains", "startswith", "endswith", "regex"}}).
				Done().
			Arg("value").
				FieldValuesFrom("", "field").
				Done().
			Accumulate().
			Local().
			Help("Filter condition: -if <field> <op> <value> (use +if to negate)").
			Done().

		Flag("-if-expr", "-x").
			Arg("expression").
				Completer(cf.NoCompleter{Hint: "<boolean-expression>"}).
				Done().
			Accumulate().
			Local().
			Help("Filter using boolean expression: -if-expr <expr> (use +if-expr to negate)").
			Done().

		Handler(func(ctx *cf.Context) error {
			var generate bool

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateWhereCode(ctx)
			}

			// Pre-compile all expressions ONCE before processing records
			type exprEval struct {
				eval    func(ssql.Record) (any, error)
				negated bool
			}
			type clauseData struct {
				conditions []Condition
				exprEvals  []exprEval
			}

			var clauses []clauseData

			for _, clause := range ctx.Clauses {
				// Skip empty clauses
				hasWhere := clause.Flags["-if"] != nil
				hasWhereExpr := clause.Flags["-if-expr"] != nil
				if !hasWhere && !hasWhereExpr {
					continue
				}

				cd := clauseData{}

				// Parse -if conditions (validates operators)
				conditions, err := parseConditions(clause.Flags["-if"])
				if err != nil {
					return err
				}
				cd.conditions = conditions

				// Parse and compile -if-expr / +if-expr conditions ONCE
				if exprsRaw, ok := clause.Flags["-if-expr"]; ok && exprsRaw != nil {
					exprs, ok := exprsRaw.([]any)
					if ok {
						for _, exprRaw := range exprs {
							var expression string
							var negated bool
							switch v := exprRaw.(type) {
							case string:
								expression = v
							case map[string]any:
								expression, _ = v["expression"].(string)
								negated, _ = v["_negated"].(bool)
							}
							if expression == "" {
								continue
							}

							compiled, err := compileExpression(expression)
							if err != nil {
								return fmt.Errorf("compiling expression %q: %w", expression, err)
							}
							cd.exprEvals = append(cd.exprEvals, exprEval{eval: compiled, negated: negated})
						}
					}
				}

				clauses = append(clauses, cd)
			}

			// Collect all field names from -if conditions for first-record validation
			var allFilterFields []string
			for _, cd := range clauses {
				allFilterFields = append(allFilterFields, conditionFields(cd.conditions)...)
			}

			// Build filter that uses pre-compiled expressions
			var filterErr error
			validated := false
			filter := func(r ssql.Record) bool {
				if filterErr != nil {
					return false
				}
				if len(clauses) == 0 {
					return true
				}

				// On first record, validate that all -if field names exist
				if !validated {
					validated = true
					if err := validateFields(r, allFilterFields, "where"); err != nil {
						filterErr = err
						return false
					}
				}

				// OR logic between clauses
				for _, clause := range clauses {
					clauseMatches := true

					// Check -if / +if conditions
					for _, cond := range clause.conditions {
						fieldValue, exists := ssql.Get[any](r, cond.Field)
						match := exists && applyOperator(fieldValue, cond.Operator, cond.Value)
						if cond.Negated {
							match = !match
						}
						if !match {
							clauseMatches = false
							break
						}
					}

					// Check -if-expr / +if-expr conditions (using pre-compiled expressions)
					if clauseMatches {
						for _, ee := range clause.exprEvals {
							result, err := ee.eval(r)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error evaluating expression: %v\n", err)
								clauseMatches = false
								break
							}

							boolResult, ok := result.(bool)
							if !ok {
								fmt.Fprintf(os.Stderr, "Expression must return boolean, got %T\n", result)
								clauseMatches = false
								break
							}

							if ee.negated {
								boolResult = !boolResult
							}

							if !boolResult {
								clauseMatches = false
								break
							}
						}
					}

					if clauseMatches {
						return true
					}
				}

				return false
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Apply filter
			filtered := ssql.Where(filter)(records)

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(os.Stdout, schemaAndRecords.Schema, filtered); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			if filterErr != nil {
				return filterErr
			}

			return nil
		}).
		Done()
	return cmd
}

// generateWhereCode generates Go code for the where command
func generateWhereCode(ctx *cf.Context) error {
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

	// Get input variable name from last fragment
	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	// Generate filter code from clauses
	filterCode, imports, preCompileVars, params := generateWhereCodeFromClauses(ctx.Clauses)

	// Build complete statement with pre-compiled expressions
	var codeLines []string
	for _, preVar := range preCompileVars {
		codeLines = append(codeLines, preVar)
	}

	outputVar := "filtered"
	codeLines = append(codeLines, fmt.Sprintf("%s := ssql.Where(%s)(%s)", outputVar, filterCode, inputVar))
	code := strings.Join(codeLines, "\n")

	// Create code fragment
	frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
	frag.Params = params

	// Write to stdout
	return lib.WriteCodeFragment(frag)
}

// generateWhereCodeFromClauses generates the filter function code
func generateWhereCodeFromClauses(clauses []cf.Clause) (string, []string, []string, []lib.CodeParam) {
	var imports []string
	var clauseConditions []string
	var preCompileVars []string
	var params []lib.CodeParam
	exprCounter := 0

	// Build conditions for each clause (OR logic between clauses)
	for _, clause := range clauses {
		var andConditions []string

		// Process -if conditions
		if matchesRaw, ok := clause.Flags["-if"]; ok && matchesRaw != nil {
			matches, ok := matchesRaw.([]any)
			if ok && len(matches) > 0 {
				for _, matchRaw := range matches {
					matchMap, ok := matchRaw.(map[string]any)
					if !ok {
						continue
					}

					field, _ := matchMap["field"].(string)
					op, _ := matchMap["operator"].(string)
					value, _ := matchMap["value"].(string)

					if field == "" || op == "" {
						continue
					}

					// Generate condition code with parameterized value
					cond, imp, param := generateCondition(field, op, value)
					andConditions = append(andConditions, cond)
					imports = append(imports, imp...)
					if param != nil {
						params = append(params, *param)
					}
				}
			}
		}

		// Process -if-expr conditions
		if exprsRaw, ok := clause.Flags["-if-expr"]; ok && exprsRaw != nil {
			exprs, ok := exprsRaw.([]any)
			if ok && len(exprs) > 0 {
				for _, exprRaw := range exprs {
					// Expression is stored as a string (single arg flag)
					expression, ok := exprRaw.(string)
					if !ok || expression == "" {
						continue
					}

					// Generate pre-compiled expression variable
					exprCounter++
					varName := fmt.Sprintf("exprFilter%d", exprCounter)
					preCompileVars = append(preCompileVars,
						fmt.Sprintf("var %s = runtime.MustCompileExprFilter(%q)", varName, expression))

					// Use the pre-compiled filter
					andConditions = append(andConditions, fmt.Sprintf("%s(r)", varName))
				}
			}
		}

		// Combine AND conditions for this clause
		if len(andConditions) > 0 {
			if len(andConditions) == 1 {
				clauseConditions = append(clauseConditions, andConditions[0])
			} else {
				clauseConditions = append(clauseConditions, "("+strings.Join(andConditions, " && ")+")")
			}
		}
	}

	if len(preCompileVars) > 0 {
		imports = append(imports, "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime")
	}

	// Combine clauses with OR
	var finalCondition string
	if len(clauseConditions) == 0 {
		finalCondition = "return true"
	} else if len(clauseConditions) == 1 {
		finalCondition = "return " + clauseConditions[0]
	} else {
		finalCondition = "return " + strings.Join(clauseConditions, " || ")
	}

	// Build function
	code := fmt.Sprintf("func(r ssql.Record) bool {\n\t\t%s\n\t}", finalCondition)

	return code, dedupeImports(imports), preCompileVars, params
}

// generateCondition generates code for a single where condition.
// Returns the condition code, any extra imports, and an optional CodeParam for the value.
func generateCondition(field, op, value string) (string, []string, *lib.CodeParam) {
	var imports []string

	// Build flag name and var name: e.g., "age-gt" → flagAgeGt
	flagName := field + "-" + op
	varName := "flag" + flagVarName(field) + flagVarName(op)

	param := &lib.CodeParam{
		Name:    flagName,
		Default: value,
		Help:    fmt.Sprintf("filter: %s %s value", field, op),
		VarName: varName,
	}

	// Detect if value is numeric
	_, err := strconv.ParseFloat(value, 64)
	isNum := err == nil

	switch op {
	case "eq":
		if isNum {
			return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) == ssql.ParseFloat64(*%s)", field, varName), nil, param
		}
		return fmt.Sprintf("ssql.GetOr(r, %q, \"\") == *%s", field, varName), nil, param

	case "ne":
		if isNum {
			return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) != ssql.ParseFloat64(*%s)", field, varName), nil, param
		}
		return fmt.Sprintf("ssql.GetOr(r, %q, \"\") != *%s", field, varName), nil, param

	case "gt":
		return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) > ssql.ParseFloat64(*%s)", field, varName), nil, param

	case "ge":
		return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) >= ssql.ParseFloat64(*%s)", field, varName), nil, param

	case "lt":
		return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) < ssql.ParseFloat64(*%s)", field, varName), nil, param

	case "le":
		return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) <= ssql.ParseFloat64(*%s)", field, varName), nil, param

	case "contains":
		imports = append(imports, "strings")
		return fmt.Sprintf("strings.Contains(ssql.GetOr(r, %q, \"\"), *%s)", field, varName), imports, param

	case "startswith":
		imports = append(imports, "strings")
		return fmt.Sprintf("strings.HasPrefix(ssql.GetOr(r, %q, \"\"), *%s)", field, varName), imports, param

	case "endswith":
		imports = append(imports, "strings")
		return fmt.Sprintf("strings.HasSuffix(ssql.GetOr(r, %q, \"\"), *%s)", field, varName), imports, param

	case "regex":
		imports = append(imports, "regexp")
		return fmt.Sprintf("regexp.MustCompile(*%s).MatchString(ssql.GetOr(r, %q, \"\"))", varName, field), imports, param

	default:
		return "false", nil, nil
	}
}

// dedupeImports removes duplicate imports
func dedupeImports(imports []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, imp := range imports {
		if imp != "" && !seen[imp] {
			seen[imp] = true
			result = append(result, imp)
		}
	}
	return result
}
