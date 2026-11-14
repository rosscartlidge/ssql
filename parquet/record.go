// Package parquet reads and writes Parquet files as ssql records
// (Read, Write, …) and as typed structs (ReadTyped[T], WriteTyped[T],
// the parallel Stream forms), and reads a catalog's Parquet shards.
//
// It is its own package so that a program links Apache Arrow and
// Parquet only when it imports it (DFC168). Importing it also registers
// its reader with the root package, which reads a catalog's local
// Parquet shards in process when it is linked.
package parquet

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
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/internal/arrowconv"
	"github.com/rosscartlidge/ssql/v4/internal/pqcols"
)

func init() { ssql.RegisterParquetReader(Read) }

// Read reads a Parquet file and returns an iterator of Records.
func Read(filename string) (iter.Seq[ssql.Record], error) {
	return ReadColumns(filename, nil)
}

// ReadColumns reads a Parquet file, optionally selecting only the named columns.
// If columns is nil or empty, all columns are read.
// This is the primary optimization lever for wide Parquet files — reading 3 of 50 columns
// means ~94% less I/O.
func ReadColumns(filename string, columns []string) (iter.Seq[ssql.Record], error) {
	// A file or a URL (OpenInputAt: range requests over HTTP).
	f, err := ssql.OpenInputAt(filename)
	if err != nil {
		return nil, fmt.Errorf("opening Parquet file: %w", err)
	}

	return readParquetFromReaderWithColumns(f, columns)
}

// ReadFiles reads several Parquet files as one stream, in order:
// the files of each argument in turn, a glob argument's matches sorted
// (ExpandFilePatterns). The files must share one schema
// (CheckSchemas) — the error names the file and the column that
// differ — so every backend reads them alike. A file that fails to open
// partway through the stream panics with the error (ssql.Run reports
// it). One file is exactly ReadColumns.
func ReadFiles(paths, columns []string) (iter.Seq[ssql.Record], error) {
	files, err := ssql.ExpandFilePatterns(paths)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Parquet files given")
	}
	if len(files) == 1 {
		return ReadColumns(files[0], columns)
	}
	if err := CheckSchemas(files); err != nil {
		return nil, err
	}
	return func(yield func(ssql.Record) bool) {
		for _, f := range files {
			seq, err := ReadColumns(f, columns)
			if err != nil {
				panic(fmt.Errorf("%s: %w", f, err))
			}
			for r := range seq {
				if !yield(r) {
					return
				}
			}
		}
	}, nil
}

// CheckSchemas reads each file's footer and requires the same
// columns, in the same order, with the same types as the first file.
// Strict on purpose: DuckDB's read_parquet over a list binds the files
// to the first one's schema, the typed backend decodes into a struct
// derived from it, and the record backend would otherwise carry a
// different column set per file — one rule keeps them in agreement.
func CheckSchemas(files []string) error {
	var first []parquetColumnType
	for i, f := range files {
		cols, err := parquetColumnTypes(f)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		if i == 0 {
			first = cols
			continue
		}
		if d := diffParquetColumns(first, cols); d != "" {
			return fmt.Errorf("%s does not match %s: %s — files read together must share one schema", f, files[0], d)
		}
	}
	return nil
}

type parquetColumnType struct{ name, typ string }

func parquetColumnTypes(filename string) ([]parquetColumnType, error) {
	r, err := ssql.OpenInputAt(filename)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	pf, err := file.NewParquetReader(r)
	if err != nil {
		return nil, fmt.Errorf("reading parquet footer: %w", err)
	}
	defer pf.Close()
	reader, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, memory.DefaultAllocator)
	if err != nil {
		return nil, err
	}
	sch, err := reader.Schema()
	if err != nil {
		return nil, err
	}
	cols := make([]parquetColumnType, sch.NumFields())
	for i, f := range sch.Fields() {
		cols[i] = parquetColumnType{f.Name, f.Type.String()}
	}
	return cols, nil
}

// diffParquetColumns describes the first difference, or "" when equal.
func diffParquetColumns(want, got []parquetColumnType) string {
	for i := 0; i < len(want) || i < len(got); i++ {
		switch {
		case i >= len(got):
			return fmt.Sprintf("it lacks column %q", want[i].name)
		case i >= len(want):
			return fmt.Sprintf("it has an extra column %q", got[i].name)
		case want[i].name != got[i].name:
			return fmt.Sprintf("column %d is %q, not %q", i+1, got[i].name, want[i].name)
		case want[i].typ != got[i].typ:
			return fmt.Sprintf("column %q is %s, not %s", got[i].name, got[i].typ, want[i].typ)
		}
	}
	return ""
}

// ReadFrom reads all columns from a Parquet reader.
func ReadFrom(r parquet.ReaderAtSeeker) (iter.Seq[ssql.Record], error) {
	return readParquetFromReaderWithColumns(r, nil)
}

