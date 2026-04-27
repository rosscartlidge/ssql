//go:build !slim

package typed

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/apache/arrow/go/v18/parquet"
	"github.com/apache/arrow/go/v18/parquet/compress"
	"github.com/apache/arrow/go/v18/parquet/file"
	"github.com/apache/arrow/go/v18/parquet/pqarrow"
)

// ParquetOption configures a Parquet reader. Pass to [ReadParquet],
// [ReadParquetParallel], and the safe variants.
type ParquetOption func(*parquetOpts)

type parquetOpts struct {
	strict  bool
	columns []string
}

// ParquetStrict mirrors [Strict] / [DelimStrict]. Header columns
// without a matching struct field, OR required (non-pointer)
// struct fields without a matching column, become hard errors.
func ParquetStrict() ParquetOption { return func(o *parquetOpts) { o.strict = true } }

// ParquetColumns selects a subset of columns to read from the
// Parquet file. Reading 3 of 50 columns means ~94% less I/O — the
// primary lever for wide tables. Names are matched
// case-insensitively against the Parquet schema. Unknown column
// names are silently dropped (unless [ParquetStrict] is set, in
// which case they're an error).
func ParquetColumns(names ...string) ParquetOption {
	return func(o *parquetOpts) { o.columns = append(o.columns, names...) }
}

func resolveParquetOpts(opts []ParquetOption) parquetOpts {
	var o parquetOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// ReadParquet streams rows of T from a Parquet file. The Parquet
// schema is matched against T's struct fields by name (case
// insensitive); unknown columns are silently dropped, missing
// fields keep their zero value. Pass [ParquetStrict] to reject
// schema mismatches.
//
// Reflection happens once at file-open time. Per-row decoding uses
// Arrow column arrays directly — no string parsing, no boxing
// through `any`.
func ReadParquet[T any](filename string, opts ...ParquetOption) iter.Seq[T] {
	return func(yield func(T) bool) {
		f, err := os.Open(filename)
		if err != nil {
			return
		}
		defer f.Close()
		ReadParquetFromReaderAt[T](f, opts...)(yield)
	}
}

// ReadParquetSafe is the error-reporting variant of [ReadParquet].
// Errors during open / schema mapping / per-row decoding are
// surfaced via the iter.Seq2 second value.
func ReadParquetSafe[T any](filename string, opts ...ParquetOption) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		f, err := os.Open(filename)
		if err != nil {
			var zero T
			yield(zero, fmt.Errorf("typed.ReadParquet: open %q: %w", filename, err))
			return
		}
		defer f.Close()
		ReadParquetSafeFromReaderAt[T](f, opts...)(yield)
	}
}

// ReadParquetFromReaderAt is the [parquet.ReaderAtSeeker] variant of
// [ReadParquet]. Parquet is a random-access format, so the source
// must support both Read+Seek+ReadAt — `*os.File` and
// `bytes.Reader` satisfy this naturally.
func ReadParquetFromReaderAt[T any](r parquet.ReaderAtSeeker, opts ...ParquetOption) iter.Seq[T] {
	o := resolveParquetOpts(opts)
	return func(yield func(T) bool) {
		readParquet[T](r, o, func(row T) bool {
			return yield(row)
		}, nil)
	}
}

// ReadParquetSafeFromReaderAt is the error-reporting [parquet.ReaderAtSeeker]
// variant of [ReadParquet].
func ReadParquetSafeFromReaderAt[T any](r parquet.ReaderAtSeeker, opts ...ParquetOption) iter.Seq2[T, error] {
	o := resolveParquetOpts(opts)
	return func(yield func(T, error) bool) {
		errYield := func(err error) bool {
			var zero T
			return yield(zero, err)
		}
		readParquet[T](r, o, func(row T) bool { return yield(row, nil) }, errYield)
	}
}

