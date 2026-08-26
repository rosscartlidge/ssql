package commands

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func registerFromTSV(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("tsv").
		Description("Read TSV file(s) or stdin").
		Example("ssql from tsv data.tsv | ssql to table", "Read TSV file").
		Example("ssql from tsv *.tsv | ssql to table", "Read multiple TSV files").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-merge-schemas").
		Bool().
		Global().
		Help("Allow files with different headers (merge schemas)").
		Done().
		Flag("-source").
		String().
		Global().
		Default("").
		Help("Add field with source filename: -source file").
		Done().
		Flag("-unordered").
		Bool().
		Global().
		Help("Don't preserve file order in pushdown (faster, lower memory)").
		Done().
		Flag("-records").
		Bool().
		Global().
		Default(false).
		Help("Print only the record count this invocation would produce (cheapest per format: parquet footer, line count for csv/tsv/jsonl) and exit").
		Done().
		Flag("-sample").
		Int().
		Global().
		Default(0).
		Help("Fast approximate sample of N rows via byte-offset seeks (probability ~ line length; use the `sample` stage for exact uniform). 0 = read everything").
		Done().
		Flag("-sample-seed").
		Int().
		Global().
		Default(0).
		Help("Seed for -sample; when omitted, one is chosen and printed to stderr").
		Done().
		Flag("FILE").
		String().
		Variadic().
		Completer(&cf.FileCompleter{Pattern: "*.tsv"}).
		Global().
		Default("").
		Help("Input TSV file(s) (or stdin if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			cfg := extractMultiFileConfig(ctx)

			if rv, _ := ctx.GlobalFlags["-records"].(bool); rv {
				sn := int64(-1)
				if v, _ := ctx.GlobalFlags["-sample"].(int); v > 0 {
					sn = int64(v)
				}
				return runFromRecords("tsv", cfg.files, sn)
			}
			sampleN, _ := ctx.GlobalFlags["-sample"].(int)
			sampleSeed, _ := ctx.GlobalFlags["-sample-seed"].(int)
			if sampleN > 0 {
				if len(cfg.files) != 1 {
					return fmt.Errorf("from %s -sample needs exactly one file (got %d) — for stdin or multi-file input use the `sample` pipe stage", "tsv", len(cfg.files))
				}
				if len(ctx.RemainingArgs) > 0 {
					return fmt.Errorf("from tsv -sample cannot combine with pushdown (--)")
				}
				return executeFromTSVSample(cfg.files[0], sampleN, int64(sampleSeed), flagWasProvided(ctx, "-sample-seed"), cfg.generate)
			}
			if sampleN < 0 {
				return fmt.Errorf("from tsv -sample must be positive, got %d", sampleN)
			}

			if len(ctx.RemainingArgs) > 0 {
				if len(cfg.files) == 0 {
					return fmt.Errorf("pushdown (--) requires at least one file")
				}
				return executeFromMultiFilePushdown(cfg.files, "tsv", cfg.sourceField, cfg.unordered, ctx.RemainingArgs)
			}

			if len(cfg.files) <= 1 {
				inputFile := ""
				if len(cfg.files) == 1 {
					inputFile = cfg.files[0]
				}
				return executeFromTSV(inputFile, cfg.generate)
			}

			readFile := func(file *os.File) iter.Seq[ssql.Record] {
				return ssql.ReadTSVFromReader(file)
			}
			readHeaders := func(filename string) ([]string, error) {
				file, err := os.Open(filename)
				if err != nil {
					return nil, err
				}
				defer file.Close()
				return readTSVHeaders(file)
			}
			return executeFromMultiFile(cfg, "TSV", readFile, readHeaders)
		}).
		Done()
}

