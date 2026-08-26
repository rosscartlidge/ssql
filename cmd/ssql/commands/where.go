package commands

import (
	"errors"
	"fmt"
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
		Example("ssql from data.csv | ssql where -if-expr 'has(\"email\") and email contains \"@\"'", "Validate email field exists and format").
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
		Expression().
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
				for _, ec := range parseExprConds(clause.Flags["-if-expr"]) {
					compiled, err := compileExpression(ec.Expression)
					if err != nil {
						return fmt.Errorf("compiling expression %q: %w", ec.Expression, err)
					}
					cd.exprEvals = append(cd.exprEvals, exprEval{eval: compiled, negated: ec.Negated})
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
								fmt.Fprintf(ctx.Stderr(), "Error evaluating expression: %v\n", err)
								clauseMatches = false
								break
							}

							boolResult, ok := result.(bool)
							if !ok {
								fmt.Fprintf(ctx.Stderr(), "Expression must return boolean, got %T\n", result)
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
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Apply filter
			filtered := ssql.Where(filter)(records)

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), schemaAndRecords.Schema, filtered); err != nil {
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
	var prevSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	} else {
		inputVar = "records"
	}

	// Typed-mode branch — emits typed.Where with direct field access.
	// -if-expr predicates transpile to native Go (exprToGo); the two
	// fall-throughs to record code below are:
	//   - prevSchema==nil (upstream is Record after a typed→Record
	//     boundary)
	//   - an expression outside the transpilable subset (Tier R: the
	//     planner inserts the Serial()+toRecord boundary upstream)
	if typedMode() && prevSchema != nil {
		handled, err := generateWhereCodeTyped(ctx.Clauses, inputVar, prevSchema, fragments)
		if handled || err != nil {
			return err
		}
	}

	// Record mode: advisory column types from the upstream fragment let
	// -if-expr predicates transpile to native GetOr code (Phase 4).
	var advisory map[string]string
	if len(fragments) > 0 {
		advisory = fragments[len(fragments)-1].AdvisoryTypes
	}

	// Generate filter code from clauses
	filterCode, imports, preCompileVars, params, planNotes, gerr := generateWhereCodeFromClauses(ctx.Clauses, advisory)
	if gerr != nil {
		return lib.WriteErrorAndExit(getCommandString(), gerr)
	}

	// Build complete statement with pre-compiled expressions
	var codeLines []string
	for _, preVar := range preCompileVars {
		codeLines = append(codeLines, preVar)
	}

	outputVar := uniqueVarName("filtered", fragments)
	codeLines = append(codeLines, fmt.Sprintf("%s := ssql.Where(%s)(%s)", outputVar, filterCode, inputVar))
	code := strings.Join(codeLines, "\n")

	// Create code fragment
	frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
	frag.Params = params
	frag.PlanNotes = planNotes
	frag.AdvisoryTypes = advisory // where preserves the schema

	// Write to stdout
	return lib.WriteCodeFragment(frag)
}

