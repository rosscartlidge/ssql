package commands

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterSort registers the sort subcommand
func RegisterSort(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("sort").
		Description("Sort records by one or more fields").
		Example("ssql from data.csv | ssql sort name", "Sort by name ascending").
		Example("ssql from data.csv | ssql sort dept -desc salary", "Sort by dept descending, then salary ascending").
		Example("ssql from data.csv | ssql sort dept - salary -desc", "Sort by dept ascending, salary descending").
		ClauseDescription("Each clause specifies fields with a sort direction").

		Flag("FIELDS").
			String().
			Variadic().
			Required().
			FieldsFromFlag("").
			Local().
			Help("Fields to sort by").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-desc", "-d").
			Bool().
			Local().
			Help("Sort descending (use +desc for ascending)").
			Done().

		Flag("-asc", "-a").
			Bool().
			Local().
			Help("Sort ascending (default, use +asc for descending)").
			Done().

		Handler(func(ctx *cf.Context) error {
			var generate bool
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Parse clauses into OrderField slice
			var orderBy []ssql.OrderField
			seen := make(map[string]bool)

			for _, clause := range ctx.Clauses {
				var fields []string
				var desc bool

				if fieldsVal, ok := clause.Flags["FIELDS"]; ok {
					switch v := fieldsVal.(type) {
					case []string:
						fields = v
					case []any:
						for _, item := range v {
							if s, ok := item.(string); ok {
								fields = append(fields, s)
							}
						}
					case string:
						if v != "" {
							fields = []string{v}
						}
					}
				}

				if descVal, ok := clause.Flags["-desc"]; ok {
					desc = descVal.(bool)
				}

				for _, f := range fields {
					if seen[f] {
						return fmt.Errorf("sort: field %q specified more than once", f)
					}
					seen[f] = true
					orderBy = append(orderBy, ssql.OrderField{Field: f, Desc: desc})
				}
			}

			if len(orderBy) == 0 {
				return fmt.Errorf("no sort fields specified")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateSortCode(orderBy)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Validate sort fields against schema
			var fieldNames []string
			for _, of := range orderBy {
				fieldNames = append(fieldNames, of.Field)
			}
			if err := validateFieldsSchema(schemaAndRecords.Schema, fieldNames, "sort"); err != nil {
				return err
			}

			// Sort using SortRecords (proper cross-type comparison)
			result := ssql.SortRecords(orderBy)(records)

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), schemaAndRecords.Schema, result); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateSortCode generates Go code for the sort command
func generateSortCode(orderBy []ssql.OrderField) error {
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
	outputVar := "sorted"

	// Phase B fall-through: prevSchema==nil → Record-mode upstream.
	if typedMode() && prevSchema != nil {
		// Sort is SerialOnly — the planner inserts Stream.Serial()
		// upstream automatically when input is a Stream. The serial
		// codegen below always runs.
		if len(orderBy) == 0 {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'sort' requires at least one field"))
		}
		// Resolve every field, validating existence and type sortability.
		type sortField struct {
			f    lib.TypedSchemaField
			desc bool
		}
		fields := make([]sortField, 0, len(orderBy))
		for _, of := range orderBy {
			f, ok := lookupSchemaField(prevSchema, of.Field)
			if !ok {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: 'sort' references unknown field %q", of.Field))
			}
			if !isSortableGoType(f.GoType) {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: 'sort' on field %q (type %s) not supported (need int/float/string)", of.Field, f.GoType))
			}
			fields = append(fields, sortField{f: f, desc: of.Desc})
		}

		// Single-field path: prefer the simpler typed.SortBy /
		// typed.SortByDesc. Multi-field path: build a comparator
		// closure and use typed.SortByFunc.
		var code string
		if len(fields) == 1 {
			fn := "typed.SortBy"
			if fields[0].desc {
				fn = "typed.SortByDesc"
			}
			code = fmt.Sprintf("%s := %s(func(r %s) %s { return r.%s })(%s)",
				outputVar, fn, prevSchema.TypeName, fields[0].f.GoType, fields[0].f.GoName, inputVar)
		} else {
			// Build the comparator: each field contributes a < / > /
			// 0 chain. Descending fields swap a/b.
			var cmpBody strings.Builder
			for i, sf := range fields {
				lhs, rhs := "a."+sf.f.GoName, "b."+sf.f.GoName
				if sf.desc {
					lhs, rhs = rhs, lhs
				}
				if i > 0 {
					cmpBody.WriteString("\n\t\t")
				}
				cmpBody.WriteString(fmt.Sprintf(`if %s < %s { return -1 }; if %s > %s { return 1 }`, lhs, rhs, lhs, rhs))
			}
			cmpBody.WriteString("\n\t\treturn 0")
			code = fmt.Sprintf(`%s := typed.SortByFunc(func(a, b %s) int {
		%s
	})(%s)`, outputVar, prevSchema.TypeName, cmpBody.String(), inputVar)
		}

		frag := lib.NewStmtFragment(outputVar, inputVar, code, []string{"github.com/rosscartlidge/ssql/v4/typed"}, getCommandString())
		frag.InputTypedSchema = prevSchema
		frag.OutputTypedSchema = prevSchema
		frag.Capabilities = &lib.Capabilities{
			Accepts:    lib.ShapeSeqTyped,
			Produces:   lib.ShapeSeqTyped,
			SerialOnly: true,
		}
		return lib.WriteCodeFragment(frag)
	}

	// Build OrderField slice literal
	var fields []string
	for _, of := range orderBy {
		fields = append(fields, fmt.Sprintf(`{Field: %q, Desc: %v}`, of.Field, of.Desc))
	}
	code := fmt.Sprintf("%s := ssql.SortRecords([]ssql.OrderField{%s})(%s)",
		outputVar, strings.Join(fields, ", "), inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// isSortableGoType reports whether a field's Go type satisfies our
// typed.SortBy[T,K Ordered] constraint. Pointer types and time.Time
// don't satisfy cmp.Ordered directly; defer them to a future
// SortByFunc that takes a cmp closure.
func isSortableGoType(t string) bool {
	switch t {
	case "string", "int", "int32", "int64", "uint64", "float32", "float64":
		return true
	}
	return false
}
