package commands

// ssql resample (DFC121): snap a timestamp field to an epoch-aligned
// grid, filling one or more numeric value fields via previous (LOCF),
// next, or linear interpolation. Semantics live in
// ssql.ResampleRecords — exec, record codegen, and typed codegen all
// call the SAME implementation (the typed template shims records at
// the barrier and re-enters typed with a synthesized output struct),
// so the lanes cannot drift.

import (
	"fmt"
	"os"
	"strings"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func init() {
	registerSchemaOp("resample", func(_ any, in []string, args []string) ([]string, bool) {
		_, flags := walkStage(args, map[string]int{
			"-time": 1, "-every": 1, "-value": 1, "-fill": 1,
			"-from": 1, "-to": 1, "-time-unit": 1, "-time-format": 1,
			"-generate": 0, "-g": 0,
		})
		var timeField string
		var values []string
		for _, f := range flags {
			switch f.name {
			case "-time":
				if len(f.args) > 0 {
					timeField = f.args[0]
				}
			case "-value":
				if len(f.args) > 0 {
					values = append(values, f.args[0])
				}
			}
		}
		if timeField == "" || len(values) == 0 {
			return in, false
		}
		return append([]string{timeField}, values...), true
	})
}

// RegisterResample registers the resample subcommand.
func RegisterResample(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("resample").
		Description("Snap a timestamp field to a regular epoch-aligned grid, filling numeric values (previous/next/linear)").
		Example("ssql from metrics.csv | ssql resample -time ts -every 1m -value cpu | ssql to chart -type line -x ts -y cpu",
			"One-minute grid with last-observation-carried-forward").
		Example("ssql from sensors.csv | ssql resample -time ts -every 5s -value temp -value rpm -fill linear",
			"Interpolate two series onto a 5-second grid").
		Example("ssql from events.jsonl | ssql resample -time when -every 1h -value n -fill next -from 2026-01-01T00:00:00Z",
			"Hourly grid from an explicit start, backfilled").
		Flag("-time").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Global().
		Required().
		Help("Timestamp field (int64/float64 epoch — unit auto-detected loudly — or RFC3339/SQL datetime strings)").
		Done().
		Flag("-every").
		String().
		Global().
		Required().
		Help("Sample period as a Go duration (5s, 1m, 2h30m)").
		Done().
		Flag("-value").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Accumulate().
		Global().
		Required().
		Help("Numeric field to carry onto the grid (repeat for multiple)").
		Done().
		Flag("-fill").
		String().
		Completer(&cf.StaticCompleter{Options: []string{"previous", "next", "linear"}}).
		Default("previous").
		Global().
		Help("Fill mode: previous (LOCF, default), next (backfill), linear (interpolate); edges clamp loudly").
		Done().
		Flag("-from").
		String().
		Global().
		Help("Grid start bound (same format as the data); snapped onto the epoch grid, loudly").
		Done().
		Flag("-to").
		String().
		Global().
		Help("Grid end bound (same format as the data); snapped onto the epoch grid, loudly").
		Done().
		Flag("-time-unit").
		String().
		Completer(&cf.StaticCompleter{Options: []string{"ns", "us", "ms", "s"}}).
		Global().
		Help("Epoch unit override (default: auto-detected by magnitude, announced on stderr)").
		Done().
		Flag("-time-format").
		String().
		Global().
		Help("Go time layout for string timestamps outside RFC3339/SQL datetime").
		Done().
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Handler(func(ctx *cf.Context) error {
			if schemaMode() {
				return runSchemaModeTransform(ctx, "resample")
			}

			cfg := ssql.ResampleConfig{}
			cfg.TimeField, _ = ctx.GlobalFlags["-time"].(string)
			everyStr, _ := ctx.GlobalFlags["-every"].(string)
			cfg.Fill, _ = ctx.GlobalFlags["-fill"].(string)
			cfg.From, _ = ctx.GlobalFlags["-from"].(string)
			cfg.To, _ = ctx.GlobalFlags["-to"].(string)
			cfg.TimeUnit, _ = ctx.GlobalFlags["-time-unit"].(string)
			cfg.TimeFormat, _ = ctx.GlobalFlags["-time-format"].(string)
			if vv, ok := ctx.GlobalFlags["-value"].([]any); ok {
				for _, v := range vv {
					if s, ok := v.(string); ok && s != "" {
						cfg.Values = append(cfg.Values, s)
					}
				}
			}
			every, err := time.ParseDuration(everyStr)
			if err != nil || every <= 0 {
				return fmt.Errorf("resample: -every must be a positive Go duration (5s, 1m, …), got %q", everyStr)
			}
			cfg.Every = every

			var generate bool
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if shouldGenerate(generate) {
				return generateResampleCode(cfg)
			}

			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			checkFields := append([]string{cfg.TimeField}, cfg.Values...)
			if err := validateFieldsSchema(schemaAndRecords.Schema, checkFields, "resample"); err != nil {
				return err
			}
			result, err := ssql.ResampleRecords(schemaAndRecords.Records, cfg)
			if err != nil {
				return err
			}
			outSchema := resampleOutputSchema(schemaAndRecords.Schema, cfg)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outSchema, result); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}

// resampleOutputSchema: the time field keeps its input type; every
// value field becomes float64 (interpolation's type).
func resampleOutputSchema(in *lib.Schema, cfg ssql.ResampleConfig) *lib.Schema {
	out := &lib.Schema{
		Fields: append([]string{cfg.TimeField}, cfg.Values...),
		Types:  map[string]string{},
	}
	tsType := "int"
	if in != nil && in.Types != nil {
		if t, ok := in.Types[cfg.TimeField]; ok {
			tsType = t
		}
	}
	out.Types[cfg.TimeField] = tsType
	for _, v := range cfg.Values {
		out.Types[v] = "float"
	}
	return out
}

// resampleConfigLiteral renders the config as a Go literal for
// generated code — the fragment calls the SAME ResampleRecords.
func resampleConfigLiteral(cfg ssql.ResampleConfig) string {
	var vals []string
	for _, v := range cfg.Values {
		vals = append(vals, fmt.Sprintf("%q", v))
	}
	return fmt.Sprintf(
		"ssql.ResampleConfig{TimeField: %q, Every: %d, Values: []string{%s}, Fill: %q, From: %q, To: %q, TimeUnit: %q, TimeFormat: %q}",
		cfg.TimeField, cfg.Every, strings.Join(vals, ", "), cfg.Fill, cfg.From, cfg.To, cfg.TimeUnit, cfg.TimeFormat)
}

func generateResampleCode(cfg ssql.ResampleConfig) error {
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
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	}
	outputVar := "resampled"

	if typedMode() && prevSchema != nil {
		return generateResampleTyped(cfg, fragments, inputVar, outputVar, prevSchema)
	}

	// Record mode: filter-shaped (the assembler composes stmt
	// fragments via ssql.Chain), one call into the shared
	// implementation.
	code := fmt.Sprintf("%s := ssql.ResampleFilter(%s)(%s)",
		outputVar, resampleConfigLiteral(cfg), inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	stampResampleOp(frag, cfg)
	return lib.WriteCodeFragment(frag)
}

// stampResampleOp records resample's SEMANTIC config on the
// fragment's Op (DFC123 slice 3) so backends that cannot execute Go —
// the SQL translator — read the command's own parse instead of
// re-implementing its flag grammar (aliases, defaults, accumulation).
// The command parses its flags ONCE; a changed default here can no
// longer drift silently in generate sql.
func stampResampleOp(frag *lib.CodeFragment, cfg ssql.ResampleConfig) {
	if frag.Op == nil {
		return
	}
	frag.Op.Fields = append([]string{cfg.TimeField}, cfg.Values...)
	frag.Op.Args = map[string]any{
		"time":   cfg.TimeField,
		"every":  cfg.Every, // nanoseconds
		"values": cfg.Values,
		"fill":   cfg.Fill,
	}
	if cfg.From != "" {
		frag.Op.Args["from"] = cfg.From
	}
	if cfg.To != "" {
		frag.Op.Args["to"] = cfg.To
	}
	if cfg.TimeUnit != "" {
		frag.Op.Args["time_unit"] = cfg.TimeUnit
	}
	if cfg.TimeFormat != "" {
		frag.Op.Args["time_format"] = cfg.TimeFormat
	}
}

// generateResampleTyped: typed-in, typed-out. The barrier shims T →
// Record, runs the ONE implementation, and re-enters typed with a
// synthesized output struct (time keeps its Go type, values are
// float64) — results are identical to exec/record by construction; a
// native typed merge is a later optimization.
func generateResampleTyped(cfg ssql.ResampleConfig, fragments []*lib.CodeFragment, inputVar, outputVar string, prevSchema *lib.TypedSchema) error {
	tsField, ok := lookupSchemaField(prevSchema, cfg.TimeField)
	if !ok {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: 'resample' references unknown field %q", cfg.TimeField))
	}
	switch tsField.GoType {
	case "int64", "float64", "string":
	default:
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: 'resample' time field %q has type %s (need int64/float64/string)", cfg.TimeField, tsField.GoType))
	}
	var valFields []lib.TypedSchemaField
	for _, v := range cfg.Values {
		f, ok := lookupSchemaField(prevSchema, v)
		if !ok {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'resample' references unknown field %q", v))
		}
		switch f.GoType {
		case "int64", "float64", "int", "int32", "float32":
		default:
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'resample' value field %q has type %s (need a numeric field)", v, f.GoType))
		}
		valFields = append(valFields, f)
	}

	typeName := fmt.Sprintf("Resampled%d", len(fragments))
	var def strings.Builder
	fmt.Fprintf(&def, "// %s is the synthesized resample output row (DFC121).\n", typeName)
	fmt.Fprintf(&def, "type %s struct {\n", typeName)
	fmt.Fprintf(&def, "\t%s %s `ssql:%q`\n", tsField.GoName, tsField.GoType, cfg.TimeField)
	for i, f := range valFields {
		fmt.Fprintf(&def, "\t%s float64 `ssql:%q`\n", f.GoName, cfg.Values[i])
	}
	def.WriteString("}")
	// Shim in: T → Record with just the fields resample needs.
	var shimSets strings.Builder
	switch tsField.GoType {
	case "int64":
		fmt.Fprintf(&shimSets, ".Int(%q, r.%s)", cfg.TimeField, tsField.GoName)
	case "float64":
		fmt.Fprintf(&shimSets, ".Float(%q, r.%s)", cfg.TimeField, tsField.GoName)
	case "string":
		fmt.Fprintf(&shimSets, ".String(%q, r.%s)", cfg.TimeField, tsField.GoName)
	}
	for i, f := range valFields {
		if f.GoType == "float64" {
			fmt.Fprintf(&shimSets, ".Float(%q, r.%s)", cfg.Values[i], f.GoName)
		} else {
			fmt.Fprintf(&shimSets, ".Float(%q, float64(r.%s))", cfg.Values[i], f.GoName)
		}
	}
	// Shim out: Record → synthesized struct.
	var outSets strings.Builder
	switch tsField.GoType {
	case "int64":
		fmt.Fprintf(&outSets, "%s: ssql.GetOr(rec, %q, int64(0)),", tsField.GoName, cfg.TimeField)
	case "float64":
		fmt.Fprintf(&outSets, "%s: ssql.GetOr(rec, %q, float64(0)),", tsField.GoName, cfg.TimeField)
	case "string":
		fmt.Fprintf(&outSets, "%s: ssql.GetOr(rec, %q, \"\"),", tsField.GoName, cfg.TimeField)
	}
	for i, f := range valFields {
		fmt.Fprintf(&outSets, " %s: ssql.GetOr(rec, %q, float64(0)),", f.GoName, cfg.Values[i])
	}

	code := fmt.Sprintf(`%sRecs, err := ssql.ResampleRecords(func(yield func(ssql.Record) bool) {
		for r := range %s {
			if !yield(ssql.MakeMutableRecord()%s.Freeze()) {
				return
			}
		}
	}, %s)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	%s := func(yield func(%s) bool) {
		for rec := range %sRecs {
			if !yield(%s{%s}) {
				return
			}
		}
	}`, outputVar, inputVar, shimSets.String(), resampleConfigLiteral(cfg),
		outputVar, typeName, outputVar, typeName, outSets.String())

	outSchema := &lib.TypedSchema{TypeName: typeName}
	outSchema.Fields = append(outSchema.Fields, lib.TypedSchemaField{
		Name: cfg.TimeField, GoName: tsField.GoName, GoType: tsField.GoType,
	})
	for i, f := range valFields {
		outSchema.Fields = append(outSchema.Fields, lib.TypedSchemaField{
			Name: cfg.Values[i], GoName: f.GoName, GoType: "float64",
		})
	}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, []string{"fmt", "os"}, getCommandString())
	stampResampleOp(frag, cfg)
	frag.StructDefs = []string{def.String()}
	frag.InputTypedSchema = prevSchema
	frag.OutputTypedSchema = outSchema
	frag.Capabilities = &lib.Capabilities{
		Accepts:    lib.ShapeSeqTyped,
		Produces:   lib.ShapeSeqTyped,
		SerialOnly: true,
	}
	return lib.WriteCodeFragment(frag)
}

var _ = os.Stderr
