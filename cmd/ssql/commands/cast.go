package commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterCast registers the cast subcommand for type conversion
func RegisterCast(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("cast").
		Description("Convert field types for all records").
		Example("ssql from data.csv | ssql cast -type age int", "Convert age to integer").
		Example("ssql from data.csv | ssql cast -type price float -type active bool", "Convert multiple fields").
		Example("ssql from data.csv | ssql cast -type zipcode string -type phone string", "Preserve leading zeros as strings").

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
				Completer(&cf.StaticCompleter{Options: []string{"string", "int", "float", "bool"}}).
				Done().
			Accumulate().
			Global().
			Help("Convert field to type: -type <field> <type>").
			Done().

		Handler(func(ctx *cf.Context) error {
			var generate bool
			typeConversions := make(map[string]ssql.FieldType)

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
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
				return generateCastCode(ctx, typeConversions)
			}

			// Read JSONL from stdin
			records := lib.ReadJSONL(os.Stdin)

			// Build cast filter that converts field types for ALL records
			var castErr error
			castValidated := false
			castFilter := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
				if castErr != nil {
					return mut
				}
				frozen := mut.Freeze()

				// Validate field names on first record
				if !castValidated {
					castValidated = true
					var castFields []string
					for f := range typeConversions {
						castFields = append(castFields, f)
					}
					if err := validateFields(frozen, castFields, "cast"); err != nil {
						castErr = err
						return mut
					}
				}

				for field, targetType := range typeConversions {
					value, exists := ssql.Get[any](frozen, field)
					if !exists {
						continue
					}

					// Convert to target type
					converted := convertFieldType(value, targetType)
					mut = applyValueToRecord(mut, field, converted)
				}

				return mut
			})

			// Apply cast
			casted := castFilter(records)

			// Write output as JSONL
			if err := lib.WriteJSONL(os.Stdout, casted); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			if castErr != nil {
				return castErr
			}

			return nil
		}).
		Done()
	return cmd
}

// convertFieldType converts a value to the target FieldType
func convertFieldType(value any, targetType ssql.FieldType) any {
	switch targetType {
	case ssql.FieldTypeString:
		switch v := value.(type) {
		case string:
			return v
		case int64:
			return strconv.FormatInt(v, 10)
		case float64:
			return strconv.FormatFloat(v, 'g', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		default:
			return fmt.Sprintf("%v", v)
		}

	case ssql.FieldTypeInt:
		switch v := value.(type) {
		case int64:
			return v
		case float64:
			return int64(v)
		case string:
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				return i
			}
			// Try parsing as float first then convert
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return int64(f)
			}
			return int64(0)
		case bool:
			if v {
				return int64(1)
			}
			return int64(0)
		default:
			return int64(0)
		}

	case ssql.FieldTypeFloat:
		switch v := value.(type) {
		case float64:
			return v
		case int64:
			return float64(v)
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
			return float64(0)
		case bool:
			if v {
				return float64(1)
			}
			return float64(0)
		default:
			return float64(0)
		}

	case ssql.FieldTypeBool:
		switch v := value.(type) {
		case bool:
			return v
		case int64:
			return v != 0
		case float64:
			return v != 0
		case string:
			lower := strings.ToLower(v)
			switch lower {
			case "true", "1", "yes", "y", "on":
				return true
			default:
				return false
			}
		default:
			return false
		}

	default:
		return value
	}
}

