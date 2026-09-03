package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterLimit registers the limit subcommand
func RegisterLimit(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("limit").
		Description("Take first N records (SQL LIMIT), or the last N with -last (looking for tail? this is it); 0 = no limit (pass-through)").
		Example("ssql from data.csv | ssql limit 10", "Show first 10 records").
		Example("ssql from log.csv | ssql limit -last 20", "The last 20 records in arrival order (tail)").
		Example("ssql from large.csv | ssql limit 100 | ssql to table", "Preview first 100 records").
		Example("ssql from large.csv | ssql limit 0 | ssql to table", "Limit dialled to 0: all records pass through (and code generation skips the stage)").

		Flag("-last").
			Bool().
			Global().
			Help("Keep the last N records instead of the first N (tail). Buffers only N records; emits when the input ends").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("N").
			Int().
			Required().
			Global().
			Help("Number of records to take").
			Done().

		Handler(func(ctx *cf.Context) error {
			var n int
			var generate bool

			// Get flags from context
			if nVal, ok := ctx.GlobalFlags["N"]; ok {
				n = nVal.(int)
			} else {
				return fmt.Errorf("N argument is required")
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			var last bool
			if v, ok := ctx.GlobalFlags["-last"]; ok {
				last = v.(bool)
			}

			if n < 0 {
				return fmt.Errorf("limit must be non-negative, got %d (use 0 for no limit)", n)
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateLimitCode(n, last)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Apply limit — 0 means no limit, pass everything through
			// (lets pipelines keep a `limit N` stage and dial it to 0
			// for full runs).
			if n > 0 && last {
				records = ssql.TakeLast[ssql.Record](n)(records)
			} else if n > 0 {
				records = ssql.Limit[ssql.Record](n)(records)
			}

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), schemaAndRecords.Schema, records); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateLimitCode generates Go code for the limit command
func generateLimitCode(n int, last bool) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}
	// limit 0 = no limit: emit no fragment at all, so the stage vanishes
	// from generated go/sql/ssql alike.
	if n == 0 {
		return nil
	}
	var inputVar string
	var prevSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	} else {
		inputVar = "records"
	}
	outputVar := "limited"
	params := []lib.CodeParam{
		{Name: "limit", Default: fmt.Sprintf("%d", n), Help: "maximum number of records", VarName: "flagLimit", Type: "int"},
	}
	// -last: the same shape with the tail primitive (ssql.TakeLast /
	// typed.TakeLast) — one implementation per runtime, both barriers.
	fn := "Limit"
	if last {
		fn = "TakeLast"
	}

	// Phase B: when prevSchema==nil, the upstream is Record-mode
	// (a typed→Record boundary inserted by the planner). Fall
	// through to the ssql.Limit[ssql.Record] form below.
	if typedMode() && prevSchema != nil {
		// limit is SerialOnly — planner inserts Stream.Serial()
		// upstream automatically when input is a Stream.
		code := fmt.Sprintf("%s := typed.%s[%s](*flagLimit)(%s)", outputVar, fn, prevSchema.TypeName, inputVar)
		frag := lib.NewStmtFragment(outputVar, inputVar, code, []string{"github.com/rosscartlidge/ssql/v4/typed"}, getCommandString())
		frag.Params = params
		frag.InputTypedSchema = prevSchema
		frag.OutputTypedSchema = prevSchema
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped, SerialOnly: true}
		return lib.WriteCodeFragment(frag)
	}

	code := fmt.Sprintf("%s := ssql.%s[ssql.Record](*flagLimit)(%s)", outputVar, fn, inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
