package commands

import (
	"fmt"
	"strings"
)

// KeyBinding describes one member of the ssql readline action-key family.
// This table is the SINGLE SOURCE OF TRUTH: the emitters' `bind` lines
// (KeyBindLines) and the Alt-H cheat-sheet (KeyBindingsHelp, embedded in
// HelpKeybindingScript) are both generated from it, and keybindings_test.go
// checks every emitted script binds each row in each keymap — so the help
// can never silently drift from the actual bindings.
//
// A key that starts with ESC (Alt-x) is bound only in emacs: in vi, ESC is
// the key that leaves insert mode, and an ESC-prefixed binding turns ESC h
// typed within keyseq-timeout — ordinary vi editing, cursor left — into
// the action (Ross, 2026-11-03, `set -o vi`). Such a row names a ViSeq,
// Ctrl-X and a letter, bound in vi-insert and vi-command, and in emacs as
// well so one set of keys works in both.
type KeyBinding struct {
	Key     string // human label, e.g. "Alt-g"
	Seq     string // readline key sequence bound, e.g. \eg
	ViKey   string // label of the vi-safe key ("" when Key is safe everywhere)
	ViSeq   string // its sequence, e.g. \C-xg
	Fn      string // bash function bound, e.g. _ssql_show_go
	Emitter string // flag that emits the binding script, e.g. -code-keybinding
	Desc    string // one-line description for the cheat-sheet
}

// KeyBindings is the ordered family. Order here is the display order in the
// Alt-H cheat-sheet. When you add a binding, add it here — the cheat-sheet
// and the bind lines follow, and the consistency test checks the scripts.
var KeyBindings = []KeyBinding{
	{"Ctrl-O", "\\C-o", "", "", "_ssql_complete_field", "-field-keybinding", "complete a field name from the upstream pipeline schema"},
	{"Ctrl-T", "\\C-t", "", "", "_ssql_optimise_pipeline", "-optimise-keybinding", "optimise the ssql pipeline on the line, in place"},
	{"Alt-g", "\\eg", "Ctrl-X g", "\\C-xg", "_ssql_show_go", "-code-keybinding", "show the typed Go this pipeline generates (without running)"},
	{"Alt-r", "\\er", "Ctrl-X r", "\\C-xr", "_ssql_typed_run", "-run-keybinding", "compile the pipeline as typed Go and run it"},
	{"Alt-h", "\\eh", "Ctrl-X h", "\\C-xh", "_ssql_help_at", "-help-keybinding", "help for the flag / command under the cursor"},
	{"Alt-H", "\\eH", "Ctrl-X H", "\\C-xH", "_ssql_help_keys", "-help-keybinding", "list these key bindings"},
}

// Label is the row's keys as the help shows them: "Alt-g / Ctrl-X g".
func (kb KeyBinding) Label() string {
	if kb.ViKey == "" {
		return kb.Key
	}
	return kb.Key + " / " + kb.ViKey
}

// KeyBindLines is the `bind -x` lines for the bash function fn, every row
// that binds it, in every keymap: the row's Seq in emacs and, for an
// ESC-prefixed key, its ViSeq in emacs, vi-insert and vi-command instead
// of Seq in the vi maps (see KeyBinding), where Seq is unbound.
func KeyBindLines(fn string) string {
	var b strings.Builder
	for _, kb := range KeyBindings {
		if kb.Fn != fn {
			continue
		}
		vi := kb.Seq
		fmt.Fprintf(&b, "bind -m emacs -x '\"%s\": %s'\n", kb.Seq, fn)
		if kb.ViSeq != "" {
			vi = kb.ViSeq
			fmt.Fprintf(&b, "bind -m emacs -x '\"%s\": %s'\n", kb.ViSeq, fn)
		}
		if kb.ViSeq != "" {
			// A shell that sourced an older script holds the ESC key in
			// the vi maps; re-sourcing this one takes it back.
			fmt.Fprintf(&b, "bind -m vi-insert -r '%s'\n", kb.Seq)
			fmt.Fprintf(&b, "bind -m vi-command -r '%s'\n", kb.Seq)
		}
		fmt.Fprintf(&b, "bind -m vi-insert -x '\"%s\": %s'\n", vi, fn)
		fmt.Fprintf(&b, "bind -m vi-command -x '\"%s\": %s'\n", vi, fn)
	}
	return b.String()
}

// KeyBindingsHelp renders the Alt-H cheat-sheet body shown by _ssql_help_keys.
// Generated from KeyBindings so it can never drift from the actual family. The
// text is embedded in a bash double-quoted string, so descriptions must stay
// free of ", $, ` and \ (plain English — review + tests keep them so).
func KeyBindingsHelp() string {
	var b strings.Builder
	b.WriteString("ssql key bindings (vi mode: the Ctrl-X keys)\n\n")
	for _, kb := range KeyBindings {
		fmt.Fprintf(&b, "  %-18s %s\n", kb.Label(), kb.Desc)
	}
	b.WriteString("\nRun 'ssql' with no arguments to see how to enable each binding.")
	return b.String()
}
