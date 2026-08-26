//go:build !slim

package ssql

import (
	"context"
	"fmt"
	"io"
	"iter"
	"os"

	"strings"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/apache/arrow/go/v18/parquet"
	"github.com/apache/arrow/go/v18/parquet/compress"
	"github.com/apache/arrow/go/v18/parquet/file"
	"github.com/apache/arrow/go/v18/parquet/pqarrow"
)

// ReadParquet reads a Parquet file and returns an iterator of Records.
func ReadParquet(filename string) (iter.Seq[Record], error) {
	if IsHTTPURL(filename) {
		h, err := OpenHTTPFile(filename)
		if err != nil {
			return nil, err
		}
		return ReadParquetFromReader(h)
	}

	return ReadParquetColumns(filename, nil)
}

// ReadParquetColumns reads a Parquet file, optionally selecting only the named columns.
// If columns is nil or empty, all columns are read.
// This is the primary optimization lever for wide Parquet files — reading 3 of 50 columns
// means ~94% less I/O.
func ReadParquetColumns(filename string, columns []string) (iter.Seq[Record], error) {
	if IsHTTPURL(filename) {
		h, err := OpenHTTPFile(filename)
		if err != nil {
			return nil, err
		}
		return readParquetFromReaderWithColumns(h, columns)
	}

	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening Parquet file: %w", err)
	}

	return readParquetFromReaderWithColumns(f, columns)
}

// ReadParquetFromReader reads all columns from a Parquet reader.
func ReadParquetFromReader(r parquet.ReaderAtSeeker) (iter.Seq[Record], error) {
	return readParquetFromReaderWithColumns(r, nil)
}

func readParquetFromReaderWithColumns(r parquet.ReaderAtSeeker, columns []string) (iter.Seq[Record], error) {
	ctx := context.Background()
	mem := memory.DefaultAllocator

	pf, err := file.NewParquetReader(r)
	if err != nil {
		return nil, fmt.Errorf("reading Parquet metadata: %w", err)
	}

	reader, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, mem)
	if err != nil {
		return nil, fmt.Errorf("creating Arrow reader: %w", err)
	}

	arrowSchema, err := reader.Schema()
	if err != nil {
		return nil, fmt.Errorf("reading Parquet schema: %w", err)
	}

	// Resolve column indices
	var colIndices []int
	if len(columns) > 0 {
		colSet := make(map[string]bool)
		for _, c := range columns {
			colSet[strings.TrimSpace(c)] = true
		}
		for i, field := range arrowSchema.Fields() {
			if colSet[field.Name] {
				colIndices = append(colIndices, i)
			}
		}
		if len(colIndices) == 0 {
			return nil, fmt.Errorf("none of the requested columns found in Parquet schema")
		}
	} else {
		// All columns
		for i := range arrowSchema.Fields() {
			colIndices = append(colIndices, i)
		}
	}

	// All row groups
	var rowGroups []int
	for i := 0; i < pf.NumRowGroups(); i++ {
		rowGroups = append(rowGroups, i)
	}

	tbl, err := reader.ReadRowGroups(ctx, colIndices, rowGroups)
	if err != nil {
		return nil, fmt.Errorf("reading Parquet row groups: %w", err)
	}

	resultSchema := tbl.Schema()
	ssqlSchema := arrowSchemaToSSQLSchema(resultSchema)

	return func(yield func(Record) bool) {
		defer tbl.Release()

		nRows := int(tbl.NumRows())
		nCols := int(tbl.NumCols())

		for row := 0; row < nRows; row++ {
			values := make([]any, len(ssqlSchema.fields))

			for col := 0; col < nCols; col++ {
				chunked := tbl.Column(col)
				offset := row
				for _, chunk := range chunked.Data().Chunks() {
					if offset < chunk.Len() {
						if !chunk.IsNull(offset) {
							idx := ssqlSchema.Index(resultSchema.Field(col).Name)
							if idx >= 0 {
								values[idx] = arrowValueToGo(chunk, offset)
							}
						}
						break
					}
					offset -= chunk.Len()
				}
			}

			if !yield(Record{schema: ssqlSchema, values: values}) {
				return
			}
		}
	}, nil
}

// ParquetWriteOption configures a Parquet writer. Pass to
// [WriteParquet] / [WriteParquetToWriter].
type ParquetWriteOption func(*parquetWriteOpts)

type parquetWriteOpts struct {
	rowGroupSize int64
	compression  string
}

// WithRowGroupSize sets the maximum number of rows per Parquet row
// group. Default is 1_000_000. Smaller values increase reader-side
// parallelism (one row group → one shard in
// [github.com/rosscartlidge/ssql/v4/typed.ReadParquetParallel]) at
// the cost of more metadata overhead. Pass 0 to put all rows in a
// single group (caps reader parallelism at 1).
func WithRowGroupSize(n int) ParquetWriteOption {
	return func(o *parquetWriteOpts) { o.rowGroupSize = int64(n) }
}

// WithCompression sets the Parquet column compression. Accepted
// values: "snappy" (default), "gzip", "zstd", "none"/"uncompressed".
// Unknown values fall back to Snappy with a warning to stderr.
func WithCompression(name string) ParquetWriteOption {
	return func(o *parquetWriteOpts) { o.compression = name }
}

