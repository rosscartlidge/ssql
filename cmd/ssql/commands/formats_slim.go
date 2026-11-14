//go:build slim

package commands

import (
	"fmt"
	"io"
	"iter"

	"github.com/rosscartlidge/ssql/v4"
)

// The slim build (playground, WebVM, explore) links no heavy format
// (DFC151, DFC168): these refuse, naming what is missing.

func readXLSX(path, sheet string) (iter.Seq[ssql.Record], error) {
	return nil, fmt.Errorf("xlsx support not available in slim build")
}

func writeXLSX(records iter.Seq[ssql.Record], path, sheet string) error {
	return fmt.Errorf("xlsx support not available in slim build")
}

var (
	errArrowSlim   = fmt.Errorf("arrow support not available in slim build")
	errParquetSlim = fmt.Errorf("parquet support not available in slim build")
)

func readArrow(path string) (iter.Seq[ssql.Record], error)      { return nil, errArrowSlim }
func readArrowFrom(r io.Reader) (iter.Seq[ssql.Record], error)   { return nil, errArrowSlim }
func extractSignalFromArrow(path, field string) (ssql.Signal, error) { return nil, errArrowSlim }
func writeArrow(records iter.Seq[ssql.Record], path string, fields []string, types map[string]string) error {
	return errArrowSlim
}

func readParquetColumns(path string, columns []string) (iter.Seq[ssql.Record], error) {
	return nil, errParquetSlim
}
func readParquetFiles(paths, columns []string) (iter.Seq[ssql.Record], error) {
	return nil, errParquetSlim
}
func checkParquetSchemas(files []string) error { return errParquetSlim }
func parquetSchemaFields(path string) ([]string, map[string]string, error) {
	return nil, nil, errParquetSlim
}
func parquetRowCount(path string) (int64, error) { return 0, errParquetSlim }
func writeParquet(records iter.Seq[ssql.Record], path string, rowGroupSize int, compression string, fields []string, types map[string]string) error {
	return errParquetSlim
}
