package commands

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"iter"
	"os"
	"sort"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterFrom registers the from subcommand with nested format subcommands.
// Supports both explicit format: "ssql from csv data.csv"
// and extension inference: "ssql from data.csv" (bare form).
func RegisterFrom(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	fromCmd := cmd.Subcommand("from").
		Description("Read data from files or command output").
		Example("ssql from data.csv | ssql where -where age gt 18", "Read CSV (infers format from extension)").
		Example("ssql from csv data.csv -type zipcode string", "Read CSV with explicit format and type overrides")

	// Format subcommands
	registerFromCSV(fromCmd)
	registerFromTSV(fromCmd)
	registerFromJSON(fromCmd)
	registerFromJSONL(fromCmd)
	registerFromArrow(fromCmd)
	registerFromWAV(fromCmd)
	registerFromXLSX(fromCmd)

	// Operational subcommands
	registerFromCommand(fromCmd)

	// Bare "from FILE" handler — infers format from extension
	fromCmd.
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.{csv,tsv,json,jsonl,arrow,wav,xlsx}"}).
		Global().
		Default("").
		Help("Input file (format inferred from extension). Reads JSONL from stdin if not specified.").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if inputFile == "" {
				// No file — default to JSONL from stdin
				return executeFromJSON("", generate)
			}

			// Detect format from extension, delegate
			lower := strings.ToLower(inputFile)
			switch {
			case strings.HasSuffix(lower, ".csv"):
				return executeFromCSV(inputFile, nil, "auto", generate)
			case strings.HasSuffix(lower, ".tsv"):
				return executeFromTSV(inputFile, generate)
			case strings.HasSuffix(lower, ".json"), strings.HasSuffix(lower, ".jsonl"):
				return executeFromJSON(inputFile, generate)
			case strings.HasSuffix(lower, ".arrow"):
				return executeFromArrow(inputFile, generate)
			case strings.HasSuffix(lower, ".wav"):
				return executeFromWAV(inputFile, -1, generate)
			case strings.HasSuffix(lower, ".xlsx"):
				return executeFromXLSX(inputFile, "", generate)
			default:
				return executeFromJSON(inputFile, generate)
			}
		}).
		Done()

	fromCmd.Done()
	return cmd
}

// --- Format subcommands ---

func registerFromCSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("csv").
		Description("Read CSV file or stdin").
		Example("ssql from csv data.csv | ssql to table", "Read CSV file").
		Example("cat data.csv | ssql from csv | ssql to json", "Read CSV from stdin").
		Example("ssql from csv data.csv -type zipcode string -type phone string", "Force fields to string").
		Example("ssql from csv data.csv -default-type string", "Treat all fields as strings").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
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
		Completer(&cf.FileCompleter{Pattern: "*.csv"}).
		Global().
		Default("").
		Help("Input CSV file (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool
			var defaultType string
			typeOverrides := make(map[string]string)

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if dtVal, ok := ctx.GlobalFlags["-default-type"]; ok {
				defaultType = dtVal.(string)
			}
			if typeVal, ok := ctx.GlobalFlags["-type"]; ok {
				typeOverrides = parseTypeOverrides(typeVal)
			}

			return executeFromCSV(inputFile, typeOverrides, defaultType, generate)
		}).
		Done()
}

func registerFromTSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("tsv").
		Description("Read TSV file or stdin").
		Example("ssql from tsv data.tsv | ssql to table", "Read TSV file").
		Example("cat data.tsv | ssql from tsv | ssql to json", "Read TSV from stdin").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.tsv"}).
		Global().
		Default("").
		Help("Input TSV file (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			return executeFromTSV(inputFile, generate)
		}).
		Done()
}

func registerFromJSON(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("json").
		Description("Read JSON file or stdin").
		Example("ssql from json data.json | ssql to table", "Read JSON file").
		Example("curl -s api/data | ssql from json | ssql to table", "Read JSON from stdin").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.json"}).
		Global().
		Default("").
		Help("Input JSON file (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			return executeFromJSON(inputFile, generate)
		}).
		Done()
}

