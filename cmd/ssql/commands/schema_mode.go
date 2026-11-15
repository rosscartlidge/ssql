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
	"bufio"
	"io"
	"iter"
	"strings"

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
// When overrides (the user's -type plus a sidecar's, DFC146) type every
// column, no row is read: the schema is exact without a sample.
func writeSchemaModeDelimited(w io.Writer, headers []string, overrides map[string]string, records iter.Seq[ssql.Record]) error {
	types := map[string]string{}
	names := headers
	if len(headers) > 0 && len(overrides) >= len(headers) {
		complete := true
		for _, h := range headers {
			t, ok := overrides[h]
			if !ok || t == "auto" {
				complete = false
				break
			}
			types[h] = t
		}
		if complete {
			return writeSchemaModeOutputTyped(w, headers, types)
		}
		clear(types)
	}
	func() {
		defer func() { _ = recover() }() // a bad cell types nothing; exec reports it
		sample := sampleSchemaRows(records)
		schema := lib.InferFromSample(sample)
		for _, h := range headers {
			if schema.HasField(h) {
				types[h] = schema.TypeOf(h)
			}
		}
		names = nestedPathNames(headers, types, sample)
	}()
	return writeSchemaModeOutputTyped(w, names, types)
}

// sampleSchemaRows reads the rows schema mode types a source from.
func sampleSchemaRows(records iter.Seq[ssql.Record]) []ssql.Record {
	var sample []ssql.Record
	for rec := range records {
		sample = append(sample, rec)
		if len(sample) >= schemaModeSampleRows {
			break
		}
	}
	return sample
}

// nestedPathNames appends the completable paths inside the sample's
// json-typed fields to names and types them: addr.city, addr.geo.lat
// (to lib.NestedPathDepth; a list's indices are typed by hand). The
// first sampled row holding an object names it, as flatten does
// (DFC144 Level 2). Every source's schema mode goes through here, so a
// JSONL file with a header and a delimited source list the same paths
// a JSON array file does (until 2026-10-09 only the array file did).
func nestedPathNames(names []string, types map[string]string, sample []ssql.Record) []string {
	var nested []string
	for _, n := range names {
		if types[n] != lib.TypeJSON {
			continue
		}
		for _, rec := range sample {
			js, ok := ssql.Get[ssql.JSONString](rec, n)
			if !ok || !strings.HasPrefix(strings.TrimSpace(string(js)), "{") {
				continue
			}
			for _, p := range lib.NestedPaths(n, js) {
				nested = append(nested, p.Name)
				types[p.Name] = p.Type
			}
			break
		}
	}
	return append(names, nested...)
}

// openJSONSource reads a JSON source (a JSON array file or JSON Lines,
// told apart by the first non-blank byte) far enough to know its shape:
// a `_schema` header's fields and types when the lines carry one
// (headed), and the records either way — a headed file's for sampling
// what its json-typed fields hold, a headless file's for naming and
// typing its columns. Shared by schema mode and the SQL translator's
// column seeding (jsonHeader) so the two cannot disagree about which
// files have a header (the translator read array files as lines until
// 2026-10-08 and so knew no columns for them).
func openJSONSource(br *bufio.Reader) (fields []string, types map[string]string, records iter.Seq[ssql.Record], headed bool) {
	for {
		b, err := br.Peek(1)
		if err != nil {
			return nil, nil, nil, false
		}
		if b[0] == ' ' || b[0] == '\t' || b[0] == '\n' || b[0] == '\r' {
			br.ReadByte()
			continue
		}
		break
	}
	if b, _ := br.Peek(1); b[0] == '[' {
		// A JSON array file has no header; sample its elements (this path
		// returned no names at all until 2026-10-08).
		return nil, nil, lib.ReadJSON(br), false
	}
	sr := lib.ReadJSONLWithSchema(br)
	if sr.Schema != nil && len(sr.Schema.Fields) > 0 {
		return sr.Schema.Fields, sr.Schema.Types, sr.Records, true
	}
	return nil, nil, sr.Records, false
}

// writeSchemaModeJSON is the schema-mode output of a JSON/JSONL source:
// the _schema header's names and types when present, otherwise the
// names in the first record's key order, typed from a sample like a
// delimited source (so a nested value shows `json`, not `any` — DFC144
// Level 0); either way the paths inside its json-typed objects follow.
// The source's -type overrides coerce the sampled rows as exec's reader
// coerces every row, and type their columns (they were ignored until
// 2026-11-15: `-type age float` was predicted int; DFC170).
func writeSchemaModeJSON(w io.Writer, r io.Reader, overrides map[string]ssql.FieldType) error {
	fields, headerTypes, records, headed := openJSONSource(bufio.NewReader(r))
	records = ssql.CoerceFieldTypes(records, overrides)
	names := fields
	types := map[string]string{}
	for k, t := range headerTypes {
		types[k] = t
	}
	func() {
		defer func() { _ = recover() }() // a bad line types nothing; exec reports it
		sample := sampleSchemaRows(records)
		defer func() {
			for f := range overrides {
				for _, row := range sample {
					if v, ok := ssql.Get[any](row, f); ok && v != nil {
						types[f] = lib.InferTypeString(v)
						break
					}
				}
			}
		}()
		if headed {
			names = nestedPathNames(fields, types, sample)
			return
		}
		if len(sample) > 0 {
			for k := range sample[0].KeysIter() {
				names = append(names, k)
			}
		}
		schema := lib.InferFromSample(sample)
		for _, n := range names {
			if schema.HasField(n) {
				types[n] = schema.TypeOf(n)
			}
		}
		names = nestedPathNames(names, types, sample)
	}()
	return writeSchemaModeOutputTyped(w, names, types)
}

