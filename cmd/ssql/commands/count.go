package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterCount registers the count subcommand — a sink that
// consumes its input and prints the row count to stdout (like
// `wc -l`). Discoverable shorthand for the common
// `ssql group-by -count n` case when you don't actually want the
// group-by structure, just the total.
func RegisterCount(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("count").
		Description("Count rows and print to stdout (sink, like wc -l)").
		Example("ssql from data.csv | ssql count", "Count rows in data.csv").
		Example("ssql from data.csv | ssql where -if age gt 25 | ssql count", "Count adults").
		Example("ssql from logs.csv | ssql where -if status eq error | ssql count", "Count errors").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Handler(func(ctx *cf.Context) error {
			var generate bool
			if v, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = v.(bool)
			}
			if shouldGenerate(generate) {
				return generateCountCode()
			}
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			var n int64
			for range schemaAndRecords.Records {
				n++
			}
			fmt.Println(n)
			return nil
		}).
		Done()
	return cmd
}

// generateCountCode emits a final fragment that drains the
// pipeline and prints a row count. Three flavours:
//
//   - Record mode: `for range records { n++ }` — no helper
//     needed; one extra import (`fmt`).
//   - Typed serial: `n := typed.Count[T](records)`.
//   - Typed parallel (Stream[T]): `n := records.SerialCount()` —
//     drains shards concurrently without paying the per-row
//     Serial() fan-in cost. Declared with Accepts=ShapeStream so
//     the planner keeps the source parallel.
func generateCountCode() error {
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
	var prevSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	} else {
		inputVar = "records"
	}

	if typedMode() {
		if prevSchema == nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'count' has no typed input; %s does not yet support typed mode", lastNamedCommand(fragments)))
		}
		// Emit BOTH templates. The Stream form (records.SerialCount())
		// drains shards concurrently with no fan-in channel; the serial
		// form (typed.Count(records)) is a one-loop count over iter.Seq[T].
		// The planner picks based on whether any downstream needs
		// Stream — for `count` itself, declaring Accepts=ShapeStream
		// keeps the source parallel.
		streamCode := fmt.Sprintf(`fmt.Println(%s.SerialCount())`, inputVar)
		serialCode := fmt.Sprintf(`fmt.Println(typed.Count(%s))`, inputVar)
		imports := []string{"fmt", "github.com/rosscartlidge/ssql/v4/typed"}

		frag := lib.NewFinalFragment(inputVar, streamCode, imports, getCommandString())
		frag.InputTypedSchema = prevSchema
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: lib.ShapeNone}
		frag.AltCodeIfSeq = serialCode
		frag.AltImportsIfSeq = imports
		frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeNone}
		return lib.WriteCodeFragment(frag)
	}

	// Record mode: one-loop count over iter.Seq[ssql.Record].
	code := fmt.Sprintf(`var n int64
	for range %s {
		n++
	}
	fmt.Println(n)`, inputVar)
	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt"}, getCommandString())
	return lib.WriteCodeFragment(frag)
}
