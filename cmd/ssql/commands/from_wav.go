package commands

import (
	"fmt"
	"iter"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func registerFromWAV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("wav").
		Description("Read WAV audio file").
		Example("ssql from wav audio.wav | ssql fft -field amplitude", "Read WAV for FFT analysis").
		Example("ssql from wav stereo.wav -channel 0 | ssql to table", "Read left channel only").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-channel", "-ch").
			Int().
			Global().
			Default(-1).
			Help("Extract specific channel (0=left, 1=right). Default: mix to mono.").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.wav"}).
			Global().
			Default("").
			Help("Input WAV file").
			Done().

		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool
			var channel int = -1

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if chVal, ok := ctx.GlobalFlags["-channel"]; ok {
				channel = chVal.(int)
			}

			return executeFromWAV(inputFile, channel, generate)
		}).
		Done()
}

// executeFromWAV handles WAV reading for both the subcommand and bare form.
func executeFromWAV(inputFile string, channel int, generate bool) error {
	if shouldGenerate(generate) {
		return generateFromWAVCode(inputFile, channel)
	}

	var records iter.Seq[ssql.Record]
	var wavMeta *ssql.WAVMetadata

	if inputFile == "" {
		var err error
		records, wavMeta, err = ssql.ReadWAVFromReader(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading WAV from stdin: %w", err)
		}
	} else {
		var err error
		if channel >= 0 {
			records, wavMeta, err = ssql.ReadWAVChannel(inputFile, channel)
		} else {
			records, wavMeta, err = ssql.ReadWAV(inputFile)
		}
		if err != nil {
			return fmt.Errorf("reading WAV file: %w", err)
		}
	}

	records = wrapWithFieldCaching(records, inputFile)
	opts := writeWithInferredSchemaOptions{}
	if wavMeta != nil {
		opts.sampleRate = wavMeta.SampleRate
	}
	return writeWithInferredSchema(records, opts)
}

// generateFromWAVCode generates Go code for reading WAV.
func generateFromWAVCode(filename string, channel int) error {
	var code string
	var imports []string
	var params []lib.CodeParam

	if filename == "" {
		code = `records, _, err := ssql.ReadWAVFromReader(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading WAV: %w", err))
		os.Exit(1)
	}`
		imports = []string{"fmt", "os"}
	} else {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input WAV file", VarName: "flagInput"})
		if channel >= 0 {
			code = fmt.Sprintf(`records, _, err := ssql.ReadWAVChannel(*flagInput, %d)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading WAV: %%w", err))
		os.Exit(1)
	}`, channel)
		} else {
			code = `records, _, err := ssql.ReadWAV(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading WAV: %w", err))
		os.Exit(1)
	}`
		}
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
