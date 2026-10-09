package commands

import (
	"fmt"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
		Example("ssql from trades.csv | ssql join quotes.csv -using sym -asof ts", "ASOF join: each trade takes the quote in force at its time (DFC137)").
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
		Flag("-asof").
		String().
		FieldsFromFlag("").
		Global().
		Help("ASOF join on this ordered field (same name both sides): each left row takes the nearest right row at or before its value").
		Done().
		Flag("-asof-on").
		Arg("left-field").
		FieldsFromFlag("").
		Done().
		Arg("right-field").
		Completer(&cf.NoCompleter{Hint: FieldHintToken}).
		Done().
		Global().
		Help("ASOF join on ordered fields with different names: -asof-on <left> <right>").
		Done().
		Flag("-after").
		Bool().
		Global().
		Help("ASOF: the nearest right row at or AFTER the left's (default: at or before)").
		Done().
		Flag("-strict").
		Bool().
		Global().
		Help("ASOF: strictly before / after, never at the same value").
		Done().
		Flag("-tolerance").
		String().
		Global().
		Help("ASOF: no match farther than this (a duration such as 5m for time fields, a number otherwise)").
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
			asof, err := parseAsofSpec(ctx)
			if err != nil {
				return err
			}

			// Validate we have at least one join condition (an ASOF join
			// without one treats the right side as a single series)
			if len(clauses) == 0 && !asof.active() {
				return fmt.Errorf("join condition required: use -using <field> OR -on <left> <right>")
			}

			opts := joinOptions{suffix: suffix, excludeLeft: excludeLeft, excludeRight: excludeRight}

			// Check if generation mode is enabled
			if shouldGenerate(generate) {
				return generateJoinCode(rightFile, joinType, clauses, opts, asof)
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

			if asof.active() {
				return execAsofJoin(ctx, leftRecords, leftSchema, rightSeq, rightSchema, clauses, asof, joinType, opts)
			}

			var leftFields []string
			if leftSchema != nil {
				leftFields = leftSchema.Fields
			}
			plan, err := resolveJoin(leftFields, rightSchema.Fields, clauses, opts)
			if err != nil {
				return err
			}
			clauses = plan.clauses

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
					// the renamed fields in the RIGHT schema's order, not the
					// map's (a range over the map put -suffix columns in a
					// different order on different runs)
					for _, clause := range clauses {
						for _, rightField := range rightSchema.Fields {
							newName, ok := clause.FieldRenames[rightField]
							if !ok {
								continue
							}
							if !outputSchema.HasField(newName) {
								outputSchema.AddField(newName, rightSchema.TypeOf(rightField))
							}
						}
					}
				}
			}

			// Apply -exclude-left: remove non-key left fields from output
			if len(plan.dropLeft) > 0 {
				joined = ssql.Select(func(r ssql.Record) ssql.Record {
					return ssql.Without(r, plan.dropLeft...)
				})(joined)
				if outputSchema != nil {
					filtered := lib.NewSchema()
					for _, f := range outputSchema.Fields {
						if !slices.Contains(plan.dropLeft, f) {
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

// joinOptions are the flags that shape the merged row beyond the key.
type joinOptions struct {
	suffix                    string
	excludeLeft, excludeRight bool
}

func (o joinOptions) any() bool { return o.suffix != "" || o.excludeLeft || o.excludeRight }

// joinPlan is what resolveJoin decides: the clauses with every rename
// the flags imply, and the non-key left fields -exclude-left drops.
type joinPlan struct {
	clauses  []ssql.LookupClause
	dropLeft []string
}

// resolveJoin applies the merge rules ONCE for every lane (exec calls it
// with the real schemas, the generators with the fields they can see;
// nil = unknown, and a rule that needs an unknown side is skipped, or
// refused when the user asked for it): key fields must exist; -suffix
// renames every non-key right field; -exclude-right copies nothing from
// the right; -exclude-left drops the non-key left fields; and with none
// of those, a non-key field present on BOTH sides is refused rather than
// silently overwritten (until v4.108 only exec refused: record codegen
// let the right win, typed the left, and SQL emitted both columns).
func resolveJoin(leftFields, rightFields []string, clauses []ssql.LookupClause, opts joinOptions) (joinPlan, error) {
	plan := joinPlan{clauses: slices.Clone(clauses)}
	for i := range plan.clauses {
		plan.clauses[i].FieldRenames = maps.Clone(plan.clauses[i].FieldRenames)
	}
	clauses = plan.clauses
	for _, c := range clauses {
		// A key may be a dotted path into a nested value (addr.city): the
		// lookup reads it through ssql.Get like every other field position
		// (DFC144 Level 2; refused by name until 2026-10-09).
		if leftFields != nil && !fieldListHasOrPath(leftFields, c.LeftField) {
			return plan, fmt.Errorf("join left field %q not found (available: %s)", c.LeftField, strings.Join(leftFields, ", "))
		}
		if rightFields != nil && !fieldListHasOrPath(rightFields, c.RightField) {
			return plan, fmt.Errorf("join right field %q not found (available: %s)", c.RightField, strings.Join(rightFields, ", "))
		}
	}
	joinKeys := make(map[string]bool)
	// A key field is exempt from the collision rule when the two sides
	// share its NAME (the row keeps one); a right key with its own name
	// (-on id cust, or an ASOF time named differently) is an ordinary
	// right column, added to the row, and collides with a left field of
	// that name like any other (found by the random tester: two ASOF
	// stages both adding r_f, the second's overwriting the first's).
	sharedKeys := make(map[string]bool)
	for _, c := range clauses {
		joinKeys[c.LeftField] = true
		joinKeys[c.RightField] = true
		if c.LeftField == c.RightField {
			sharedKeys[c.LeftField] = true
		}
	}
	if opts.suffix != "" {
		if rightFields == nil {
			return plan, fmt.Errorf("join -suffix needs the right side's field names, which are not known here — name the renames with -as, or read the file directly (join FILE.csv)")
		}
		if len(clauses) > 0 && clauses[0].FieldRenames == nil {
			clauses[0].FieldRenames = make(map[string]string)
		}
		for _, rf := range rightFields {
			if joinKeys[rf] {
				continue
			}
			suffixed := rf + opts.suffix
			if slices.Contains(leftFields, suffixed) {
				return plan, fmt.Errorf("join: suffixed field %q collides with left-side field", suffixed)
			}
			clauses[0].FieldRenames[rf] = suffixed
		}
	}
	if !opts.any() && leftFields != nil && rightFields != nil {
		var collisions []string
		for _, rf := range rightFields {
			if sharedKeys[rf] {
				continue
			}
			renamed := false
			for _, c := range clauses {
				if _, ok := c.FieldRenames[rf]; ok {
					renamed = true
					break
				}
			}
			if !renamed && slices.Contains(leftFields, rf) {
				collisions = append(collisions, rf)
			}
		}
		if len(collisions) > 0 {
			return plan, fmt.Errorf("join field collision: %s exist in both sides — use -as to rename, -suffix to auto-rename, or -exclude-left/-exclude-right",
				strings.Join(collisions, ", "))
		}
	}
	if opts.excludeRight {
		for i := range clauses {
			if clauses[i].FieldRenames == nil {
				clauses[i].FieldRenames = make(map[string]string)
			}
		}
	}
	if opts.excludeLeft {
		if leftFields == nil {
			return plan, fmt.Errorf("join -exclude-left needs the left side's field names, which are not known here — use `include` on the left instead")
		}
		for _, f := range leftFields {
			if !joinKeys[f] {
				plan.dropLeft = append(plan.dropLeft, f)
			}
		}
	}
	return plan, nil
}

// fragmentFields is the field list a record-mode fragment chain yields,
// as far as it can be known at generation time: the source's advisory
// columns folded through each stage's schemaOp (the same per-command
// rules completion and SSQL_MODE=schema use). nil when unknown (a
// source without advisory types, or a stage whose output the rules
// cannot predict).
func fragmentFields(fragments []*lib.CodeFragment) []string {
	var fields []string
	seeded := false
	for _, f := range fragments {
		if f.Type == "func" || f.Command == "" || f.Op == nil || f.Op.Kind == "" {
			continue
		}
		if !seeded {
			if f.AdvisoryTypes == nil {
				return nil
			}
			fields = slices.Sorted(maps.Keys(f.AdvisoryTypes))
			seeded = true
			continue
		}
		switch f.Op.Kind {
		case "join", "union", "merge":
			return nil // another source's columns, which the rules cannot see
		}
		// the argv as the rules read it: -arg VALUE collapsed to VALUE
		args, err := collapseArgFlag(stageArgs(f))
		if err != nil || len(args) < 2 {
			return nil
		}
		out, ok := lookupSchemaOp(f.Op.Kind)(nil, fields, args[2:])
		if !ok {
			return nil
		}
		fields = out
	}
	if !seeded {
		return nil
	}
	return fields
}

// sideFileFields returns a named side file's field names from its
// header (through readAuxInput, as exec reads it); nil when unreadable
// or headerless.
func sideFileFields(file string) []string {
	_, schema, err := readAuxInput(file)
	if err != nil || schema == nil || len(schema.Fields) == 0 {
		return nil // an empty or unreadable file is not a field list
	}
	return schema.Fields
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
func generateJoinCode(rightFile, joinType string, clauses []ssql.LookupClause, opts joinOptions, asof asofSpec) error {
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

	// A key that is a dotted path into a nested value (addr.city) has no
	// typed form: the join falls back to record mode, where ssql.Get
	// resolves it (DFC144 Level 2; it exited until 2026-10-09).
	var leftKeys, rightKeys []string
	for _, c := range clauses {
		leftKeys = append(leftKeys, c.LeftField)
		rightKeys = append(rightKeys, c.RightField)
	}
	if asof.active() {
		leftKeys = append(leftKeys, asof.leftTime)
		rightKeys = append(rightKeys, asof.rightTime)
	}
	pathKey := nestedPathIn(leftSchema, leftKeys...)
	// A right side whose last stage is record-shaped joins in record mode
	// too (the func returns records; it exited as "no typed schema" before).
	typedJoin := func(rightSchema *lib.TypedSchema) bool {
		return typedMode() && rightSchema != nil && !pathKey && !nestedPathIn(rightSchema, rightKeys...)
	}

	// The merge rules, resolved against the fields each source kind
	// lets generation see (typed: both schemas, in emitTypedJoin).
	leftFields := fragmentFields(fragments)
	if leftSchema != nil {
		leftFields = typedFieldNames(leftSchema)
	}
	resolve := func(rightFields []string) (joinPlan, error) {
		plan, err := resolveJoin(leftFields, rightFields, clauses, opts)
		if err != nil {
			return plan, lib.WriteErrorAndExit(getCommandString(), err)
		}
		return plan, nil
	}
	// Record-mode ASOF: the checks the visible fields allow, then one
	// ssql.AsofJoin statement over the func fragment.
	asofRecord := func(rightFields []string, rightType func(string) string) error {
		leftType := noType
		if len(fragments) > 0 && fragments[len(fragments)-1].AdvisoryTypes != nil {
			adv := fragments[len(fragments)-1].AdvisoryTypes
			leftType = func(f string) string { return advisoryToSchemaType(adv[f]) }
		}
		if err := asofCheck(leftFields, rightFields, leftType, rightType, clauses, asof, opts); err != nil {
			return lib.WriteErrorAndExit(getCommandString(), err)
		}
		cfg, err := asofConfig(clauses, asof, joinType)
		if err != nil {
			return lib.WriteErrorAndExit(getCommandString(), err)
		}
		return generateAsofStmt(inputVar, funcName, cfg)
	}

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
			if rightSchema := findOutputSchema(rightFragments); typedJoin(rightSchema) {
				if rightSchema == nil {
					return lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: right side of join did not produce a typed schema (inner pipeline must use typed-mode commands)"))
				}
				if leftSchema == nil {
					return lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: 'join' must follow a typed-mode source"))
				}
				if asof.active() {
					return emitTypedAsofJoin(inputVar, funcName, leftSchema, rightSchema, rightFragments, subCommandStr, clauses, asof, joinType, opts)
				}
				return emitTypedJoin(inputVar, funcName, leftSchema, rightSchema, rightFragments, subCommandStr, clauses, joinType, opts)
			}

			// The record join reads records: a right side that ends typed
			// (typed mode, the join itself in record mode for a path key)
			// gets the typed→record adapters appended.
			rightFields := fragmentFields(rightFragments)
			rightFragments = lib.EndInRecords(rightFragments)
			if asof.active() {
				funcFrag := lib.NewFuncFragment(funcName, rightFragments, subCommandStr)
				if err := lib.WriteCodeFragment(funcFrag); err != nil {
					return fmt.Errorf("writing func fragment: %w", err)
				}
				return asofRecord(rightFields, noType)
			}

			plan, err := resolve(rightFields)
			if err != nil {
				return err
			}

			// Create a func fragment that wraps the subprocess pipeline
			funcFrag := lib.NewFuncFragment(funcName, rightFragments, subCommandStr)
			if err := lib.WriteCodeFragment(funcFrag); err != nil {
				return fmt.Errorf("writing func fragment: %w", err)
			}

			// Generate join statement using the function call
			return generateJoinStmtWithFunc(inputVar, funcName, joinType, plan)
		}
		// If reading fragments failed, fall through to normal file handling
	}

	// Typed-mode regular-file path: sample the right-side file ourselves
	// (a CSV/TSV right side has no nested values; only a left path key
	// sends the join to record mode).
	if typedMode() && !pathKey {
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
		if asof.active() {
			return emitTypedAsofJoin(inputVar, funcName, leftSchema, rightSchema,
				[]*lib.CodeFragment{rightInit}, fmt.Sprintf("ssql from %s", rightFile), clauses, asof, joinType, opts)
		}
		return emitTypedJoin(inputVar, funcName, leftSchema, rightSchema,
			[]*lib.CodeFragment{rightInit},
			fmt.Sprintf("ssql from %s", rightFile),
			clauses,
			joinType,
			opts,
		)
	}

	var plan joinPlan
	if !asof.active() {
		plan, err = resolve(sideFileFields(rightFile))
		if err != nil {
			return err
		}
	}

	// For regular files, create a simple func fragment that reads the file
	// Build init fragment for the file read - detect file type by extension
	joinParams := []lib.CodeParam{
		{Name: "join", Default: rightFile, Help: "join file", VarName: "flagJoin"},
	}
	// A right-hand file that cannot be opened is a fatal error, exactly
	// as in exec mode — until v4.92.0 this read `return nil`, so a
	// missing file was an empty right side (an inner join emitted
	// nothing, a left join matched nothing) with exit status 0.
	// .jsonl goes through the schema-aware reader: a `_schema` header
	// line (ssql tee output) is not a record.
	var initCode string
	imports := []string{"fmt", "os"}
	lower := strings.ToLower(rightFile)
	switch {
	case strings.HasSuffix(lower, ".jsonl"):
		initCode = `joinHandle, err := os.Open(*flagJoin)
	if err != nil {
		return fmt.Errorf("opening %s: %w", *flagJoin, err)
	}
	// closed when the reader is done, NOT when this function returns: the
	// reader is lazy (a deferred Close here truncated large side files)
	records := ssql.CloseWhenDone(lib.ReadJSONLWithSchema(joinHandle).Records, joinHandle)`
		imports = append(imports, "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib", "github.com/rosscartlidge/ssql/v4")
	case strings.HasSuffix(lower, ".json"):
		initCode = joinReadTemplate("ReadJSON")
	case strings.HasSuffix(lower, ".tsv"):
		initCode = joinReadTemplate("ReadTSV")
	default:
		initCode = joinReadTemplate("ReadCSV")
	}
	initFrag := lib.NewInitFragment("records", initCode, imports, fmt.Sprintf("ssql from %s", rightFile))
	initFrag.Params = joinParams

	// Create func fragment with just the init
	funcFrag := lib.NewFuncFragment(funcName, []*lib.CodeFragment{initFrag}, fmt.Sprintf("ssql from %s", rightFile))
	if err := lib.WriteCodeFragment(funcFrag); err != nil {
		return fmt.Errorf("writing func fragment: %w", err)
	}

	if asof.active() {
		rightType := noType
		var rightFields []string
		if _, schema, err := readAuxInput(rightFile); err == nil && schema != nil {
			rightFields, rightType = schema.Fields, schema.TypeOf
		}
		return asofRecord(rightFields, rightType)
	}
	return generateJoinStmtWithFunc(inputVar, funcName, joinType, plan)
}

// advisoryToSchemaType maps a record fragment's advisory Go type to the
// wire schema's type name.
func advisoryToSchemaType(goType string) string {
	switch goType {
	case "int64":
		return lib.TypeInt
	case "float64":
		return lib.TypeFloat
	case "time.Time":
		return lib.TypeTime
	case "":
		return ""
	}
	return lib.TypeString
}

// joinReadTemplate is the record-mode read of a join's right-hand file
// with ssql.<reader>: any error (missing file, unreadable cell) is
// reported and fatal, mirroring the union/merge side-file templates.
func joinReadTemplate(reader string) string {
	return `records, err := ssql.` + reader + `(*flagJoin)
	if err != nil {
		return fmt.Errorf("opening %s: %w", *flagJoin, err)
	}`
}

// findOutputSchema returns the OutputTypedSchema of the last fragment
// in the slice that has one set, or nil.
// findOutputSchema is the typed schema a subprocess chain ends with —
// nil when its last stage is record-shaped (it skipped back to an
// earlier typed stage until 2026-10-09, so a right side ending in a
// record fallback was joined on the wrong columns).
func findOutputSchema(fragments []*lib.CodeFragment) *lib.TypedSchema {
	return lib.TailTypedSchema(fragments)
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
	joinType string,
	opts joinOptions,
) error {
	if opts.any() {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: join -suffix / -exclude-left / -exclude-right are not supported in typed mode (single-clause joins without renames only); use SSQL_MODE=record"))
	}
	if _, err := resolveJoin(typedFieldNames(leftSchema), typedFieldNames(rightSchema), clauses, opts); err != nil {
		return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: %w", err))
	}
	if joinType != "" && joinType != "inner" {
		// An unmatched left row of a left/full join has its right fields
		// ABSENT; a typed struct cannot hold absence (DFC124 §3) and zero
		// values would be a silent semantic change. Refuse, as the
		// -invalid missing cast does, rather than emit the inner join
		// this lane emitted for every -type until v4.108.
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: join -type %s has no typed form (an unmatched row's right fields are absent, which a struct cannot hold — DFC124 §3); use SSQL_MODE=record", joinType))
	}
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
	// The key is compared as ONE Go type. An int key on one side and a
	// float on the other is ordinary data — a single 2.5 in one file's
	// column makes the reader type that whole column float — so numbers
	// widen to float64, as the other lanes compare them (DFC133: the
	// interpreted join matched nothing here, and this lane refused).
	keyType := leftKeyField.GoType
	leftKeyExpr, rightKeyExpr := "l."+leftKeyField.GoName, "r."+rightKeyField.GoName
	if leftKeyField.GoType != rightKeyField.GoType {
		numeric := func(t string) bool { return t == "int64" || t == "float64" }
		if !numeric(leftKeyField.GoType) || !numeric(rightKeyField.GoType) {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: join key types differ (left %s: %s, right %s: %s)", leftKeyField.Name, leftKeyField.GoType, rightKeyField.Name, rightKeyField.GoType))
		}
		keyType = "float64"
		leftKeyExpr, rightKeyExpr = "float64("+leftKeyExpr+")", "float64("+rightKeyExpr+")"
	}

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
		func(l %s) %s { return %s },
		func(r %s) %s { return %s },
		func(l %s, r %s) %s {
			return %s{
				%s,
			}
		})`,
			joinFn, inputVar, funcName,
			leftSchema.TypeName, keyType, leftKeyExpr,
			rightSchema.TypeName, keyType, rightKeyExpr,
			leftSchema.TypeName, rightSchema.TypeName, mergedSchema.TypeName,
			mergedSchema.TypeName,
			strings.Join(mergeAssignments, ",\n\t\t\t\t"),
		)
	}
	// The MULTI forms: a right side with a repeated key (orders per
	// customer) must yield one row per match, as every other lane does;
	// HashJoin/HashJoinParallel keep the last right row per key.
	parallelCode := makeJoinCode("typed.HashJoinMultiParallel")
	serialCode := makeJoinCode("typed.HashJoinMulti")
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
	// Tagged like every generated row type: `ssql` for the typed
	// runtime, `json` so a library caller's encoding/json sees the
	// pipeline's column names (DFC142 library mode).
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
		fmt.Fprintf(&b, "\t%-*s %-*s `ssql:%q json:%q`\n", maxName, f.GoName, maxType, f.GoType, f.Name, f.Name)
	}
	b.WriteString("}\n")
	return merged, b.String()
}

// typedFieldNames are a typed schema's CSV column names.
func typedFieldNames(s *lib.TypedSchema) []string {
	out := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		out[i] = f.Name
	}
	return out
}

// generateJoinStmtWithFunc generates a join statement that calls a function for the right source
func generateJoinStmtWithFunc(inputVar, funcName, joinType string, plan joinPlan) error {
	clauses := plan.clauses
	outputVar := "joined"
	var stmtCode string
	var stmtImports []string

	// For single clause without renames, use traditional join
	if len(clauses) == 1 && clauses[0].FieldRenames == nil {
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
	if err := lib.WriteCodeFragment(stmtFrag); err != nil {
		return err
	}
	if len(plan.dropLeft) > 0 {
		// -exclude-left: the non-key left fields, dropped after the merge,
		// as a continuation fragment (Command ""), the way group-by's
		// second fragment is: the assembler expects each stmt to end in
		// its (inputVar), which it rewires when it re-plans.
		drop := fmt.Sprintf("joinedLeftExcluded := ssql.Select(func(r ssql.Record) ssql.Record { return ssql.Without(r, %s) })(%s)",
			quotedList(plan.dropLeft), outputVar)
		return lib.WriteCodeFragment(lib.NewStmtFragment("joinedLeftExcluded", outputVar, drop, nil, ""))
	}
	return nil
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
