package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/wasm"
)

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
