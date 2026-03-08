package commands

import (
	"fmt"
	"iter"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterUnion registers the union subcommand
func RegisterUnion(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("union").
		Description("Combine records from multiple sources (SQL UNION). Additional files must be JSONL.").
		Example("ssql from 2023.jsonl | ssql union -file 2024.jsonl", "Combine two JSONL files (removes duplicates)").
		Example("ssql from 2023.csv | ssql union -file <(ssql from csv 2024.csv)", "Combine CSV files via process substitution").
		Example("ssql from east.csv | ssql union -all -file <(ssql from csv west.csv) -file <(ssql from csv south.csv)", "Combine multiple CSV files (UNION ALL)").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-file", "-f").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.jsonl"}).
		Accumulate().
		Local().
		Help("Additional JSONL file. For CSV: -file <(ssql from csv FILE)").
		Done().
		Flag("-all", "-a").
		Bool().
		Global().
		Help("Keep duplicates (UNION ALL instead of UNION)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var unionAll bool
			var generate bool

			if allVal, ok := ctx.GlobalFlags["-all"]; ok {
				unionAll = allVal.(bool)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Get additional files from -file flags
			var additionalFiles []string
			if len(ctx.Clauses) > 0 {
				clause := ctx.Clauses[0]
				if filesRaw, ok := clause.Flags["-file"]; ok {
					if filesSlice, ok := filesRaw.([]any); ok {
						for _, v := range filesSlice {
							if file, ok := v.(string); ok && file != "" {
								additionalFiles = append(additionalFiles, file)
							}
						}
					}
				}
			}

			if len(additionalFiles) == 0 {
				return fmt.Errorf("at least one file required for union (use -file)")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateUnionCode(additionalFiles, unionAll)
			}

			// Read first input from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			firstRecords := schemaAndRecords.Records

			// Chain all iterators together
			combined := chainRecords(firstRecords, additionalFiles)

			// Apply distinct if not UNION ALL
			var result iter.Seq[ssql.Record]
			if unionAll {
				result = combined
			} else {
				// Apply distinct using DistinctBy with full record key
				distinct := ssql.DistinctBy(unionRecordToKey)
				result = distinct(combined)
			}

			// Write output as JSONL (preserving schema from first input)
			if err := lib.WriteJSONLWithSchema(os.Stdout, schemaAndRecords.Schema, result); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateUnionCode generates Go code for the union command
// Handles two scenarios for each additional file:
// 1. Direct JSONL file: generates code to read the file
// 2. Process substitution (/dev/fd/N): reads fragments from it and incorporates them
func generateUnionCode(additionalFiles []string, unionAll bool) error {
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
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	// Build code to read additional files and combine with Concat
	var codeLines []string
	var readVars []string
	needsLibImport := false // Only true if we generate lib.ReadJSONL code

	for i, file := range additionalFiles {
		varName := fmt.Sprintf("records%d", i+1)

		// Check if this is a non-regular file (e.g., /dev/fd/N, named pipe)
		// In generation mode, these contain code fragments from the inner command
		fileInfo, statErr := os.Stat(file)
		if statErr == nil && !fileInfo.Mode().IsRegular() {
			fileFragments, err := lib.ReadCodeFragmentsFromFile(file)
			if err == nil && len(fileFragments) > 0 {
				// Rename all variables in secondary fragments to avoid collision
				// We assign new names per fragment index, then track what each original var maps to
				prefix := fmt.Sprintf("union%d_", i+1)
				newVarNames := make([]string, len(fileFragments))
				varRename := make(map[string]string) // maps old var name to new (updated per fragment)

				for j, frag := range fileFragments {
					if frag.Var != "" {
						var newName string
						if j == len(fileFragments)-1 {
							// Last fragment's output becomes recordsN
							newName = varName
						} else {
							// Intermediate variables get unique prefixed names
							newName = fmt.Sprintf("%s%s_%d", prefix, frag.Var, j)
						}
						newVarNames[j] = newName
						varRename[frag.Var] = newName
					}
				}

				// Apply renames to all fragments
				for j, frag := range fileFragments {
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
					if newVarNames[j] != "" {
						oldVar := frag.Var
						newVar := newVarNames[j]
						frag.Var = newVar
						// Update code to use new variable name
						frag.Code = strings.Replace(frag.Code, oldVar+", err :=", newVar+", err :=", 1)
						frag.Code = strings.Replace(frag.Code, oldVar+" :=", newVar+" :=", 1)
						// Update varRename for subsequent fragments that reference this output
						varRename[oldVar] = newVar
					}

					if err := lib.WriteCodeFragment(frag); err != nil {
						return fmt.Errorf("writing fragment from %s: %w", file, err)
					}
				}
				readVars = append(readVars, varName)
				continue // Skip to next file
			}
			// If reading fragments failed, fall through to normal file handling
		}

		// Generate JSONL reading code
		needsLibImport = true
		readVars = append(readVars, varName)
		codeLines = append(codeLines, fmt.Sprintf(`%sFile, err := os.Open(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening %s: %%v\n", err)
		os.Exit(1)
	}
	defer %sFile.Close()
	%s := lib.ReadJSONL(%sFile)`, varName, file, file, varName, varName, varName))
	}

	// Build Concat call
	var concatArgs strings.Builder
	concatArgs.WriteString(inputVar)
	for _, v := range readVars {
		concatArgs.WriteString(", " + v)
	}
	codeLines = append(codeLines, fmt.Sprintf("combined := ssql.Concat(%s)", concatArgs.String()))

	// Apply distinct if not UNION ALL
	outputVar := "combined"
	if !unionAll {
		codeLines = append(codeLines, "result := ssql.DistinctBy(ssql.RecordKey)(combined)")
		outputVar = "result"
	}

	code := strings.Join(codeLines, "\n\t")
	var imports []string
	if needsLibImport {
		imports = []string{"fmt", "os", "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"}
	}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
