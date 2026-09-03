package commands

import (
	"fmt"
	"io"
	"iter"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// linesTypedSchema is the fixed typed schema of `from lines`: the
// typed package's own Line struct, so no StructDefs are emitted.
var linesTypedSchema = &lib.TypedSchema{
	TypeName: "typed.Line",
	Fields: []lib.TypedSchemaField{
		{Name: "line_number", GoName: "LineNumber", GoType: "int64"},
		{Name: "line", GoName: "Line", GoType: "string"},
	},
}

func registerFromLines(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("lines").
		Description("Read raw text: one record per line with line_number (1-based) and line — pair with extract to turn text into fields").
		Example("ssql from lines app.log | ssql extract -field line -re '^(?P<ts>\\S+) (?P<lvl>\\w+) (?P<msg>.*)$' -skip", "Parse a log into fields").
		Example("ssql from lines notes.txt | ssql where -if line contains TODO", "grep, but the result is records").
		Example("journalctl -o short-iso | ssql from lines | ssql limit -last 20", "stdin works too").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*"}).
		Global().
		Default("").
		Help("Text file (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var file string
			if v, ok := ctx.GlobalFlags["FILE"]; ok {
				file, _ = v.(string)
			}
			var generate bool
			if v, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = v.(bool)
			}
			return executeFromLines(file, generate)
		}).
		Done()
}

// executeFromLines handles `from lines` (subcommand and the bare
// `from x.log` / `from x.txt` forms).
func executeFromLines(inputFile string, generate bool) error {
	if schemaMode() {
		return writeSchemaModeOutput(os.Stdout, []string{"line_number", "line"})
	}
	if shouldGenerate(generate) {
		return generateFromLinesCode(inputFile)
	}
	var r io.Reader = os.Stdin
	if inputFile != "" {
		f, err := os.Open(inputFile)
		if err != nil {
			return fmt.Errorf("reading text file: %w", err)
		}
		defer f.Close()
		r = f
	}
	var records iter.Seq[ssql.Record] = ssql.ReadLinesFromReader(r)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

func generateFromLinesCode(filename string) error {
	var params []lib.CodeParam
	if filename != "" {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input text file", VarName: "flagInput"})
	}
	if typedMode() {
		// Serial source: line boundaries are sequential, so there is no
		// parallel form (typed.ReadLines → iter.Seq[typed.Line]).
		code := `records := typed.ReadLinesFromReader(os.Stdin)`
		imports := []string{"os", "github.com/rosscartlidge/ssql/v4/typed"}
		if filename != "" {
			code = `records := typed.ReadLines(*flagInput)`
			imports = []string{"github.com/rosscartlidge/ssql/v4/typed"}
		}
		frag := lib.NewInitFragment("records", code, imports, getCommandString())
		frag.Params = params
		frag.OutputTypedSchema = linesTypedSchema
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeNone, Produces: lib.ShapeSeqTyped}
		return lib.WriteCodeFragment(frag)
	}
	code := `records := ssql.ReadLinesFromReader(os.Stdin)`
	imports := []string{"os"}
	if filename != "" {
		code = `records, err := ssql.ReadLines(*flagInput)
	if err != nil {
		return fmt.Errorf("reading lines: %w", err)
	}`
		imports = []string{"fmt"}
	}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
