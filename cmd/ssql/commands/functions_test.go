package commands

import (
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestFunctionEntry: one function's block can be lifted out of the
// detailed reference — what `ssql functions NAME` prints and what Alt-h
// shows when the cursor is on that name in an expression.
func TestFunctionEntry(t *testing.T) {
	entry, ok := FunctionEntry("bucket")
	if !ok || !strings.HasPrefix(entry, "  bucket(ts, dur)") || !strings.Contains(entry, "Example:") || strings.Contains(entry, "Common Usage") {
		t.Errorf("bucket entry wrong: ok=%v\n%s", ok, entry)
	}
	for _, name := range []string{"upper", "has", "getOr", "sha256", "hash", "date", "len", "round", "bitand"} {
		if _, ok := FunctionEntry(name); !ok {
			t.Errorf("no entry for %s", name)
		}
	}
	if _, ok := FunctionEntry("nosuch"); ok {
		t.Errorf("nosuch should have no entry")
	}
	// Every function the detailed reference lists (any category) is in
	// the concise `ssql functions` listing too — the date category grew
	// 17 functions in DFC147 and the plain listing kept showing the old
	// five. The concise reference is what a user sees first.
	entryRe := regexp.MustCompile(`^  ([A-Za-z_][A-Za-z0-9_]*)\(`)
	for _, line := range strings.Split(functionCatalog(), "\n") {
		if m := entryRe.FindStringSubmatch(line); m != nil && !strings.Contains(FunctionsReference, m[1]+"(") {
			t.Errorf("the detailed reference lists %s but `ssql functions` does not", m[1])
		}
	}
	// And every function the expression engine binds is documented:
	// ssql's compile-time functions (the calendar set, date) and the
	// runtime's environment helpers. A new function cannot ship unlisted.
	engine := append(slices.Sorted(maps.Keys(ssql.ExprCompiledFunctions)), runtime.HelperNames()...)
	for _, name := range engine {
		if _, ok := FunctionEntry(name); !ok {
			t.Errorf("the expression engine binds %s but `ssql functions -category …` has no entry for it", name)
		}
		if !strings.Contains(FunctionsReference, name+"(") {
			t.Errorf("the expression engine binds %s but `ssql functions` does not list it", name)
		}
	}
	// Every function the concise reference lists has a detailed entry.
	for _, line := range strings.Split(FunctionsReference, "\n") {
		if !strings.HasPrefix(line, "  ") || strings.Contains(line, "  +  ") {
			continue
		}
		for _, item := range strings.Split(line, ",") {
			item = strings.TrimSpace(item)
			if i := strings.Index(item, "("); i > 0 {
				name := item[:i]
				if _, ok := FunctionEntry(name); !ok {
					t.Errorf("concise reference lists %s but the detailed reference has no entry", name)
				}
			}
		}
	}
}

// TestExprFunctionCandidates: the word arrives cut at the cursor; the
// identifier under it comes first, then the innermost enclosing call.
func TestExprFunctionCandidates(t *testing.T) {
	cases := map[string]string{
		"bucket":                      "bucket",
		"bucket(":                     "bucket",
		"bucket(ts, ":                 "bucket",
		"salary > 1 && bucket(ts, \"": "bucket",
		"upper(trim(name":             "name trim", // name is a field: the caller falls through to trim
		"upper(trim(name), ":          "upper",
		"salary > ":                   "",
		"len(x) > ":                   "",
		"date(created) > date(":       "date",
		"a + 1":                       "",
		"":                            "",
	}
	for in, want := range cases {
		if got := strings.Join(exprFunctionCandidates(in), " "); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
