package commands

import (
	"fmt"
	"math"
	"os"
	"slices"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterSpectrogram registers the spectrogram subcommand
func RegisterSpectrogram(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("spectrogram").
		Description("Compute Short-Time Fourier Transform (STFT) on a numeric field").
		Example("ssql from signal.csv | ssql spectrogram -field amplitude", "Compute spectrogram with default settings (window=1024, hop=256, hann)").
		Example("ssql spectrogram -file signal.arrow -field value -window-size 2048 -rate 44100", "Direct Arrow read with custom window size and sample rate").
		Example("ssql from audio.csv | ssql spectrogram -field sample -window-size 512 -hop 128 -window-type hamming", "Custom hop size and Hamming window").
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
		Flag("-window-size", "-w").
		Int().
		Default(1024).
		Global().
		Help("FFT window size in samples (default: 1024)").
		Done().
		Flag("-hop", "-h").
		Int().
		Default(0).
		Global().
		Help("Hop size in samples between windows (default: window-size/4)").
		Done().
		Flag("-window-type", "-t").
		String().
		Default("hann").
		Global().
		Completer(&cf.StaticCompleter{Options: []string{"hann", "hamming", "blackman", "none"}}).
		Help("Window function: hann, hamming, blackman, none (default: hann)").
		Done().
		Flag("-rate", "-r").
		Float().
		Default(1.0).
		Global().
		Help("Sample rate in Hz (for frequency/time calculation)").
		Done().
		Flag("-output", "-o").
		String().
		Default("magnitude").
		Global().
		Completer(&cf.StaticCompleter{Options: []string{"magnitude", "power", "db"}}).
		Help("Output format: magnitude (default), power (magnitude^2), db (20*log10)").
		Done().
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var field string
			var windowSize int = 1024
			var hopSize int
			var windowType string = "hann"
			var sampleRate float64 = 1.0
			var outputFormat string = "magnitude"
			var generate bool

			if val, ok := ctx.GlobalFlags["-file"]; ok {
				inputFile = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-field"]; ok {
				field = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-window-size"]; ok {
				windowSize = val.(int)
			}
			if val, ok := ctx.GlobalFlags["-hop"]; ok {
				hopSize = val.(int)
			}
			if val, ok := ctx.GlobalFlags["-window-type"]; ok {
				windowType = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-rate"]; ok {
				sampleRate = val.(float64)
			}
			if val, ok := ctx.GlobalFlags["-output"]; ok {
				outputFormat = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = val.(bool)
			}

			if field == "" {
				return fmt.Errorf("-field is required")
			}

			if shouldGenerate(generate) {
				return generateSpectrogramCode(inputFile, field, windowSize, hopSize, windowType, sampleRate, outputFormat)
			}

			opts := ssql.SpectrogramOptions{
				WindowSize: windowSize,
				HopSize:    hopSize,
				Window:     windowType,
				SampleRate: sampleRate,
			}

			// Extract signal based on input source
			var signal ssql.Signal
			var err error

			if inputFile != "" && strings.HasSuffix(strings.ToLower(inputFile), ".arrow") {
				signal, err = ssql.ExtractSignalFromArrow(inputFile, field)
				if err != nil {
					return fmt.Errorf("extracting signal from Arrow: %w", err)
				}
			} else if inputFile != "" {
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
				schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
				records := slices.Collect(schemaAndRecords.Records)
				signal = ssql.ExtractSignalFromSlice(records, field)
			}

			if len(signal) == 0 {
				return fmt.Errorf("no signal data found in field %q", field)
			}

			// Compute spectrogram
			bins, err := ssql.Spectrogram(signal, opts)
			if err != nil {
				return fmt.Errorf("computing spectrogram: %w", err)
			}

			// Apply output format transformation
			bins = applyOutputFormat(bins, outputFormat)

			// Convert to records and write as JSONL
			spectrogramRecords := ssql.SpectrogramToRecords(bins)
			if err := lib.WriteJSONL(os.Stdout, spectrogramRecords); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}

// applyOutputFormat transforms magnitude values based on the output format.
func applyOutputFormat(bins []ssql.SpectrogramBin, format string) []ssql.SpectrogramBin {
	switch format {
	case "power":
		for i := range bins {
			bins[i].Magnitude = bins[i].Magnitude * bins[i].Magnitude
		}
	case "db":
		for i := range bins {
			mag := bins[i].Magnitude
			if mag > 0 {
				bins[i].Magnitude = 20 * math.Log10(mag)
			} else {
				bins[i].Magnitude = -200 // floor for zero magnitude
			}
		}
	}
	return bins
}

// generateSpectrogramCode generates Go code for the spectrogram command
func generateSpectrogramCode(inputFile, field string, windowSize, hopSize int, windowType string, sampleRate float64, outputFormat string) error {
	if outputFormat != "" && outputFormat != "magnitude" {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("-output %s is not yet supported with -generate (only magnitude is supported)", outputFormat))
	}

	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	// Build options code
	optsCode := fmt.Sprintf(`ssql.SpectrogramOptions{
		WindowSize: %d,
		HopSize:    %d,
		Window:     %q,
		SampleRate: %v,
	}`, windowSize, hopSize, windowType, sampleRate)

	// If reading directly from Arrow file, generate optimized code
	if inputFile != "" && strings.HasSuffix(strings.ToLower(inputFile), ".arrow") {
		code := fmt.Sprintf(`signal, err := ssql.ExtractSignalFromArrow(%q, %q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error extracting signal: %%v\n", err)
		os.Exit(1)
	}

	spectrogramBins, err := ssql.Spectrogram(signal, %s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error computing spectrogram: %%v\n", err)
		os.Exit(1)
	}

	spectrogramRecords := ssql.SpectrogramToRecords(spectrogramBins)`,
			inputFile, field, optsCode)

		frag := lib.NewInitFragment("spectrogramRecords", code, []string{"os"}, getCommandString())
		return lib.WriteCodeFragment(frag)
	}

	// Standard pipeline mode
	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	outputVar := "spectrogramRecords"

	code := fmt.Sprintf(`%s := ssql.SpectrogramFilter(%q, %s)(%s)`,
		outputVar, field, optsCode, inputVar)

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
