package ssql

// CSV schema sidecars (DFC146): a JSON file beside a delimited file that
// names and types its columns. Two standards are read — W3C CSV on the
// Web metadata (X.csv-metadata.json / csv-metadata.json) and a
// Frictionless Data Package (datapackage.json) or bare Table Schema —
// and one is written, the Frictionless package. Either way the result
// is a TableSchema: the columns and their ssql field types, which the
// readers take as per-column type overrides and schema mode takes as
// the exact types without reading a row.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// TableField is one column of a TableSchema. Shape is "object" or
// "array" for a json field when known (Frictionless tells them apart;
// a value written tells it too), else "".
type TableField struct {
	Name  string
	Type  FieldType
	Shape string
}

// TableSchema is a sidecar's description of a delimited file.
type TableSchema struct {
	Fields []TableField
	// Source is the sidecar file the schema came from; Kind is "csvw" or
	// "frictionless".
	Source, Kind string
}

// TypeOverrides is the schema as `-type COL TYPE` overrides: every
// typed column by its ssql type name. A column typed `any` (infer) is
// left out.
func (s *TableSchema) TypeOverrides() map[string]string {
	out := make(map[string]string, len(s.Fields))
	for _, f := range s.Fields {
		if f.Type != FieldTypeAuto {
			out[f.Name] = f.Type.String()
		}
	}
	return out
}

// FindTableSchema applies the discovery rule to a local delimited file:
// X.csv-metadata.json (CSVW), then csv-metadata.json in the directory
// with a table whose url names the file, then datapackage.json in the
// directory with a resource whose path names the file. The first that
// exists and names the file wins; none is (nil, nil). A sidecar that
// exists but does not parse is an error — a wrong schema must not be
// silently ignored.
func FindTableSchema(csvPath string) (*TableSchema, error) {
	dir, base := filepath.Split(csvPath)
	for _, candidate := range []string{csvPath + "-metadata.json", filepath.Join(dir, "csv-metadata.json"), filepath.Join(dir, "datapackage.json")} {
		data, err := os.ReadFile(candidate)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("sidecar %s: %w", candidate, err)
		}
		s, err := ParseTableSchema(data, base, filepath.Dir(candidate))
		if err != nil {
			if errors.Is(err, errTableNotDescribed) {
				continue // a package for other files
			}
			return nil, fmt.Errorf("sidecar %s: %w", candidate, err)
		}
		s.Source = candidate
		return s, nil
	}
	return nil, nil
}

// ReadTableSchema reads a named sidecar for csvPath ("" when the data
// has no name — stdin — in which case a package's first resource is
// taken).
func ReadTableSchema(sidecarPath, csvPath string) (*TableSchema, error) {
	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		return nil, fmt.Errorf("sidecar: %w", err)
	}
	base := ""
	if csvPath != "" {
		base = filepath.Base(csvPath)
	}
	s, err := ParseTableSchema(data, base, filepath.Dir(sidecarPath))
	if err != nil {
		return nil, fmt.Errorf("sidecar %s: %w", sidecarPath, err)
	}
	s.Source = sidecarPath
	return s, nil
}

var errTableNotDescribed = errors.New("the sidecar describes other files")

// ParseTableSchema parses a sidecar of any of the three shapes, told
// apart by their top-level keys: a Frictionless package (`resources`),
// a bare Table Schema (`fields`), or CSVW metadata (`tableSchema` or
// `tables`). csvBase is the data file's base name, matched against a
// package resource's path or a CSVW table's url; "" takes the first.
// dir resolves a schema given as a relative file path.
func ParseTableSchema(data []byte, csvBase, dir string) (*TableSchema, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	switch {
	case top["resources"] != nil:
		return parseFrictionlessPackage(top, csvBase, dir)
	case top["fields"] != nil:
		return parseFrictionlessSchema(data)
	case top["tableSchema"] != nil, top["tables"] != nil:
		return parseCSVW(top, csvBase, dir)
	}
	return nil, fmt.Errorf("neither a Frictionless package (resources), a Table Schema (fields) nor CSVW metadata (tableSchema)")
}

