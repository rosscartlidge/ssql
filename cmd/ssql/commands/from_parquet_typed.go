//go:build !slim

package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/apache/arrow/go/v18/parquet/file"
	"github.com/apache/arrow/go/v18/parquet/pqarrow"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// generateFromParquetCodeTyped emits a typed-mode init fragment for
// `ssql from parquet FILE [-columns ...]`. Reads the Parquet file's
// schema (no sampling needed — Parquet stores types explicitly), emits
// the corresponding Go struct definition, and a typed.ReadParquet[T]
// (or typed.ReadParquetParallel[T] in parallel mode) call.
//
// `-columns` flags are threaded through as typed.ParquetColumns(...)
// arguments — the runtime then reads only the named columns from the
// file. This is the primary I/O lever for wide Parquet tables: 3 of
// 50 columns means ~94% less disk read.
//
// Pre-emptive pruning: if columns is empty, the codegen path could in
// principle run the same downstream-field analysis that `generate ssql`
// uses (collectDownstreamFields) and emit ParquetColumns automatically.
// Not implemented here yet — for now users get column projection by
// either (a) passing `-columns` explicitly, or (b) running
// `generate ssql` first and piping the result back through `from`.
func generateFromParquetCodeTyped(filename string, columns []string) error {
	if filename == "" {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: 'from parquet' requires a file (random access format, no stdin)"))
	}

	schema, err := readParquetTypedSchema(filename, columns)
	if err != nil {
		return lib.WriteErrorAndExit(getCommandString(),
			fmt.Errorf("ssql generate go -typed: %w", err))
	}
	structDef := lib.RenderStructDef(schema)

	params := []lib.CodeParam{{
		Name: "input", Default: filename, Help: "input Parquet file", VarName: "flagInput",
	}}
	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	if needsTimeImport(schema) {
		imports = append(imports, "time")
	}

	// Build the optional ParquetColumns(...) trailing arg.
	var colsArg string
	if len(columns) > 0 {
		quoted := make([]string, len(columns))
		for i, c := range columns {
			quoted[i] = fmt.Sprintf("%q", c)
		}
		colsArg = ", typed.ParquetColumns(" + strings.Join(quoted, ", ") + ")"
	}

	var code string
	isStream := false
	if parallelMode() {
		code = fmt.Sprintf(`records := typed.ReadParquetParallel[%s](*flagInput, runtime.GOMAXPROCS(0)%s)`, schema.TypeName, colsArg)
		imports = append(imports, "runtime")
		isStream = true
	} else {
		code = fmt.Sprintf(`records := typed.ReadParquet[%s](*flagInput%s)`, schema.TypeName, colsArg)
	}

	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	frag.OutputTypedSchema = schema
	frag.StructDefs = []string{structDef}
	frag.IsStream = isStream
	produces := lib.ShapeSeqTyped
	if isStream {
		produces = lib.ShapeStream
	}
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeNone, Produces: produces}
	return lib.WriteCodeFragment(frag)
}

// readParquetTypedSchema opens the Parquet file, reads its Arrow
// schema, and projects it into a lib.TypedSchema using Go type names
// the typed runtime understands. If columns is non-empty, the
// returned schema only contains those columns (in the order given).
func readParquetTypedSchema(filename string, columns []string) (*lib.TypedSchema, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening Parquet file: %w", err)
	}
	defer f.Close()

	pf, err := file.NewParquetReader(f)
	if err != nil {
		return nil, fmt.Errorf("reading Parquet metadata: %w", err)
	}
	defer pf.Close()

	reader, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, memory.DefaultAllocator)
	if err != nil {
		return nil, fmt.Errorf("creating Arrow reader: %w", err)
	}
	arrowSchema, err := reader.Schema()
	if err != nil {
		return nil, fmt.Errorf("reading Parquet schema: %w", err)
	}

	// Optional column selection. Resolution is case-insensitive,
	// matching the typed runtime.
	var keep []arrow.Field
	if len(columns) > 0 {
		byLower := make(map[string]arrow.Field, len(arrowSchema.Fields()))
		for _, f := range arrowSchema.Fields() {
			byLower[strings.ToLower(f.Name)] = f
		}
		for _, c := range columns {
			f, ok := byLower[strings.ToLower(c)]
			if !ok {
				return nil, fmt.Errorf("column %q not found in Parquet schema", c)
			}
			keep = append(keep, f)
		}
	} else {
		keep = arrowSchema.Fields()
	}

	fields := make([]lib.TypedSchemaField, 0, len(keep))
	usedNames := make(map[string]int, len(keep))
	for _, af := range keep {
		gn := lib.GoNameFromColumn(af.Name)
		if usedNames[gn] > 0 {
			usedNames[gn]++
			gn = fmt.Sprintf("%s%d", gn, usedNames[gn])
		} else {
			usedNames[gn] = 1
		}
		gt, err := arrowToGoTypeName(af.Type)
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", af.Name, err)
		}
		fields = append(fields, lib.TypedSchemaField{
			Name:   af.Name,
			GoName: gn,
			GoType: gt,
		})
	}

	return &lib.TypedSchema{
		TypeName: lib.TypeNameFromFilename(filename),
		Fields:   fields,
	}, nil
}

// arrowToGoTypeName maps an Arrow data type to the Go type identifier
// the typed runtime expects. Mirrors the cases the parquet decoder
// handles in typed/io_parquet.go.
func arrowToGoTypeName(t arrow.DataType) (string, error) {
	switch t.ID() {
	case arrow.STRING, arrow.LARGE_STRING:
		return "string", nil
	case arrow.BOOL:
		return "bool", nil
	case arrow.INT8:
		return "int8", nil
	case arrow.INT16:
		return "int16", nil
	case arrow.INT32:
		return "int32", nil
	case arrow.INT64:
		return "int64", nil
	case arrow.UINT8:
		return "uint8", nil
	case arrow.UINT16:
		return "uint16", nil
	case arrow.UINT32:
		return "uint32", nil
	case arrow.UINT64:
		return "uint64", nil
	case arrow.FLOAT32:
		return "float32", nil
	case arrow.FLOAT64:
		return "float64", nil
	case arrow.TIMESTAMP, arrow.DATE32, arrow.DATE64:
		return "time.Time", nil
	}
	return "", fmt.Errorf("unsupported Arrow column type %v", t)
}
