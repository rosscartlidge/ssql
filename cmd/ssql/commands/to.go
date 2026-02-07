package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterTo registers the to subcommand with nested format subcommands
func RegisterTo(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	toCmd := cmd.Subcommand("to").
		Description("Write output in various formats (table, csv, tsv, json, chart, wav)")

	// Register nested subcommands
	registerToTable(toCmd)
	registerToCSV(toCmd)
	registerToTSV(toCmd)
	registerToJSON(toCmd)
	registerToArrow(toCmd)
	registerToWAV(toCmd)
	registerToChart(toCmd)

	toCmd.Done()
	return cmd
}

// registerToTable registers the "to table" subcommand
func registerToTable(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("table").
		Description("Display as formatted table").
		Example("ssql from data.csv | ssql to table", "Display CSV as formatted table").
		Example("ssql from data.csv | ssql to table name age city", "Display with name, age, city first, then other fields").
		Example("ssql from data.csv | ssql to table -only name age", "Display only name and age columns").
		Example("ssql from data.csv | ssql to table -max-width 30", "Display with custom column width").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-max-width").
		Int().
		Global().
		Default(50).
		Help("Maximum column width (truncate longer values)").
		Done().
		Flag("-only").
		Bool().
		Global().
		Help("Only show specified fields (hide others)").
		Done().
		Flag("FIELDS").
		String().
		Variadic().
		FieldsFromFlag("").
		Global().
		Help("Field names to display first (in order)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var generate bool
			var maxWidth int
			var onlySpecified bool
			var fields []string

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if widthVal, ok := ctx.GlobalFlags["-max-width"]; ok {
				maxWidth = widthVal.(int)
			}

			if onlyVal, ok := ctx.GlobalFlags["-only"]; ok {
				onlySpecified = onlyVal.(bool)
			}

			if fieldsVal, ok := ctx.GlobalFlags["FIELDS"]; ok {
				if fieldsSlice, ok := fieldsVal.([]any); ok {
					for _, f := range fieldsSlice {
						if s, ok := f.(string); ok && s != "" {
							fields = append(fields, s)
						}
					}
				}
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToTableCode(maxWidth, fields, onlySpecified)
			}

			// Read all records from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// If no fields specified but schema is present, use schema field order
			if len(fields) == 0 && schemaAndRecords.Schema != nil {
				fields = schemaAndRecords.Schema.Fields
			}

			ssql.DisplayTableWithFields(records, maxWidth, fields, onlySpecified)
			return nil
		}).
		Done()
}

// registerToCSV registers the "to csv" subcommand
func registerToCSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("csv").
		Description("Write as CSV file").
		Example("ssql from data.json | ssql to csv output.csv", "Convert JSON to CSV").
		Example("ssql from data.csv | ssql where -where status eq active | ssql to csv active.csv", "Filter and save to CSV").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.csv"}).
		Global().
		Default("").
		Help("Output CSV file (or stdout if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToCSVCode(outputFile)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Build CSV config with field order from schema if present
			config := ssql.DefaultCSVConfig()
			if schemaAndRecords.Schema != nil {
				config.Fields = schemaAndRecords.Schema.Fields
			}

			// Write as CSV with field order
			if outputFile == "" {
				return ssql.WriteCSVToWriter(records, os.Stdout, config)
			} else {
				return ssql.WriteCSV(records, outputFile, config)
			}
		}).
		Done()
}

// registerToTSV registers the "to tsv" subcommand
func registerToTSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("tsv").
		Description("Write as TSV file (simpler and faster than CSV)").
		Example("ssql from data.json | ssql to tsv output.tsv", "Convert JSON to TSV").
		Example("ssql from data.csv | ssql to tsv -separator '|' output.psv", "Use pipe separator").
		Example("ssql from data.csv | ssql to tsv", "Write TSV to stdout").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-separator", "-s").
		String().
		Global().
		Default("\t").
		Completer(&cf.StaticCompleter{Options: []string{"\\t", "|", ";", ","}}). // Common separators
		Help("Field separator character (default: tab)").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.tsv"}).
		Global().
		Default("").
		Help("Output TSV file (or stdout if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var separator string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if sepVal, ok := ctx.GlobalFlags["-separator"]; ok {
				separator = sepVal.(string)
			}
			if separator == "" || separator == "\\t" {
				separator = "\t"
			}
			sep := rune(separator[0])

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToTSVCode(outputFile, sep)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Write as TSV
			if outputFile == "" {
				return ssql.WriteTSVToWriterWithSeparator(records, os.Stdout, sep)
			} else {
				return ssql.WriteTSVWithSeparator(records, outputFile, sep)
			}
		}).
		Done()
}

