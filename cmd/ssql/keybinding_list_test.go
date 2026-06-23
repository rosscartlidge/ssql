package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/commands"
)

// TestBareSSQLListsKeybindingEmitters ensures the shell-integration list
// printed by bare `ssql` mentions every keybinding emitter in the KeyBindings
// table. Together with TestKeyBindingsInSync (commands package), this means a
// newly added binding can't ship without appearing in BOTH the Alt-H
// cheat-sheet (generated) and the bare-`ssql` discovery list (this test
// forces the eval line to be added).
func TestBareSSQLListsKeybindingEmitters(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("bare ssql: %v\n%s", err, out)
	}
	s := string(out)
	seen := map[string]bool{}
	for _, kb := range commands.KeyBindings {
		if seen[kb.Emitter] {
			continue
		}
		seen[kb.Emitter] = true
		if !strings.Contains(s, kb.Emitter) {
			t.Errorf("bare `ssql` shell-integration list does not mention emitter %q (add an eval line in main.go)", kb.Emitter)
		}
	}
}
