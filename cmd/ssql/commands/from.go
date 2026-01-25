package commands

import (
	"encoding/csv"
	"fmt"
	"iter"
	"os"
	"sort"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterFrom registers the from subcommand (SQL-style source command)
// Note: Schema headers are ALWAYS emitted. This enables strongly-typed pipelines
// and future optimizations like GPU acceleration.
func RegisterFrom(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("from").
		Description("Read data from file or command output (auto-detects CSV, TSV, JSON, JSONL, Arrow). Always emits schema header.").
		Example("ssql from data.csv | ssql where -where age gt 18", "Read CSV file (schema header included)").
		Example("ssql from data.tsv | ssql where -where age gt 18", "Read TSV file (auto-detects separator)").
		Example("ssql from data.csv -type zipcode string -type phone string", "Force fields to string (preserve leading zeros)").
		Example("ssql from data.csv -default-type string", "Treat all fields as strings (no auto-detection)").
		Example("ssql from -- ps aux | ssql where -where USER eq root", "Execute command and parse output").
		Example("ssql from data.arrow | ssql where -where age gt 18", "Read Arrow file (10-20x faster than CSV)").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-format", "-f").
		String().
		Global().
		Default("").
		Completer(&cf.StaticCompleter{Options: []string{"csv", "tsv", "json", "jsonl", "arrow"}}).
		Help("Input format for stdin: csv (default), tsv, json, jsonl, arrow").
		Done().
		Flag("-type", "-t").
		Arg("field").Completer(cf.NoCompleter{Hint: "<field-name>"}).Done().
		Arg("type").Completer(&cf.StaticCompleter{Options: []string{"string", "int", "float", "bool", "auto"}}).Done().
		Accumulate().
		Global().
		Help("Override type for field: -type zipcode string -type age int").
		Done().
		Flag("-default-type", "-dt").
		String().
		Global().
		Default("auto").
		Completer(&cf.StaticCompleter{Options: []string{"auto", "string", "int", "float", "bool"}}).
		Help("Default type for all fields: auto (default), string, int, float, bool").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.{csv,tsv,json,jsonl,arrow}"}).
		Global().
		Default("").
		Help("Input file (CSV, TSV, JSON, JSONL, or Arrow). Reads from stdin if not specified.").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var format string
			var generate bool
			var defaultType string
			typeOverrides := make(map[string]string)

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}

			if fmtVal, ok := ctx.GlobalFlags["-format"]; ok {
				format = fmtVal.(string)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if dtVal, ok := ctx.GlobalFlags["-default-type"]; ok {
				defaultType = dtVal.(string)
			}

			// Parse -type flag accumulations
			if typeVal, ok := ctx.GlobalFlags["-type"]; ok {
				if typeSlice, ok := typeVal.([]any); ok {
					for _, item := range typeSlice {
						// autocli returns named args as map[string]interface{}
						if argMap, ok := item.(map[string]interface{}); ok {
							field := fmt.Sprintf("%v", argMap["field"])
							typ := fmt.Sprintf("%v", argMap["type"])
							typeOverrides[field] = typ
						}
					}
				}
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

				// Always write with schema header
				return writeWithInferredSchema(records)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateFromCode(inputFile, format, typeOverrides, defaultType)
			}

			// Build CSV config with type overrides
			csvConfig, err := buildCSVConfig(typeOverrides, defaultType)
			if err != nil {
				return err
			}

			// Detect format and read
			var originalRecords iter.Seq[ssql.Record]
			var csvHeaders []string // Track CSV headers for schema field order

			if inputFile == "" {
				// Reading from stdin - use -format flag or default to CSV
				switch format {
				case "json", "jsonl":
					originalRecords = lib.ReadJSON(os.Stdin)
				case "arrow":
					originalRecords = ssql.ReadArrowFromReader(os.Stdin)
				case "tsv":
					originalRecords = ssql.ReadTSVFromReader(os.Stdin)
				default: // "csv" or empty
					originalRecords = ssql.ReadCSVFromReader(os.Stdin, csvConfig)
				}
			} else {
				// Detect format from extension
				lower := strings.ToLower(inputFile)
				switch {
				case strings.HasSuffix(lower, ".csv"):
					// Read CSV headers for schema field ordering
					csvHeaders, _ = readCSVHeaders(inputFile)
					originalRecords, err = ssql.ReadCSV(inputFile, csvConfig)
					if err != nil {
						return fmt.Errorf("reading file: %w", err)
					}
				case strings.HasSuffix(lower, ".tsv"):
					originalRecords, err = ssql.ReadTSV(inputFile)
					if err != nil {
						return fmt.Errorf("reading TSV file: %w", err)
					}
				case strings.HasSuffix(lower, ".json"), strings.HasSuffix(lower, ".jsonl"):
					file, ferr := lib.OpenInputFile(inputFile)
					if ferr != nil {
						return ferr
					}
					defer file.Close()
					originalRecords = lib.ReadJSON(file)
				case strings.HasSuffix(lower, ".arrow"):
					var rerr error
					originalRecords, rerr = ssql.ReadArrow(inputFile)
					if rerr != nil {
						return fmt.Errorf("reading Arrow file: %w", rerr)
					}
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

			// Always write with schema header
			return writeWithInferredSchema(records, csvHeaders)
		}).
		Done()
	return cmd
}

// buildCSVConfig creates a CSVConfig from type override parameters
func buildCSVConfig(typeOverrides map[string]string, defaultType string) (ssql.CSVConfig, error) {
	config := ssql.DefaultCSVConfig()

	// Parse default type
	if defaultType != "" && defaultType != "auto" {
		dt, err := ssql.ParseFieldType(defaultType)
		if err != nil {
			return config, err
		}
		config.DefaultType = dt
	}

	// Parse type overrides
	if len(typeOverrides) > 0 {
		config.TypeOverrides = make(map[string]ssql.FieldType)
		for field, typeName := range typeOverrides {
			ft, err := ssql.ParseFieldType(typeName)
			if err != nil {
				return config, fmt.Errorf("field %q: %w", field, err)
			}
			config.TypeOverrides[field] = ft
		}
	}

	return config, nil
}

// generateFromCode generates Go code for the from command
func generateFromCode(filename, format string, typeOverrides map[string]string, defaultType string) error {
	var code string
	var imports []string

	// Generate config code if we have type overrides
	configCode := generateCSVConfigCode(typeOverrides, defaultType)
	hasConfig := configCode != ""

	if filename == "" {
		// Reading from stdin - use format flag or default to CSV
		switch format {
		case "json", "jsonl":
			code = `records := ssql.ReadJSONFromReader(os.Stdin)`
			imports = []string{"os"}
		case "arrow":
			code = `records := ssql.ReadArrowFromReader(os.Stdin)`
			imports = []string{"os"}
		case "tsv":
			code = `records := ssql.ReadTSVFromReader(os.Stdin)`
			imports = []string{"os"}
		default:
			if hasConfig {
				code = configCode + "\n\trecords := ssql.ReadCSVFromReader(os.Stdin, csvConfig)"
			} else {
				code = `records := ssql.ReadCSVFromReader(os.Stdin)`
			}
			imports = []string{"os"}
		}
	} else {
		// Detect format from extension
		lower := strings.ToLower(filename)
		switch {
		case strings.HasSuffix(lower, ".csv"):
			if hasConfig {
				code = fmt.Sprintf(`%s
	records, err := ssql.ReadCSV(%q, csvConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading CSV: %%w", err))
		os.Exit(1)
	}`, configCode, filename)
			} else {
				code = fmt.Sprintf(`records, err := ssql.ReadCSV(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading CSV: %%w", err))
		os.Exit(1)
	}`, filename)
			}
			imports = []string{"fmt", "os"}
		case strings.HasSuffix(lower, ".tsv"):
			code = fmt.Sprintf(`records, err := ssql.ReadTSV(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading TSV: %%w", err))
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
		case strings.HasSuffix(lower, ".arrow"):
			code = fmt.Sprintf(`records, err := ssql.ReadArrow(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading Arrow: %%w", err))
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

// generateCSVConfigCode generates Go code for CSV config with type overrides
func generateCSVConfigCode(typeOverrides map[string]string, defaultType string) string {
	// No config needed if using defaults
	if len(typeOverrides) == 0 && (defaultType == "" || defaultType == "auto") {
		return ""
	}

	var parts []string
	parts = append(parts, "csvConfig := ssql.CSVConfig{")
	parts = append(parts, "\t\tHasHeaders: true,")
	parts = append(parts, "\t\tDelimiter:  ',',")
	parts = append(parts, "\t\tComment:    '#',")

	// Default type
	if defaultType != "" && defaultType != "auto" {
		parts = append(parts, fmt.Sprintf("\t\tDefaultType: ssql.FieldType%s,", capitalizeFieldType(defaultType)))
	}

	// Type overrides
	if len(typeOverrides) > 0 {
		parts = append(parts, "\t\tTypeOverrides: map[string]ssql.FieldType{")
		for field, typeName := range typeOverrides {
			parts = append(parts, fmt.Sprintf("\t\t\t%q: ssql.FieldType%s,", field, capitalizeFieldType(typeName)))
		}
		parts = append(parts, "\t\t},")
	}

	parts = append(parts, "\t}")

	return strings.Join(parts, "\n\t")
}

// capitalizeFieldType converts "string" to "String", "int" to "Int", etc.
func capitalizeFieldType(typeName string) string {
	switch strings.ToLower(typeName) {
	case "string", "str", "text":
		return "String"
	case "int", "integer", "int64":
		return "Int"
	case "float", "float64", "double", "number":
		return "Float"
	case "bool", "boolean":
		return "Bool"
	default:
		return "Auto"
	}
}

// readCSVHeaders reads just the header row from a CSV file
func readCSVHeaders(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	headers, err := reader.Read()
	if err != nil {
		return nil, err
	}
	return headers, nil
}

// writeWithInferredSchema infers schema from first record and writes with schema header
// If fieldOrder is provided, uses that order; otherwise uses sorted field names for determinism
// Only buffers first record (O(1) memory), then streams remaining records.
func writeWithInferredSchema(records iter.Seq[ssql.Record], fieldOrder ...[]string) error {
	// Use pull-style iteration to peek at first record
	next, stop := iter.Pull(records)
	defer stop()

	// Get first record to infer schema
	firstRecord, ok := next()
	if !ok {
		// No records - nothing to write
		return nil
	}

	// Determine field order
	var order []string
	if len(fieldOrder) > 0 && len(fieldOrder[0]) > 0 {
		order = fieldOrder[0]
	} else {
		// Use sorted field names for deterministic output
		for k := range firstRecord.All() {
			order = append(order, k)
		}
		sort.Strings(order)
	}

	// Infer schema from first record
	schema := lib.InferFromRecordOrdered(firstRecord, order)

	// Create streaming iterator: first record + remaining records
	allRecords := func(yield func(ssql.Record) bool) {
		// Yield first record
		if !yield(firstRecord) {
			return
		}
		// Stream remaining records
		for {
			r, ok := next()
			if !ok {
				return
			}
			if !yield(r) {
				return
			}
		}
	}

	// Write with schema header and ordered fields (streams without buffering)
	return lib.WriteJSONLWithSchemaOrdered(os.Stdout, schema, allRecords)
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