// executeFromTSV handles TSV reading for both the subcommand and bare form.
func executeFromTSV(inputFile string, generate bool) error {
	if schemaMode() {
		var r io.Reader = os.Stdin
		if inputFile != "" {
			if ssql.IsHTTPURL(inputFile) {
				body, err := ssql.OpenHTTPStream(inputFile)
				if err != nil {
					return err
				}
				defer body.Close()
				r = body
			} else {
				f, err := os.Open(inputFile)
				if err != nil {
					return fmt.Errorf("reading TSV file: %w", err)
				}
				defer f.Close()
				r = f
			}
		}
		// Same delimiter auto-detection as the readers and typed
		// sampling (first non-identifier byte; default tab) — a raw
		// tab split here made Ctrl-O field completion on a
		// pipe-delimited file offer one bogus "name|age|dept" field.
		line, _ := bufio.NewReader(r).ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		cr := csv.NewReader(strings.NewReader(line))
		cr.Comma = rune(lib.DetectDelimInHeader(line))
		headers, err := cr.Read()
		if err != nil {
			headers = strings.Split(line, string(lib.DetectDelimInHeader(line)))
		}
		return writeSchemaModeOutput(os.Stdout, headers)
	}

	if shouldGenerate(generate) {
		return generateFromTSVCode(inputFile)
	}

	var records iter.Seq[ssql.Record]
	if inputFile == "" {
		records = ssql.ReadTSVFromReader(os.Stdin)
	} else if ssql.IsHTTPURL(inputFile) {
		body, err := ssql.OpenHTTPStream(inputFile)
		if err != nil {
			return err
		}
		defer body.Close()
		records = ssql.ReadTSVFromReader(body)
	} else {
		file, err := os.Open(inputFile)
		if err != nil {
			return fmt.Errorf("reading TSV file: %w", err)
		}
		defer file.Close()
		records = ssql.ReadTSVFromReader(file)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// generateFromTSVCode generates Go code for reading TSV.
func generateFromTSVCode(filename string) error {
	if typedMode() {
		return generateFromTSVCodeTyped(filename)
	}

	var code string
	var imports []string
	var params []lib.CodeParam

	if filename == "" {
		code = `records := ssql.ReadTSVFromReader(os.Stdin)`
		imports = []string{"os"}
	} else {
		params = append(params, lib.CodeParam{Name: "input", Default: filename, Help: "input TSV file", VarName: "flagInput"})
		code = `records, err := ssql.ReadTSV(*flagInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading TSV: %w", err))
		os.Exit(1)
	}`
		imports = []string{"fmt", "os"}
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// typedDelimArg renders the optional typed.WithDelim(...) trailing arg
// for a detected delimiter — empty for the default tab. Shared by the
// from-tsv typed init and the typed join right-side reader.
func typedDelimArg(delim byte) string {
	if delim == '\t' {
		return ""
	}
	switch delim {
	case '|':
		return ", typed.WithDelim('|')"
	case ':':
		return ", typed.WithDelim(':')"
	case ',':
		return ", typed.WithDelim(',')"
	case ';':
		return ", typed.WithDelim(';')"
	case ' ':
		return ", typed.WithDelim(' ')"
	default:
		return fmt.Sprintf(", typed.WithDelim(0x%02x)", delim)
	}
}

// generateFromTSVCodeTyped emits a Phase-1.8 typed-mode init fragment
// for `ssql from tsv FILE`. Auto-detects the delimiter (first
// non-identifier byte in the header; defaults to '\t'), samples the
// file to infer per-column Go types, emits the corresponding struct
// definition, and produces a typed.ReadDelim[T] /
// typed.ReadDelimParallel[T] call. When the detected delimiter is
// not '\t', emits a typed.WithDelim(...) option.
func generateFromTSVCodeTyped(filename string) error {
	if filename == "" {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: 'from tsv' from stdin not supported in typed mode (need a file to sample for schema inference)"))
	}

	schema, structDef, delim, err := lib.SampleTSVSchema(filename, "", 0)
	if err != nil {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: %w", err))
	}

	delimArg := typedDelimArg(delim)

	params := []lib.CodeParam{{
		Name: "input", Default: filename, Help: "input TSV file", VarName: "flagInput",
	}}
	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if needsTimeImport(schema) {
		imports = append(imports, "time")
	}

	// Always emit BOTH templates; planner picks per pipeline.
	parallelCode := fmt.Sprintf(`records := typed.ReadDelimParallel[%s](*flagInput, runtime.GOMAXPROCS(0)%s)`, schema.TypeName, delimArg)
	parallelImports := append(append([]string{}, imports...), "runtime")
	serialCode := fmt.Sprintf(`records := typed.ReadDelim[%s](*flagInput%s)`, schema.TypeName, delimArg)
	serialImports := append([]string{}, imports...)

	frag := lib.NewInitFragment("records", parallelCode, parallelImports, getCommandString())
	frag.Params = params
	frag.OutputTypedSchema = schema
	frag.StructDefs = []string{structDef}
	frag.IsStream = true
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeNone, Produces: lib.ShapeStream}
	frag.AltCodeIfSeq = serialCode
	frag.AltImportsIfSeq = serialImports
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeNone, Produces: lib.ShapeSeqTyped}
	return lib.WriteCodeFragment(frag)
}

func readTSVHeaders(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty file")
	}
	header := scanner.Text()
	sep := ssql.DetectTSVSeparator(header)
	return strings.Split(header, string(sep)), nil
}

// executeFromTSVSample is the -sample path for TSV (byte-offset
// sampling; delimiter auto-detected by the standard TSV reader).
func executeFromTSVSample(inputFile string, n int, seed int64, seedGiven bool, generate bool) error {
	resolvedSeed := resolveSampleSeed(seed, seedGiven)
	if shouldGenerate(generate) {
		return generateFromFileSampleCode("ssql.SampleTSVFile", "input TSV file", inputFile, n, resolvedSeed)
	}
	records, err := ssql.SampleTSVFile(inputFile, n, resolvedSeed)
	if err != nil {
		return err
	}
	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}
