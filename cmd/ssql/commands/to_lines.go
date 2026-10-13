package commands

import (
	"fmt"
	"slices"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToLines registers "to lines": one field's text per record,
// one line each — the twin of `from lines`. Its reason to exist is the
// views table (DFC148 §4.4): a row's pipeline column, selected with
// where, feeds `ssql run -stdin`.
func registerToLines(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("lines").
		Description("Write one field's text per record, one line each (the twin of from lines) — a views table's pipeline column feeds ssql run -stdin").
		Example("ssql from csv views.csv | ssql where -if name eq services | ssql to lines pipeline | ssql run -stdin", "Run a named view from a views table").
		Example("ssql from lines app.log | ssql where -if line contains ERROR | ssql to lines line", "grep, with the lines back as text").
		Example("ssql from data.csv | ssql to lines name names.txt", "One value per line to a file").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FIELD").
			String().
			FieldsFromFlag("").
			Global().
			Help("Field whose value becomes each line").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*"}).
			Global().
			Default("").
			Help("Output text file (or stdout if not specified)").
			Done().

		Handler(func(ctx *cf.Context) error {
			if schemaMode() {
				return runSchemaModeTransform(ctx, "to") // a sink passes the schema through
			}
			field, _ := ctx.GlobalFlags["FIELD"].(string)
			if field == "" {
				return fmt.Errorf("to lines: a FIELD is required (the field whose value becomes each line)")
			}
			outputFile, _ := ctx.GlobalFlags["FILE"].(string)
			generate, _ := ctx.GlobalFlags["-generate"].(bool)

			if shouldGenerate(generate) {
				return generateToLinesCode(field, outputFile)
			}

			records := lib.ReadJSONLWithSchema(ctx.Stdin()).Records
			if outputFile == "" {
				return ssql.WriteLinesToWriter(records, field, ctx.Stdout())
			}
			return ssql.WriteLines(records, field, outputFile)
		}).
		Done()
}

// generateToLinesCode emits the sink in record mode
// (ssql.WriteLines{,ToWriter}) or, after a typed upstream, the typed
// form: typed.WriteLines{,ToWriter} with a closure that renders the row's
// field by ssql.FormatValue, so every lane writes the same text. The
// typed form is serial only — the lines are the output's order.
func generateToLinesCode(field, filename string) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	inputVar := "records"
	var prevSchema *lib.TypedSchema
	var prevRecordFields []string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
		prevRecordFields = fragments[len(fragments)-1].OutputRecordFields
	}
	// Record mode knows the field list only when the upstream declared
	// it (group-by, tee); then a misspelt field is refused here. When it
	// is unknown the generated program refuses at its first record with
	// the same message (ssql.WriteLines validates).
	if len(prevRecordFields) > 0 && !slices.Contains(prevRecordFields, field) {
		return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("to lines: unknown field %q (available: %s)", field, joinStrings(prevRecordFields, ", ")))
	}

	var params []lib.CodeParam
	if filename != "" {
		params = append(params, lib.CodeParam{Name: "output", Default: filename, Help: "output text file", VarName: "flagOutput"})
	}

	if typedMode() && prevSchema != nil {
		goName := ""
		var names []string
		for _, f := range prevSchema.Fields {
			names = append(names, f.Name)
			if f.Name == field {
				goName = f.GoName
			}
		}
		if goName == "" {
			return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("to lines: unknown field %q (available: %s)", field, joinStrings(names, ", ")))
		}
		text := fmt.Sprintf("func(r %s) string { return ssql.FormatValue(r.%s) }", prevSchema.TypeName, goName)
		var code string
		if filename == "" {
			code = fmt.Sprintf(`if err := typed.WriteLinesToWriter(%s, %s, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar, text)
		} else {
			code = fmt.Sprintf(`if err := typed.WriteLines(%s, %s, *flagOutput); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar, text)
		}
		imports := []string{"github.com/rosscartlidge/ssql/v4", "github.com/rosscartlidge/ssql/v4/typed", "fmt", "os"}
		frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
		frag.Params = params
		frag.InputTypedSchema = prevSchema
		// Serial only: the planner inserts Stream.Serial() before this
		// sink when the upstream is a Stream — the lines ARE the order.
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeNone, SerialOnly: true}
		return lib.WriteCodeFragment(frag)
	}

	var code string
	if filename == "" {
		code = fmt.Sprintf(`if err := ssql.WriteLinesToWriter(%s, %q, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar, field)
	} else {
		code = fmt.Sprintf(`if err := ssql.WriteLines(%s, %q, *flagOutput); err != nil {
		fmt.Fprintf(os.Stderr, "write: %%v\n", err)
		os.Exit(1)
	}`, inputVar, field)
	}
	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt", "os"}, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