// ---- Frictionless -------------------------------------------------------

type frictionlessField struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Format string `json:"format"`
}

type frictionlessDialect struct {
	Delimiter string `json:"delimiter"`
	Header    *bool  `json:"header"`
}

type frictionlessResource struct {
	Name    string              `json:"name"`
	Path    json.RawMessage     `json:"path"`
	Schema  json.RawMessage     `json:"schema"`
	Dialect frictionlessDialect `json:"dialect"`
}

func parseFrictionlessPackage(top map[string]json.RawMessage, csvBase, dir string) (*TableSchema, error) {
	var resources []frictionlessResource
	if err := json.Unmarshal(top["resources"], &resources); err != nil {
		return nil, fmt.Errorf("resources: %w", err)
	}
	for _, r := range resources {
		if csvBase != "" && !pathNames(r.Path, csvBase) {
			continue
		}
		if len(r.Schema) == 0 {
			return nil, fmt.Errorf("resource %q has no schema", r.Name)
		}
		if err := checkDialect(r.Dialect.Delimiter, r.Dialect.Header); err != nil {
			return nil, err
		}
		schemaData := []byte(r.Schema)
		var ref string
		if json.Unmarshal(r.Schema, &ref) == nil {
			b, err := os.ReadFile(filepath.Join(dir, ref))
			if err != nil {
				return nil, fmt.Errorf("resource %q schema %s: %w", r.Name, ref, err)
			}
			schemaData = b
		}
		return parseFrictionlessSchema(schemaData)
	}
	return nil, errTableNotDescribed
}

// pathNames reports whether a resource path (a string or an array of
// them) names csvBase: equal to it, or ending in "/" + it.
func pathNames(raw json.RawMessage, csvBase string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == csvBase || strings.HasSuffix(one, "/"+csvBase)
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, p := range many {
			if p == csvBase || strings.HasSuffix(p, "/"+csvBase) {
				return true
			}
		}
	}
	return false
}

