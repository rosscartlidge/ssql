package commands

import (
	"fmt"
	"os"
	"slices"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterCorrelate registers the correlate subcommand
func RegisterCorrelate(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("correlate").
		Description("Compute cross-correlation or autocorrelation of numeric fields").
		Example("ssql from signal.csv | ssql correlate -field value -auto", "Compute autocorrelation to find repeating patterns").
		Example("ssql correlate -file signal.arrow -field value -auto -max-lag 1000", "Direct Arrow read (fastest)").
		Example("ssql from data.csv | ssql correlate -field signal -with template", "Find where template pattern occurs in signal").
		Example("ssql from sensors.csv | ssql correlate -field sensor1 -with sensor2 -same", "Cross-correlate two sensor readings").

		Flag("-file").
			String().
			Global().
			Completer(&cf.FileCompleter{Pattern: "*.{arrow,csv,json,jsonl}"}).
			Help("Input file (Arrow files use optimized direct signal extraction)").
			Done().

		Flag("-field", "-f").
			String().
			Global().
			Required().
			FieldsFromFlag("-file").
			Help("Primary field containing numeric signal").
			Done().

		Flag("-with", "-w").
			String().
			Global().
			FieldsFromFlag("-file").
			Help("Second field to correlate with (for cross-correlation)").
			Done().

		Flag("-auto", "-a").
			Bool().
			Global().
			Help("Compute autocorrelation (use +auto for cross-correlation)").
			Done().

		Flag("-max-lag", "-m").
			Int().
			Default(0).
			Global().
			Help("Maximum lag for autocorrelation (0 = full, requires -auto)").
			Done().

		Flag("-output", "-o").
			String().
			Global().
			Help("Output field name (default: correlation)").
			Done().

		Flag("-same").
			Bool().
			Global().
			Help("Output same length as input (use +same for full length)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			var inputFile, field, withField, outputField string
			var auto, same, generate bool
			var maxLag int

			if val, ok := ctx.GlobalFlags["-file"]; ok {
				inputFile = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-field"]; ok {
				field = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-with"]; ok {
				withField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-auto"]; ok {
				auto = val.(bool)
			}
			if val, ok := ctx.GlobalFlags["-max-lag"]; ok {
				maxLag = val.(int)
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

			if maxLag > 0 && !auto {
				return fmt.Errorf("-max-lag requires -auto")
			}

			if maxLag > 0 && same {
				return fmt.Errorf("cannot use both -max-lag and -same")
			}

			if outputField == "" {
				outputField = "correlation"
			}

			// For autocorrelation, correlate field with itself
			if auto {
				withField = field
			}

			if shouldGenerate(generate) {
				return generateCorrelateCode(inputFile, field, withField, outputField, auto, same, maxLag)
			}

			// Extract signals based on input source
			var signalA, signalB ssql.Signal
			var records []ssql.Record
			var err error

			if inputFile != "" && strings.HasSuffix(strings.ToLower(inputFile), ".arrow") {
				// Optimized path: direct Arrow signal extraction
				signalA, err = ssql.ExtractSignalFromArrow(inputFile, field)
				if err != nil {
					return fmt.Errorf("extracting signal from Arrow: %w", err)
				}
				if !auto || maxLag == 0 {
					// Need second signal for cross-correlation or full autocorrelation
					signalB, err = ssql.ExtractSignalFromArrow(inputFile, withField)
					if err != nil {
						return fmt.Errorf("extracting second signal from Arrow: %w", err)
					}
				}
			} else if inputFile != "" {
				// Read from other file formats via Records
				lower := strings.ToLower(inputFile)
				switch {
				case strings.HasSuffix(lower, ".csv"):
					recs, rerr := ssql.ReadCSV(inputFile)
					if rerr != nil {
						return fmt.Errorf("reading CSV: %w", rerr)
					}
					records = slices.Collect(recs)
				case strings.HasSuffix(lower, ".json"):
					recs, rerr := ssql.ReadJSON(inputFile)
					if rerr != nil {
						return fmt.Errorf("reading JSON: %w", rerr)
					}
					records = slices.Collect(recs)
				case strings.HasSuffix(lower, ".jsonl"):
					file, ferr := os.Open(inputFile)
					if ferr != nil {
						return fmt.Errorf("opening JSONL file: %w", ferr)
					}
					defer file.Close()
					records = slices.Collect(lib.ReadJSONL(file))
				default:
					return fmt.Errorf("unsupported file format: %s", inputFile)
				}
				signalA = ssql.ExtractSignalFromSlice(records, field)
				if !auto || maxLag == 0 {
					signalB = ssql.ExtractSignalFromSlice(records, withField)
				}
			} else {
				// Read from stdin (pipeline mode)
				schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
				records = slices.Collect(schemaAndRecords.Records)
				signalA = ssql.ExtractSignalFromSlice(records, field)
				if !auto || maxLag == 0 {
					signalB = ssql.ExtractSignalFromSlice(records, withField)
				}
			}

			if len(signalA) == 0 {
				return fmt.Errorf("no signal data found in field %q", field)
			}

			// Compute correlation
			var result ssql.Signal

			if auto && maxLag > 0 {
				// Autocorrelation with max lag limit
				result, err = ssql.AutoCorrelateMax(signalA, maxLag)
			} else {
				// Cross-correlation or full autocorrelation
				if len(signalB) == 0 {
					return fmt.Errorf("no signal data found in field %q", withField)
				}

				if same {
					result, err = ssql.CorrelateSame(signalA, signalB)
				} else {
					result, err = ssql.Correlate(signalA, signalB)
				}
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
			} else if auto && maxLag > 0 {
				// Max-lag autocorrelation - output lag and value
				for lag, v := range result {
					mut := ssql.MakeMutableRecord()
					mut = mut.Int("lag", int64(lag))
					mut = mut.Float(outputField, v)
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
			if err := lib.WriteJSONL(ctx.Stdout(), slices.Values(output)); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateCorrelateCode generates Go code for the correlate command
func generateCorrelateCode(inputFile, fieldA, fieldB, outputField string, auto, same bool, maxLag int) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	// If reading directly from Arrow file, generate optimized code
	if inputFile != "" && strings.HasSuffix(strings.ToLower(inputFile), ".arrow") {
		var correlateCall string
		if auto && maxLag > 0 {
			correlateCall = fmt.Sprintf(`ssql.AutoCorrelateMax(signalA, %d)`, maxLag)
		} else if auto {
			if same {
				correlateCall = `ssql.CorrelateSame(signalA, signalA)`
			} else {
				correlateCall = `ssql.Correlate(signalA, signalA)`
			}
		} else {
			if same {
				correlateCall = `ssql.CorrelateSame(signalA, signalB)`
			} else {
				correlateCall = `ssql.Correlate(signalA, signalB)`
			}
		}

		var signalBExtract string
		if !auto {
			signalBExtract = fmt.Sprintf(`signalB, err := ssql.ExtractSignalFromArrow(%q, %q)
	if err != nil {
		fmt.Fprintf(ctx.Stderr(), "Error extracting second signal: %%v\n", err)
		os.Exit(1)
	}

	`, inputFile, fieldB)
		}

		code := fmt.Sprintf(`signalA, err := ssql.ExtractSignalFromArrow(%q, %q)
	if err != nil {
		fmt.Fprintf(ctx.Stderr(), "Error extracting signal: %%v\n", err)
		os.Exit(1)
	}

	%sresult, err := %s
	if err != nil {
		fmt.Fprintf(ctx.Stderr(), "Error computing correlation: %%v\n", err)
		os.Exit(1)
	}

	correlatedRecords := signalToRecordsFunc(result, %q)`,
			inputFile, fieldA,
			signalBExtract,
			correlateCall,
			outputField)

		frag := lib.NewInitFragment("correlatedRecords", code, []string{"os"}, getCommandString())
		return lib.WriteCodeFragment(frag)
	}

	// Standard pipeline mode
	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	outputVar := uniqueVarName("correlatedRecords", fragments)

	var code string
	if auto && maxLag > 0 {
		code = fmt.Sprintf(`%s := ssql.AutoCorrelateMaxFilter(%q, %q, %d)(%s)`,
			outputVar, fieldA, outputField, maxLag, inputVar)
	} else if auto {
		code = fmt.Sprintf(`%s := ssql.AutoCorrelateFilter(%q, %q, %v)(%s)`,
			outputVar, fieldA, outputField, same, inputVar)
	} else {
		code = fmt.Sprintf(`%s := ssql.CorrelateFilter(%q, %q, %q, %v)(%s)`,
			outputVar, fieldA, fieldB, outputField, same, inputVar)
	}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
