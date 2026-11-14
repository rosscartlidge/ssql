// Package arrowconv converts between ssql records and Arrow arrays: the
// shared core of package arrow (IPC files) and package parquet's record
// reader and writer (DFC168). It is internal because its API is the
// formats' implementation, not a public one.
package arrowconv

import (
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"os"
	"time"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/decimal128"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/internal/pqcols"
)

// SchemaToSSQL converts an Arrow schema to an ssql Schema.
func SchemaToSSQL(schema *arrow.Schema) *ssql.Schema {
	fields := make([]string, len(schema.Fields()))
	for i, f := range schema.Fields() {
		fields[i] = f.Name
	}
	return ssql.NewSchema(fields)
}

// RowToRecord extracts a single row from an Arrow record batch. fields
// is schema.Fields(), taken once by the caller rather than per row.
func RowToRecord(rec arrow.Record, row int, schema *ssql.Schema, fields []string) ssql.Record {
	values := make([]any, len(fields))

	for idx, field := range fields {
		colIdx := rec.Schema().FieldIndices(field)
		if len(colIdx) == 0 {
			continue
		}

		col := rec.Column(colIdx[0])
		if col.IsNull(row) {
			continue
		}

		values[idx] = ValueToGo(col, row)
	}

	return ssql.NewRecordFromSchema(schema, values)
}

// ValueToGo converts an Arrow array value at a given index to a Go value.
func ValueToGo(arr arrow.Array, idx int) any {
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
		if v := a.Value(idx); v > math.MaxInt64 {
			return ssql.Int128FromWords(0, v) // past int64: exact, not wrapped (DFC155 C)
		} else {
			return int64(v)
		}
	case *array.Decimal128:
		// A DECIMAL with no fraction is an int128 (DFC155 C); one with a
		// scale is a decimal of that scale (E). A precision past 38 (none
		// a Decimal128 holds) would stay its text.
		n := a.Value(idx)
		scale := int(a.DataType().(*arrow.Decimal128Type).Scale)
		if scale == 0 {
			return ssql.Int128FromWords(n.HighBits(), n.LowBits())
		}
		if d, err := ssql.NewDecimal(ssql.Int128FromWords(n.HighBits(), n.LowBits()), scale); err == nil {
			return d
		}
		return a.ValueStr(idx)
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
	case *array.Struct, *array.List, *array.LargeList, *array.FixedSizeList, *array.Map:
		// A nested value is its JSON text (DFC144 Level 0), as from json
		// reads one; a map is an object, as DuckDB writes it.
		return ssql.JSONString(pqcols.JSON(a, idx))
	default:
		// For unsupported types, return string representation
		return fmt.Sprintf("%v", arr.ValueStr(idx))
	}
}

// WriteFile creates filename and runs write on it, removing the
// file if the write fails so no truncated file is left for a reader.
func WriteFile(filename, format string, write func(io.Writer) error) error {
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

// Columns is a stream's `_schema` header: its column order and
// wire types. It settles what the values cannot: a column that is null
// in every row, and the columns of an input with no rows at all.
type Columns struct {
	Fields []string
	Types  map[string]string
}

// Collect buffers the records and types every column from
// every value it holds (see arrowColumnKind), so a column that is null
// in row 1, or an int in row 1 and a float in row 2, is typed from all
// of its values and not from the first record. Columns appear in the
// declared order, then in the order they are first seen. An input with
// no records and no declared columns is an error: there is no schema
// to write, and a zero-byte file is not Arrow or Parquet.
func Collect(records iter.Seq[ssql.Record], declared *Columns) ([]ssql.Record, *arrow.Schema, error) {
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
		for _, f := range declared.Fields {
			add(f, arrowKindForWireType(declared.Types[f]))
		}
	}
	var all []ssql.Record
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
	arrowKindInt128 // an int128 column: Arrow Decimal128(38,0) (DFC155 C)
	arrowKindFloat
	arrowKindBool
	arrowKindTime
	arrowKindString

	// arrowKindDecimal is the flag bit of a decimal column's kind, the
	// scale in the low bits (arrowDecimalKind, scale): Decimal128(38,S)
	// (DFC155 E). Never a case label; test isDecimal first.
	arrowKindDecimal arrowColumnKind = 1 << 8
)

func arrowDecimalKind(scale int) arrowColumnKind {
	return arrowKindDecimal | arrowColumnKind(scale&0xff)
}
func (k arrowColumnKind) isDecimal() bool { return k&arrowKindDecimal != 0 }
func (k arrowColumnKind) scale() int      { return int(k &^ arrowKindDecimal) }

// arrowTimestampType is how a time is written: microseconds, adjusted
// to UTC — the same type typed.WriteParquet writes, so the record and
// typed lanes produce the same file.
var arrowTimestampType = &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}

// arrowDecimal128Type is the Arrow/Parquet type of an int128 column
// (DFC155 C): DECIMAL(38,0), which DuckDB and Spark read as a number.
// It holds 38 digits where an int128 holds 39; a value past ±(10^38 − 1)
// refuses on write (Decimal256 was declined).
var arrowDecimal128Type = &arrow.Decimal128Type{Precision: 38, Scale: 0}

var decimal38Max, decimal38Min = ssql.MustParseInt128("99999999999999999999999999999999999999"), ssql.MustParseInt128("-99999999999999999999999999999999999999")

