package commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/wasm"
)

// RegisterTo registers the to subcommand with nested format subcommands
func RegisterTo(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	toCmd := cmd.Subcommand("to").
		Description("Write output in various formats (table, csv, tsv, json, xlsx, chart, animate, explore, wav)")

	// Register nested subcommands
	registerToTable(toCmd)
	registerToCSV(toCmd)
	registerToTSV(toCmd)
	registerToJSON(toCmd)
	registerToArrow(toCmd)
	registerToWAV(toCmd)
	registerToXLSX(toCmd)
	registerToChart(toCmd)
	registerToAnimate(toCmd)
	registerToExplore(toCmd)

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
		Example("ssql from huge.csv | ssql to table --sample 50", "Stream output, infer widths from first 50 records").
		Example("ssql from data.csv | ssql to table --sample 0", "Materialize all records for perfect column widths").
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
		Flag("-sample").
		Int().
		Global().
		Default(100).
		Help("Records to sample for column widths (0 = materialize all)").
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
			var sampleSize int
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

			if sampleVal, ok := ctx.GlobalFlags["-sample"]; ok {
				sampleSize = sampleVal.(int)
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

			if sampleSize > 0 {
				ssql.DisplayTableStreaming(records, maxWidth, sampleSize, fields, onlySpecified)
			} else {
				ssql.DisplayTableWithFields(records, maxWidth, fields, onlySpecified)
			}
			return nil
		}).
		Done()
}

// registerToCSV registers the "to csv" subcommand
func registerToCSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("csv").
		Description("Write as CSV file").
		Example("ssql from data.json | ssql to csv output.csv", "Convert JSON to CSV").
		Example("ssql from data.csv | ssql where -if status eq active | ssql to csv active.csv", "Filter and save to CSV").
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

// registerToXLSX registers the "to xlsx" subcommand
func registerToXLSX(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("xlsx").
		Description("Write as Excel XLSX file").
		Example("ssql from data.csv | ssql to xlsx output.xlsx", "Convert CSV to Excel").
		Example("ssql from data.json | ssql to xlsx -sheet Sales output.xlsx", "Convert JSON to Excel with custom sheet name").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-sheet").
		String().
		Global().
		Default("").
		Help("Sheet name (default: Sheet1)").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.xlsx"}).
		Global().
		Required().
		Help("Output XLSX file (required)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var sheet string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if sheetVal, ok := ctx.GlobalFlags["-sheet"]; ok {
				sheet = sheetVal.(string)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if outputFile == "" {
				return fmt.Errorf("output file required")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToXLSXCode(outputFile, sheet)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Build XLSX config
			var xlsxConfig []ssql.XLSXConfig
			if sheet != "" {
				xlsxConfig = append(xlsxConfig, ssql.XLSXConfig{SheetName: sheet})
			}

			// Write as XLSX
			if err := ssql.WriteXLSX(records, outputFile, xlsxConfig...); err != nil {
				return fmt.Errorf("writing XLSX file: %w", err)
			}

			return nil
		}).
		Done()
}

