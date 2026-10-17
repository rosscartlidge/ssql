//go:build slim

package commands

import "github.com/rosscartlidge/ssql/v4"

// generateFromCatalogCodeTyped is the slim-build stub: no Parquet
// reader, so no in-process typed form; the record form carries the
// pipeline, as from_catalog_typed.go's comment says.
func generateFromCatalogCodeTyped(catalogFile string, hive bool, filters []ssql.CatalogFilter, shardField string, pipelineArgs []string) (handled bool, reason string, err error) {
	return false, "this build has no Parquet reader (slim)", nil
}