func registerFromJSONL(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("jsonl").
		Description("Read JSONL file or stdin").
		Example("ssql from jsonl data.jsonl | ssql to table", "Read JSONL file").
		Example("cat data.jsonl | ssql from jsonl | ssql to table", "Read JSONL from stdin").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.jsonl"}).
		Global().
		Default("").
		Help("Input JSONL file (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			return executeFromJSON(inputFile, generate)
		}).
		Done()
}

func registerFromArrow(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("arrow").
		Description("Read Arrow file or stdin").
		Example("ssql from arrow data.arrow | ssql to table", "Read Arrow file (10-20x faster than CSV)").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.arrow"}).
		Global().
		Default("").
		Help("Input Arrow file (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			return executeFromArrow(inputFile, generate)
		}).
		Done()
}

func registerFromWAV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("wav").
		Description("Read WAV audio file").
		Example("ssql from wav audio.wav | ssql fft -field amplitude", "Read WAV for FFT analysis").
		Example("ssql from wav stereo.wav -channel 0 | ssql to table", "Read left channel only").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-channel", "-ch").
		Int().
		Global().
		Default(-1).
		Help("Extract specific channel (0=left, 1=right). Default: mix to mono.").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.wav"}).
		Global().
		Default("").
		Help("Input WAV file").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool
			var channel int = -1

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if chVal, ok := ctx.GlobalFlags["-channel"]; ok {
				channel = chVal.(int)
			}

			return executeFromWAV(inputFile, channel, generate)
		}).
		Done()
}

func registerFromXLSX(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("xlsx").
		Description("Read Excel spreadsheet").
		Example("ssql from xlsx data.xlsx | ssql to table", "Read Excel spreadsheet").
		Example("ssql from xlsx workbook.xlsx -sheet Sales | ssql to csv", "Read specific sheet").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-sheet").
		String().
		Global().
		Default("").
		Help("Sheet name to read (default: first sheet)").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.xlsx"}).
		Global().
		Help("Input XLSX file (required — XLSX cannot be read from stdin)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool
			var sheet string

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if sheetVal, ok := ctx.GlobalFlags["-sheet"]; ok {
				sheet = sheetVal.(string)
			}

			if inputFile == "" {
				return fmt.Errorf("XLSX format cannot be read from stdin (it requires random file access); use a file path")
			}

			return executeFromXLSX(inputFile, sheet, generate)
		}).
		Done()
}

// --- Operational subcommands ---

func registerFromCommand(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("command").
		Description("Execute a command and read its output").
		Example("ssql from command -- ps aux | ssql where -where USER eq root", "Parse ps output").
		Example("ssql from command -- docker ps | ssql to table", "Parse docker output").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Handler(func(ctx *cf.Context) error {
			var generate bool
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if len(ctx.RemainingArgs) == 0 {
				return fmt.Errorf("usage: ssql from command -- <command> [args...]")
			}

			command := ctx.RemainingArgs[0]
			args := ctx.RemainingArgs[1:]

			if shouldGenerate(generate) {
				return generateFromExecCode(command, args)
			}

			records, err := ssql.ExecCommand(command, args)
			if err != nil {
				return fmt.Errorf("executing command: %w", err)
			}

			return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
		}).
		Done()
}

// --- Shared execution functions ---

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

