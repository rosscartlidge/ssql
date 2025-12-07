package commands

import (
	"fmt"
	"iter"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
)

// RegisterUnion registers the union subcommand
func RegisterUnion(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("union").
		Description("Combine records from multiple sources (SQL UNION)").
		Example("ssql from 2023.csv | ssql union -file 2024.csv", "Combine two years of data (removes duplicates)").
		Example("ssql from east.csv | ssql union -all -file west.csv -file south.csv", "Combine three regions keeping all records (UNION ALL)").
		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
		Done().
		Flag("-file", "-f").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.{csv,jsonl}"}).
			Accumulate().
			Local().
			Help("Additional file to union (CSV or JSONL)").
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

			// Read first input from stdin
			firstRecords := lib.ReadJSONL(os.Stdin)

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

			// Write output as JSONL
			if err := lib.WriteJSONL(os.Stdout, result); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateUnionCode generates Go code for the union command
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

	for i, file := range additionalFiles {
		varName := fmt.Sprintf("records%d", i+1)
		readVars = append(readVars, varName)

		// Detect file type by extension
		if strings.HasSuffix(strings.ToLower(file), ".csv") {
			codeLines = append(codeLines, fmt.Sprintf(`%s, err := ssql.ReadCSV(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %%v\n", err)
		os.Exit(1)
	}`, varName, file, file))
		} else {
			// JSON/JSONL
			codeLines = append(codeLines, fmt.Sprintf(`%s, err := ssql.ReadJSONAuto(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %%v\n", err)
		os.Exit(1)
	}`, varName, file, file))
		}
	}

	// Build Concat call
	concatArgs := inputVar
	for _, v := range readVars {
		concatArgs += ", " + v
	}
	codeLines = append(codeLines, fmt.Sprintf("combined := ssql.Concat(%s)", concatArgs))

	// Apply distinct if not UNION ALL
	outputVar := "combined"
	if !unionAll {
		codeLines = append(codeLines, "result := ssql.DistinctBy(ssql.RecordKey)(combined)")
		outputVar = "result"
	}

	code := strings.Join(codeLines, "\n\t")
	imports := []string{"fmt", "os"}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
