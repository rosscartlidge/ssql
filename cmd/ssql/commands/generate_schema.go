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
	cmd.Subcommand("schema").
		Description("List the fields produced by an SSQL_MODE=schema pipeline (for completion)").
		Example("(export SSQL_MODE=schema; ssql from csv data.csv | ssql group-by dept -count n) | ssql generate schema", "Fields after group-by").
		Example("(export SSQL_MODE=schema; ssql from csv data.csv | ssql rename -as name person) | ssql generate schema", "Fields after rename").
		Handler(func(ctx *cf.Context) error {
			w := ctx.Stdout()
			for _, name := range readSchemaModeInput(ctx.Stdin()) {
				fmt.Fprintln(w, name)
			}
			return nil
		}).
		Done()
}
