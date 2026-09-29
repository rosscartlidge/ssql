package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
)

// registerGenerateSchema registers the "generate schema" subcommand —
// the terminal stage of an SSQL_MODE=schema pipeline. It reads the
// final schema header and prints the field names, one per line, for a
// bash completion shim to feed to compgen.
//
//	(export SSQL_MODE=schema; ssql from csv data.csv | ssql rename -as name person) | ssql generate schema
//	  → person
//	    <other fields…>
func registerGenerateSchema(cmd *cf.SubcommandBuilder) {
	sub := cmd.Subcommand("schema").
		Description("List the fields produced by an SSQL_MODE=schema pipeline (for completion)").
		Example("(export SSQL_MODE=schema; ssql from csv data.csv | ssql group-by dept -count n) | ssql generate schema", "Fields after group-by").
		Example("ssql generate schema -pipeline 'ssql from csv data.csv | ssql rename -as name person'", "The same without the export: the source flags every generate target has").
		Example("ssql generate schema -json pipeline.json", "Fields a pipeline document produces")
	// The source flags mean the same here as on every target: run the
	// pipeline in the mode THIS target consumes (schema) and read what it
	// writes — a schema header rather than fragments.
	pipelineSourceFlags(sub, "list the fields of", "schema").
		Handler(func(ctx *cf.Context) error {
			src, err := generateFragmentSource(ctx, "schema", "schema")
			if err != nil {
				return err
			}
			w := ctx.Stdout()
			for _, name := range readSchemaModeInput(src) {
				fmt.Fprintln(w, name)
			}
			return nil
		}).
		Done()
}
