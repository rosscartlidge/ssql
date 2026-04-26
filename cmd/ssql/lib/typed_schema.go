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
	r.ReuseRecord = false // we hold rec slices across iterations
	header, err := r.Read()
	if err != nil {
		return nil, "", fmt.Errorf("typed schema sample: read header: %w", err)
	}

	// Per-column running state for type inference.
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
			// Tolerate a malformed row mid-sample — keep what we have.
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
		// Disambiguate Go-name collisions (e.g. "User Name" + "user_name").
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

// goNameFromColumn turns a CSV column name into an exported Go field
// name: "dept_id" → "DeptID", "first name" → "FirstName".
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
