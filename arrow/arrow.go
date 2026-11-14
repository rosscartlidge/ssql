// Package arrow reads and writes Arrow IPC files (the File and Stream
// formats) as ssql records, and extracts a numeric column as a Signal.
//
// It is its own package so that a program links Apache Arrow only when
// it imports it (DFC168).
package arrow

import (
	"fmt"
	"io"
	"iter"
	"os"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/ipc"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/internal/arrowconv"
)

// Read reads an Arrow IPC file and returns an iterator of Records.
// The file format is auto-detected (IPC File or IPC Stream).
func Read(filename string) (iter.Seq[ssql.Record], error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening Arrow file: %w", err)
	}

	return ReadFrom(file), nil
}

// ReadFrom reads Arrow IPC format from a reader.
// Supports both IPC File format (random access) and IPC Stream format.
// For IPC File format, the reader must implement io.Reader, io.ReaderAt, and io.Seeker.
func ReadFrom(r io.Reader) iter.Seq[ssql.Record] {
	return func(yield func(ssql.Record) bool) {
		// Try to use FileReader if reader supports random access
		if ra, ok := r.(ReadAtSeeker); ok {
			readArrowFile(ra, yield)
			return
		}

		// Fall back to stream reader
		readArrowStream(r, yield)
	}
}

// ReadAtSeeker combines io.ReaderAt and io.Seeker for Arrow file reading.
type ReadAtSeeker interface {
	io.Reader
	io.ReaderAt
	io.Seeker
}

// readArrowFile reads an Arrow IPC file using random access.
func readArrowFile(r ReadAtSeeker, yield func(ssql.Record) bool) {
	fr, err := ipc.NewFileReader(r)
	if err != nil {
		// Not a valid Arrow file, return empty
		return
	}
	defer fr.Close()

	schema := fr.Schema()
	ssqlSchema := arrowconv.SchemaToSSQL(schema)
	fields := ssqlSchema.Fields()

	for i := 0; i < fr.NumRecords(); i++ {
		rec, err := fr.Record(i)
		if err != nil {
			continue
		}

		// Convert each row in the record batch to a Record
		for row := 0; row < int(rec.NumRows()); row++ {
			record := arrowconv.RowToRecord(rec, row, ssqlSchema, fields)
			if !yield(record) {
				return
			}
		}
	}
}

// readArrowStream reads an Arrow IPC stream.
func readArrowStream(r io.Reader, yield func(ssql.Record) bool) {
	sr, err := ipc.NewReader(r)
	if err != nil {
		return
	}
	defer sr.Release()

	schema := sr.Schema()
	ssqlSchema := arrowconv.SchemaToSSQL(schema)
	fields := ssqlSchema.Fields()

	for sr.Next() {
		rec := sr.Record()

		for row := 0; row < int(rec.NumRows()); row++ {
			record := arrowconv.RowToRecord(rec, row, ssqlSchema, fields)
			if !yield(record) {
				return
			}
		}
	}
}

// WriteOption configures [Write] / [WriteTo].
type WriteOption func(*writeOpts)

type writeOpts struct {
	columns *arrowconv.Columns
}

// WithColumnTypes declares the stream's columns and their ssql
// wire types ("int", "float", "bool", "time", "string", …), as a
// `_schema` header carries them. See [WithColumnTypes].
func WithColumnTypes(fields []string, types map[string]string) WriteOption {
	return func(o *writeOpts) { o.columns = &arrowconv.Columns{Fields: fields, Types: types} }
}

// Write writes Records to an Arrow IPC file. The file is created
// only once the records have been collected and typed, so an input with
// nothing to write leaves no file behind.
func Write(records iter.Seq[ssql.Record], filename string, opts ...WriteOption) error {
	o := writeOpts{}
	for _, fn := range opts {
		fn(&o)
	}
	all, schema, err := arrowconv.Collect(records, o.columns)
	if err != nil {
		return err
	}
	return arrowconv.WriteFile(filename, "Arrow", func(w io.Writer) error {
		return writeArrowIPC(all, schema, w)
	})
}

// WriteTo writes Records to an Arrow IPC format writer.
func WriteTo(records iter.Seq[ssql.Record], w io.Writer, opts ...WriteOption) error {
	o := writeOpts{}
	for _, fn := range opts {
		fn(&o)
	}
	all, schema, err := arrowconv.Collect(records, o.columns)
	if err != nil {
		return err
	}
	return writeArrowIPC(all, schema, w)
}

func writeArrowIPC(all []ssql.Record, schema *arrow.Schema, w io.Writer) error {
	mem := memory.DefaultAllocator
	fw, err := ipc.NewFileWriter(w,
		ipc.WithSchema(schema),
		ipc.WithAllocator(mem),
		ipc.WithCompressConcurrency(1),
		ipc.WithZstd(),
	)
	if err != nil {
		return fmt.Errorf("creating Arrow writer: %w", err)
	}
	batch, err := arrowconv.RecordsToBatch(all, schema, mem)
	if err != nil {
		fw.Close()
		return fmt.Errorf("building Arrow batch: %w", err)
	}
	defer batch.Release()
	if err := fw.Write(batch); err != nil {
		fw.Close()
		return fmt.Errorf("writing Arrow batch: %w", err)
	}
	// Close writes the footer; its error is the file's.
	return fw.Close()
}

