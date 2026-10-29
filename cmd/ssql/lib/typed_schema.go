package lib

import (
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/rosscartlidge/ssql/v4"
)

// SampleCSVSchema reads the header and up to maxRows data rows from the
// given CSV file, infers a Go-friendly schema, and returns it as a
// TypedSchema together with the source for the corresponding struct
// declaration.
//
// The Go type for each column is the most-restrictive type that all
// non-empty sampled values parse as, in this preference order:
//
//	int64 → float64 → bool → time.Time → string
//
// Columns whose samples are entirely empty default to *string
// (nullable string) so future non-empty rows don't crash on parse.
//
// typeName overrides the generated Go type name. If empty, the type
// name is derived from the filename: "employees.csv" → "EmployeeRow".
//
// The returned struct definition uses ssql:"colname" tags so the typed
// runtime maps CSV columns case-insensitively even when the CSV header
// uses snake_case.
func SampleCSVSchema(filename, typeName string, maxRows int, opts ...TypeOptions) (*TypedSchema, string, error) {
	return sampleDelimitedSchema(filename, typeName, maxRows, ',', firstTypeOptions(opts))
}

// TypeOptions carries the user's `-type FIELD TYPE` / `-default-type`
// overrides into typed schema sampling: an overridden column takes the
// named type (string, int, float, bool; "auto" = infer) instead of the
// sampled one. Before v4.91.0 typed codegen REFUSED overrides, so the
// remedy the exec error names ("override with -type v float") had no
// typed form.
type TypeOptions struct {
	Fields  map[string]string // column → type name
	Default string            // type for every column without an override ("" / "auto" = infer)
	Formats map[string]string // column → time pattern (-format COL PATTERN): the column is a time read with it
	Columns []string          // the columns to keep, in this order (-columns); nil = every header column
}

func firstTypeOptions(opts []TypeOptions) TypeOptions {
	if len(opts) > 0 {
		return opts[0]
	}
	return TypeOptions{}
}

// goTypeFor maps a `-type` name to the Go field type; ok is false for
// "auto" (or unknown), meaning "infer".
func (o TypeOptions) goTypeFor(column string) (string, bool) {
	name, ok := o.Fields[column]
	if !ok {
		name = o.Default
	}
	if base, scale := ParseWireType(name); base == TypeDecimal && scale >= 0 {
		return "ssql.Decimal", true // the scale: scaleFor (DFC155 E)
	}
	switch strings.ToLower(name) {
	case "string":
		return "string", true
	case "int":
		return "int64", true
	case "int128":
		return "ssql.Int128", true
	case "float":
		return "float64", true
	case "bool":
		return "bool", true
	case "time":
		return "time.Time", true
	case "json":
		// A nested value is its JSON text in the typed lane (as a json
		// field from a JSONL header); the typed→Record boundary re-wraps
		// it as a JSONString. The reader keeps the cell as text.
		return "string", true
	}
	return "", false
}

// scaleFor is the scale of a column overridden to decimal:S (0 otherwise).
func (o TypeOptions) scaleFor(column string) int {
	name, ok := o.Fields[column]
	if !ok {
		name = o.Default
	}
	if base, scale := ParseWireType(name); base == TypeDecimal && scale >= 0 {
		return scale
	}
	return 0
}

// isJSON reports whether column is overridden to json.
func (o TypeOptions) isJSON(column string) bool {
	name, ok := o.Fields[column]
	if !ok {
		name = o.Default
	}
	return strings.EqualFold(name, "json")
}

// SampleTSVSchema is the [SampleCSVSchema] variant for TSV files. It
// auto-detects the delimiter from the header (first non-identifier
// rune; defaults to '\t'), then samples the file with that
// delimiter. Returns the inferred schema, the rendered struct
// definition, and the detected delimiter byte (so the caller can
// emit `typed.WithDelim(...)` if it's non-tab).
func SampleTSVSchema(filename, typeName string, maxRows int, opts ...TypeOptions) (*TypedSchema, string, byte, error) {
	delim, err := detectTSVDelim(filename)
	if err != nil {
		return nil, "", 0, err
	}
	schema, def, err := sampleDelimitedSchema(filename, typeName, maxRows, rune(delim), firstTypeOptions(opts))
	return schema, def, delim, err
}

