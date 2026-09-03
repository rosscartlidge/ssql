package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// describeColumns is describe's fixed output schema (DFC122 Tier 1):
// one row per field. Numeric stats are ABSENT on non-numeric fields.
var describeColumns = []string{"field", "type", "count", "missing", "distinct", "min", "max", "mean", "median"}

func init() {
	// Schema op: describe replaces the schema with its fixed columns
	// whatever the input (pipeline-aware completion downstream sees
	// field/type/count/…).
	registerSchemaOp("describe", func(_ any, _ []string, _ []string) ([]string, bool) {
		return append([]string(nil), describeColumns...), true
	})
}

// RegisterDescribe registers the describe subcommand — the explorer's
// first command: a per-field profile of the stream.
func RegisterDescribe(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	// Order behavior (DFC123 §7): an aggregation — destroys record
	// order without consuming it (a sort feeding describe is dead).
	lib.DeclareOrder("describe", lib.OrderReset)

	cmd.Subcommand("describe").
		Description("Profile every field: type, count, missing, distinct, and min/max/mean/median for numeric fields (one row per field)").
		Example("ssql from data.csv | ssql describe | ssql to table", "Profile a file — the first thing to run on unfamiliar data").
		Example("ssql from data.csv | ssql describe salary age | ssql to table", "Profile only the named fields, in that order").
		Example("ssql from data.csv | ssql where -if status eq active | ssql describe | ssql to json", "Profile a filtered subset").

		Flag("FIELDS").
			String().
			Variadic().
			FieldsFromFlag("").
			Global().
			Help("Fields to profile (default: every field, in first-seen order)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			var fields []string
			if v, ok := ctx.GlobalFlags["FIELDS"]; ok {
				switch fv := v.(type) {
				case []string:
					fields = fv
				case []any:
					for _, item := range fv {
						if s, ok := item.(string); ok {
							fields = append(fields, s)
						}
					}
				default:
					// Fail loudly: a silently ignored FIELDS list would
					// describe everything and look like success (the
					// duckdb equivalence lane caught exactly that).
					return fmt.Errorf("describe: unexpected FIELDS value %T", v)
				}
			}
			var generate bool
			if v, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = v.(bool)
			}
			cfg := ssql.DescribeConfig{Fields: fields}

			if schemaMode() {
				return runSchemaModeTransform(ctx, "describe")
			}
			if shouldGenerate(generate) {
				return generateDescribeCode(cfg)
			}

			sr := lib.ReadJSONLWithSchema(ctx.Stdin())
			if len(fields) > 0 && sr.Schema != nil {
				if err := validateFieldsSchema(sr.Schema, fields, "describe"); err != nil {
					return err
				}
			}
			out := ssql.DescribeRecords(sr.Records, cfg)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), describeOutputSchema(), out); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}

func describeOutputSchema() *lib.Schema {
	return &lib.Schema{
		Fields: append([]string(nil), describeColumns...),
		Types: map[string]string{
			"field": lib.TypeString, "type": lib.TypeString,
			"count": lib.TypeInt, "missing": lib.TypeInt, "distinct": lib.TypeInt,
			"min": lib.TypeFloat, "max": lib.TypeFloat, "mean": lib.TypeFloat, "median": lib.TypeFloat,
		},
	}
}

// generateDescribeCode emits the record-shaped stage. describe's
// output is inherently heterogeneous per row (numeric stats present
// only for numeric fields), which is the record model's home turf — a
// typed struct would have to lie (zeros/NaN) for string fields. So in
// typed pipelines describe re-enters record mode at this stage (the
// planner's typed→Record boundary, as for pivot); there is no typed
// template by design, recorded in DFC122.
func generateDescribeCode(cfg ssql.DescribeConfig) error {
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
	outputVar := "described"
	var quoted []string
	for _, f := range cfg.Fields {
		quoted = append(quoted, fmt.Sprintf("%q", f))
	}
	code := fmt.Sprintf("%s := ssql.DescribeFilter(ssql.DescribeConfig{Fields: []string{%s}})(%s)",
		outputVar, joinStrings(quoted, ", "), inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	if frag.Op != nil {
		frag.Op.Fields = append([]string(nil), cfg.Fields...)
		frag.Op.Args = map[string]any{"fields": cfg.Fields}
	}
	return lib.WriteCodeFragment(frag)
}
