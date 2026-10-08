package commands

import (
	"fmt"
	"iter"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func init() {
	// Schema op: the field set is unchanged (the list field now holds an
	// element); its type is decided from the data at run time.
	registerSchemaOp("explode", func(_ any, in []string, _ []string) ([]string, bool) {
		return in, true
	})
}

// RegisterExplode registers explode — one row per element of a nested
// list (SQL UNNEST; the inverse of `group-by -collect`). DFC144 Level 2.
func RegisterExplode(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	// Order behavior (DFC123 §7): row-local — a row's elements come out
	// in list order, in the row's place; transparent.
	lib.DeclareOrder("explode", lib.OrderTransparent)

	cmd.Subcommand("explode").
		Description("One row per element of a list field (SQL UNNEST): the field holds the element, the other fields repeat; an empty or missing list gives no row unless -keep-empty").
		Example("ssql from events.jsonl | ssql explode tags | ssql group-by tags -count n | ssql sort -desc n", "Count tag occurrences across rows whose tags field is a JSON list").
		Example("ssql from orders.csv | ssql group-by customer -collect product products | ssql explode products", "The inverse of -collect: one row per collected value").
		Example("ssql from events.jsonl | ssql explode tags -keep-empty | ssql to table", "Keep rows whose list is empty, with no value in the field").

		Flag("FIELD").
			String().
			Required().
			FieldsFromFlag("").
			Global().
			Help("The list field to explode").
			Done().

		Flag("-keep-empty").
			Bool().
			Global().
			Help("A row whose list is empty or missing comes out once, with no value in the field (default: no row)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			field, _ := ctx.GlobalFlags["FIELD"].(string)
			keepEmpty, _ := ctx.GlobalFlags["-keep-empty"].(bool)
			generate, _ := ctx.GlobalFlags["-generate"].(bool)
			if field == "" {
				return fmt.Errorf("explode: FIELD is required")
			}
			if schemaMode() {
				return runSchemaModeTransform(ctx, "explode")
			}
			if shouldGenerate(generate) {
				return generateExplodeCode(field, keepEmpty)
			}
			sr := lib.ReadJSONLWithSchema(ctx.Stdin())
			if sr.Schema != nil {
				if err := validateFieldsSchema(sr.Schema, []string{field}, "explode"); err != nil {
					return err
				}
			}
			out := ssql.Explode(field, keepEmpty)(sr.Records)
			// The field's wire type is the element's: learn it from the first
			// exploded row (a `json` list of ints becomes an int column).
			first, rest, ok := peekFirst(out)
			schema := sr.Schema
			if ok && schema != nil {
				schema = schema.Clone()
				if v, has := ssql.Get[any](first, field); has && v != nil {
					schema.Types[field] = lib.InferTypeString(v)
				}
			}
			return lib.WriteJSONLWithSchema(ctx.Stdout(), schema, rest)
		}).
		Done()
	return cmd
}

// generateExplodeCode emits the record-shaped stage; in a typed pipeline
// the planner inserts the typed→Record boundary before it.
func generateExplodeCode(field string, keepEmpty bool) error {
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
	code := fmt.Sprintf("exploded := ssql.Explode(%q, %v)(%s)", field, keepEmpty, inputVar)
	frag := lib.NewStmtFragment("exploded", inputVar, code, nil, getCommandString())
	if frag.Op != nil {
		frag.Op.Fields = []string{field}
		frag.Op.Args = map[string]any{"field": field, "keep_empty": keepEmpty}
	}
	if typedMode() && len(fragments) > 0 && fragments[len(fragments)-1].OutputTypedSchema != nil {
		frag.PlanNotes = []string{"record-shaped stage (explode has no typed form; the planner inserts the boundary)"}
	}
	return lib.WriteCodeFragment(frag)
}

// peekFirst pulls the first record of a sequence and returns it with a
// sequence that yields it again followed by the rest, so a writer can
// decide its schema header from the data before streaming.
func peekFirst(seq iter.Seq[ssql.Record]) (ssql.Record, iter.Seq[ssql.Record], bool) {
	next, stop := iter.Pull(seq)
	first, ok := next()
	if !ok {
		stop()
		return ssql.Record{}, func(func(ssql.Record) bool) {}, false
	}
	rest := func(yield func(ssql.Record) bool) {
		defer stop()
		if !yield(first) {
			return
		}
		for {
			r, ok := next()
			if !ok {
				return
			}
			if !yield(r) {
				return
			}
		}
	}
	return first, rest, true
}
