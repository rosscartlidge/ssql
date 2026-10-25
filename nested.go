package ssql

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// A nested value (a JSON array or object) is one thing in a Record: a
// JSONString holding its text (DFC144 Level 0). These two functions are
// the boundary between that text and the Go values the expression
// language works on (DFC144 Level 1): ExprValue opens a JSONString for an
// expression, JSONValue closes an expression's list or map result back
// into one.

// ExprValue is the value an expression sees for a record field: a
// JSONString parsed into []any / map[string]any (whole numbers as int64,
// as the readers type them), anything else as it is. Text that does not
// parse is returned unchanged.
func ExprValue(v any) any {
	js, ok := v.(JSONString)
	if !ok {
		return v
	}
	if js == "" {
		return nil // a typed program's missing nested field
	}
	// Numbers are decoded as their text (UseNumber), not as float64: an
	// integer literal becomes the exact int64 it is, where encoding/json's
	// float64 had already lost every digit past 2^53 (until v4.123.0 a
	// list element 9007199254740993 exploded to 9007199254740992, and
	// one past 2^53 came out a float in scientific notation).
	dec := json.NewDecoder(strings.NewReader(string(js)))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return v
	}
	return normaliseJSONNumbers(parsed)
}

// normaliseJSONNumbers turns the decoder's json.Number values into the
// scalars the readers type: an integer literal that fits is an int64,
// anything else a float64, recursively, so `scores[0]` on [10,20] is the
// int 10 the CSV reader would have produced. A float64 (a value built in
// Go rather than decoded) that is whole and within 2^53 is an int64 too.
func normaliseJSONNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		text := string(x)
		if !strings.ContainsAny(text, ".eE") {
			if i, err := strconv.ParseInt(text, 10, 64); err == nil {
				return i
			}
			if w, err := ParseInt128(text); err == nil {
				return w // exact past int64 (DFC155 C)
			}
		}
		f, err := x.Float64()
		if err != nil {
			return text // not a number Go can hold: keep the text
		}
		return f
	case float64:
		if x == float64(int64(x)) && x >= -9.007199254740992e15 && x <= 9.007199254740992e15 {
			return int64(x)
		}
		return x
	case []any:
		for i := range x {
			x[i] = normaliseJSONNumbers(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = normaliseJSONNumbers(x[k])
		}
		return x
	}
	return v
}

// NestedValue reports whether v is a non-scalar expression result — any
// slice or map (expr-lang returns []string from split, []any from
// filter/map, map[string]any from a map literal), or a JSONString — and
// returns it as the one representation, JSON text. A string, a []byte
// and the scalars are not nested.
func NestedValue(v any) (JSONString, bool) {
	switch x := v.(type) {
	case JSONString:
		return x, true
	case nil, string, []byte, int64, float64, bool:
		return "", false
	}
	switch reflect.TypeOf(v).Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return JSONValue(v), true
	}
	return "", false
}

// JSONValue is the JSONString for a non-scalar expression result (a
// []any, a map[string]any, or a JSONString already). A value that cannot
// be marshalled is a programming error and panics with an error value.
func JSONValue(v any) JSONString {
	if js, ok := v.(JSONString); ok {
		return js
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Errorf("json value: %w", err))
	}
	return JSONString(b)
}

// SynthesizeDottedFields makes a column whose NAME is a dotted path —
// items.qty after `flatten items`, addr.city after an include — reachable
// in an expression as items.qty: for every such column whose head is not
// itself a field, env[head] becomes a map (nested for a.b.c) of those
// columns. Only heads the expression names are built when identifiers is
// given; nil builds them all (the group-by batch environment, whose
// values are per-field arrays).
func SynthesizeDottedFields(env map[string]any, identifiers []string) {
	var want map[string]bool
	if identifiers != nil {
		want = make(map[string]bool, len(identifiers))
		for _, id := range identifiers {
			want[id] = true
		}
	}
	for name, v := range env {
		i := strings.IndexByte(name, '.')
		if i <= 0 {
			continue
		}
		head := name[:i]
		if want != nil && !want[head] {
			continue
		}
		if existing, ok := env[head]; ok {
			if _, isMap := existing.(map[string]any); !isMap {
				continue // a real field named head wins
			}
		}
		m, _ := env[head].(map[string]any)
		if m == nil {
			m = map[string]any{}
			env[head] = m
		}
		rest := strings.Split(name[i+1:], ".")
		for len(rest) > 1 {
			sub, _ := m[rest[0]].(map[string]any)
			if sub == nil {
				sub = map[string]any{}
				m[rest[0]] = sub
			}
			m, rest = sub, rest[1:]
		}
		m[rest[0]] = v
	}
}