// decimal128Of is an Int128 as an Arrow Decimal128(38,0) value, or a
// panic naming the value when it has 39 digits.
func decimal128Of(v ssql.Int128) decimal128.Num {
	if v.Cmp(decimal38Max) > 0 || v.Cmp(decimal38Min) < 0 {
		panic(fmt.Errorf("%s does not fit DECIMAL(38,0), the Parquet/Arrow form of an int128 (38 digits; cast the column to string, or to float, before writing)", v))
	}
	hi, lo := v.Words()
	return decimal128.New(hi, lo)
}

// decimal128At is a value as the units of an Arrow Decimal128(38,scale):
// a Decimal brought to the scale (rounding half away from zero), an
// integer of either width exactly; a value past 38 digits at the scale
// panics naming it (DFC155 E).
func decimal128At(v any, scale int) (decimal128.Num, bool) {
	var d ssql.Decimal
	var err error
	// ssql:domain exact
	switch x := v.(type) {
	case ssql.Decimal:
		d, err = x.Rescale(scale)
	case ssql.Int128:
		d, err = ssql.DecimalFromInt128(x, scale)
	case int64:
		d, err = ssql.DecimalFromInt128(ssql.Int128From(x), scale)
	case int:
		d, err = ssql.DecimalFromInt128(ssql.Int128From(int64(x)), scale)
	case int32:
		d, err = ssql.DecimalFromInt128(ssql.Int128From(int64(x)), scale)
	default:
		return decimal128.Num{}, false
	}
	if err != nil {
		panic(fmt.Errorf("%v does not fit DECIMAL(38,%d), the Parquet/Arrow form of the column (cast the column to string, or to float, before writing)", v, scale))
	}
	hi, lo := d.Units().Words()
	return decimal128.New(hi, lo), true
}

func arrowKindOf(v any) arrowColumnKind {
	// ssql:domain scalar
	switch x := v.(type) {
	case nil:
		return arrowKindNull
	case int64, int, int32:
		return arrowKindInt
	case ssql.Int128:
		return arrowKindInt128
	case ssql.Decimal:
		return arrowDecimalKind(x.Scale())
	case float64, float32:
		return arrowKindFloat
	case bool:
		return arrowKindBool
	case time.Time:
		return arrowKindTime
	case string, ssql.Inet:
		return arrowKindString // an inet is its text in a file (DFC162 §2)
	default:
		return arrowKindString
	}
}

func arrowKindForWireType(t string) arrowColumnKind {
	if base, scale := ssql.ParseWireType(t); base == "decimal" && scale >= 0 {
		return arrowDecimalKind(scale)
	}
	// ssql:domain scalar
	switch t {
	case "":
		return arrowKindNull
	case "int":
		return arrowKindInt
	case "int128":
		return arrowKindInt128
	case "float":
		return arrowKindFloat
	case "bool":
		return arrowKindBool
	case "time":
		return arrowKindTime
	case "string", "inet":
		return arrowKindString
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
	case a.isDecimal() || b.isDecimal():
		// A decimal beside a decimal: the larger scale; beside an integer
		// of either width: the decimal; beside a float: a float (DFC155 E).
		switch {
		case a.isDecimal() && b.isDecimal():
			return arrowDecimalKind(max(a.scale(), b.scale()))
		case a.isDecimal() && (b == arrowKindInt || b == arrowKindInt128):
			return a
		case b.isDecimal() && (a == arrowKindInt || a == arrowKindInt128):
			return b
		case a == arrowKindFloat || b == arrowKindFloat:
			return arrowKindFloat
		}
		return arrowKindString
	case (a == arrowKindInt && b == arrowKindInt128) || (a == arrowKindInt128 && b == arrowKindInt):
		return arrowKindInt128 // int beside int128 is int128
	case (a == arrowKindInt || a == arrowKindInt128) && b == arrowKindFloat, a == arrowKindFloat && (b == arrowKindInt || b == arrowKindInt128):
		return arrowKindFloat
	default:
		return arrowKindString
	}
}

func (k arrowColumnKind) arrowType() arrow.DataType {
	if k.isDecimal() {
		return &arrow.Decimal128Type{Precision: 38, Scale: int32(k.scale())}
	}
	switch k {
	case arrowKindInt:
		return arrow.PrimitiveTypes.Int64
	case arrowKindInt128:
		return arrowDecimal128Type
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

// RecordsToBatch converts a slice of Records to an Arrow RecordBatch.
func RecordsToBatch(records []ssql.Record, schema *arrow.Schema, mem memory.Allocator) (arrow.Record, error) {
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
			val, exists := ssql.Get[any](record, field.Name)
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
// column type Collect chose for it. Widening guarantees the
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
	case *array.Decimal128Builder:
		if scale := int(b.Type().(*arrow.Decimal128Type).Scale); scale > 0 {
			// A decimal column: every value at the column's scale (DFC155 E).
			if n, ok := decimal128At(val, scale); ok {
				b.Append(n)
			} else {
				b.AppendNull()
			}
			break
		}
		// ssql:domain exact
		switch v := val.(type) {
		case ssql.Int128:
			b.Append(decimal128Of(v))
		case ssql.Decimal:
			b.Append(decimal128Of(v.MustRescale(0).Units())) // a decimal into an int128 column: whole, half away from zero
		case int64:
			b.Append(decimal128.FromI64(v))
		case int:
			b.Append(decimal128.FromI64(int64(v)))
		case int32:
			b.Append(decimal128.FromI64(int64(v)))
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
		b.Append(ssql.FormatValue(val))
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
