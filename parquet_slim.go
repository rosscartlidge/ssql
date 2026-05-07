//go:build slim

package ssql

import (
	"fmt"
	"io"
	"iter"
)

func ReadParquet(filename string) (iter.Seq[Record], error) {
	return nil, fmt.Errorf("parquet support not available in slim build")
}

func ReadParquetColumns(filename string, columns []string) (iter.Seq[Record], error) {
	return nil, fmt.Errorf("parquet support not available in slim build")
}

// ParquetWriteOption is a slim-build stub for the option type defined
// in the !slim variant of this file. Calling any of the writer
// functions returns the standard "not available in slim build" error.
type ParquetWriteOption func(*struct{})

func WithRowGroupSize(n int) ParquetWriteOption      { return func(*struct{}) {} }
func WithCompression(name string) ParquetWriteOption { return func(*struct{}) {} }

func WriteParquet(records iter.Seq[Record], filename string, opts ...ParquetWriteOption) error {
	return fmt.Errorf("parquet support not available in slim build")
}

func WriteParquetToWriter(records iter.Seq[Record], w io.Writer, opts ...ParquetWriteOption) error {
	return fmt.Errorf("parquet support not available in slim build")
}
