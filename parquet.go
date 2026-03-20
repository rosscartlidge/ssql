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
	return ReadParquetColumns(filename, nil)
}

// ReadParquetColumns reads a Parquet file, optionally selecting only the named columns.
// If columns is nil or empty, all columns are read.
// This is the primary optimization lever for wide Parquet files — reading 3 of 50 columns
// means ~94% less I/O.
func ReadParquetColumns(filename string, columns []string) (iter.Seq[Record], error) {
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

// WriteParquet writes Records to a Parquet file with Snappy compression.
func WriteParquet(records iter.Seq[Record], filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating Parquet file: %w", err)
	}
	defer f.Close()

	return WriteParquetToWriter(records, f)
}

// WriteParquetToWriter writes Records to a Parquet format writer.
// Uses Snappy compression (the Parquet default).
func WriteParquetToWriter(records iter.Seq[Record], w io.Writer) error {
	mem := memory.DefaultAllocator

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

	// Write as Parquet with Snappy compression
	props := parquet.NewWriterProperties(
		parquet.WithCompression(compress.Codecs.Snappy),
	)
	arrProps := pqarrow.NewArrowWriterProperties()

	return pqarrow.WriteTable(tbl, w, int64(len(allRecords)), props, arrProps)
}