// readParquet is the shared implementation. errYield is nil when
// errors should be silently dropped (the [ReadParquet] case);
// otherwise it's called for each error.
func readParquet[T any](r parquet.ReaderAtSeeker, o parquetOpts, rowYield func(T) bool, errYield func(error) bool) {
	ctx := context.Background()
	mem := memory.DefaultAllocator

	pf, err := file.NewParquetReader(r)
	if err != nil {
		if errYield != nil {
			errYield(fmt.Errorf("typed.ReadParquet: open: %w", err))
		}
		return
	}
	defer pf.Close()

	reader, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, mem)
	if err != nil {
		if errYield != nil {
			errYield(fmt.Errorf("typed.ReadParquet: arrow reader: %w", err))
		}
		return
	}

	arrowSchema, err := reader.Schema()
	if err != nil {
		if errYield != nil {
			errYield(fmt.Errorf("typed.ReadParquet: schema: %w", err))
		}
		return
	}

	// Map struct fields to parquet columns.
	plan, err := buildParquetReadPlan[T](arrowSchema, o)
	if err != nil {
		if errYield != nil {
			errYield(err)
		}
		return
	}

	// Read all (selected) row groups into one Arrow table — same
	// as the Record-mode path for simplicity. Parallel reading is
	// in [ReadParquetParallel].
	rowGroups := make([]int, pf.NumRowGroups())
	for i := range rowGroups {
		rowGroups[i] = i
	}
	tbl, err := reader.ReadRowGroups(ctx, plan.colIndices, rowGroups)
	if err != nil {
		if errYield != nil {
			errYield(fmt.Errorf("typed.ReadParquet: read row groups: %w", err))
		}
		return
	}
	defer tbl.Release()

	yieldTableRows[T](tbl, plan, rowYield, errYield)
}

// parquetReadPlan is the precomputed I/O plan: one decoder per
// selected Arrow column, plus the column indices to actually read
// from the file.
type parquetReadPlan struct {
	colIndices []int
	// decoders[i] decodes Arrow column i (indexed against colIndices,
	// not the raw Parquet schema) into a struct field at the given
	// offset. nil decoder means "drop this column".
	decoders []parquetColDecoder
}

// parquetColDecoder reads one value from the Arrow array at idx and
// writes it into the struct at offset off.
type parquetColDecoder struct {
	off uintptr
	fn  parquetDecodeFn
}

type parquetDecodeFn func(p unsafe.Pointer, arr arrow.Array, idx int)

// buildParquetReadPlan walks the struct's exported fields, finds the
// matching Arrow column, and constructs a decoder that copies the
// column's value into the struct field. Reflection happens here,
// once.
func buildParquetReadPlan[T any](sch *arrow.Schema, o parquetOpts) (*parquetReadPlan, error) {
	var zero T
	rt := reflect.TypeOf(zero)
	if rt == nil || rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("typed: T must be a struct, got %v", rt)
	}

	// Build a case-insensitive lookup of arrow columns.
	arrIdx := make(map[string]int, len(sch.Fields()))
	for i, f := range sch.Fields() {
		arrIdx[strings.ToLower(f.Name)] = i
	}

	// Optional column filter (case insensitive).
	colFilter := map[string]bool{}
	for _, c := range o.columns {
		colFilter[strings.ToLower(strings.TrimSpace(c))] = true
	}

	// Walk struct fields, build decoders.
	type pending struct {
		fieldName string
		colIdx    int
		decoder   parquetColDecoder
	}
	var pendings []pending
	matchedArrCols := make(map[int]bool, rt.NumField())

	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		name, skip := columnName(f)
		if skip {
			continue
		}
		key := strings.ToLower(name)
		idx, ok := arrIdx[key]
		if !ok {
			if o.strict && f.Type.Kind() != reflect.Pointer {
				return nil, fmt.Errorf("typed.ReadParquet: strict: struct field %q has no matching Parquet column", name)
			}
			continue
		}
		if len(colFilter) > 0 && !colFilter[key] {
			// User asked to skip this column.
			continue
		}
		dfn, err := parquetDecoderFor(sch.Field(idx).Type, f.Type)
		if err != nil {
			return nil, fmt.Errorf("typed.ReadParquet: field %q: %w", name, err)
		}
		pendings = append(pendings, pending{
			fieldName: name,
			colIdx:    idx,
			decoder:   parquetColDecoder{off: f.Offset, fn: dfn},
		})
		matchedArrCols[idx] = true
	}

	if o.strict {
		// Reverse check: any Parquet column that's not nil-tagged in T
		// is an error in strict mode (would silently drop data).
		for i, af := range sch.Fields() {
			if matchedArrCols[i] {
				continue
			}
			if len(colFilter) > 0 && !colFilter[strings.ToLower(af.Name)] {
				continue
			}
			return nil, fmt.Errorf("typed.ReadParquet: strict: Parquet column %q has no matching struct field", af.Name)
		}
	}

	// Sort pendings by Arrow column index so colIndices is sorted —
	// the Arrow library prefers it that way, and the decoder array
	// indexes line up with the read column array indices.
	// (Stable sort isn't required since each Arrow column appears at
	// most once.)
	for i := 0; i < len(pendings); i++ {
		for j := i + 1; j < len(pendings); j++ {
			if pendings[j].colIdx < pendings[i].colIdx {
				pendings[i], pendings[j] = pendings[j], pendings[i]
			}
		}
	}

	plan := &parquetReadPlan{
		colIndices: make([]int, len(pendings)),
		decoders:   make([]parquetColDecoder, len(pendings)),
	}
	for i, p := range pendings {
		plan.colIndices[i] = p.colIdx
		plan.decoders[i] = p.decoder
	}
	return plan, nil
}