// registerToJSON registers the "to json" subcommand
func registerToJSON(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("json").
		Description("Write as JSONL (default) or pretty JSON array (-pretty)").
		Example("ssql from data.csv | ssql to json", "Convert CSV to JSONL").
		Example("ssql from data.csv | ssql to json -pretty > output.json", "Convert CSV to pretty JSON array").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-pretty", "-p").
		Bool().
		Global().
		Help("Pretty-print as JSON array (default: JSONL)").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.{json,jsonl}"}).
		Global().
		Default("").
		Help("Output JSON/JSONL file (or stdout if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var pretty bool
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if prettyVal, ok := ctx.GlobalFlags["-pretty"]; ok {
				pretty = prettyVal.(bool)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToJSONCode(outputFile, pretty)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Get field order from schema if present
			var fieldOrder []string
			if schemaAndRecords.Schema != nil {
				fieldOrder = schemaAndRecords.Schema.Fields
			}

			// Write to stdout or file
			if outputFile == "" {
				return lib.WriteJSONWithFieldOrder(os.Stdout, records, pretty, fieldOrder)
			} else {
				output, err := lib.OpenOutput(outputFile)
				if err != nil {
					return err
				}
				defer output.Close()
				return lib.WriteJSONWithFieldOrder(output, records, pretty, fieldOrder)
			}
		}).
		Done()
}

// registerToArrow registers the "to arrow" subcommand
func registerToArrow(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("arrow").
		Description("Write as Apache Arrow IPC file (10-20x faster I/O)").
		Example("ssql from data.csv | ssql to arrow output.arrow", "Convert CSV to Arrow for faster subsequent reads").
		Example("ssql from large.json | ssql to arrow data.arrow", "Convert JSON to Arrow format").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.arrow"}).
		Global().
		Required().
		Help("Output Arrow file (required)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if outputFile == "" {
				return fmt.Errorf("output file required")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToArrowCode(outputFile)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Write as Arrow
			if err := ssql.WriteArrow(records, outputFile); err != nil {
				return fmt.Errorf("writing Arrow file: %w", err)
			}

			return nil
		}).
		Done()
}

// registerToWAV registers the "to wav" subcommand
func registerToWAV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("wav").
		Description("Write as WAV audio file (expects amplitude field)").
		Example("ssql from audio.wav | ssql to wav output.wav", "Copy WAV file (round-trip)").
		Example("ssql from audio.wav | ssql to wav -rate 22050 output.wav", "Resample to 22050 Hz").
		Example("ssql from signal.jsonl | ssql to wav -rate 44100 audio.wav", "Convert signal data to audio").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-rate", "-r").
		Int().
		Global().
		Default(0).
		Help("Sample rate in Hz (default: from schema header, or 44100 if not specified)").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.wav"}).
		Global().
		Required().
		Help("Output WAV file (required)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var sampleRate int
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if rateVal, ok := ctx.GlobalFlags["-rate"]; ok {
				sampleRate = rateVal.(int)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if outputFile == "" {
				return fmt.Errorf("output file required")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToWAVCode(outputFile, sampleRate)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Determine sample rate: flag > schema > default
			if sampleRate == 0 && schemaAndRecords.Schema != nil {
				sampleRate = schemaAndRecords.Schema.SampleRate
			}
			if sampleRate == 0 {
				sampleRate = 44100 // Default
			}

			// Write as WAV
			if err := ssql.WriteWAV(records, outputFile, sampleRate); err != nil {
				return fmt.Errorf("writing WAV file: %w", err)
			}

			return nil
		}).
		Done()
}

// registerToChart registers the "to chart" subcommand
func registerToChart(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("chart").
		Description("Create interactive HTML chart").
		Example("ssql from data.csv | ssql to chart -x date -y revenue", "Create line chart of revenue over time").
		Example("ssql from sales.csv | ssql to chart -x product -y sales -output sales.html", "Create chart with custom output file").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-x").
		String().
		FieldsFromFlag("").
		Global().
		Help("X-axis field").
		Done().
		Flag("-y").
		String().
		FieldsFromFlag("").
		Global().
		Help("Y-axis field").
		Done().
		Flag("-output", "-o").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.html"}).
		Global().
		Default("chart.html").
		Help("Output HTML file (default: chart.html)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var xField, yField, outputFile string
			var generate bool

			if xVal, ok := ctx.GlobalFlags["-x"]; ok {
				xField = xVal.(string)
			}
			if yVal, ok := ctx.GlobalFlags["-y"]; ok {
				yField = yVal.(string)
			}
			if outVal, ok := ctx.GlobalFlags["-output"]; ok {
				outputFile = outVal.(string)
			} else {
				outputFile = "chart.html"
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToChartCode(xField, yField, outputFile)
			}

			// Validate required fields
			if xField == "" {
				return fmt.Errorf("X-axis field required (use -x)")
			}
			if yField == "" {
				return fmt.Errorf("Y-axis field required (use -y)")
			}

			// Read JSONL from stdin (with schema if present - consumes schema header)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Create chart
			if err := ssql.QuickChart(records, xField, yField, outputFile); err != nil {
				return fmt.Errorf("creating chart: %w", err)
			}

			fmt.Printf("Chart created: %s\n", outputFile)
			return nil
		}).
		Done()
}