// registerToChart registers the "to chart" subcommand
func registerToChart(cmd *cf.SubcommandBuilder) {
	chartTypes := []string{"line", "bar", "scatter", "pie", "doughnut", "radar", "heatmap"}
	colorScales := []string{"viridis", "plasma", "inferno", "magma", "cividis", "turbo"}

	cmd.Subcommand("chart").
		Description("Create interactive HTML chart").
		Example("ssql from data.csv | ssql to chart -x date -y revenue", "Create line chart of revenue over time").
		Example("ssql from sales.csv | ssql to chart -x product -y sales -y profit", "Create multi-series chart with sales and profit").
		Example("ssql from spectro.jsonl | ssql to chart -x time -y frequency -z magnitude -type heatmap", "Create heatmap from spectrogram data").
		Example("ssql from data.csv | ssql to chart -x frequency -y magnitude -log-x", "Create chart with logarithmic X-axis").
		Example("ssql from customers.csv | ssql to chart -x age -y spend -color region -type scatter", "Scatter plot with points colored by region").
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
		Accumulate().
		Help("Y-axis field (can specify multiple times for multi-series)").
		Done().
		Flag("-z").
		String().
		FieldsFromFlag("").
		Global().
		Help("Z-axis field (for heatmap color values)").
		Done().
		Flag("-type", "-t").
		String().
		Completer(&cf.StaticCompleter{Options: chartTypes}).
		Global().
		Default("line").
		Help("Chart type: line, bar, scatter, pie, doughnut, radar, heatmap").
		Done().
		Flag("-log-x").
		Bool().
		Global().
		Help("Use logarithmic scale for X-axis").
		Done().
		Flag("-log-y").
		Bool().
		Global().
		Help("Use logarithmic scale for Y-axis").
		Done().
		Flag("-color").
		String().
		FieldsFromFlag("").
		Global().
		Help("Color-by field for scatter plots (categorical coloring)").
		Done().
		Flag("-colorscale").
		String().
		Completer(&cf.StaticCompleter{Options: colorScales}).
		Global().
		Default("viridis").
		Help("Color scale for heatmaps: viridis, plasma, inferno, magma, cividis, turbo").
		Done().
		Flag("-zmin").
		String().
		Global().
		Default("").
		Help("Minimum value for color scale (heatmap only, empty = auto)").
		Done().
		Flag("-zmax").
		String().
		Global().
		Default("").
		Help("Maximum value for color scale (heatmap only, empty = auto)").
		Done().
		Flag("-log-freq").
		Bool().
		Global().
		Help("Use logarithmic Y-axis for spectrograms (heatmap only)").
		Done().
		Flag("-output", "-o").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.html"}).
		Global().
		Default("chart.html").
		Help("Output HTML file (default: chart.html)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var xField, zField, colorField, colorScale, chartType, outputFile string
			var zMinStr, zMaxStr string
			var yFields []string
			var generate, logX, logY, logFreq bool

			if xVal, ok := ctx.GlobalFlags["-x"]; ok {
				xField = xVal.(string)
			}
			// Handle accumulated -y flags
			if yVal, ok := ctx.GlobalFlags["-y"]; ok {
				switch v := yVal.(type) {
				case []any:
					for _, y := range v {
						if s, ok := y.(string); ok && s != "" {
							yFields = append(yFields, s)
						}
					}
				case string:
					if v != "" {
						yFields = append(yFields, v)
					}
				}
			}
			if zVal, ok := ctx.GlobalFlags["-z"]; ok {
				zField = zVal.(string)
			}
			if typeVal, ok := ctx.GlobalFlags["-type"]; ok {
				chartType = typeVal.(string)
			} else {
				chartType = "line"
			}
			if logXVal, ok := ctx.GlobalFlags["-log-x"]; ok {
				logX = logXVal.(bool)
			}
			if logYVal, ok := ctx.GlobalFlags["-log-y"]; ok {
				logY = logYVal.(bool)
			}
			if colorVal, ok := ctx.GlobalFlags["-color"]; ok {
				colorField = colorVal.(string)
			}
			if csVal, ok := ctx.GlobalFlags["-colorscale"]; ok {
				colorScale = csVal.(string)
			} else {
				colorScale = "viridis"
			}
			if zMinVal, ok := ctx.GlobalFlags["-zmin"]; ok {
				zMinStr = zMinVal.(string)
			}
			if zMaxVal, ok := ctx.GlobalFlags["-zmax"]; ok {
				zMaxStr = zMaxVal.(string)
			}
			if logFreqVal, ok := ctx.GlobalFlags["-log-freq"]; ok {
				logFreq = logFreqVal.(bool)
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
				return generateToChartCode(xField, yFields, zField, chartType, logX, logY, logFreq, colorField, colorScale, zMinStr, zMaxStr, outputFile)
			}

			// Validate required fields
			if xField == "" {
				return fmt.Errorf("X-axis field required (use -x)")
			}
			if len(yFields) == 0 && chartType != "heatmap" {
				return fmt.Errorf("Y-axis field required (use -y)")
			}
			if chartType == "heatmap" && zField == "" {
				return fmt.Errorf("Z-axis field required for heatmap (use -z)")
			}

			// Read JSONL from stdin (with schema if present - consumes schema header)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Heatmap uses specialized Plotly-based renderer
			if chartType == "heatmap" {
				var zMin, zMax float64
				if zMinStr != "" {
					v, err := parseFloat(zMinStr)
					if err != nil {
						return fmt.Errorf("invalid -zmin value: %s", zMinStr)
					}
					zMin = v
				}
				if zMaxStr != "" {
					v, err := parseFloat(zMaxStr)
					if err != nil {
						return fmt.Errorf("invalid -zmax value: %s", zMaxStr)
					}
					zMax = v
				}

				config := ssql.DefaultHeatmapConfig()
				config.XField = xField
				if len(yFields) > 0 {
					config.YField = yFields[0]
				}
				config.ZField = zField
				config.ColorScale = colorScale
				config.ZMin = zMin
				config.ZMax = zMax
				config.LogFreq = logFreq

				if err := ssql.HeatmapChart(records, config, outputFile); err != nil {
					return fmt.Errorf("creating heatmap: %w", err)
				}

				fmt.Printf("Heatmap created: %s\n", outputFile)
				return nil
			}

			// Build chart config for non-heatmap types
			config := ssql.DefaultChartConfig()
			config.ChartType = chartType
			config.XField = xField
			config.YFields = yFields
			config.ZField = zField
			config.ColorField = colorField
			config.ColorScale = colorScale
			if logX {
				config.XAxisType = "logarithmic"
			}
			if logY {
				config.YAxisType = "logarithmic"
			}

			// Create chart using enhanced function
			if err := ssql.EnhancedChart(records, config, outputFile); err != nil {
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
	var result strings.Builder
	result.WriteString(strs[0])
	for _, s := range strs[1:] {
		result.WriteString(sep + s)
	}
	return result.String()
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

func generateToXLSXCode(filename string, sheet string) error {
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
	if sheet != "" {
		code = fmt.Sprintf(`if err := ssql.WriteXLSX(%s, %q, ssql.XLSXConfig{SheetName: %q}); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing XLSX: %%v\n", err)
		os.Exit(1)
	}`, inputVar, filename, sheet)
	} else {
		code = fmt.Sprintf(`if err := ssql.WriteXLSX(%s, %q); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing XLSX: %%v\n", err)
		os.Exit(1)
	}`, inputVar, filename)
	}

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

func generateToChartCode(xField string, yFields []string, zField, chartType string, logX, logY, logFreq bool, colorField, colorScale, zMinStr, zMaxStr, outputFile string) error {
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
	if len(yFields) == 0 && chartType != "heatmap" {
		return fmt.Errorf("Y-axis field required (use -y)")
	}

	if outputFile == "" {
		outputFile = "chart.html"
	}

	// Heatmap uses specialized Plotly-based renderer
	if chartType == "heatmap" {
		var zMin, zMax float64
		if zMinStr != "" {
			v, err := parseFloat(zMinStr)
			if err != nil {
				return fmt.Errorf("invalid -zmin value: %s", zMinStr)
			}
			zMin = v
		}
		if zMaxStr != "" {
			v, err := parseFloat(zMaxStr)
			if err != nil {
				return fmt.Errorf("invalid -zmax value: %s", zMaxStr)
			}
			zMax = v
		}

		var yField string
		if len(yFields) > 0 {
			yField = yFields[0]
		}

		var configLines []string
		configLines = append(configLines, "config := ssql.DefaultHeatmapConfig()")
		configLines = append(configLines, fmt.Sprintf("\tconfig.XField = %q", xField))
		configLines = append(configLines, fmt.Sprintf("\tconfig.YField = %q", yField))
		configLines = append(configLines, fmt.Sprintf("\tconfig.ZField = %q", zField))

		if colorScale != "" && colorScale != "viridis" {
			configLines = append(configLines, fmt.Sprintf("\tconfig.ColorScale = %q", colorScale))
		}
		if zMin != 0 {
			configLines = append(configLines, fmt.Sprintf("\tconfig.ZMin = %v", zMin))
		}
		if zMax != 0 {
			configLines = append(configLines, fmt.Sprintf("\tconfig.ZMax = %v", zMax))
		}
		if logFreq {
			configLines = append(configLines, "\tconfig.LogFreq = true")
		}

		code := fmt.Sprintf(`%s
	if err := ssql.HeatmapChart(%s, config, %q); err != nil {
		return fmt.Errorf("creating heatmap: %%w", err)
	}
	fmt.Printf("Heatmap created: %%s\n", %q)`, joinStrings(configLines, "\n"), inputVar, outputFile, outputFile)

		frag := lib.NewFinalFragment(inputVar, code, []string{"fmt"}, getCommandString())
		return lib.WriteCodeFragment(frag)
	}

	// Build config setup code for non-heatmap chart types
	var configLines []string
	configLines = append(configLines, "config := ssql.DefaultChartConfig()")
	configLines = append(configLines, fmt.Sprintf("\tconfig.ChartType = %q", chartType))
	configLines = append(configLines, fmt.Sprintf("\tconfig.XField = %q", xField))

	if len(yFields) > 0 {
		quotedFields := make([]string, len(yFields))
		for i, f := range yFields {
			quotedFields[i] = fmt.Sprintf("%q", f)
		}
		configLines = append(configLines, fmt.Sprintf("\tconfig.YFields = []string{%s}", joinStrings(quotedFields, ", ")))
	}

	if zField != "" {
		configLines = append(configLines, fmt.Sprintf("\tconfig.ZField = %q", zField))
	}
	if colorField != "" {
		configLines = append(configLines, fmt.Sprintf("\tconfig.ColorField = %q", colorField))
	}
	if colorScale != "" && colorScale != "viridis" {
		configLines = append(configLines, fmt.Sprintf("\tconfig.ColorScale = %q", colorScale))
	}
	if logX {
		configLines = append(configLines, "\tconfig.XAxisType = \"logarithmic\"")
	}
	if logY {
		configLines = append(configLines, "\tconfig.YAxisType = \"logarithmic\"")
	}

	code := fmt.Sprintf(`%s
	if err := ssql.EnhancedChart(%s, config, %q); err != nil {
		return fmt.Errorf("creating chart: %%w", err)
	}
	fmt.Printf("Chart created: %%s\n", %q)`, joinStrings(configLines, "\n"), inputVar, outputFile, outputFile)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt"}, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// parseFloat parses a string as float64
func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// registerToAnimate registers the "to animate" subcommand
func registerToAnimate(cmd *cf.SubcommandBuilder) {
	colorScales := []string{"viridis", "plasma", "inferno", "magma", "cividis", "turbo"}
	chartTypes := []string{"heatmap", "histogram"}

	cmd.Subcommand("animate").
		Description("Create animated heatmap or histogram with video-player controls").
		Example("ssql from spectrogram.jsonl | ssql to animate -frame segment -x freq -y time -z magnitude",
			"Animated spectrogram over segments").
		Example("ssql from distributions.jsonl | ssql to animate -frame year -x bin -y count -type histogram",
			"Animated histogram of distributions over years").
		Example("ssql from data.csv | ssql to animate -frame epoch -x x -y y -z val -fps 10 -loop",
			"Fast looping heatmap animation").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-frame").
		String().
		FieldsFromFlag("").
		Global().
		Required().
		Help("Field that defines animation frames (ordered)").
		Done().
		Flag("-x").
		String().
		FieldsFromFlag("").
		Global().
		Required().
		Help("X-axis field").
		Done().
		Flag("-y").
		String().
		FieldsFromFlag("").
		Global().
		Required().
		Help("Y-axis field (value field for histogram)").
		Done().
		Flag("-z").
		String().
		FieldsFromFlag("").
		Global().
		Help("Z-axis field for heatmap cell values (required for heatmap)").
		Done().
		Flag("-type").
		String().
		Completer(&cf.StaticCompleter{Options: chartTypes}).
		Global().
		Default("heatmap").
		Help("Chart type: heatmap or histogram").
		Done().
		Flag("-fps").
		Int().
		Global().
		Default(5).
		Help("Playback frames per second (default: 5)").
		Done().
		Flag("-colorscale").
		String().
		Completer(&cf.StaticCompleter{Options: colorScales}).
		Global().
		Default("viridis").
		Help("Color scale for heatmap: viridis, plasma, inferno, magma, cividis, turbo").
		Done().
		Flag("-loop").
		Bool().
		Global().
		Help("Loop playback").
		Done().
		Flag("-output", "-o").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.html"}).
		Global().
		Default("animate.html").
		Help("Output HTML file (default: animate.html)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var frameField, xField, yField, zField, chartType, colorScale, outputFile string
			var fps int
			var loop, generate bool

			if val, ok := ctx.GlobalFlags["-frame"]; ok {
				frameField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-x"]; ok {
				xField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-y"]; ok {
				yField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-z"]; ok {
				zField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-type"]; ok {
				chartType = val.(string)
			} else {
				chartType = "heatmap"
			}
			if val, ok := ctx.GlobalFlags["-fps"]; ok {
				fps = val.(int)
			} else {
				fps = 5
			}
			if val, ok := ctx.GlobalFlags["-colorscale"]; ok {
				colorScale = val.(string)
			} else {
				colorScale = "viridis"
			}
			if val, ok := ctx.GlobalFlags["-loop"]; ok {
				loop = val.(bool)
			}
			if val, ok := ctx.GlobalFlags["-output"]; ok {
				outputFile = val.(string)
			} else {
				outputFile = "animate.html"
			}
			if val, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = val.(bool)
			}

			if shouldGenerate(generate) {
				return generateToAnimateCode(frameField, xField, yField, zField, chartType, fps, colorScale, loop, outputFile)
			}

			// Validate required fields
			if frameField == "" {
				return fmt.Errorf("frame field required (use -frame)")
			}
			if xField == "" {
				return fmt.Errorf("X-axis field required (use -x)")
			}
			if yField == "" {
				return fmt.Errorf("Y-axis field required (use -y)")
			}
			if chartType == "heatmap" && zField == "" {
				return fmt.Errorf("Z-axis field required for heatmap (use -z)")
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Build config
			config := ssql.DefaultAnimateConfig()
			config.FrameField = frameField
			config.XField = xField
			config.YField = yField
			config.ZField = zField
			config.ChartType = chartType
			config.FPS = fps
			config.ColorScale = colorScale
			config.Loop = loop

			if err := ssql.AnimateChart(records, config, outputFile); err != nil {
				return fmt.Errorf("creating animation: %w", err)
			}

			fmt.Printf("Animation created: %s\n", outputFile)
			return nil
		}).
		Done()
}

func generateToAnimateCode(frameField, xField, yField, zField, chartType string, fps int, colorScale string, loop bool, outputFile string) error {
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

	if frameField == "" {
		return fmt.Errorf("frame field required (use -frame)")
	}
	if xField == "" {
		return fmt.Errorf("X-axis field required (use -x)")
	}
	if yField == "" {
		return fmt.Errorf("Y-axis field required (use -y)")
	}

	if outputFile == "" {
		outputFile = "animate.html"
	}

	var configLines []string
	configLines = append(configLines, "config := ssql.DefaultAnimateConfig()")
	configLines = append(configLines, fmt.Sprintf("\tconfig.FrameField = %q", frameField))
	configLines = append(configLines, fmt.Sprintf("\tconfig.XField = %q", xField))
	configLines = append(configLines, fmt.Sprintf("\tconfig.YField = %q", yField))

	if zField != "" {
		configLines = append(configLines, fmt.Sprintf("\tconfig.ZField = %q", zField))
	}
	if chartType != "" && chartType != "heatmap" {
		configLines = append(configLines, fmt.Sprintf("\tconfig.ChartType = %q", chartType))
	}
	if fps != 0 && fps != 5 {
		configLines = append(configLines, fmt.Sprintf("\tconfig.FPS = %d", fps))
	}
	if colorScale != "" && colorScale != "viridis" {
		configLines = append(configLines, fmt.Sprintf("\tconfig.ColorScale = %q", colorScale))
	}
	if loop {
		configLines = append(configLines, "\tconfig.Loop = true")
	}

	code := fmt.Sprintf(`%s
	if err := ssql.AnimateChart(%s, config, %q); err != nil {
		return fmt.Errorf("creating animation: %%w", err)
	}
	fmt.Printf("Animation created: %%s\n", %q)`, joinStrings(configLines, "\n"), inputVar, outputFile, outputFile)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt"}, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// registerToExplore registers the "to explore" subcommand
func registerToExplore(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("explore").
		Description("Create interactive data exploration app with table, charts, and aggregation").
		Example("ssql from data.csv | ssql to explore output.html",
			"Generate explorer from CSV data").
		Example("ssql from logs.jsonl | ssql where -if level eq ERROR | ssql to explore errors.html",
			"Explore filtered error logs").
		Example("ssql from sales.csv | ssql to explore -wasm -theme dark analysis.html",
			"Explorer with WASM-powered client-side transforms").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-title").
		String().
		Global().
		Default("Data Explorer").
		Help("Page title").
		Done().
		Flag("-theme").
		String().
		Global().
		Default("light").
		Completer(&cf.StaticCompleter{Options: []string{"light", "dark"}}).
		Help("Theme: light or dark").
		Done().
		Flag("-x").
		String().
		FieldsFromFlag("").
		Global().
		Help("Initial X-axis field").
		Done().
		Flag("-y").
		String().
		FieldsFromFlag("").
		Global().
		Help("Initial Y-axis field").
		Done().
		Flag("-pagesize").
		Int().
		Global().
		Default(50).
		Help("Rows per page in table (default 50)").
		Done().
		Flag("-wasm").
		Bool().
		Global().
		Help("Enable WASM-powered client-side transforms").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.html"}).
		Global().
		Default("explore.html").
		Help("Output HTML file (default: explore.html)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var title, theme, xField, yField, outputFile string
			var pageSize int
			var generate, useWasm bool

			if titleVal, ok := ctx.GlobalFlags["-title"]; ok {
				title = titleVal.(string)
			} else {
				title = "Data Explorer"
			}
			if themeVal, ok := ctx.GlobalFlags["-theme"]; ok {
				theme = themeVal.(string)
			} else {
				theme = "light"
			}
			if xVal, ok := ctx.GlobalFlags["-x"]; ok {
				xField = xVal.(string)
			}
			if yVal, ok := ctx.GlobalFlags["-y"]; ok {
				yField = yVal.(string)
			}
			if psVal, ok := ctx.GlobalFlags["-pagesize"]; ok {
				pageSize = psVal.(int)
			} else {
				pageSize = 50
			}
			if outVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = outVal.(string)
			} else {
				outputFile = "explore.html"
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if wasmVal, ok := ctx.GlobalFlags["-wasm"]; ok {
				useWasm = wasmVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToExploreCode(title, theme, xField, yField, pageSize, outputFile)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Build explore config
			config := ssql.DefaultExploreConfig()
			config.Title = title
			config.Theme = theme
			config.InitialXField = xField
			config.InitialYField = yField
			config.PageSize = pageSize

			// Enable WASM with embedded binary
			if useWasm {
				config.WasmEnabled = true
				config.WasmExecJS = wasm.WasmExecJS
				config.SsqlWasmJS = wasm.SsqlWasmJS
				config.WasmBinary = wasm.WasmBinaryBase64()
			}

			// Create explorer
			if err := ssql.DataExplore(records, config, outputFile); err != nil {
				return fmt.Errorf("creating explorer: %w", err)
			}

			if useWasm {
				fmt.Printf("Explorer created: %s (with WASM)\n", outputFile)
			} else {
				fmt.Printf("Explorer created: %s\n", outputFile)
			}
			return nil
		}).
		Done()
}

func generateToExploreCode(title, theme, xField, yField string, pageSize int, outputFile string) error {
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

	if outputFile == "" {
		outputFile = "explore.html"
	}

	// Build config setup code
	var configLines []string
	configLines = append(configLines, "config := ssql.DefaultExploreConfig()")
	if title != "" && title != "Data Explorer" {
		configLines = append(configLines, fmt.Sprintf("\tconfig.Title = %q", title))
	}
	if theme != "" && theme != "light" {
		configLines = append(configLines, fmt.Sprintf("\tconfig.Theme = %q", theme))
	}
	if xField != "" {
		configLines = append(configLines, fmt.Sprintf("\tconfig.InitialXField = %q", xField))
	}
	if yField != "" {
		configLines = append(configLines, fmt.Sprintf("\tconfig.InitialYField = %q", yField))
	}
	if pageSize != 50 && pageSize > 0 {
		configLines = append(configLines, fmt.Sprintf("\tconfig.PageSize = %d", pageSize))
	}

	code := fmt.Sprintf(`%s
	if err := ssql.DataExplore(%s, config, %q); err != nil {
		return fmt.Errorf("creating explorer: %%w", err)
	}
	fmt.Printf("Explorer created: %%s\n", %q)`, joinStrings(configLines, "\n"), inputVar, outputFile, outputFile)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt"}, getCommandString())
	return lib.WriteCodeFragment(frag)
}
