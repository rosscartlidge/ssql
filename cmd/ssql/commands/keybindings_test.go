package commands

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bindLineRe matches a readline action bind, e.g.
//
//	bind -m emacs -x '"\eg": _ssql_show_go'
//
// capturing the key sequence and the bound bash function.
var bindLineRe = regexp.MustCompile(`bind -m \w+ -x '"([^"]+)": (\w+)'`)

// scanBoundKeys returns the set of (seq, fn) pairs actually bound by every
// *_keybinding.go emitter source in the package directory.
func scanBoundKeys(t *testing.T) map[[2]string]bool {
	t.Helper()
	files, err := filepath.Glob("*_keybinding.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no *_keybinding.go files found (glob err=%v)", err)
	}
	bound := map[[2]string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range bindLineRe.FindAllStringSubmatch(string(src), -1) {
			bound[[2]string{m[1], m[2]}] = true
		}
	}
	if len(bound) == 0 {
		t.Fatal("no bind lines matched in *_keybinding.go — layout or regex changed")
	}
	return bound
}

// TestKeyBindingsInSync is the anti-drift guard. Every key actually bound by
// an emitter script must appear in the KeyBindings table (so the generated
// Alt-H cheat-sheet lists it), and every table row must be bound by some
// emitter. Adding a binding without updating the table — or vice versa —
// fails here.
func TestKeyBindingsInSync(t *testing.T) {
	bound := scanBoundKeys(t)

	table := map[[2]string]KeyBinding{}
	for _, kb := range KeyBindings {
		table[[2]string{kb.Seq, kb.Fn}] = kb
	}

	for k := range bound {
		if _, ok := table[k]; !ok {
			t.Errorf("key bound in an emitter script but missing from KeyBindings: seq=%q fn=%q — add it to commands/keybindings.go (the Alt-H cheat-sheet is generated from that table)", k[0], k[1])
		}
	}
	for k, kb := range table {
		if !bound[k] {
			t.Errorf("KeyBindings lists %s (seq=%q fn=%q) but no emitter script binds it", kb.Key, k[0], k[1])
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
