package commands

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// The macro flags (DFC152 §3.2) and their lowering (§3.3). See macros.go
// for the file and the loader.
//
//	update -set-macro FIELD NAME ARG...    FIELD = NAME(ARG...)
//	where  -if-macro  NAME ARG...          condition NAME(ARG...); +if-macro negates
//	update -if-macro  NAME ARG...          the clause's condition
//
// ARG... is a repeating field slot whose count autocli asks macroArity
// for, so the parser knows exactly where the arguments end and completion
// offers a field at each position and flag names after the last.

const macroFlagsHelpTail = " (the macro's parameters in order; `ssql macro list` shows them). The same as the macro's body as an expression with one -param-field per argument, and that is what generated code carries"

// setMacroFlag registers -set-macro on update.
func setMacroFlag(sb *cf.SubcommandBuilder) *cf.SubcommandBuilder {
	return sb.
		Flag("-set-macro").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Arg("macro").
		Completer(cf.CompletionFunc(completeMacroName)).
		Done().
		Arg("args").
		FieldsFromFlag("").
		Repeat(macroArity).
		Done().
		Accumulate().
		Local().
		Help("Set FIELD to a macro of ~/.config/ssql/macros applied to fields: -set-macro <field> <macro> <field>..." + macroFlagsHelpTail).
		Done()
}

// ifMacroFlag registers -if-macro on where and update.
func ifMacroFlag(sb *cf.SubcommandBuilder) *cf.SubcommandBuilder {
	return sb.
		Flag("-if-macro").
		Arg("macro").
		Completer(cf.CompletionFunc(completeMacroName)).
		Done().
		Arg("args").
		FieldsFromFlag("").
		Repeat(macroArity).
		Done().
		Accumulate().
		Local().
		Help("Condition from a boolean macro of ~/.config/ssql/macros applied to fields: -if-macro <macro> <field>... (use +if-macro to negate)" + macroFlagsHelpTail).
		Done()
}

// macroCall is one parsed -set-macro / -if-macro entry.
type macroCall struct {
	flag    string // "-set-macro" or "-if-macro"
	field   string // -set-macro's target
	m       macro
	args    []string
	negated bool
}

// clauseMacroCalls reads the macro entries of a clause (the parser's
// map-per-occurrence shape: field, macro, args []any).
func clauseMacroCalls(clause cf.Clause) ([]macroCall, error) {
	var calls []macroCall
	for _, flag := range []string{"-set-macro", "-if-macro"} {
		list, _ := clause.Flags[flag].([]any)
		for _, raw := range list {
			e, _ := raw.(map[string]any)
			if e == nil {
				continue
			}
			name, _ := e["macro"].(string)
			m, err := lookupMacro(name)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", flag, err)
			}
			c := macroCall{flag: flag, m: m}
			c.field, _ = e["field"].(string)
			c.negated, _ = e["_negated"].(bool)
			argv, _ := e["args"].([]any)
			for _, a := range argv {
				s, _ := a.(string)
				c.args = append(c.args, s)
			}
			if len(c.args) != len(m.Params) {
				return nil, fmt.Errorf("%s %s: takes %d field(s) %s, got %d", flag, name, len(m.Params), m.Signature(), len(c.args))
			}
			for _, a := range c.args {
				if a == "" {
					return nil, fmt.Errorf("%s %s: a field name is required for each parameter of %s", flag, name, m.Signature())
				}
			}
			calls = append(calls, c)
		}
	}
	return calls, nil
}

