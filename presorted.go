package ssql

import (
	"fmt"
	"strings"
)

// presortedGuard enforces the contract of -presorted on a streaming
// stage (StreamGroupByFields, StreamWindow; typed.GroupByOrdered has its
// own): the rows of a key are contiguous — a key never returns after
// another has started — and, for a window, the order fields never go
// backwards within a key. The streaming forms detect a key change and
// nothing else, so until 2026-10-29 unsorted input made every run its own
// group and the output was wrong without a word. The check is one
// comparison per row and a set of the keys seen, O(keys), which the
// materialised forms hold anyway.
type presortedGuard struct {
	stage  string   // the command, for the message
	fields []string // the key fields, for the message
	seen   map[string]struct{}
	row    int64
}

func newPresortedGuard(stage string, fields []string) *presortedGuard {
	return &presortedGuard{stage: stage, fields: fields, seen: make(map[string]struct{})}
}

// keyChange records that row r starts the key; a key seen before is loud.
func (g *presortedGuard) keyChange(key string, r Record) {
	if _, dup := g.seen[key]; dup {
		panic(fmt.Errorf("%s -presorted: row %d returns to %s, which ended earlier — the input is not sorted by %s; sort it first, or drop -presorted",
			g.stage, g.row, keyText(r, g.fields), strings.Join(g.fields, ", ")))
	}
	g.seen[key] = struct{}{}
}

// orderCheck records that row r follows prev within one key; r before
// prev by the order fields is loud.
func (g *presortedGuard) orderCheck(prev, r Record, orderBy []OrderField) {
	if len(orderBy) == 0 || CompareRecordFields(prev, r, orderBy) <= 0 {
		return
	}
	var fields []string
	for _, o := range orderBy {
		fields = append(fields, o.Field)
	}
	panic(fmt.Errorf("%s -presorted: row %d (%s) comes before the row above it (%s) by the order fields — the input is not sorted by %s; sort it first, or drop -presorted",
		g.stage, g.row, keyText(r, fields), keyText(prev, fields), strings.Join(fields, ", ")))
}

// keyText is "d=a, n=1" for the message.
func keyText(r Record, fields []string) string {
	if len(fields) == 0 {
		return "the whole input"
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		v, _ := Get[any](r, f)
		parts = append(parts, fmt.Sprintf("%s=%v", f, v))
	}
	return strings.Join(parts, ", ")
}
