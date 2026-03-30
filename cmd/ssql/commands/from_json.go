package commands

import (
	"bufio"
	"io"
	"iter"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func registerFromJSON(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("json").
		Description("Read JSON file(s) or stdin").
		Example("ssql from json data.json | ssql to table", "Read JSON file").
		Example("ssql from json *.json | ssql to table", "Read multiple JSON files").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-merge-schemas").
			Bool().
			Global().
			Help("Allow files with different fields (merge schemas)").
			Done().

		Flag("-source").
			String().
			Global().
			Default("").
			Help("Add field with source filename: -source file").
			Done().

		Flag("FILE").
			String().
			Variadic().
			Completer(&cf.FileCompleter{Pattern: "*.json"}).
			Global().
			Default("").
			Help("Input JSON file(s) (or stdin if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			cfg := extractMultiFileConfig(ctx)

			if len(cfg.files) <= 1 {
				inputFile := ""
				if len(cfg.files) == 1 {
					inputFile = cfg.files[0]
				}
				return executeFromJSON(inputFile, cfg.generate)
			}

			readFile := func(file *os.File) iter.Seq[ssql.Record] {
				return readJSONSchemaAware(file)
			}
			return executeFromMultiFile(cfg, "JSON", readFile, nil)
		}).
		Done()
}

func registerFromJSONL(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("jsonl").
		Description("Read JSONL file(s) or stdin").
		Example("ssql from jsonl data.jsonl | ssql to table", "Read JSONL file").
		Example("ssql from jsonl *.jsonl | ssql to table", "Read multiple JSONL files").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-merge-schemas").
			Bool().
			Global().
			Help("Allow files with different fields (merge schemas)").
			Done().

		Flag("-source").
			String().
			Global().
			Default("").
			Help("Add field with source filename: -source file").
			Done().

		Flag("FILE").
			String().
			Variadic().
			Completer(&cf.FileCompleter{Pattern: "*.jsonl"}).
			Global().
			Default("").
			Help("Input JSONL file(s) (or stdin if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			cfg := extractMultiFileConfig(ctx)

			if len(cfg.files) <= 1 {
				inputFile := ""
				if len(cfg.files) == 1 {
					inputFile = cfg.files[0]
				}
				return executeFromJSON(inputFile, cfg.generate)
			}

			readFile := func(file *os.File) iter.Seq[ssql.Record] {
				return readJSONSchemaAware(file)
			}
			return executeFromMultiFile(cfg, "JSONL", readFile, nil)
		}).
		Done()
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

// generateFromJSONCode generates Go code for reading JSON/JSONL.
func generateFromJSONCode(filename string) error {
	var code string
	var imports []string
	var params []lib.CodeParam

	if filename == "" {
		code = `records := ssql.ReadJSONFromReader(os.Stdin)`
		imports = []string{"os"}
	} else {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input JSON file", VarName: "flagInput"})
		code = `records, err := ssql.ReadJSONAuto(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading JSON: %w", err))
		os.Exit(1)
	}`
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
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
