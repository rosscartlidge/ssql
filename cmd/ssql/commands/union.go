package commands

import (
	"fmt"
	"iter"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterUnion registers the union subcommand
func RegisterUnion(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("union").
		Description("Combine records from multiple sources (SQL UNION). Additional files must be JSONL.").
		ClauseDescription("Each clause specifies additional files to combine.").
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
		Help("Keep duplicates (UNION ALL, use +all for UNION)").
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

	// Build code to read additional files and combine with Concat.
	// Process substitution sources become func fragments (self-contained functions)
	// to avoid variable name collisions.
	var sourceCalls []string // Function calls or variable names for Concat
	needsLibImport := false

	for _, file := range additionalFiles {
		// Check if this is a non-regular file (e.g., /dev/fd/N, named pipe)
		// In generation mode, these contain code fragments from the inner command
		fileInfo, statErr := os.Stat(file)
		if statErr == nil && !fileInfo.Mode().IsRegular() {
			fileFragments, err := lib.ReadCodeFragmentsFromFile(file)
			if err == nil && len(fileFragments) > 0 {
				// Wrap subprocess fragments in a func fragment.
				// Each becomes its own function, encapsulating all internal variables.
				// Use index-based name since NextFuncName() resets per process.
				funcName := fmt.Sprintf("unionSource%d", len(sourceCalls)+1)
				funcFrag := lib.NewFuncFragment(funcName, fileFragments, getCommandString())
				if err := lib.WriteCodeFragment(funcFrag); err != nil {
					return fmt.Errorf("writing func fragment from %s: %w", file, err)
				}
				sourceCalls = append(sourceCalls, funcName+"()")
				continue
			}
		}

		// Generate JSONL reading code for regular files
		needsLibImport = true
		varName := fmt.Sprintf("unionFile%d", len(sourceCalls)+1)
		// Write an init fragment for the file read
		code := fmt.Sprintf(`%sHandle, err := os.Open(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening %s: %%v\n", err)
		os.Exit(1)
	}
	defer %sHandle.Close()
	%s := lib.ReadJSONL(%sHandle)`, varName, file, file, varName, varName, varName)
		fileFrag := lib.NewInitFragment(varName, code,
			[]string{"fmt", "os", "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"}, "")
		if err := lib.WriteCodeFragment(fileFrag); err != nil {
			return fmt.Errorf("writing file read fragment: %w", err)
		}
		sourceCalls = append(sourceCalls, varName)
	}

	// Build a filter-compatible closure so it works with Chain().
	// Pattern: func(input iter.Seq[ssql.Record]) iter.Seq[ssql.Record] { ... }
	var concatArgs string
	for _, src := range sourceCalls {
		concatArgs += ", " + src
	}

	var filterBody string
	if unionAll {
		filterBody = fmt.Sprintf("return ssql.Concat(input%s)", concatArgs)
	} else {
		filterBody = fmt.Sprintf("return ssql.DistinctBy(ssql.RecordKey)(ssql.Concat(input%s))", concatArgs)
	}

	outputVar := "unioned"
	code := fmt.Sprintf("%s := func(input iter.Seq[ssql.Record]) iter.Seq[ssql.Record] {\n\t\t%s\n\t}(%s)",
		outputVar, filterBody, inputVar)

	imports := []string{"iter"}
	if needsLibImport {
		imports = append(imports, "fmt", "os", "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib")
	}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