// parquetDecoderFor returns a decoder closure that copies one Arrow
// column value into a struct field at offset off. The closure is
// matched on the **Arrow** column type (the Parquet logical type
// after pqarrow conversion); the struct field type only matters for
// numeric width.
//
// Pointer fields not yet supported in v1 — parquet schema would need
// to track nullability and we'd need a "set if not null" path.
// They're rare in typed pipelines and easy to add later.
func parquetDecoderFor(arrowT arrow.DataType, structT reflect.Type) (parquetDecodeFn, error) {
	if structT.Kind() == reflect.Pointer {
		return nil, fmt.Errorf("pointer field types not yet supported in typed.ReadParquet")
	}
	switch arrowT.ID() {
	case arrow.STRING, arrow.LARGE_STRING:
		if structT.Kind() != reflect.String {
			return nil, fmt.Errorf("Parquet column is string, struct field is %v", structT)
		}
		return decodeParquetString, nil
	case arrow.BOOL:
		if structT.Kind() != reflect.Bool {
			return nil, fmt.Errorf("Parquet column is bool, struct field is %v", structT)
		}
		return decodeParquetBool, nil
	case arrow.INT8:
		return makeIntDecoder[int8](structT)
	case arrow.INT16:
		return makeIntDecoder[int16](structT)
	case arrow.INT32:
		return makeIntDecoder[int32](structT)
	case arrow.INT64:
		return makeIntDecoder[int64](structT)
	case arrow.UINT8:
		return makeUintDecoder[uint8](structT)
	case arrow.UINT16:
		return makeUintDecoder[uint16](structT)
	case arrow.UINT32:
		return makeUintDecoder[uint32](structT)
	case arrow.UINT64:
		return makeUintDecoder[uint64](structT)
	case arrow.FLOAT32:
		return makeFloatDecoder[float32](structT)
	case arrow.FLOAT64:
		return makeFloatDecoder[float64](structT)
	case arrow.TIMESTAMP, arrow.DATE32, arrow.DATE64:
		if structT != timeType {
			return nil, fmt.Errorf("Parquet column is timestamp, struct field is %v (must be time.Time)", structT)
		}
		return decodeParquetTime, nil
	default:
		return nil, fmt.Errorf("unsupported Arrow column type %v", arrowT)
	}
}

// decodeParquetString aliases the Arrow string buffer into the struct
// field. Arrow's String / LargeString columns store all values in a
// single contiguous bytes buffer; reading via .Value(idx) returns a
// string that aliases that buffer. The buffer lives until
// arrow.Table.Release() is called by the iter.Seq's defer, so the
// alias is safe for the duration of the iter.Seq.
func decodeParquetString(p unsafe.Pointer, arr arrow.Array, idx int) {
	switch a := arr.(type) {
	case *array.String:
		*(*string)(p) = a.Value(idx)
	case *array.LargeString:
		*(*string)(p) = a.Value(idx)
	}
}

func decodeParquetBool(p unsafe.Pointer, arr arrow.Array, idx int) {
	a := arr.(*array.Boolean)
	*(*bool)(p) = a.Value(idx)
}

