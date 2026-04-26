package commands

import (
	"encoding/csv"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func registerFromCSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("csv").
		Description("Read CSV file(s) or stdin").
		Example("ssql from csv data.csv | ssql to table", "Read CSV file").
		Example("ssql from csv *.csv | ssql to table", "Read multiple CSV files").
		Example("ssql from csv *.csv -merge-schemas | ssql to table", "Merge files with different headers").
		Example("ssql from csv data.csv -type zipcode string -type phone string", "Force fields to string").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-merge-schemas").
			Bool().
			Global().
			Help("Allow files with different headers (merge schemas)").
			Done().

		Flag("-source").
			String().
			Global().
			Default("").
			Help("Add field with source filename: -source file").
			Done().

		Flag("-unordered").
			Bool().
			Global().
			Help("Don't preserve file order in pushdown (faster, lower memory)").
			Done().

		Flag("-type", "-t").
			Arg("field").
				Completer(cf.NoCompleter{Hint: "<field-name>"}).
				Done().
			Arg("type").
				Completer(&cf.StaticCompleter{Options: []string{"string", "int", "float", "bool", "auto"}}).
				Done().
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
			Variadic().
			Completer(&cf.FileCompleter{Pattern: "*.csv"}).
			Global().
			Default("").
			Help("Input CSV file(s) (or stdin if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			var files []string
			var generate, mergeSchemas bool
			var defaultType, sourceField string
			typeOverrides := make(map[string]string)

			if filesVal, ok := ctx.GlobalFlags["FILE"]; ok {
				switch v := filesVal.(type) {
				case []string:
					files = v
				case []any:
					for _, item := range v {
						if s, ok := item.(string); ok {
							files = append(files, s)
						}
					}
				case string:
					if v != "" {
						files = []string{v}
					}
				}
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if msVal, ok := ctx.GlobalFlags["-merge-schemas"]; ok {
				mergeSchemas = msVal.(bool)
			}
			if srcVal, ok := ctx.GlobalFlags["-source"]; ok {
				sourceField = srcVal.(string)
			}
			if dtVal, ok := ctx.GlobalFlags["-default-type"]; ok {
				defaultType = dtVal.(string)
			}
			if typeVal, ok := ctx.GlobalFlags["-type"]; ok {
				typeOverrides = parseTypeOverrides(typeVal)
			}

			var unordered bool
			if uVal, ok := ctx.GlobalFlags["-unordered"]; ok {
				unordered = uVal.(bool)
			}

			// Pushdown: ssql from csv *.csv -- where -if age gt 25
			if len(ctx.RemainingArgs) > 0 {
				if len(files) == 0 {
					return fmt.Errorf("pushdown (--) requires at least one file")
				}
				return executeFromMultiFilePushdown(files, "csv", sourceField, unordered, ctx.RemainingArgs)
			}
			_ = unordered // only used with pushdown

			if len(files) <= 1 {
				inputFile := ""
				if len(files) == 1 {
					inputFile = files[0]
				}
				return executeFromCSV(inputFile, typeOverrides, defaultType, generate)
			}

			return executeFromMultiCSV(files, typeOverrides, defaultType, mergeSchemas, sourceField, generate)
		}).
		Done()
}

// executeFromCSV handles CSV reading for both the subcommand and bare form.
func executeFromCSV(inputFile string, typeOverrides map[string]string, defaultType string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromCSVCode(inputFile, typeOverrides, defaultType)
	}

	csvConfig, err := buildCSVConfig(typeOverrides, defaultType)
	if err != nil {
		return err
	}

	var records iter.Seq[ssql.Record]
	var csvHeaders []string

	if inputFile == "" {
		records = ssql.ReadCSVFromReader(os.Stdin, csvConfig)
	} else {
		file, ferr := os.Open(inputFile)
		if ferr != nil {
			return fmt.Errorf("reading file: %w", ferr)
		}
		defer file.Close()
		if _, serr := file.Seek(0, 0); serr == nil {
			csvHeaders, _ = readCSVHeadersFromReader(file)
			file.Seek(0, 0)
		}
		records = ssql.ReadCSVFromReader(file, csvConfig)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{fieldOrder: csvHeaders})
}

// executeFromMultiCSV reads multiple CSV files and outputs merged JSONL.
func executeFromMultiCSV(files []string, typeOverrides map[string]string, defaultType string, mergeSchemas bool, sourceField string, generate bool) error {
	csvConfig, err := buildCSVConfig(typeOverrides, defaultType)
	if err != nil {
		return err
	}

	cfg := multiFileConfig{
		files:        files,
		mergeSchemas: mergeSchemas,
		sourceField:  sourceField,
		generate:     generate,
	}

	readFile := func(file *os.File) iter.Seq[ssql.Record] {
		return ssql.ReadCSVFromReader(file, csvConfig)
	}

	readHeaders := func(filename string) ([]string, error) {
		file, err := os.Open(filename)
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", filename, err)
		}
		defer file.Close()
		return readCSVHeadersFromReader(file)
	}

	return executeFromMultiFile(cfg, "CSV", readFile, readHeaders)
}

