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

// SampleJSONLSchema infers the typed row struct for a JSONL file so
// `from jsonl FILE` has a typed form (typed-codegen roadmap item 9;
// before it, a typed compile of any JSONL pipeline fell back to record
// mode for the WHOLE program — 14.8 s and 3.9 GB for a 3M-row
// group-by that the typed CSV path does in 0.27 s).
//
// Two sources of truth, in order:
//
//  1. A leading `_schema` header line (a tee'd or ssql-written file):
//     its field order and wire types are authoritative — no sampling.
//  2. Otherwise the first maxRows lines (DefaultInferRows when zero),
//     parsed with the same fast line parser exec uses; fields are in
//     JSON key order (new keys appended as first seen) and typed by
//     the narrowest Go type every non-null value fits: int64 →
//     float64 → bool → string. Nested objects and arrays parse as JSON
//     text and are string fields. A key that is null throughout the
//     sample is still a column — a string field.
//
// A JSON array file (first non-space byte `[`) has no typed form and
// returns an error; the caller falls back to record codegen.
func SampleJSONLSchema(filename, typeName string, maxRows int) (*TypedSchema, string, error) {
	if maxRows <= 0 {
		maxRows = ssql.DefaultInferRows
	}
	if typeName == "" {
		typeName = TypeNameFromFilename(filename)
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, "", fmt.Errorf("typed schema sample: %w", err)
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 1<<20)
	// Peek past leading whitespace: a JSON array is not JSONL.
	for {
		b, err := br.Peek(1)
		if err != nil {
			return nil, "", fmt.Errorf("typed schema sample: %s is empty", filename)
		}
		if b[0] == ' ' || b[0] == '\t' || b[0] == '\n' || b[0] == '\r' {
			br.ReadByte()
			continue
		}
		if b[0] == '[' {
			return nil, "", fmt.Errorf("typed schema sample: %s is a JSON array, not JSONL (no typed form)", filename)
		}
		break
	}

	type colInfer struct {
		seen, allInt, allNum, allBool bool
	}
	var order []string
	infer := map[string]*colInfer{}
	rows := 0
	for rows < maxRows {
		line, err := br.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			break
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if err != nil {
				break
			}
			continue
		}
		// The header is authoritative when present (only ever the first line).
		if rows == 0 && bytes.HasPrefix(line, []byte(`{"_schema"`)) {
			var hdr map[string]any
			if jerr := json.Unmarshal(line, &hdr); jerr == nil {
				if s, ok := ParseSchemaHeader(hdr); ok {
					return TypedSchemaFromHeader(s, typeName)
				}
			}
		}
		mut, perr := ssql.ParseJSONLine(line)
		if perr != nil {
			if err != nil {
				break
			}
			continue // a malformed line carries no type information
		}
		rows++
		rec := mut.Freeze()
		// Field order is the JSON key order of the line (a Record
		// iterates its schema alphabetically, which would reorder the
		// user's columns); new keys are appended as first seen.
		for _, k := range topLevelJSONKeys(line) {
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
			default:
				c.allInt, c.allNum, c.allBool = false, false, false
			}
			if _, isBool := v.(bool); !isBool {
				c.allBool = false
			}
		}
		if err == io.EOF {
			break
		}
	}
	if rows == 0 {
		return nil, "", fmt.Errorf("typed schema sample: %s has no JSON rows", filename)
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
		gn := goNameFromColumn(name)
		if usedNames[gn] > 0 {
			usedNames[gn]++
			gn = fmt.Sprintf("%s%d", gn, usedNames[gn])
		} else {
			usedNames[gn] = 1
		}
		fields = append(fields, TypedSchemaField{Name: name, GoName: gn, GoType: goType})
	}
	schema := &TypedSchema{TypeName: typeName, Fields: fields}
	return schema, RenderStructDef(schema), nil
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
