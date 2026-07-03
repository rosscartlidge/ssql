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

// RegisterFFT registers the fft subcommand
func RegisterFFT(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("fft").
		Description("Compute Fast Fourier Transform on a numeric field").
		Example("ssql from signal.csv | ssql fft -field amplitude", "Compute FFT of amplitude field").
		Example("ssql fft -file signal.arrow -field amplitude", "Direct Arrow read (fastest, bypasses Record conversion)").
		Example("ssql from audio.csv | ssql fft -field sample -rate 44100", "Compute FFT with sample rate for frequency calculation").
		Example("ssql from data.csv | ssql fft -field value -phase", "Include phase information in output").

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
			Help("Field containing numeric signal values").
			Done().

		Flag("-rate", "-r").
			Float().
			Default(1.0).
			Global().
			Help("Sample rate in Hz (for frequency calculation)").
			Done().

		Flag("-phase", "-p").
			Bool().
			Global().
			Help("Include phase information (use +phase to exclude)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var field string
			var sampleRate float64 = 1.0
			var includePhase bool
			var generate bool

			if val, ok := ctx.GlobalFlags["-file"]; ok {
				inputFile = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-field"]; ok {
				field = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-rate"]; ok {
				sampleRate = val.(float64)
			}
			if val, ok := ctx.GlobalFlags["-phase"]; ok {
				includePhase = val.(bool)
			}
			if val, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = val.(bool)
			}

			if field == "" {
				return fmt.Errorf("-field is required")
			}

			if shouldGenerate(generate) {
				return generateFFTCode(inputFile, field, sampleRate, includePhase)
			}

			// Extract signal based on input source
			var signal ssql.Signal
			var err error

			if inputFile != "" && strings.HasSuffix(strings.ToLower(inputFile), ".arrow") {
				// Optimized path: direct Arrow signal extraction (bypasses Record conversion)
				signal, err = ssql.ExtractSignalFromArrow(inputFile, field)
				if err != nil {
					return fmt.Errorf("extracting signal from Arrow: %w", err)
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
				signal = ssql.ExtractSignalFromSlice(records, field)
			} else {
				// Read from stdin (pipeline mode)
				schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
				records := slices.Collect(schemaAndRecords.Records)
				signal = ssql.ExtractSignalFromSlice(records, field)
			}

			if len(signal) == 0 {
				return fmt.Errorf("no signal data found in field %q", field)
			}

			// Compute FFT
			var spectrum *ssql.Spectrum
			if includePhase {
				spectrum, err = ssql.FFTWithPhase(signal)
			} else {
				spectrum, err = ssql.FFT(signal)
			}
			if err != nil {
				return fmt.Errorf("computing FFT: %w", err)
			}

			// Convert spectrum to records
			spectrumRecords := ssql.SpectrumToRecords(spectrum, sampleRate)

			// Write output as JSONL
			if err := lib.WriteJSONL(ctx.Stdout(), spectrumRecords); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateFFTCode generates Go code for the fft command
func generateFFTCode(inputFile, field string, sampleRate float64, includePhase bool) error {
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
		// Direct Arrow extraction - bypasses Record conversion
		code := fmt.Sprintf(`signal, err := ssql.ExtractSignalFromArrow(%q, %q)
	if err != nil {
		fmt.Fprintf(ctx.Stderr(), "Error extracting signal: %%v\n", err)
		os.Exit(1)
	}

	spectrum, err := ssql.FFT%s(signal)
	if err != nil {
		fmt.Fprintf(ctx.Stderr(), "Error computing FFT: %%v\n", err)
		os.Exit(1)
	}

	fftRecords := ssql.SpectrumToRecords(spectrum, %v)`,
			inputFile, field,
			map[bool]string{true: "WithPhase", false: ""}[includePhase],
			sampleRate)

		frag := lib.NewInitFragment("fftRecords", code, []string{"os"}, getCommandString())
		return lib.WriteCodeFragment(frag)
	}

	// Standard pipeline mode
	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	outputVar := uniqueVarName("fftRecords", fragments)

	// Use the FFTFilter function that works with the code generation system
	code := fmt.Sprintf(`%s := ssql.FFTFilter(%q, %v, %v)(%s)`,
		outputVar, field, sampleRate, includePhase, inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
