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
	in := readSchemaModeInput(ctx.Stdin())
	args := ctx.RawArgs
	if len(args) > 0 && args[0] == cmdName {
		args = args[1:]
	}
	out, ok := lookupSchemaOp(cmdName)(nil, in, args)
	if !ok {
		return writeSchemaModeOutput(ctx.Stdout(), nil)
	}
	return writeSchemaModeOutput(ctx.Stdout(), out)
}
