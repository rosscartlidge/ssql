package lib

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
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
func SampleCSVSchema(filename, typeName string, maxRows int) (*TypedSchema, string, error) {
	return sampleDelimitedSchema(filename, typeName, maxRows, ',')
}

// SampleTSVSchema is the [SampleCSVSchema] variant for TSV files. It
// auto-detects the delimiter from the header (first non-identifier
// rune; defaults to '\t'), then samples the file with that
// delimiter. Returns the inferred schema, the rendered struct
// definition, and the detected delimiter byte (so the caller can
// emit `typed.WithDelim(...)` if it's non-tab).
func SampleTSVSchema(filename, typeName string, maxRows int) (*TypedSchema, string, byte, error) {
	delim, err := detectTSVDelim(filename)
	if err != nil {
		return nil, "", 0, err
	}
	schema, def, err := sampleDelimitedSchema(filename, typeName, maxRows, rune(delim))
	return schema, def, delim, err
}

// detectTSVDelim peeks the first line of a file and returns the first
// non-identifier byte, defaulting to '\t' when the entire header
// consists of identifier characters.
func detectTSVDelim(filename string) (byte, error) {
	f, err := os.Open(filename)
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
func sampleDelimitedSchema(filename, typeName string, maxRows int, delim rune) (*TypedSchema, string, error) {
	if maxRows <= 0 {
		maxRows = 1000
	}
	if typeName == "" {
		typeName = TypeNameFromFilename(filename)
	}

	f, err := os.Open(filename)
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
			cols[i].observe(v)
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
		fields[i] = TypedSchemaField{
			Name:   name,
			GoName: gn,
			GoType: cols[i].resolve(),
		}
	}

	schema := &TypedSchema{TypeName: typeName, Fields: fields}
	return schema, RenderStructDef(schema), nil
}

// RenderStructDef returns Go source for a struct declaration matching
// the given TypedSchema. Field tags use ssql:"colname".
func RenderStructDef(s *TypedSchema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s is the row type inferred from the CSV header.\n", s.TypeName)
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
		fmt.Fprintf(&b, "\t%-*s %-*s `ssql:%q`\n", maxName, f.GoName, maxType, f.GoType, f.Name)
	}
	b.WriteString("}\n")
	return b.String()
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
	allFlt  bool
	allBool bool
	allTime bool
}

func newColInfer() colInfer {
	return colInfer{allInt: true, allFlt: true, allBool: true, allTime: true}
}

func (c *colInfer) observe(v string) {
	if v == "" {
		return
	}
	c.any = true
	if c.allInt {
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			c.allInt = false
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
	if c.allTime {
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			c.allTime = false
		}
	}
}

func (c *colInfer) resolve() string {
	switch {
	case !c.any:
		return "*string" // nullable-by-default for all-empty columns
	case c.allInt:
		return "int64"
	case c.allFlt:
		return "float64"
	case c.allBool:
		return "bool"
	case c.allTime:
		return "time.Time"
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
		switch s.Types[name] {
		case "int":
			goType = "int64"
		case "float":
			goType = "float64"
		case "bool":
			goType = "bool"
		case "string":
			goType = "string"
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
		fields[i] = TypedSchemaField{Name: name, GoName: gn, GoType: goType}
	}
	schema := &TypedSchema{TypeName: typeName, Fields: fields}
	return schema, RenderStructDef(schema), nil
}
