// JSON parser and serializer for the WASM module.
// Manual recursive-descent parser — no encoding/json dependency.
// Follows patterns from ssql core.go ParseJSONLine.
package main

import (
	"fmt"
	"strconv"
	"strings"
)

// parseJSONArray parses a JSON array of objects into a dataset.
// The first object defines the schema; subsequent objects share it.
func parseJSONArray(data string) (dataset, error) {
	b := []byte(data)
	pos := 0
	n := len(b)

	pos = skipWS(b, pos, n)
	if pos >= n {
		return dataset{s: newSchema(nil)}, nil
	}

	if b[pos] != '[' {
		return dataset{}, fmt.Errorf("expected '[' at position %d", pos)
	}
	pos++

	var s *schema
	var rows []record

	for {
		pos = skipWS(b, pos, n)
		if pos >= n {
			return dataset{}, fmt.Errorf("unexpected end of JSON array")
		}
		if b[pos] == ']' {
			break
		}

		// Parse one object
		if b[pos] != '{' {
			return dataset{}, fmt.Errorf("expected '{' at position %d", pos)
		}

		fields, vals, newPos, err := parseObject(b, pos, n)
		if err != nil {
			return dataset{}, err
		}
		pos = newPos

		if s == nil {
			// First object defines the schema
			s = newSchema(fields)
			values := make([]any, len(fields))
			copy(values, vals)
			rows = append(rows, record{s: s, values: values})
		} else {
			// Subsequent objects: map fields to schema positions.
			// If this object has new fields not in schema, expand it.
			needExpand := false
			for _, f := range fields {
				if _, ok := s.index[f]; !ok {
					needExpand = true
					break
				}
			}
			if needExpand {
				// Expand schema with new fields
				newFields := make([]string, len(s.fields))
				copy(newFields, s.fields)
				for _, f := range fields {
					if _, ok := s.index[f]; !ok {
						newFields = append(newFields, f)
					}
				}
				newSchema := newSchema(newFields)
				// Re-index existing rows
				for i, r := range rows {
					nv := make([]any, len(newFields))
					copy(nv, r.values)
					rows[i] = record{s: newSchema, values: nv}
				}
				s = newSchema
			}

			values := make([]any, len(s.fields))
			for i, f := range fields {
				if idx, ok := s.index[f]; ok {
					values[idx] = vals[i]
				}
			}
			rows = append(rows, record{s: s, values: values})
		}

		pos = skipWS(b, pos, n)
		if pos < n && b[pos] == ',' {
			pos++
			continue
		}
	}

	if s == nil {
		s = newSchema(nil)
	}
	return dataset{s: s, rows: rows}, nil
}

// parseObject parses a JSON object, returning field names and values in order.
func parseObject(b []byte, pos, n int) ([]string, []any, int, error) {
	// pos is at '{'
	pos++

	var fields []string
	var vals []any

	for {
		pos = skipWS(b, pos, n)
		if pos >= n {
			return nil, nil, pos, fmt.Errorf("unexpected end of JSON object")
		}
		if b[pos] == '}' {
			pos++
			return fields, vals, pos, nil
		}

		// Field name
		if b[pos] != '"' {
			return nil, nil, pos, fmt.Errorf("expected '\"' for field name at position %d", pos)
		}
		fieldName, newPos, err := parseString(b, pos, n)
		if err != nil {
			return nil, nil, pos, err
		}
		pos = newPos

		pos = skipWS(b, pos, n)
		if pos >= n || b[pos] != ':' {
			return nil, nil, pos, fmt.Errorf("expected ':' after field name at position %d", pos)
		}
		pos++

		pos = skipWS(b, pos, n)

		// Value
		val, newPos2, err := parseValue(b, pos, n)
		if err != nil {
			return nil, nil, pos, err
		}
		pos = newPos2

		fields = append(fields, fieldName)
		vals = append(vals, val)

		pos = skipWS(b, pos, n)
		if pos >= n {
			return nil, nil, pos, fmt.Errorf("unexpected end of JSON object")
		}
		if b[pos] == ',' {
			pos++
			continue
		}
		if b[pos] == '}' {
			pos++
			return fields, vals, pos, nil
		}
		return nil, nil, pos, fmt.Errorf("expected ',' or '}' at position %d, got '%c'", pos, b[pos])
	}
}

