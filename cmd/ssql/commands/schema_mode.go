package commands

// SSQL_MODE=schema — the bash two-pass mode (Phase 2 of the
// schema-aware-completion work). Each command, run under
// SSQL_MODE=schema, reads an input schema header from stdin instead of
// data, applies its schemaOp (the same per-command rules the in-process
// serve completion uses), and writes the output schema header to
// stdout. A terminal `ssql generate schema` turns the final header into
// a plain field list a bash completion shim can feed to compgen.
//
// Unlike the serve case (which hand-decodes raw pipeline tokens in
// tabComplete), here each command runs as a real subprocess and the
// handler has a parsed Context — but to keep ONE rule per command we
// still drive the slice-5 schemaOps, feeding them ctx.RawArgs.
//
// See doc/research/schema-aware-completion.md §5–§6.

import (
	"io"
	"iter"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// schemaMode reports whether the pipeline is running under
// SSQL_MODE=schema.
func schemaMode() bool {
	return modeEnv() == "schema"
}

// readSchemaModeInput reads the field names from a schema-mode stdin
// (a lone _schema header, no records). Returns nil when the header is
// absent or poisoned.
func readSchemaModeInput(r io.Reader) []string {
	sr := lib.ReadJSONLWithSchema(r)
	if sr.Schema == nil {
		return nil
	}
	return sr.Schema.Fields
}

// writeSchemaModeOutput writes a schema header carrying just the given
// field names (type "any" — schema mode tracks names, not types). No
// records follow.
func writeSchemaModeOutput(w io.Writer, names []string) error {
	schema := lib.NewSchema()
	for _, n := range names {
		schema.AddField(n, "any")
	}
	return lib.WriteJSONLWithSchema(w, schema, func(func(ssql.Record) bool) {})
}

// writeSchemaModeOutputTyped is writeSchemaModeOutput for sources that
// KNOW their types (parquet footers) — downstream schemaOps and the
// header consumers see real wire types instead of "any".
func writeSchemaModeOutputTyped(w io.Writer, names []string, types map[string]string) error {
	schema := lib.NewSchema()
	for _, n := range names {
		t := types[n]
		if t == "" {
			t = "any"
		}
		schema.AddField(n, t)
	}
	return lib.WriteJSONLWithSchema(w, schema, func(func(ssql.Record) bool) {})
}

// schemaModeSampleRows is how many records a delimited source reads in
// schema mode to type its columns: enough for the reader's inference
// to see a float below the ints, cheap on any file.
const schemaModeSampleRows = 200

// writeSchemaModeDelimited is the schema-mode output of a CSV/TSV source
// WITH types: the header names in file order, and each column's wire
// type from a sample read with the same config (so `-type ts time` and
// `-default-type` hold) — schema mode carried names only ("any") until
// v4.109, which is why `generate schema -data` showed no types. A read
// error in the sample (a malformed row, a cell that will not cast)
// leaves the column untyped ("any") rather than failing completion.
func writeSchemaModeDelimited(w io.Writer, headers []string, records iter.Seq[ssql.Record]) error {
	types := map[string]string{}
	func() {
		defer func() { _ = recover() }() // a bad cell types nothing; exec reports it
		var sample []ssql.Record
		for rec := range records {
			sample = append(sample, rec)
			if len(sample) >= schemaModeSampleRows {
				break
			}
		}
		schema := lib.InferFromSample(sample)
		for _, h := range headers {
			if schema.HasField(h) {
				types[h] = schema.TypeOf(h)
			}
		}
	}()
	return writeSchemaModeOutputTyped(w, headers, types)
}

// schemaModeJSONNames reads field names from a JSON/JSONL source under
// schema mode: the _schema header when present, otherwise the first
// record's keys.
func schemaModeJSONNames(r io.Reader) []string {
	sr := lib.ReadJSONLWithSchema(r)
	if sr.Schema != nil && len(sr.Schema.Fields) > 0 {
		return sr.Schema.Fields
	}
	for rec := range sr.Records {
		var names []string
		for k := range rec.KeysIter() {
			names = append(names, k)
		}
		return names
	}
	return nil
}

// runSchemaModeTransform applies a transform command's schemaOp to the
// incoming schema header and writes the result. cmdName keys the op
// registry; ctx.RawArgs (minus a leading command name, if present)
// supplies the argv the op decodes. An undeterminable op (ok=false)
// emits an empty schema, which propagates downstream as "no fields".
func runSchemaModeTransform(ctx *cf.Context, cmdName string) error {
	sr := lib.ReadJSONLWithSchema(ctx.Stdin())
	var in []string
	if sr.Schema != nil {
		in = sr.Schema.Fields
	}
	args := ctx.RawArgs
	if len(args) > 0 && args[0] == cmdName {
		args = args[1:]
	}
	out, ok := lookupSchemaOp(cmdName)(nil, in, args)
	if !ok {
		return writeSchemaModeOutput(ctx.Stdout(), nil)
	}
	// The rules track names; a field that survives the stage keeps the
	// type its source gave it, a field the stage creates is "any" (the
	// rules do not know that -count makes an int, and saying so here
	// would be a second implementation of every aggregate's type).
	types := map[string]string{}
	if sr.Schema != nil {
		for _, f := range out {
			if sr.Schema.HasField(f) {
				types[f] = sr.Schema.TypeOf(f)
			}
		}
		// rename moves a field's type with its name: `-as old new`
		if cmdName == "rename" {
			for i := 0; i+2 < len(args); i++ {
				if (args[i] == "-as" || args[i] == "-a") && sr.Schema.HasField(args[i+1]) {
					types[args[i+2]] = sr.Schema.TypeOf(args[i+1])
				}
			}
		}
	}
	return writeSchemaModeOutputTyped(ctx.Stdout(), out, types)
}
