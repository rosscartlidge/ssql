package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/runtime"
)

// Expression macros (DFC152): a named expression with typed parameters,
// defined once in a per-user file and called from a flag whose arguments
// are field slots, so the name completes on Tab and every argument on
// Ctrl-O:
//
//	# ~/.config/ssql/macros
//	add3(x int, y int, z int) = x + y + z
//	margin(price float, cost float) = (price - cost) / price
//
//	ssql update -set-macro s3 add3 f1 f2 f3     # s3 = f1 + f2 + f3
//	ssql where  -if-macro  cheap price cost     # cheap(price float, cost float) = price < cost * 1.1
//
// A call is exactly `-set-expr FIELD BODY` (or `-if-expr BODY`) plus one
// `-param-field NAME TYPE ARG` per argument (DFC135), which every lane
// already lowers; the command rewrites the call to that form before
// anything else reads the clause (macro_flags.go), so exec, generated Go,
// SQL and the optimiser never see a macro. A macro is a keyboard
// convenience: whatever is generated carries the expansion and does not
// depend on this file.
//
// The file is `$SSQL_MACROS`, else `$XDG_CONFIG_HOME/ssql/macros`, else
// `~/.config/ssql/macros`; absent means no macros. Bodies are compiled at
// load with the parameters as their only environment, so a bad line is
// reported with its number before any pipeline runs (`ssql macro check`).

// macroParam is one declared parameter: an expression identifier bound,
// at the call, to a field viewed as Type (the -param-field rule).
type macroParam struct {
	Name string
	Type ssql.FieldType
}

// macro is one parsed definition.
type macro struct {
	Name   string
	Params []macroParam
	Body   string
	Line   int
}

// Signature is "add3(x int, y int, z int)".
func (m macro) Signature() string {
	parts := make([]string, len(m.Params))
	for i, p := range m.Params {
		parts[i] = p.Name + " " + p.Type.String()
	}
	return m.Name + "(" + strings.Join(parts, ", ") + ")"
}

// String is the definition line in its canonical spelling.
func (m macro) String() string {
	return m.Signature() + " = " + m.Body
}

// macrosEnv overrides the file's location; `/dev/null` turns macros off.
const macrosEnv = "SSQL_MACROS"

// macroFile is the path the macros are read from ("" when no home
// directory can be found and nothing overrides it).
func macroFile() string {
	if p := os.Getenv(macrosEnv); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "ssql", "macros")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "ssql", "macros")
}

// macroCache holds the last file read, keyed by path so a test that
// points SSQL_MACROS elsewhere sees its own file.
var macroCache struct {
	sync.Mutex
	path string
	set  map[string]macro
	err  error
}

// loadMacros returns the macros of macroFile(), read once per path.
func loadMacros() (map[string]macro, error) {
	path := macroFile()
	macroCache.Lock()
	defer macroCache.Unlock()
	if macroCache.set == nil || macroCache.path != path {
		macroCache.path = path
		macroCache.set, macroCache.err = parseMacroFile(path)
	}
	return macroCache.set, macroCache.err
}

// parseMacroFile reads one macros file. An absent file is an empty set.
func parseMacroFile(path string) (map[string]macro, error) {
	set := map[string]macro{}
	if path == "" {
		return set, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return set, nil
		}
		return set, err
	}
	defer f.Close()
	label := path
	if path == macroFile() && os.Getenv(macrosEnv) == "" {
		label = "macros"
	}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m, err := parseMacroLine(line, n)
		if err != nil {
			return set, fmt.Errorf("%s:%d: %w", label, n, err)
		}
		if prev, dup := set[m.Name]; dup {
			return set, fmt.Errorf("%s:%d: %s: already defined on line %d", label, n, m.Name, prev.Line)
		}
		set[m.Name] = m
	}
	if err := sc.Err(); err != nil {
		return set, fmt.Errorf("%s: %w", label, err)
	}
	return set, nil
}

