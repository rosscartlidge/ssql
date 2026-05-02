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
	var prevRecordFields []string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
		prevIsStream = fragments[len(fragments)-1].IsStream
		prevRecordFields = fragments[len(fragments)-1].OutputRecordFields
	} else {
		inputVar = "records"
	}

	if typedMode() {
		if prevSchema == nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'to table' has no typed input; %s does not yet support typed mode (Tier 2 or Tier 3) — drop -typed or refactor the pipeline", lastNamedCommand(fragments)))
		}
		// Width-aligned table output. typed.WriteTableToWriter
		// uses struct-tag info to right-align numeric/bool columns
		// and left-align strings; the helper buffers all rows so it
		// can size columns exactly. Field selection / reordering
		// uses typed.WriteTableSelectedToWriter with one
		// TableColumn{Header, Format, RightAlign} per column.
		//
		// to_table only consumes iter.Seq[T] (the helper buffers all
		// rows for column sizing — there's no parallel-write
		// equivalent). Declare Accepts=ShapeSeqTyped + SerialOnly so
		// the planner inserts a Stream.Serial() boundary upstream
		// when the previous fragment produces a Stream.
		_ = prevIsStream
		imports := []string{"fmt", "github.com/rosscartlidge/ssql/v4/typed", "os"}
		code := generateTypedToTableCode(prevSchema, fields, onlySpecified, inputVar)
		frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
		frag.InputTypedSchema = prevSchema
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeNone, SerialOnly: true}
		return lib.WriteCodeFragment(frag)
	}

	var code string
	switch {
	case len(fields) > 0:
		// User explicitly specified column order
		quotedFields := make([]string, len(fields))
		for i, f := range fields {
			quotedFields[i] = fmt.Sprintf("%q", f)
		}
		fieldsStr := "[]string{" + joinStrings(quotedFields, ", ") + "}"
		code = fmt.Sprintf("ssql.DisplayTableWithFields(%s, %d, %s, %t)", inputVar, maxWidth, fieldsStr, onlySpecified)
	case len(prevRecordFields) > 0:
		// Upstream command (e.g. group-by) populated the natural
		// field order. Without this, the generated program would
		// render columns alphabetically — mismatching the CLI
		// pipeline's JSONL-schema-preserved order.
		quotedFields := make([]string, len(prevRecordFields))
		for i, f := range prevRecordFields {
			quotedFields[i] = fmt.Sprintf("%q", f)
		}
		fieldsStr := "[]string{" + joinStrings(quotedFields, ", ") + "}"
		code = fmt.Sprintf("ssql.DisplayTableWithFields(%s, %d, %s, false)", inputVar, maxWidth, fieldsStr)
	default:
		// No upstream order info; fall back to alphabetical (current
		// behaviour, matches DisplayTable's historical default).
		code = fmt.Sprintf("ssql.DisplayTable(%s, %d)", inputVar, maxWidth)
	}
	frag := lib.NewFinalFragment(inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// generateTypedToTableCode emits a width-aligned table writer.
// Calls typed.WriteTableToWriter when no field filter is in play
// (the common case — print everything in struct order); calls
// typed.WriteTableSelectedToWriter with a one-TableColumn-per-field
// list when fields/onlySpecified are set.
func generateTypedToTableCode(schema *lib.TypedSchema, fields []string, onlySpecified bool, inputVar string) string {
	// No field selection — let the helper read the struct schema.
	if len(fields) == 0 && !onlySpecified {
		return fmt.Sprintf(`if err := typed.WriteTableToWriter(%s, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "to table: %%v\n", err)
		os.Exit(1)
	}`, inputVar)
	}

	// Resolve fields in display order.
	var selected []lib.TypedSchemaField
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

	// Emit a TableColumn[<RowType>]-typed slice literal then call
	// typed.WriteTableSelectedToWriter.
	var b strings.Builder
	fmt.Fprintf(&b, "if err := typed.WriteTableSelectedToWriter(%s, os.Stdout, []typed.TableColumn[%s]{\n", inputVar, schema.TypeName)
	for _, f := range selected {
		right := isNumericOrBoolGoType(f.GoType)
		fmt.Fprintf(&b, "\t\t{Header: %q, Format: func(r *%s) string { return fmt.Sprintf(\"%%v\", r.%s) }, RightAlign: %t},\n",
			f.Name, schema.TypeName, f.GoName, right)
	}
	b.WriteString("\t}); err != nil {\n")
	b.WriteString("\t\tfmt.Fprintf(os.Stderr, \"to table: %v\\n\", err)\n")
	b.WriteString("\t\tos.Exit(1)\n")
	b.WriteString("\t}")
	return b.String()
}

func isNumericOrBoolGoType(t string) bool {
	switch t {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64",
		"bool":
		return true
	}
	return false
}
