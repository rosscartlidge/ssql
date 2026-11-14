package ssql

import (
	"fmt"
	"iter"
)

// The heavy formats live in their own packages (DFC168): parquet, arrow
// and xlsx under github.com/rosscartlidge/ssql/v4, which a program
// imports when it uses them, so a CSV program links none of them. The
// root package reaches a format only where its own code needs one, and
// then through a hook that the format package sets in its init.

var parquetReader func(path string) (iter.Seq[Record], error)

// RegisterParquetReader is called by package parquet's init. It lets the
// catalog read a local Parquet shard in process (catalog_local.go).
func RegisterParquetReader(read func(path string) (iter.Seq[Record], error)) {
	parquetReader = read
}

// parquetLinked reports whether package parquet is in the program.
func parquetLinked() bool { return parquetReader != nil }

// readParquet reads through the registered reader, or fails naming the
// import that links it.
func readParquet(path string) (iter.Seq[Record], error) {
	if parquetReader == nil {
		return nil, fmt.Errorf(`%s: parquet support is not linked into this program: import _ "github.com/rosscartlidge/ssql/v4/parquet"`, path)
	}
	return parquetReader(path)
}
