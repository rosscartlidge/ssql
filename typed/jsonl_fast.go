package typed

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// The positional JSONL decoder (typed-codegen roadmap §9, step 2).
//
// encoding/json reflects on every row; this plan reflects ONCE per type
// (like the CSV reader) to map each JSON key to a field offset and a
// fieldDecoder, then walks each line's bytes exactly once, writing
// values straight into the struct. Same fieldDecoder closures as the
// CSV reader — one set of field-parsing rules for both formats.
//
// Semantics for the supported field kinds (string, the integer and
// float kinds, bool, time.Time as RFC 3339, and pointers to those):
//
//   - an unknown key is skipped (any value shape);
//   - a missing key leaves the field at its zero value;
//   - JSON null leaves a plain field zero and sets a pointer field nil;
//   - a string field takes a JSON string's unescaped text, and a
//     number, bool, object or array as its raw JSON text (exec stores
//     nested values the same way; encoding/json would reject the row);
//   - a numeric field requires a JSON number, a bool field a JSON
//     bool, a time field a JSON string — anything else is a row error
//     (the lossy reader skips the row, the Safe reader yields it), as
//     with encoding/json.
//
// Keys match a field's `json` tag, else its `ssql`/`csv` tag, else its
// name — exactly first, then case-insensitively, like encoding/json.
// The `ssql` tag fallback is an extension over encoding/json (which
// ignores it): a struct written for the CSV reader (`ssql:"dept_id"`)
// reads a JSONL file with dept_id keys without a second set of tags.
// A type with a field kind this decoder cannot handle (slices, maps,
// nested structs, interfaces) falls back to encoding/json for every
// row, so nothing that worked before stops working.

type jsonKind uint8

const (
	jkString jsonKind = iota
	jkNumber
	jkBool
	jkTime
)

type jsonField struct {
	name  string
	dec   fieldDecoder
	kind  jsonKind
	isPtr bool
}

type jsonlPlan struct {
	exact map[string]*jsonField
	lower map[string]*jsonField
}

// jsonFieldName mirrors encoding/json's tag rule (`json:"name,omitempty"`,
// "-" excludes), falling back to the CSV column name.
func jsonFieldName(f reflect.StructField) (string, bool) {
	if tag, ok := f.Tag.Lookup("json"); ok {
		name := tag
		if i := strings.IndexByte(tag, ','); i >= 0 {
			name = tag[:i]
		}
		if name == "-" {
			return "", true
		}
		if name != "" {
			return name, false
		}
	}
	return columnName(f)
}

func jsonKindFor(t reflect.Type) (jsonKind, error) {
	if t == timeType {
		return jkTime, nil
	}
	switch t.Kind() {
	case reflect.String:
		return jkString, nil
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint64, reflect.Float32, reflect.Float64:
		return jkNumber, nil
	case reflect.Bool:
		return jkBool, nil
	}
	return 0, fmt.Errorf("typed: JSONL positional decoder has no rule for %v", t)
}

// buildJSONLPlan reflects over T once. An error means "use encoding/json".
func buildJSONLPlan[T any]() (*jsonlPlan, error) {
	var zero T
	rt := reflect.TypeOf(zero)
	if rt == nil || rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("typed: T must be a struct, got %v", rt)
	}
	pl := &jsonlPlan{exact: map[string]*jsonField{}, lower: map[string]*jsonField{}}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		name, skip := jsonFieldName(f)
		if skip {
			continue
		}
		ft := f.Type
		isPtr := ft.Kind() == reflect.Pointer
		if isPtr {
			ft = ft.Elem()
		}
		kind, err := jsonKindFor(ft)
		if err != nil {
			return nil, err
		}
		dec, err := decoderFor(f.Type, f.Offset)
		if err != nil {
			return nil, err
		}
		jf := &jsonField{name: name, dec: dec, kind: kind, isPtr: isPtr}
		if _, dup := pl.exact[name]; !dup {
			pl.exact[name] = jf
		}
		if lk := strings.ToLower(name); pl.lower[lk] == nil {
			pl.lower[lk] = jf
		}
	}
	return pl, nil
}

func (pl *jsonlPlan) lookup(key []byte) *jsonField {
	if f, ok := pl.exact[string(key)]; ok { // no alloc: map lookup by []byte→string conversion is optimised
		return f
	}
	// Case-insensitive fallback, as encoding/json.
	return pl.lower[strings.ToLower(string(key))]
}

var errJSONSyntax = errors.New("invalid JSON")

