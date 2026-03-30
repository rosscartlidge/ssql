package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

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

		params := []lib.CodeParam{
			{Name: "output", Default: outputFile, Help: "output HTML file", VarName: "flagOutput"},
		}
		code := fmt.Sprintf(`%s
	if err := ssql.HeatmapChart(%s, config, *flagOutput); err != nil {
		return fmt.Errorf("creating heatmap: %%w", err)
	}
	fmt.Printf("Heatmap created: %%s\n", *flagOutput)`, joinStrings(configLines, "\n"), inputVar)

		frag := lib.NewFinalFragment(inputVar, code, []string{"fmt"}, getCommandString())
		frag.Params = params
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

	params := []lib.CodeParam{
		{Name: "output", Default: outputFile, Help: "output HTML file", VarName: "flagOutput"},
	}
	code := fmt.Sprintf(`%s
	if err := ssql.EnhancedChart(%s, config, *flagOutput); err != nil {
		return fmt.Errorf("creating chart: %%w", err)
	}
	fmt.Printf("Chart created: %%s\n", *flagOutput)`, joinStrings(configLines, "\n"), inputVar)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt"}, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
