package commands

import (
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
	for _, name := range []string{"upper", "has", "getOr", "sha256", "date", "len", "round", "bitand"} {
		if _, ok := FunctionEntry(name); !ok {
			t.Errorf("no entry for %s", name)
		}
	}
	if _, ok := FunctionEntry("nosuch"); ok {
		t.Errorf("nosuch should have no entry")
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
