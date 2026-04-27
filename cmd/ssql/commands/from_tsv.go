package commands

import (
	"bufio"
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
	if shouldGenerate(generate) {
		return generateFromTSVCode(inputFile)
	}

	var records iter.Seq[ssql.Record]
	if inputFile == "" {
		records = ssql.ReadTSVFromReader(os.Stdin)
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

	// Build the optional WithDelim trailing arg. Only emit when the
	// delimiter isn't the default tab — keeps the generated code
	// minimal in the common case.
	var delimArg string
	if delim != '\t' {
		// Use Go rune literal syntax — handles printable ASCII
		// without needing escape rules for common separators.
		switch delim {
		case '|':
			delimArg = ", typed.WithDelim('|')"
		case ':':
			delimArg = ", typed.WithDelim(':')"
		case ',':
			delimArg = ", typed.WithDelim(',')"
		case ';':
			delimArg = ", typed.WithDelim(';')"
		case ' ':
			delimArg = ", typed.WithDelim(' ')"
		default:
			delimArg = fmt.Sprintf(", typed.WithDelim(0x%02x)", delim)
		}
	}

	params := []lib.CodeParam{{
		Name: "input", Default: filename, Help: "input TSV file", VarName: "flagInput",
	}}
	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if needsTimeImport(schema) {
		imports = append(imports, "time")
	}

	var code string
	isStream := false
	if parallelMode() {
		code = fmt.Sprintf(`records := typed.ReadDelimParallel[%s](*flagInput, runtime.GOMAXPROCS(0)%s)`, schema.TypeName, delimArg)
		imports = append(imports, "runtime")
		isStream = true
	} else {
		code = fmt.Sprintf(`records := typed.ReadDelim[%s](*flagInput%s)`, schema.TypeName, delimArg)
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	frag.OutputTypedSchema = schema
	frag.StructDefs = []string{structDef}
	frag.IsStream = isStream
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
