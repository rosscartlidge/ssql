package commands

import (
	"fmt"
	"io"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
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
		Example("ssql generate schema -json pipeline.json", "Fields a pipeline document produces").
		Example("ssql generate schema -pipeline 'ssql from csv data.csv | ssql group-by dept -count n' -data | ssql to table", "The fields and their types as a table")
	// The source flags mean the same here as on every target: run the
	// pipeline in the mode THIS target consumes (schema) and read what it
	// writes — a schema header rather than fragments.
	pipelineSourceFlags(sub, "list the fields of", "schema").
		Flag("-data").
			Bool().
			Global().
			Help("Emit the fields as records (field, type) on the normal wire, so the list can flow on: … -data | ssql to table").
			Done().

		Flag("-datapackage").
			Bool().
			Global().
			Help("Print the fields as a Frictionless Table Schema ({\"fields\": [{name, type}]}) — the sidecar `to csv -sidecar` writes, for a pipeline's output (DFC146)").
			Done().

		Handler(func(ctx *cf.Context) error {
			src, err := generateFragmentSource(ctx, "schema", "schema")
			if err != nil {
				return err
			}
			asData, _ := ctx.GlobalFlags["-data"].(bool)
			asPackage, _ := ctx.GlobalFlags["-datapackage"].(bool)
			switch {
			case asData && asPackage:
				return fmt.Errorf("generate schema: -data and -datapackage are exclusive — pick one")
			case asData:
				return writeSchemaAsRecords(ctx.Stdout(), src)
			case asPackage:
				return writeSchemaAsTableSchema(ctx.Stdout(), src)
			}
			w := ctx.Stdout()
			for _, name := range readSchemaModeInput(src) {
				fmt.Fprintln(w, name)
			}
			return nil
		}).
		Done()
}

// writeSchemaAsRecords turns the final schema header into one record
// per field (field, type) on the wire, header first, so the list is
// data any stage can consume. A source that could not type a column
// (schema mode knows names before types) reports it as "any".
func writeSchemaAsRecords(w io.Writer, src io.Reader) error {
	sr := lib.ReadJSONLWithSchema(src)
	out := lib.NewSchema()
	out.AddField("field", lib.TypeString)
	out.AddField("type", lib.TypeString)
	records := func(yield func(ssql.Record) bool) {
		if sr.Schema == nil {
			return
		}
		for _, name := range sr.Schema.Fields {
			rec := ssql.MakeMutableRecord().String("field", name).String("type", sr.Schema.TypeOf(name)).Freeze()
			if !yield(rec) {
				return
			}
		}
	}
	return lib.WriteJSONLWithSchema(w, out, records)
}

// writeSchemaAsTableSchema prints the final schema header as a
// Frictionless Table Schema document (DFC146): the wire types mapped to
// the standard's names; a column schema mode could not type is `any`.
func writeSchemaAsTableSchema(w io.Writer, src io.Reader) error {
	sr := lib.ReadJSONLWithSchema(src)
	var fields []ssql.TableField
	if sr.Schema != nil {
		for _, name := range sr.Schema.Fields {
			fields = append(fields, ssql.TableField{Name: name, Type: wireFieldType(sr.Schema.TypeOf(name))})
		}
	}
	out, err := ssql.TableSchemaJSON(fields)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}