// runSchemaModeTransform answers schema mode for a stage that only passes
// the schema on (tee, the `to` sinks): it reads the incoming header and
// hands it to answerSchemaMode. A transform reads its input itself,
// checks its fields as exec does, and then calls answerSchemaMode
// (DFC170: answering before the check let `group-by department` through).
func runSchemaModeTransform(ctx *cf.Context, cmdName string) error {
	return answerSchemaMode(ctx, cmdName, lib.ReadJSONLWithSchema(ctx.Stdin()).Schema)
}

// schemaOutputFromArgs names the commands whose output columns their own
// arguments decide, so they answer even after an unknown input: a
// group-by's keys and results, describe's fixed columns. Any other stage
// after an unknown input is unknown too (an include of x does not say
// whether x exists).
var schemaOutputFromArgs = map[string]bool{"group-by": true, "describe": true}

// answerSchemaMode applies a command's schemaOp to the incoming schema
// and writes the result. cmdName keys the op registry; ctx.RawArgs
// (minus a leading command name, if present) supplies the argv the op
// decodes. "Unknown" is no header at all, as in exec: an undeterminable
// op (ok=false), or an unknown input the op cannot see past, writes
// nothing, and every stage downstream reads a nil schema and checks no
// names against it. Until 2026-11-15 both wrote `fields: []`, which reads
// as "known to be empty", and the stage after a pivot refused every field
// it named (DFC170 §2.2).
func answerSchemaMode(ctx *cf.Context, cmdName string, in *lib.Schema) error {
	var inFields []string
	if in != nil {
		inFields = in.Fields
	}
	args := ctx.RawArgs
	if len(args) > 0 && args[0] == cmdName {
		args = args[1:]
	}
	out, ok := lookupSchemaOp(cmdName)(nil, inFields, args)
	if !ok || (in == nil && !schemaOutputFromArgs[cmdName]) {
		return nil
	}
	// The name rules say which fields come out; a field that survives
	// the stage keeps its source's type, and the stage's type op (where
	// it has one: group-by from the aggregate registry, cast, rename)
	// types the fields it creates or retypes. Anything else is "any".
	types := map[string]string{}
	inTypes := map[string]string{}
	for _, f := range inFields {
		inTypes[f] = in.TypeOf(f)
	}
	for _, f := range out {
		if t, ok := inTypes[f]; ok {
			types[f] = t
		}
	}
	if op, ok := schemaTypeOps[cmdName]; ok {
		created := op(inTypes, args)
		for _, f := range out {
			if t, ok := created[f]; ok {
				types[f] = t
				continue
			}
			if _, passed := inTypes[f]; passed {
				continue // an input column (a group key) is not a rollup copy
			}
			// a rollup/cube copy carries its base result's type
			for base, t := range created {
				if strings.HasSuffix(f, "_"+base) {
					types[f] = t
				}
			}
		}
	}
	return writeSchemaModeOutputTyped(ctx.Stdout(), out, types)
}

// writeSchemaAnswer is schema mode's answer for a command that builds its
// output header with the same function exec uses (extract, unpivot,
// resample, describe): the header with no records. A nil schema is
// "unknown" and writes nothing.
func writeSchemaAnswer(ctx *cf.Context, s *lib.Schema) error {
	if s == nil {
		return nil
	}
	return lib.WriteJSONLWithSchema(ctx.Stdout(), s, func(func(ssql.Record) bool) {})
}

// schemaModeSignal is the signal a signal command computes on in schema
// mode, where no rows arrive: zeros, enough for any default window, so the
// command's own code builds its output and the header-only write
// (writeWithInferredSchema) answers with exec's header. They refused with
// "no signal data found" until 2026-11-15, failing every schema-mode
// pipeline through them (DFC170 §2.4). Outside schema mode, or with a
// signal, it is the identity.
func schemaModeSignal(s ssql.Signal) ssql.Signal {
	if schemaMode() && len(s) == 0 {
		return make(ssql.Signal, 4096)
	}
	return s
}

// exprStaticWireType is the wire type an expression yields over rows of
// the header in, as the expr→Go transpiler types it for typed codegen (held
// to the VM by TestExprGoDifferential): schema mode's stand-in for the
// type exec reads off the first row (DFC170 §6). "" when the transpiler
// refuses the expression, or it reads a column whose type is unknown.
func exprStaticWireType(expression string, in *lib.Schema) string {
	if in == nil {
		return ""
	}
	known := lib.NewSchema()
	for _, f := range in.Fields {
		if t := in.TypeOf(f); t != "" && t != "any" {
			known.AddField(f, t)
		}
	}
	if len(known.Fields) == 0 {
		return ""
	}
	ts, _, err := lib.TypedSchemaFromHeader(known, "Row")
	if err != nil {
		return ""
	}
	v, err := exprToGo(expression, ts, "r")
	if err != nil {
		return ""
	}
	return exprGoWireType(v)
}

// exprGoWireType is the wire type of a transpiled expression's result.
func exprGoWireType(v exprGo) string {
	// ssql:domain scalar
	switch v.Type {
	case exprGoInt:
		return lib.TypeInt
	case exprGoInt128:
		return lib.TypeInt128
	case exprGoDecimal:
		return lib.WireDecimal(v.Scale)
	case exprGoFloat:
		return lib.TypeFloat
	case exprGoString:
		return lib.TypeString
	case exprGoBool:
		return lib.TypeBool
	case exprGoTime:
		return lib.TypeTime
	case exprGoInet:
		return lib.TypeInet
	}
	return ""
}
