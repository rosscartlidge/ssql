package commands

import (
	"fmt"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterRename registers the rename subcommand
func RegisterRename(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("rename").
		Description("Rename fields").
		Example("ssql from data.csv | ssql rename -as oldname newname", "Rename a single field").
		Example("ssql from users.csv | ssql rename -as first_name firstName -as last_name lastName", "Rename multiple fields to camelCase").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-as").
			Arg("old-field").
				FieldsFromFlag("").
				Done().
			Arg("new-field").
				Completer(cf.NoCompleter{Hint: "<new-name>"}).
				Done().
			Accumulate().
			Global().
			Help("Rename old-field to new-field").
			Done().

		Handler(func(ctx *cf.Context) error {
			var generate bool

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Extract rename mappings from -as flags
			var renames []struct{ oldField, newField string }
			if asVal, ok := ctx.GlobalFlags["-as"]; ok {
				asSlice, ok := asVal.([]any)
				if !ok {
					return fmt.Errorf("invalid -as flag format")
				}
				for _, item := range asSlice {
					asMap, ok := item.(map[string]any)
					if !ok {
						return fmt.Errorf("invalid -as flag: expected map format")
					}
					oldField, _ := asMap["old-field"].(string)
					newField, _ := asMap["new-field"].(string)
					if oldField == "" || newField == "" {
						return fmt.Errorf("invalid -as flag: both old-field and new-field are required")
					}
					renames = append(renames, struct{ oldField, newField string }{oldField, newField})
				}
			}

			if len(renames) == 0 {
				return fmt.Errorf("no renames specified")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateRenameCode(renames)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Build renamer function using Rename()
			renamer := func(r ssql.Record) ssql.Record {
				mut := r.ToMutable()
				for _, ren := range renames {
					mut = mut.Rename(ren.oldField, ren.newField)
				}
				return mut.Freeze()
			}

			// Apply rename
			renamed := ssql.Select(renamer)(records)

			// Update schema to rename fields
			var outputSchema *lib.Schema
			if schemaAndRecords.Schema != nil {
				outputSchema = schemaAndRecords.Schema.Clone()
				for _, ren := range renames {
					outputSchema.RenameField(ren.oldField, ren.newField)
				}
			}

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(os.Stdout, outputSchema, renamed); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateRenameCode generates Go code for the rename command
func generateRenameCode(renames []struct{ oldField, newField string }) error {
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

	// Get input variable from last fragment
	var inputVar string
	var prevSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	} else {
		inputVar = "records"
	}

	if typedMode() {
		if prevSchema == nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'rename' has no typed input; %s does not yet support typed mode", lastNamedCommand(fragments)))
		}
		// Convert to map old -> new for emitTypedProjection.
		renameMap := make(map[string]string, len(renames))
		for _, r := range renames {
			renameMap[r.oldField] = r.newField
		}
		return emitTypedProjection("rename", "Renamed", inputVar, prevSchema, nil, false, renameMap)
	}

	// Generate rename statements
	var renameStmts strings.Builder
	for _, r := range renames {
		renameStmts.WriteString(fmt.Sprintf("\n\t\tmut = mut.Rename(%q, %q)", r.oldField, r.newField))
	}

	// Generate code
	outputVar := "renamed"
	code := fmt.Sprintf(`%s := ssql.Select(func(r ssql.Record) ssql.Record {
		mut := r.ToMutable()%s
		return mut.Freeze()
	})(%s)`, outputVar, renameStmts.String(), inputVar)

	// Create stmt fragment
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
