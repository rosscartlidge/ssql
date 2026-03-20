package commands

import (
	"fmt"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterJoin registers the join subcommand
func RegisterJoin(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("join").
		Description("Join records from two data sources. Supports multiple lookups with clauses.").
		Example("ssql from users.csv | ssql join orders.jsonl -using user_id", "Join on same field name").
		Example("ssql from users.csv | ssql join orders.jsonl -on user_id order_user_id", "Join on different field names").
		Example("ssql from data.csv | ssql join <(ssql from kind.csv) -on a_kind kind -as kind_name a_kind_name - -on z_kind kind -as kind_name z_kind_name", "Multiple lookups from same file").
		ClauseDescription("Each clause performs a separate lookup from the right-side file").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-type", "-t").
		String().
		Completer(&cf.StaticCompleter{Options: []string{"inner", "left", "right", "full"}}).
		Global().
		Default("inner").
		Help("Join type: inner, left, right, full (default: inner)").
		Done().
		Flag("-using").
		String().
		FieldsFromFlag("").
		Accumulate().
		Local().
		Help("Field name for equality join (same name in both sides)").
		Done().
		Flag("-on").
		Arg("left-field").FieldsFromFlag("").Done().
		Arg("right-field").Completer(&cf.NoCompleter{Hint: "<right-field>"}).Done().
		Accumulate().
		Local().
		Help("Join on different field names: -on <left> <right>").
		Done().
		Flag("-as").
		Arg("right-field").Completer(&cf.NoCompleter{Hint: "<right-field>"}).Done().
		Arg("new-name").Completer(&cf.NoCompleter{Hint: "<new-name>"}).Done().
		Accumulate().
		Local().
		Help("Rename field from right side: -as <old> <new>").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.{json,jsonl}"}).
		Global().
		Required().
		Help("Right-side file (JSONL/JSON). For CSV: ssql join <(ssql from FILE) ...").
		Done().
		Handler(func(ctx *cf.Context) error {
			var rightFile, joinType string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				rightFile = fileVal.(string)
			}
			if typeVal, ok := ctx.GlobalFlags["-type"]; ok {
				joinType = typeVal.(string)
			} else {
				joinType = "inner" // default
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Validate required file
			if rightFile == "" {
				return fmt.Errorf("right-side file required")
			}

			// Parse all clauses into LookupClauses
			clauses := parseJoinClauses(ctx.Clauses)

			// Validate we have at least one join condition
			if len(clauses) == 0 {
				return fmt.Errorf("join condition required: use -using <field> OR -on <left> <right>")
			}

			// Check if generation mode is enabled
			if shouldGenerate(generate) {
				return generateJoinCode(rightFile, joinType, clauses)
			}

			// Read left-side input from stdin (with schema if present)
			leftSchemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			leftRecords := leftSchemaAndRecords.Records
			leftSchema := leftSchemaAndRecords.Schema

			// Read right-side file (JSONL only - use process substitution for CSV)
			rightInput, err := os.Open(rightFile)
			if err != nil {
				if !strings.HasPrefix(rightFile, "/dev/fd/") && !strings.HasSuffix(strings.ToLower(rightFile), ".jsonl") {
					return fmt.Errorf("opening %s: %w\nFor CSV files use: ssql join <(ssql from %s) ...", rightFile, err, rightFile)
				}
				return fmt.Errorf("opening right file: %w", err)
			}
			defer rightInput.Close()
			rightSchemaAndRecords := lib.ReadJSONLWithSchema(rightInput)
			rightSeq := rightSchemaAndRecords.Records
			rightSchema := rightSchemaAndRecords.Schema

			// For single clause without -as, use traditional join for full merge
			// For multiple clauses or clauses with -as, use LookupJoin
			if len(clauses) == 1 && len(clauses[0].FieldRenames) == 0 {
				// Traditional join - merge all fields from right
				clause := clauses[0]
				var predicate ssql.JoinPredicate
				if clause.LeftField == clause.RightField {
					predicate = ssql.OnFields(clause.LeftField)
				} else {
					predicate = ssql.OnFieldPair(clause.LeftField, clause.RightField)
				}

				var joinFilter ssql.Filter[ssql.Record, ssql.Record]
				switch joinType {
				case "inner":
					joinFilter = ssql.InnerJoin(rightSeq, predicate)
				case "left":
					joinFilter = ssql.LeftJoin(rightSeq, predicate)
				case "right":
					joinFilter = ssql.RightJoin(rightSeq, predicate)
				case "full":
					joinFilter = ssql.FullJoin(rightSeq, predicate)
				default:
					return fmt.Errorf("unsupported join type: %s", joinType)
				}

				joined := joinFilter(leftRecords)

				// Build output schema by merging left and right schemas
				var outputSchema *lib.Schema
				if leftSchema != nil || rightSchema != nil {
					outputSchema = lib.NewSchema()
					// Add all fields from left schema
					if leftSchema != nil {
						for _, field := range leftSchema.Fields {
							outputSchema.AddField(field, leftSchema.TypeOf(field))
						}
					}
					// Add fields from right schema (if not already present)
					if rightSchema != nil {
						for _, field := range rightSchema.Fields {
							if !outputSchema.HasField(field) {
								outputSchema.AddField(field, rightSchema.TypeOf(field))
							}
						}
					}
				}

				if err := lib.WriteJSONLWithSchema(os.Stdout, outputSchema, joined); err != nil {
					return fmt.Errorf("writing output: %w", err)
				}
				return nil
			}

			// Multi-clause or selective field lookup
			joined := ssql.LookupJoin(rightSeq, clauses)(leftRecords)

			// Build output schema: left schema + fields from -as renames
			var outputSchema *lib.Schema
			if leftSchema != nil || rightSchema != nil {
				outputSchema = lib.NewSchema()
				// Add all fields from left schema
				if leftSchema != nil {
					for _, field := range leftSchema.Fields {
						outputSchema.AddField(field, leftSchema.TypeOf(field))
					}
				}
				// Add renamed fields from clauses
				for _, clause := range clauses {
					for rightField, newName := range clause.FieldRenames {
						// Get type from right schema if available
						typ := "string" // default
						if rightSchema != nil && rightSchema.HasField(rightField) {
							typ = rightSchema.TypeOf(rightField)
						}
						if !outputSchema.HasField(newName) {
							outputSchema.AddField(newName, typ)
						}
					}
				}
			}

			if err := lib.WriteJSONLWithSchema(os.Stdout, outputSchema, joined); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}

// parseJoinClauses parses clauses into LookupClauses
func parseJoinClauses(clauses []cf.Clause) []ssql.LookupClause {
	var result []ssql.LookupClause

	for _, clause := range clauses {
		lc := ssql.LookupClause{
			FieldRenames: make(map[string]string),
		}

		// Parse -using flags (same field name both sides)
		if usingRaw, ok := clause.Flags["-using"]; ok {
			if usingSlice, ok := usingRaw.([]any); ok {
				for _, v := range usingSlice {
					if field, ok := v.(string); ok && field != "" {
						// For -using, left and right field are the same
						lc.LeftField = field
						lc.RightField = field
					}
				}
			}
		}

		// Parse -on flags (different field names: left, right)
		// autocli stores multi-arg flags as []any of map[string]any
		if onRaw, ok := clause.Flags["-on"]; ok {
			if onSlice, ok := onRaw.([]any); ok {
				for _, v := range onSlice {
					if onMap, ok := v.(map[string]any); ok {
						if left, ok := onMap["left-field"].(string); ok {
							lc.LeftField = left
						}
						if right, ok := onMap["right-field"].(string); ok {
							lc.RightField = right
						}
					}
				}
			}
		}

		// Parse -as flags (rename: old, new)
		// autocli stores multi-arg flags as []any of map[string]any
		if asRaw, ok := clause.Flags["-as"]; ok {
			if asSlice, ok := asRaw.([]any); ok {
				for _, v := range asSlice {
					if asMap, ok := v.(map[string]any); ok {
						oldName, _ := asMap["right-field"].(string)
						newName, _ := asMap["new-name"].(string)
						if oldName != "" && newName != "" {
							lc.FieldRenames[oldName] = newName
						}
					}
				}
			}
		}

		// Only add clause if it has a join condition
		if lc.LeftField != "" && lc.RightField != "" {
			result = append(result, lc)
		}
	}

	return result
}

// generateJoinCode generates Go code for the join command
// Handles two scenarios:
// 1. Direct JSONL file: generates a simple function to read the file
// 2. Process substitution (/dev/fd/N): wraps subprocess fragments into a function
func generateJoinCode(rightFile, joinType string, clauses []ssql.LookupClause) error {
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

	// Generate unique function name for the right source
	// Count existing func fragments to ensure unique naming across the pipeline
	funcCount := 1
	for _, frag := range fragments {
		if frag.Type == "func" {
			funcCount++
		}
	}
	funcName := fmt.Sprintf("rightSource%d", funcCount)

	// Check if rightFile is a non-regular file (e.g., /dev/fd/N, named pipe)
	// In generation mode, these contain code fragments from the inner command
	fileInfo, statErr := os.Stat(rightFile)
	if statErr == nil && !fileInfo.Mode().IsRegular() {
		rightFragments, err := lib.ReadCodeFragmentsFromFile(rightFile)
		if err == nil && len(rightFragments) > 0 {
			// Build command string from subprocess fragments
			var subCommands []string
			for _, frag := range rightFragments {
				if frag.Command != "" {
					subCommands = append(subCommands, frag.Command)
				}
			}
			subCommandStr := strings.Join(subCommands, " | ")

			// Create a func fragment that wraps the subprocess pipeline
			funcFrag := lib.NewFuncFragment(funcName, rightFragments, subCommandStr)
			if err := lib.WriteCodeFragment(funcFrag); err != nil {
				return fmt.Errorf("writing func fragment: %w", err)
			}

			// Generate join statement using the function call
			return generateJoinStmtWithFunc(inputVar, funcName, joinType, clauses)
		}
		// If reading fragments failed, fall through to normal file handling
	}

	// For regular files, create a simple func fragment that reads the file
	// Build init fragment for the file read - detect file type by extension
	joinParams := []lib.CodeParam{
		{Name: "join", Default: rightFile, Help: "join file", VarName: "flagJoin"},
	}
	var initCode string
	if strings.HasSuffix(strings.ToLower(rightFile), ".jsonl") || strings.HasSuffix(strings.ToLower(rightFile), ".json") {
		initCode = `records, err := ssql.ReadJSON(*flagJoin)
	if err != nil {
		return nil
	}`
	} else {
		initCode = `records, err := ssql.ReadCSV(*flagJoin)
	if err != nil {
		return nil
	}`
	}
	initFrag := lib.NewInitFragment("records", initCode, nil, fmt.Sprintf("ssql from %s", rightFile))
	initFrag.Params = joinParams

	// Create func fragment with just the init
	funcFrag := lib.NewFuncFragment(funcName, []*lib.CodeFragment{initFrag}, fmt.Sprintf("ssql from %s", rightFile))
	if err := lib.WriteCodeFragment(funcFrag); err != nil {
		return fmt.Errorf("writing func fragment: %w", err)
	}

	return generateJoinStmtWithFunc(inputVar, funcName, joinType, clauses)
}

// generateJoinStmtWithFunc generates a join statement that calls a function for the right source
func generateJoinStmtWithFunc(inputVar, funcName, joinType string, clauses []ssql.LookupClause) error {
	outputVar := "joined"
	var stmtCode string
	var stmtImports []string

	// For single clause without renames, use traditional join
	if len(clauses) == 1 && len(clauses[0].FieldRenames) == 0 {
		clause := clauses[0]
		var predicateCode string

		if clause.LeftField == clause.RightField {
			predicateCode = fmt.Sprintf("ssql.OnFields(%q)", clause.LeftField)
		} else {
			predicateCode = fmt.Sprintf("ssql.OnFieldPair(%q, %q)", clause.LeftField, clause.RightField)
		}

		joinFunc := getJoinFunc(joinType)
		stmtCode = fmt.Sprintf("%s := %s(%s(), %s)(%s)", outputVar, joinFunc, funcName, predicateCode, inputVar)
	} else {
		// Multi-clause or selective field lookup - use LookupJoin
		clausesCode := generateClausesCode(clauses)
		stmtCode = fmt.Sprintf("%s := ssql.LookupJoin(%s(), %s)(%s)", outputVar, funcName, clausesCode, inputVar)
	}

	// Write stmt fragment
	stmtFrag := lib.NewStmtFragment(outputVar, inputVar, stmtCode, stmtImports, getCommandString())
	return lib.WriteCodeFragment(stmtFrag)
}

// generateClausesCode generates Go code for []ssql.LookupClause using the Lookup() helper
func generateClausesCode(clauses []ssql.LookupClause) string {
	var clauseStrs []string
	for _, c := range clauses {
		// Build rename pairs as variadic args: "old1", "new1", "old2", "new2"
		var renameArgs []string
		for old, newName := range c.FieldRenames {
			renameArgs = append(renameArgs, fmt.Sprintf("%q", old), fmt.Sprintf("%q", newName))
		}

		if len(renameArgs) > 0 {
			clauseStrs = append(clauseStrs, fmt.Sprintf(
				"ssql.Lookup(%q, %q, %s)",
				c.LeftField, c.RightField, strings.Join(renameArgs, ", ")))
		} else {
			clauseStrs = append(clauseStrs, fmt.Sprintf(
				"ssql.Lookup(%q, %q)",
				c.LeftField, c.RightField))
		}
	}
	return fmt.Sprintf("[]ssql.LookupClause{\n\t\t%s,\n\t}", strings.Join(clauseStrs, ",\n\t\t"))
}

// getJoinFunc returns the ssql join function name for the given join type
func getJoinFunc(joinType string) string {
	switch joinType {
	case "left":
		return "ssql.LeftJoin"
	case "right":
		return "ssql.RightJoin"
	case "full":
		return "ssql.FullJoin"
	default:
		return "ssql.InnerJoin"
	}
}
