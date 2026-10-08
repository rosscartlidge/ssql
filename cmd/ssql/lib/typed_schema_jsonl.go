package lib

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/rosscartlidge/ssql/v4"
)

// SampleJSONLSchema is SampleJSONSchema without the shape flag, kept for
// callers that know their file is JSON Lines (a JSON array file is
// accepted too; the reader to pair with the schema is then ReadJSON).
func SampleJSONLSchema(filename, typeName string, maxRows int, opts ...TypeOptions) (*TypedSchema, string, error) {
	schema, def, _, err := SampleJSONSchema(filename, typeName, maxRows, opts...)
	return schema, def, err
}

// SampleJSONSchema infers the typed row struct for a JSON source so
// `from json|jsonl FILE` can stay typed: the `_schema` header when the
// lines carry one (authoritative), else a sample of objects — the first
// maxRows (0 → ssql.DefaultInferRows) lines of a JSON Lines file, or the
// first maxRows elements of a JSON ARRAY file (`[{…},{…}]`, told apart
// by the first non-blank byte; isArray reports which, so the caller
// pairs the schema with typed.ReadJSON rather than typed.ReadJSONL).
// Types: all int → int64, numeric → float64, all bool → bool, else
// string (a field never seen non-null is string); a nested array or
// object is a string holding its text, flagged JSON. Field order is the
// key order of the first object that has each key — a Record iterates
// alphabetically, which would reorder the user's columns. opts override
// per field as for CSV (`-type FIELD TYPE`).
func SampleJSONSchema(filename, typeName string, maxRows int, opts ...TypeOptions) (schema *TypedSchema, structDef string, isArray bool, err error) {
	o := firstTypeOptions(opts)
	if maxRows <= 0 {
		maxRows = ssql.DefaultInferRows
	}
	if typeName == "" {
		typeName = TypeNameFromFilename(filename)
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, "", false, fmt.Errorf("typed schema sample: %w", err)
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 1<<20)
	// Peek past leading whitespace: a JSON array is sampled by element.
	for {
		b, perr := br.Peek(1)
		if perr != nil {
			return nil, "", false, fmt.Errorf("typed schema sample: %s is empty", filename)
		}
		if b[0] == ' ' || b[0] == '\t' || b[0] == '\n' || b[0] == '\r' {
			br.ReadByte()
			continue
		}
		isArray = b[0] == '['
		break
	}

	type colInfer struct {
		seen, allInt, allNum, allBool, json bool
	}
	var order []string
	infer := map[string]*colInfer{}
	rows := 0
	// observe folds one object's keys and values into the inference;
	// false when the bytes are not a JSON object (no type information).
	observe := func(obj []byte) bool {
		mut, perr := ssql.ParseJSONLine(obj)
		if perr != nil {
			return false
		}
		rows++
		rec := mut.Freeze()
		for _, k := range topLevelJSONKeys(obj) {
			if _, ok := infer[k]; !ok {
				infer[k] = &colInfer{allInt: true, allNum: true, allBool: true}
				order = append(order, k)
			}
		}
		for k, v := range rec.All() {
			c, ok := infer[k]
			if !ok {
				c = &colInfer{allInt: true, allNum: true, allBool: true}
				infer[k] = c
				order = append(order, k)
			}
			if v == nil {
				continue
			}
			c.seen = true
			switch v.(type) {
			case int64:
			case float64:
				c.allInt = false
			case bool:
				c.allInt, c.allNum = false, false
			case ssql.JSONString:
				c.allInt, c.allNum, c.allBool = false, false, false
				c.json = true
			default:
				c.allInt, c.allNum, c.allBool = false, false, false
			}
			if _, isBool := v.(bool); !isBool {
				c.allBool = false
			}
		}
		return true
	}

	if isArray {
		dec := json.NewDecoder(br)
		if _, derr := dec.Token(); derr != nil { // the opening '['
			return nil, "", true, fmt.Errorf("typed schema sample: %s: %w", filename, derr)
		}
		for rows < maxRows && dec.More() {
			var raw json.RawMessage
			if derr := dec.Decode(&raw); derr != nil {
				return nil, "", true, fmt.Errorf("typed schema sample: %s: element %d: %w", filename, rows+1, derr)
			}
			observe(raw)
		}
	} else {
		for rows < maxRows {
			line, rerr := br.ReadBytes('\n')
			if len(line) == 0 && rerr != nil {
				break
			}
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				if rerr != nil {
					break
				}
				continue
			}
			// The header is authoritative when present (only ever the first line).
			if rows == 0 && bytes.HasPrefix(line, []byte(`{"_schema"`)) {
				var hdr map[string]any
				if jerr := json.Unmarshal(line, &hdr); jerr == nil {
					if s, ok := ParseSchemaHeader(hdr); ok {
						schema, def, herr := TypedSchemaFromHeader(s, typeName)
						if herr == nil && (len(o.Fields) > 0 || o.Default != "") {
							for i := range schema.Fields {
								if t, ok := o.goTypeFor(schema.Fields[i].Name); ok {
									schema.Fields[i].GoType = t
								}
							}
							def = RenderStructDef(schema)
						}
						return schema, def, false, herr
					}
				}
			}
			observe(line) // a malformed line carries no type information
			if rerr == io.EOF {
				break
			}
		}
	}
	if rows == 0 {
		return nil, "", isArray, fmt.Errorf("typed schema sample: %s has no JSON rows", filename)
	}

	fields := make([]TypedSchemaField, 0, len(order))
	usedNames := make(map[string]int, len(order))
	for _, name := range order {
		c := infer[name]
		goType := "string"
		switch {
		case !c.seen:
			goType = "string"
		case c.allInt:
			goType = "int64"
		case c.allNum:
			goType = "float64"
		case c.allBool:
			goType = "bool"
		}
		if t, ok := o.goTypeFor(name); ok {
			goType = t
		}
		gn := goNameFromColumn(name)
		if usedNames[gn] > 0 {
			usedNames[gn]++
			gn = fmt.Sprintf("%s%d", gn, usedNames[gn])
		} else {
			usedNames[gn] = 1
		}
		fields = append(fields, TypedSchemaField{Name: name, GoName: gn, GoType: goType, JSON: c.json && goType == "string"})
	}
	schema = &TypedSchema{TypeName: typeName, Fields: fields}
	return schema, RenderStructDef(schema), isArray, nil
}

// topLevelJSONKeys returns an object line's top-level keys in document
// order (encoding/json's Decoder tokens at depth 1); nested keys and
// malformed lines contribute nothing.
func topLevelJSONKeys(line []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(line))
	var keys []string
	depth := 0
	expectKey := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return keys
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				depth++
				expectKey = depth == 1
			case '}':
				depth--
				expectKey = depth == 1
			case '[':
				depth++
				expectKey = false
			case ']':
				depth--
				expectKey = depth == 1
			}
		case string:
			if depth == 1 && expectKey {
				keys = append(keys, t)
				expectKey = false
			} else if depth == 1 {
				expectKey = true // this was a value; the next string is a key
			}
		default:
			if depth == 1 {
				expectKey = true
			}
		}
	}
}