// generateCastCode generates Go code for the cast command
func generateCastCode(ctx *cf.Context, typeConversions map[string]ssql.FieldType) error {
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

	if typedMode() {
		// cast is SerialOnly — planner inserts Stream.Serial()
		// upstream automatically when input is a Stream.
		if prevSchema == nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: 'cast' has no typed input; %s does not yet support typed mode", lastNamedCommand(fragments)))
		}
		return emitTypedCast(inputVar, prevSchema, typeConversions)
	}

	// Generate cast code
	var codeBody strings.Builder
	codeBody.WriteString("\t\tfrozen := mut.Freeze()\n\n")

	for field, targetType := range typeConversions {
		codeBody.WriteString(fmt.Sprintf("\t\tif val, exists := ssql.Get[any](frozen, %q); exists {\n", field))

		switch targetType {
		case ssql.FieldTypeString:
			codeBody.WriteString(fmt.Sprintf("\t\t\tswitch v := val.(type) {\n"))
			codeBody.WriteString("\t\t\tcase string:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.String(%q, v)\n", field))
			codeBody.WriteString("\t\t\tcase int64:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.String(%q, strconv.FormatInt(v, 10))\n", field))
			codeBody.WriteString("\t\t\tcase float64:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.String(%q, strconv.FormatFloat(v, 'g', -1, 64))\n", field))
			codeBody.WriteString("\t\t\tcase bool:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.String(%q, strconv.FormatBool(v))\n", field))
			codeBody.WriteString("\t\t\tdefault:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.String(%q, fmt.Sprintf(\"%%v\", v))\n", field))
			codeBody.WriteString("\t\t\t}\n")

		case ssql.FieldTypeInt:
			codeBody.WriteString(fmt.Sprintf("\t\t\tswitch v := val.(type) {\n"))
			codeBody.WriteString("\t\t\tcase int64:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.Int(%q, v)\n", field))
			codeBody.WriteString("\t\t\tcase float64:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.Int(%q, int64(v))\n", field))
			codeBody.WriteString("\t\t\tcase string:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tif i, err := strconv.ParseInt(v, 10, 64); err == nil {\n"))
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Int(%q, i)\n", field))
			codeBody.WriteString("\t\t\t\t} else if f, err := strconv.ParseFloat(v, 64); err == nil {\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Int(%q, int64(f))\n", field))
			codeBody.WriteString("\t\t\t\t} else {\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Int(%q, 0)\n", field))
			codeBody.WriteString("\t\t\t\t}\n")
			codeBody.WriteString("\t\t\tcase bool:\n")
			codeBody.WriteString("\t\t\t\tif v {\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Int(%q, 1)\n", field))
			codeBody.WriteString("\t\t\t\t} else {\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Int(%q, 0)\n", field))
			codeBody.WriteString("\t\t\t\t}\n")
			codeBody.WriteString("\t\t\tdefault:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.Int(%q, 0)\n", field))
			codeBody.WriteString("\t\t\t}\n")

		case ssql.FieldTypeFloat:
			codeBody.WriteString(fmt.Sprintf("\t\t\tswitch v := val.(type) {\n"))
			codeBody.WriteString("\t\t\tcase float64:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.Float(%q, v)\n", field))
			codeBody.WriteString("\t\t\tcase int64:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.Float(%q, float64(v))\n", field))
			codeBody.WriteString("\t\t\tcase string:\n")
			codeBody.WriteString("\t\t\t\tif f, err := strconv.ParseFloat(v, 64); err == nil {\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Float(%q, f)\n", field))
			codeBody.WriteString("\t\t\t\t} else {\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Float(%q, 0)\n", field))
			codeBody.WriteString("\t\t\t\t}\n")
			codeBody.WriteString("\t\t\tcase bool:\n")
			codeBody.WriteString("\t\t\t\tif v {\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Float(%q, 1)\n", field))
			codeBody.WriteString("\t\t\t\t} else {\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Float(%q, 0)\n", field))
			codeBody.WriteString("\t\t\t\t}\n")
			codeBody.WriteString("\t\t\tdefault:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.Float(%q, 0)\n", field))
			codeBody.WriteString("\t\t\t}\n")

		case ssql.FieldTypeBool:
			codeBody.WriteString(fmt.Sprintf("\t\t\tswitch v := val.(type) {\n"))
			codeBody.WriteString("\t\t\tcase bool:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.Bool(%q, v)\n", field))
			codeBody.WriteString("\t\t\tcase int64:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.Bool(%q, v != 0)\n", field))
			codeBody.WriteString("\t\t\tcase float64:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.Bool(%q, v != 0)\n", field))
			codeBody.WriteString("\t\t\tcase string:\n")
			codeBody.WriteString("\t\t\t\tlower := strings.ToLower(v)\n")
			codeBody.WriteString("\t\t\t\tswitch lower {\n")
			codeBody.WriteString("\t\t\t\tcase \"true\", \"1\", \"yes\", \"y\", \"on\":\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Bool(%q, true)\n", field))
			codeBody.WriteString("\t\t\t\tdefault:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\t\tmut = mut.Bool(%q, false)\n", field))
			codeBody.WriteString("\t\t\t\t}\n")
			codeBody.WriteString("\t\t\tdefault:\n")
			codeBody.WriteString(fmt.Sprintf("\t\t\t\tmut = mut.Bool(%q, false)\n", field))
			codeBody.WriteString("\t\t\t}\n")
		}

		codeBody.WriteString("\t\t}\n\n")
	}

	outputVar := "casted"
	castCode := fmt.Sprintf(`%s := ssql.Update(func(mut ssql.MutableRecord) ssql.MutableRecord {
%s		return mut
	})(%s)`, outputVar, codeBody.String(), inputVar)

	// Determine imports needed
	imports := []string{"strconv", "strings", "fmt"}

	// Create and write fragment
	frag := lib.NewStmtFragment(outputVar, inputVar, castCode, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
