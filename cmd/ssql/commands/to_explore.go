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
		Example("ssql from sales.csv | ssql to explore -theme dark analysis.html",
			"Full workspace by default: pipeline bar, completion, uploads, downloads").
		Example("ssql from big.csv | ssql to explore -light small.html",
			"Light viewer (~1MB): grid browsing only, no embedded engine").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-allow-empty").
			Bool().
			Global().
			Default(false).
			Help("Permit zero input records (a deliberately blank workspace page); without it, empty input is a loud error").
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
			Default(true).
			Help("Embed the ssql engine for client-side pipelines (DEFAULT; use -light to disable)").
			Done().

		Flag("-light").
			Bool().
			Global().
			Help("Light viewer: no embedded engine (~1MB page, grid browsing only)").
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
			if lightVal, ok := ctx.GlobalFlags["-light"]; ok && lightVal.(bool) {
				useWasm = false
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToExploreCode(title, theme, xField, yField, pageSize, outputFile)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Build explore config
			config := ssql.DefaultExploreConfig()
			config.Title = title
			config.Theme = theme
			allowEmpty, _ := ctx.GlobalFlags["-allow-empty"].(bool)
			config.AllowEmpty = allowEmpty
			config.InitialXField = xField
			config.InitialYField = yField
			config.PageSize = pageSize

			// Enable client-side transforms via the embedded engine —
			// the SAME slim playground wasm, gzipped (DFC107).
			if useWasm {
				// Slim builds (playground, WebVM) carry no engine — since
				// -wasm is the DEFAULT, downgrade gracefully to light
				// rather than failing every `to explore`.
				if !wasm.Available() {
					fmt.Fprintln(os.Stderr, "note: this build carries no embedded engine — generating the light explorer")
					useWasm = false
				}
			}
			if useWasm {
				config.WasmEnabled = true
				config.WasmExecJS = wasm.WasmExecJS
				config.FsPolyfillJS = wasm.FsPolyfillJS
				config.SsqlUIJS = wasm.SsqlUIJS
				config.WasmBinary = wasm.WasmGzBase64()
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