func decodeParquetTime(p unsafe.Pointer, arr arrow.Array, idx int) {
	switch a := arr.(type) {
	case *array.Timestamp:
		unit := a.DataType().(*arrow.TimestampType).Unit
		*(*time.Time)(p) = a.Value(idx).ToTime(unit)
	case *array.Date32:
		*(*time.Time)(p) = a.Value(idx).ToTime()
	case *array.Date64:
		*(*time.Time)(p) = a.Value(idx).ToTime()
	}
}

// makeIntDecoder returns a decoder that reads an Arrow IntN column
// and writes it into a Go int / int64 / int32 / etc. struct field.
// Width-narrowing (e.g. parquet INT64 → struct int8) is allowed
// only if the struct field can hold the value; we don't bounds-check
// per row because parquet readers are typically given correct
// widths. The cost would be substantial if added.
func makeIntDecoder[A interface {
	~int8 | ~int16 | ~int32 | ~int64
}](structT reflect.Type) (parquetDecodeFn, error) {
	switch structT.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
	default:
		return nil, fmt.Errorf("Parquet column is integer, struct field is %v", structT)
	}
	dst := structT.Kind()
	return func(p unsafe.Pointer, arr arrow.Array, idx int) {
		var v int64
		switch a := arr.(type) {
		case *array.Int8:
			v = int64(a.Value(idx))
		case *array.Int16:
			v = int64(a.Value(idx))
		case *array.Int32:
			v = int64(a.Value(idx))
		case *array.Int64:
			v = a.Value(idx)
		}
		switch dst {
		case reflect.Int:
			*(*int)(p) = int(v)
		case reflect.Int8:
			*(*int8)(p) = int8(v)
		case reflect.Int16:
			*(*int16)(p) = int16(v)
		case reflect.Int32:
			*(*int32)(p) = int32(v)
		case reflect.Int64:
			*(*int64)(p) = v
		}
	}, nil
}

func makeUintDecoder[A interface {
	~uint8 | ~uint16 | ~uint32 | ~uint64
}](structT reflect.Type) (parquetDecodeFn, error) {
	switch structT.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
	default:
		return nil, fmt.Errorf("Parquet column is unsigned integer, struct field is %v", structT)
	}
	dst := structT.Kind()
	return func(p unsafe.Pointer, arr arrow.Array, idx int) {
		var v uint64
		switch a := arr.(type) {
		case *array.Uint8:
			v = uint64(a.Value(idx))
		case *array.Uint16:
			v = uint64(a.Value(idx))
		case *array.Uint32:
			v = uint64(a.Value(idx))
		case *array.Uint64:
			v = a.Value(idx)
		}
		switch dst {
		case reflect.Uint:
			*(*uint)(p) = uint(v)
		case reflect.Uint8:
			*(*uint8)(p) = uint8(v)
		case reflect.Uint16:
			*(*uint16)(p) = uint16(v)
		case reflect.Uint32:
			*(*uint32)(p) = uint32(v)
		case reflect.Uint64:
			*(*uint64)(p) = v
		case reflect.Int:
			*(*int)(p) = int(v)
		case reflect.Int8:
			*(*int8)(p) = int8(v)
		case reflect.Int16:
			*(*int16)(p) = int16(v)
		case reflect.Int32:
			*(*int32)(p) = int32(v)
		case reflect.Int64:
			*(*int64)(p) = int64(v)
		}
	}, nil
}

func makeFloatDecoder[A interface{ ~float32 | ~float64 }](structT reflect.Type) (parquetDecodeFn, error) {
	switch structT.Kind() {
	case reflect.Float32, reflect.Float64:
	default:
		return nil, fmt.Errorf("Parquet column is float, struct field is %v", structT)
	}
	dst := structT.Kind()
	return func(p unsafe.Pointer, arr arrow.Array, idx int) {
		var v float64
		switch a := arr.(type) {
		case *array.Float32:
			v = float64(a.Value(idx))
		case *array.Float64:
			v = a.Value(idx)
		}
		switch dst {
		case reflect.Float32:
			*(*float32)(p) = float32(v)
		case reflect.Float64:
			*(*float64)(p) = v
		}
	}, nil
}

