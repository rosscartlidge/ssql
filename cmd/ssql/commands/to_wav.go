package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToWAV registers the "to wav" subcommand
func registerToWAV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("wav").
		Description("Write as WAV audio file (expects amplitude field)").
		Example("ssql from audio.wav | ssql to wav output.wav", "Copy WAV file (round-trip)").
		Example("ssql from audio.wav | ssql to wav -rate 22050 output.wav", "Resample to 22050 Hz").
		Example("ssql from signal.jsonl | ssql to wav -rate 44100 audio.wav", "Convert signal data to audio").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-rate", "-r").
			Int().
			Global().
			Default(0).
			Help("Sample rate in Hz (default: from schema header, or 44100 if not specified)").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.wav"}).
			Global().
			Required().
			Help("Output WAV file (required)").
			Done().

		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var sampleRate int
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}

			if rateVal, ok := ctx.GlobalFlags["-rate"]; ok {
				sampleRate = rateVal.(int)
			}

			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if outputFile == "" {
				return fmt.Errorf("output file required")
			}

			// Check if generation is enabled (flag or env var)
			if shouldGenerate(generate) {
				return generateToWAVCode(outputFile, sampleRate)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
			records := schemaAndRecords.Records

			// Determine sample rate: flag > schema > default
			if sampleRate == 0 && schemaAndRecords.Schema != nil {
				sampleRate = schemaAndRecords.Schema.SampleRate
			}
			if sampleRate == 0 {
				sampleRate = 44100 // Default
			}

			// Write as WAV
			if err := ssql.WriteWAV(records, outputFile, sampleRate); err != nil {
				return fmt.Errorf("writing WAV file: %w", err)
			}

			return nil
		}).
		Done()
}

func generateToWAVCode(filename string, sampleRate int) error {
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

	// Use provided sample rate or default to 44100
	if sampleRate == 0 {
		sampleRate = 44100
	}

	params := []lib.CodeParam{
		{Name: "output", Default: filename, Help: "output WAV file", VarName: "flagOutput"},
	}
	code := fmt.Sprintf(`if err := ssql.WriteWAV(%s, *flagOutput, %d); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing WAV: %%v\n", err)
		os.Exit(1)
	}`, inputVar, sampleRate)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt", "os"}, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
