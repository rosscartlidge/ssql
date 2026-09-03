package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterTee registers the tee subcommand: write the stream to FILE as
// schema-headed JSONL (the pipeline wire format) while passing every
// record through unchanged — Unix tee for pipelines, for saving
// intermediate results. The file replays with `ssql from FILE` and,
// being schema-headed, feeds join/merge/union directly.
func RegisterTee(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("tee").
		Description("Write the stream to a file and pass it through (save intermediate results)").
		Example("ssql from big.csv | ssql where -if x gt 5 | ssql tee filtered.jsonl | ssql group-by k -count n | ssql to table", "Snapshot the filtered records while continuing the pipeline").
		Example("ssql from a.csv | ssql join <(ssql from b.csv) -using id | ssql tee joined.jsonl | ssql to table", "Keep the join result for later replay: ssql from joined.jsonl").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FILE").
			String().
			Required().
			Global().
			Help("Output file (schema-headed JSONL — replay with `ssql from FILE`)").
			Done().

		Handler(func(ctx *cf.Context) error {
			if schemaMode() {
				return runSchemaModeTransform(ctx, "tee")
			}

			var generate bool
			var filename string
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if fVal, ok := ctx.GlobalFlags["FILE"]; ok {
				if s, ok := fVal.(string); ok {
					filename = s
				}
			}
			if filename == "" {
				return fmt.Errorf("tee requires an output FILE")
			}

			if shouldGenerate(generate) {
				return generateTeeCode(filename)
			}

			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Preserve the stream's declared field order in the file.
			var fieldOrder []string
			if schemaAndRecords.Schema != nil {
				fieldOrder = schemaAndRecords.Schema.Fields
			}

			teed := ssql.TeeFile(filename, fieldOrder...)(records)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), schemaAndRecords.Schema, teed); err != nil {
				return fmt.Errorf("tee: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}

// generateTeeCode emits the record-mode pass-through fragment. A single
// `var := filter(input)` expression on purpose — the record assembler's
// extractFilter only understands that shape for stmt fragments — which
// is why ssql.TeeFile opens the file itself. No typed template: under
// SSQL_MODE=typed the Phase B boundary converts to Records first (a
// typed.TeeFile fast path is tracked in TODO).
func generateTeeCode(filename string) error {
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

	outputVar := "teed"
	params := []lib.CodeParam{
		{Name: "tee-out", Default: filename, Help: "tee output file (schema-headed JSONL)", VarName: "flagTeeOut"},
	}

	// Inherit the upstream's natural field order for the file header.
	orderArgs := ""
	for _, f := range prevRecordFields {
		orderArgs += fmt.Sprintf(", %q", f)
	}
	code := fmt.Sprintf("%s := ssql.TeeFile(*flagTeeOut%s)(%s)", outputVar, orderArgs, inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	frag.Params = params
	frag.OutputRecordFields = prevRecordFields
	return lib.WriteCodeFragment(frag)
}
