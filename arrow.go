//go:build !slim

package ssql

import (
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"time"

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

// ArrowWriteOption configures [WriteArrow] / [WriteArrowToWriter].
type ArrowWriteOption func(*arrowWriteOpts)

type arrowWriteOpts struct {
	columns *declaredColumns
}

// WithArrowColumnTypes declares the stream's columns and their ssql
// wire types ("int", "float", "bool", "time", "string", …), as a
// `_schema` header carries them. See [WithColumnTypes].
func WithArrowColumnTypes(fields []string, types map[string]string) ArrowWriteOption {
	return func(o *arrowWriteOpts) { o.columns = &declaredColumns{fields: fields, types: types} }
}

// WriteArrow writes Records to an Arrow IPC file. The file is created
// only once the records have been collected and typed, so an input with
// nothing to write leaves no file behind.
func WriteArrow(records iter.Seq[Record], filename string, opts ...ArrowWriteOption) error {
	o := arrowWriteOpts{}
	for _, fn := range opts {
		fn(&o)
	}
	all, schema, err := collectForArrow(records, o.columns)
	if err != nil {
		return err
	}
	return writeFileWith(filename, "Arrow", func(w io.Writer) error {
		return writeArrowIPC(all, schema, w)
	})
}

// WriteArrowToWriter writes Records to an Arrow IPC format writer.
func WriteArrowToWriter(records iter.Seq[Record], w io.Writer, opts ...ArrowWriteOption) error {
	o := arrowWriteOpts{}
	for _, fn := range opts {
		fn(&o)
	}
	all, schema, err := collectForArrow(records, o.columns)
	if err != nil {
		return err
	}
	return writeArrowIPC(all, schema, w)
}

func writeArrowIPC(all []Record, schema *arrow.Schema, w io.Writer) error {
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
	batch, err := recordsToArrowBatch(all, schema, mem)
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

// writeFileWith creates filename and runs write on it, removing the
// file if the write fails so no truncated file is left for a reader.
func writeFileWith(filename, format string, write func(io.Writer) error) error {
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating %s file: %w", format, err)
	}
	if err := write(f); err != nil {
		f.Close()
		os.Remove(filename)
		return err
	}
	// pqarrow closes the file itself when it finishes; that is not an error.
	if err := f.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		os.Remove(filename)
		return err
	}
	return nil
}

// declaredColumns is a stream's `_schema` header: its column order and
// wire types. It settles what the values cannot: a column that is null
// in every row, and the columns of an input with no rows at all.
type declaredColumns struct {
	fields []string
	types  map[string]string
}

// collectForArrow buffers the records and types every column from
// every value it holds (see arrowColumnKind), so a column that is null
// in row 1, or an int in row 1 and a float in row 2, is typed from all
// of its values and not from the first record. Columns appear in the
// declared order, then in the order they are first seen. An input with
// no records and no declared columns is an error: there is no schema
// to write, and a zero-byte file is not Arrow or Parquet.
func collectForArrow(records iter.Seq[Record], declared *declaredColumns) ([]Record, *arrow.Schema, error) {
	var names []string
	kinds := map[string]arrowColumnKind{}
	add := func(name string, k arrowColumnKind) {
		if len(name) > 0 && name[0] == '_' {
			return // internal fields
		}
		old, seen := kinds[name]
		if !seen {
			names = append(names, name)
		}
		kinds[name] = widenArrowKind(old, k)
	}
	if declared != nil {
		for _, f := range declared.fields {
			add(f, arrowKindForWireType(declared.types[f]))
		}
	}
	var all []Record
	for record := range records {
		for k, v := range record.All() {
			add(k, arrowKindOf(v))
		}
		all = append(all, record)
	}
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("nothing to write: the input has no records and no schema")
	}
	fields := make([]arrow.Field, len(names))
	for i, n := range names {
		fields[i] = arrow.Field{Name: n, Type: kinds[n].arrowType(), Nullable: true}
	}
	return all, arrow.NewSchema(fields, nil), nil
}

// arrowColumnKind is the arrow type of a column, decided from every
// value in it. Kinds widen: null joins anything, int and float make
// float, and any other mix makes string.
type arrowColumnKind int

const (
	arrowKindNull arrowColumnKind = iota
	arrowKindInt
	arrowKindFloat
	arrowKindBool
	arrowKindTime
	arrowKindString
)

// arrowTimestampType is how a time is written: microseconds, adjusted
// to UTC — the same type typed.WriteParquet writes, so the record and
// typed lanes produce the same file.
var arrowTimestampType = &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}

func arrowKindOf(v any) arrowColumnKind {
	switch v.(type) {
	case nil:
		return arrowKindNull
	case int64, int, int32:
		return arrowKindInt
	case float64, float32:
		return arrowKindFloat
	case bool:
		return arrowKindBool
	case time.Time:
		return arrowKindTime
	default:
		return arrowKindString
	}
}

func arrowKindForWireType(t string) arrowColumnKind {
	switch t {
	case "":
		return arrowKindNull
	case "int":
		return arrowKindInt
	case "float":
		return arrowKindFloat
	case "bool":
		return arrowKindBool
	case "time":
		return arrowKindTime
	default:
		return arrowKindString
	}
}

func widenArrowKind(a, b arrowColumnKind) arrowColumnKind {
	switch {
	case a == b || b == arrowKindNull:
		return a
	case a == arrowKindNull:
		return b
	case (a == arrowKindInt && b == arrowKindFloat) || (a == arrowKindFloat && b == arrowKindInt):
		return arrowKindFloat
	default:
		return arrowKindString
	}
}

func (k arrowColumnKind) arrowType() arrow.DataType {
	switch k {
	case arrowKindInt:
		return arrow.PrimitiveTypes.Int64
	case arrowKindFloat:
		return arrow.PrimitiveTypes.Float64
	case arrowKindBool:
		return arrow.FixedWidthTypes.Boolean
	case arrowKindTime:
		return arrowTimestampType
	default:
		return arrow.BinaryTypes.String // also a column null in every row
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
			if !exists || val == nil {
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

// appendValueToBuilder appends a non-nil Go value to the builder of the
// column type collectForArrow chose for it. Widening guarantees the
// value fits: an int in a float column is converted, and anything in a
// string column is written as its wire text (formatValue: RFC 3339 for
// a time, never Go's Time.String()).
func appendValueToBuilder(builder array.Builder, val any) {
	switch b := builder.(type) {
	case *array.Int64Builder:
		switch v := val.(type) {
		case int64:
			b.Append(v)
		case int:
			b.Append(int64(v))
		case int32:
			b.Append(int64(v))
		default:
			b.AppendNull()
		}
	case *array.Float64Builder:
		switch v := val.(type) {
		case float64:
			b.Append(v)
		case float32:
			b.Append(float64(v))
		case int64:
			b.Append(float64(v))
		case int:
			b.Append(float64(v))
		case int32:
			b.Append(float64(v))
		default:
			b.AppendNull()
		}
	case *array.TimestampBuilder:
		if t, ok := val.(time.Time); ok {
			b.Append(arrow.Timestamp(t.UnixMicro()))
		} else {
			b.AppendNull()
		}
	case *array.StringBuilder:
		b.Append(formatValue(val))
	case *array.BooleanBuilder:
		if v, ok := val.(bool); ok {
			b.Append(v)
		} else {
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