// detectTSVDelim peeks the first line of a file and returns the first
// non-identifier byte, defaulting to '\t' when the entire header
// consists of identifier characters.
func detectTSVDelim(filename string) (byte, error) {
	f, err := ssql.OpenInput(filename)
	if err != nil {
		return 0, fmt.Errorf("typed schema sample: %w", err)
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	n, _ := f.Read(buf)
	return DetectDelimInHeader(string(buf[:n])), nil
}

// DetectDelimInHeader returns the delimiter implied by a header line:
// the first non-identifier byte, defaulting to '\t'. The single
// detection rule shared by typed schema sampling and schema-mode
// header parsing — every backend must agree on how a "TSV" splits.
func DetectDelimInHeader(line string) byte {
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\n' || c == '\r' {
			break
		}
		if !isIdentByte(c, i == 0) {
			return c
		}
	}
	return '\t'
}

func isIdentByte(c byte, first bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		return true
	case !first && c >= '0' && c <= '9':
		return true
	}
	return false
}

// sampleDelimitedSchema is the shared body of [SampleCSVSchema] and
// [SampleTSVSchema]. delim is passed through to encoding/csv's
// Comma field (which natively supports any single-rune delimiter).
func sampleDelimitedSchema(filename, typeName string, maxRows int, delim rune, opts TypeOptions) (*TypedSchema, string, error) {
	if maxRows <= 0 {
		maxRows = 1000
	}
	if typeName == "" {
		typeName = TypeNameFromFilename(filename)
	}

	f, err := ssql.OpenInput(filename)
	if err != nil {
		return nil, "", fmt.Errorf("typed schema sample: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.Comma = delim
	r.ReuseRecord = false
	r.FieldsPerRecord = -1 // tolerate ragged rows during sampling
	header, err := r.Read()
	if err != nil {
		return nil, "", fmt.Errorf("typed schema sample: read header: %w", err)
	}

	cols := make([]colInfer, len(header))
	for i := range cols {
		cols[i] = newColInfer()
	}

	for n := 0; n < maxRows; n++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		for i, v := range rec {
			if i >= len(cols) {
				continue
			}
			cols[i].observe(strings.TrimSpace(v)) // as the decoder and every other lane do
		}
	}

	fields := make([]TypedSchemaField, len(header))
	usedNames := make(map[string]int, len(header))
	for i, name := range header {
		gn := goNameFromColumn(name)
		if usedNames[gn] > 0 {
			usedNames[gn]++
			gn = fmt.Sprintf("%s%d", gn, usedNames[gn])
		} else {
			usedNames[gn] = 1
		}
		goType := cols[i].resolve()
		var isJSON bool
		var scale int
		if t, ok := opts.goTypeFor(name); ok {
			goType = t
			isJSON = t == "string" && opts.isJSON(name)
			scale = opts.scaleFor(name)
		}
		if _, formatted := opts.Formats[name]; formatted {
			goType = "time.Time" // a pattern declares the column a time
		}
		fields[i] = TypedSchemaField{
			Name:   name,
			GoName: gn,
			GoType: goType,
			JSON:   isJSON,
			Scale:  scale,
		}
	}

	if len(opts.Columns) > 0 {
		if fields, err = selectSchemaFields(fields, opts.Columns, header); err != nil {
			return nil, "", err
		}
		typeName = ProjectedTypeName(typeName, fields)
	}

	schema := &TypedSchema{TypeName: typeName, Fields: fields}
	return schema, RenderStructDef(schema), nil
}

// selectSchemaFields keeps the named columns, in the order named: the
// struct holds only these, so the reader decodes (and clones) only these
// — `from csv -columns`, the projection the optimiser pushes into a CSV
// source as it does into a Parquet one. An unknown name is an error
// listing the header, as the Parquet reader's is.
func selectSchemaFields(fields []TypedSchemaField, columns, header []string) ([]TypedSchemaField, error) {
	out := make([]TypedSchemaField, 0, len(columns))
	for _, c := range columns {
		i := slices.Index(header, c)
		if i < 0 {
			return nil, fmt.Errorf("typed schema sample: -columns names %q, which is not in the header (available: %s)", c, strings.Join(header, ", "))
		}
		out = append(out, fields[i])
	}
	return out, nil
}

// ProjectedTypeName names the struct of a source read with -columns:
// the file's type name plus the columns it holds (EmployeesRowOfDept),
// so it cannot clash with the full row type of the same file elsewhere
// in the program — a process substitution's inner pipeline is not
// optimised, so `from F | include dept | union -file <(from F | include
// dept)` has the pruned EmployeesRow beside the unpruned one, and two
// structs of one name and different bodies do not compile (found by the
// corpus the day the CSV pruning rule landed, 2026-10-28).
func ProjectedTypeName(base string, fields []TypedSchemaField) string {
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("Of")
	for _, f := range fields {
		b.WriteString(f.GoName)
	}
	return b.String()
}

// RenderStructDef returns Go source for a struct declaration matching
// the given TypedSchema. Field tags use ssql:"colname".
func RenderStructDef(s *TypedSchema) string {
	return RenderStructDefWithComment(s, fmt.Sprintf("%s is the row type inferred from the input's header and a sample of its rows.\n"+
		"A value that does not fit a field's type is a fatal typed.ReadError naming the\n"+
		"row: fix the data, or set the column type with `-type COL TYPE` on the from stage\n"+
		"and regenerate.", s.TypeName))
}

// RenderStructDefWithComment is RenderStructDef with the struct's doc
// comment supplied (one or more lines, no `//`): a synthetic source
// (`from range`) says what its struct is instead of "inferred from the
// input's header".
func RenderStructDefWithComment(s *TypedSchema, comment string) string {
	var b strings.Builder
	for _, line := range strings.Split(comment, "\n") {
		b.WriteString("// " + line + "\n")
	}
	fmt.Fprintf(&b, "type %s struct {\n", s.TypeName)
	maxName := 0
	maxType := 0
	for _, f := range s.Fields {
		if len(f.GoName) > maxName {
			maxName = len(f.GoName)
		}
		if len(f.GoType) > maxType {
			maxType = len(f.GoType)
		}
	}
	for _, f := range s.Fields {
		// Both tags: `ssql` for the CSV/typed runtime, `json` so
		// encoding/json (typed.ReadJSONL / WriteJSONL) maps the same
		// column names — one struct serves every reader and writer.
		fmt.Fprintf(&b, "\t%-*s %-*s %s\n", maxName, f.GoName, maxType, f.GoType, FieldTag(f))
	}
	b.WriteString("}\n")
	return b.String()
}

// FieldTag is a typed struct field's tag: `ssql` for the CSV/typed
// runtime — with the decimal scale as an option, `ssql:"price,decimal:2"`
// (DFC155 E) — and `json` so encoding/json maps the same column names.
func FieldTag(f TypedSchemaField) string {
	ssqlTag := f.Name
	if strings.TrimPrefix(f.GoType, "*") == "ssql.Decimal" {
		ssqlTag += "," + WireDecimal(f.Scale)
	}
	return fmt.Sprintf("`ssql:%q json:%q`", ssqlTag, f.Name)
}

// TypeNameFromFilename derives a Go type identifier from a CSV
// filename: "employees.csv" → "EmployeeRow", "/path/to/q4_sales.tsv"
// → "Q4SalesRow". Strips path and extension, splits on non-letter
// runes, capitalizes each chunk, suffixes "Row".
func TypeNameFromFilename(filename string) string {
	base := filepath.Base(filename)
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	var b strings.Builder
	upper := true
	for _, r := range base {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if upper {
				b.WriteRune(unicode.ToUpper(r))
				upper = false
			} else {
				b.WriteRune(r)
			}
		} else {
			upper = true
		}
	}
	name := b.String()
	if name == "" || !unicode.IsLetter(rune(name[0])) {
		name = "Row" + name
	} else {
		name += "Row"
	}
	return name
}

