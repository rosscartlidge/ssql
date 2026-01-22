package commands

import (
	"fmt"
	"os"
	"slices"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterFFT registers the fft subcommand
func RegisterFFT(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("fft").
		Description("Compute Fast Fourier Transform on a numeric field").
		Example("ssql from signal.csv | ssql fft -field amplitude", "Compute FFT of amplitude field").
		Example("ssql from audio.csv | ssql fft -field sample -rate 44100", "Compute FFT with sample rate for frequency calculation").
		Example("ssql from data.csv | ssql fft -field value -phase", "Include phase information in output").
		Flag("-field", "-f").
		String().
		Global().
		Required().
		FieldsFromFlag("").
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
		Help("Include phase information in output").
		Done().
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Handler(func(ctx *cf.Context) error {
			var field string
			var sampleRate float64 = 1.0
			var includePhase bool
			var generate bool

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
				return generateFFTCode(field, sampleRate, includePhase)
			}

			// Read all records from stdin
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := slices.Collect(schemaAndRecords.Records)

			// Extract signal from field
			signal := ssql.ExtractSignalFromSlice(records, field)

			if len(signal) == 0 {
				return fmt.Errorf("no signal data found in field %q", field)
			}

			// Compute FFT
			var spectrum *ssql.Spectrum
			var err error
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
			if err := lib.WriteJSONL(os.Stdout, spectrumRecords); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// generateFFTCode generates Go code for the fft command
func generateFFTCode(field string, sampleRate float64, includePhase bool) error {
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

	outputVar := "fftRecords"

	// Use the FFTFilter function that works with the code generation system
	code := fmt.Sprintf(`%s := ssql.FFTFilter(%q, %v, %v)(%s)`,
		outputVar, field, sampleRate, includePhase, inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