// yieldTableRows walks an arrow.Table row by row, yielding decoded
// T values. Used by both the serial reader and (per row group) the
// parallel reader.
func yieldTableRows[T any](tbl arrow.Table, plan *parquetReadPlan, yield func(T) bool, errYield func(error) bool) {
	nRows := int(tbl.NumRows())
	if nRows == 0 {
		return
	}
	// For each column, collect the chunks ahead of time. Most parquet
	// row groups produce a single chunk per column, but the API
	// allows splits.
	type chunkSpan struct {
		arr    arrow.Array
		offset int // start row index of this chunk in the column
	}
	type colChunks struct {
		spans []chunkSpan
	}

	cols := make([]colChunks, len(plan.decoders))
	for i := range plan.decoders {
		chunked := tbl.Column(i)
		offset := 0
		for _, ch := range chunked.Data().Chunks() {
			cols[i].spans = append(cols[i].spans, chunkSpan{arr: ch, offset: offset})
			offset += ch.Len()
		}
	}

	// Iterate rows. To handle multi-chunk columns correctly we track
	// the current chunk per column and advance as we cross chunk
	// boundaries.
	chunkPos := make([]int, len(plan.decoders)) // current chunk index per column
	for row := 0; row < nRows; row++ {
		var v T
		p := unsafe.Pointer(&v)
		for ci, dec := range plan.decoders {
			// Advance chunk if row exceeds current chunk's range.
			for chunkPos[ci]+1 < len(cols[ci].spans) &&
				row >= cols[ci].spans[chunkPos[ci]+1].offset {
				chunkPos[ci]++
			}
			span := cols[ci].spans[chunkPos[ci]]
			localIdx := row - span.offset
			if span.arr.IsNull(localIdx) {
				// Leave field at zero value. (For pointer fields we'd
				// set nil here, but we don't support pointer fields
				// in v1.)
				continue
			}
			dec.fn(unsafe.Add(p, dec.off), span.arr, localIdx)
		}
		if !yield(v) {
			return
		}
	}
	_ = errYield
}

// ReadParquetParallel reads a Parquet file using one shard per row
// group (capped at n). Each shard owns its own pqarrow FileReader
// and decodes its row groups independently into struct values.
//
// LIMITATION: peak memory is roughly nShards × max-row-group-size.
// For a Parquet file with very few large row groups, parallelism
// is bounded by row group count — there's no way to split a row
// group without re-decoding it.
//
// n=0 means runtime.GOMAXPROCS(0). If the file has fewer row
// groups than n, n is reduced accordingly.
func ReadParquetParallel[T any](filename string, n int, opts ...ParquetOption) Stream[T] {
	o := resolveParquetOpts(opts)
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}

	// Open once to read metadata. Each shard re-opens the file
	// because pqarrow.FileReader is not safe for concurrent use
	// from multiple goroutines (it shares state for read-coalescing).
	f0, err := os.Open(filename)
	if err != nil {
		return Stream[T]{shards: nil, n: 0}
	}
	pf0, err := file.NewParquetReader(f0)
	if err != nil {
		f0.Close()
		return Stream[T]{shards: nil, n: 0}
	}
	reader0, err := pqarrow.NewFileReader(pf0, pqarrow.ArrowReadProperties{}, memory.DefaultAllocator)
	if err != nil {
		pf0.Close()
		f0.Close()
		return Stream[T]{shards: nil, n: 0}
	}
	arrowSchema, err := reader0.Schema()
	if err != nil {
		pf0.Close()
		f0.Close()
		return Stream[T]{shards: nil, n: 0}
	}
	plan, err := buildParquetReadPlan[T](arrowSchema, o)
	if err != nil {
		pf0.Close()
		f0.Close()
		return Stream[T]{shards: nil, n: 0}
	}
	nRowGroups := pf0.NumRowGroups()
	pf0.Close()
	f0.Close()
	if nRowGroups == 0 {
		return Stream[T]{shards: nil, n: 0}
	}
	if n > nRowGroups {
		n = nRowGroups
	}

	// Partition row groups across shards round-robin so a long row
	// group at the end doesn't leave one worker idle. Contiguous
	// would also work; round-robin balances variance.
	shardRowGroups := make([][]int, n)
	for i := 0; i < nRowGroups; i++ {
		shardRowGroups[i%n] = append(shardRowGroups[i%n], i)
	}

	shards := make([]iter.Seq[T], n)
	for i := 0; i < n; i++ {
		rgs := shardRowGroups[i]
		shards[i] = func(yield func(T) bool) {
			f, err := os.Open(filename)
			if err != nil {
				return
			}
			defer f.Close()
			pf, err := file.NewParquetReader(f)
			if err != nil {
				return
			}
			defer pf.Close()
			reader, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, memory.DefaultAllocator)
			if err != nil {
				return
			}
			ctx := context.Background()
			// Read one row group at a time so peak memory is one
			// row group (not all-shard's row groups).
			for _, rg := range rgs {
				tbl, err := reader.ReadRowGroups(ctx, plan.colIndices, []int{rg})
				if err != nil {
					return
				}
				cont := true
				yieldTableRows[T](tbl, plan, func(row T) bool {
					if !yield(row) {
						cont = false
						return false
					}
					return true
				}, nil)
				tbl.Release()
				if !cont {
					return
				}
			}
		}
	}
	return Stream[T]{shards: shards, n: n}
}