// mergeCSVHeaders checks or merges headers from multiple files.
func mergeCSVHeaders(allHeaders [][]string, files []string, merge bool) ([]string, error) {
	if len(allHeaders) == 0 {
		return nil, fmt.Errorf("no files specified")
	}

	first := allHeaders[0]

	if !merge {
		// Require identical headers
		firstSet := make(map[string]bool, len(first))
		for _, h := range first {
			firstSet[h] = true
		}
		for i := 1; i < len(allHeaders); i++ {
			if len(allHeaders[i]) != len(first) {
				return nil, fmt.Errorf("schema mismatch: %s has %d fields, %s has %d — use -merge-schemas to combine",
					files[0], len(first), files[i], len(allHeaders[i]))
			}
			for j, h := range allHeaders[i] {
				if h != first[j] {
					return nil, fmt.Errorf("schema mismatch: field %d is %q in %s but %q in %s — use -merge-schemas to combine",
						j+1, first[j], files[0], h, files[i])
				}
			}
		}
		return first, nil
	}

	// Merge: union of all fields, preserving order from first file
	seen := make(map[string]bool)
	var merged []string
	for _, headers := range allHeaders {
		for _, h := range headers {
			if !seen[h] {
				seen[h] = true
				merged = append(merged, h)
			}
		}
	}
	return merged, nil
}

// generateFromCSVCode generates Go code for reading CSV.
func generateFromCSVCode(filename string, typeOverrides map[string]string, defaultType string) error {
	if typedMode() {
		return generateFromCSVCodeTyped(filename, typeOverrides, defaultType)
	}

	var code string
	var imports []string

	configCode := generateCSVConfigCode(typeOverrides, defaultType)
	hasConfig := configCode != ""

	var params []lib.CodeParam

	if filename == "" {
		if hasConfig {
			code = configCode + "\n\trecords := ssql.ReadCSVFromReader(os.Stdin, csvConfig)"
		} else {
			code = `records := ssql.ReadCSVFromReader(os.Stdin)`
		}
		imports = []string{"os"}
	} else {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input CSV file", VarName: "flagInput"})
		if hasConfig {
			code = configCode + `
	records, err := ssql.ReadCSV(*flagInput, csvConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading CSV: %w", err))
		os.Exit(1)
	}`
		} else {
			code = `records, err := ssql.ReadCSV(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading CSV: %w", err))
		os.Exit(1)
	}`
		}
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// generateFromCSVCodeTyped emits a Phase-2 typed-mode init fragment.
// Samples the CSV header + 1000 rows to infer per-column Go types,
// emits the corresponding struct definition, and produces a
// typed.ReadCSV[GeneratedType] call.
func generateFromCSVCodeTyped(filename string, typeOverrides map[string]string, defaultType string) error {
	if filename == "" {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: 'from' from stdin not supported in typed mode (need a file to sample for schema inference)"))
	}
	// User-supplied type overrides aren't honored in typed mode for v1
	// — the inferred types from sampling are authoritative. "auto" is
	// the flag's default value (no user override).
	if len(typeOverrides) > 0 || (defaultType != "" && defaultType != "auto") {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: -as / -default-type column overrides not supported in typed mode (schema is inferred from CSV samples)"))
	}

	schema, structDef, err := lib.SampleCSVSchema(filename, "", 0)
	if err != nil {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: %w", err))
	}

	code := fmt.Sprintf(`records := typed.ReadCSV[%s](*flagInput)`, schema.TypeName)
	params := []lib.CodeParam{{
		Name:    "input",
		Default: filename,
		Help:    "input CSV file",
		VarName: "flagInput",
	}}
	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if needsTimeImport(schema) {
		imports = append(imports, "time")
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	frag.OutputTypedSchema = schema
	frag.StructDefs = []string{structDef}
	return lib.WriteCodeFragment(frag)
}

// needsTimeImport reports whether any field in the schema uses the
// time.Time type (or pointer to it). Used to decide whether to include
// "time" in the fragment's import list.
func needsTimeImport(s *lib.TypedSchema) bool {
	for _, f := range s.Fields {
		if f.GoType == "time.Time" || f.GoType == "*time.Time" {
			return true
		}
	}
	return false
}

// generateFromMultiCSVCode generates Go code for multi-file CSV reading.
func generateFromMultiCSVCode(files []string, typeOverrides map[string]string, defaultType string, sourceField string) error {
	// For now, generate code that reads files sequentially
	// TODO: implement proper multi-file code generation
	return fmt.Errorf("code generation for multi-file CSV not yet implemented — use single file with -generate")
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

// readCSVHeadersFromReader reads just the header row from a reader
func readCSVHeadersFromReader(r io.Reader) ([]string, error) {
	reader := csv.NewReader(r)
	headers, err := reader.Read()
	if err != nil {
		return nil, err
	}
	return headers, nil
}