func resolveParquetWriteOpts(opts []ParquetWriteOption) parquetWriteOpts {
	o := parquetWriteOpts{rowGroupSize: 1_000_000, compression: "snappy"}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

func parquetCompressionCodec(name string) compress.Compression {
	switch strings.ToLower(name) {
	case "", "snappy":
		return compress.Codecs.Snappy
	case "gzip":
		return compress.Codecs.Gzip
	case "zstd":
		return compress.Codecs.Zstd
	case "none", "uncompressed":
		return compress.Codecs.Uncompressed
	default:
		fmt.Fprintf(os.Stderr, "ssql.WriteParquet: unknown compression %q, falling back to snappy\n", name)
		return compress.Codecs.Snappy
	}
}

// WriteParquet writes Records to a Parquet file. Defaults: Snappy
// compression, 1_000_000-row row-groups (so the file can be read
// in parallel by `typed.ReadParquetParallel` or DuckDB). Adjust
// via [WithRowGroupSize] / [WithCompression].
func WriteParquet(records iter.Seq[Record], filename string, opts ...ParquetWriteOption) error {
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating Parquet file: %w", err)
	}
	defer f.Close()

	return WriteParquetToWriter(records, f, opts...)
}

// WriteParquetToWriter writes Records to a Parquet format writer.
// Same defaults as [WriteParquet].
func WriteParquetToWriter(records iter.Seq[Record], w io.Writer, opts ...ParquetWriteOption) error {
	mem := memory.DefaultAllocator
	o := resolveParquetWriteOpts(opts)

	// Collect records to build Arrow table
	var allRecords []Record
	var arrowSchema *arrow.Schema

	for record := range records {
		if arrowSchema == nil {
			arrowSchema = recordToArrowSchema(record)
		}
		allRecords = append(allRecords, record)
	}

	if len(allRecords) == 0 || arrowSchema == nil {
		return nil
	}

	// Build Arrow record batch
	batch, err := recordsToArrowBatch(allRecords, arrowSchema, mem)
	if err != nil {
		return fmt.Errorf("building Arrow batch: %w", err)
	}
	defer batch.Release()

	// Convert batch to table
	tbl := array.NewTableFromRecords(arrowSchema, []arrow.Record{batch})
	defer tbl.Release()

	props := parquet.NewWriterProperties(
		parquet.WithCompression(parquetCompressionCodec(o.compression)),
	)
	arrProps := pqarrow.NewArrowWriterProperties()

	chunk := o.rowGroupSize
	nRows := int64(len(allRecords))
	if chunk <= 0 || chunk > nRows {
		chunk = nRows
	}
	return pqarrow.WriteTable(tbl, w, chunk, props, arrProps)
}

// ParquetRowCount returns the exact row count from a parquet file's
// footer metadata — O(footer read), no data scan. Used by serve's
// head-throughput display (rows/sec) where line formats need a cached
// newline count but parquet carries the answer natively.
func ParquetRowCount(filename string) (int64, error) {
	if IsHTTPURL(filename) {
		h, err := OpenHTTPFile(filename)
		if err != nil {
			return 0, err
		}
		pf, err := file.NewParquetReader(h)
		if err != nil {
			return 0, fmt.Errorf("reading parquet footer: %w", err)
		}
		defer pf.Close()
		return pf.NumRows(), nil
	}

	f, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	pf, err := file.NewParquetReader(f)
	if err != nil {
		return 0, fmt.Errorf("reading parquet footer: %w", err)
	}
	defer pf.Close()
	return pf.NumRows(), nil
}

// ParquetSchemaFields returns the column names and ssql wire types
// ("int"/"float"/"bool"/"string") from a parquet file's footer —
// O(footer), no data pages read. Powers schema mode (and therefore
// field-name completion) for parquet sources: before this, a schema
// query decoded the ENTIRE file to answer a metadata question.
func ParquetSchemaFields(filename string) ([]string, map[string]string, error) {
	if IsHTTPURL(filename) {
		h, err := OpenHTTPFile(filename)
		if err != nil {
			return nil, nil, err
		}
		return parquetSchemaFieldsFromReader(h)
	}

	f, err := os.Open(filename)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	return parquetSchemaFieldsFromReader(f)
}

func parquetSchemaFieldsFromReader(r parquet.ReaderAtSeeker) ([]string, map[string]string, error) {
	pf, err := file.NewParquetReader(r)
	if err != nil {
		return nil, nil, fmt.Errorf("reading parquet footer: %w", err)
	}
	defer pf.Close()
	reader, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, memory.DefaultAllocator)
	if err != nil {
		return nil, nil, err
	}
	arrowSchema, err := reader.Schema()
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, arrowSchema.NumFields())
	types := make(map[string]string, arrowSchema.NumFields())
	for i := 0; i < arrowSchema.NumFields(); i++ {
		fld := arrowSchema.Field(i)
		names = append(names, fld.Name)
		types[fld.Name] = wireTypeForArrow(fld.Type)
	}
	return names, types, nil
}

// wireTypeForArrow maps an arrow type to the ssql wire-type vocabulary.
func wireTypeForArrow(t arrow.DataType) string {
	switch t.ID() {
	case arrow.INT8, arrow.INT16, arrow.INT32, arrow.INT64,
		arrow.UINT8, arrow.UINT16, arrow.UINT32, arrow.UINT64:
		return "int"
	case arrow.FLOAT16, arrow.FLOAT32, arrow.FLOAT64:
		return "float"
	case arrow.BOOL:
		return "bool"
	default:
		return "string"
	}
}
