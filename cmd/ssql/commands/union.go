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

// RegisterUnion registers the union subcommand
func RegisterUnion(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("union").
		Description("Combine records from multiple sources (SQL UNION). Additional files must be JSONL.").
		ClauseDescription("Each clause specifies additional files to combine.").
		Example("ssql from 2023.jsonl | ssql union -file 2024.jsonl", "Combine two JSONL files (removes duplicates)").
		Example("ssql from 2023.csv | ssql union -file 2024.csv", "Combine CSV files directly (format inferred from extension)").
		Example("ssql from east.csv | ssql union -all -file <(ssql from csv west.csv) -file <(ssql from csv south.csv)", "Combine multiple CSV files (UNION ALL)").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-file", "-f").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.{jsonl,csv,tsv,json}"}).
		Accumulate().
		Local().
		Help("Additional file (csv/tsv/json/schema-headed jsonl — format inferred from extension)").
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
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			firstRecords := schemaAndRecords.Records

			// Open every additional file up front (extension-inferred,
			// like `from FILE`) so a bad file fails loudly before any
			// output — and a headerless .jsonl is rejected per the
			// schema rule (it used to slip through silently).
			extraSources := make([]iter.Seq[ssql.Record], 0, len(additionalFiles))
			for _, file := range additionalFiles {
				recs, schema, err := readAuxInput(file)
				if err != nil {
					return err
				}
				if schema == nil {
					return fmt.Errorf("file %s has no schema header — pipe through ssql first: <(ssql from jsonl %s)", file, file)
				}
				extraSources = append(extraSources, recs)
			}
			combined := func(yield func(ssql.Record) bool) {
				for record := range firstRecords {
					if !yield(record) {
						return
					}
				}
				for _, src := range extraSources {
					for record := range src {
						if !yield(record) {
							return
						}
					}
				}
			}

			// Apply distinct if not UNION ALL
			var result iter.Seq[ssql.Record]
			if unionAll {
				result = combined
			} else {
				// Dedup by ssql.RecordKey — value-based, matching the generated
				// code path. NOT %v, which embeds the schema pointer and never
				// matches records from different sources.
				distinct := ssql.DistinctBy(ssql.RecordKey)
				result = distinct(combined)
			}

			// Write output as JSONL (preserving schema from first input)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), schemaAndRecords.Schema, result); err != nil {
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
	var prevSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	} else {
		inputVar = "records"
	}

	// Phase B fall-through: prevSchema==nil → Record-mode upstream.
	if typedMode() && prevSchema != nil {
		// union is SerialOnly (Concat / Union of multiple sources;
		// no parallel-merge variant yet) — planner inserts
		// Stream.Serial() upstream automatically when input is a
		// Stream. emitTypedUnion sets Capabilities.
		return emitTypedUnion(inputVar, prevSchema, additionalFiles, unionAll)
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

		// Regular files: extension-inferred read, matching exec.
		varName := fmt.Sprintf("unionFile%d", len(sourceCalls)+1)
		var code string
		var imports []string
		switch strings.ToLower(filepath.Ext(file)) {
		case ".csv":
			code = fmt.Sprintf(`%s, err := ssql.ReadCSV(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening %s: %%v\n", err)
		os.Exit(1)
	}`, varName, file, file)
			imports = []string{"fmt", "os"}
		case ".tsv":
			code = fmt.Sprintf(`%s, err := ssql.ReadTSV(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening %s: %%v\n", err)
		os.Exit(1)
	}`, varName, file, file)
			imports = []string{"fmt", "os"}
		default:
			needsLibImport = true
			code = fmt.Sprintf(`%sHandle, err := os.Open(%q)
	if err != nil {
		fmt.Fprintf(ctx.Stderr(), "Error opening %s: %%v\n", err)
		os.Exit(1)
	}
	defer %sHandle.Close()
	%s := lib.ReadJSONL(%sHandle)`, varName, file, file, varName, varName, varName)
			imports = []string{"fmt", "os", "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"}
		}
		fileFrag := lib.NewInitFragment(varName, code, imports, "")
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

	outputVar := uniqueVarName("unioned", fragments)
	code := fmt.Sprintf("%s := func(input iter.Seq[ssql.Record]) iter.Seq[ssql.Record] {\n\t\t%s\n\t}(%s)",
		outputVar, filterBody, inputVar)

	imports := []string{"iter"}
	if needsLibImport {
		imports = append(imports, "fmt", "os", "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib")
	}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