// expandClauseMacros rewrites a clause's macro calls into the flags every
// reader of the clause already understands: -set-macro FIELD NAME ARG...
// becomes a -set-expr entry {FIELD, BODY}, -if-macro NAME ARG... an
// -if-expr entry {BODY[, negated]}, and each parameter a -param-field entry
// {NAME, TYPE, ARG}. Called once per command before the exec / generate
// branch, so both see only the lowered form. One clause has one parameter
// environment (DFC134): two calls that declare the same parameter name, or
// a call and the clause's own -param, are reported here by name.
func expandClauseMacros(clause *cf.Clause) error {
	calls, err := clauseMacroCalls(*clause)
	if err != nil {
		return err
	}
	if len(calls) == 0 {
		return nil
	}
	owner := map[string]string{} // parameter name → who declared it
	for _, flag := range []string{"-param", "-param-field"} {
		list, _ := clause.Flags[flag].([]any)
		for _, raw := range list {
			if e, _ := raw.(map[string]any); e != nil {
				if n, _ := e["name"].(string); n != "" {
					owner[n] = flag + " " + n
				}
			}
		}
	}
	for _, c := range calls {
		for _, p := range c.m.Params {
			if by, taken := owner[p.Name]; taken {
				return fmt.Errorf("%s %s: parameter %s is already declared in this clause by %s; call it in its own clause (+) or rename the parameter", c.flag, c.m.Name, p.Name, by)
			}
			owner[p.Name] = "macro " + c.m.Name
		}
	}
	lowered := func(c macroCall) (flag string, entry map[string]any) {
		switch c.flag {
		case "-set-macro":
			return "-set-expr", map[string]any{"field": c.field, "expression": c.m.Body}
		default:
			entry := map[string]any{"expression": c.m.Body}
			if c.negated {
				entry["_negated"] = true
			}
			return "-if-expr", entry
		}
	}
	if clause.Order == nil {
		// A clause built by hand (tests): the lowered entries go last.
		for _, c := range calls {
			flag, entry := lowered(c)
			insertFlagEntry(clause, flag, -1, entry)
		}
	} else {
		// The clause's order (autocli Clause.Order) decides where each
		// lowered entry sits in its list: a -set-macro written between two
		// -set-expr is the second of three -set-expr afterwards, so the
		// sets still apply in the order written (clauseSets).
		byFlag := map[string][]macroCall{}
		for _, c := range calls {
			byFlag[c.flag] = append(byFlag[c.flag], c)
		}
		nextCall := map[string]int{}
		seen := map[string]int{} // entries of each flag emitted into the new order so far
		order := make([]string, 0, len(clause.Order))
		for _, flag := range clause.Order {
			if flag == "-set-macro" || flag == "-if-macro" {
				c := byFlag[flag][nextCall[flag]]
				nextCall[flag]++
				flag, entry := lowered(c)
				insertFlagEntry(clause, flag, seen[flag], entry)
				order = append(order, flag)
				seen[flag]++
				continue
			}
			order = append(order, flag)
			seen[flag]++
		}
		clause.Order = order
	}
	for _, c := range calls {
		for _, p := range c.m.exprParams(c.args) {
			insertFlagEntry(clause, "-param-field", -1, map[string]any{"name": p.Name, "type": p.Type.String(), "field": p.Field})
			if clause.Order != nil {
				clause.Order = append(clause.Order, "-param-field")
			}
		}
	}
	delete(clause.Flags, "-set-macro")
	delete(clause.Flags, "-if-macro")
	return nil
}

// insertFlagEntry adds one occurrence to an Accumulate flag's list at
// index i (-1: at the end).
func insertFlagEntry(clause *cf.Clause, flag string, i int, entry any) {
	list, _ := clause.Flags[flag].([]any)
	if i < 0 || i >= len(list) {
		clause.Flags[flag] = append(list, entry)
		return
	}
	list = append(list[:i], append([]any{entry}, list[i:]...)...)
	clause.Flags[flag] = list
}

// expandMacros lowers every clause of the context (the handler's first
// step after schema mode).
func expandMacros(ctx *cf.Context) error {
	for i := range ctx.Clauses {
		if err := expandClauseMacros(&ctx.Clauses[i]); err != nil {
			return err
		}
	}
	return nil
}

// argvHasMacroFlag reports a -set-macro / -if-macro / +if-macro token.
func argvHasMacroFlag(argv []string) bool {
	for _, a := range argv {
		switch a {
		case "-set-macro", "-if-macro", "+if-macro":
			return true
		}
	}
	return false
}

