package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v3"
	"github.com/rosscartlidge/ssql/v2"
	"github.com/rosscartlidge/ssql/v2/cmd/ssql/lib"
)

// RegisterChart registers the chart subcommand
func RegisterChart(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("chart").
		Description("Create interactive HTML chart from data").
		Example("ssql read-csv data.csv | ssql chart -x date -y revenue", "Create line chart of revenue over time").
		Example("ssql read-csv sales.csv | ssql chart -x product -y sales -output sales.html", "Create chart with custom output file").
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
		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.jsonl"}).
			Global().
			Default("").
			Help("Input JSONL file (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var xField, yField, outputFile, inputFile string
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
			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateChartCode(xField, yField, outputFile)
			}

			// Validate required fields
			if xField == "" {
				return fmt.Errorf("X-axis field required (use -x)")
			}
			if yField == "" {
				return fmt.Errorf("Y-axis field required (use -y)")
			}

			// Read JSONL from stdin or file
			input, err := lib.OpenInput(inputFile)
			if err != nil {
				return err
			}
			defer input.Close()

			records := lib.ReadJSONL(input)

			// Create chart
			err = ssql.QuickChart(records, xField, yField, outputFile)
			if err != nil {
				return fmt.Errorf("creating chart: %w", err)
			}

			fmt.Printf("Chart created: %s\n", outputFile)
			return nil
		}).
		Done()
	return cmd
}

// generateChartCode generates Go code for the chart command
func generateChartCode(xField, yField, outputFile string) error {
	// Read all previous code fragments from stdin
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	// Pass through all previous fragments
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	// Get input variable from last fragment
	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	// Validate required fields
	if xField == "" {
		return fmt.Errorf("X-axis field required (use -x)")
	}
	if yField == "" {
		return fmt.Errorf("Y-axis field required (use -y)")
	}

	if outputFile == "" {
		outputFile = "chart.html"
	}

	// Generate QuickChart call
	code := fmt.Sprintf(`if err := ssql.QuickChart(%s, %q, %q, %q); err != nil {
		return fmt.Errorf("creating chart: %%w", err)
	}
	fmt.Printf("Chart created: %%s\n", %q)`, inputVar, xField, yField, outputFile, outputFile)

	// Create final fragment (chart is a terminal operation with side effects)
	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt"}, getCommandString())
	return lib.WriteCodeFragment(frag)
}
