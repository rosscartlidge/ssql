//go:build !slim

package typed

import (
	"fmt"
	"iter"
	"reflect"
	"strings"
	"unsafe"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// A catalog's shards in the typed lane (DFC149 §4.6). A catalog adds
// columns to every record of a shard that the shard's file does not
// hold: the key=value directories of a hive path, the catalog CSV's own
// columns, -shard-field. In the record lane those are attached per
// record (catalogEnricher); here they are string fields of T that the
// reader sets on every row of a file after the file's columns are
// decoded — one slice of (offset, value) per file, no per-row lookup.
// A generated typed program over `from catalog` reads its local Parquet
// shards this way instead of compiling the whole program in record form
// (ten times slower on the seven rail files, measured 2026-10-17).

// ParquetConstant is one column a file's rows carry beyond the file's
// own: its name, as the struct field is named (case-insensitive, the
// `ssql` tag honoured), and the value every row of the file takes.
// FromPath marks a value derived from the file's path (a hive key): a
// column of the file with the same name is then an error, as it is in
// the record lane (ssql's hive-clash rule) — the file would silently
// shadow the layout. A catalog's own column takes the column's place
// instead, as it does in the record lane.
type ParquetConstant struct {
	Name     string
	Value    string
	FromPath bool
}

// ParquetSource is one file of a multi-file read and the constants its
// rows carry. The files of one read share one schema, as
// [ReadParquetFiles] requires.
type ParquetSource struct {
	Path      string
	Constants []ParquetConstant
}

// ReadParquetSources is [ReadParquetFiles] over sources: each file's
// rows, in order, with its constants set. Every constant must name a
// string field of T — the program was generated for the catalog's
// layout, and a layout that adds a key is a loud mismatch, not a
// dropped column. No sources: an empty sequence (a catalog pruned to
// nothing has no rows).
func ReadParquetSources[T any](sources []ParquetSource, opts ...ParquetOption) iter.Seq[T] {
	return func(yield func(T) bool) {
		if len(sources) == 0 {
			return
		}
		files, consts := parquetSourceParts("typed.ReadParquetSources", sources)
		s := readParquetUnitsParallel[T]("typed.ReadParquetSources", files, consts, 1, opts)
		for _, shard := range s.shards {
			shard(yield)
		}
	}
}

// ReadParquetSourcesParallel is [ReadParquetFilesParallel] over sources:
// one shard per row group across the files, n shards (0: GOMAXPROCS),
// each file's constants set on its rows.
func ReadParquetSourcesParallel[T any](sources []ParquetSource, n int, opts ...ParquetOption) Stream[T] {
	if len(sources) == 0 {
		return Stream[T]{shards: nil, n: 0}
	}
	files, consts := parquetSourceParts("typed.ReadParquetSourcesParallel", sources)
	return readParquetUnitsParallel[T]("typed.ReadParquetSourcesParallel", files, consts, n, opts)
}

// parquetSourceParts splits sources into the parallel slices the unit
// reader takes, checking that the files share one schema.
func parquetSourceParts(op string, sources []ParquetSource) ([]string, [][]ParquetConstant) {
	files := make([]string, len(sources))
	consts := make([][]ParquetConstant, len(sources))
	for i, s := range sources {
		files[i] = s.Path
		consts[i] = s.Constants
	}
	if len(files) > 1 {
		if err := ssql.CheckParquetSchemas(files); err != nil {
			failRead(op, strings.Join(files, " "), err)
		}
	}
	return files, consts
}

// CatalogParquetSources is the typed program's view of a loaded catalog:
// every entry must be a Parquet file on this host (the record lane's
// in-process fast path, ssql.CatalogLocalParquet), its constants the
// metadata ssql.CatalogRecordMetadata attaches in the record lane —
// -shard-field first, then the catalog's columns in record order, a
// hive key marked FromPath. A shard on another host or in another
// format is an error naming it: the typed program is generated for a
// catalog of local Parquet shards; the ship-and-run form over hosts is
// the record program's (SSQL_MODE=record).
func CatalogParquetSources(entries []ssql.CatalogEntry, shardField string) ([]ParquetSource, error) {
	sources := make([]ParquetSource, 0, len(entries))
	for _, e := range entries {
		if !ssql.CatalogLocalParquet(e, nil) {
			return nil, fmt.Errorf("shard %s:%s is not a Parquet file on this host (format %q): the typed program reads a catalog of local Parquet shards in-process; generate with SSQL_MODE=record for the ship-and-run form", e.Host, e.Path, ssql.CatalogEntryFormat(e))
		}
		fromPath := map[string]bool{}
		for _, k := range e.HiveKeys {
			fromPath[k] = true
		}
		names, values := ssql.CatalogRecordMetadata(e, shardField)
		src := ParquetSource{Path: e.Path, Constants: make([]ParquetConstant, len(names))}
		for i, name := range names {
			src.Constants[i] = ParquetConstant{Name: name, Value: values[i], FromPath: fromPath[name]}
		}
		sources = append(sources, src)
	}
	return sources, nil
}

// constantSetter writes one constant into a row of T.
type constantSetter struct {
	off   uintptr
	value string
}

func (s constantSetter) set(row unsafe.Pointer) {
	*(*string)(unsafe.Add(row, s.off)) = s.value
}

// constantSetters resolves a file's constants against T and the file's
// columns: each must be a string field of T, and one derived from the
// path must not also be a column of the file. fileCols holds the
// file's column names, lower-cased.
func constantSetters[T any](op, file string, consts []ParquetConstant, fileCols map[string]bool) []constantSetter {
	if len(consts) == 0 {
		return nil
	}
	var zero T
	rt := reflect.TypeOf(zero)
	fields := map[string]reflect.StructField{}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		name, skip := columnName(f)
		if skip {
			continue
		}
		fields[strings.ToLower(name)] = f
	}
	setters := make([]constantSetter, 0, len(consts))
	for _, c := range consts {
		key := strings.ToLower(c.Name)
		if c.FromPath && fileCols[key] {
			failRead(op, file, fmt.Errorf("partition key %q is also a column of the file — rename one of them", c.Name))
		}
		f, ok := fields[key]
		if !ok {
			failRead(op, file, fmt.Errorf("the catalog adds column %q, which %s has no field for — the program was generated for a different catalog layout; generate it again", c.Name, rt.Name()))
		}
		if f.Type.Kind() != reflect.String {
			failRead(op, file, fmt.Errorf("the catalog's column %q is field %s of type %s; a catalog column is a string", c.Name, f.Name, f.Type))
		}
		setters = append(setters, constantSetter{off: f.Offset, value: c.Value})
	}
	return setters
}
