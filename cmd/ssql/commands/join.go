package commands

import (
	"fmt"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
)

// RegisterJoin registers the join subcommand
func RegisterJoin(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("join").
		Description("Join records from two data sources (SQL JOIN). Secondary source must be JSONL.").
		Example("ssql from users.csv | ssql join orders.jsonl -on user_id", "Join with JSONL file").
		Example("ssql from users.csv | ssql join <(ssql from csv orders.csv) -on user_id", "Join with CSV via process substitution").
		Example("ssql from employees.csv | ssql join -type left <(ssql from csv departments.csv) -on dept_id", "Left join with CSV").
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
		Flag("-on").
			String().
			FieldsFromFlag("").
			Accumulate().
			Local().
			Help("Field name for equality join (same name in both sides)").
		Done().
		Flag("-left-field").
			String().
			FieldsFromFlag("").
			Local().
			Help("Field name from left side").
		Done().
		Flag("-right-field").
			String().
			FieldsFromFlag("").
			Local().
			Help("Field name from right side").
		Done().
		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.jsonl"}).
			Global().
			Required().
			Help("Right-side JSONL file. For CSV: ssql join <(ssql from csv FILE) -on ...").
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

			// Parse join condition from first clause
			var onFields []string
			var leftField, rightField string

			if len(ctx.Clauses) > 0 {
				clause := ctx.Clauses[0]

				// Get -on fields (simple equality on same field name)
				if onRaw, ok := clause.Flags["-on"]; ok {
					if onSlice, ok := onRaw.([]any); ok {
						for _, v := range onSlice {
							if field, ok := v.(string); ok && field != "" {
								onFields = append(onFields, field)
							}
						}
					}
				}

				// Get -left-field and -right-field
				if lf, ok := clause.Flags["-left-field"].(string); ok {
					leftField = lf
				}
				if rf, ok := clause.Flags["-right-field"].(string); ok {
					rightField = rf
				}
			}

			// Validate join conditions
			if len(onFields) == 0 && (leftField == "" || rightField == "") {
				return fmt.Errorf("join condition required: use -on <field> OR (-left-field <field> -right-field <field>)")
			}
			if len(onFields) > 0 && (leftField != "" || rightField != "") {
				return fmt.Errorf("cannot use both -on and -left-field/-right-field")
			}

			// Check if generation mode is enabled
			if shouldGenerate(generate) {
				return generateJoinCode(rightFile, joinType, onFields, leftField, rightField)
			}

			// Read left-side input from stdin
			leftRecords := lib.ReadJSONL(os.Stdin)

			// Read right-side file (JSONL only - use process substitution for CSV)
			// For CSV files use: ssql join <(ssql from csv data.csv) -on field
			rightInput, err := os.Open(rightFile)
			if err != nil {
				// Provide helpful error for non-JSONL files
				if !strings.HasPrefix(rightFile, "/dev/fd/") && !strings.HasSuffix(strings.ToLower(rightFile), ".jsonl") {
					return fmt.Errorf("opening %s: %w\nFor CSV files use: ssql join <(ssql from csv %s) -on ...", rightFile, err, rightFile)
				}
				return fmt.Errorf("opening right file: %w", err)
			}
			defer rightInput.Close()
			rightSeq := lib.ReadJSONL(rightInput)

			// Build join predicate
			var predicate ssql.JoinPredicate
			if len(onFields) > 0 {
				predicate = ssql.OnFields(onFields...)
			} else {
				// Use different field names
				predicate = ssql.OnCondition(func(left, right ssql.Record) bool {
					leftVal, leftOk := ssql.Get[any](left, leftField)
					rightVal, rightOk := ssql.Get[any](right, rightField)
					if !leftOk || !rightOk {
						return false
					}
					return fmt.Sprintf("%v", leftVal) == fmt.Sprintf("%v", rightVal)
				})
			}

			// Apply appropriate join
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

			// Write output as JSONL
			if err := lib.WriteJSONL(os.Stdout, joined); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateJoinCode generates Go code for the join command
// Handles two scenarios:
// 1. Direct JSONL file: generates a simple function to read the file
// 2. Process substitution (/dev/fd/N): wraps subprocess fragments into a function
func generateJoinCode(rightFile, joinType string, onFields []string, leftField, rightField string) error {
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
			return generateJoinStmtWithFunc(inputVar, funcName, joinType, onFields, leftField, rightField)
		}
		// If reading fragments failed, fall through to normal file handling
	}

	// For regular files, create a simple func fragment that reads the file
	// Build init fragment for the file read
	initCode := fmt.Sprintf(`records, err := ssql.ReadCSV(%q)
	if err != nil {
		return nil
	}`, rightFile)
	initFrag := lib.NewInitFragment("records", initCode, nil, fmt.Sprintf("ssql from %s", rightFile))

	// Create func fragment with just the init
	funcFrag := lib.NewFuncFragment(funcName, []*lib.CodeFragment{initFrag}, fmt.Sprintf("ssql from %s", rightFile))
	if err := lib.WriteCodeFragment(funcFrag); err != nil {
		return fmt.Errorf("writing func fragment: %w", err)
	}

	return generateJoinStmtWithFunc(inputVar, funcName, joinType, onFields, leftField, rightField)
}

