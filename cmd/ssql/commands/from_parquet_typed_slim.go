//go:build slim

package commands

import (
	"fmt"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// generateFromParquetCodeTyped is the slim-build stub. The full
// implementation lives in from_parquet_typed.go behind the !slim
// build tag.
func generateFromParquetCodeTyped(filename string, columns []string) error {
	return lib.WriteErrorAndExit(getCommandString(),
		fmt.Errorf("ssql generate go -typed: 'from parquet' not available in slim build (no Apache Arrow Parquet support)"))
}