// generateWhereCodeTyped emits a typed.Where call against the input schema.
// -if conditions render via typedWhereCondition; -if-expr predicates
// transpile to native Go via exprToGoBool. Returns handled=false — WITHOUT
// writing a fragment — when an expression falls outside the transpilable
// subset, so the caller can fall back to record-mode codegen (Tier R).
// Unknown fields and invalid operators stay loud errors: they'd fail in
// every mode, just later and worse.
func generateWhereCodeTyped(clauses []cf.Clause, inputVar string, schema *lib.TypedSchema, fragments []*lib.CodeFragment) (bool, error) {
	if schema == nil {
		return true, lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: 'where' must follow a typed-mode source (e.g. 'from FILE.csv') so the input schema is known"))
	}

	// Index fields by lowercased CSV name for case-insensitive lookup.
	byName := make(map[string]lib.TypedSchemaField, len(schema.Fields))
	for _, f := range schema.Fields {
		byName[strings.ToLower(f.Name)] = f
	}

	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	var hoisted []string
	var planNotes []string
	var clauseConds []string
	for _, clause := range clauses {
		var ands []string

		matchesRaw, ok := clause.Flags["-if"]
		if ok && matchesRaw != nil {
			matches, _ := matchesRaw.([]any)
			for _, mr := range matches {
				m, _ := mr.(map[string]any)
				field, _ := m["field"].(string)
				op, _ := m["operator"].(string)
				value, _ := m["value"].(string)
				negated, _ := m["_negated"].(bool)
				if field == "" || op == "" {
					continue
				}
				f, ok := byName[strings.ToLower(field)]
				if !ok {
					return true, lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: 'where' references unknown field %q (schema has %s)", field, fieldNamesList(schema)))
				}
				res, err := typedWhereCondition(f, op, value)
				if err != nil {
					return true, lib.WriteErrorAndExit(getCommandString(), err)
				}
				imports = append(imports, res.Imports...)
				hoisted = append(hoisted, res.Hoisted...)
				cond := res.Src
				if negated {
					cond = "!(" + cond + ")"
				}
				ands = append(ands, cond)
			}
		}

		// -if-expr / +if-expr: transpile to native Go (Tier N); outside the
		// subset, evaluate with the VM against a static env (Tier V) — the
		// stage stays typed either way, so downstream stages keep their
		// parallel forms.
		for _, ec := range parseExprConds(clause.Flags["-if-expr"]) {
			var cond string
			res, err := exprToGoBool(ec.Expression, schema, "r")
			switch {
			case err == nil:
				cond = res.Src
				imports = append(imports, res.Imports...)
				hoisted = append(hoisted, res.Hoisted...)
				planNotes = append(planNotes, fmt.Sprintf("expr %q: native", ec.Expression))
			default:
				var unknownField *exprUnknownFieldError
				if errors.As(err, &unknownField) {
					return true, lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: 'where -if-expr': %w", err))
				}
				call, tvImports, tvHoisted, verr := exprTierVFilter(ec.Expression, schema)
				if verr != nil {
					// Doesn't even compile in the VM — invalid in every mode.
					return true, lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: 'where -if-expr' %q: %w", ec.Expression, verr))
				}
				cond = call
				imports = append(imports, tvImports...)
				hoisted = append(hoisted, tvHoisted...)
				planNotes = append(planNotes, fmt.Sprintf("expr %q: VM with static env (%s)", ec.Expression, exprTierReason(ec.Expression, err)))
			}
			if ec.Negated {
				cond = "!(" + cond + ")"
			}
			ands = append(ands, cond)
		}

		if len(ands) > 0 {
			if len(ands) == 1 {
				clauseConds = append(clauseConds, ands[0])
			} else {
				clauseConds = append(clauseConds, "("+strings.Join(ands, " && ")+")")
			}
		}
	}

	var body string
	switch len(clauseConds) {
	case 0:
		body = "return true"
	case 1:
		body = "return " + clauseConds[0]
	default:
		body = "return " + strings.Join(clauseConds, " || ")
	}

	outputVar := uniqueVarName("filtered", fragments)
	if schemaUsesTime(schema) {
		imports = append(imports, "time")
	}
	imports = dedupeImports(imports)

	// `where` is a pass-through. Stream.Where is embarrassingly
	// parallel; typed.Where is the serial form. Emit BOTH templates
	// so the planner can swap to the iter.Seq form when the
	// upstream's running shape ends up being iter.Seq (e.g. after
	// a source downgrade by parallelism-reach analysis).
	parallelCode := fmt.Sprintf("%s := %s.Where(func(r %s) bool {\n\t\t%s\n\t})",
		outputVar, inputVar, schema.TypeName, body)
	serialCode := fmt.Sprintf("%s := typed.Where(func(r %s) bool {\n\t\t%s\n\t})(%s)",
		outputVar, schema.TypeName, body, inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, parallelCode, imports, getCommandString())
	frag.InputTypedSchema = schema
	frag.OutputTypedSchema = schema
	frag.StructDefs = hoisted // package-level decls (hoisted regexp/VM vars), deduped by the assembler
	frag.PlanNotes = planNotes
	frag.IsStream = true
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeStream}
	frag.AltCodeIfSeq = serialCode
	frag.AltImportsIfSeq = imports
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	return true, lib.WriteCodeFragment(frag)
}

