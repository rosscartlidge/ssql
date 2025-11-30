package commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v2"
	"github.com/rosscartlidge/ssql/v2/cmd/ssql/lib"
)

// RegisterWhere registers the where subcommand
func RegisterWhere(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("where").
		Description("Filter records based on field conditions").

		Example("ssql read-csv data.csv | ssql where -where age gt 18", "Filter records where age > 18").
		Example("ssql read-csv sales.csv | ssql where -where-expr 'price * qty > 1000'", "Filter using expression (price * qty > 1000)").
		Example("ssql read-csv users.csv | ssql where -where dept eq Sales + -where dept eq Marketing", "Sales OR Marketing departments").
		Example("ssql read-csv users.csv | ssql where -where-expr 'age >= 18 and status == \"active\"'", "Multiple conditions with AND logic").
		Example("ssql read-csv data.csv | ssql where -where-expr 'has(\"email\") and contains(email, \"@\")'", "Validate email field exists and format").
		Example("ssql read-csv sales.csv | ssql where -where-expr '(age >= 18 and verified) or role == \"admin\"'", "Complex boolean logic").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
		Done().
		Flag("-where", "-w").
			Arg("field").FieldsFromFlag("FILE").Done().
			Arg("operator").Completer(&cf.StaticCompleter{Options: []string{"eq", "ne", "gt", "ge", "lt", "le", "contains", "startswith", "endswith", "regex"}}).Done().
			Arg("value").FieldValuesFrom("FILE", "field").Done().
			Accumulate().
			Local().
			Help("Filter condition: -where <field> <operator> <value>").
		Done().
		Flag("-where-expr", "-x").
			Arg("expression").Completer(cf.NoCompleter{Hint: "<boolean-expression>"}).Done().
			Accumulate().
			Local().
			Help("Filter using boolean expression: -where-expr <expression>").
		Done().
		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.jsonl"}).
			Global().
			Default("").
			Help("Input JSONL file (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateWhereCode(ctx, inputFile)
			}

			// Pre-compile all expressions ONCE before processing records
			type clauseData struct {
				matches []struct {
					field string
					op    string
					value string
				}
				exprEvals []func(ssql.Record) (any, error)
			}

			var clauses []clauseData

			for _, clause := range ctx.Clauses {
				// Skip empty clauses
				hasWhere := clause.Flags["-where"] != nil
				hasWhereExpr := clause.Flags["-where-expr"] != nil
				if !hasWhere && !hasWhereExpr {
					continue
				}

				cd := clauseData{}

				// Parse -where conditions
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
								cd.matches = append(cd.matches, struct {
									field string
									op    string
									value string
								}{field, op, value})
							}
						}
					}
				}

				// Parse and compile -where-expr conditions ONCE
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
								return fmt.Errorf("compiling expression %q: %w", expression, err)
							}
							cd.exprEvals = append(cd.exprEvals, eval)
						}
					}
				}

				clauses = append(clauses, cd)
			}

			// Build filter that uses pre-compiled expressions
			filter := func(r ssql.Record) bool {
				if len(clauses) == 0 {
					return true
				}

				// OR logic between clauses
				for _, clause := range clauses {
					clauseMatches := true

					// Check -where conditions
					for _, match := range clause.matches {
						fieldValue, exists := ssql.Get[any](r, match.field)
						if !exists || !applyOperator(fieldValue, match.op, match.value) {
							clauseMatches = false
							break
						}
					}

					// Check -where-expr conditions (using pre-compiled expressions)
					if clauseMatches {
						for _, eval := range clause.exprEvals {
							result, err := eval(r)
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

			// Read JSONL from stdin or file
			input, err := lib.OpenInput(inputFile)
			if err != nil {
				return err
			}
			defer input.Close()

			records := lib.ReadJSONL(input)

			// Apply filter
			filtered := ssql.Where(filter)(records)

			// Write output as JSONL
			if err := lib.WriteJSONL(os.Stdout, filtered); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateWhereCode generates Go code for the where command
func generateWhereCode(ctx *cf.Context, inputFile string) error {
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
	filterCode, imports, preCompileVars := generateWhereCodeFromClauses(ctx.Clauses)

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

	// Write to stdout
	return lib.WriteCodeFragment(frag)
}

// generateWhereCodeFromClauses generates the filter function code
func generateWhereCodeFromClauses(clauses []cf.Clause) (string, []string, []string) {
	var imports []string
	var clauseConditions []string
	var preCompileVars []string
	exprCounter := 0

	// Build conditions for each clause (OR logic between clauses)
	for _, clause := range clauses {
		var andConditions []string

		// Process -where conditions
		if matchesRaw, ok := clause.Flags["-where"]; ok && matchesRaw != nil {
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

					// Generate condition code
					cond, imp := generateCondition(field, op, value)
					andConditions = append(andConditions, cond)
					imports = append(imports, imp...)
				}
			}
		}

		// Process -where-expr conditions
		if exprsRaw, ok := clause.Flags["-where-expr"]; ok && exprsRaw != nil {
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
		imports = append(imports, "github.com/rosscartlidge/ssql/v2/cmd/ssql/lib/runtime")
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

	return code, dedupeImports(imports), preCompileVars
}

// generateCondition generates code for a single where condition
func generateCondition(field, op, value string) (string, []string) {
	var imports []string

	// Detect if value is numeric
	_, err := strconv.ParseFloat(value, 64)
	isNum := err == nil

	switch op {
	case "eq":
		if isNum {
			return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) == %s", field, value), nil
		}
		return fmt.Sprintf("ssql.GetOr(r, %q, \"\") == %q", field, value), nil

	case "ne":
		if isNum {
			return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) != %s", field, value), nil
		}
		return fmt.Sprintf("ssql.GetOr(r, %q, \"\") != %q", field, value), nil

	case "gt":
		return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) > %s", field, value), nil

	case "ge":
		return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) >= %s", field, value), nil

	case "lt":
		return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) < %s", field, value), nil

	case "le":
		return fmt.Sprintf("ssql.GetOr(r, %q, float64(0)) <= %s", field, value), nil

	case "contains":
		imports = append(imports, "strings")
		return fmt.Sprintf("strings.Contains(ssql.GetOr(r, %q, \"\"), %q)", field, value), imports

	case "startswith":
		imports = append(imports, "strings")
		return fmt.Sprintf("strings.HasPrefix(ssql.GetOr(r, %q, \"\"), %q)", field, value), imports

	case "endswith":
		imports = append(imports, "strings")
		return fmt.Sprintf("strings.HasSuffix(ssql.GetOr(r, %q, \"\"), %q)", field, value), imports

	case "regex":
		imports = append(imports, "regexp")
		return fmt.Sprintf("regexp.MustCompile(%q).MatchString(ssql.GetOr(r, %q, \"\"))", value, field), imports

	default:
		return "false", nil
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