// parseValue parses any JSON value.
func parseValue(b []byte, pos, n int) (any, int, error) {
	if pos >= n {
		return nil, pos, fmt.Errorf("unexpected end of JSON value")
	}

	c := b[pos]
	switch {
	case c == '"':
		s, newPos, err := parseString(b, pos, n)
		return s, newPos, err

	case c == '-' || (c >= '0' && c <= '9'):
		return parseNumber(b, pos, n)

	case c == 't':
		if pos+4 <= n && string(b[pos:pos+4]) == "true" {
			return true, pos + 4, nil
		}
		return nil, pos, fmt.Errorf("invalid JSON value at position %d", pos)

	case c == 'f':
		if pos+5 <= n && string(b[pos:pos+5]) == "false" {
			return false, pos + 5, nil
		}
		return nil, pos, fmt.Errorf("invalid JSON value at position %d", pos)

	case c == 'n':
		if pos+4 <= n && string(b[pos:pos+4]) == "null" {
			return nil, pos + 4, nil
		}
		return nil, pos, fmt.Errorf("invalid JSON value at position %d", pos)

	case c == '[' || c == '{':
		return parseRaw(b, pos, n)

	default:
		return nil, pos, fmt.Errorf("unexpected character '%c' at position %d", c, pos)
	}
}

// parseString parses a JSON string starting at the opening '"'.
func parseString(b []byte, pos, n int) (string, int, error) {
	if pos >= n || b[pos] != '"' {
		return "", pos, fmt.Errorf("expected '\"' at position %d", pos)
	}
	pos++ // skip opening quote

	// Fast path: no escapes
	start := pos
	for pos < n {
		c := b[pos]
		if c == '"' {
			return string(b[start:pos]), pos + 1, nil
		}
		if c == '\\' {
			break
		}
		pos++
	}

	// Slow path: handle escapes
	var buf []byte
	buf = append(buf, b[start:pos]...)
	for pos < n {
		c := b[pos]
		if c == '"' {
			return string(buf), pos + 1, nil
		}
		if c == '\\' {
			pos++
			if pos >= n {
				return "", pos, fmt.Errorf("unexpected end of string escape")
			}
			switch b[pos] {
			case '"', '\\', '/':
				buf = append(buf, b[pos])
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'u':
				if pos+4 >= n {
					return "", pos, fmt.Errorf("invalid unicode escape")
				}
				r, err := strconv.ParseUint(string(b[pos+1:pos+5]), 16, 16)
				if err != nil {
					return "", pos, fmt.Errorf("invalid unicode escape: %w", err)
				}
				buf = append(buf, string(rune(r))...)
				pos += 4
			default:
				return "", pos, fmt.Errorf("invalid escape character '\\%c'", b[pos])
			}
			pos++
		} else {
			buf = append(buf, c)
			pos++
		}
	}
	return "", pos, fmt.Errorf("unterminated string")
}

// parseNumber parses a JSON number. Returns int64 or float64.
func parseNumber(b []byte, pos, n int) (any, int, error) {
	start := pos

	if pos < n && b[pos] == '-' {
		pos++
	}
	if pos >= n {
		return nil, pos, fmt.Errorf("invalid number at position %d", start)
	}
	if b[pos] == '0' {
		pos++
	} else if b[pos] >= '1' && b[pos] <= '9' {
		for pos < n && b[pos] >= '0' && b[pos] <= '9' {
			pos++
		}
	} else {
		return nil, pos, fmt.Errorf("invalid number at position %d", start)
	}

	isFloat := false
	if pos < n && b[pos] == '.' {
		isFloat = true
		pos++
		if pos >= n || b[pos] < '0' || b[pos] > '9' {
			return nil, pos, fmt.Errorf("invalid number: expected digit after decimal at position %d", pos)
		}
		for pos < n && b[pos] >= '0' && b[pos] <= '9' {
			pos++
		}
	}
	if pos < n && (b[pos] == 'e' || b[pos] == 'E') {
		isFloat = true
		pos++
		if pos < n && (b[pos] == '+' || b[pos] == '-') {
			pos++
		}
		if pos >= n || b[pos] < '0' || b[pos] > '9' {
			return nil, pos, fmt.Errorf("invalid number: expected digit in exponent at position %d", pos)
		}
		for pos < n && b[pos] >= '0' && b[pos] <= '9' {
			pos++
		}
	}

	numStr := string(b[start:pos])
	if isFloat {
		f, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return nil, pos, fmt.Errorf("invalid float: %w", err)
		}
		return f, pos, nil
	}

	i, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		// Overflow → float64
		f, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return nil, pos, fmt.Errorf("invalid number: %w", err)
		}
		return f, pos, nil
	}
	return i, pos, nil
}

