package commands

import (
	"fmt"
	"os"
	"strconv"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterTo registers the to subcommand with nested format subcommands
func RegisterTo(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	toCmd := cmd.Subcommand("to").
		Description("Write output in various formats (table, csv, tsv, json, chart, heatmap, wav)")

	// Register nested subcommands
	registerToTable(toCmd)
	registerToCSV(toCmd)
	registerToTSV(toCmd)
	registerToJSON(toCmd)
	registerToArrow(toCmd)
	registerToWAV(toCmd)
	registerToChart(toCmd)
	registerToHeatmap(toCmd)

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
		Flag("-output", "-o").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.html"}).
		Global().
		Default("chart.html").
		Help("Output HTML file (default: chart.html)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var xField, zField, colorField, colorScale, chartType, outputFile string
			var yFields []string
			var generate, logX, logY bool

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
				return generateToChartCode(xField, yFields, zField, chartType, logX, logY, colorField, colorScale, outputFile)
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

			// Build chart config
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

// registerToHeatmap registers the "to heatmap" subcommand
func registerToHeatmap(cmd *cf.SubcommandBuilder) {
	colorScales := []string{"viridis", "plasma", "inferno", "magma", "cividis", "turbo"}

	cmd.Subcommand("heatmap").
		Description("Create specialized heatmap visualization (spectrograms, matrices)").
		Example("ssql from audio.wav | ssql spectrogram ... | ssql to heatmap -x time -y frequency -z magnitude", "Spectrogram from audio").
		Example("ssql from matrix.csv | ssql to heatmap -x row -y col -z value -zmin -1 -zmax 1", "Correlation matrix with fixed range").
		Example("ssql from data.csv | ssql to heatmap -x time -y freq -z db -log-freq -colorscale plasma", "Log frequency axis with plasma colors").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-x").
		String().
		FieldsFromFlag("").
		Global().
		Required().
		Help("X-axis field (e.g., time)").
		Done().
		Flag("-y").
		String().
		FieldsFromFlag("").
		Global().
		Required().
		Help("Y-axis field (e.g., frequency)").
		Done().
		Flag("-z").
		String().
		FieldsFromFlag("").
		Global().
		Required().
		Help("Z-axis field for color values (e.g., magnitude)").
		Done().
		Flag("-colorscale").
		String().
		Completer(&cf.StaticCompleter{Options: colorScales}).
		Global().
		Default("viridis").
		Help("Color scale: viridis, plasma, inferno, magma, cividis, turbo").
		Done().
		Flag("-zmin").
		String().
		Global().
		Default("").
		Help("Minimum value for color scale (empty = auto)").
		Done().
		Flag("-zmax").
		String().
		Global().
		Default("").
		Help("Maximum value for color scale (empty = auto)").
		Done().
		Flag("-log-freq").
		Bool().
		Global().
		Help("Use logarithmic Y-axis (for frequency spectrograms)").
		Done().
		Flag("-output", "-o").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.html"}).
		Global().
		Default("heatmap.html").
		Help("Output HTML file (default: heatmap.html)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var xField, yField, zField, colorScale, outputFile string
			var zMinStr, zMaxStr string
			var zMin, zMax float64
			var logFreq, generate bool

			if xVal, ok := ctx.GlobalFlags["-x"]; ok {
				xField = xVal.(string)
			}
			if yVal, ok := ctx.GlobalFlags["-y"]; ok {
				yField = yVal.(string)
			}
			if zVal, ok := ctx.GlobalFlags["-z"]; ok {
				zField = zVal.(string)
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
			// Parse zMin/zMax as floats (empty string = 0 = auto)
			if zMinStr != "" {
				if v, err := parseFloat(zMinStr); err == nil {
					zMin = v
				} else {
					return fmt.Errorf("invalid -zmin value: %s", zMinStr)
				}
			}
			if zMaxStr != "" {
				if v, err := parseFloat(zMaxStr); err == nil {
					zMax = v
				} else {
					return fmt.Errorf("invalid -zmax value: %s", zMaxStr)
				}
			}
			if logVal, ok := ctx.GlobalFlags["-log-freq"]; ok {
				logFreq = logVal.(bool)
			}
			if outVal, ok := ctx.GlobalFlags["-output"]; ok {
				outputFile = outVal.(string)
			} else {
				outputFile = "heatmap.html"
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToHeatmapCode(xField, yField, zField, colorScale, zMin, zMax, logFreq, outputFile)
			}

			// Validate required fields
			if xField == "" {
				return fmt.Errorf("X-axis field required (use -x)")
			}
			if yField == "" {
				return fmt.Errorf("Y-axis field required (use -y)")
			}
			if zField == "" {
				return fmt.Errorf("Z-axis field required (use -z)")
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Build heatmap config
			config := ssql.DefaultHeatmapConfig()
			config.XField = xField
			config.YField = yField
			config.ZField = zField
			config.ColorScale = colorScale
			config.ZMin = zMin
			config.ZMax = zMax
			config.LogFreq = logFreq

			// Create heatmap
			if err := ssql.HeatmapChart(records, config, outputFile); err != nil {
				return fmt.Errorf("creating heatmap: %w", err)
			}

			fmt.Printf("Heatmap created: %s\n", outputFile)
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

func generateToChartCode(xField string, yFields []string, zField, chartType string, logX, logY bool, colorField, colorScale, outputFile string) error {
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

	// Build config setup code
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

func generateToHeatmapCode(xField, yField, zField, colorScale string, zMin, zMax float64, logFreq bool, outputFile string) error {
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
	if zField == "" {
		return fmt.Errorf("Z-axis field required (use -z)")
	}

	if outputFile == "" {
		outputFile = "heatmap.html"
	}

	// Build config setup code
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
