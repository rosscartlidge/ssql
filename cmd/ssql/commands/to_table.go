package commands

import (
	"fmt"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToTable registers the "to table" subcommand
func registerToTable(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("table").
		Description("Display as formatted table").
		Example("ssql from data.csv | ssql to table", "Display CSV as formatted table").
		Example("ssql from data.csv | ssql to table name age city", "Display with name, age, city first, then other fields").
		Example("ssql from data.csv | ssql to table -only name age", "Display only name and age columns").
		Example("ssql from data.csv | ssql to table -max-width 30", "Display with custom column width").
		Example("ssql from huge.csv | ssql to table --sample 50", "Stream output, infer widths from first 50 records").
		Example("ssql from data.csv | ssql to table --sample 0", "Materialize all records for perfect column widths").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-max-width").
			Int().
			Global().
			Default(50).
			Help("Maximum column width (truncate longer values)").
			Done().

		Flag("-sample").
			Int().
			Global().
			Default(100).
			Help("Records to sample for column widths (0 = materialize all)").
			Done().

		Flag("-only").
			Bool().
			Global().
			Help("Only show specified fields (hide others)").
			Done().

		Flag("FIELDS").
			String().
			Variadic().
			FieldsFromFlag("").
			Global().
			Help("Field names to display first (in order)").
			Done().

		Handler(func(ctx *cf.Context) error {
			var generate bool
			var maxWidth int
			var onlySpecified bool
			var sampleSize int
			var fields []string

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if widthVal, ok := ctx.GlobalFlags["-max-width"]; ok {
				maxWidth = widthVal.(int)
			}

			if onlyVal, ok := ctx.GlobalFlags["-only"]; ok {
				onlySpecified = onlyVal.(bool)
			}

			if sampleVal, ok := ctx.GlobalFlags["-sample"]; ok {
				sampleSize = sampleVal.(int)
			}

			if fieldsVal, ok := ctx.GlobalFlags["FIELDS"]; ok {
				if fieldsSlice, ok := fieldsVal.([]any); ok {
					for _, f := range fieldsSlice {
						if s, ok := f.(string); ok && s != "" {
							fields = append(fields, s)
						}
					}
				}
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToTableCode(maxWidth, fields, onlySpecified)
			}

			// Read all records from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// If no fields specified but schema is present, use schema field order
			if len(fields) == 0 && schemaAndRecords.Schema != nil {
				fields = schemaAndRecords.Schema.Fields
			}

			if sampleSize > 0 {
				ssql.DisplayTableStreaming(records, maxWidth, sampleSize, fields, onlySpecified)
			} else {
				ssql.DisplayTableWithFields(records, maxWidth, fields, onlySpecified)
			}
			return nil
		}).
		Done()
}

func generateToTableCode(maxWidth int, fields []string, onlySpecified bool) error {
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
	var prevIsStream bool
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
		prevIsStream = fragments[len(fragments)-1].IsStream
	} else {
		inputVar = "records"
	}

	if typedMode() {
		if prevSchema == nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'to table' has no typed input; %s does not yet support typed mode (Tier 2 or Tier 3) — drop -typed or refactor the pipeline", lastNamedCommand(fragments)))
		}
		// Tier 1: emit a tab-separated print loop. We don't try to
		// match ssql.DisplayTable's column-width alignment in v1 — too
		// much code for a feature most pipelines route through 'to
		// csv' anyway. Users who care about pretty alignment can pipe
		// the generated program's output through the `column` utility.
		readVar := inputVar
		imports := []string{"fmt"}
		if prevIsStream {
			readVar = inputVar + ".Serial()"
			imports = append(imports, "github.com/rosscartlidge/ssql/v4/typed")
		}
		code := generateTypedToTableCode(prevSchema, fields, onlySpecified, readVar)
		frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
		frag.InputTypedSchema = prevSchema
		return lib.WriteCodeFragment(frag)
	}

	var code string
	if len(fields) > 0 {
		// Generate code with field ordering
		quotedFields := make([]string, len(fields))
		for i, f := range fields {
			quotedFields[i] = fmt.Sprintf("%q", f)
		}
		fieldsStr := "[]string{" + joinStrings(quotedFields, ", ") + "}"
		code = fmt.Sprintf("ssql.DisplayTableWithFields(%s, %d, %s, %t)", inputVar, maxWidth, fieldsStr, onlySpecified)
	} else {
		// No fields specified - use simple DisplayTable
		code = fmt.Sprintf("ssql.DisplayTable(%s, %d)", inputVar, maxWidth)
	}
	frag := lib.NewFinalFragment(inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// generateTypedToTableCode emits a tab-separated print loop over the
// schema's struct fields. fields/onlySpecified select which columns
// are printed; if fields is empty, all schema fields are used.
func generateTypedToTableCode(schema *lib.TypedSchema, fields []string, onlySpecified bool, inputVar string) string {
	// Resolve which fields to print, in order.
	var selected []lib.TypedSchemaField
	if len(fields) == 0 {
		selected = schema.Fields
	} else {
		byName := make(map[string]lib.TypedSchemaField, len(schema.Fields))
		for _, f := range schema.Fields {
			byName[strings.ToLower(f.Name)] = f
		}
		seen := make(map[string]bool, len(fields))
		for _, name := range fields {
			if f, ok := byName[strings.ToLower(name)]; ok {
				selected = append(selected, f)
				seen[strings.ToLower(name)] = true
			}
		}
		if !onlySpecified {
			for _, f := range schema.Fields {
				if !seen[strings.ToLower(f.Name)] {
					selected = append(selected, f)
				}
			}
		}
	}

	var b strings.Builder
	// Header
	headers := make([]string, len(selected))
	for i, f := range selected {
		headers[i] = fmt.Sprintf("%q", f.Name)
	}
	b.WriteString(fmt.Sprintf("fmt.Println(%s)\n", strings.Join(headers, ` + "\t" + `)))
	b.WriteString(fmt.Sprintf("\tfor row := range %s {\n", inputVar))
	args := make([]string, len(selected))
	for i, f := range selected {
		args[i] = "row." + f.GoName
	}
	// Use %v for each — works for all of int64/float64/string/bool/time.Time.
	b.WriteString("\t\tfmt.Printf(\"")
	for i := range selected {
		if i > 0 {
			b.WriteString("\\t")
		}
		b.WriteString("%v")
	}
	b.WriteString("\\n\", ")
	b.WriteString(strings.Join(args, ", "))
	b.WriteString(")\n\t}")
	return b.String()
}