// parseRaw captures a nested JSON array or object as rawJSON.
func parseRaw(b []byte, pos, n int) (rawJSON, int, error) {
	if pos >= n {
		return "", pos, fmt.Errorf("unexpected end of JSON")
	}
	start := pos
	c := b[pos]
	if c != '[' && c != '{' {
		return "", pos, fmt.Errorf("expected '[' or '{' at position %d", pos)
	}

	depth := 1
	pos++
	inString := false

	for pos < n && depth > 0 {
		ch := b[pos]
		if inString {
			if ch == '\\' && pos+1 < n {
				pos += 2
				continue
			}
			if ch == '"' {
				inString = false
			}
			pos++
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
		pos++
	}

	if depth != 0 {
		return "", pos, fmt.Errorf("unterminated nested value at position %d", start)
	}
	return rawJSON(b[start:pos]), pos, nil
}

// skipWS advances pos past whitespace.
func skipWS(b []byte, pos, n int) int {
	for pos < n && (b[pos] == ' ' || b[pos] == '\t' || b[pos] == '\n' || b[pos] == '\r') {
		pos++
	}
	return pos
}

// ============================================================================
// Serializer
// ============================================================================

// toJSON serializes a dataset to a JSON array string.
func (ds dataset) toJSON() string {
	var buf strings.Builder
	buf.WriteByte('[')
	for i, r := range ds.rows {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('{')
		first := true
		for j, f := range r.s.fields {
			v := r.values[j]
			if v == nil {
				continue // skip nil values (matches ssql behavior)
			}
			if !first {
				buf.WriteByte(',')
			}
			first = false
			appendJSONString(&buf, f)
			buf.WriteByte(':')
			appendJSONValue(&buf, v)
		}
		buf.WriteByte('}')
	}
	buf.WriteByte(']')
	return buf.String()
}

// appendJSONValue writes a JSON value to buf.
func appendJSONValue(buf *strings.Builder, v any) {
	switch val := v.(type) {
	case string:
		appendJSONString(buf, val)
	case int64:
		buf.WriteString(strconv.FormatInt(val, 10))
	case float64:
		buf.WriteString(strconv.FormatFloat(val, 'f', -1, 64))
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case rawJSON:
		buf.WriteString(string(val))
	default:
		buf.WriteString("null")
	}
}

// appendJSONString writes a JSON-escaped string (with quotes) to buf.
func appendJSONString(buf *strings.Builder, s string) {
	buf.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, c)
			} else {
				buf.WriteByte(c)
			}
		}
	}
	buf.WriteByte('"')
}

// ============================================================================
// Helpers
// ============================================================================

// errorJSON returns a JSON error object string.
func errorJSON(msg string) string {
	var buf strings.Builder
	buf.WriteString(`{"error":`)
	appendJSONString(&buf, msg)
	buf.WriteByte('}')
	return buf.String()
}

// aggSpec describes one aggregation in a group_by_multi operation.
type aggSpec struct {
	Field string
	Func  string
	Alias string
}

// pipelineOp describes one operation in a pipeline.
type pipelineOp struct {
	Op         string
	Field      string
	Operator   string
	Value      string
	Desc       bool
	GroupField string
	AggField   string
	AggFunc    string
	N          int
	Offset     int
	// group_by_multi
	GroupFields []string
	Aggs        []aggSpec
	// compute
	Name string
	Expr string
	// pivot
	RowField string
	ColField string
	ValField string
}

