package commands

import (
	"fmt"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func init() {
	// Schema op: identity plus any -default field not already present.
	registerSchemaOp("fill", func(_ any, in []string, args []string) ([]string, bool) {
		_, flags := walkStage(args, map[string]int{"-down": 1, "-default": 2, "-generate": 0, "-g": 0})
		out := append([]string(nil), in...)
		have := map[string]bool{}
		for _, f := range in {
			have[f] = true
		}
		for _, f := range flags {
			if f.name == "-default" && len(f.args) > 0 && !have[f.args[0]] {
				out = append(out, f.args[0])
				have[f.args[0]] = true
			}
		}
		return out, true
	})
}

// RegisterFill registers fill — carry values down over gaps and/or
// default missing values (mlr fill-down / fill-empty).
func RegisterFill(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	// Order behavior (DFC123 §7): -down carries the PREVIOUS record's
	// value, so fill consumes order (a sort before it is live). The
	// declaration is per command, so the conservative union applies
	// even to -default-only invocations.
	lib.DeclareOrder("fill", lib.OrderConsumes)

	cmd.Subcommand("fill").
		Description("Fill missing values: carry the last seen value down (-down) and/or default them (-default FIELD VALUE); missing = absent, null, or empty").
		Example("ssql from sheet.csv | ssql fill -down region", "Merged-cell exports: the region appears only on a block's first row — carry it down").
		Example("ssql from data.csv | ssql fill -default status unknown -default score 0", "Give missing values a constant").
		Example("ssql from log.csv | ssql sort ts | ssql fill -down host -default level info", "Carry host forward in time order; default the level").

		Flag("-down").
			String().
			Accumulate().
			FieldsFromFlag("").
			Global().
			Help("Field to carry forward over missing values (repeat for several); applied before -default").
			Done().

		Flag("-default").
			Arg("field").
				FieldsFromFlag("").
				Done().
			Arg("value").
				Completer(cf.NoCompleter{Hint: "<value>"}).
				Done().
			Accumulate().
			Global().
			Help("Give FIELD the constant VALUE where it is missing (repeat for several)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			var cfg ssql.FillConfig
			cfg.Down = stringList(ctx.GlobalFlags["-down"])
			var rawDefaults [][2]string
			if v, ok := ctx.GlobalFlags["-default"]; ok {
				items, ok := v.([]any)
				if !ok {
					return fmt.Errorf("fill: unexpected -default value %T", v)
				}
				for _, it := range items {
					m, ok := it.(map[string]any)
					if !ok {
						return fmt.Errorf("fill: unexpected -default entry %T", it)
					}
					f, _ := m["field"].(string)
					val, _ := m["value"].(string)
					if f == "" {
						return fmt.Errorf("fill: -default needs FIELD VALUE")
					}
					rawDefaults = append(rawDefaults, [2]string{f, val})
				}
			}
			var generate bool
			if v, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = v.(bool)
			}
			if len(cfg.Down) == 0 && len(rawDefaults) == 0 {
				return fmt.Errorf("fill: nothing to do — give -down FIELD and/or -default FIELD VALUE")
			}

			if schemaMode() {
				return runSchemaModeTransform(ctx, "fill")
			}

			sr := (*lib.SchemaAndRecords)(nil)
			var schema *lib.Schema
			if !shouldGenerate(generate) {
				sr = lib.ReadJSONLWithSchema(ctx.Stdin())
				schema = sr.Schema
				if schema != nil {
					if err := validateFieldsSchema(schema, cfg.Down, "fill"); err != nil {
						return err
					}
				}
			}
			// Default literals take the field's schema type when known
			// (so `-default score 0` on an int column is int64 0), else
			// the literal's own inferred type.
			for _, d := range rawDefaults {
				cfg.Defaults = append(cfg.Defaults, ssql.FillDefault{Field: d[0], Value: fillLiteral(d[1], schemaTypeOf(schema, d[0]))})
			}

			if shouldGenerate(generate) {
				return generateFillCode(cfg, rawDefaults)
			}
			out := ssql.FillRecords(sr.Records, cfg)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), fillOutputSchema(schema, cfg), out); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}

func schemaTypeOf(s *lib.Schema, field string) string {
	if s == nil || s.Types == nil {
		return ""
	}
	return s.Types[field]
}

// fillLiteral parses a -default VALUE: by the field's schema type when
// known, otherwise int → float → bool → string inference (the CSV
// reader's own order).
func fillLiteral(s, schemaType string) any {
	switch schemaType {
	case lib.TypeString:
		return s
	case lib.TypeInt:
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
	case lib.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	case lib.TypeBool:
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if s == "true" || s == "false" {
		return s == "true"
	}
	return s
}

func fillOutputSchema(in *lib.Schema, cfg ssql.FillConfig) *lib.Schema {
	if in == nil {
		return nil
	}
	out := &lib.Schema{Fields: append([]string(nil), in.Fields...), Types: map[string]string{}}
	for k, v := range in.Types {
		out.Types[k] = v
	}
	have := map[string]bool{}
	for _, f := range in.Fields {
		have[f] = true
	}
	for _, d := range cfg.Defaults {
		if !have[d.Field] {
			out.Fields = append(out.Fields, d.Field)
			have[d.Field] = true
			switch d.Value.(type) {
			case int64:
				out.Types[d.Field] = lib.TypeInt
			case float64:
				out.Types[d.Field] = lib.TypeFloat
			case bool:
				out.Types[d.Field] = lib.TypeBool
			default:
				out.Types[d.Field] = lib.TypeString
			}
		}
	}
	return out
}

func goLiteral(v any) string {
	switch x := v.(type) {
	case string:
		return fmt.Sprintf("%q", x)
	case int64:
		return fmt.Sprintf("int64(%d)", x)
	case float64:
		return fmt.Sprintf("float64(%v)", x)
	case bool:
		return fmt.Sprintf("%v", x)
	}
	return fmt.Sprintf("%q", fmt.Sprintf("%v", v))
}

// generateFillCode emits the record-shaped stage. Typed structs cannot
// represent a missing value (DFC124 §3), so there is no typed template
// by design: typed pipelines re-enter record mode here via the planner
// boundary, as for describe.
func generateFillCode(cfg ssql.FillConfig, rawDefaults [][2]string) error {
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
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	}
	outputVar := "filled"
	var down []string
	for _, f := range cfg.Down {
		down = append(down, fmt.Sprintf("%q", f))
	}
	var defs []string
	for _, d := range cfg.Defaults {
		defs = append(defs, fmt.Sprintf("{Field: %q, Value: %s}", d.Field, goLiteral(d.Value)))
	}
	code := fmt.Sprintf("%s := ssql.FillFilter(ssql.FillConfig{Down: []string{%s}, Defaults: []ssql.FillDefault{%s}})(%s)",
		outputVar, strings.Join(down, ", "), strings.Join(defs, ", "), inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	if frag.Op != nil {
		var pairs []any
		for _, d := range rawDefaults {
			pairs = append(pairs, map[string]any{"field": d[0], "value": d[1]})
		}
		frag.Op.Fields = append([]string(nil), cfg.Down...)
		for _, d := range rawDefaults {
			frag.Op.Fields = append(frag.Op.Fields, d[0])
		}
		frag.Op.Args = map[string]any{"down": cfg.Down, "defaults": pairs}
	}
	return lib.WriteCodeFragment(frag)
}
