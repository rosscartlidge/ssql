package commands

import (
	"fmt"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func registerFromParquet(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("parquet").
		Description("Read Parquet file (columnar format, DuckDB compatible)").
		Example("ssql from parquet data.parquet | ssql to table", "Read Parquet file").
		Example("ssql from parquet data.parquet -columns name -columns age -columns dept | ssql to table", "Read only selected columns (faster for wide files)").
		Example("ssql from parquet data.parquet | ssql to csv output.csv", "Convert Parquet to CSV").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("FILE").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.parquet"}).
			Global().
			Required().
			Help("Input Parquet file (random access required, no stdin)").
			Done().

		Flag("-columns", "-c").
			Arg("column").
				FieldsFromFlag("FILE").
				Done().
			Accumulate().
			Global().
			Help("Column to read (repeat for multiple). Omit for all columns. Reduces I/O for wide files.").
			Done().

		Handler(func(ctx *cf.Context) error {
			var inputFile string
			var generate bool

			if fileVal, ok := ctx.GlobalFlags["FILE"]; ok {
				inputFile = fileVal.(string)
			}
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			// Extract accumulated column names
			var columns []string
			if colVal, ok := ctx.GlobalFlags["-columns"]; ok && colVal != nil {
				if colSlice, ok := colVal.([]any); ok {
					for _, v := range colSlice {
						if s, ok := v.(string); ok && s != "" {
							columns = append(columns, s)
						}
					}
				}
			}

			return executeFromParquet(inputFile, columns, generate)
		}).
		Done()
}

// executeFromParquet handles Parquet reading.
func executeFromParquet(inputFile string, columns []string, generate bool) error {
	// Schema mode answers from the FOOTER — names AND real wire types,
	// O(footer). Before this branch existed, a schema query fell
	// through to a full-file decode (14.6M rows to list two columns).
	if schemaMode() {
		if inputFile == "" {
			return fmt.Errorf("Parquet requires a file (random access format, no stdin support)")
		}
		names, types, err := ssql.ParquetSchemaFields(inputFile)
		if err != nil {
			return fmt.Errorf("reading Parquet schema: %w", err)
		}
		if len(columns) > 0 {
			names, types = filterSchemaColumns(names, types, columns)
		}
		return writeSchemaModeOutputTyped(os.Stdout, names, types)
	}

	if shouldGenerate(generate) {
		return generateFromParquetCode(inputFile, columns)
	}

	if inputFile == "" {
		return fmt.Errorf("Parquet requires a file (random access format, no stdin support)")
	}

	records, err := ssql.ReadParquetColumns(inputFile, columns)
	if err != nil {
		return fmt.Errorf("reading Parquet file: %w", err)
	}

	records = wrapWithFieldCaching(records, inputFile)
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// generateFromParquetCode generates Go code for reading Parquet.
func generateFromParquetCode(filename string, columns []string) error {
	if typedMode() {
		return generateFromParquetCodeTyped(filename, columns)
	}
	if filename == "" {
		return fmt.Errorf("Parquet code generation requires a file (no stdin support)")
	}

	params := []lib.CodeParam{
		{Name: "input", Default: filename, Help: "input Parquet file", VarName: "flagInput"},
	}

	var code string
	if len(columns) > 0 {
		// Build Go string slice literal for the columns
		quoted := make([]string, len(columns))
		for i, c := range columns {
			quoted[i] = fmt.Sprintf("%q", c)
		}
		colLiteral := "[]string{" + strings.Join(quoted, ", ") + "}"
		code = fmt.Sprintf(`records, err := ssql.ReadParquetColumns(*flagInput, %s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("reading Parquet: %%w", err))
		os.Exit(1)
	}`, colLiteral)
	} else {
		code = `records, err := ssql.ReadParquetColumns(*flagInput, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", fmt.Errorf("reading Parquet: %w", err))
		os.Exit(1)
	}`
	}

	frag := lib.NewInitFragment("records", code, []string{"fmt", "os"}, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// filterSchemaColumns restricts a schema answer to the -columns
// selection, preserving selection order (matching the reader).
func filterSchemaColumns(names []string, types map[string]string, columns []string) ([]string, map[string]string) {
	keep := make([]string, 0, len(columns))
	kt := make(map[string]string, len(columns))
	have := make(map[string]bool, len(names))
	for _, n := range names {
		have[n] = true
	}
	for _, c := range columns {
		if have[c] {
			keep = append(keep, c)
			kt[c] = types[c]
		}
	}
	return keep, kt
}
