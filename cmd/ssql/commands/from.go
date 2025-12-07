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

// RegisterFrom registers the from subcommand (SQL-style source command)
func RegisterFrom(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("from").
		Description("Read data from file or command output (auto-detects CSV, JSON, JSONL)").
		Example("ssql from data.csv | ssql where -where age gt 18", "Read CSV file").
		Example("ssql from data.json | ssql limit 10", "Read JSON array or JSONL file").
		Example("ssql from -- ps aux | ssql where -where USER eq root", "Execute command and parse output").
		Example("ssql from -- ls -la | ssql include FILE SIZE", "Parse command output and select fields").
		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
		Done().
		Flag("-format", "-f").
			String().
			Global().
			Default("").
			Completer(&cf.StaticCompleter{Options: []string{"csv", "json", "jsonl"}}).
			Help("Input format for stdin: csv (default), json, jsonl").
		Done().
		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.{csv,json,jsonl}"}).
			Global().
			Default("").
			Help("Input file (CSV, JSON, or JSONL). Reads from stdin if not specified.").
		Done().
		CacheFieldsFrom("FILE").
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var format string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}

			if fmtVal, ok := ctx.GlobalFlags["-format"]; ok {
				format = fmtVal.(string)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if command execution mode (-- separator used)
			if len(ctx.RemainingArgs) > 0 {
				// Check if generation is enabled (flag or env var)
				if shouldGenerate(generate) {
					return generateFromExecCode(ctx.RemainingArgs[0], ctx.RemainingArgs[1:])
				}

				command := ctx.RemainingArgs[0]
				args := ctx.RemainingArgs[1:]

				// Execute command and parse output
				records, err := ssql.ExecCommand(command, args)
				if err != nil {
					return fmt.Errorf("executing command: %w", err)
				}

				// Write as JSONL to stdout
				if err := lib.WriteJSONL(os.Stdout, records); err != nil {
					return fmt.Errorf("writing JSONL: %w", err)
				}

				return nil
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateFromCode(inputFile, format)
			}

			// Detect format and read
			var originalRecords iter.Seq[ssql.Record]
			var err error

			if inputFile == "" {
				// Reading from stdin - use -format flag or default to CSV
				switch format {
				case "json", "jsonl":
					originalRecords = lib.ReadJSON(os.Stdin)
				default: // "csv" or empty
					originalRecords = ssql.ReadCSVFromReader(os.Stdin)
				}
			} else {
				// Detect format from extension
				lower := strings.ToLower(inputFile)
				switch {
				case strings.HasSuffix(lower, ".csv"):
					originalRecords, err = ssql.ReadCSV(inputFile)
				case strings.HasSuffix(lower, ".json"), strings.HasSuffix(lower, ".jsonl"):
					file, ferr := lib.OpenInputFile(inputFile)
					if ferr != nil {
						return ferr
					}
					defer file.Close()
					originalRecords = lib.ReadJSON(file)
				default:
					// Try to auto-detect by peeking at content
					file, ferr := lib.OpenInputFile(inputFile)
					if ferr != nil {
						return ferr
					}
					defer file.Close()
					// Default to JSON/JSONL reader which auto-detects array vs lines
					originalRecords = lib.ReadJSON(file)
				}
				if err != nil {
					return fmt.Errorf("reading file: %w", err)
				}
			}

			// Cache field names for completion in pipelines
			var firstRecord *ssql.Record
			var fieldNames []string
			records := func(yield func(ssql.Record) bool) {
				for r := range originalRecords {
					if firstRecord == nil {
						firstRecord = &r
						for k := range r.All() {
							fieldNames = append(fieldNames, k)
						}
						os.Setenv("AUTOCLI_FIELDS", strings.Join(fieldNames, ","))
						if inputFile != "" {
							cleanName := strings.ReplaceAll(inputFile, ".", "_")
							cleanName = strings.ReplaceAll(cleanName, "/", "_")
							os.Setenv("AUTOCLI_FIELDS_"+cleanName, strings.Join(fieldNames, ","))
						}
					}
					if !yield(r) {
						return
					}
				}
			}

			// Write as JSONL to stdout
			if err := lib.WriteJSONL(os.Stdout, records); err != nil {
				return fmt.Errorf("writing JSONL: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateFromCode generates Go code for the from command
func generateFromCode(filename, format string) error {
	var code string
	var imports []string

	if filename == "" {
		// Reading from stdin - use format flag or default to CSV
		switch format {
		case "json", "jsonl":
			code = `records := ssql.ReadJSONFromReader(os.Stdin)`
			imports = []string{"os"}
		default:
			code = `records := ssql.ReadCSVFromReader(os.Stdin)`
			imports = []string{"os"}
		}
	} else {
		// Detect format from extension
		lower := strings.ToLower(filename)
		switch {
		case strings.HasSuffix(lower, ".csv"):
			code = fmt.Sprintf(`records, err := ssql.ReadCSV(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading CSV: %%w", err))
		os.Exit(1)
	}`, filename)
			imports = []string{"fmt", "os"}
		case strings.HasSuffix(lower, ".json"), strings.HasSuffix(lower, ".jsonl"):
			code = fmt.Sprintf(`records, err := ssql.ReadJSONAuto(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading JSON: %%w", err))
		os.Exit(1)
	}`, filename)
			imports = []string{"fmt", "os"}
		default:
			// Default to JSON/JSONL
			code = fmt.Sprintf(`records, err := ssql.ReadJSONAuto(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading file: %%w", err))
		os.Exit(1)
	}`, filename)
			imports = []string{"fmt", "os"}
		}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// generateFromExecCode generates Go code for from command with command execution
func generateFromExecCode(command string, args []string) error {
	// Build the args slice literal
	var argsCode string
	if len(args) == 0 {
		argsCode = "nil"
	} else {
		quotedArgs := make([]string, len(args))
		for i, arg := range args {
			quotedArgs[i] = fmt.Sprintf("%q", arg)
		}
		argsCode = fmt.Sprintf("[]string{%s}", strings.Join(quotedArgs, ", "))
	}

	code := fmt.Sprintf(`records, err := ssql.ExecCommand(%q, %s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("executing command: %%w", err))
		os.Exit(1)
	}`, command, argsCode)

	imports := []string{"fmt", "os"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