// ---- Write path ----

// parquetWriteEncoder pushes one struct field's value into an Arrow
// array builder. The closure is bound to a specific (struct offset,
// builder type) pair at schema-build time.
type parquetWriteEncoder struct {
	off uintptr
	fn  func(p unsafe.Pointer, b array.Builder)
}

type parquetWritePlan struct {
	schema   *arrow.Schema
	encoders []parquetWriteEncoder
}

// buildParquetWriteSchema builds the Arrow schema for T plus the
// per-field encoders. Reflection happens here, once per call.
func buildParquetWriteSchema[T any]() (*parquetWritePlan, error) {
	var zero T
	rt := reflect.TypeOf(zero)
	if rt == nil || rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("typed.WriteParquet: T must be a struct, got %v", rt)
	}

	var fields []arrow.Field
	var encoders []parquetWriteEncoder
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		name, skip := columnName(f)
		if skip {
			continue
		}
		ft, enc, err := parquetEncoderFor(f.Type)
		if err != nil {
			return nil, fmt.Errorf("typed.WriteParquet: field %q: %w", name, err)
		}
		fields = append(fields, arrow.Field{Name: name, Type: ft, Nullable: f.Type.Kind() == reflect.Pointer})
		encoders = append(encoders, parquetWriteEncoder{off: f.Offset, fn: enc})
	}
	return &parquetWritePlan{
		schema:   arrow.NewSchema(fields, nil),
		encoders: encoders,
	}, nil
}

