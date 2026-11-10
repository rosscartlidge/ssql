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
	// Edit marks a key that takes over bash's own editor key (Seq in
	// emacs, ViSeq in vi command mode only, where v is a command): Fn
	// rewrites the line on the hidden editRewriteSeq, then bash's
	// edit-and-execute-command runs on editOpenSeq, so bash still opens
	// $VISUAL and runs the file on exit. A bind -x function cannot call a
	// readline command, so the key is a macro of the two.
	Edit bool
}

// The hidden sequences an Edit row's macro is made of, unbound in a
// default bash in both emacs and vi-command.
const (
	editRewriteSeq = `\C-x\C-p`
	editOpenSeq    = `\C-x\C-q`
)

// KeyBindings is the ordered family. Order here is the display order in the
// Alt-H cheat-sheet. When you add a binding, add it here — the cheat-sheet
// and the bind lines follow, and the consistency test checks the scripts.
var KeyBindings = []KeyBinding{
	{"Ctrl-O", "\\C-o", "", "", "_ssql_complete_field", "-field-keybinding", "complete a field name from the upstream pipeline schema", false},
	{"Ctrl-T", "\\C-t", "", "", "_ssql_optimise_pipeline", "-optimise-keybinding", "optimise the ssql pipeline on the line, in place", false},
	{"Alt-g", "\\eg", "Ctrl-X g", "\\C-xg", "_ssql_show_go", "-code-keybinding", "show the typed Go this pipeline generates (without running)", false},
	{"Alt-r", "\\er", "Ctrl-X r", "\\C-xr", "_ssql_typed_run", "-run-keybinding", "compile the pipeline as typed Go and run it", false},
	{"Alt-h", "\\eh", "Ctrl-X h", "\\C-xh", "_ssql_help_at", "-help-keybinding", "help for the flag / command under the cursor", false},
	{"Alt-H", "\\eH", "Ctrl-X H", "\\C-xH", "_ssql_help_keys", "-help-keybinding", "list these key bindings", false},
	{Key: "Ctrl-X Ctrl-E", Seq: "\\C-x\\C-e", ViKey: "v in command mode", ViSeq: "v", Fn: "_ssql_pretty_line", Emitter: "-edit-keybinding", Desc: "open the line in your editor, the ssql pipeline laid out one stage to a line", Edit: true},
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
		if kb.Edit {
			b.WriteString(editBindLines(kb))
			continue
		}
		vi := kb.Seq
		fmt.Fprintf(&b, "bind -m emacs -x '\"%s\": %s'\n", kb.Seq, fn)
		if kb.ViSeq != "" {
			vi = kb.ViSeq
			fmt.Fprintf(&b, "bind -m emacs -x '\"%s\": %s'\n", kb.ViSeq, fn)
		}
		if strings.HasPrefix(kb.ViSeq, `\C-x`) {
			// vi-insert binds Ctrl-X itself (self-insert), so Ctrl-X h is
			// ambiguous there: readline waits only keyseq-timeout for the
			// h and then inserts ^X (Ross, 2026-11-03). Unbind it while it
			// is still that plain default — removing it once ssql's keys
			// hang off it would remove them too.
			b.WriteString(`bind -m vi-insert -p 2>/dev/null | grep -qxF '"\C-x": self-insert' && bind -m vi-insert -r '\C-x'` + "\n")
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

// editBindLines binds an Edit row: in emacs and vi-command, Fn on the
// hidden rewrite sequence, bash's editor command on the hidden open
// sequence, and the row's key as the macro of the two.
func editBindLines(kb KeyBinding) string {
	var b strings.Builder
	for _, m := range []struct{ keymap, key, open string }{
		{"emacs", kb.Seq, "edit-and-execute-command"},
		{"vi-command", kb.ViSeq, "vi-edit-and-execute-command"},
	} {
		fmt.Fprintf(&b, "bind -m %s -x '\"%s\": %s'\n", m.keymap, editRewriteSeq, kb.Fn)
		fmt.Fprintf(&b, "bind -m %s '\"%s\": %s'\n", m.keymap, editOpenSeq, m.open)
		fmt.Fprintf(&b, "bind -m %s '\"%s\": \"%s%s\"'\n", m.keymap, m.key, editRewriteSeq, editOpenSeq)
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
