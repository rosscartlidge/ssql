package commands

import (
	"fmt"
	"iter"
	"os"
	"slices"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterIFFT registers the ifft subcommand
func RegisterIFFT(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("ifft").
		Description("Compute Inverse Fast Fourier Transform to reconstruct time-domain signal").
		Example("ssql from spectrum.csv | ssql ifft -magnitude mag -phase phase", "Reconstruct signal from magnitude and phase").
		Example("ssql ifft -file spectrum.arrow -magnitude mag -phase phase", "Direct Arrow read (fastest)").
		Example("ssql from signal.csv | ssql fft -field value -phase | ssql ifft -magnitude magnitude -phase phase", "Round-trip FFT then IFFT").

		Flag("-file").
			String().
			Global().
			Completer(&cf.FileCompleter{Pattern: "*.{arrow,csv,json,jsonl}"}).
			Help("Input file (Arrow files use optimized direct signal extraction)").
			Done().

		Flag("-magnitude", "-m").
			String().
			Global().
			Required().
			FieldsFromFlag("-file").
			Help("Field containing magnitude values").
			Done().

		Flag("-phase", "-p").
			String().
			Global().
			Required().
			FieldsFromFlag("-file").
			Help("Field containing phase values (in radians)").
			Done().

		Flag("-output", "-o").
			String().
			Default("signal").
			Global().
			Help("Output field name for reconstructed signal (default: signal)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var magnitudeField string
			var phaseField string
			var outputField string = "signal"
			var generate bool

			if val, ok := ctx.GlobalFlags["-file"]; ok {
				inputFile = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-magnitude"]; ok {
				magnitudeField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-phase"]; ok {
				phaseField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-output"]; ok {
				outputField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = val.(bool)
			}

			if magnitudeField == "" {
				return fmt.Errorf("-magnitude is required")
			}
			if phaseField == "" {
				return fmt.Errorf("-phase is required")
			}

			if shouldGenerate(generate) {
				return generateIFFTCode(inputFile, magnitudeField, phaseField, outputField)
			}

			// Extract magnitude and phase based on input source
			var magnitude, phase []float64
			var err error

			if inputFile != "" && strings.HasSuffix(strings.ToLower(inputFile), ".arrow") {
				// Optimized path: direct Arrow signal extraction
				magnitude, err = ssql.ExtractSignalFromArrow(inputFile, magnitudeField)
				if err != nil {
					return fmt.Errorf("extracting magnitude from Arrow: %w", err)
				}
				phase, err = ssql.ExtractSignalFromArrow(inputFile, phaseField)
				if err != nil {
					return fmt.Errorf("extracting phase from Arrow: %w", err)
				}
			} else if inputFile != "" {
				// Read from other file formats via Records
				var records []ssql.Record
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
				magnitude = make([]float64, len(records))
				phase = make([]float64, len(records))
				for i, r := range records {
					magnitude[i] = ssql.GetOr(r, magnitudeField, 0.0)
					phase[i] = ssql.GetOr(r, phaseField, 0.0)
				}
			} else {
				// Read from stdin (pipeline mode)
				schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
				records := slices.Collect(schemaAndRecords.Records)
				magnitude = make([]float64, len(records))
				phase = make([]float64, len(records))
				for i, r := range records {
					magnitude[i] = ssql.GetOr(r, magnitudeField, 0.0)
					phase[i] = ssql.GetOr(r, phaseField, 0.0)
				}
			}

			if len(magnitude) == 0 {
				return fmt.Errorf("no data found in fields %q and %q", magnitudeField, phaseField)
			}

			// Compute inverse FFT
			signal, err := ssql.IFFT(magnitude, phase)
			if err != nil {
				return fmt.Errorf("computing IFFT: %w", err)
			}

			// Convert signal to records
			signalRecords := signalToRecords(signal, outputField)

			// Write output as JSONL
			if err := lib.WriteJSONL(os.Stdout, signalRecords); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// signalToRecords converts a signal to a sequence of records with index and value fields.
func signalToRecords(signal ssql.Signal, outputField string) iter.Seq[ssql.Record] {
	return func(yield func(ssql.Record) bool) {
		for i, v := range signal {
			mut := ssql.MakeMutableRecord()
			mut = mut.Int("index", int64(i))
			mut = mut.Float(outputField, v)
			if !yield(mut.Freeze()) {
				return
			}
		}
	}
}

// generateIFFTCode generates Go code for the ifft command
func generateIFFTCode(inputFile, magnitudeField, phaseField, outputField string) error {
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
		code := fmt.Sprintf(`magnitude, err := ssql.ExtractSignalFromArrow(%q, %q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error extracting magnitude: %%v\n", err)
		os.Exit(1)
	}
	phase, err := ssql.ExtractSignalFromArrow(%q, %q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error extracting phase: %%v\n", err)
		os.Exit(1)
	}

	signal, err := ssql.IFFT(magnitude, phase)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error computing IFFT: %%v\n", err)
		os.Exit(1)
	}

	ifftRecords := signalToRecordsFunc(signal, %q)`,
			inputFile, magnitudeField,
			inputFile, phaseField,
			outputField)

		frag := lib.NewInitFragment("ifftRecords", code, []string{"os"}, getCommandString())
		return lib.WriteCodeFragment(frag)
	}

	// Standard pipeline mode
	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	outputVar := "ifftRecords"

	// Use the IFFTFilter function that works with the code generation system
	code := fmt.Sprintf(`%s := ssql.IFFTFilter(%q, %q, %q)(%s)`,
		outputVar, magnitudeField, phaseField, outputField, inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
