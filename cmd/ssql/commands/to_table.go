package commands

import (
	"fmt"
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
			Help("Maximum column width; longer values end in \"...\". 0 = no truncation, otherwise at least 3").
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

		Flag("-time-format").
			String().
			Global().
			Default("").
			Completer(&cf.StaticCompleter{Options: []string{"%Y-%m-%d", "%Y-%m-%d %H:%M", "%d/%m/%Y", "%H:%M:%S", "%b %-d, %Y", "2006-01-02"}}).
			Help("Render time columns with a pattern on the pipeline zone's wall clock: -time-format '%Y-%m-%d %H:%M' (strftime, or a Go layout). Default: RFC 3339").
			Done().

		Flag("FIELDS").
			String().
			Variadic().
			FieldsFromFlag("").
			Global().
			Help("Field names to display first (in order)").
			Done().

		Handler(func(ctx *cf.Context) error {
			if schemaMode() {
				return runSchemaModeTransform(ctx, "to") // a sink passes the schema through
			}
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
			// 0 = no truncation; a positive cap needs room for the "...". Any
			// other value used to panic deep in the renderer (slice bounds).
			if maxWidth < 0 || (maxWidth > 0 && maxWidth < 3) {
				return fmt.Errorf("to table: -max-width must be 0 (no truncation) or at least 3 (room for \"...\"), got %d", maxWidth)
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

			timeFormat, err := sinkTimeFormat(ctx)
			if err != nil {
				return err
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToTableCode(maxWidth, fields, onlySpecified, timeFormat)
			}

			// Read all records from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			// If no fields specified but schema is present, use schema field order
			if len(fields) == 0 && schemaAndRecords.Schema != nil {
				fields = schemaAndRecords.Schema.Fields
			}

			ssql.DisplayTableOpts(ctx.Stdout(), records, ssql.DisplayOptions{
				MaxWidth: maxWidth, Fields: fields, OnlySpecified: onlySpecified, Sample: sampleSize, TimeFormat: timeFormat,
			})
			return nil
		}).
		Done()
}

func generateToTableCode(maxWidth int, fields []string, onlySpecified bool, timeFormat string) error {
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

	// Phase B fall-through: prevSchema==nil → Record-mode upstream
	// (e.g. pivot/signal). Falls through to ssql.DisplayTable below.
	// A dotted path among the columns (addr.city) has no typed form; the
	// typed table silently dropped it until 2026-10-09 — record mode
	// resolves it through ssql.Get (DFC144 Level 2).
	if nestedPathIn(prevSchema, fields...) {
		prevSchema = nil
	}
	if typedMode() && prevSchema != nil {
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
		code, needsSSQL := generateTypedToTableCode(prevSchema, fields, onlySpecified, inputVar, maxWidth, timeFormat)
		if needsSSQL {
			imports = append(imports, "github.com/rosscartlidge/ssql/v4")
		}
		frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
		frag.InputTypedSchema = prevSchema
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeNone, SerialOnly: true}
		return lib.WriteCodeFragment(frag)
	}

	// Column order: the user's fields, else the upstream command's natural
	// order (e.g. group-by; without it the generated program rendered
	// columns alphabetically, mismatching the CLI's schema-preserved
	// order), else alphabetical (DisplayTable's historical default).
	order, only := fields, onlySpecified
	if len(order) == 0 {
		order, only = prevRecordFields, false
	}
	fieldsStr := "nil"
	if len(order) > 0 {
		quotedFields := make([]string, len(order))
		for i, f := range order {
			quotedFields[i] = fmt.Sprintf("%q", f)
		}
		fieldsStr = "[]string{" + joinStrings(quotedFields, ", ") + "}"
	}
	var code string
	var imports []string
	switch {
	case timeFormat != "":
		code = fmt.Sprintf("ssql.DisplayTableOpts(os.Stdout, %s, ssql.DisplayOptions{MaxWidth: %d, Fields: %s, OnlySpecified: %t, TimeFormat: %q})", inputVar, maxWidth, fieldsStr, only, timeFormat)
		imports = []string{"os"}
	case len(order) > 0:
		code = fmt.Sprintf("ssql.DisplayTableWithFields(%s, %d, %s, %t)", inputVar, maxWidth, fieldsStr, only)
	default:
		code = fmt.Sprintf("ssql.DisplayTable(%s, %d)", inputVar, maxWidth)
	}
	frag := lib.NewFinalFragment(inputVar, code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}

// generateTypedToTableCode emits a width-aligned table writer.
// Calls typed.WriteTableToWriter when no field filter is in play
// (the common case — print everything in struct order); calls
// typed.WriteTableSelectedToWriter with a one-TableColumn-per-field
// list when fields/onlySpecified are set.
func generateTypedToTableCode(schema *lib.TypedSchema, fields []string, onlySpecified bool, inputVar string, maxWidth int, timeFormat string) (code string, needsSSQL bool) {
	// No field selection and no time pattern — let the helper read the
	// struct schema. (A -time-format goes through the column list below,
	// where a time field's Format closure applies it.)
	if len(fields) == 0 && !onlySpecified && timeFormat == "" {
		return fmt.Sprintf(`if err := typed.WriteTableToWriter(%s, os.Stdout, %d); err != nil {
		fmt.Fprintf(os.Stderr, "to table: %%v\n", err)
		os.Exit(1)
	}`, inputVar, maxWidth), false
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
		if f.GoType == "time.Time" {
			// RFC 3339 like exec's table (fmt's %v printed Go's debug
			// form until DFC147), or the sink's -time-format.
			needsSSQL = true
			fmt.Fprintf(&b, "\t\t{Header: %q, Format: func(r *%s) string { return ssql.FormatTimePattern(r.%s, %q) }, RightAlign: %t},\n",
				f.Name, schema.TypeName, f.GoName, timeFormat, right)
			continue
		}
		fmt.Fprintf(&b, "\t\t{Header: %q, Format: func(r *%s) string { return fmt.Sprintf(\"%%v\", r.%s) }, RightAlign: %t},\n",
			f.Name, schema.TypeName, f.GoName, right)
	}
	fmt.Fprintf(&b, "\t}, %d); err != nil {\n", maxWidth)
	b.WriteString("\t\tfmt.Fprintf(os.Stderr, \"to table: %v\\n\", err)\n")
	b.WriteString("\t\tos.Exit(1)\n")
	b.WriteString("\t}")
	return b.String(), needsSSQL
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
