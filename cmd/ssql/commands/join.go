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
// 1. Direct JSONL file: generates init fragment to read the file
// 2. Process substitution (/dev/fd/N): reads fragments from it and incorporates them
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

	// Generate unique variable name for right records
	rightVarName := "rightRecords"
	if len(fragments) > 0 {
		// Count how many init fragments exist to create unique names
		initCount := 0
		for _, f := range fragments {
			if f.Type == "init" {
				initCount++
			}
		}
		if initCount > 1 {
			rightVarName = fmt.Sprintf("rightRecords_%d", initCount-1)
		}
	}

	// Check if rightFile is a non-regular file (e.g., /dev/fd/N, named pipe)
	// In generation mode, these contain code fragments from the inner command
	fileInfo, statErr := os.Stat(rightFile)
	if statErr == nil && !fileInfo.Mode().IsRegular() {
		rightFragments, err := lib.ReadCodeFragmentsFromFile(rightFile)
		if err == nil && len(rightFragments) > 0 {
			// Rename all variables in secondary fragments to avoid collision with primary pipeline
			// We assign new names per fragment index, then track what each original var maps to
			// for updating input references
			newVarNames := make([]string, len(rightFragments))
			varRename := make(map[string]string) // maps old var name to new (updated per fragment)

			for i, frag := range rightFragments {
				if frag.Var != "" {
					var newName string
					if i == len(rightFragments)-1 {
						// Last fragment's output becomes rightRecords (or rightRecords_N)
						newName = rightVarName
					} else {
						// Intermediate variables get unique "right_" prefixed names
						newName = fmt.Sprintf("right_%s_%d", frag.Var, i)
					}
					newVarNames[i] = newName
					varRename[frag.Var] = newName // Track latest mapping for this var name
				}
			}

			// Apply renames to all fragments
			for i, frag := range rightFragments {
				// First, rename input variable reference using the mapping built so far
				// (must happen before we update the map with this fragment's output)
				if frag.Input != "" {
					if newInput, ok := varRename[frag.Input]; ok {
						oldInput := frag.Input
						frag.Input = newInput
						// Update code to reference renamed input
						frag.Code = strings.Replace(frag.Code, ")("+oldInput+")", ")("+newInput+")", 1)
					}
				}

				// Then, rename output variable
				if newVarNames[i] != "" {
					oldVar := frag.Var
					newVar := newVarNames[i]
					frag.Var = newVar
					// Update code to use new variable name
					frag.Code = strings.Replace(frag.Code, oldVar+", err :=", newVar+", err :=", 1)
					frag.Code = strings.Replace(frag.Code, oldVar+" :=", newVar+" :=", 1)
					// Update varRename for subsequent fragments that reference this output
					varRename[oldVar] = newVar
				}

				if err := lib.WriteCodeFragment(frag); err != nil {
					return fmt.Errorf("writing secondary fragment: %w", err)
				}
			}
			// Skip to generating join statement (no file-reading init needed)
			return generateJoinStmt(inputVar, rightVarName, joinType, onFields, leftField, rightField)
		}
		// If reading fragments failed, fall through to normal file handling
	}

	// Fragment 1: Init fragment to read right file (JSONL only)
	rightVarInit := fmt.Sprintf(`rightFile_%s, err := os.Open(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening: %%v\n", err)
		os.Exit(1)
	}
	defer rightFile_%s.Close()
	%s := lib.ReadJSONL(rightFile_%s)`, rightVarName, rightFile, rightVarName, rightVarName, rightVarName)
	initImports := []string{"fmt", "os", "github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"}

	// Write init fragment (note: empty command string since join command will be on stmt fragment)
	initFrag := lib.NewInitFragment(rightVarName, rightVarInit, initImports, "")
	if err := lib.WriteCodeFragment(initFrag); err != nil {
		return fmt.Errorf("writing init fragment: %w", err)
	}

	return generateJoinStmt(inputVar, rightVarName, joinType, onFields, leftField, rightField)
}

// generateJoinStmt generates the join statement fragment
func generateJoinStmt(inputVar, rightVarName, joinType string, onFields []string, leftField, rightField string) error {
	// Generate predicate code
	var predicateCode string
	var stmtImports []string
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
		stmtImports = append(stmtImports, "fmt")
	}

	// Generate join function call
	var joinFunc string
	switch joinType {
	case "left":
		joinFunc = "ssql.LeftJoin"
	case "right":
		joinFunc = "ssql.RightJoin"
	case "full":
		joinFunc = "ssql.FullJoin"
	default:
		joinFunc = "ssql.InnerJoin"
	}

	// Build stmt code (simple assignment that can be extracted for Chain())
	outputVar := "joined"
	stmtCode := fmt.Sprintf("%s := %s(%s, %s)(%s)", outputVar, joinFunc, rightVarName, predicateCode, inputVar)

	// Write stmt fragment
	stmtFrag := lib.NewStmtFragment(outputVar, inputVar, stmtCode, stmtImports, getCommandString())
	return lib.WriteCodeFragment(stmtFrag)
}