// lowerMacroArgv is expandClauseMacros on argv tokens, for the fragment:
// `-set-macro F NAME a b c` becomes `-set-expr F BODY -param-field x int a
// …`, `-if-macro NAME a` becomes `-if-expr BODY -param-field …`, `+if-macro`
// `+if-expr`. Other tokens are copied. (A value of another flag that is
// literally "-set-macro" would be misread; the same walk argvHasFlag makes
// for -tz.)
func lowerMacroArgv(argv []string) ([]string, error) {
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		var exprFlag, field string
		fixed := 0
		switch tok {
		case "-set-macro":
			exprFlag, fixed = "-set-expr", 2
		case "-if-macro":
			exprFlag, fixed = "-if-expr", 1
		case "+if-macro":
			exprFlag, fixed = "+if-expr", 1
		default:
			out = append(out, tok)
			continue
		}
		if i+fixed >= len(argv) {
			return nil, fmt.Errorf("%s: incomplete", tok)
		}
		if fixed == 2 {
			field = argv[i+1]
		}
		name := argv[i+fixed]
		m, err := lookupMacro(name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tok, err)
		}
		n := len(m.Params)
		if i+fixed+n >= len(argv) {
			return nil, fmt.Errorf("%s %s: takes %d field(s), got %d", tok, name, n, len(argv)-i-fixed-1)
		}
		args := argv[i+fixed+1 : i+fixed+1+n]
		if field != "" {
			out = append(out, exprFlag, field, m.Body)
		} else {
			out = append(out, exprFlag, m.Body)
		}
		for _, p := range m.exprParams(args) {
			out = append(out, "-param-field", p.Name, p.Type.String(), p.Field)
		}
		i += fixed + n
	}
	return out, nil
}

// applyMacroLowering rewrites a fragment whose argv calls a macro into the
// expanded form, so every generate target (go, sql, ssql, json) carries an
// expression that does not depend on the macros file, and keeps the typed
// argv in AsWrittenArgv for `generate ssql -as-written`. The DFC147 -tz
// pattern, applied to a rewrite rather than an append.
func applyMacroLowering(frag *lib.CodeFragment) error {
	if frag.Op == nil || !argvHasMacroFlag(frag.Op.Argv) {
		return nil
	}
	lowered, err := lowerMacroArgv(frag.Op.Argv)
	if err != nil {
		return err
	}
	frag.AsWrittenArgv = frag.Op.Argv
	frag.Op.Argv = lowered
	frag.Op.Order = lib.OrderForArgv(frag.Op.Kind, lowered)
	frag.Command = commandStringFor(frag.Op.Kind, lowered)
	var names []string
	for i := 0; i < len(frag.AsWrittenArgv)-1; i++ {
		switch frag.AsWrittenArgv[i] {
		case "-set-macro":
			if i+2 < len(frag.AsWrittenArgv) {
				names = append(names, frag.AsWrittenArgv[i+2])
			}
		case "-if-macro", "+if-macro":
			names = append(names, frag.AsWrittenArgv[i+1])
		}
	}
	frag.PlanNotes = append(frag.PlanNotes, "macros expanded: "+strings.Join(names, ", "))
	return nil
}

// restoreAsWritten puts a fragment's argv back as the user typed it (the
// macro call), for `generate ssql -as-written`; recursive through the
// side-input bodies of a func fragment, like dropResolvedZone.
func restoreAsWritten(frag *lib.CodeFragment) {
	for _, body := range frag.FuncBody {
		restoreAsWritten(body)
	}
	if frag.AsWrittenArgv == nil || frag.Op == nil {
		return
	}
	frag.Op.Argv = frag.AsWrittenArgv
	frag.Op.Order = lib.OrderForArgv(frag.Op.Kind, frag.Op.Argv)
	frag.Command = commandStringFor(frag.Op.Kind, frag.Op.Argv)
	frag.AsWrittenArgv = nil
}

// commandStringFor renders `ssql KIND ARGV...`, each word shell-quoted:
// the Command of a fragment (getCommandString) for an argv the command
// chose rather than the one it was invoked with.
func commandStringFor(kind string, argv []string) string {
	parts := make([]string, 0, len(argv)+2)
	parts = append(parts, "ssql")
	if kind != "" {
		parts = append(parts, ssql.ShellQuote(kind))
	}
	for _, a := range argv {
		parts = append(parts, ssql.ShellQuote(a))
	}
	return strings.Join(parts, " ")
}