// generateJoinStmtWithFunc generates a join statement that calls a function for the right source
func generateJoinStmtWithFunc(inputVar, funcName, joinType string, onFields []string, leftField, rightField string) error {
	// Generate predicate code
	predicateCode, stmtImports := generateJoinPredicate(onFields, leftField, rightField)

	// Generate join function call
	joinFunc := getJoinFunc(joinType)

	// Build stmt code using function call: ssql.InnerJoin(funcName(), predicate)
	outputVar := "joined"
	stmtCode := fmt.Sprintf("%s := %s(%s(), %s)(%s)", outputVar, joinFunc, funcName, predicateCode, inputVar)

	// Write stmt fragment
	stmtFrag := lib.NewStmtFragment(outputVar, inputVar, stmtCode, stmtImports, getCommandString())
	return lib.WriteCodeFragment(stmtFrag)
}

// generateJoinStmt generates the join statement fragment (legacy, for direct variable reference)
func generateJoinStmt(inputVar, rightVarName, joinType string, onFields []string, leftField, rightField string) error {
	// Generate predicate code
	predicateCode, stmtImports := generateJoinPredicate(onFields, leftField, rightField)

	// Generate join function call
	joinFunc := getJoinFunc(joinType)

	// Build stmt code (simple assignment that can be extracted for Chain())
	outputVar := "joined"
	stmtCode := fmt.Sprintf("%s := %s(%s, %s)(%s)", outputVar, joinFunc, rightVarName, predicateCode, inputVar)

	// Write stmt fragment
	stmtFrag := lib.NewStmtFragment(outputVar, inputVar, stmtCode, stmtImports, getCommandString())
	return lib.WriteCodeFragment(stmtFrag)
}

// generateJoinPredicate generates the predicate code for a join
func generateJoinPredicate(onFields []string, leftField, rightField string) (string, []string) {
	var predicateCode string
	var imports []string

	if len(onFields) > 0 {
		// Simple OnFields predicate
		quotedFields := make([]string, len(onFields))
		for i, f := range onFields {
			quotedFields[i] = fmt.Sprintf("%q", f)
		}
		predicateCode = fmt.Sprintf("ssql.OnFields(%s)", strings.Join(quotedFields, ", "))
	} else {
		// OnCondition with different field names
		predicateCode = fmt.Sprintf(`ssql.OnCondition(func(left, right ssql.Record) bool {
		leftVal, leftOk := ssql.Get[any](left, %q)
		rightVal, rightOk := ssql.Get[any](right, %q)
		if !leftOk || !rightOk {
			return false
		}
		return fmt.Sprintf("%%v", leftVal) == fmt.Sprintf("%%v", rightVal)
	})`, leftField, rightField)
		imports = append(imports, "fmt")
	}

	return predicateCode, imports
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