// parseMacroLine parses `NAME(p type, ...) = BODY` and compiles BODY with
// the parameters as its only environment.
func parseMacroLine(line string, n int) (macro, error) {
	open := strings.IndexByte(line, '(')
	if open < 0 {
		return macro{}, fmt.Errorf("expected NAME(PARAM TYPE, ...) = BODY")
	}
	name := strings.TrimSpace(line[:open])
	if !exprParamName.MatchString(name) || exprParamReserved[name] {
		return macro{}, fmt.Errorf("%q is not a valid macro name (letters, digits and _, not starting with a digit, not an expression keyword)", name)
	}
	if _, isFunc := FunctionEntry(name); isFunc {
		return macro{}, fmt.Errorf("%s: names an expression function; choose another name", name)
	}
	closeIdx := strings.IndexByte(line[open:], ')')
	if closeIdx < 0 {
		return macro{}, fmt.Errorf("%s: missing ) after the parameters", name)
	}
	closeIdx += open
	rest := strings.TrimSpace(line[closeIdx+1:])
	if !strings.HasPrefix(rest, "=") {
		return macro{}, fmt.Errorf("%s: expected = BODY after the parameters", name)
	}
	body := strings.TrimSpace(rest[1:])
	if body == "" {
		return macro{}, fmt.Errorf("%s: the body is empty", name)
	}
	m := macro{Name: name, Body: body, Line: n}
	seen := map[string]bool{}
	if params := strings.TrimSpace(line[open+1 : closeIdx]); params != "" {
		for _, p := range strings.Split(params, ",") {
			fields := strings.Fields(p)
			if len(fields) != 2 {
				return macro{}, fmt.Errorf("%s: parameter %q must be NAME TYPE", name, strings.TrimSpace(p))
			}
			pname, typ := fields[0], fields[1]
			if !exprParamName.MatchString(pname) || exprParamReserved[pname] {
				return macro{}, fmt.Errorf("%s: %q is not a valid parameter name", name, pname)
			}
			if seen[pname] {
				return macro{}, fmt.Errorf("%s: parameter %s declared twice", name, pname)
			}
			seen[pname] = true
			ft, err := ssql.ParseFieldType(typ)
			if err != nil || ft == ssql.FieldTypeAuto || ft == ssql.FieldTypeJSON {
				return macro{}, fmt.Errorf("%s: parameter %s: type must be one of string, int, float, bool, time (got %q)", name, pname, typ)
			}
			m.Params = append(m.Params, macroParam{Name: pname, Type: ft})
		}
	}
	// The body sees its parameters and nothing else: a reader of the file
	// knows what a macro depends on, and a call cannot change its meaning.
	idents, err := runtime.ExprIdentifiers(body)
	if err != nil {
		return macro{}, fmt.Errorf("%s: %w", name, err)
	}
	used := map[string]bool{}
	for _, id := range idents {
		if !seen[id] {
			return macro{}, fmt.Errorf("%s: the body uses %s, which is not a parameter", name, id)
		}
		used[id] = true
	}
	for _, p := range m.Params {
		if !used[p.Name] {
			return macro{}, fmt.Errorf("%s: parameter %s is not used by the body", name, p.Name)
		}
	}
	if _, err := compileClauseExpr(body, m.exprParams(nil)); err != nil {
		return macro{}, fmt.Errorf("%s: %w", name, err)
	}
	return m, nil
}

// exprParams is the macro's parameters as the -param-field bindings a
// call makes, each viewing the field at the same position of args (the
// parameter's own name when args is nil: the load-time compile).
func (m macro) exprParams(args []string) []ExprParam {
	out := make([]ExprParam, len(m.Params))
	for i, p := range m.Params {
		field := p.Name
		if i < len(args) {
			field = args[i]
		}
		out[i] = ExprParam{Name: p.Name, Type: p.Type, Field: field}
	}
	return out
}

// macroNames lists the defined names, sorted.
func macroNames(set map[string]macro) []string {
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// lookupMacro finds NAME or explains what is defined.
func lookupMacro(name string) (macro, error) {
	set, err := loadMacros()
	if err != nil {
		return macro{}, err
	}
	m, ok := set[name]
	if !ok {
		if len(set) == 0 {
			return macro{}, fmt.Errorf("unknown macro %s: no macros are defined (%s)", name, macroFileLabel())
		}
		return macro{}, fmt.Errorf("unknown macro %s; defined: %s", name, strings.Join(macroNames(set), ", "))
	}
	return m, nil
}

// macroFileLabel names the file for messages.
func macroFileLabel() string {
	if p := macroFile(); p != "" {
		return p
	}
	return "no macros file: set " + macrosEnv
}

// macroArity is the arity callback of the macro flags (autocli
// Arg(...).Repeat): the last fixed argument is the macro's NAME, and the
// number of field arguments that follow is its parameter count. An unknown
// name, or a file that does not parse, is the parse error of the flag.
func macroArity(prev []string) (int, error) {
	m, err := lookupMacro(prev[len(prev)-1])
	if err != nil {
		return 0, err
	}
	return len(m.Params), nil
}

// completeMacroName offers the defined names (Tab after -set-macro FIELD
// or -if-macro); the file is read at completion time.
func completeMacroName(ctx cf.CompletionContext) ([]string, error) {
	set, err := loadMacros()
	if err != nil || len(set) == 0 {
		return []string{"<macro>"}, nil
	}
	var out []string
	for _, n := range macroNames(set) {
		if strings.HasPrefix(n, ctx.Partial) {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return []string{"<macro>"}, nil
	}
	return out, nil
}
