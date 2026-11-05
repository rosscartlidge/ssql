package typedomain

import (
	"fmt"
	"sort"
	"strings"
)

// The members a domain is made of: the scalar types a record holds,
// named by their wire spelling. A new scalar type starts here; the lint
// then names every switch and table whose domain now misses it (DFC163).
const (
	mInt64   = "int64"
	mFloat64 = "float64"
	mString  = "string"
	mBool    = "bool"
	mTime    = "time"
	mInt128  = "int128"
	mDecimal = "decimal"
	mInet    = "inet"
)

// members is every scalar member, in the order diagnostics list them.
var members = []string{mInt64, mInt128, mDecimal, mFloat64, mString, mTime, mInet, mBool}

// structScalars are the members whose Go form is a struct: a switch or
// table naming one of them is a switch over record values and must
// declare its domain. The basic types alone name too much ordinary code.
// ssql:domain fixed: the lint's own tables
var structScalars = map[string]bool{mTime: true, mInt128: true, mDecimal: true, mInet: true}

// domains is the one table of domains (DFC163 §2.2).
var domains = map[string][]string{
	"wide":    {mInt128, mDecimal},
	"exact":   {mInt64, mInt128, mDecimal},
	"numeric": {mInt64, mInt128, mDecimal, mFloat64},
	"ordered": {mInt64, mInt128, mDecimal, mFloat64, mString, mTime, mInet},
	"scalar":  members,
}

// annotation is a parsed `// ssql:domain …` comment.
type annotation struct {
	members map[string]bool // nil for a fixed switch
	fixed   bool
}

const marker = "ssql:domain"

// parseAnnotation reads the text after the marker: a `+`-joined union of
// domain and member names, or `fixed: reason`.
func parseAnnotation(text string) (annotation, error) {
	text = strings.TrimSpace(text)
	if rest, ok := strings.CutPrefix(text, "fixed"); ok {
		rest = strings.TrimSpace(rest)
		reason, ok := strings.CutPrefix(rest, ":")
		if !ok || strings.TrimSpace(reason) == "" {
			return annotation{}, fmt.Errorf("ssql:domain fixed needs a reason: `// ssql:domain fixed: why`")
		}
		return annotation{fixed: true}, nil
	}
	word := text
	if i := strings.IndexAny(word, " \t"); i >= 0 {
		word = word[:i]
	}
	if word == "" {
		return annotation{}, fmt.Errorf("ssql:domain names no domain (%s)", known())
	}
	a := annotation{members: map[string]bool{}}
	for _, part := range strings.Split(word, "+") {
		if ms, ok := domains[part]; ok {
			for _, m := range ms {
				a.members[m] = true
			}
			continue
		}
		if isMember(part) {
			a.members[part] = true
			continue
		}
		return annotation{}, fmt.Errorf("ssql:domain %q is no domain or type (%s)", part, known())
	}
	return a, nil
}

func isMember(s string) bool {
	for _, m := range members {
		if m == s {
			return true
		}
	}
	return false
}

func known() string {
	var ds []string
	for d := range domains {
		ds = append(ds, d)
	}
	sort.Strings(ds)
	return "domains " + strings.Join(ds, ", ") + "; types " + strings.Join(members, ", ") + "; or fixed: reason"
}

// missing lists the declared members the cases do not cover, in
// members order.
func (a annotation) missing(covered map[string]bool) []string {
	var out []string
	for _, m := range members {
		if a.members[m] && !covered[m] {
			out = append(out, m)
		}
	}
	return out
}

// Members is every scalar member's wire name, for the tests that must
// cover each one (the type matrix's type table, DFC163 §2.1).
func Members() []string { return append([]string(nil), members...) }