// parquetEncoderFor returns (Arrow type, encoder closure) for a Go
// struct field type. Pointer fields are not supported in v1.
func parquetEncoderFor(t reflect.Type) (arrow.DataType, func(unsafe.Pointer, array.Builder), error) {
	if t.Kind() == reflect.Pointer {
		return nil, nil, fmt.Errorf("pointer field types not yet supported in typed.WriteParquet")
	}
	if t == timeType {
		return &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, func(p unsafe.Pointer, b array.Builder) {
			tt := *(*time.Time)(p)
			b.(*array.TimestampBuilder).Append(arrow.Timestamp(tt.UnixMicro()))
		}, nil
	}
	switch t.Kind() {
	case reflect.String:
		return arrow.BinaryTypes.String, func(p unsafe.Pointer, b array.Builder) {
			b.(*array.StringBuilder).Append(*(*string)(p))
		}, nil
	case reflect.Bool:
		return arrow.FixedWidthTypes.Boolean, func(p unsafe.Pointer, b array.Builder) {
			b.(*array.BooleanBuilder).Append(*(*bool)(p))
		}, nil
	case reflect.Int8:
		return arrow.PrimitiveTypes.Int8, func(p unsafe.Pointer, b array.Builder) {
			b.(*array.Int8Builder).Append(*(*int8)(p))
		}, nil
	case reflect.Int16:
		return arrow.PrimitiveTypes.Int16, func(p unsafe.Pointer, b array.Builder) {
			b.(*array.Int16Builder).Append(*(*int16)(p))
		}, nil
	case reflect.Int32:
		return arrow.PrimitiveTypes.Int32, func(p unsafe.Pointer, b array.Builder) {
			b.(*array.Int32Builder).Append(*(*int32)(p))
		}, nil
	case reflect.Int, reflect.Int64:
		return arrow.PrimitiveTypes.Int64, func(p unsafe.Pointer, b array.Builder) {
			if t.Kind() == reflect.Int {
				b.(*array.Int64Builder).Append(int64(*(*int)(p)))
			} else {
				b.(*array.Int64Builder).Append(*(*int64)(p))
			}
		}, nil
	case reflect.Uint8:
		return arrow.PrimitiveTypes.Uint8, func(p unsafe.Pointer, b array.Builder) {
			b.(*array.Uint8Builder).Append(*(*uint8)(p))
		}, nil
	case reflect.Uint16:
		return arrow.PrimitiveTypes.Uint16, func(p unsafe.Pointer, b array.Builder) {
			b.(*array.Uint16Builder).Append(*(*uint16)(p))
		}, nil
	case reflect.Uint32:
		return arrow.PrimitiveTypes.Uint32, func(p unsafe.Pointer, b array.Builder) {
			b.(*array.Uint32Builder).Append(*(*uint32)(p))
		}, nil
	case reflect.Uint, reflect.Uint64:
		return arrow.PrimitiveTypes.Uint64, func(p unsafe.Pointer, b array.Builder) {
			if t.Kind() == reflect.Uint {
				b.(*array.Uint64Builder).Append(uint64(*(*uint)(p)))
			} else {
				b.(*array.Uint64Builder).Append(*(*uint64)(p))
			}
		}, nil
	case reflect.Float32:
		return arrow.PrimitiveTypes.Float32, func(p unsafe.Pointer, b array.Builder) {
			b.(*array.Float32Builder).Append(*(*float32)(p))
		}, nil
	case reflect.Float64:
		return arrow.PrimitiveTypes.Float64, func(p unsafe.Pointer, b array.Builder) {
			b.(*array.Float64Builder).Append(*(*float64)(p))
		}, nil
	}
	return nil, nil, fmt.Errorf("unsupported field type %v", t)
}

// WriteParquet writes a sequence of T to a Parquet file with Snappy
// compression. The Arrow schema is derived from T's struct fields
// (same `ssql:"name"` / `csv:"name"` tag rules as the CSV writers).
//
// All rows are buffered in memory before flushing; Parquet's
// footer-last format requires this. For very large outputs that
// don't fit in RAM, write to multiple files and concatenate.
func WriteParquet[T any](seq iter.Seq[T], filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return WriteParquetToWriter(seq, f)
}

// WriteParquetToWriter is the [io.Writer] variant of [WriteParquet].
func WriteParquetToWriter[T any](seq iter.Seq[T], w io.Writer) error {
	mem := memory.DefaultAllocator
	plan, err := buildParquetWriteSchema[T]()
	if err != nil {
		return err
	}
	return writeParquetSeq[T](seq, w, plan, mem)
}

// writeParquetSeq materializes the sequence into Arrow column
// builders, finalises a single arrow.Record + Table, and writes it
// out as Parquet with Snappy compression.
func writeParquetSeq[T any](seq iter.Seq[T], w io.Writer, plan *parquetWritePlan, mem memory.Allocator) error {
	builders := make([]array.Builder, len(plan.encoders))
	for i, f := range plan.schema.Fields() {
		builders[i] = array.NewBuilder(mem, f.Type)
	}
	defer func() {
		for _, b := range builders {
			b.Release()
		}
	}()

	var nRows int64
	for v := range seq {
		p := unsafe.Pointer(&v)
		for i, enc := range plan.encoders {
			enc.fn(unsafe.Add(p, enc.off), builders[i])
		}
		nRows++
	}
	if nRows == 0 {
		// Empty input: still write a valid Parquet file with the
		// schema and zero rows. Some downstream consumers expect
		// the file to exist.
	}

	cols := make([]arrow.Array, len(builders))
	for i, b := range builders {
		cols[i] = b.NewArray()
	}
	defer func() {
		for _, a := range cols {
			a.Release()
		}
	}()

	rec := array.NewRecord(plan.schema, cols, nRows)
	defer rec.Release()

	tbl := array.NewTableFromRecords(plan.schema, []arrow.Record{rec})
	defer tbl.Release()

	props := parquet.NewWriterProperties(
		parquet.WithCompression(compress.Codecs.Snappy),
	)
	arrProps := pqarrow.NewArrowWriterProperties()
	return pqarrow.WriteTable(tbl, w, nRows, props, arrProps)
}

