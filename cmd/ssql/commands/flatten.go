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
	// Schema op: the object field is removed (unless -keep); its keys are
	// data, so the new names are learnt from the records at run time and
	// are not known here.
	registerSchemaOp("flatten", func(_ any, in []string, args []string) ([]string, bool) {
		pos, flags := walkStage(args, map[string]int{"-depth": 1, "-keep": 0, "-generate": 0, "-g": 0})
		field := ""
		if len(pos) > 0 {
			field = pos[0]
		}
		keep := false
		for _, f := range flags {
			if f.name == "-keep" {
				keep = true
			}
		}
		var out []string
		for _, f := range in {
			if f != field || keep {
				out = append(out, f)
			}
		}
		return out, true
	})
}

// RegisterFlatten registers flatten — a nested object's keys become
// sibling fields named FIELD.key (DuckDB's `s.*`). DFC144 Level 2.
func RegisterFlatten(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	lib.DeclareOrder("flatten", lib.OrderTransparent)

	cmd.Subcommand("flatten").
		Description("Turn an object field's keys into fields named FIELD.key (SQL s.*); the keys are fixed by the first row's object, as a STRUCT's are").
		Example("ssql from events.jsonl | ssql flatten addr | ssql group-by addr.city -count n", "An address object becomes addr.city, addr.zip, … columns").
		Example("ssql from events.jsonl | ssql flatten meta -depth 2 -keep", "Two levels of keys (meta.a.b), keeping the original object too").

		Flag("FIELD").
			String().
			Required().
			FieldsFromFlag("").
			Global().
			Help("The object field to flatten").
			Done().

		Flag("-depth").
			Int().
			Global().
			Default(1).
			Help("How many levels of nested objects become fields (default 1); deeper values and lists stay JSON").
			Done().

		Flag("-keep").
			Bool().
			Global().
			Help("Keep the object field (default: it is replaced by its keys)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			field, _ := ctx.GlobalFlags["FIELD"].(string)
			depth := 1
			switch d := ctx.GlobalFlags["-depth"].(type) {
			case int:
				depth = d
			case int64:
				depth = int(d)
			case string:
				if n, err := strconv.Atoi(d); err == nil {
					depth = n
				}
			}
			keep, _ := ctx.GlobalFlags["-keep"].(bool)
			generate, _ := ctx.GlobalFlags["-generate"].(bool)
			if field == "" {
				return fmt.Errorf("flatten: FIELD is required")
			}
			if depth < 1 {
				return fmt.Errorf("flatten: -depth must be at least 1")
			}
			if schemaMode() {
				return runSchemaModeTransform(ctx, "flatten")
			}
			if shouldGenerate(generate) {
				return generateFlattenCode(field, depth, keep)
			}
			sr := lib.ReadJSONLWithSchema(ctx.Stdin())
			if sr.Schema != nil {
				if err := validateFieldsSchema(sr.Schema, []string{field}, "flatten"); err != nil {
					return err
				}
			}
			out := ssql.FlattenField(field, depth, keep)(sr.Records)
			// The new fields and their types come from the first row.
			first, rest, ok := peekFirst(out)
			schema := sr.Schema
			if ok && schema != nil {
				schema = flattenOutputSchema(schema, first, field, keep)
			}
			return lib.WriteJSONLWithSchema(ctx.Stdout(), schema, rest)
		}).
		Done()
	return cmd
}

// flattenOutputSchema: the input's fields minus the object (unless keep),
// then the first row's new FIELD.key fields in its order, typed from
// their values.
func flattenOutputSchema(in *lib.Schema, first ssql.Record, field string, keep bool) *lib.Schema {
	out := &lib.Schema{Types: map[string]string{}}
	for _, f := range in.Fields {
		if f == field && !keep {
			continue
		}
		out.Fields = append(out.Fields, f)
		out.Types[f] = in.Types[f]
	}
	prefix := field + "."
	for k := range first.KeysIter() {
		if !strings.HasPrefix(k, prefix) || out.HasField(k) {
			continue
		}
		out.Fields = append(out.Fields, k)
		if v, ok := ssql.Get[any](first, k); ok && v != nil {
			out.Types[k] = lib.InferTypeString(v)
		} else {
			out.Types[k] = "any"
		}
	}
	return out
}

func generateFlattenCode(field string, depth int, keep bool) error {
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
	code := fmt.Sprintf("flattened := ssql.FlattenField(%q, %d, %v)(%s)", field, depth, keep, inputVar)
	frag := lib.NewStmtFragment("flattened", inputVar, code, nil, getCommandString())
	if frag.Op != nil {
		frag.Op.Fields = []string{field}
		frag.Op.Args = map[string]any{"field": field, "depth": depth, "keep": keep}
	}
	if typedMode() && len(fragments) > 0 && fragments[len(fragments)-1].OutputTypedSchema != nil {
		frag.PlanNotes = []string{"record-shaped stage (flatten has no typed form; the planner inserts the boundary)"}
	}
	return lib.WriteCodeFragment(frag)
}
