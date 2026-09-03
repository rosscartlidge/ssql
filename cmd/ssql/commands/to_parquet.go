package commands

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerToParquet registers the "to parquet" subcommand
func registerToParquet(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("parquet").
		Description("Write as Parquet file (Snappy compression, DuckDB compatible)").
		Example("ssql from data.csv | ssql to parquet output.parquet", "Convert CSV to Parquet").
		Example("ssql from large.json | ssql to parquet data.parquet -row-group-size 500000", "Tune row-group size for downstream parallel reads").
		Example("ssql from large.csv | ssql to parquet data.parquet -compression zstd", "Use ZSTD compression").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("-row-group-size").
		Int().
		Global().
		Default(1_000_000).
		Help("Maximum rows per Parquet row group (default 1_000_000). Smaller → more reader-side parallelism. Pass 0 for a single row group.").
		Done().
		Flag("-compression").
		String().
		Global().
		Default("snappy").
		Help("Compression codec: snappy (default), gzip, zstd, none").
		Done().
		Flag("FILE").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.parquet"}).
		Global().
		Required().
		Help("Output Parquet file (required)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var generate bool
			rowGroupSize := 1_000_000
			compression := "snappy"

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				outputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}
			if v, ok := ctx.GlobalFlags["-row-group-size"]; ok {
				if n, ok := v.(int64); ok {
					rowGroupSize = int(n)
				} else if n, ok := v.(int); ok {
					rowGroupSize = n
				}
			}
			if v, ok := ctx.GlobalFlags["-compression"]; ok {
				if s, ok := v.(string); ok && s != "" {
					compression = s
				}
			}
			if !validCompressionFlag(compression) {
				return fmt.Errorf("ssql to parquet: unknown compression %q (accepted: snappy, gzip, zstd, none)", compression)
			}

			if outputFile == "" {
				return fmt.Errorf("output file required")
			}

			if shouldGenerate(generate) {
				return generateToParquetCode(outputFile, rowGroupSize, compression)
			}

			// Read JSONL from stdin (with schema if present)
			schemaAndRecords := lib.ReadJSONLWithSchema(ctx.Stdin())
			records := schemaAndRecords.Records

			if err := ssql.WriteParquet(records, outputFile,
				ssql.WithRowGroupSize(rowGroupSize),
				ssql.WithCompression(compression),
			); err != nil {
				return fmt.Errorf("writing Parquet file: %w", err)
			}

			return nil
		}).
		Done()
}

func validCompressionFlag(s string) bool {
	switch strings.ToLower(s) {
	case "snappy", "gzip", "zstd", "none", "uncompressed":
		return true
	}
	return false
}

func generateToParquetCode(filename string, rowGroupSize int, compression string) error {
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
	var prevSchema *lib.TypedSchema
	var prevIsStream bool
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		prevSchema = fragments[len(fragments)-1].OutputTypedSchema
		prevIsStream = fragments[len(fragments)-1].IsStream
	} else {
		inputVar = "records"
	}

	params := []lib.CodeParam{
		{Name: "output", Default: filename, Help: "output Parquet file", VarName: "flagOutput"},
	}
	optsArg := fmt.Sprintf(", typed.WithRowGroupSize(%d), typed.WithCompression(%q)", rowGroupSize, compression)
	// Phase B fall-through: prevSchema==nil → Record-mode upstream.
	// to_parquet's record-mode path uses ssql.WriteParquet below.
	if typedMode() && prevSchema != nil {
		// Stream → call Stream.WriteParquet (per-shard one row group);
		// iter.Seq[T] → call typed.WriteParquet (single row group of
		// configured size).
		var code string
		if prevIsStream {
			code = fmt.Sprintf(`if err := %s.WriteParquet(*flagOutput%s); err != nil {
		fmt.Fprintf(ctx.Stderr(), "Error writing Parquet: %%v\n", err)
		os.Exit(1)
	}`, inputVar, optsArg)
		} else {
			code = fmt.Sprintf(`if err := typed.WriteParquet(%s, *flagOutput%s); err != nil {
		fmt.Fprintf(ctx.Stderr(), "Error writing Parquet: %%v\n", err)
		os.Exit(1)
	}`, inputVar, optsArg)
		}
		frag := lib.NewFinalFragment(inputVar, code,
			[]string{"fmt", "os", "github.com/rosscartlidge/ssql/v4/typed"}, getCommandString())
		frag.Params = params
		frag.InputTypedSchema = prevSchema
		return lib.WriteCodeFragment(frag)
	}

	code := fmt.Sprintf(`if err := ssql.WriteParquet(%s, *flagOutput, ssql.WithRowGroupSize(%d), ssql.WithCompression(%q)); err != nil {
		fmt.Fprintf(ctx.Stderr(), "Error writing Parquet: %%v\n", err)
		os.Exit(1)
	}`, inputVar, rowGroupSize, compression)

	frag := lib.NewFinalFragment(inputVar, code, []string{"fmt", "os"}, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
