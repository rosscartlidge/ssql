package commands

import (
	"fmt"
	"iter"
	"os"
	"slices"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterIFFT registers the ifft subcommand
func RegisterIFFT(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("ifft").
		Description("Compute Inverse Fast Fourier Transform to reconstruct time-domain signal").
		Example("ssql from spectrum.csv | ssql ifft -magnitude mag -phase phase", "Reconstruct signal from magnitude and phase").
		Example("ssql from signal.csv | ssql fft -field value -phase | ssql ifft -magnitude magnitude -phase phase", "Round-trip FFT then IFFT").
		Example("ssql from spectrum.csv | ssql ifft -magnitude magnitude -phase phase -output amplitude", "Specify output field name").
		Flag("-magnitude", "-m").
		String().
		Global().
		Required().
		FieldsFromFlag("").
		Help("Field containing magnitude values").
		Done().
		Flag("-phase", "-p").
		String().
		Global().
		Required().
		FieldsFromFlag("").
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
			var magnitudeField string
			var phaseField string
			var outputField string = "signal"
			var generate bool

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
				return generateIFFTCode(magnitudeField, phaseField, outputField)
			}

			// Read all records from stdin
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := slices.Collect(schemaAndRecords.Records)

			// Extract magnitude and phase from fields
			magnitude := make([]float64, len(records))
			phase := make([]float64, len(records))
			for i, r := range records {
				magnitude[i] = ssql.GetOr(r, magnitudeField, 0.0)
				phase[i] = ssql.GetOr(r, phaseField, 0.0)
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
func generateIFFTCode(magnitudeField, phaseField, outputField string) error {
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

	outputVar := "ifftRecords"

	// Use the IFFTFilter function that works with the code generation system
	code := fmt.Sprintf(`%s := ssql.IFFTFilter(%q, %q, %q)(%s)`,
		outputVar, magnitudeField, phaseField, outputField, inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