// parsePipelineOps parses a JSON array of pipeline operation objects.
func parsePipelineOps(jsonStr string) ([]pipelineOp, error) {
	b := []byte(jsonStr)
	pos := 0
	n := len(b)

	pos = skipWS(b, pos, n)
	if pos >= n || b[pos] != '[' {
		return nil, fmt.Errorf("expected '[' at position %d", pos)
	}
	pos++

	var ops []pipelineOp

	for {
		pos = skipWS(b, pos, n)
		if pos >= n {
			return nil, fmt.Errorf("unexpected end of pipeline JSON")
		}
		if b[pos] == ']' {
			break
		}

		if b[pos] != '{' {
			return nil, fmt.Errorf("expected '{' at position %d", pos)
		}

		fields, vals, newPos, err := parseObject(b, pos, n)
		if err != nil {
			return nil, err
		}
		pos = newPos

		op := pipelineOp{}
		for i, f := range fields {
			switch f {
			case "op":
				op.Op, _ = vals[i].(string)
			case "field":
				op.Field, _ = vals[i].(string)
			case "operator":
				op.Operator, _ = vals[i].(string)
			case "value":
				op.Value = formatValue(vals[i])
			case "desc":
				op.Desc, _ = vals[i].(bool)
			case "groupField":
				op.GroupField, _ = vals[i].(string)
			case "aggField":
				op.AggField, _ = vals[i].(string)
			case "aggFunc":
				op.AggFunc, _ = vals[i].(string)
			case "n":
				op.N = toInt(vals[i])
			case "offset":
				op.Offset = toInt(vals[i])
			case "groupFields":
				op.GroupFields = parseStringArray(vals[i])
			case "aggs":
				op.Aggs = parseAggSpecs(vals[i])
			case "name":
				op.Name, _ = vals[i].(string)
			case "expr":
				op.Expr, _ = vals[i].(string)
			case "rowField":
				op.RowField, _ = vals[i].(string)
			case "colField":
				op.ColField, _ = vals[i].(string)
			case "valField":
				op.ValField, _ = vals[i].(string)
			}
		}
		ops = append(ops, op)

		pos = skipWS(b, pos, n)
		if pos < n && b[pos] == ',' {
			pos++
			continue
		}
	}

	return ops, nil
}

// parseStringArray extracts a []string from a rawJSON array value.
func parseStringArray(v any) []string {
	raw, ok := v.(rawJSON)
	if !ok {
		return nil
	}
	b := []byte(string(raw))
	pos := 0
	n := len(b)
	pos = skipWS(b, pos, n)
	if pos >= n || b[pos] != '[' {
		return nil
	}
	pos++
	var result []string
	for {
		pos = skipWS(b, pos, n)
		if pos >= n {
			return result
		}
		if b[pos] == ']' {
			return result
		}
		if b[pos] == '"' {
			s, newPos, err := parseString(b, pos, n)
			if err != nil {
				return result
			}
			result = append(result, s)
			pos = newPos
		} else {
			pos++
			continue
		}
		pos = skipWS(b, pos, n)
		if pos < n && b[pos] == ',' {
			pos++
		}
	}
}

// parseAggSpecs extracts []aggSpec from a rawJSON array of objects.
func parseAggSpecs(v any) []aggSpec {
	raw, ok := v.(rawJSON)
	if !ok {
		return nil
	}
	b := []byte(string(raw))
	pos := 0
	n := len(b)
	pos = skipWS(b, pos, n)
	if pos >= n || b[pos] != '[' {
		return nil
	}
	pos++
	var result []aggSpec
	for {
		pos = skipWS(b, pos, n)
		if pos >= n {
			return result
		}
		if b[pos] == ']' {
			return result
		}
		if b[pos] != '{' {
			pos++
			continue
		}
		fields, vals, newPos, err := parseObject(b, pos, n)
		if err != nil {
			return result
		}
		pos = newPos
		spec := aggSpec{}
		for i, f := range fields {
			switch f {
			case "field":
				spec.Field, _ = vals[i].(string)
			case "func":
				spec.Func, _ = vals[i].(string)
			case "alias":
				spec.Alias, _ = vals[i].(string)
			}
		}
		if spec.Alias == "" {
			spec.Alias = spec.Func
		}
		result = append(result, spec)
		pos = skipWS(b, pos, n)
		if pos < n && b[pos] == ',' {
			pos++
		}
	}
}
