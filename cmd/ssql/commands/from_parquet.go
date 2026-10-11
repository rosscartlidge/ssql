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
	zoneFlag(cmd.Subcommand("parquet")).
		Description("Read Parquet file (columnar format, DuckDB compatible)").
		Example("ssql from parquet data.parquet | ssql to table", "Read Parquet file").
		Example("ssql from parquet data.parquet -columns name -columns age -columns dept | ssql to table", "Read only selected columns (faster for wide files)").
		Example("ssql from parquet data.parquet | ssql to csv output.csv", "Convert Parquet to CSV").
		Example("ssql from parquet 'sales/year=*/*.parquet' | ssql group-by region -sum amount total", "Read every matching file as one stream (the quoted glob is expanded each time the pipeline runs)").
		Flag("-generate", "-g").
		Bool().
		Global().
		Help("Generate Go code instead of executing").
		Done().
		Flag("FILE").
		String().
		Variadic().
		Completer(&cf.FileCompleter{Pattern: "*.parquet"}).
		Global().
		Required().
		Help("Input Parquet file(s) or glob patterns, read in order as one stream; the files must share one schema (random access required, no stdin)").
		Done().
		Flag("-records").
		Bool().
		Global().
		Default(false).
		Help("Print only the record count this invocation would produce (cheapest per format: parquet footer, line count for csv/tsv/jsonl) and exit").
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
			if err := resolveFromZone(ctx); err != nil {
				return err
			}
			files, err := ssql.ExpandFilePatterns(parquetFileArgs(ctx.GlobalFlags["FILE"]))
			if err != nil {
				return err
			}
			if rv, _ := ctx.GlobalFlags["-records"].(bool); rv {
				return runFromRecords("parquet", files, -1)
			}
			var generate bool
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

			return executeFromParquet(parquetFileArgs(ctx.GlobalFlags["FILE"]), files, columns, generate)
		}).
		Done()
}

// parquetFileArgs is the FILE flag's value as given (patterns
// unexpanded): a variadic flag arrives as []any, an older single one as
// a string.
func parquetFileArgs(v any) []string {
	var out []string
	switch v := v.(type) {
	case []string:
		out = v
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case string:
		if v != "" {
			out = []string{v}
		}
	}
	return out
}

// executeFromParquet handles Parquet reading. args are the FILE
// arguments as given (the generated program keeps a glob and expands it
// when it runs); files are their expansion now.
func executeFromParquet(args, files []string, columns []string, generate bool) error {
	if len(files) == 0 {
		return fmt.Errorf("Parquet requires a file (random access format, no stdin support)")
	}
	if len(files) > 1 {
		if err := ssql.CheckParquetSchemas(files); err != nil {
			return err
		}
	}
	// Schema mode answers from the FOOTER — names AND real wire types,
	// O(footer). Before this branch existed, a schema query fell
	// through to a full-file decode (14.6M rows to list two columns).
	if schemaMode() {
		names, types, err := ssql.ParquetSchemaFields(files[0])
		if err != nil {
			return fmt.Errorf("reading Parquet schema: %w", err)
		}
		if len(columns) > 0 {
			names, types = filterSchemaColumns(names, types, columns)
		}
		return writeSchemaModeOutputTyped(os.Stdout, names, types)
	}

	if shouldGenerate(generate) {
		return generateFromParquetCode(args, files, columns)
	}

	records, err := ssql.ReadParquetFiles(files, columns)
	if err != nil {
		return fmt.Errorf("reading Parquet file: %w", err)
	}

	records = wrapWithFieldCaching(records, files[0])
	return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
}

// singleParquetFile reports whether the arguments name one file with no
// pattern — the case generated code keeps its -input parameter for.
func singleParquetFile(args []string) bool {
	return len(args) == 1 && (ssql.IsHTTPURL(args[0]) || !strings.ContainsAny(args[0], "*?["))
}

// goStringSlice renders a []string literal.
func goStringSlice(items []string) string {
	quoted := make([]string, len(items))
	for i, c := range items {
		quoted[i] = fmt.Sprintf("%q", c)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// generateFromParquetCode generates Go code for reading Parquet. One
// plain file keeps the -input parameter; several, or a pattern, are
// written into the program as given, so a glob is expanded when the
// program runs.
func generateFromParquetCode(args, files []string, columns []string) error {
	if typedMode() {
		return generateFromParquetCodeTyped(args, files, columns)
	}
	colLiteral := "nil"
	if len(columns) > 0 {
		colLiteral = goStringSlice(columns)
	}
	var code string
	var params []lib.CodeParam
	if singleParquetFile(args) {
		params = []lib.CodeParam{
			{Name: "input", Default: args[0], Help: "input Parquet file", VarName: "flagInput"},
		}
		code = fmt.Sprintf(`records, err := ssql.ReadParquetColumns(*flagInput, %s)
	if err != nil {
		return fmt.Errorf("reading Parquet: %%w", err)
	}`, colLiteral)
	} else {
		code = fmt.Sprintf(`records, err := ssql.ReadParquetFiles(%s, %s)
	if err != nil {
		return fmt.Errorf("reading Parquet: %%w", err)
	}`, goStringSlice(args), colLiteral)
	}

	frag := fromInitFragment("records", code, []string{"fmt", "os"})
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
