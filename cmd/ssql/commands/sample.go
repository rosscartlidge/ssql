package commands

import (
	"fmt"
	"os"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterSample registers the sample subcommand (DFC110): seeded
// random row sampling — sample N (exact count, reservoir) or
// -percent P (Bernoulli, streaming). Selection is a pure function of
// (seed, row index) via the spec-stable RNG in the ssql package, so a
// seeded sample is byte-identical across every backend.
func RegisterSample(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("sample").
		Description("Random row sample (SQL TABLESAMPLE); N rows exactly or -percent P; neither = pass-through, 0 = no records").
		Example("ssql from big.csv | ssql sample 1000", "Uniform 1000-row sample (seed printed to stderr for reproducibility)").
		Example("ssql from big.csv | ssql sample -percent 5 | ssql to table", "Keep ~5% of rows, streaming").
		Example("ssql from big.csv | ssql sample 1000 -seed 42", "Reproducible sample — identical rows every run and in generated code").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("N").
			Int().
			Global().
			Default(-1).
			Help("Number of rows to keep (exact, uniform); omit N and -percent to dial the stage off (pass-through); 0 = no records").
			Done().

		Flag("-percent", "-p").
			Float().
			Global().
			Default(float64(-1)).
			Help("Keep each row independently with this probability (0 < P <= 100)").
			Done().

		Flag("-seed").
			Int().
			Global().
			Default(0).
			Help("RNG seed for reproducible sampling; when omitted, a seed is chosen and printed to stderr").
			Done().

		Handler(func(ctx *cf.Context) error {
			n, _ := ctx.GlobalFlags["N"].(int)
			percent, _ := ctx.GlobalFlags["-percent"].(float64)
			seed, _ := ctx.GlobalFlags["-seed"].(int)
			generate, _ := ctx.GlobalFlags["-generate"].(bool)
			seedGiven := flagWasProvided(ctx, "-seed")

			haveN := n >= 0
			havePct := percent >= 0
			if haveN && havePct {
				return fmt.Errorf("sample: give N or -percent, not both (e.g. `sample 1000` or `sample -percent 5`)")
			}
			if havePct && (percent == 0 || percent > 100) {
				return fmt.Errorf("sample: -percent must be in (0, 100], got %v (omit N and -percent for pass-through)", percent)
			}
			// Neither given = the stage is dialled off: pass everything
			// through (the same convention as a bare `limit`). Until
			// v4.92.0 the dial was `sample 0`, which now means no records.
			passThrough := !haveN && !havePct

			if shouldGenerate(generate) {
				return generateSampleCode(n, percent, haveN, passThrough, int64(seed), seedGiven)
			}

			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			if passThrough {
				return lib.WriteJSONLWithSchema(ctx.Stdout(), schemaAndRecords.Schema, records)
			}

			resolvedSeed := resolveSampleSeed(int64(seed), seedGiven, "-seed")
			if haveN {
				records = ssql.SampleN[ssql.Record](n, resolvedSeed)(records)
			} else {
				records = ssql.SamplePercent[ssql.Record](percent, resolvedSeed)(records)
			}
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), schemaAndRecords.Schema, records); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}

// resolveSampleSeed returns the user's seed, or picks one and SAYS SO —
// loud reproducibility: every exploratory sample can be re-run.
func resolveSampleSeed(seed int64, given bool, seedFlag string) int64 {
	if given {
		return seed
	}
	s := time.Now().UnixNano()
	// seedFlag names the CALLER'S flag — `sample` takes -seed, `from
	// … -sample` takes -sample-seed; a hint naming the wrong flag
	// sends the user to an unknown-flag error (found by Ross).
	fmt.Fprintf(os.Stderr, "sample: seed %d (pass %s %d to reproduce)\n", s, seedFlag, s)
	return s
}

// flagWasProvided reports whether the user explicitly passed the flag
// (vs its default) by scanning the invoking argv.
func flagWasProvided(_ *cf.Context, name string) bool {
	for _, a := range os.Args[1:] {
		if a == name {
			return true
		}
	}
	return false
}

// generateSampleCode emits the sample stage for record/typed codegen.
// The RESOLVED seed is baked as the -seed param default (overridable
// at runtime), keeping generated programs reproducible even when the
// CLI invocation was unseeded. In schema mode this never runs — the
// exec path is already an identity transform over the header.
func generateSampleCode(n int, percent float64, haveN, passThrough bool, seed int64, seedGiven bool) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}
	// bare `sample` = pass-through: the stage vanishes from generated code.
	if passThrough {
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
	outputVar := "sampled"
	resolvedSeed := resolveSampleSeed(seed, seedGiven, "-seed")
	params := []lib.CodeParam{
		{Name: "sample-seed", Default: fmt.Sprintf("%d", resolvedSeed), Help: "sampling RNG seed", VarName: "flagSampleSeed", Type: "int"},
	}

	rowType := "ssql.Record"
	var extraImports []string
	if typedMode() && prevSchema != nil {
		rowType = prevSchema.TypeName
		// The typed assembler auto-imports only the typed package; the
		// sampling primitives live in the root package.
		extraImports = []string{"github.com/rosscartlidge/ssql/v4"}
	}

	var code string
	if haveN {
		code = fmt.Sprintf("%s := ssql.SampleN[%s](%d, int64(*flagSampleSeed))(%s)", outputVar, rowType, n, inputVar)
	} else {
		code = fmt.Sprintf("%s := ssql.SamplePercent[%s](%v, int64(*flagSampleSeed))(%s)", outputVar, rowType, percent, inputVar)
	}
	frag := lib.NewStmtFragment(outputVar, inputVar, code, extraImports, getCommandString())
	frag.Params = params
	if typedMode() && prevSchema != nil {
		// SerialOnly (DFC110 v1): index-driven selection needs a global
		// row order the parallel shards don't have. The planner inserts
		// Stream.Serial() upstream; sampling reduces data, so downstream
		// can re-enter parallelism where profitable.
		frag.InputTypedSchema = prevSchema
		frag.OutputTypedSchema = prevSchema
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped, SerialOnly: true}
	}
	return lib.WriteCodeFragment(frag)
}