// Direct Signal Extraction from Arrow (GPU-optimized)
// ============================================================================

// ExtractSignal reads an Arrow file and extracts a numeric field as a Signal.
// This bypasses Record conversion for efficient GPU processing.
// Returns the signal and the number of samples extracted.
func ExtractSignal(filename string, field string) (ssql.Signal, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening Arrow file: %w", err)
	}
	defer file.Close()

	return ExtractSignalFrom(file, field)
}

// ExtractSignalFrom extracts a numeric field from Arrow data as a Signal.
// For IPC File format, the reader must implement ReadAtSeeker.
func ExtractSignalFrom(r io.Reader, field string) (ssql.Signal, error) {
	// Try file reader first for random access
	if ra, ok := r.(ReadAtSeeker); ok {
		return extractSignalFromArrowFile(ra, field)
	}
	return extractSignalFromArrowStream(r, field)
}

// extractSignalFromArrowFile extracts a signal from an Arrow IPC file.
func extractSignalFromArrowFile(r ReadAtSeeker, field string) (ssql.Signal, error) {
	fr, err := ipc.NewFileReader(r)
	if err != nil {
		return nil, fmt.Errorf("opening Arrow reader: %w", err)
	}
	defer fr.Close()

	schema := fr.Schema()
	fieldIndices := schema.FieldIndices(field)
	if len(fieldIndices) == 0 {
		return nil, fmt.Errorf("field %q not found in Arrow schema", field)
	}
	fieldIdx := fieldIndices[0]

	// Pre-allocate by counting total rows
	totalRows := 0
	for i := 0; i < fr.NumRecords(); i++ {
		rec, err := fr.Record(i)
		if err != nil {
			continue
		}
		totalRows += int(rec.NumRows())
	}

	signal := make(ssql.Signal, 0, totalRows)

	// Extract values directly from Arrow arrays
	for i := 0; i < fr.NumRecords(); i++ {
		rec, err := fr.Record(i)
		if err != nil {
			continue
		}

		col := rec.Column(fieldIdx)
		signal = appendArrowColumnToSignal(signal, col)
	}

	return signal, nil
}

// extractSignalFromArrowStream extracts a signal from an Arrow IPC stream.
func extractSignalFromArrowStream(r io.Reader, field string) (ssql.Signal, error) {
	sr, err := ipc.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("opening Arrow stream: %w", err)
	}
	defer sr.Release()

	schema := sr.Schema()
	fieldIndices := schema.FieldIndices(field)
	if len(fieldIndices) == 0 {
		return nil, fmt.Errorf("field %q not found in Arrow schema", field)
	}
	fieldIdx := fieldIndices[0]

	var signal ssql.Signal

	for sr.Next() {
		rec := sr.Record()
		col := rec.Column(fieldIdx)
		signal = appendArrowColumnToSignal(signal, col)
	}

	return signal, nil
}

// appendArrowColumnToSignal appends values from an Arrow array to a Signal.
// Handles various numeric types by converting to float64.
func appendArrowColumnToSignal(signal ssql.Signal, col arrow.Array) ssql.Signal {
	n := col.Len()

	switch arr := col.(type) {
	case *array.Float64:
		// Direct access to float64 values - most efficient path
		for i := range n {
			if arr.IsNull(i) {
				signal = append(signal, 0)
			} else {
				signal = append(signal, arr.Value(i))
			}
		}
	case *array.Float32:
		for i := range n {
			if arr.IsNull(i) {
				signal = append(signal, 0)
			} else {
				signal = append(signal, float64(arr.Value(i)))
			}
		}
	case *array.Int64:
		for i := range n {
			if arr.IsNull(i) {
				signal = append(signal, 0)
			} else {
				signal = append(signal, float64(arr.Value(i)))
			}
		}
	case *array.Int32:
		for i := range n {
			if arr.IsNull(i) {
				signal = append(signal, 0)
			} else {
				signal = append(signal, float64(arr.Value(i)))
			}
		}
	case *array.Int16:
		for i := range n {
			if arr.IsNull(i) {
				signal = append(signal, 0)
			} else {
				signal = append(signal, float64(arr.Value(i)))
			}
		}
	case *array.Int8:
		for i := range n {
			if arr.IsNull(i) {
				signal = append(signal, 0)
			} else {
				signal = append(signal, float64(arr.Value(i)))
			}
		}
	case *array.Uint64:
		for i := range n {
			if arr.IsNull(i) {
				signal = append(signal, 0)
			} else {
				signal = append(signal, float64(arr.Value(i)))
			}
		}
	case *array.Uint32:
		for i := range n {
			if arr.IsNull(i) {
				signal = append(signal, 0)
			} else {
				signal = append(signal, float64(arr.Value(i)))
			}
		}
	case *array.Uint16:
		for i := range n {
			if arr.IsNull(i) {
				signal = append(signal, 0)
			} else {
				signal = append(signal, float64(arr.Value(i)))
			}
		}
	case *array.Uint8:
		for i := range n {
			if arr.IsNull(i) {
				signal = append(signal, 0)
			} else {
				signal = append(signal, float64(arr.Value(i)))
			}
		}
	default:
		// For unsupported types, append zeros
		for range n {
			signal = append(signal, 0)
		}
	}

	return signal
}
