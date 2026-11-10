package commands

import (
	"regexp"
	"strings"
	"testing"
)

// bindLineRe matches a readline action bind, e.g.
//
//	bind -m emacs -x '"\eg": _ssql_show_go'
//
// capturing the keymap, the key sequence and the bound bash function.
var bindLineRe = regexp.MustCompile(`bind -m ([\w-]+) -x '"([^"]+)": (\w+)'`)

// emittedScripts is every key-binding script ssql emits.
func emittedScripts() []string {
	return []string{FieldKeybindingScript, OptimiseKeybindingScript, CodeKeybindingScript, RunKeybindingScript, HelpKeybindingScript, EditKeybindingScript}
}

// TestKeyBindingsInSync is the anti-drift guard over the scripts as
// emitted. Every key bound must be a KeyBindings row's; every row is
// bound in every keymap — Seq in emacs, plus ViSeq where it has one, and
// in vi-insert and vi-command its ViSeq or, without one, Seq; and no
// ESC-prefixed key is bound in a vi keymap, where ESC leaves insert mode
// and ESC h is the cursor moving left (Ross, 2026-11-03, set -o vi).
func TestKeyBindingsInSync(t *testing.T) {
	bound := map[[3]string]bool{}
	for _, script := range emittedScripts() {
		for _, m := range bindLineRe.FindAllStringSubmatch(script, -1) {
			bound[[3]string{m[1], m[2], m[3]}] = true
		}
	}
	if len(bound) == 0 {
		t.Fatal("no bind lines matched in the emitted scripts — layout or regex changed")
	}
	want := map[[3]string]bool{}
	for _, kb := range KeyBindings {
		if kb.Edit {
			// The rewrite is bound on the hidden sequence in emacs and
			// vi-command only; TestEditKeyIsAMacro checks the macro.
			want[[3]string{"emacs", editRewriteSeq, kb.Fn}] = true
			want[[3]string{"vi-command", editRewriteSeq, kb.Fn}] = true
			continue
		}
		vi := kb.Seq
		want[[3]string{"emacs", kb.Seq, kb.Fn}] = true
		if kb.ViSeq != "" {
			vi = kb.ViSeq
			want[[3]string{"emacs", kb.ViSeq, kb.Fn}] = true
		}
		want[[3]string{"vi-insert", vi, kb.Fn}] = true
		want[[3]string{"vi-command", vi, kb.Fn}] = true
	}
	for k := range bound {
		if !want[k] {
			t.Errorf("bound in an emitted script but not in KeyBindings: %s %q → %s — add it to commands/keybindings.go", k[0], k[1], k[2])
		}
		if k[0] != "emacs" && strings.HasPrefix(k[1], `\e`) {
			t.Errorf("%s binds the ESC-prefixed %q (%s): ESC leaves insert mode in vi, so ESC+key must stay vi editing", k[0], k[1], k[2])
		}
	}
	for k := range want {
		if !bound[k] {
			t.Errorf("KeyBindings wants %s %q → %s but no emitted script binds it", k[0], k[1], k[2])
		}
	}
}

// TestKeyBindingsHelpListsAll confirms the generated cheat-sheet lists every
// binding (key label + description).
func TestKeyBindingsHelpListsAll(t *testing.T) {
	help := KeyBindingsHelp()
	for _, kb := range KeyBindings {
		if !strings.Contains(help, kb.Key) {
			t.Errorf("cheat-sheet missing key %q", kb.Key)
		}
		if !strings.Contains(help, kb.Desc) {
			t.Errorf("cheat-sheet missing description for %q", kb.Key)
		}
	}
}

// TestHelpKeybindingEmbedsCheatSheet ties the emitted help script to the
// table: the script must embed exactly the generated cheat-sheet.
func TestHelpKeybindingEmbedsCheatSheet(t *testing.T) {
	if !strings.Contains(HelpKeybindingScript, KeyBindingsHelp()) {
		t.Error("HelpKeybindingScript does not embed KeyBindingsHelp() — generation is broken")
	}
}

// TestEditKeyIsAMacro: an Edit row's key is bound, in emacs and in vi
// command mode only, to the macro of its rewrite and bash's own editor
// command — a bind -x alone would rewrite the line and never open the
// editor, and v bound in vi-insert would stop the letter v being typed.
func TestEditKeyIsAMacro(t *testing.T) {
	n := 0
	for _, kb := range KeyBindings {
		if !kb.Edit {
			continue
		}
		n++
		for _, want := range []string{
			`bind -m emacs '"` + editOpenSeq + `": edit-and-execute-command'`,
			`bind -m emacs '"` + kb.Seq + `": "` + editRewriteSeq + editOpenSeq + `"'`,
			`bind -m vi-command '"` + editOpenSeq + `": vi-edit-and-execute-command'`,
			`bind -m vi-command '"` + kb.ViSeq + `": "` + editRewriteSeq + editOpenSeq + `"'`,
		} {
			if !strings.Contains(EditKeybindingScript, want) {
				t.Errorf("EditKeybindingScript lacks %s", want)
			}
		}
		if strings.Contains(EditKeybindingScript, "vi-insert") {
			t.Errorf("an editor key is bound in vi-insert, where %q is text", kb.ViSeq)
		}
	}
	if n == 0 {
		t.Fatal("no Edit row in KeyBindings")
	}
}
