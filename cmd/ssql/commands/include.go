package commands

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterInclude registers the include subcommand
func RegisterInclude(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	// Order behavior (DFC123 §7): neither consumes nor destroys record order.
	lib.DeclareOrder("include", lib.OrderTransparent)

	cmd.Subcommand("include").
		Description("Include only specified fields").
		Example("ssql from data.csv | ssql include name age", "Select only name and age columns").
		Example("ssql from users.json | ssql include email status | ssql to csv out.csv", "Extract email and status to CSV").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FIELDS").
			String().
			Variadic().
			FieldsFromFlag("").
			Global().
			Help("Fields to include").
			Done().

		Handler(func(ctx *cf.Context) error {
			if schemaMode() {
				return runSchemaModeTransform(ctx, "include")
			}

			var generate bool
			var fields []string

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if fieldsVal, ok := ctx.GlobalFlags["FIELDS"]; ok {
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
					fields = []string{v}
				}
			}

			if len(fields) == 0 {
				return fmt.Errorf("no fields specified")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateIncludeCode(fields)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// Validate field names against schema
			if err := validateFieldsSchema(schemaAndRecords.Schema, fields, "include"); err != nil {
				return err
			}

			// Build included fields map
			// Keep the named fields, in the order named (ssql.Project: the
			// same primitive generated record code calls).
			includer := func(r ssql.Record) ssql.Record {
				return ssql.Project(r, fields...)
			}

			// Apply inclusion
			included := ssql.Select(includer)(records)

			// Update schema to only include specified fields (in specified order)
			var outputSchema *lib.Schema
			if schemaAndRecords.Schema != nil {
				outputSchema = lib.NewSchema()
				for _, field := range fields {
					if schemaAndRecords.Schema.HasField(field) {
						outputSchema.AddField(field, schemaAndRecords.Schema.TypeOf(field))
					}
				}
			}

			// Write output as JSONL (preserving schema if present)
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outputSchema, included); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateIncludeCode generates Go code for the include command
func generateIncludeCode(fields []string) error {
	// Read all previous code fragments from stdin
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	// Pass through all previous fragments
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	// Get input variable from last fragment
	var inputVar string
	var prevSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
	} else {
		inputVar = "records"
	}

	// Phase B fall-through: prevSchema==nil → Record-mode upstream.
	if typedMode() && prevSchema != nil {
		// include is SerialOnly — planner inserts Stream.Serial()
		// upstream automatically when input is a Stream.
		// emitTypedProjection sets Capabilities.
		_, err := emitTypedProjection("include", "Subset", inputVar, prevSchema, fields, false, nil, fragments)
		return err
	}

	// The named fields, in the order named: ssql.Project, the primitive
	// exec's include calls too.
	outputVar := "included"
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = fmt.Sprintf("%q", f)
	}
	code := fmt.Sprintf(`%s := ssql.Select(func(r ssql.Record) ssql.Record {
		return ssql.Project(r, %s)
	})(%s)`, outputVar, strings.Join(quoted, ", "), inputVar)

	// Create stmt fragment
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