// GoNameFromColumn turns a CSV column name into an exported Go field
// name: "dept_id" → "DeptID", "first name" → "FirstName".
//
// Exported for use by command-side code generators that need to derive
// a Go field name from a CSV-style identifier (e.g. when emitting a
// rename projection's new field name).
func GoNameFromColumn(col string) string { return goNameFromColumn(col) }

// goNameFromColumn is the implementation; see GoNameFromColumn.
func goNameFromColumn(col string) string {
	if col == "" {
		return "Col"
	}
	var b strings.Builder
	upper := true
	for _, r := range col {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if upper {
				b.WriteRune(unicode.ToUpper(r))
				upper = false
			} else {
				b.WriteRune(r)
			}
		} else {
			upper = true
		}
	}
	name := b.String()
	if name == "" {
		return "Col"
	}
	if !unicode.IsLetter(rune(name[0])) {
		name = "F" + name
	}
	// Common ID suffix idioms: "id" → "ID", "url" → "URL".
	for _, suffix := range []string{"Id", "Url", "Uri", "Ip", "Sql", "Json", "Xml", "Http", "Https"} {
		if strings.HasSuffix(name, suffix) {
			name = name[:len(name)-len(suffix)] + strings.ToUpper(suffix)
		}
	}
	return name
}

// ---- per-column type inference ----

