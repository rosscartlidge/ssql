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
	"bufio"
	"fmt"
	"os"
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
func resolveSidecarTypes(files []string, overrides map[string]string, sidecar string, disabled bool, formats map[string]string) (map[string]string, map[string]string, error) {
	sidecarColumns = nil
	if disabled {
		return overrides, formats, nil
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
		return nil, nil, err
	}
	if ts == nil {
		return overrides, formats, nil
	}
	if overrides == nil {
		overrides = map[string]string{}
	}
	sidecarColumns = map[string]string{}
	for name, t := range ts.TypeOverrides() {
		if _, user := overrides[name]; !user {
			overrides[name] = t
			sidecarColumns[name] = ts.Source
		}
	}
	// A time column's cell pattern (DFC147 §7.2), under the user's -format.
	for name, pattern := range ts.TimeFormats() {
		if formats == nil {
			formats = map[string]string{}
		}
		if _, user := formats[name]; !user {
			formats[name] = pattern
			sidecarColumns[name] = ts.Source
		}
	}
	return overrides, formats, nil
}

// sidecarColumns is the current read's columns whose type or date format
// the sidecar supplied, column → the sidecar's path, so a column missing
// from the header is blamed on the sidecar, not on a -type the user never
// gave (checkTypeOverrideColumns; it said "-type names column(s)" until
// 2026-11-11). Set by resolveSidecarTypes: a process reads one source,
// as fromTimeFormats records.
var sidecarColumns map[string]string

// fromWithoutSidecar is the way past a sidecar a source can take itself.
const fromWithoutSidecar = "read without it with -no-sidecar"

// sidecarTypesFromArgs is resolveSidecarTypes for a translator that
// holds the source's argv (the SQL assembler): the -type / -t pairs, the
// -format pairs and the sidecar flags are read from args, and the sidecar
// is resolved for file. Returns the merged overrides (column → ssql type
// name, a formatted column implied time) and the time formats.
func sidecarTypesFromArgs(args []string, file string) (map[string]string, map[string]string, error) {
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
	overrides, formats, err := resolveSidecarTypes(files, overrides, sidecar, disabled, timeFormatsFromArgs(args))
	if err != nil {
		return nil, nil, err
	}
	for col := range formats {
		if t, ok := overrides[col]; !ok || t == "" || t == "auto" {
			overrides[col] = "time"
		}
	}
	return overrides, formats, nil
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
func goFieldType(goType string, isJSON bool, scale int) ssql.FieldType {
	// ssql:domain scalar
	switch strings.TrimPrefix(goType, "*") {
	case "int64":
		return ssql.FieldTypeInt
	case "ssql.Int128":
		return ssql.FieldTypeInt128
	case "ssql.Decimal":
		return ssql.DecimalType(scale)
	case "float64":
		return ssql.FieldTypeFloat
	case "bool":
		return ssql.FieldTypeBool
	case "time.Time":
		return ssql.FieldTypeTime
	case "ssql.Inet":
		return ssql.FieldTypeInet
	case "string":
		if isJSON {
			return ssql.FieldTypeJSON
		}
		return ssql.FieldTypeString
	}
	return ssql.FieldTypeString
}

// fieldTypeConst is the Go source for a FieldType (generated code): the
// constant, or ssql.DecimalType(S) for a decimal (no constant has a scale).
func fieldTypeConst(ft ssql.FieldType) string {
	if ft.IsDecimal() {
		return fmt.Sprintf("ssql.DecimalType(%d)", ft.Scale())
	}
	return "ssql.FieldType" + capitalizeFieldType(ft.String())
}

// sidecarNeedsFile is the error for `to csv -sidecar` without a FILE.
func sidecarNeedsFile(cmd string) error {
	return fmt.Errorf("%s -sidecar needs an output FILE: the datapackage.json is written beside it", cmd)
}

// sideFileTyping is a side input's declared column types as `from FILE`
// reads them (DFC146): the schema sidecar beside a CSV or TSV file, as
// -type overrides (a column with a date format implied time) and the
// formats to read those columns with. join, traverse -start-file, union,
// merge, except and intersect read a named side file directly; until
// 2026-11-11 every lane read it without its sidecar, so `join a.csv` and
// `join <(ssql from csv a.csv)` typed a decimal column differently. A
// process substitution, a missing file or another format: no typing.
func sideFileTyping(file string) (overrides, formats map[string]string, err error) {
	fi, ok := formatForPath(file)
	if !ok || (fi.Name != "csv" && fi.Name != "tsv") {
		return nil, nil, nil
	}
	if st, serr := os.Stat(file); serr != nil || !st.Mode().IsRegular() {
		return nil, nil, nil
	}
	overrides, formats, err = resolveSidecarTypes([]string{file}, nil, "", false, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(overrides) == 0 && len(formats) == 0 {
		return nil, nil, nil
	}
	// a sidecar column the file does not have is refused here as `from
	// FILE` refuses it, in every lane (the readers ignore an override for
	// a column that is not there)
	without := fmt.Sprintf("read the file without it: <(ssql from %s %s -no-sidecar)", fi.Name, file)
	if err := checkTypeOverrideColumns(sideFileHeader(file, fi.Name), overrides, file, without); err != nil {
		return nil, nil, err
	}
	overrides, formats = formatImpliedTypes(overrides, formats)
	return overrides, formats, nil
}

// sideFileHeader is a CSV or TSV file's header row (nil if unreadable,
// and the check is skipped, as the sources skip it).
func sideFileHeader(file, format string) []string {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	if format == "csv" {
		headers, _ := readCSVHeadersFromReader(f)
		return headers
	}
	line, _ := bufio.NewReader(f).ReadString('\n')
	return splitDelimHeader(strings.TrimRight(line, "\r\n"))
}

// sideCSVConfig is the reader config for a side file with that typing
// (tsv: the delimiter left to header detection, as from tsv reads it).
func sideCSVConfig(overrides, formats map[string]string, tsv bool) (ssql.CSVConfig, error) {
	cfg := ssql.DefaultCSVConfig()
	if tsv {
		cfg = ssql.DefaultTSVConfig()
	}
	if len(formats) > 0 {
		cfg.TimeFormats = formats
	}
	var err error
	cfg.TypeOverrides, err = fieldTypeMap(overrides)
	return cfg, err
}