// decode fills the struct at p from one JSONL line. p must point at a
// zero-valued T (the callers allocate `var row T` per line).
func (pl *jsonlPlan) decode(line []byte, p unsafe.Pointer) error {
	i := skipWS(line, 0)
	if i >= len(line) || line[i] != '{' {
		return errJSONSyntax
	}
	i = skipWS(line, i+1)
	if i < len(line) && line[i] == '}' {
		return trailing(line, i+1)
	}
	for {
		// key
		if i >= len(line) || line[i] != '"' {
			return errJSONSyntax
		}
		kStart, kEnd, kEsc, next, err := scanString(line, i)
		if err != nil {
			return err
		}
		i = skipWS(line, next)
		if i >= len(line) || line[i] != ':' {
			return errJSONSyntax
		}
		i = skipWS(line, i+1)
		if i >= len(line) {
			return errJSONSyntax
		}

		var key []byte
		if kEsc {
			k, uerr := strconv.Unquote(string(line[kStart-1 : kEnd+1]))
			if uerr != nil {
				return errJSONSyntax
			}
			key = []byte(k)
		} else {
			key = line[kStart:kEnd]
		}
		f := pl.lookup(key)

		// value
		vStart := i
		switch c := line[i]; {
		case c == '"':
			s, e, esc, next, err := scanString(line, i)
			if err != nil {
				return err
			}
			i = next
			if f != nil {
				switch f.kind {
				case jkString, jkTime:
					var text string
					if esc {
						u, uerr := strconv.Unquote(string(line[s-1 : e+1]))
						if uerr != nil {
							return errJSONSyntax
						}
						text = u
					} else {
						text = string(line[s:e])
					}
					if f.kind == jkTime && text == "" {
						// The CSV decoder reads an empty cell as the zero time;
						// in JSON an empty string is not a time (encoding/json
						// agrees) — null is how a missing time is spelled.
						return fmt.Errorf("field %q: cannot parse \"\" as RFC 3339 time", f.name)
					}
					if err := f.dec(p, text); err != nil {
						return fmt.Errorf("field %q: %w", f.name, err)
					}
				default:
					return fmt.Errorf("field %q: cannot unmarshal string into %s", f.name, kindName(f.kind))
				}
			}
		case c == 't' || c == 'f':
			lit := "true"
			if c == 'f' {
				lit = "false"
			}
			if len(line)-i < len(lit) || string(line[i:i+len(lit)]) != lit {
				return errJSONSyntax
			}
			i += len(lit)
			if f != nil {
				switch f.kind {
				case jkBool, jkString:
					if err := f.dec(p, lit); err != nil {
						return fmt.Errorf("field %q: %w", f.name, err)
					}
				default:
					return fmt.Errorf("field %q: cannot unmarshal bool into %s", f.name, kindName(f.kind))
				}
			}
		case c == 'n':
			if len(line)-i < 4 || string(line[i:i+4]) != "null" {
				return errJSONSyntax
			}
			i += 4
			if f != nil && f.isPtr {
				if err := f.dec(p, ""); err != nil { // pointer decoders take "" as nil
					return fmt.Errorf("field %q: %w", f.name, err)
				}
			}
		case c == '-' || (c >= '0' && c <= '9'):
			e := i + 1
			for e < len(line) {
				d := line[e]
				if (d >= '0' && d <= '9') || d == '.' || d == 'e' || d == 'E' || d == '+' || d == '-' {
					e++
					continue
				}
				break
			}
			num := line[i:e]
			i = e
			if f != nil {
				switch f.kind {
				case jkNumber:
					// The decoders parse immediately and never retain s, so a
					// zero-copy string view of the line is safe here.
					if err := f.dec(p, unsafe.String(&num[0], len(num))); err != nil {
						return fmt.Errorf("field %q: %w", f.name, err)
					}
				case jkString:
					if err := f.dec(p, string(num)); err != nil {
						return fmt.Errorf("field %q: %w", f.name, err)
					}
				default:
					return fmt.Errorf("field %q: cannot unmarshal number into %s", f.name, kindName(f.kind))
				}
			}
		case c == '{' || c == '[':
			e, err := skipNested(line, i)
			if err != nil {
				return err
			}
			i = e
			if f != nil {
				if f.kind != jkString {
					return fmt.Errorf("field %q: cannot unmarshal nested JSON into %s", f.name, kindName(f.kind))
				}
				if err := f.dec(p, string(line[vStart:e])); err != nil {
					return fmt.Errorf("field %q: %w", f.name, err)
				}
			}
		default:
			return errJSONSyntax
		}

		i = skipWS(line, i)
		if i >= len(line) {
			return errJSONSyntax
		}
		switch line[i] {
		case ',':
			i = skipWS(line, i+1)
		case '}':
			return trailing(line, i+1)
		default:
			return errJSONSyntax
		}
	}
}

func kindName(k jsonKind) string {
	switch k {
	case jkNumber:
		return "a numeric field"
	case jkBool:
		return "a bool field"
	case jkTime:
		return "a time.Time field"
	}
	return "a string field"
}

func skipWS(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// trailing accepts only whitespace after the closing brace.
func trailing(b []byte, i int) error {
	if skipWS(b, i) != len(b) {
		return errJSONSyntax
	}
	return nil
}

// scanString scans a JSON string starting at the opening quote at b[i].
// Returns the content bounds (exclusive of quotes), whether it contains
// escapes, and the index after the closing quote.
func scanString(b []byte, i int) (start, end int, escaped bool, next int, err error) {
	start = i + 1
	j := start
	for j < len(b) {
		switch b[j] {
		case '"':
			return start, j, escaped, j + 1, nil
		case '\\':
			escaped = true
			j += 2
			continue
		}
		j++
	}
	return 0, 0, false, 0, errJSONSyntax
}

// skipNested skips a balanced object or array starting at b[i],
// honouring strings. Returns the index after it.
func skipNested(b []byte, i int) (int, error) {
	depth := 0
	for i < len(b) {
		switch b[i] {
		case '"':
			_, _, _, next, err := scanString(b, i)
			if err != nil {
				return 0, err
			}
			i = next
			continue
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1, nil
			}
		}
		i++
	}
	return 0, errJSONSyntax
}

// timeTypeCheck keeps the time import honest for jsonKindFor.
var _ = time.RFC3339
