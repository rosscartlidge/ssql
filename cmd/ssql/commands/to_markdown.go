package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToMarkdown registers the "to markdown" subcommand: a
// GitHub-flavored Markdown table sink (header + alignment row + one row
// per record; numeric/bool columns right-aligned, pipes escaped) — for
// pasting results into READMEs, issues, and PRs.
func registerToMarkdown(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("markdown").
		Description("Output as a GitHub-flavored Markdown table").
		Example("ssql from data.csv | ssql to markdown", "Markdown table, columns in schema order").
		Example("ssql from data.csv | ssql group-by dept -count n | ssql to markdown", "Paste aggregation results into a PR or issue").
		Example("ssql from data.csv | ssql to markdown -only name salary", "Only the named columns, in order").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-output", "-o").
		String().
		Global().
		Help("Write to a file instead of stdout: -o report.md").
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
			var generate, onlySpecified bool
			var fields []string
			var outputFile string

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if outVal, ok := ctx.GlobalFlags["-output"]; ok {
				if s, ok := outVal.(string); ok {
					outputFile = s
				}
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

			if shouldGenerate(generate) {
				return generateToMarkdownCode(fields, onlySpecified, outputFile)
			}

			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Default column order from the schema, like `to table`.
			if len(fields) == 0 && schemaAndRecords.Schema != nil {
				fields = schemaAndRecords.Schema.Fields
			}

			out := ctx.Stdout()
			if outputFile != "" {
				f, err := os.Create(outputFile)
				if err != nil {
					return fmt.Errorf("to markdown: %w", err)
				}
				defer f.Close()
				out = f
			}
			if err := ssql.WriteMarkdownTo(out, records, fields, onlySpecified); err != nil {
				return fmt.Errorf("to markdown: %w", err)
			}
			return nil
		}).
		Done()
}

// generateToMarkdownCode emits the record-mode sink fragment. There is
// deliberately NO typed template: under SSQL_MODE=typed the planner's
// Phase B machinery tags this Record-mode final fragment and inserts the
// Serial()+toRecord() boundary — the typed (parallel) upstream is kept
// and the sink renders from Records, exactly matching exec.
func generateToMarkdownCode(fields []string, onlySpecified bool, outputFile string) error {
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
	var prevRecordFields []string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevRecordFields = fragments[len(fragments)-1].OutputRecordFields
	} else {
		inputVar = "records"
	}

	// Explicit fields win; otherwise inherit the upstream's natural
	// order (same rationale as to_table — without it the generated
	// program renders columns alphabetically, mismatching the CLI).
	order := fields
	if len(order) == 0 {
		order = prevRecordFields
	}
	quoted := make([]string, len(order))
	for i, f := range order {
		quoted[i] = fmt.Sprintf("%q", f)
	}
	fieldsStr := "nil"
	if len(quoted) > 0 {
		fieldsStr = "[]string{" + joinStrings(quoted, ", ") + "}"
	}

	var code string
	var params []lib.CodeParam
	if outputFile != "" {
		params = append(params, lib.CodeParam{Name: "output", Default: outputFile, Help: "output Markdown file", VarName: "flagOutput"})
		code = fmt.Sprintf(`mdOut, err := os.Create(*flagOutput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "to markdown: %%v\n", err)
		os.Exit(1)
	}
	defer mdOut.Close()
	if err := ssql.WriteMarkdownTo(mdOut, %s, %s, %t); err != nil {
		fmt.Fprintf(os.Stderr, "to markdown: %%v\n", err)
		os.Exit(1)
	}`, inputVar, fieldsStr, onlySpecified)
	} else {
		code = fmt.Sprintf(`if err := ssql.WriteMarkdownTo(os.Stdout, %s, %s, %t); err != nil {
		fmt.Fprintf(os.Stderr, "to markdown: %%v\n", err)
		os.Exit(1)
	}`, inputVar, fieldsStr, onlySpecified)
	}
	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt", "os"}, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
