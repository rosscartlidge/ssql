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

func WriteParquet(records iter.Seq[Record], filename string) error {
	return fmt.Errorf("parquet support not available in slim build")
}

func WriteParquetToWriter(records iter.Seq[Record], w io.Writer) error {
	return fmt.Errorf("parquet support not available in slim build")
}