func parseFrictionlessSchema(data []byte) (*TableSchema, error) {
	var s struct {
		Fields []frictionlessField `json:"fields"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	if len(s.Fields) == 0 {
		return nil, fmt.Errorf("schema has no fields")
	}
	out := &TableSchema{Kind: "frictionless"}
	for _, f := range s.Fields {
		if f.Name == "" {
			return nil, fmt.Errorf("a field has no name")
		}
		ft, shape, err := sidecarFieldType(f.Name, f.Type, f.Format)
		if err != nil {
			return nil, err
		}
		out.Fields = append(out.Fields, TableField{Name: f.Name, Type: ft, Shape: shape})
	}
	return out, nil
}

// ---- CSVW ----------------------------------------------------------------

type csvwColumn struct {
	Name     string          `json:"name"`
	Titles   json.RawMessage `json:"titles"`
	Datatype json.RawMessage `json:"datatype"`
	Virtual  bool            `json:"virtual"`
}

type csvwTable struct {
	URL         string              `json:"url"`
	TableSchema json.RawMessage     `json:"tableSchema"`
	Dialect     frictionlessDialect `json:"dialect"`
}

func parseCSVW(top map[string]json.RawMessage, csvBase, dir string) (*TableSchema, error) {
	var tables []csvwTable
	if top["tables"] != nil {
		if err := json.Unmarshal(top["tables"], &tables); err != nil {
			return nil, fmt.Errorf("tables: %w", err)
		}
	} else {
		var t csvwTable
		if url := top["url"]; url != nil {
			json.Unmarshal(url, &t.URL)
		}
		t.TableSchema = top["tableSchema"]
		if d := top["dialect"]; d != nil {
			json.Unmarshal(d, &t.Dialect)
		}
		tables = []csvwTable{t}
	}
	for _, t := range tables {
		// A single-table document with no url describes its own file.
		if csvBase != "" && t.URL != "" && filepath.Base(t.URL) != csvBase {
			continue
		}
		if err := checkDialect(t.Dialect.Delimiter, t.Dialect.Header); err != nil {
			return nil, err
		}
		schemaData := []byte(t.TableSchema)
		var ref string
		if json.Unmarshal(t.TableSchema, &ref) == nil {
			b, err := os.ReadFile(filepath.Join(dir, ref))
			if err != nil {
				return nil, fmt.Errorf("tableSchema %s: %w", ref, err)
			}
			schemaData = b
		}
		var ts struct {
			Columns []csvwColumn `json:"columns"`
		}
		if err := json.Unmarshal(schemaData, &ts); err != nil {
			return nil, fmt.Errorf("tableSchema: %w", err)
		}
		if len(ts.Columns) == 0 {
			return nil, fmt.Errorf("tableSchema has no columns")
		}
		out := &TableSchema{Kind: "csvw"}
		for _, c := range ts.Columns {
			if c.Virtual {
				continue
			}
			name := c.Name
			if name == "" {
				name = firstTitle(c.Titles)
			}
			if name == "" {
				return nil, fmt.Errorf("a column has neither name nor titles")
			}
			base, format := "string", ""
			if len(c.Datatype) > 0 {
				if json.Unmarshal(c.Datatype, &base) != nil {
					var obj struct {
						Base   string `json:"base"`
						Format string `json:"format"`
					}
					if err := json.Unmarshal(c.Datatype, &obj); err != nil {
						return nil, fmt.Errorf("column %q datatype: %w", name, err)
					}
					base, format = obj.Base, obj.Format
				}
			}
			ft, shape, err := sidecarFieldType(name, base, format)
			if err != nil {
				return nil, err
			}
			out.Fields = append(out.Fields, TableField{Name: name, Type: ft, Shape: shape})
		}
		return out, nil
	}
	return nil, errTableNotDescribed
}

// firstTitle: CSVW titles are a string, an array, or a language map.
func firstTitle(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
		return list[0]
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) == nil {
		for _, v := range m {
			return firstTitle(v)
		}
	}
	return ""
}

// ---- shared ---------------------------------------------------------------

// checkDialect refuses what the readers cannot honour (DFC146 §5): a file
// without a header row, and a delimiter other than the one the command
// reads (from tsv detects any single character from the header).
func checkDialect(delimiter string, header *bool) error {
	if header != nil && !*header {
		return fmt.Errorf("dialect says the file has no header row; ssql reads headed delimited files — add a header row")
	}
	if delimiter != "" && delimiter != "," && delimiter != "\t" {
		return fmt.Errorf("dialect delimiter %q: read the file with `from tsv`, which detects the delimiter from the header", delimiter)
	}
	return nil
}

// isoTimeFormats are the sidecar date formats the reader's fixed layouts
// cover (ParseTime); any other format is refused rather than leaving a
// `time` column as unparsed text.
var isoTimeFormats = map[string]bool{
	"": true, "default": true, "any": true,
	"yyyy-MM-dd": true, "yyyy-MM-ddTHH:mm:ss": true, "yyyy-MM-ddTHH:mm:ssZ": true, "yyyy-MM-dd HH:mm:ss": true,
	"%Y-%m-%d": true, "%Y-%m-%dT%H:%M:%S": true, "%Y-%m-%dT%H:%M:%SZ": true, "%Y-%m-%d %H:%M:%S": true,
}

// sidecarFieldType maps a sidecar type name (either standard's) to the
// ssql field type and, for json, the value's shape.
func sidecarFieldType(name, typeName, format string) (FieldType, string, error) {
	switch strings.ToLower(typeName) {
	case "integer", "int", "long", "short", "byte", "year":
		return FieldTypeInt, "", nil
	case "number", "decimal", "double", "float":
		return FieldTypeFloat, "", nil
	case "boolean", "bool":
		return FieldTypeBool, "", nil
	case "date", "datetime", "time", "timestamp":
		if !isoTimeFormats[format] {
			return 0, "", fmt.Errorf("field %q: date format %q is not supported (ssql reads ISO 8601 / RFC 3339 forms) — drop the format or read the column as text with -type %s string", name, format, name)
		}
		return FieldTypeTime, "", nil
	case "object", "geojson":
		return FieldTypeJSON, "object", nil
	case "array":
		return FieldTypeJSON, "array", nil
	case "json":
		return FieldTypeJSON, "", nil
	case "string", "", "yearmonth", "duration", "geopoint", "anyuri":
		return FieldTypeString, "", nil
	case "any":
		return FieldTypeAuto, "", nil
	}
	return 0, "", fmt.Errorf("field %q: unknown type %q (integer, number, boolean, date/datetime, string, object, array, any)", name, typeName)
}

// TableTypeName is the Frictionless type name a field is written as.
func TableTypeName(f TableField) string {
	switch f.Type {
	case FieldTypeInt:
		return "integer"
	case FieldTypeFloat:
		return "number"
	case FieldTypeBool:
		return "boolean"
	case FieldTypeTime:
		return "datetime"
	case FieldTypeJSON:
		if f.Shape != "" {
			return f.Shape
		}
		return "any"
	case FieldTypeString:
		return "string"
	}
	return "any"
}

// ---- writing --------------------------------------------------------------

// TableSchemaJSON renders fields as a Frictionless Table Schema document
// ({"fields": [{"name", "type"}, …]}, indented, trailing newline) — what
// `generate schema -datapackage` prints and a package's resource holds.
func TableSchemaJSON(fields []TableField) ([]byte, error) {
	list := make([]any, 0, len(fields))
	for _, f := range fields {
		list = append(list, map[string]any{"name": f.Name, "type": TableTypeName(f)})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"fields": list}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var packageNameClean = regexp.MustCompile(`[^a-z0-9._-]+`)

// WriteDatapackage writes a Frictionless datapackage.json beside csvPath
// describing it: one resource named after the file's stem, with the
// fields' types. An existing package in that directory is kept and the
// resource for this path replaced (or appended), so a directory of
// outputs accumulates one package. Written with a trailing newline.
func WriteDatapackage(csvPath string, fields []TableField) error {
	dir, base := filepath.Split(csvPath)
	pkgPath := filepath.Join(dir, "datapackage.json")

	pkg := map[string]any{}
	var resources []any
	if data, err := os.ReadFile(pkgPath); err == nil {
		if err := json.Unmarshal(data, &pkg); err != nil {
			return fmt.Errorf("%s exists but is not a JSON object: %w", pkgPath, err)
		}
		if rs, ok := pkg["resources"].([]any); ok {
			for _, r := range rs {
				if m, ok := r.(map[string]any); ok {
					if raw, _ := json.Marshal(m["path"]); pathNames(raw, base) {
						continue // replaced below
					}
				}
				resources = append(resources, r)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", pkgPath, err)
	}

	fieldList := make([]any, 0, len(fields))
	for _, f := range fields {
		fieldList = append(fieldList, map[string]any{"name": f.Name, "type": TableTypeName(f)})
	}
	format := "csv"
	if strings.EqualFold(filepath.Ext(base), ".tsv") {
		format = "tsv"
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	resources = append(resources, map[string]any{
		"name":     packageNameClean.ReplaceAllString(strings.ToLower(stem), "_"),
		"path":     base,
		"profile":  "tabular-data-resource",
		"format":   format,
		"encoding": "utf-8",
		"schema":   map[string]any{"fields": fieldList},
	})
	if _, ok := pkg["name"]; !ok {
		dirName := filepath.Base(filepath.Clean(dir))
		if dir == "" || dirName == "." || dirName == "/" {
			dirName = "data"
		}
		pkg["name"] = packageNameClean.ReplaceAllString(strings.ToLower(dirName), "_")
	}
	if _, ok := pkg["profile"]; !ok {
		pkg["profile"] = "tabular-data-package"
	}
	pkg["resources"] = resources

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(pkg); err != nil {
		return err
	}
	return os.WriteFile(pkgPath, buf.Bytes(), 0o644)
}

// TableFieldsFromRecords observes the records' field types as they
// pass: the type of a field is its first non-nil value's, widened int →
// float when both appear; a json value's shape is its first character.
// known seeds types the caller holds exactly (a schema header); order is
// the first-appearance order the CSV writer uses. The returned function
// yields the fields seen once the sequence has been consumed.
func TableFieldsFromRecords(records iter.Seq[Record], known map[string]FieldType) (iter.Seq[Record], func() []TableField) {
	var order []string
	seen := map[string]*TableField{}
	note := func(name string, v any) {
		f := seen[name]
		if f == nil {
			f = &TableField{Name: name, Type: FieldTypeAuto}
			if t, ok := known[name]; ok {
				f.Type = t
			}
			seen[name] = f
			order = append(order, name)
		}
		if v == nil {
			return
		}
		if f.Type == FieldTypeJSON && f.Shape == "" {
			if js, ok := v.(JSONString); ok {
				f.Shape = jsonShape(js)
			}
			return
		}
		if _, fixed := known[name]; fixed {
			return
		}
		switch x := v.(type) {
		case int64:
			if f.Type == FieldTypeAuto {
				f.Type = FieldTypeInt
			}
		case float64:
			if f.Type == FieldTypeAuto || f.Type == FieldTypeInt {
				f.Type = FieldTypeFloat
			}
		case bool:
			if f.Type == FieldTypeAuto {
				f.Type = FieldTypeBool
			}
		case time.Time:
			if f.Type == FieldTypeAuto {
				f.Type = FieldTypeTime
			}
		case JSONString:
			if f.Type == FieldTypeAuto {
				f.Type, f.Shape = FieldTypeJSON, jsonShape(x)
			}
		case string:
			if f.Type == FieldTypeAuto {
				f.Type = FieldTypeString
			}
		}
	}
	seq := func(yield func(Record) bool) {
		for r := range records {
			for k, v := range r.All() {
				note(k, v)
			}
			if !yield(r) {
				return
			}
		}
	}
	fields := func() []TableField {
		out := make([]TableField, 0, len(order))
		for _, name := range order {
			f := *seen[name]
			if f.Type == FieldTypeAuto {
				f.Type = FieldTypeString // never saw a value: text
			}
			out = append(out, f)
		}
		return out
	}
	return seq, fields
}

func jsonShape(js JSONString) string {
	switch t := strings.TrimSpace(string(js)); {
	case strings.HasPrefix(t, "{"):
		return "object"
	case strings.HasPrefix(t, "["):
		return "array"
	}
	return ""
}

// WriteCSVSidecar is WriteCSV followed by WriteDatapackage for the
// columns written, typed from the records (TableFieldsFromRecords; known
// seeds exact types). The CSV is written first; a package that cannot be
// written is an error after a complete data file.
func WriteCSVSidecar(records iter.Seq[Record], filename string, known map[string]FieldType, config ...CSVConfig) error {
	seq, fields := TableFieldsFromRecords(records, known)
	if err := WriteCSV(seq, filename, config...); err != nil {
		return err
	}
	cols := fields()
	if len(config) > 0 && config[0].Fields != nil {
		// The writer's column order is the configured one.
		byName := map[string]TableField{}
		for _, f := range cols {
			byName[f.Name] = f
		}
		ordered := make([]TableField, 0, len(config[0].Fields))
		for _, name := range config[0].Fields {
			f, ok := byName[name]
			if !ok {
				f = TableField{Name: name, Type: FieldTypeString}
				if t, known := known[name]; known {
					f.Type = t
				}
			}
			ordered = append(ordered, f)
		}
		cols = ordered
	}
	return WriteDatapackage(filename, cols)
}
