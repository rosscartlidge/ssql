package commands

import (
	"io"
	"iter"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// writeJSONLTypedFromFirstRow writes records under schema, first setting
// each named column's wire type from its value in the FIRST row. An
// expression result keeps the VM's type — group-by's `-expr` and
// `-stream-expr`, window's `-stream-expr`: an int fold is int64, exact
// past 2^53; `string(...)` a string — and the header must say so, because
// the next stage reads a column by its declared type and "float" would
// round a big integer at the pipe boundary (Ross's Fibonacci fold,
// 2026-10-21). A column absent (nil) in the first row keeps the
// placeholder type. A nil schema writes records with no header, as before.
func writeJSONLTypedFromFirstRow(w io.Writer, schema *lib.Schema, names []string, records iter.Seq[ssql.Record]) error {
	if schema == nil || len(names) == 0 {
		return lib.WriteJSONLWithSchema(w, schema, records)
	}
	next, stop := iter.Pull(records)
	defer stop()
	first, ok := next()
	if !ok {
		return lib.WriteJSONLWithSchema(w, schema, func(func(ssql.Record) bool) {})
	}
	typed := make(map[string]bool, len(names))
	for _, name := range names {
		typed[name] = true
	}
	for k, v := range first.All() {
		if typed[k] && v != nil {
			schema.SetType(k, lib.InferTypeString(v))
		}
	}
	return lib.WriteJSONLWithSchema(w, schema, func(yield func(ssql.Record) bool) {
		if !yield(first) {
			return
		}
		for {
			r, ok := next()
			if !ok || !yield(r) {
				return
			}
		}
	})
}
