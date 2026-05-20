//go:build !slim

package commands

import (
	"fmt"
	"iter"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// buildServeCLI assembles the small autocli Command tree shown at the
// SSH prompt. Each handler reads from ctx.State (a *serveState) and
// writes to ctx.Stdout(). Handlers are intentionally minimal in v1 —
// just enough to demonstrate the operator-console UX.
//
// Future additions: query (run a pipeline against the loaded data),
// reload (re-read the file), :var (named intermediates), etc.
func buildServeCLI() *cf.Command {
	b := cf.NewCommand("serve")

	// Serve-specific commands: state shortcuts + pipeline source.
	b.Subcommand("from-loaded").
		Description("emit the in-memory dataset as JSONL — pipeline source").
		Handler(serveFromLoadedHandler).
		Done()

	b.Subcommand("status").
		Description("show uptime, dataset path, row count").
		Handler(serveStatusHandler).
		Done()

	b.Subcommand("schema").
		Description("show the dataset schema (field names + inferred types)").
		Handler(serveSchemaHandler).
		Done()

	// Bash transforms — reused unchanged. Each reads JSONL from
	// ctx.Stdin() and writes JSONL to ctx.Stdout(), so they compose
	// downstream of `from-loaded` in a Position 2 pipe.
	b = RegisterWhere(b)
	b = RegisterCount(b)
	b = RegisterLimit(b)
	b = RegisterOffset(b)
	b = RegisterTop(b)
	b = RegisterSort(b)
	b = RegisterDistinct(b)
	b = RegisterInclude(b)
	b = RegisterExclude(b)
	b = RegisterRename(b)
	b = RegisterCast(b)
	b = RegisterUpdate(b)
	b = RegisterGroupBy(b)
	b = RegisterPivot(b)
	b = RegisterWindow(b)

	// Stream-output sinks under `to`. File-writing variants
	// (parquet/arrow/xlsx/wav/chart/explore/animate) are deliberately
	// not registered — they'd write to server-side paths, which is
	// surprising for SSH operators. Future v2 can sandbox / forbid.
	toCmd := b.Subcommand("to").
		Description("Write output in various formats (table, csv, tsv, json, jsonl)")
	registerToTable(toCmd)
	registerToCSV(toCmd)
	registerToTSV(toCmd)
	registerToJSON(toCmd)
	registerToJSONL(toCmd)
	toCmd.Done()

	return b.Build()
}

func serveStatusHandler(ctx *cf.Context) error {
	srv := ctx.State.(*serveState)
	w := ctx.Stdout()
	fmt.Fprintf(w, "uptime:  %v\n", time.Since(srv.started).Round(time.Second))
	fmt.Fprintf(w, "loaded:  %v ago in %v\n", time.Since(srv.started).Round(time.Second), srv.loadTime)
	fmt.Fprintf(w, "path:    %s\n", srv.path)
	fmt.Fprintf(w, "rows:    %d\n", len(srv.records))
	fmt.Fprintf(w, "fields:  %d\n", len(srv.schema))
	return nil
}

func serveSchemaHandler(ctx *cf.Context) error {
	srv := ctx.State.(*serveState)
	w := ctx.Stdout()
	if len(srv.records) == 0 {
		fmt.Fprintln(w, "(empty dataset — no schema)")
		return nil
	}
	r := srv.records[0]
	for _, name := range srv.schema {
		v := ssql.GetOr[any](r, name, nil)
		fmt.Fprintf(w, "  %-20s  %s\n", name, inferTypeForServe(v))
	}
	return nil
}

// serveFromLoadedHandler emits the in-memory dataset as schema-headed
// JSONL on ctx.Stdout(). Pipeline source — sits at the start of a
// `from-loaded | where … | to table` chain. The wire format matches
// what bash `ssql from FOO.csv` produces, so downstream transforms
// (which read JSONL from ctx.Stdin()) work unchanged.
//
// Schema is inferred once from the first record's field types; types
// are NOT re-inferred per row (the dataset is uniformly-typed by
// virtue of having come through ssql.ReadCSV / ReadJSONL at startup).
func serveFromLoadedHandler(ctx *cf.Context) error {
	srv := ctx.State.(*serveState)
	if len(srv.records) == 0 {
		// Empty dataset — emit nothing. Downstream sees EOF immediately.
		return nil
	}

	schema := lib.NewSchema()
	first := srv.records[0]
	for _, name := range srv.schema {
		v := ssql.GetOr[any](first, name, nil)
		schema.AddField(name, lib.InferTypeString(v))
	}

	// Wrap []Record as iter.Seq[Record] for the writer.
	records := func(yield func(ssql.Record) bool) {
		for _, r := range srv.records {
			if !yield(r) {
				return
			}
		}
	}
	return lib.WriteJSONLWithSchema(ctx.Stdout(), schema, iter.Seq[ssql.Record](records))
}

// inferTypeForServe returns a short, readable type tag for a value.
// Used by serveSchemaHandler to summarise field types.
func inferTypeForServe(v any) string {
	switch v.(type) {
	case int, int32, int64:
		return "int"
	case float32, float64:
		return "float"
	case bool:
		return "bool"
	case time.Time:
		return "time"
	case nil:
		return "null"
	default:
		return "string"
	}
}