type colInfer struct {
	any     bool // saw any non-empty value
	allInt  bool
	allI128 bool // every value an integer literal that fits int128 (DFC155 C)
	allFlt  bool
	allBool bool
	// No time inference: an RFC 3339 column stays a string unless -type,
	// -format or a sidecar declares it, as in exec and the SQL lane (whose
	// read_csv candidates exclude timestamps). Until 2026-10-14 the typed
	// lane alone made it a time.Time, so `ts > since` compared in typed
	// and errored per row in exec.
}

func newColInfer() colInfer {
	return colInfer{allInt: true, allI128: true, allFlt: true, allBool: true}
}

func (c *colInfer) observe(v string) {
	if v == "" {
		return
	}
	c.any = true
	if ssql.ZeroPaddedNumber(v) {
		c.allInt, c.allI128, c.allFlt = false, false, false // 007 is an identifier, not 7 (DFC133)
	}
	if c.allInt {
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			c.allInt = false
		}
	}
	if c.allI128 && !c.allInt {
		if _, err := ssql.ParseInt128(v); err != nil {
			c.allI128 = false
		}
	}
	if c.allFlt {
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			c.allFlt = false
		}
	}
	if c.allBool {
		if _, err := strconv.ParseBool(v); err != nil {
			c.allBool = false
		}
	}
}

func (c *colInfer) resolve() string {
	switch {
	case !c.any:
		return "*string" // nullable-by-default for all-empty columns
	case c.allInt:
		return "int64"
	case c.allI128:
		return "ssql.Int128" // a literal past int64 types the column int128, as exec does
	case c.allFlt:
		return "float64"
	case c.allBool:
		return "bool"
	default:
		return "string"
	}
}

// TypedSchemaFromHeader builds a TypedSchema from a JSONL `_schema`
// header (field order + wire types), for Record-mode sources entering
// typed codegen (DFC109 — e.g. `from ssh` under SSQL_MODE=typed). The
// header comes from sampling the source at generate time, so unlike
// [SampleCSVSchema] there is no value inference: the wire types are
// authoritative.
//
// Wire→Go mapping: int→int64, float→float64, bool→bool,
// string→string. Any other wire type (notably "any") is an error —
// the caller must fail loudly rather than guess (a wrong struct field
// silently drops every value via GetOr's default).
func TypedSchemaFromHeader(s *Schema, typeName string) (*TypedSchema, string, error) {
	if s == nil || len(s.Fields) == 0 {
		return nil, "", fmt.Errorf("typed schema from header: empty schema")
	}
	fields := make([]TypedSchemaField, len(s.Fields))
	usedNames := make(map[string]int, len(s.Fields))
	for i, name := range s.Fields {
		var goType string
		var isJSON bool
		var scale int
		base, sc := ParseWireType(s.Types[name])
		if base == TypeDecimal && sc >= 0 {
			base, scale = TypeDecimal, sc
		}
		switch base {
		case TypeDecimal:
			goType = "ssql.Decimal" // DFC155 E: the scale rides on the field
		case "int":
			goType = "int64"
		case "int128":
			goType = "ssql.Int128" // DFC155 C: a comparable two-word struct field
		case "float":
			goType = "float64"
		case "bool":
			goType = "bool"
		case "string":
			goType = "string"
		case "time":
			goType = "time.Time"
		case "json":
			// A nested value is its JSON text in the typed lane (the fast
			// JSONL decoder takes raw JSON into a string field); the
			// typed→Record boundary re-wraps it as a JSONString.
			goType = "string"
			isJSON = true
		default:
			return nil, "", fmt.Errorf(
				"typed schema from header: field %q has wire type %q — cannot map to a Go type (run with SSQL_MODE=record)",
				name, s.Types[name])
		}
		gn := goNameFromColumn(name)
		if usedNames[gn] > 0 {
			usedNames[gn]++
			gn = fmt.Sprintf("%s%d", gn, usedNames[gn])
		} else {
			usedNames[gn] = 1
		}
		fields[i] = TypedSchemaField{Name: name, GoName: gn, GoType: goType, JSON: isJSON, Scale: scale}
	}
	schema := &TypedSchema{TypeName: typeName, Fields: fields}
	return schema, RenderStructDef(schema), nil
}
