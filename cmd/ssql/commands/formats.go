//go:build !slim

package commands

import (
	"io"
	"iter"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/arrow"
	"github.com/rosscartlidge/ssql/v4/parquet"
	"github.com/rosscartlidge/ssql/v4/xlsx"
)

// The heavy formats live in their own packages (DFC168), which the CLI
// links in every build but slim. Exec-mode commands reach them through
// these functions, whose slim twins in formats_slim.go refuse; generated
// code imports the package itself (xlsxImport and friends).

func readXLSX(path, sheet string) (iter.Seq[ssql.Record], error) {
	if sheet == "" {
		return xlsx.Read(path)
	}
	return xlsx.Read(path, xlsx.Config{SheetName: sheet})
}

func writeXLSX(records iter.Seq[ssql.Record], path, sheet string) error {
	if sheet == "" {
		return xlsx.Write(records, path)
	}
	return xlsx.Write(records, path, xlsx.Config{SheetName: sheet})
}

func readArrow(path string) (iter.Seq[ssql.Record], error) { return arrow.Read(path) }

func readArrowFrom(r io.Reader) (iter.Seq[ssql.Record], error) { return arrow.ReadFrom(r), nil }

// writeArrow writes records, with the `_schema` header's columns and
// types when the stream carried one (fields nil otherwise).
func writeArrow(records iter.Seq[ssql.Record], path string, fields []string, types map[string]string) error {
	var opts []arrow.WriteOption
	if fields != nil {
		opts = append(opts, arrow.WithColumnTypes(fields, types))
	}
	return arrow.Write(records, path, opts...)
}

func extractSignalFromArrow(path, field string) (ssql.Signal, error) {
	return arrow.ExtractSignal(path, field)
}

func readParquetColumns(path string, columns []string) (iter.Seq[ssql.Record], error) {
	return parquet.ReadColumns(path, columns)
}

func readParquetFiles(paths, columns []string) (iter.Seq[ssql.Record], error) {
	return parquet.ReadFiles(paths, columns)
}

func checkParquetSchemas(files []string) error { return parquet.CheckSchemas(files) }

func parquetSchemaFields(path string) ([]string, map[string]string, error) {
	return parquet.SchemaFields(path)
}

func parquetRowCount(path string) (int64, error) { return parquet.RowCount(path) }

// writeParquet writes records at the row group size and compression
// given, with the `_schema` header's columns when there was one.
func writeParquet(records iter.Seq[ssql.Record], path string, rowGroupSize int, compression string, fields []string, types map[string]string) error {
	opts := []parquet.WriteOption{parquet.WithRowGroupSize(rowGroupSize), parquet.WithCompression(compression)}
	if fields != nil {
		opts = append(opts, parquet.WithColumnTypes(fields, types))
	}
	return parquet.Write(records, path, opts...)
}
