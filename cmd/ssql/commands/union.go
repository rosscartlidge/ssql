package commands

import (
	"fmt"
	"iter"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
)

// RegisterUnion registers the union subcommand
func RegisterUnion(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("union").
		Description("Combine records from multiple sources (SQL UNION)").
		Example("ssql read-csv 2023.csv | ssql union -file 2024.csv", "Combine records from two CSV files (removes duplicates)").
		Example("ssql read-csv east.csv | ssql union -all -file west.csv -file south.csv", "Combine three files keeping all records (UNION ALL)").
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
		Flag("-input", "-i").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.jsonl"}).
			Global().
			Default("").
			Help("First input JSONL file (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var unionAll bool

			if fileVal, ok := ctx.GlobalFlags["-input"]; ok {
				inputFile = fileVal.(string)
			}
			if allVal, ok := ctx.GlobalFlags["-all"]; ok {
				unionAll = allVal.(bool)
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

			// Read first input (stdin or file)
			firstInput, err := lib.OpenInput(inputFile)
			if err != nil {
				return fmt.Errorf("opening first input: %w", err)
			}
			defer firstInput.Close()

			firstRecords := lib.ReadJSONL(firstInput)

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