// typedWhereCondition emits a single typed condition like `(r.Age > 30)`
// or `(r.Status == "active")`. Since convergence Phase B it is a thin
// wrapper: it resolves the struct field and delegates the OPERATOR to the
// shared condOpToExprGo lowering (the same emissions the expression form
// uses) — which also makes `regex` work in typed mode (hoisted compiled
// pattern), previously a Tier-3 error. The returned exprGo carries any
// imports ("strings"/"regexp") and hoisted decls the emission needs.
func typedWhereCondition(f lib.TypedSchemaField, op, value string) (exprGo, error) {
	field := "r." + f.GoName
	var lhs exprGo
	switch f.GoType {
	case "int64", "int", "int32", "uint64":
		lhs = exprGo{Src: field, Type: exprGoInt}
	case "float64", "float32":
		lhs = exprGo{Src: field, Type: exprGoFloat}
	case "string":
		lhs = exprGo{Src: field, Type: exprGoString}
	case "bool":
		lhs = exprGo{Src: field, Type: exprGoBool}
	case "time.Time":
		// Narrow legacy support: equality against a parsed literal. Ordering
		// operators never compiled for time.Time and remain unsupported.
		if op == "eq" || op == "ne" {
			lit, err := typedLiteral(f.GoType, value)
			if err != nil {
				return exprGo{}, fmt.Errorf("ssql generate go -typed: where %s %s %q: %w", f.Name, op, value, err)
			}
			sym := map[string]string{"eq": "==", "ne": "!="}[op]
			return exprGo{Src: fmt.Sprintf("(%s %s %s)", field, sym, lit), Type: exprGoBool}, nil
		}
		return exprGo{}, fmt.Errorf("ssql generate go -typed: operator %q not supported for time.Time field %s", op, f.Name)
	default:
		return exprGo{}, fmt.Errorf("ssql generate go -typed: where on %s field %s not supported", f.GoType, f.Name)
	}
	res, err := condOpToExprGo(lhs, op, value, "")
	if err != nil {
		return exprGo{}, fmt.Errorf("ssql generate go -typed: where %s %s %q: %w", f.Name, op, value, err)
	}
	return res, nil
}

// typedLiteral formats value as a Go literal of the given type.
func typedLiteral(goType, value string) (string, error) {
	switch goType {
	case "string":
		return fmt.Sprintf("%q", value), nil
	case "bool":
		if _, err := strconv.ParseBool(value); err != nil {
			return "", fmt.Errorf("invalid bool literal %q", value)
		}
		return value, nil
	case "int", "int32", "int64", "uint64":
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return "", fmt.Errorf("invalid int literal %q", value)
		}
		return value, nil
	case "float32", "float64":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return "", fmt.Errorf("invalid float literal %q", value)
		}
		return value, nil
	case "time.Time":
		// Compare against a parsed RFC3339 literal.
		if _, err := strconv.Unquote(`"` + value + `"`); err != nil {
			// strconv.Unquote validation; just pass value through if it round-trips
			_ = err
		}
		return fmt.Sprintf("func() time.Time { t, _ := time.Parse(time.RFC3339, %q); return t }()", value), nil
	default:
		return "", fmt.Errorf("typed where: unsupported field type %s", goType)
	}
}

// advisoryTypeOf looks up a field's advisory Go type case-insensitively;
// "" when unknown (no advisory, or field not in it).
func advisoryTypeOf(advisory map[string]string, field string) string {
	if t, ok := advisory[field]; ok {
		return t
	}
	for name, t := range advisory {
		if strings.EqualFold(name, field) {
			return t
		}
	}
	return ""
}

func fieldNamesList(s *lib.TypedSchema) string {
	names := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		names[i] = f.Name
	}
	return strings.Join(names, ", ")
}

func schemaUsesTime(s *lib.TypedSchema) bool {
	for _, f := range s.Fields {
		if f.GoType == "time.Time" || f.GoType == "*time.Time" {
			return true
		}
	}
	return false
}

