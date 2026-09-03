package commands

import (
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterJoin registers the join subcommand
func RegisterJoin(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("join").
		Description("Join records from two data sources. Supports multiple lookups with clauses.").
		Example("ssql from users.csv | ssql join orders.csv -using user_id", "Join a file directly — csv/tsv/json inferred from the extension").
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
		Arg("left-field").
		FieldsFromFlag("").
		Done().
		Arg("right-field").
		// Ctrl-O (and the browser's Tab) resolve this from the join's
		// right source since the direct-file/procsub CompleteSource fix
		// — hint the actionable key, not a dead placeholder.
		Completer(&cf.NoCompleter{Hint: FieldHintToken}).
		Done().
		Accumulate().
		Local().
		Help("Join on different field names: -on <left> <right>").
		Done().
		Flag("-as").
		Arg("right-field").
		Completer(&cf.NoCompleter{Hint: FieldHintToken}).
		Done().
		Arg("new-name").
		Completer(&cf.NoCompleter{Hint: "<new-name>"}).
		Done().
		Accumulate().
		Local().
		Help("Rename field from right side: -as <old> <new>").
		Done().
		Flag("-suffix").
		String().
		Global().
		Default("").
		Help("Add suffix to all right-side non-key fields: -suffix _right → name_right").
		Done().
		Flag("-exclude-left").
		Bool().
		Global().
		Help("Exclude non-key fields from left side").
		Done().
		Flag("-exclude-right").
		Bool().
		Global().
		Help("Exclude non-key fields from right side (only bring key + -as fields)").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.{json,jsonl,csv,tsv}"}).
		Global().
		Required().
		Help("Right-side file (JSONL/JSON). For CSV: ssql join <(ssql from FILE) ...").
		Done().
		Handler(func(ctx *cf.Context) error {
			var rightFile, joinType, suffix string
			var generate, excludeLeft, excludeRight bool

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
			if sfxVal, ok := ctx.GlobalFlags["-suffix"]; ok {
				suffix = sfxVal.(string)
			}
			if elVal, ok := ctx.GlobalFlags["-exclude-left"]; ok {
				excludeLeft = elVal.(bool)
			}
			if erVal, ok := ctx.GlobalFlags["-exclude-right"]; ok {
				excludeRight = erVal.(bool)
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
			leftSchemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			leftRecords := leftSchemaAndRecords.Records
			leftSchema := leftSchemaAndRecords.Schema

			// Read the right-side file with extension-inferred format —
			// the same convenience `from FILE` provides. CSV/TSV/JSON
			// read directly; .jsonl / procsubs carry the wire format.
			rightSeq, rightSchema, err := readAuxInput(rightFile)
			if err != nil {
				return err
			}
			// Bare .jsonl without a schema header must fail LOUDLY —
			// a headerless file silently loses field information.
			if rightSchema == nil {
				return fmt.Errorf("right-side file %s has no schema header — pipe through ssql first: ssql join <(ssql from jsonl %s) ...", rightFile, rightFile)
			}

			// Validate join field names against schemas
			for _, clause := range clauses {
				if leftSchema != nil && !leftSchema.HasField(clause.LeftField) {
					return fmt.Errorf("join left field %q not found (available: %s)",
						clause.LeftField, strings.Join(leftSchema.Fields, ", "))
				}
				if rightSchema != nil && !rightSchema.HasField(clause.RightField) {
					return fmt.Errorf("join right field %q not found (available: %s)",
						clause.RightField, strings.Join(rightSchema.Fields, ", "))
				}
			}

			// Handle field collision, suffix, and exclude flags
			if leftSchema != nil && rightSchema != nil {
				// Collect join key fields
				joinKeys := make(map[string]bool)
				for _, c := range clauses {
					joinKeys[c.LeftField] = true
					if c.LeftField != c.RightField {
						joinKeys[c.RightField] = true
					}
				}

				leftFields := make(map[string]bool)
				for _, f := range leftSchema.Fields {
					leftFields[f] = true
				}

				// Apply -suffix to ALL non-key right fields (not just collisions)
				if suffix != "" {
					if len(clauses) > 0 && clauses[0].FieldRenames == nil {
						clauses[0].FieldRenames = make(map[string]string)
					}
					for _, rf := range rightSchema.Fields {
						if joinKeys[rf] {
							continue
						}
						suffixed := rf + suffix
						// Check suffixed name doesn't collide with left
						if leftFields[suffixed] {
							return fmt.Errorf("join: suffixed field %q collides with left-side field", suffixed)
						}
						clauses[0].FieldRenames[rf] = suffixed
					}
				}

				// Check for remaining collisions (when no suffix and no exclude)
				if suffix == "" && !excludeLeft && !excludeRight {
					var collisions []string
					for _, rf := range rightSchema.Fields {
						if joinKeys[rf] {
							continue
						}
						// Check if -as renames handle this field
						renamed := false
						for _, c := range clauses {
							if _, ok := c.FieldRenames[rf]; ok {
								renamed = true
								break
							}
						}
						if !renamed && leftFields[rf] {
							collisions = append(collisions, rf)
						}
					}
					if len(collisions) > 0 {
						return fmt.Errorf("join field collision: %s exist in both sides — use -as to rename, -suffix to auto-rename, or -exclude-left/-exclude-right",
							strings.Join(collisions, ", "))
					}
				}
			}

			// Collect join key fields for exclude logic
			joinKeys := make(map[string]bool)
			for _, c := range clauses {
				joinKeys[c.LeftField] = true
				if c.LeftField != c.RightField {
					joinKeys[c.RightField] = true
				}
			}

			// -exclude-right: set empty FieldRenames so LookupJoin copies nothing from right
			if excludeRight {
				for i := range clauses {
					if clauses[i].FieldRenames == nil {
						clauses[i].FieldRenames = make(map[string]string)
					}
				}
			}

			// Execute join
			var joined iter.Seq[ssql.Record]
			var outputSchema *lib.Schema

			if len(clauses) == 1 && clauses[0].FieldRenames == nil && !excludeRight {
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

				joined = joinFilter(leftRecords)

				// Build output schema by merging left and right schemas
				if leftSchema != nil || rightSchema != nil {
					outputSchema = lib.NewSchema()
					if leftSchema != nil {
						for _, field := range leftSchema.Fields {
							outputSchema.AddField(field, leftSchema.TypeOf(field))
						}
					}
					if rightSchema != nil {
						for _, field := range rightSchema.Fields {
							if !outputSchema.HasField(field) {
								outputSchema.AddField(field, rightSchema.TypeOf(field))
							}
						}
					}
				}
			} else {
				// Multi-clause, selective field lookup, or -exclude-right
				joined = ssql.LookupJoin(rightSeq, clauses)(leftRecords)

				// Build output schema: left schema + renamed fields
				if leftSchema != nil || rightSchema != nil {
					outputSchema = lib.NewSchema()
					if leftSchema != nil {
						for _, field := range leftSchema.Fields {
							outputSchema.AddField(field, leftSchema.TypeOf(field))
						}
					}
					for _, clause := range clauses {
						for rightField, newName := range clause.FieldRenames {
							typ := lib.TypeString
							if rightSchema != nil && rightSchema.HasField(rightField) {
								typ = rightSchema.TypeOf(rightField)
							}
							if !outputSchema.HasField(newName) {
								outputSchema.AddField(newName, typ)
							}
						}
					}
				}
			}

			// Apply -exclude-left: remove non-key left fields from output
			if excludeLeft && leftSchema != nil {
				excludeFields := make(map[string]bool)
				for _, f := range leftSchema.Fields {
					if !joinKeys[f] {
						excludeFields[f] = true
					}
				}
				joined = ssql.Select(func(r ssql.Record) ssql.Record {
					mut := r.ToMutable()
					for f := range excludeFields {
						mut = mut.Delete(f)
					}
					return mut.Freeze()
				})(joined)
				if outputSchema != nil {
					filtered := lib.NewSchema()
					for _, f := range outputSchema.Fields {
						if !excludeFields[f] {
							filtered.AddField(f, outputSchema.TypeOf(f))
						}
					}
					outputSchema = filtered
				}
			}

			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outputSchema, joined); err != nil {
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
		lc := ssql.LookupClause{}

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
							if lc.FieldRenames == nil {
								lc.FieldRenames = make(map[string]string)
							}
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
	var leftSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		leftSchema = fragments[len(fragments)-1].OutputTypedSchema
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

			// Typed mode: subprocess fragments must carry a schema.
			if typedMode() {
				rightSchema := findOutputSchema(rightFragments)
				if rightSchema == nil {
					return lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: right side of join did not produce a typed schema (inner pipeline must use typed-mode commands)"))
				}
				if leftSchema == nil {
					return lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: 'join' must follow a typed-mode source"))
				}
				return emitTypedJoin(inputVar, funcName, leftSchema, rightSchema, rightFragments, subCommandStr, clauses)
			}

			// Create a func fragment that wraps the subprocess pipeline
			funcFrag := lib.NewFuncFragment(funcName, rightFragments, subCommandStr)
			if err := lib.WriteCodeFragment(funcFrag); err != nil {
				return fmt.Errorf("writing func fragment: %w", err)
			}

			// Generate join statement using the function call
			return generateJoinStmtWithFunc(inputVar, funcName, joinType, clauses, fragments)
		}
		// If reading fragments failed, fall through to normal file handling
	}

	// Typed-mode regular-file path: sample the right-side file ourselves.
	if typedMode() {
		if leftSchema == nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'join' must follow a typed-mode source"))
		}
		joinFmt := ""
		if fi, ok := formatForPath(rightFile); ok {
			joinFmt = fi.Name
		}
		var rightSchema *lib.TypedSchema
		var rightStructDef, readCode string
		switch joinFmt {
		case "csv":
			var err error
			rightSchema, rightStructDef, err = lib.SampleCSVSchema(rightFile, "", 0)
			if err != nil {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: %w", err))
			}
			readCode = fmt.Sprintf("right%s := typed.ReadCSV[%s](%q)", rightSchema.TypeName, rightSchema.TypeName, rightFile)
		case "tsv":
			var delim byte
			var err error
			rightSchema, rightStructDef, delim, err = lib.SampleTSVSchema(rightFile, "", 0)
			if err != nil {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: %w", err))
			}
			readCode = fmt.Sprintf("right%s := typed.ReadDelim[%s](%q%s)", rightSchema.TypeName, rightSchema.TypeName, rightFile, typedDelimArg(delim))
		default:
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'join' on %s files not yet supported in typed mode — use <(ssql from %s)", filepath.Ext(rightFile), rightFile))
		}
		// Build a synthesized init fragment for the right side and an
		// inline func fragment that wraps it.
		rightInit := lib.NewInitFragment(
			fmt.Sprintf("right%s", rightSchema.TypeName),
			readCode,
			[]string{"github.com/rosscartlidge/ssql/v4/typed"},
			fmt.Sprintf("ssql from %s", rightFile),
		)
		rightInit.OutputTypedSchema = rightSchema
		rightInit.StructDefs = []string{rightStructDef}
		return emitTypedJoin(inputVar, funcName, leftSchema, rightSchema,
			[]*lib.CodeFragment{rightInit},
			fmt.Sprintf("ssql from %s", rightFile),
			clauses,
		)
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
	} else if strings.HasSuffix(strings.ToLower(rightFile), ".tsv") {
		initCode = `records, err := ssql.ReadTSV(*flagJoin)
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

	return generateJoinStmtWithFunc(inputVar, funcName, joinType, clauses, fragments)
}

// findOutputSchema returns the OutputTypedSchema of the last fragment
// in the slice that has one set, or nil.
func findOutputSchema(fragments []*lib.CodeFragment) *lib.TypedSchema {
	for i := len(fragments) - 1; i >= 0; i-- {
		if fragments[i].OutputTypedSchema != nil {
			return fragments[i].OutputTypedSchema
		}
	}
	return nil
}

// emitTypedJoin writes the func+stmt fragments for a typed-mode join.
// rightFragments become the body of a `func rightSourceN() iter.Seq[RightT]`,
// and the stmt fragment emits the typed.HashJoin call with merge function.
func emitTypedJoin(
	inputVar, funcName string,
	leftSchema, rightSchema *lib.TypedSchema,
	rightFragments []*lib.CodeFragment,
	subCommandStr string,
	clauses []ssql.LookupClause,
) error {
	if joinTypeIsUnsupported(clauses) {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: only single-clause joins without -as renames are supported in v1; got %d clause(s) with renames", len(clauses)))
	}
	clause := clauses[0]

	// Build merged struct.
	mergedSchema, mergedDef := mergeJoinSchemas(leftSchema, rightSchema)

	// Func fragment carrying the right side.
	funcFrag := lib.NewFuncFragment(funcName, rightFragments, subCommandStr)
	if err := lib.WriteCodeFragment(funcFrag); err != nil {
		return fmt.Errorf("writing func fragment: %w", err)
	}

	// Find the right-side Go field name for the join key.
	leftKeyField, ok := lookupSchemaField(leftSchema, clause.LeftField)
	if !ok {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: 'join -on/-using' references unknown left field %q", clause.LeftField))
	}
	rightKeyField, ok := lookupSchemaField(rightSchema, clause.RightField)
	if !ok {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: 'join -on/-using' references unknown right field %q", clause.RightField))
	}
	if leftKeyField.GoType != rightKeyField.GoType {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: join key types differ (left %s: %s, right %s: %s)", leftKeyField.Name, leftKeyField.GoType, rightKeyField.Name, rightKeyField.GoType))
	}
	keyType := leftKeyField.GoType

	// Build merge function body.
	var mergeAssignments []string
	for _, f := range mergedSchema.Fields {
		// Look up where this field came from. Prefer left side.
		if _, isLeft := lookupSchemaField(leftSchema, f.Name); isLeft {
			lf, _ := lookupSchemaField(leftSchema, f.Name)
			mergeAssignments = append(mergeAssignments, fmt.Sprintf("%s: l.%s", f.GoName, lf.GoName))
		} else {
			rf, _ := lookupSchemaField(rightSchema, f.Name)
			mergeAssignments = append(mergeAssignments, fmt.Sprintf("%s: r.%s", f.GoName, rf.GoName))
		}
	}

	// Emit BOTH templates so the planner can pick. The HashJoinParallel
	// form (left=Stream[L]) is preferred when the upstream produces
	// ShapeStream; the HashJoin form (left=iter.Seq[L]) is the
	// alternative for serial pipelines (or after a planner-driven
	// source downgrade). The right side is always read serially via
	// funcName() — process substitution / file source.
	makeJoinCode := func(joinFn string) string {
		return fmt.Sprintf(`joined := %s(%s, %s(),
		func(l %s) %s { return l.%s },
		func(r %s) %s { return r.%s },
		func(l %s, r %s) %s {
			return %s{
				%s,
			}
		})`,
			joinFn, inputVar, funcName,
			leftSchema.TypeName, keyType, leftKeyField.GoName,
			rightSchema.TypeName, keyType, rightKeyField.GoName,
			leftSchema.TypeName, rightSchema.TypeName, mergedSchema.TypeName,
			mergedSchema.TypeName,
			strings.Join(mergeAssignments, ",\n\t\t\t\t"),
		)
	}
	parallelCode := makeJoinCode("typed.HashJoinParallel")
	serialCode := makeJoinCode("typed.HashJoin")
	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}

	stmtFrag := lib.NewStmtFragment("joined", inputVar, parallelCode, imports, getCommandString())
	stmtFrag.InputTypedSchema = leftSchema
	stmtFrag.OutputTypedSchema = mergedSchema
	stmtFrag.StructDefs = []string{mergedDef}
	stmtFrag.IsStream = true
	stmtFrag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeStream}
	stmtFrag.AltCodeIfSeq = serialCode
	stmtFrag.AltImportsIfSeq = imports
	stmtFrag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
	return lib.WriteCodeFragment(stmtFrag)
}

func joinTypeIsUnsupported(clauses []ssql.LookupClause) bool {
	if len(clauses) != 1 {
		return true
	}
	if len(clauses[0].FieldRenames) > 0 {
		return true
	}
	return false
}

func lookupSchemaField(s *lib.TypedSchema, name string) (lib.TypedSchemaField, bool) {
	for _, f := range s.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return lib.TypedSchemaField{}, false
}

// mergeJoinSchemas returns the union of left + right fields. Right-side
// fields whose CSV name collides with a left-side name are dropped (the
// left value wins). The Go type name is "<Left>_<Right>".
func mergeJoinSchemas(left, right *lib.TypedSchema) (*lib.TypedSchema, string) {
	merged := &lib.TypedSchema{
		TypeName: fmt.Sprintf("%s_%s", left.TypeName, right.TypeName),
	}
	seen := make(map[string]bool, len(left.Fields))
	for _, f := range left.Fields {
		merged.Fields = append(merged.Fields, f)
		seen[strings.ToLower(f.Name)] = true
	}
	for _, f := range right.Fields {
		if seen[strings.ToLower(f.Name)] {
			continue
		}
		merged.Fields = append(merged.Fields, f)
	}
	// Render struct with no tags — this is a synthetic merge type, not
	// meant to round-trip through CSV reading.
	var b strings.Builder
	fmt.Fprintf(&b, "// %s is the merged row type produced by the join.\n", merged.TypeName)
	fmt.Fprintf(&b, "type %s struct {\n", merged.TypeName)
	maxName := 0
	maxType := 0
	for _, f := range merged.Fields {
		if len(f.GoName) > maxName {
			maxName = len(f.GoName)
		}
		if len(f.GoType) > maxType {
			maxType = len(f.GoType)
		}
	}
	for _, f := range merged.Fields {
		fmt.Fprintf(&b, "\t%-*s %s\n", maxName, f.GoName, f.GoType)
	}
	b.WriteString("}\n")
	return merged, b.String()
}

// generateJoinStmtWithFunc generates a join statement that calls a function for the right source
func generateJoinStmtWithFunc(inputVar, funcName, joinType string, clauses []ssql.LookupClause, fragments []*lib.CodeFragment) error {
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
