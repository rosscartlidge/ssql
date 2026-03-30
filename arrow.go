//go:build !slim

package ssql

import (
	"fmt"
	"io"
	"iter"
	"os"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/ipc"
	"github.com/apache/arrow/go/v18/arrow/memory"
)

// ReadArrow reads an Arrow IPC file and returns an iterator of Records.
// The file format is auto-detected (IPC File or IPC Stream).
func ReadArrow(filename string) (iter.Seq[Record], error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening Arrow file: %w", err)
	}

	return ReadArrowFromReader(file), nil
}

// ReadArrowFromReader reads Arrow IPC format from a reader.
// Supports both IPC File format (random access) and IPC Stream format.
// For IPC File format, the reader must implement io.Reader, io.ReaderAt, and io.Seeker.
func ReadArrowFromReader(r io.Reader) iter.Seq[Record] {
	return func(yield func(Record) bool) {
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
func readArrowFile(r ReadAtSeeker, yield func(Record) bool) {
	fr, err := ipc.NewFileReader(r)
	if err != nil {
		// Not a valid Arrow file, return empty
		return
	}
	defer fr.Close()

	schema := fr.Schema()
	ssqlSchema := arrowSchemaToSSQLSchema(schema)

	for i := 0; i < fr.NumRecords(); i++ {
		rec, err := fr.Record(i)
		if err != nil {
			continue
		}

		// Convert each row in the record batch to a Record
		for row := 0; row < int(rec.NumRows()); row++ {
			record := arrowRowToRecord(rec, row, ssqlSchema)
			if !yield(record) {
				return
			}
		}
	}
}

// readArrowStream reads an Arrow IPC stream.
func readArrowStream(r io.Reader, yield func(Record) bool) {
	sr, err := ipc.NewReader(r)
	if err != nil {
		return
	}
	defer sr.Release()

	schema := sr.Schema()
	ssqlSchema := arrowSchemaToSSQLSchema(schema)

	for sr.Next() {
		rec := sr.Record()

		for row := 0; row < int(rec.NumRows()); row++ {
			record := arrowRowToRecord(rec, row, ssqlSchema)
			if !yield(record) {
				return
			}
		}
	}
}

// arrowSchemaToSSQLSchema converts an Arrow schema to an ssql Schema.
func arrowSchemaToSSQLSchema(schema *arrow.Schema) *Schema {
	fields := make([]string, len(schema.Fields()))
	for i, f := range schema.Fields() {
		fields[i] = f.Name
	}
	return NewSchema(fields)
}

// arrowRowToRecord extracts a single row from an Arrow record batch.
func arrowRowToRecord(rec arrow.Record, row int, schema *Schema) Record {
	values := make([]any, len(schema.fields))

	for idx, field := range schema.fields {
		colIdx := rec.Schema().FieldIndices(field)
		if len(colIdx) == 0 {
			continue
		}

		col := rec.Column(colIdx[0])
		if col.IsNull(row) {
			continue
		}

		values[idx] = arrowValueToGo(col, row)
	}

	return Record{schema: schema, values: values}
}

// arrowValueToGo converts an Arrow array value at a given index to a Go value.
func arrowValueToGo(arr arrow.Array, idx int) any {
	if arr.IsNull(idx) {
		return nil
	}

	switch a := arr.(type) {
	case *array.Int8:
		return int64(a.Value(idx))
	case *array.Int16:
		return int64(a.Value(idx))
	case *array.Int32:
		return int64(a.Value(idx))
	case *array.Int64:
		return a.Value(idx)
	case *array.Uint8:
		return int64(a.Value(idx))
	case *array.Uint16:
		return int64(a.Value(idx))
	case *array.Uint32:
		return int64(a.Value(idx))
	case *array.Uint64:
		return int64(a.Value(idx))
	case *array.Float32:
		return float64(a.Value(idx))
	case *array.Float64:
		return a.Value(idx)
	case *array.String:
		return a.Value(idx)
	case *array.LargeString:
		return a.Value(idx)
	case *array.Boolean:
		return a.Value(idx)
	case *array.Timestamp:
		return a.Value(idx).ToTime(a.DataType().(*arrow.TimestampType).Unit)
	case *array.Date32:
		return a.Value(idx).ToTime()
	case *array.Date64:
		return a.Value(idx).ToTime()
	default:
		// For unsupported types, return string representation
		return fmt.Sprintf("%v", arr.ValueStr(idx))
	}
}

// WriteArrow writes Records to an Arrow IPC file.
func WriteArrow(records iter.Seq[Record], filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating Arrow file: %w", err)
	}
	defer file.Close()

	return WriteArrowToWriter(records, file)
}

// WriteArrowToWriter writes Records to an Arrow IPC format writer.
func WriteArrowToWriter(records iter.Seq[Record], w io.Writer) error {
	mem := memory.DefaultAllocator

	// Collect records to determine schema and build batches
	var allRecords []Record
	var schema *arrow.Schema

	for record := range records {
		if schema == nil {
			schema = recordToArrowSchema(record)
		}
		allRecords = append(allRecords, record)
	}

	if len(allRecords) == 0 {
		return nil // Nothing to write
	}

	// Create file writer with ZSTD compression
	fw, err := ipc.NewFileWriter(w,
		ipc.WithSchema(schema),
		ipc.WithAllocator(mem),
		ipc.WithCompressConcurrency(1),
		ipc.WithZstd(),
	)
	if err != nil {
		return fmt.Errorf("creating Arrow writer: %w", err)
	}
	defer fw.Close()

	// Build record batch from all records
	batch, err := recordsToArrowBatch(allRecords, schema, mem)
	if err != nil {
		return fmt.Errorf("building Arrow batch: %w", err)
	}
	defer batch.Release()

	// Write the batch
	if err := fw.Write(batch); err != nil {
		return fmt.Errorf("writing Arrow batch: %w", err)
	}

	return nil
}

// recordToArrowSchema infers an Arrow schema from a Record.
func recordToArrowSchema(record Record) *arrow.Schema {
	var fields []arrow.Field

	for k, v := range record.All() {
		// Skip internal fields
		if len(k) > 0 && k[0] == '_' {
			continue
		}

		arrowType := goTypeToArrowType(v)
		fields = append(fields, arrow.Field{Name: k, Type: arrowType, Nullable: true})
	}

	return arrow.NewSchema(fields, nil)
}

// goTypeToArrowType converts a Go type to an Arrow type.
func goTypeToArrowType(v any) arrow.DataType {
	switch v.(type) {
	case int64:
		return arrow.PrimitiveTypes.Int64
	case float64:
		return arrow.PrimitiveTypes.Float64
	case string:
		return arrow.BinaryTypes.String
	case bool:
		return arrow.FixedWidthTypes.Boolean
	default:
		// Default to string for complex types
		return arrow.BinaryTypes.String
	}
}

// recordsToArrowBatch converts a slice of Records to an Arrow RecordBatch.
func recordsToArrowBatch(records []Record, schema *arrow.Schema, mem memory.Allocator) (arrow.Record, error) {
	nRows := len(records)
	nCols := len(schema.Fields())

	builders := make([]array.Builder, nCols)
	for i, field := range schema.Fields() {
		builders[i] = array.NewBuilder(mem, field.Type)
	}
	defer func() {
		for _, b := range builders {
			b.Release()
		}
	}()

	// Populate builders with data from records
	for _, record := range records {
		for i, field := range schema.Fields() {
			val, exists := Get[any](record, field.Name)
			if !exists {
				builders[i].AppendNull()
				continue
			}
			appendValueToBuilder(builders[i], val)
		}
	}

	// Build arrays
	arrays := make([]arrow.Array, nCols)
	for i, b := range builders {
		arrays[i] = b.NewArray()
	}
	defer func() {
		for _, a := range arrays {
			a.Release()
		}
	}()

	// Create record batch
	batch := array.NewRecord(schema, arrays, int64(nRows))
	batch.Retain() // Keep it alive after arrays are released
	return batch, nil
}

// appendValueToBuilder appends a Go value to an Arrow builder.
func appendValueToBuilder(builder array.Builder, val any) {
	switch b := builder.(type) {
	case *array.Int64Builder:
		switch v := val.(type) {
		case int64:
			b.Append(v)
		case float64:
			b.Append(int64(v))
		default:
			b.AppendNull()
		}
	case *array.Float64Builder:
		switch v := val.(type) {
		case float64:
			b.Append(v)
		case int64:
			b.Append(float64(v))
		default:
			b.AppendNull()
		}
	case *array.StringBuilder:
		switch v := val.(type) {
		case string:
			b.Append(v)
		default:
			b.Append(fmt.Sprintf("%v", v))
		}
	case *array.BooleanBuilder:
		switch v := val.(type) {
		case bool:
			b.Append(v)
		default:
			b.AppendNull()
		}
	default:
		builder.AppendNull()
	}
}

// ============================================================================
// Direct Signal Extraction from Arrow (GPU-optimized)
// ============================================================================

// ExtractSignalFromArrow reads an Arrow file and extracts a numeric field as a Signal.
// This bypasses Record conversion for efficient GPU processing.
// Returns the signal and the number of samples extracted.
func ExtractSignalFromArrow(filename string, field string) (Signal, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening Arrow file: %w", err)
	}
	defer file.Close()

	return ExtractSignalFromArrowReader(file, field)
}

// ExtractSignalFromArrowReader extracts a numeric field from Arrow data as a Signal.
// For IPC File format, the reader must implement ReadAtSeeker.
func ExtractSignalFromArrowReader(r io.Reader, field string) (Signal, error) {
	// Try file reader first for random access
	if ra, ok := r.(ReadAtSeeker); ok {
		return extractSignalFromArrowFile(ra, field)
	}
	return extractSignalFromArrowStream(r, field)
}

// extractSignalFromArrowFile extracts a signal from an Arrow IPC file.
func extractSignalFromArrowFile(r ReadAtSeeker, field string) (Signal, error) {
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

	signal := make(Signal, 0, totalRows)

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
func extractSignalFromArrowStream(r io.Reader, field string) (Signal, error) {
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

	var signal Signal

	for sr.Next() {
		rec := sr.Record()
		col := rec.Column(fieldIdx)
		signal = appendArrowColumnToSignal(signal, col)
	}

	return signal, nil
}

// appendArrowColumnToSignal appends values from an Arrow array to a Signal.
// Handles various numeric types by converting to float64.
func appendArrowColumnToSignal(signal Signal, col arrow.Array) Signal {
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
