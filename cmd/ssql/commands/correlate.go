package commands

import (
	"fmt"
	"os"
	"slices"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterCorrelate registers the correlate subcommand
func RegisterCorrelate(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("correlate").
		Description("Compute cross-correlation or autocorrelation of numeric fields").
		Example("ssql from signal.csv | ssql correlate -field value -auto", "Compute autocorrelation to find repeating patterns").
		Example("ssql from data.csv | ssql correlate -field signal -with template", "Find where template pattern occurs in signal").
		Example("ssql from sensors.csv | ssql correlate -field sensor1 -with sensor2 -same", "Cross-correlate two sensor readings").
		Flag("-field", "-f").
		String().
		Global().
		Required().
		Help("Primary field containing numeric signal").
		Done().
		Flag("-with", "-w").
		String().
		Global().
		Help("Second field to correlate with (for cross-correlation)").
		Done().
		Flag("-auto", "-a").
		Bool().
		Global().
		Help("Compute autocorrelation (correlate field with itself)").
		Done().
		Flag("-output", "-o").
		String().
		Global().
		Help("Output field name (default: correlation)").
		Done().
		Flag("-same").
		Bool().
		Global().
		Help("Output same length as input (truncate edges)").
		Done().
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Handler(func(ctx *cf.Context) error {
			var field, withField, outputField string
			var auto, same, generate bool

			if val, ok := ctx.GlobalFlags["-field"]; ok {
				field = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-with"]; ok {
				withField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-auto"]; ok {
				auto = val.(bool)
			}
			if val, ok := ctx.GlobalFlags["-output"]; ok {
				outputField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-same"]; ok {
				same = val.(bool)
			}
			if val, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = val.(bool)
			}

			if field == "" {
				return fmt.Errorf("-field is required")
			}

			if !auto && withField == "" {
				return fmt.Errorf("either -auto or -with is required")
			}

			if auto && withField != "" {
				return fmt.Errorf("cannot use both -auto and -with")
			}

			if outputField == "" {
				outputField = "correlation"
			}

			// For autocorrelation, correlate field with itself
			if auto {
				withField = field
			}

			if shouldGenerate(generate) {
				return generateCorrelateCode(field, withField, outputField, auto, same)
			}

			// Read all records from stdin
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := slices.Collect(schemaAndRecords.Records)

			// Extract signals from fields
			signalA := ssql.ExtractSignalFromSlice(records, field)
			signalB := ssql.ExtractSignalFromSlice(records, withField)

			if len(signalA) == 0 {
				return fmt.Errorf("no signal data found in field %q", field)
			}
			if len(signalB) == 0 {
				return fmt.Errorf("no signal data found in field %q", withField)
			}

			// Compute correlation
			var result ssql.Signal
			var err error
			if same {
				result, err = ssql.CorrelateSame(signalA, signalB)
			} else {
				result, err = ssql.Correlate(signalA, signalB)
			}
			if err != nil {
				return fmt.Errorf("computing correlation: %w", err)
			}

			// Build output records
			var output []ssql.Record
			if same {
				// Same length - add to original records
				for i, r := range records {
					mut := r.ToMutable()
					if i < len(result) {
						mut = mut.Float(outputField, result[i])
					}
					output = append(output, mut.Freeze())
				}
			} else {
				// Full correlation - create new records with index and value
				for i, v := range result {
					mut := ssql.MakeMutableRecord()
					mut = mut.Int("index", int64(i))
					mut = mut.Float(outputField, v)
					output = append(output, mut.Freeze())
				}
			}

			// Write output as JSONL
			if err := lib.WriteJSONL(os.Stdout, slices.Values(output)); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateCorrelateCode generates Go code for the correlate command
func generateCorrelateCode(fieldA, fieldB, outputField string, auto, same bool) error {
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
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	outputVar := "correlatedRecords"

	var code string
	if auto {
		code = fmt.Sprintf(`%s := ssql.AutoCorrelateFilter(%q, %q, %v)(%s)`,
			outputVar, fieldA, outputField, same, inputVar)
	} else {
		code = fmt.Sprintf(`%s := ssql.CorrelateFilter(%q, %q, %q, %v)(%s)`,
			outputVar, fieldA, fieldB, outputField, same, inputVar)
	}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