func readParquetFromReaderWithColumns(r parquet.ReaderAtSeeker, columns []string) (iter.Seq[ssql.Record], error) {
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
	// Selected columns come out in the order given — as the typed
	// reader, schema mode and the SQL projection have them (this reader
	// used the file's order until 2026-10-11, and skipped an unknown name
	// unless none matched).
	var colIndices []int
	if len(columns) > 0 {
		byName := make(map[string]int, arrowSchema.NumFields())
		for i, field := range arrowSchema.Fields() {
			byName[field.Name] = i
		}
		for _, c := range columns {
			i, ok := byName[strings.TrimSpace(c)]
			if !ok {
				names := make([]string, 0, arrowSchema.NumFields())
				for _, f := range arrowSchema.Fields() {
					names = append(names, f.Name)
				}
				return nil, fmt.Errorf("column %q not found in Parquet schema (columns: %s)", c, strings.Join(names, ", "))
			}
			colIndices = append(colIndices, i)
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

	// pqarrow reads by LEAF column: a nested field is several leaves, and
	// field indices read the wrong columns past the first one (DFC162 §1).
	leaves := pqcols.Leaves(reader.Manifest, colIndices)

	if h, ok := r.(*ssql.HTTPFile); ok {
		if err := h.Prefetch(parquetChunkRanges(pf, leaves, rowGroups, h.Size())); err != nil {
			return nil, fmt.Errorf("reading Parquet column chunks: %w", err)
		}
		defer h.DropPrefetched()
	}

	tbl, err := reader.ReadRowGroups(ctx, leaves, rowGroups)
	if err != nil {
		return nil, fmt.Errorf("reading Parquet row groups: %w", err)
	}

	resultSchema := tbl.Schema()
	ssqlSchema := arrowconv.SchemaToSSQL(resultSchema)
	// A UUID column arrives as 16 raw bytes; it reads as its canonical text.
	uuidField := pqcols.UUIDFields(pf, reader.Manifest)
	isUUID := make([]bool, resultSchema.NumFields())
	for col := range isUUID {
		if i := arrowSchema.FieldIndices(resultSchema.Field(col).Name); len(i) > 0 {
			isUUID[col] = uuidField[i[0]]
		}
	}

	return func(yield func(ssql.Record) bool) {
		defer tbl.Release()

		nRows := int(tbl.NumRows())
		nCols := int(tbl.NumCols())

		for row := 0; row < nRows; row++ {
			values := make([]any, ssqlSchema.Width())

			for col := 0; col < nCols; col++ {
				chunked := tbl.Column(col)
				offset := row
				for _, chunk := range chunked.Data().Chunks() {
					if offset < chunk.Len() {
						if !chunk.IsNull(offset) {
							idx := ssqlSchema.Index(resultSchema.Field(col).Name)
							if idx >= 0 && isUUID[col] {
								values[idx] = pqcols.UUIDString(chunk.(*array.FixedSizeBinary).Value(offset))
							} else if idx >= 0 {
								values[idx] = arrowconv.ValueToGo(chunk, offset)
							}
						}
						break
					}
					offset -= chunk.Len()
				}
			}

			if !yield(ssql.NewRecordFromSchema(ssqlSchema, values)) {
				return
			}
		}
	}, nil
}

// parquetChunkRanges lists the byte range of every column chunk the
// reader is about to read: the given leaf columns, in every selected
// row group. The range is the one arrow's
// RowGroupReader computes (dictionary page first when it precedes the
// data, plus the PARQUET-816 padding for old parquet-mr files), so
// arrow's own ReadAt lands inside it.
func parquetChunkRanges(pf *file.Reader, leaves, rowGroups []int, size int64) []ssql.ByteRange {
	const maxDictHeaderSize = 100 // arrow's PARQUET-816 padding
	md := pf.MetaData()
	var ranges []ssql.ByteRange
	for _, rg := range rowGroups {
		rgMeta := md.RowGroup(rg)
		for _, j := range leaves {
			col, err := rgMeta.ColumnChunk(j)
			if err != nil {
				continue // arrow reports it when it reads the chunk
			}
			start := col.DataPageOffset()
			if col.HasDictionaryPage() && col.DictionaryPageOffset() > 0 && start > col.DictionaryPageOffset() {
				start = col.DictionaryPageOffset()
			}
			length := col.TotalCompressedSize()
			if start < 0 || length <= 0 || start >= size {
				continue
			}
			length += min(maxDictHeaderSize, size-(start+length))
			ranges = append(ranges, ssql.ByteRange{Off: start, Len: length})
		}
	}
	return ranges
}

// WriteOption configures a Parquet writer. Pass to
// [Write] / [WriteTo].
type WriteOption func(*writeOpts)

type writeOpts struct {
	rowGroupSize int64
	compression  string
	columns      *arrowconv.Columns
}

// WithColumnTypes declares the stream's columns and their ssql wire
// types ("int", "float", "bool", "time", "string", …), as a `_schema`
// header carries them. Values still decide a column's type (an int
// column holding a float is written as float), but a declared column
// that is null in every row keeps its type, and an input with no rows
// writes a valid zero-row file with these columns instead of failing.
func WithColumnTypes(fields []string, types map[string]string) WriteOption {
	return func(o *writeOpts) { o.columns = &arrowconv.Columns{Fields: fields, Types: types} }
}

// WithRowGroupSize sets the maximum number of rows per Parquet row
// group. Default is 1_000_000. Smaller values increase reader-side
// parallelism (one row group → one shard in
// [github.com/rosscartlidge/ssql/v4/typed.ReadParquetParallel]) at
// the cost of more metadata overhead. Pass 0 to put all rows in a
// single group (caps reader parallelism at 1).
func WithRowGroupSize(n int) WriteOption {
	return func(o *writeOpts) { o.rowGroupSize = int64(n) }
}

// WithCompression sets the Parquet column compression. Accepted
// values: "snappy" (default), "gzip", "zstd", "none"/"uncompressed".
// Unknown values fall back to Snappy with a warning to stderr.
func WithCompression(name string) WriteOption {
	return func(o *writeOpts) { o.compression = name }
}

func resolveWriteOpts(opts []WriteOption) writeOpts {
	o := writeOpts{rowGroupSize: 1_000_000, compression: "snappy"}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

func compressionCodec(name string) compress.Compression {
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
		fmt.Fprintf(os.Stderr, "ssql.Write: unknown compression %q, falling back to snappy\n", name)
		return compress.Codecs.Snappy
	}
}

// Write writes Records to a Parquet file. Defaults: Snappy
// compression, 1_000_000-row row-groups (so the file can be read
// in parallel by `typed.ReadParquetParallel` or DuckDB). Adjust
// via [WithRowGroupSize] / [WithCompression].
func Write(records iter.Seq[ssql.Record], filename string, opts ...WriteOption) error {
	o := resolveWriteOpts(opts)
	all, schema, err := arrowconv.Collect(records, o.columns)
	if err != nil {
		return err
	}
	return arrowconv.WriteFile(filename, "Parquet", func(w io.Writer) error {
		return writeParquetTable(all, schema, w, o)
	})
}

// WriteTo writes Records to a Parquet format writer.
// Same defaults as [Write].
func WriteTo(records iter.Seq[ssql.Record], w io.Writer, opts ...WriteOption) error {
	o := resolveWriteOpts(opts)
	all, schema, err := arrowconv.Collect(records, o.columns)
	if err != nil {
		return err
	}
	return writeParquetTable(all, schema, w, o)
}

// writeParquetTable writes collected records; an empty slice with a
// declared schema writes a valid zero-row file.
func writeParquetTable(all []ssql.Record, schema *arrow.Schema, w io.Writer, o writeOpts) error {
	mem := memory.DefaultAllocator
	batch, err := arrowconv.RecordsToBatch(all, schema, mem)
	if err != nil {
		return fmt.Errorf("building Arrow batch: %w", err)
	}
	defer batch.Release()

	tbl := array.NewTableFromRecords(schema, []arrow.Record{batch})
	defer tbl.Release()

	props := parquet.NewWriterProperties(
		parquet.WithCompression(compressionCodec(o.compression)),
	)
	arrProps := pqarrow.NewArrowWriterProperties()

	chunk := o.rowGroupSize
	nRows := int64(len(all))
	if chunk <= 0 || chunk > nRows {
		chunk = nRows
	}
	if chunk == 0 {
		chunk = 1 // pqarrow needs a positive chunk size even for zero rows
	}
	return pqarrow.WriteTable(tbl, w, chunk, props, arrProps)
}

// RowCount returns the exact row count from a parquet file's
// footer metadata — O(footer read), no data scan. Used by serve's
// head-throughput display (rows/sec) where line formats need a cached
// newline count but parquet carries the answer natively.
func RowCount(filename string) (int64, error) {
	f, err := ssql.OpenInputAt(filename)
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

// SchemaFields returns the column names and ssql wire types
// ("int"/"float"/"bool"/"string") from a parquet file's footer —
// O(footer), no data pages read. Powers schema mode (and therefore
// field-name completion) for parquet sources: before this, a schema
// query decoded the ENTIRE file to answer a metadata question.
func SchemaFields(filename string) ([]string, map[string]string, error) {
	f, err := ssql.OpenInputAt(filename)
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
	if pqcols.Nested(t) {
		return "json" // the reader yields a JSONString (DFC144 Level 0)
	}
	switch t.ID() {
	case arrow.INT8, arrow.INT16, arrow.INT32, arrow.INT64,
		arrow.UINT8, arrow.UINT16, arrow.UINT32, arrow.UINT64:
		return "int"
	case arrow.FLOAT16, arrow.FLOAT32, arrow.FLOAT64:
		return "float"
	case arrow.BOOL:
		return "bool"
	case arrow.TIMESTAMP, arrow.DATE32, arrow.DATE64:
		return "time" // the reader yields time.Time for these
	case arrow.DECIMAL128:
		if scale := t.(*arrow.Decimal128Type).Scale; scale == 0 {
			return "int128" // DECIMAL(p,0): an integer past int64 (DFC155 C)
		} else {
			return ssql.DecimalType(int(scale)).String() // DECIMAL(p,s): a decimal of the scale (DFC155 E)
		}
	default:
		return "string"
	}
}