// WriteParquet writes a Stream[T] to a Parquet file with Snappy
// compression. Each shard concurrently writes its rows into its own
// in-memory Parquet payload; the per-shard payloads are concatenated
// into a single output file by writing one row group per shard.
//
// Trade-off: peak memory ~2× output size (each shard buffers its
// payload). For huge outputs that don't fit in RAM, fall back to
// `Stream.Serial()` + [WriteParquet].
//
// Order: rows from shard 0 come before shard 1 etc. Within a shard,
// input order is preserved. Same as [Stream.WriteCSV].
func (s Stream[T]) WriteParquet(filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return s.WriteParquetToWriter(f)
}

// WriteParquetToWriter is the [io.Writer] variant of [Stream.WriteParquet].
//
// Implementation: each shard buffers its rows into Arrow column
// builders concurrently (no serial fan-in). The orchestrator then
// finalises and writes them as a single multi-row-group Parquet
// table — Parquet doesn't support concatenating independent files
// at the byte level, so we buffer the per-shard arrow.Records and
// pass them all to a single pqarrow.WriteTable call.
func (s Stream[T]) WriteParquetToWriter(w io.Writer) error {
	mem := memory.DefaultAllocator
	plan, err := buildParquetWriteSchema[T]()
	if err != nil {
		return err
	}

	// Each shard builds its own arrow.Record (one row group worth).
	records := make([]arrow.Record, len(s.shards))
	errs := make([]error, len(s.shards))
	var wg sync.WaitGroup
	wg.Add(len(s.shards))
	for i, shard := range s.shards {
		i, shard := i, shard
		go func() {
			defer wg.Done()
			builders := make([]array.Builder, len(plan.encoders))
			for j, f := range plan.schema.Fields() {
				builders[j] = array.NewBuilder(mem, f.Type)
			}
			defer func() {
				for _, b := range builders {
					b.Release()
				}
			}()
			var n int64
			for v := range shard {
				p := unsafe.Pointer(&v)
				for j, enc := range plan.encoders {
					enc.fn(unsafe.Add(p, enc.off), builders[j])
				}
				n++
			}
			cols := make([]arrow.Array, len(builders))
			for j, b := range builders {
				cols[j] = b.NewArray()
			}
			records[i] = array.NewRecord(plan.schema, cols, n)
			for _, c := range cols {
				c.Release()
			}
		}()
	}
	wg.Wait()

	for _, e := range errs {
		if e != nil {
			for _, r := range records {
				if r != nil {
					r.Release()
				}
			}
			return e
		}
	}

	// Assemble a Table containing all shard records.
	defer func() {
		for _, r := range records {
			if r != nil {
				r.Release()
			}
		}
	}()

	// Drop empty shard records — pqarrow requires at least one row
	// per record block.
	nonEmpty := make([]arrow.Record, 0, len(records))
	var totalRows int64
	for _, r := range records {
		if r == nil {
			continue
		}
		if r.NumRows() == 0 {
			continue
		}
		nonEmpty = append(nonEmpty, r)
		totalRows += r.NumRows()
	}
	if totalRows == 0 {
		// Write empty file with schema only.
		buf := &bytes.Buffer{}
		tbl := array.NewTableFromRecords(plan.schema, nil)
		defer tbl.Release()
		props := parquet.NewWriterProperties(parquet.WithCompression(compress.Codecs.Snappy))
		arrProps := pqarrow.NewArrowWriterProperties()
		if err := pqarrow.WriteTable(tbl, buf, 0, props, arrProps); err != nil {
			return err
		}
		_, werr := w.Write(buf.Bytes())
		return werr
	}

	tbl := array.NewTableFromRecords(plan.schema, nonEmpty)
	defer tbl.Release()
	props := parquet.NewWriterProperties(parquet.WithCompression(compress.Codecs.Snappy))
	arrProps := pqarrow.NewArrowWriterProperties()
	return pqarrow.WriteTable(tbl, w, totalRows, props, arrProps)
}