// executeFromTSV handles TSV reading for both the subcommand and bare form.
func executeFromTSV(inputFile string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromTSVCode(inputFile)
	}

	var records iter.Seq[ssql.Record]
	if inputFile == "" {
		records = ssql.ReadTSVFromReader(os.Stdin)
	} else {
		file, err := os.Open(inputFile)
		if err != nil {
			return fmt.Errorf("reading TSV file: %w", err)
		}
		defer file.Close()
		records = ssql.ReadTSVFromReader(file)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// executeFromJSON handles JSON/JSONL reading for both the subcommand and bare form.
func executeFromJSON(inputFile string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromJSONCode(inputFile)
	}

	var records iter.Seq[ssql.Record]
	if inputFile == "" {
		records = readJSONSchemaAware(os.Stdin)
	} else {
		file, err := lib.OpenInputFile(inputFile)
		if err != nil {
			return err
		}
		defer file.Close()
		records = readJSONSchemaAware(file)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// executeFromArrow handles Arrow reading for both the subcommand and bare form.
func executeFromArrow(inputFile string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromArrowCode(inputFile)
	}

	var records iter.Seq[ssql.Record]
	if inputFile == "" {
		records = ssql.ReadArrowFromReader(os.Stdin)
	} else {
		var err error
		records, err = ssql.ReadArrow(inputFile)
		if err != nil {
			return fmt.Errorf("reading Arrow file: %w", err)
		}
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// executeFromWAV handles WAV reading for both the subcommand and bare form.
func executeFromWAV(inputFile string, channel int, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromWAVCode(inputFile, channel)
	}

	var records iter.Seq[ssql.Record]
	var wavMeta *ssql.WAVMetadata

	if inputFile == "" {
		var err error
		records, wavMeta, err = ssql.ReadWAVFromReader(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading WAV from stdin: %w", err)
		}
	} else {
		var err error
		if channel >= 0 {
			records, wavMeta, err = ssql.ReadWAVChannel(inputFile, channel)
		} else {
			records, wavMeta, err = ssql.ReadWAV(inputFile)
		}
		if err != nil {
			return fmt.Errorf("reading WAV file: %w", err)
		}
	}

	records = wrapWithFieldCaching(records, inputFile)
	opts := writeWithInferredSchemaOptions{}
	if wavMeta != nil {
		opts.sampleRate = wavMeta.SampleRate
	}
	return writeWithInferredSchema(records, opts)
}

// executeFromXLSX handles XLSX reading for both the subcommand and bare form.
func executeFromXLSX(inputFile string, sheet string, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromXLSXCode(inputFile, sheet)
	}

	var xlsxConfig []ssql.XLSXConfig
	if sheet != "" {
		xlsxConfig = append(xlsxConfig, ssql.XLSXConfig{SheetName: sheet})
	}

	records, err := ssql.ReadXLSX(inputFile, xlsxConfig...)
	if err != nil {
		return fmt.Errorf("reading XLSX file: %w", err)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// --- Shared helpers ---

// wrapWithFieldCaching wraps a record iterator with field name caching for
// pipeline completion support.
func wrapWithFieldCaching(originalRecords iter.Seq[ssql.Record], inputFile string) iter.Seq[ssql.Record] {
	var firstRecord *ssql.Record
	var fieldNames []string
	return func(yield func(ssql.Record) bool) {
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
}

// parseTypeOverrides extracts type override map from autocli accumulated flag value.
func parseTypeOverrides(typeVal any) map[string]string {
	typeOverrides := make(map[string]string)
	if typeSlice, ok := typeVal.([]any); ok {
		for _, item := range typeSlice {
			if argMap, ok := item.(map[string]any); ok {
				field := fmt.Sprintf("%v", argMap["field"])
				typ := fmt.Sprintf("%v", argMap["type"])
				typeOverrides[field] = typ
			}
		}
	}
	return typeOverrides
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

// readJSONSchemaAware reads JSON/JSONL input, stripping any _schema header.
// Handles JSON arrays (starts with '[') and JSONL (one object per line).
// When a _schema header is present, it is consumed and records are type-coerced.
func readJSONSchemaAware(r io.Reader) iter.Seq[ssql.Record] {
	br := bufio.NewReader(r)

	// Peek at first non-whitespace byte to detect JSON array vs JSONL
	for {
		b, err := br.Peek(1)
		if err != nil {
			return func(yield func(ssql.Record) bool) {}
		}
		if b[0] == ' ' || b[0] == '\t' || b[0] == '\n' || b[0] == '\r' {
			br.ReadByte()
			continue
		}
		if b[0] == '[' {
			// JSON array — no schema headers possible, use standard reader
			return lib.ReadJSON(br)
		}
		break
	}

	// JSONL — use schema-aware reader that strips _schema headers
	return lib.ReadJSONLWithSchema(br).Records
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

// writeWithInferredSchemaOptions configures writeWithInferredSchema behavior
type writeWithInferredSchemaOptions struct {
	fieldOrder []string
	sampleRate int // For audio data (0 means not audio)
}

// writeWithInferredSchema infers schema from first record and writes with schema header
// If fieldOrder is provided, uses that order; otherwise uses sorted field names for determinism
// Only buffers first record (O(1) memory), then streams remaining records.
func writeWithInferredSchema(records iter.Seq[ssql.Record], opts ...writeWithInferredSchemaOptions) error {
	var options writeWithInferredSchemaOptions
	if len(opts) > 0 {
		options = opts[0]
	}
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
	if len(options.fieldOrder) > 0 {
		order = options.fieldOrder
	} else {
		// Use sorted field names for deterministic output
		for k := range firstRecord.All() {
			order = append(order, k)
		}
		sort.Strings(order)
	}

	// Infer schema from first record
	schema := lib.InferFromRecordOrdered(firstRecord, order)

	// Set sample rate for audio data
	if options.sampleRate > 0 {
		schema.SampleRate = options.sampleRate
	}

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

// --- Code generation functions ---

// generateFromCSVCode generates Go code for reading CSV.
func generateFromCSVCode(filename string, typeOverrides map[string]string, defaultType string) error {
	var code string
	var imports []string

	configCode := generateCSVConfigCode(typeOverrides, defaultType)
	hasConfig := configCode != ""

	if filename == "" {
		if hasConfig {
			code = configCode + "\n\trecords := ssql.ReadCSVFromReader(os.Stdin, csvConfig)"
		} else {
			code = `records := ssql.ReadCSVFromReader(os.Stdin)`
		}
		imports = []string{"os"}
	} else {
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
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// generateFromTSVCode generates Go code for reading TSV.
func generateFromTSVCode(filename string) error {
	var code string
	var imports []string

	if filename == "" {
		code = `records := ssql.ReadTSVFromReader(os.Stdin)`
		imports = []string{"os"}
	} else {
		code = fmt.Sprintf(`records, err := ssql.ReadTSV(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading TSV: %%w", err))
		os.Exit(1)
	}`, filename)
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// generateFromJSONCode generates Go code for reading JSON/JSONL.
func generateFromJSONCode(filename string) error {
	var code string
	var imports []string

	if filename == "" {
		code = `records := ssql.ReadJSONFromReader(os.Stdin)`
		imports = []string{"os"}
	} else {
		code = fmt.Sprintf(`records, err := ssql.ReadJSONAuto(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading JSON: %%w", err))
		os.Exit(1)
	}`, filename)
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// generateFromArrowCode generates Go code for reading Arrow.
func generateFromArrowCode(filename string) error {
	var code string
	var imports []string

	if filename == "" {
		code = `records := ssql.ReadArrowFromReader(os.Stdin)`
		imports = []string{"os"}
	} else {
		code = fmt.Sprintf(`records, err := ssql.ReadArrow(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading Arrow: %%w", err))
		os.Exit(1)
	}`, filename)
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// generateFromWAVCode generates Go code for reading WAV.
func generateFromWAVCode(filename string, channel int) error {
	var code string
	var imports []string

	if filename == "" {
		code = `records, _, err := ssql.ReadWAVFromReader(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading WAV: %w", err))
		os.Exit(1)
	}`
		imports = []string{"fmt", "os"}
	} else if channel >= 0 {
		code = fmt.Sprintf(`records, _, err := ssql.ReadWAVChannel(%q, %d)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading WAV: %%w", err))
		os.Exit(1)
	}`, filename, channel)
		imports = []string{"fmt", "os"}
	} else {
		code = fmt.Sprintf(`records, _, err := ssql.ReadWAV(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading WAV: %%w", err))
		os.Exit(1)
	}`, filename)
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// generateFromXLSXCode generates Go code for reading XLSX.
func generateFromXLSXCode(filename string, sheet string) error {
	var code string

	if sheet != "" {
		code = fmt.Sprintf(`records, err := ssql.ReadXLSX(%q, ssql.XLSXConfig{SheetName: %q})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading XLSX: %%w", err))
		os.Exit(1)
	}`, filename, sheet)
	} else {
		code = fmt.Sprintf(`records, err := ssql.ReadXLSX(%q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading XLSX: %%w", err))
		os.Exit(1)
	}`, filename)
	}

	imports := []string{"fmt", "os"}
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

// --- CSV config helpers ---

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
