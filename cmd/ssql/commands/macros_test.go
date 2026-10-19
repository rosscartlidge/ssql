package commands

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

// writeMacros writes a macros file in a temp dir and points SSQL_MACROS at
// it for the test (loadMacros re-reads when the path changes).
func writeMacros(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "macros")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(macrosEnv, path)
	return path
}

const testMacros = `# comment and blank lines are skipped

add3(x int, y int, z int) = x + y + z
margin(price float, cost float) = (price - cost) / price
cheap(price float, cost float) = price < cost * 1.1
pi() = 3.14159
`

func TestParseMacroFile(t *testing.T) {
	path := writeMacros(t, testMacros)
	set, err := parseMacroFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := macroNames(set); !reflect.DeepEqual(got, []string{"add3", "cheap", "margin", "pi"}) {
		t.Errorf("names = %v", got)
	}
	m := set["add3"]
	if m.Line != 3 || m.Body != "x + y + z" || m.Signature() != "add3(x int, y int, z int)" {
		t.Errorf("add3 = %+v", m)
	}
	if set["pi"].Params != nil || set["pi"].String() != "pi() = 3.14159" {
		t.Errorf("pi = %+v", set["pi"])
	}
	if s := set["margin"].String(); s != "margin(price float, cost float) = (price - cost) / price" {
		t.Errorf("margin = %q", s)
	}
	// Absent file: an empty set, no error. /dev/null likewise.
	for _, p := range []string{filepath.Join(t.TempDir(), "none"), os.DevNull} {
		set, err := parseMacroFile(p)
		if err != nil || len(set) != 0 {
			t.Errorf("%s: set %v, err %v", p, set, err)
		}
	}
}

func TestParseMacroFileErrors(t *testing.T) {
	bad := []struct{ line, want string }{
		{"add3 = x", "expected NAME(PARAM TYPE, ...) = BODY"},
		{"3d(x int) = x", `"3d" is not a valid macro name`},
		{"and(x int) = x", `"and" is not a valid macro name`},
		{"len(x int) = x", "len: names an expression function"},
		{"f(x int = x", "f: missing ) after the parameters"},
		{"f(x int) x", "f: expected = BODY after the parameters"},
		{"f(x int) =", "f: the body is empty"},
		{"f(x) = x", `f: parameter "x" must be NAME TYPE`},
		{"f(1x int) = 1x", `f: "1x" is not a valid parameter name`},
		{"f(x int, x int) = x", "f: parameter x declared twice"},
		{"f(x json) = x", "f: parameter x: type must be one of string, int, float, bool, time"},
		{"f(x auto) = x", "f: parameter x: type must be one of"},
		{"f(x int) = x + y", "f: the body uses y, which is not a parameter"},
		{"f(x int, y int) = x", "f: parameter y is not used by the body"},
		{"f(x int) = x +", "f: expression"},
		{"f(x int) = x\nf(y int) = y", "macros-file:2: f: already defined on line 1"},
	}
	for _, c := range bad {
		path := filepath.Join(t.TempDir(), "macros-file")
		if err := os.WriteFile(path, []byte(c.line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := parseMacroFile(path)
		if err == nil {
			t.Errorf("%q: no error", c.line)
			continue
		}
		msg := strings.ReplaceAll(err.Error(), path, "macros-file")
		if !strings.Contains(msg, c.want) {
			t.Errorf("%q: error %q, want containing %q", c.line, msg, c.want)
		}
		if !strings.Contains(msg, "macros-file:") {
			t.Errorf("%q: error %q lacks the file:line prefix", c.line, msg)
		}
	}
}

func TestMacroArityAndCompletion(t *testing.T) {
	writeMacros(t, testMacros)
	if n, err := macroArity([]string{"s3", "add3"}); err != nil || n != 3 {
		t.Errorf("add3 arity = %d, %v", n, err)
	}
	if n, err := macroArity([]string{"pi"}); err != nil || n != 0 {
		t.Errorf("pi arity = %d, %v", n, err)
	}
	if _, err := macroArity([]string{"s3", "add4"}); err == nil || !strings.Contains(err.Error(), "unknown macro add4; defined: add3, cheap, margin, pi") {
		t.Errorf("unknown: %v", err)
	}
	got, _ := completeMacroName(cf.CompletionContext{Partial: ""})
	if !reflect.DeepEqual(got, []string{"add3", "cheap", "margin", "pi"}) {
		t.Errorf("complete all = %v", got)
	}
	got, _ = completeMacroName(cf.CompletionContext{Partial: "m"})
	if !reflect.DeepEqual(got, []string{"margin"}) {
		t.Errorf("complete m = %v", got)
	}
	got, _ = completeMacroName(cf.CompletionContext{Partial: "zz"})
	if !reflect.DeepEqual(got, []string{"<macro>"}) {
		t.Errorf("complete zz = %v", got)
	}

	// No file: the arity callback says so, naming the path.
	t.Setenv(macrosEnv, os.DevNull)
	if _, err := macroArity([]string{"s3", "add3"}); err == nil || !strings.Contains(err.Error(), "no macros are defined") {
		t.Errorf("no file: %v", err)
	}
	got, _ = completeMacroName(cf.CompletionContext{Partial: ""})
	if !reflect.DeepEqual(got, []string{"<macro>"}) {
		t.Errorf("complete with no file = %v", got)
	}

	// A file that does not parse is the flag's error too.
	writeMacros(t, "f(x int) = x + y\n")
	if _, err := macroArity([]string{"f"}); err == nil || !strings.Contains(err.Error(), "the body uses y") {
		t.Errorf("bad file: %v", err)
	}
}