// generateWhereCodeFromClauses generates the filter function code. When
// advisory column types are available (record pipelines fed by a sampling
// source), -if-expr predicates transpile to native GetOr code; otherwise —
// and for expressions outside the subset — the compiled-VM filter var is
// emitted as before.
func generateWhereCodeFromClauses(clauses []cf.Clause, advisory map[string]string) (string, []string, []string, []lib.CodeParam, []string, error) {
	var imports []string
	var clauseConditions []string
	var preCompileVars []string
	var params []lib.CodeParam
	var planNotes []string
	exprCounter := 0
	flagSeen := make(map[string]int) // field+op occurrences within this fragment

	// Build conditions for each clause (OR logic between clauses)
	for _, clause := range clauses {
		var andConditions []string

		// Process -if / +if conditions
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
					negated, _ := matchMap["_negated"].(bool)

					if field == "" || op == "" {
						continue
					}

					// Generate condition code with parameterized value.
					// The advisory field type (when known) picks numeric vs
					// string comparison, matching exec's field-type branch.
					cond, imp, param, cerr := generateCondition(field, op, value, advisoryTypeOf(advisory, field), flagSeen)
					if cerr != nil {
						return "", nil, nil, nil, nil, fmt.Errorf("where -if %s %s %s: %w", field, op, value, cerr)
					}
					if negated {
						cond = "!(" + cond + ")"
					}
					andConditions = append(andConditions, cond)
					imports = append(imports, imp...)
					if param != nil {
						params = append(params, *param)
					}
				}
			}
		}

		// Process -if-expr / +if-expr conditions. parseExprConds handles the
		// map form negated entries arrive in — a plain string type-assert
		// silently dropped every +if-expr condition.
		for _, ec := range parseExprConds(clause.Flags["-if-expr"]) {
			// Native first (Phase 4): needs advisory types, a boolean
			// result, and no hoisted decls (the record assembler has no
			// package-level slot for regexp vars — those stay on the VM).
			// Unknown fields are a refusal here, not a loud error: the VM
			// validates against the real first record, and mid-pipeline
			// commands may legitimately have reshaped the rows.
			if advisory != nil {
				res, err := exprToGoRecord(ec.Expression, advisory, "r")
				if err == nil && res.Type == exprGoBool && len(res.Hoisted) == 0 {
					cond := res.Src
					if ec.Negated {
						cond = "!(" + cond + ")"
					}
					andConditions = append(andConditions, cond)
					imports = append(imports, res.Imports...)
					planNotes = append(planNotes, fmt.Sprintf("expr %q: native (record, advisory types)", ec.Expression))
					continue
				}
				reason := "hoisted declarations need the VM path in record mode"
				if err != nil {
					reason = exprTierReason(ec.Expression, err)
				} else if res.Type != exprGoBool {
					reason = fmt.Sprintf("result is %s, not a boolean predicate", res.Type)
				}
				planNotes = append(planNotes, fmt.Sprintf("expr %q: VM (%s)", ec.Expression, reason))
			} else {
				planNotes = append(planNotes, fmt.Sprintf("expr %q: VM (no advisory column types from upstream)", ec.Expression))
			}
			exprCounter++
			varName := fmt.Sprintf("exprFilter%d", exprCounter)
			preCompileVars = append(preCompileVars,
				fmt.Sprintf("var %s = runtime.MustCompileExprFilter(%q)", varName, ec.Expression))
			call := fmt.Sprintf("%s(r)", varName)
			if ec.Negated {
				call = "!" + call
			}
			andConditions = append(andConditions, call)
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

	return code, dedupeImports(imports), preCompileVars, params, planNotes, nil
}

// generateCondition generates code for a single where condition.
// Returns the condition code, any extra imports, and an optional CodeParam for the value.
// seen counts field+op occurrences within the fragment: duplicate conditions on
// the same field+op get numbered flag/var names at EMISSION time. They must —
// collectParams' cross-fragment rename rewrites references textually, and two
// identical `*flagPopGt` references in one fragment are indistinguishable there
// (both got the last name, silently replacing the first value; three didn't
// compile).
func generateCondition(field, op, value, goType string, seen map[string]int) (string, []string, *lib.CodeParam, error) {
	// Build flag name and var name: e.g., "age-gt" → flagAgeGt
	flagName := field + "-" + op
	varName := "flag" + flagVarName(field) + flagVarName(op)
	seen[flagName]++
	if n := seen[flagName]; n > 1 {
		flagName = fmt.Sprintf("%s%d", flagName, n)
		varName = fmt.Sprintf("%s%d", varName, n)
	}

	param := &lib.CodeParam{
		Name:    flagName,
		Default: value,
		Help:    fmt.Sprintf("filter: %s %s value", field, op),
		VarName: varName,
	}

	// Since convergence Phase B the OPERATOR is lowered by the shared
	// condOpToExprGo — one emission for flag and expression forms alike.
	// Only the LHS resolution stays here: record mode types the GetOr by
	// the advisory field type (exec branches on the runtime field type),
	// falling back to the value-form heuristic without one.
	lhs := recordCondLHS("r", field, op, value, goType)
	res, err := condOpToExprGo(lhs, op, value, varName)
	if err != nil {
		return "", nil, nil, err
	}
	return res.Src, res.Imports, param, nil
}

// recordCondLHS resolves a record-mode condition field to a typed GetOr
// expression. String operators always read the field as a string (the
// pre-existing record behaviour); comparisons branch on the advisory type
// when known, else on the value's form.
func recordCondLHS(recv, field, op, value, goType string) exprGo {
	switch op {
	case "contains", "startswith", "endswith", "regex":
		return exprGo{Src: fmt.Sprintf("ssql.GetOr(%s, %q, \"\")", recv, field), Type: exprGoString}
	}
	numeric := false
	switch goType {
	case "int64", "float64":
		numeric = true
	case "string":
		numeric = false
	case "bool":
		return exprGo{Src: fmt.Sprintf("ssql.GetOr(%s, %q, false)", recv, field), Type: exprGoBool}
	default:
		_, err := strconv.ParseFloat(value, 64)
		numeric = err == nil
	}
	if numeric {
		return exprGo{Src: fmt.Sprintf("ssql.GetOr(%s, %q, float64(0))", recv, field), Type: exprGoFloat}
	}
	return exprGo{Src: fmt.Sprintf("ssql.GetOr(%s, %q, \"\")", recv, field), Type: exprGoString}
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
