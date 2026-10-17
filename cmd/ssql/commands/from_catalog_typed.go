//go:build !slim

package commands

import (
	"fmt"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// The typed lane of `from catalog` (DFC149 §4.6). A catalog whose pruned
// shards are all Parquet files on this host, with no pushdown stages, is
// read in the generated program by the typed Parquet reader over the
// catalog's files (typed.ReadParquetSourcesParallel): the struct is the
// first shard's Parquet schema plus the columns the catalog adds to
// every record — -shard-field and the metadata columns, as strings, the
// order schema mode gives them. The program re-reads and prunes the
// catalog when it runs (the filters are its flags, as in the record
// form), so a file added under the same layout is read; a layout that
// adds a key is a loud mismatch when the program runs. Before this the
// catalog source had no typed form and the whole program compiled as
// records: ten times slower than the typed program over the same files
// as a glob (measured 2026-10-17, seven rail partitions).

// catalogTypedUnsupported says why a catalog has no in-process typed
// form, or "" when it has one: a shard on another host or in another
// format, pushdown stages, no shard left after pruning at generate
// time. The record form then carries the pipeline (each shard runs the
// typed pipeline on its host; the local merge is records), and
// generateFromCatalogCode says so on stderr.
func catalogTypedUnsupported(entries []ssql.CatalogEntry, pipelineArgs []string) string {
	if len(pipelineArgs) > 0 {
		return "the pipeline pushes stages down to the shards (`-- …`)"
	}
	if len(entries) == 0 {
		return "no shard matches the catalog filters, so there is no schema to build the row type from"
	}
	for _, e := range entries {
		if !ssql.CatalogLocalParquet(e, nil) {
			return fmt.Sprintf("shard %s:%s is not a Parquet file on this host (format %q)", e.Host, e.Path, ssql.CatalogEntryFormat(e))
		}
	}
	return ""
}

// catalogTypedSchema is the row type of a typed program over the
// catalog: the first shard's Parquet schema, then the columns the
// catalog adds (schemaModeFromCatalog's list, as strings). A catalog
// column that is also a column of the shard takes its place in the
// record lane, so the field must already be a string; a hive key that
// is one is the record lane's clash error, raised when the program runs.
func catalogTypedSchema(catalogFile string, hive bool, entries []ssql.CatalogEntry, shardField string) (*lib.TypedSchema, error) {
	schema, err := readParquetTypedSchema(entries[0].Path, nil)
	if err != nil {
		return nil, fmt.Errorf("shard %s: %w", entries[0].Path, err)
	}
	cols, ranges, err := catalogMetadataColumns(catalogFile, hive, entries)
	if err != nil {
		return nil, err
	}
	var added []string
	if shardField != "" {
		added = append(added, shardField)
	}
	for _, c := range cols {
		if !ranges[c] {
			added = append(added, c)
		}
	}
	byName := map[string]int{}
	usedGo := map[string]int{}
	for i, f := range schema.Fields {
		byName[strings.ToLower(f.Name)] = i
		usedGo[f.GoName]++
	}
	for _, name := range added {
		if i, ok := byName[strings.ToLower(name)]; ok {
			if f := schema.Fields[i]; f.GoType != "string" {
				return nil, fmt.Errorf("the catalog's column %q is also a column of the shard, of type %s; the catalog's value is a string and takes the column's place — rename one of them, or run with SSQL_MODE=record", name, f.GoType)
			}
			continue
		}
		gn := lib.GoNameFromColumn(name)
		if usedGo[gn] > 0 {
			usedGo[gn]++
			gn = fmt.Sprintf("%s%d", gn, usedGo[gn])
		} else {
			usedGo[gn] = 1
		}
		byName[strings.ToLower(name)] = len(schema.Fields)
		schema.Fields = append(schema.Fields, lib.TypedSchemaField{Name: name, GoName: gn, GoType: "string"})
	}
	return schema, nil
}

// generateFromCatalogCodeTyped emits the typed init fragment, or returns
// handled=false with the reason the catalog has none.
func generateFromCatalogCodeTyped(catalogFile string, hive bool, filters []ssql.CatalogFilter, shardField string, pipelineArgs []string) (handled bool, reason string, err error) {
	entries, err := ssql.LoadCatalog(catalogFile, hive, filters)
	if err != nil {
		return true, "", lib.WriteErrorAndExit(getCommandString(), err)
	}
	if reason := catalogTypedUnsupported(entries, pipelineArgs); reason != "" {
		return false, reason, nil
	}
	schema, err := catalogTypedSchema(catalogFile, hive, entries, shardField)
	if err != nil {
		return true, "", lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: %w", err))
	}

	params, filterCode := catalogFilterParams(catalogFile, filters)
	landing := fmt.Sprintf(`entries, err := ssql.LoadCatalog(*flagCatalog, %t, %s)
	if err != nil {
		return fmt.Errorf("%%w", err)
	}
	sources, err := typed.CatalogParquetSources(entries, %q)
	if err != nil {
		return fmt.Errorf("%%w", err)
	}
	`, hive, filterCode, shardField)
	parallelCode := landing + fmt.Sprintf(`records := typed.ReadParquetSourcesParallel[%s](sources, 0)`, schema.TypeName)
	serialCode := landing + fmt.Sprintf(`records := typed.ReadParquetSources[%s](sources)`, schema.TypeName)
	imports := []string{"fmt", "github.com/rosscartlidge/ssql/v4/typed"}
	if needsTimeImport(schema) {
		imports = append(imports, "time")
	}

	frag := fromInitFragment("records", parallelCode, append([]string{}, imports...))
	frag.Params = params
	frag.OutputTypedSchema = schema
	frag.StructDefs = []string{lib.RenderStructDef(schema)}
	frag.IsStream = true
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeNone, Produces: lib.ShapeStream}
	frag.AltCodeIfSeq = serialCode
	frag.AltImportsIfSeq = append([]string{}, imports...)
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeNone, Produces: lib.ShapeSeqTyped}
	return true, "", lib.WriteCodeFragment(frag)
}
