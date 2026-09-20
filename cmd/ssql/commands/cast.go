package commands

import (
	"sort"
	"fmt"
	"slices"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterCast registers the cast subcommand for type conversion
func RegisterCast(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	// Order behavior (DFC123 §7): neither consumes nor destroys record order.
	lib.DeclareOrder("cast", lib.OrderTransparent)

	cmd.Subcommand("cast").
		Description("Convert field types for all records").
		Example("ssql from data.csv | ssql cast -type age int", "Convert age to integer").
		Example("ssql from data.csv | ssql cast -type price float -type active bool", "Convert multiple fields").
		Example("ssql from data.csv | ssql cast -type zipcode string -type phone string", "Preserve leading zeros as strings").
		Example("ssql from survey.csv | ssql cast -type score int -invalid missing", "Values that are not ints (N/A, unknown) become missing instead of stopping the pipeline").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-type", "-t").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Arg("type").
		Completer(&cf.StaticCompleter{Options: []string{"string", "int", "float", "bool", "time"}}).
		Done().
		Accumulate().
		Global().
		Help("Convert field to type: -type <field> <type>. A value that is not of that type stops the pipeline (see -invalid)").
		Done().

		Flag("-invalid").
		String().
		Completer(&cf.StaticCompleter{Options: []string{"error", "missing"}}).
		Global().
		Default("error").
		Help("What a value that cannot be converted becomes: error (default — stop, naming the field and value) or missing (the field keeps no value; a count is reported)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var generate bool
			typeConversions := make(map[string]ssql.FieldType)

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			invalidMissing := false
			if v, ok := ctx.GlobalFlags["-invalid"]; ok {
				switch fmt.Sprintf("%v", v) {
				case "error", "":
				case "missing":
					invalidMissing = true
				default:
					return fmt.Errorf("cast: unknown -invalid %q (choose error or missing)", v)
				}
			}

			// Parse -type flag accumulations
			if typeVal, ok := ctx.GlobalFlags["-type"]; ok {
				if typeSlice, ok := typeVal.([]any); ok {
					for _, item := range typeSlice {
						if argMap, ok := item.(map[string]any); ok {
							field := fmt.Sprintf("%v", argMap["field"])
							typeName := fmt.Sprintf("%v", argMap["type"])

							ft, err := ssql.ParseFieldType(typeName)
							if err != nil {
								return fmt.Errorf("field %q: %w", field, err)
							}
							// Don't allow auto for cast - must be explicit
							if ft == ssql.FieldTypeAuto {
								return fmt.Errorf("field %q: cast requires explicit type (string, int, float, bool)", field)
							}
							typeConversions[field] = ft
						}
					}
				}
			}

			if len(typeConversions) == 0 {
				return fmt.Errorf("no -type conversions specified")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateCastCode(ctx, typeConversions, invalidMissing)
			}

			// Read JSONL from stdin WITH its schema header. (cast used the
			// schema-unaware reader until 2026-09-04: the `_schema` line
			// became a record, validation failed on it, and rows passed
			// through unchanged — found by the codelab runner, DFC125.)
			sr := lib.ReadJSONLWithSchema(ctx.Stdin())
			var castFields []string
			for f := range typeConversions {
				castFields = append(castFields, f)
			}
			sort.Strings(castFields)
			if sr.Schema != nil {
				if err := validateFieldsSchema(sr.Schema, castFields, "cast"); err != nil {
					return err
				}
			}

			// Cast every record's named fields to the target types, through
			// the ONE conversion generated code also calls (ssql.CastField): a
			// value that is not of the target type stops the pipeline, or —
			// under -invalid missing — becomes a field without a value.
			fields := make([]string, 0, len(typeConversions))
			for f := range typeConversions {
				fields = append(fields, f)
			}
			slices.Sort(fields)
			var invalid int64
			casted := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
				frozen := mut.Freeze()
				for _, field := range fields {
					mut = ssql.CastField(mut, frozen, field, typeConversions[field], invalidMissing, &invalid)
				}
				return mut
			})(sr.Records)

			// The output schema carries the new types downstream.
			outSchema := sr.Schema
			if outSchema != nil {
				outSchema = &lib.Schema{Fields: append([]string(nil), sr.Schema.Fields...), Types: map[string]string{}}
				for k, v := range sr.Schema.Types {
					outSchema.Types[k] = v
				}
				for field, targetType := range typeConversions {
					if t := castTargetWireType(targetType); t != "" {
						outSchema.Types[field] = t
					}
				}
			}
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outSchema, casted); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
			if invalid > 0 {
				// -invalid missing is lenient, not silent.
				fmt.Fprintf(ctx.Stderr(), "cast: %d values could not be converted and were left without a value (-invalid missing)\n", invalid)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateCastCode generates Go code for the cast command
func generateCastCode(ctx *cf.Context, typeConversions map[string]ssql.FieldType, invalidMissing bool) error {
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

	// Get input variable from last fragment (or default to "records")
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
		// cast is SerialOnly — planner inserts Stream.Serial()
		// upstream automatically when input is a Stream.
		return emitTypedCast(inputVar, prevSchema, typeConversions, invalidMissing)
	}

	// Generate cast code: one library call per field — the same
	// ssql.CastField the interpreter runs, so the lanes cannot drift. (This
	// used to emit a sixty-line type switch per field, its own copy of the
	// conversion rules, zeros for unparseable values included.)
	fields := make([]string, 0, len(typeConversions))
	for f := range typeConversions {
		fields = append(fields, f)
	}
	slices.Sort(fields)
	var codeBody strings.Builder
	codeBody.WriteString("\t\tfrozen := mut.Freeze()\n")
	for _, field := range fields {
		codeBody.WriteString(fmt.Sprintf("\t\tmut = ssql.CastField(mut, frozen, %q, %s, %t, nil)\n",
			field, castTargetGoConst(typeConversions[field]), invalidMissing))
	}

	outputVar := "casted"
	castCode := fmt.Sprintf(`%s := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
%s		return mut
	})(%s)`, outputVar, codeBody.String(), inputVar)
	var imports []string

	// Create and write fragment
	frag := lib.NewStmtFragment(outputVar, inputVar, castCode, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}


// castTargetGoConst is the ssql.FieldType constant as Go source.
func castTargetGoConst(t ssql.FieldType) string {
	switch t {
	case ssql.FieldTypeInt:
		return "ssql.FieldTypeInt"
	case ssql.FieldTypeFloat:
		return "ssql.FieldTypeFloat"
	case ssql.FieldTypeBool:
		return "ssql.FieldTypeBool"
	case ssql.FieldTypeTime:
		return "ssql.FieldTypeTime"
	}
	return "ssql.FieldTypeString"
}

// castTargetWireType maps a cast target to the JSONL schema vocabulary
// ("" when the target has no single wire type).
func castTargetWireType(target ssql.FieldType) string {
	switch target {
	case ssql.FieldTypeInt:
		return lib.TypeInt
	case ssql.FieldTypeFloat:
		return lib.TypeFloat
	case ssql.FieldTypeString:
		return lib.TypeString
	case ssql.FieldTypeBool:
		return lib.TypeBool
	case ssql.FieldTypeTime:
		return lib.TypeTime
	}
	return ""
}