// Code generation functions

func generateToTableCode(maxWidth int, fields []string, onlySpecified bool) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	var code string
	if len(fields) > 0 {
		// Generate code with field ordering
		quotedFields := make([]string, len(fields))
		for i, f := range fields {
			quotedFields[i] = fmt.Sprintf("%q", f)
		}
		fieldsStr := "[]string{" + joinStrings(quotedFields, ", ") + "}"
		code = fmt.Sprintf("ssql.DisplayTableWithFields(%s, %d, %s, %t)", inputVar, maxWidth, fieldsStr, onlySpecified)
	} else {
		// No fields specified - use simple DisplayTable
		code = fmt.Sprintf("ssql.DisplayTable(%s, %d)", inputVar, maxWidth)
	}
	frag := lib.NewFinalFragment(inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// joinStrings joins strings with a separator (avoiding strings import for simple case)
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for _, s := range strs[1:] {
		result += sep + s
	}
	return result
}

func generateToCSVCode(filename string) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	var code string
	var imports []string
	if filename == "" {
		code = fmt.Sprintf(`ssql.WriteCSVToWriter(%s, os.Stdout)`, inputVar)
		imports = append(imports, "os")
	} else {
		code = fmt.Sprintf(`ssql.WriteCSV(%s, %q)`, inputVar, filename)
	}

	frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

func generateToJSONCode(filename string, pretty bool) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	var code string
	if filename == "" {
		// Write to stdout
		if pretty {
			code = fmt.Sprintf(`	// Collect and pretty-print records to stdout
	var recordMaps []map[string]interface{}
	for record := range %s {
		data := make(map[string]interface{})
		for k, v := range record.All() {
			data[k] = v
		}
		recordMaps = append(recordMaps, data)
	}
	jsonBytes, err := json.MarshalIndent(recordMaps, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %%v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(jsonBytes); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write([]byte("\n"))`, inputVar)
		} else {
			code = fmt.Sprintf(`	if err := ssql.WriteJSONFastToWriter(%s, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
		}
	} else {
		// Write to file
		if pretty {
			code = fmt.Sprintf(`	if err := ssql.WriteJSONPretty(%s, %q); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}`, inputVar, filename)
		} else {
			code = fmt.Sprintf(`	if err := ssql.WriteJSON(%s, %q); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON: %%v\n", err)
		os.Exit(1)
	}`, inputVar, filename)
		}
	}

	imports := []string{"fmt", "os"}
	if filename == "" && pretty {
		imports = append(imports, "encoding/json")
	}
	frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

func generateToArrowCode(filename string) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	code := fmt.Sprintf(`if err := ssql.WriteArrow(%s, %q); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing Arrow: %%v\n", err)
		os.Exit(1)
	}`, inputVar, filename)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt", "os"}, getCommandString())
	return lib.WriteCodeFragment(frag)
}

func generateToWAVCode(filename string, sampleRate int) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	// Use provided sample rate or default to 44100
	if sampleRate == 0 {
		sampleRate = 44100
	}

	code := fmt.Sprintf(`if err := ssql.WriteWAV(%s, %q, %d); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing WAV: %%v\n", err)
		os.Exit(1)
	}`, inputVar, filename, sampleRate)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt", "os"}, getCommandString())
	return lib.WriteCodeFragment(frag)
}

func generateToTSVCode(filename string, sep rune) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	var code string
	var imports []string
	if filename == "" {
		code = fmt.Sprintf(`ssql.WriteTSVToWriterWithSeparator(%s, os.Stdout, %q)`, inputVar, sep)
		imports = append(imports, "os")
	} else {
		code = fmt.Sprintf(`ssql.WriteTSVWithSeparator(%s, %q, %q)`, inputVar, filename, sep)
	}

	frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

func generateToChartCode(xField, yField, outputFile string) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	if xField == "" {
		return fmt.Errorf("X-axis field required (use -x)")
	}
	if yField == "" {
		return fmt.Errorf("Y-axis field required (use -y)")
	}

	if outputFile == "" {
		outputFile = "chart.html"
	}

	code := fmt.Sprintf(`if err := ssql.QuickChart(%s, %q, %q, %q); err != nil {
		return fmt.Errorf("creating chart: %%w", err)
	}
	fmt.Printf("Chart created: %%s\n", %q)`, inputVar, xField, yField, outputFile, outputFile)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt"}, getCommandString())
	return lib.WriteCodeFragment(frag)
}
