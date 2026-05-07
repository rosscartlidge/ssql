package commands

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// RegisterConvolve registers the convolve subcommand
func RegisterConvolve(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("convolve").
		Description("Apply convolution to a numeric field using a kernel or itself").
		Example("ssql from signal.csv | ssql convolve -field value -kernel avg -size 5", "Apply 5-point moving average").
		Example("ssql convolve -file signal.arrow -field value -kernel gaussian -size 11", "Direct Arrow read (fastest)").
		Example("ssql from sensor.csv | ssql convolve -field reading -kernel diff", "Compute first derivative").
		Example("ssql from signal.csv | ssql convolve -field value -custom 0.25,0.5,0.25", "Apply custom kernel").
		Example("ssql from signal.csv | ssql convolve -field value -auto", "Compute auto-convolution (signal with itself)").
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
		Flag("-output", "-o").
		String().
		Global().
		Help("Output field name (default: field_convolved)").
		Done().
		Flag("-kernel", "-k").
		String().
		Global().
		Help("Built-in kernel: avg, gaussian, diff, laplacian, sobel").
		Completer(&cf.StaticCompleter{Options: []string{"avg", "gaussian", "diff", "laplacian", "sobel"}}).
		Done().
		Flag("-size", "-s").
		Int().
		Default(5).
		Global().
		Help("Kernel size (for avg, gaussian)").
		Done().
		Flag("-sigma").
		Float().
		Default(1.0).
		Global().
		Help("Sigma value for Gaussian kernel").
		Done().
		Flag("-custom", "-c").
		String().
		Global().
		Help("Custom kernel as comma-separated values (e.g., '0.25,0.5,0.25')").
		Done().
		Flag("-auto", "-a").
		Bool().
		Global().
		Help("Compute auto-convolution (use +auto for cross-convolution)").
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
			var inputFile, field, outputField, kernelName, customKernel string
			var size int = 5
			var sigma float64 = 1.0
			var auto, same, generate bool

			if val, ok := ctx.GlobalFlags["-file"]; ok {
				inputFile = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-field"]; ok {
				field = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-output"]; ok {
				outputField = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-kernel"]; ok {
				kernelName = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-size"]; ok {
				size = val.(int)
			}
			if val, ok := ctx.GlobalFlags["-sigma"]; ok {
				sigma = val.(float64)
			}
			if val, ok := ctx.GlobalFlags["-custom"]; ok {
				customKernel = val.(string)
			}
			if val, ok := ctx.GlobalFlags["-auto"]; ok {
				auto = val.(bool)
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

			if !auto && kernelName == "" && customKernel == "" {
				return fmt.Errorf("either -kernel, -custom, or -auto is required")
			}

			if auto && (kernelName != "" || customKernel != "") {
				return fmt.Errorf("cannot use -auto with -kernel or -custom")
			}

			if outputField == "" {
				if auto {
					outputField = field + "_autoconv"
				} else {
					outputField = field + "_convolved"
				}
			}

			if shouldGenerate(generate) {
				return generateConvolveCode(inputFile, field, outputField, kernelName, size, sigma, customKernel, auto, same)
			}

			// Extract signal based on input source
			var signal ssql.Signal
			var records []ssql.Record
			var err error

			if inputFile != "" && strings.HasSuffix(strings.ToLower(inputFile), ".arrow") {
				// Optimized path: direct Arrow signal extraction
				signal, err = ssql.ExtractSignalFromArrow(inputFile, field)
				if err != nil {
					return fmt.Errorf("extracting signal from Arrow: %w", err)
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
				signal = ssql.ExtractSignalFromSlice(records, field)
			} else {
				// Read from stdin (pipeline mode)
				schemaAndRecords := lib.ReadJSONLWithSchema(os.Stdin)
				records = slices.Collect(schemaAndRecords.Records)
				signal = ssql.ExtractSignalFromSlice(records, field)
			}

			if len(signal) == 0 {
				return fmt.Errorf("no signal data found in field %q", field)
			}

			// Apply convolution
			var result ssql.Signal

			if auto {
				// Auto-convolution
				if same {
					result, err = ssql.AutoConvolveSame(signal)
				} else {
					result, err = ssql.AutoConvolve(signal)
				}
			} else {
				// Build kernel for regular convolution
				var kernel ssql.Signal
				if customKernel != "" {
					kernel, err = parseCustomKernel(customKernel)
					if err != nil {
						return fmt.Errorf("parsing custom kernel: %w", err)
					}
				} else {
					kernel = buildKernel(kernelName, size, sigma)
					if kernel == nil {
						return fmt.Errorf("unknown kernel: %s", kernelName)
					}
				}

				if same {
					result, err = ssql.ConvolveSame(signal, kernel)
				} else {
					result, err = ssql.Convolve(signal, kernel)
				}
			}
			if err != nil {
				return fmt.Errorf("computing convolution: %w", err)
			}

			// Add convolved signal back to records
			// If same mode, add to original records; otherwise create new records
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
				// Full convolution - create new records with just the result
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

func parseCustomKernel(s string) (ssql.Signal, error) {
	parts := strings.Split(s, ",")
	kernel := make(ssql.Signal, len(parts))
	for i, part := range parts {
		val, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid kernel value %q: %w", part, err)
		}
		kernel[i] = val
	}
	return kernel, nil
}

func buildKernel(name string, size int, sigma float64) ssql.Signal {
	switch name {
	case "avg":
		return ssql.MovingAverageKernel(size)
	case "gaussian":
		return ssql.GaussianKernel(size, sigma)
	case "diff":
		return ssql.DiffKernel()
	case "laplacian":
		return ssql.LaplacianKernel()
	case "sobel":
		return ssql.SobelKernel()
	default:
		return nil
	}
}

// generateConvolveCode generates Go code for the convolve command
func generateConvolveCode(inputFile, field, outputField, kernelName string, size int, sigma float64, customKernel string, auto, same bool) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	// Build kernel expression for non-auto modes
	var kernelExpr string
	if !auto {
		if customKernel != "" {
			kernelExpr = fmt.Sprintf(`ssql.Signal{%s}`, customKernel)
		} else {
			switch kernelName {
			case "avg":
				kernelExpr = fmt.Sprintf(`ssql.MovingAverageKernel(%d)`, size)
			case "gaussian":
				kernelExpr = fmt.Sprintf(`ssql.GaussianKernel(%d, %v)`, size, sigma)
			case "diff":
				kernelExpr = `ssql.DiffKernel()`
			case "laplacian":
				kernelExpr = `ssql.LaplacianKernel()`
			case "sobel":
				kernelExpr = `ssql.SobelKernel()`
			}
		}
	}

	// If reading directly from Arrow file, generate optimized code
	if inputFile != "" && strings.HasSuffix(strings.ToLower(inputFile), ".arrow") {
		var convolveCall string
		if auto {
			if same {
				convolveCall = `ssql.AutoConvolveSame(signal)`
			} else {
				convolveCall = `ssql.AutoConvolve(signal)`
			}
		} else {
			if same {
				convolveCall = fmt.Sprintf(`ssql.ConvolveSame(signal, %s)`, kernelExpr)
			} else {
				convolveCall = fmt.Sprintf(`ssql.Convolve(signal, %s)`, kernelExpr)
			}
		}

		code := fmt.Sprintf(`signal, err := ssql.ExtractSignalFromArrow(%q, %q)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error extracting signal: %%v\n", err)
		os.Exit(1)
	}

	result, err := %s
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error computing convolution: %%v\n", err)
		os.Exit(1)
	}

	convolvedRecords := signalToRecordsFunc(result, %q)`,
			inputFile, field,
			convolveCall,
			outputField)

		frag := lib.NewInitFragment("convolvedRecords", code, []string{"os"}, getCommandString())
		return lib.WriteCodeFragment(frag)
	}

	// Standard pipeline mode
	var inputVar string
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
	} else {
		inputVar = "records"
	}

	outputVar := "convolvedRecords"

	var code string
	if auto {
		// Auto-convolution
		code = fmt.Sprintf(`%s := ssql.AutoConvolveFilter(%q, %q, %v)(%s)`,
			outputVar, field, outputField, same, inputVar)
	} else {
		// Use the ConvolveFilter function that works with the code generation system
		code = fmt.Sprintf(`%s := ssql.ConvolveFilter(%q, %q, %s, %v)(%s)`,
			outputVar, field, outputField, kernelExpr, same, inputVar)
	}

	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, getCommandString())
	return lib.WriteCodeFragment(frag)
}
