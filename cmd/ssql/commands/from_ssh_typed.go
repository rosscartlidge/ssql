package commands

// Typed-mode codegen for `from ssh` (DFC109: Record→typed re-entry).
//
// Under SSQL_MODE=typed the struct comes from sampling the remote at
// generate time: run the remote pipeline with `limit N` injected
// right after the source and read the `_schema` header off the first
// line. Exec-mode headers carry exact wire types through every stage
// (including pushed-down group-bys), so the synthesized struct is
// authoritative, not inferred. Schema-shadow mode (SSQL_MODE=schema)
// is NOT used here — it is name-exact but type-degraded ("any").
//
// The emitted fragment is the standard dual-template source (the
// from_csv.go pattern): parallel form converts via
// typed.FromRecordsParallel (materialize + shard), serial alternative
// via typed.FromRecords (lazy). The converter closure is rendered
// per-field with ssql.GetOr, whose numeric coercion absorbs the
// JSONL wire's int/float ambiguity (a whole float arrives as int64).

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// sshSchemaSampleLimit is how many source rows the generate-time
// sampling run feeds through the (possibly pushed-down) remote
// pipeline. Types don't depend on cardinality, so a small prefix is
// enough; the limit keeps sampling cheap even when the pushdown ends
// in an aggregation.
const sshSchemaSampleLimit = 200

// sshSchemaSampleTimeout bounds the generate-time ssh round-trip.
const sshSchemaSampleTimeout = 60 * time.Second

// sampleSSHPipelineSchema runs the remote pipeline over a small
// source prefix and returns the `_schema` header of its output.
func sampleSSHPipelineSchema(host, remoteBin, path string, groups [][]string) (*lib.Schema, error) {
	sampleGroups := append([][]string{{"limit", fmt.Sprint(sshSchemaSampleLimit)}}, groups...)
	// Clear the mode vars on the remote: sampling needs exec-mode
	// JSONL, and a remote shell rc exporting SSQL_MODE would flip the
	// whole sampling pipeline into codegen. Constant prefix — no user
	// input, so no quoting concern.
	remoteCmd := "export SSQL_MODE= SSQLGO=; " + ssql.BuildRemoteCommand(remoteBin, path, "", sampleGroups)

	ctx, cancel := context.WithTimeout(context.Background(), sshSchemaSampleTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", host, remoteCmd)
	out, err := cmd.Output()
	if err != nil {
		detail := ""
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			detail = ": " + strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("sampling remote schema from %s:%s failed (%v%s)", host, path, err, detail)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if schema, ok := lib.ParseSchemaHeaderFromBytes([]byte(line)); ok {
			return schema, nil
		}
		break // first non-empty line wasn't a header — no schema on this wire
	}
	return nil, fmt.Errorf("sampling remote schema from %s:%s: output carried no _schema header", host, path)
}

// renderRecordConverter renders the explicit per-field Record→struct
// converter closure the generated code passes to typed.FromRecords*.
func renderRecordConverter(schema *lib.TypedSchema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "func(r ssql.Record) %s {\n\t\treturn %s{\n", schema.TypeName, schema.TypeName)
	for _, f := range schema.Fields {
		var def string
		switch f.GoType {
		case "int64":
			def = "int64(0)"
		case "float64":
			def = "float64(0)"
		case "bool":
			def = "false"
		default:
			def = `""`
		}
		fmt.Fprintf(&b, "\t\t\t%s: ssql.GetOr(r, %q, %s),\n", f.GoName, f.Name, def)
	}
	b.WriteString("\t\t}\n\t}")
	return b.String()
}

// generateFromSSHTypedCode is the SSQL_MODE=typed codegen path for
// `from ssh`, both plain and pushdown forms. It samples the remote
// schema, synthesizes the struct, and emits a dual-template init
// fragment whose Record landing is converted into the typed runtime.
// Sampling failure is a loud generate-time error — a user who asked
// for typed must not silently receive a Record-mode program.
func generateFromSSHTypedCode(host, path string, gpu bool, pipelineArgs []string) error {
	remoteBin := sshRemoteBin(gpu)
	groups := ssql.SplitOnPlus(pipelineArgs)

	header, err := sampleSSHPipelineSchema(host, remoteBin, path, groups)
	if err != nil {
		return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf(
			"ssql generate go -typed: %w (typed mode samples the remote schema at generate time; check the host is reachable, or run with SSQL_MODE=record)", err))
	}
	schema, structDef, err := lib.TypedSchemaFromHeader(header, lib.TypeNameFromFilename(path))
	if err != nil {
		return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: %w", err))
	}

	var landing string
	var imports []string
	var params []lib.CodeParam
	if len(pipelineArgs) > 0 {
		landing, imports, params = sshScriptLandingCode(host, path, pipelineArgs, "recordsRaw")
	} else {
		landing, imports, params = sshPlainLandingCode(host, path, remoteBin, "recordsRaw")
	}
	imports = append(imports,
		"github.com/rosscartlidge/ssql/v4",
		"github.com/rosscartlidge/ssql/v4/typed")

	conv := renderRecordConverter(schema)
	parallelCode := fmt.Sprintf("%s\n\trecords := typed.FromRecordsParallel(recordsRaw, %s, runtime.GOMAXPROCS(0))", landing, conv)
	parallelImports := append(append([]string{}, imports...), "runtime")
	serialCode := fmt.Sprintf("%s\n\trecords := typed.FromRecords(recordsRaw, %s)", landing, conv)

	frag := lib.NewInitFragment("records", parallelCode, parallelImports, getCommandString())
	frag.Params = params
	frag.OutputTypedSchema = schema
	frag.StructDefs = []string{structDef}
	frag.IsStream = true
	frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeNone, Produces: lib.ShapeStream}
	frag.AltCodeIfSeq = serialCode
	frag.AltImportsIfSeq = append([]string{}, imports...)
	frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeNone, Produces: lib.ShapeSeqTyped}
	return lib.WriteCodeFragment(frag)
}
