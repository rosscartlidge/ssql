package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToAnimate registers the "to animate" subcommand
func registerToAnimate(cmd *cf.SubcommandBuilder) {
	colorScales := []string{"viridis", "plasma", "inferno", "magma", "cividis", "turbo"}
	chartTypes := []string{"heatmap", "histogram"}

	cmd.Subcommand("animate").
		DisplaySink("animate").
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
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
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
