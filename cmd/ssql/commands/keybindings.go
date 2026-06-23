package commands

import (
	"fmt"
	"strings"
)

// KeyBinding describes one member of the ssql readline action-key family.
// This table is the SINGLE SOURCE OF TRUTH: the Alt-H cheat-sheet
// (KeyBindingsHelp, embedded in HelpKeybindingScript) is generated from it,
// and keybindings_test.go scans every emitter script for `bind` lines and
// fails if a bound key is missing here — or a row here is bound nowhere — so
// the help can never silently drift from the actual bindings.
type KeyBinding struct {
	Key     string // human label, e.g. "Alt-g"
	Seq     string // readline key sequence bound, e.g. \eg
	Fn      string // bash function bound, e.g. _ssql_show_go
	Emitter string // flag that emits the binding script, e.g. -code-keybinding
	Desc    string // one-line description for the cheat-sheet
}

// KeyBindings is the ordered family. Order here is the display order in the
// Alt-H cheat-sheet. When you add a binding, add it here — the cheat-sheet
// updates automatically and the consistency test stops complaining.
var KeyBindings = []KeyBinding{
	{"Ctrl-O", "\\C-o", "_ssql_complete_field", "-field-keybinding", "complete a field name from the upstream pipeline schema"},
	{"Ctrl-T", "\\C-t", "_ssql_optimise_pipeline", "-optimise-keybinding", "optimise the ssql pipeline on the line, in place"},
	{"Alt-g", "\\eg", "_ssql_show_go", "-code-keybinding", "show the typed Go this pipeline generates (without running)"},
	{"Alt-r", "\\er", "_ssql_typed_run", "-run-keybinding", "compile the pipeline as typed Go and run it"},
	{"Alt-h", "\\eh", "_ssql_help_at", "-help-keybinding", "help for the flag / command under the cursor"},
	{"Alt-H", "\\eH", "_ssql_help_keys", "-help-keybinding", "list these key bindings"},
}

// KeyBindingsHelp renders the Alt-H cheat-sheet body shown by _ssql_help_keys.
// Generated from KeyBindings so it can never drift from the actual family. The
// text is embedded in a bash double-quoted string, so descriptions must stay
// free of ", $, ` and \ (plain English — review + tests keep them so).
func KeyBindingsHelp() string {
	var b strings.Builder
	b.WriteString("ssql key bindings\n\n")
	for _, kb := range KeyBindings {
		fmt.Fprintf(&b, "  %-8s %s\n", kb.Key, kb.Desc)
	}
	b.WriteString("\nRun 'ssql' with no arguments to see how to enable each binding.")
	return b.String()
}
