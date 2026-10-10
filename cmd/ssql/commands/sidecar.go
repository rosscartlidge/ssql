package commands

// Schema sidecars for delimited sources (DFC146): a CSVW metadata file
// or a Frictionless data package beside a CSV/TSV names and types its
// columns. The sidecar's types become `-type` overrides — one map every
// execution path already honours (exec, schema mode, -sample, -last,
// record and typed codegen, and the SQL translator) — with the user's
// own -type winning. Resolution happens once, in the command's handler;
// the SQL translator, which sees the command's argv, resolves the same
// way through sidecarTypesFromArgs.

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
)

// sidecarFlags adds -sidecar FILE and -no-sidecar to a delimited source.
func sidecarFlags(b *cf.SubcommandBuilder) *cf.SubcommandBuilder {
	return b.
		Flag("-sidecar").
		String().
		Global().
		Default("").
		Completer(&cf.FileCompleter{Pattern: "*.json"}).
		Help("Column types from this schema sidecar (CSVW metadata, a Frictionless datapackage.json or Table Schema). Default: X.csv-metadata.json, csv-metadata.json or datapackage.json beside FILE when one names it").
		Done().
		Flag("-no-sidecar").
		Bool().
		Global().
		Help("Ignore any schema sidecar beside FILE").
		Done()
}

// sidecarFlagValues reads the two flags from a parsed context.
func sidecarFlagValues(ctx *cf.Context) (sidecar string, disabled bool) {
	sidecar, _ = ctx.GlobalFlags["-sidecar"].(string)
	disabled, _ = ctx.GlobalFlags["-no-sidecar"].(bool)
	return sidecar, disabled
}

// resolveSidecarTypes merges a sidecar's column types under the user's
// -type overrides: an explicit -sidecar FILE (for stdin too), else the
// discovery rule beside a single local file (a URL is not searched; a
// multi-file read is not searched — the files could disagree — but an
// explicit -sidecar applies to all of them). The user's own -type for a
// column wins. The sidecar's dialect and formats the readers cannot
// honour are errors from the parser (DFC146 §5).
func resolveSidecarTypes(files []string, overrides map[string]string, sidecar string, disabled bool) (map[string]string, error) {
	if disabled {
		return overrides, nil
	}
	var ts *ssql.TableSchema
	var err error
	switch {
	case sidecar != "":
		name := ""
		if len(files) == 1 {
			name = files[0]
		}
		ts, err = ssql.ReadTableSchema(sidecar, name)
	case len(files) == 1 && !ssql.IsHTTPURL(files[0]):
		ts, err = ssql.FindTableSchema(files[0])
	}
	if err != nil {
		return nil, err
	}
	if ts == nil {
		return overrides, nil
	}
	if overrides == nil {
		overrides = map[string]string{}
	}
	for name, t := range ts.TypeOverrides() {
		if _, user := overrides[name]; !user {
			overrides[name] = t
		}
	}
	return overrides, nil
}

// sidecarTypesFromArgs is resolveSidecarTypes for a translator that
// holds the source's argv (the SQL assembler): the -type / -t pairs and
// the sidecar flags are read from args, and the sidecar is resolved for
// file. Returns the merged overrides, column → ssql type name.
func sidecarTypesFromArgs(args []string, file string) (map[string]string, error) {
	overrides := map[string]string{}
	var sidecar string
	var disabled bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-type", "-t":
			if i+2 < len(args) {
				overrides[args[i+1]] = args[i+2]
				i += 2
			}
		case "-sidecar":
			if i+1 < len(args) {
				sidecar = args[i+1]
				i++
			}
		case "-no-sidecar":
			disabled = true
		}
	}
	var files []string
	if file != "" {
		files = []string{file}
	}
	return resolveSidecarTypes(files, overrides, sidecar, disabled)
}

// wireFieldType is the ssql.FieldType of a wire type name from a schema
// header ("any" and anything unknown: auto).
func wireFieldType(wire string) ssql.FieldType {
	ft, err := ssql.ParseFieldType(wire)
	if err != nil {
		return ssql.FieldTypeAuto
	}
	return ft
}

// goFieldType is the ssql.FieldType a typed field is written as in a
// sidecar: the Go type's, a JSON-flagged string as json.
func goFieldType(goType string, isJSON bool) ssql.FieldType {
	switch strings.TrimPrefix(goType, "*") {
	case "int64":
		return ssql.FieldTypeInt
	case "float64":
		return ssql.FieldTypeFloat
	case "bool":
		return ssql.FieldTypeBool
	case "time.Time":
		return ssql.FieldTypeTime
	case "string":
		if isJSON {
			return ssql.FieldTypeJSON
		}
		return ssql.FieldTypeString
	}
	return ssql.FieldTypeString
}

// fieldTypeConst is the Go source for a FieldType (generated code).
func fieldTypeConst(ft ssql.FieldType) string {
	return "ssql.FieldType" + capitalizeFieldType(ft.String())
}

// sidecarNeedsFile is the error for `to csv -sidecar` without a FILE.
func sidecarNeedsFile(cmd string) error {
	return fmt.Errorf("%s -sidecar needs an output FILE: the datapackage.json is written beside it", cmd)
}
