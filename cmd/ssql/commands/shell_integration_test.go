package commands

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// emitterDefRe matches a top-level definition of a shell-integration script
// emitter, e.g. `const FieldKeybindingScript = ...` or `var HelpKeybindingScript = …`.
var emitterDefRe = regexp.MustCompile(`(?m)^(?:const|var)\s+(\w+(?:Keybinding|Helpers)Script)\b`)

// TestShellIntegrationsCoverEmitters is the drift guard: every emitter
// const/var defined in the package must be wired into the ShellIntegrations
// table, so `ssql -shell-init` and the bare-`ssql` hint pick it up. Add a new
// *KeybindingScript without adding it to the table → this fails.
func TestShellIntegrationsCoverEmitters(t *testing.T) {
	table, err := os.ReadFile("shell_integration.go")
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range emitterDefRe.FindAllStringSubmatch(string(src), -1) {
			name := m[1]
			if seen[name] {
				continue
			}
			seen[name] = true
			if !strings.Contains(string(table), name) {
				t.Errorf("emitter %s is not referenced in the ShellIntegrations table (shell_integration.go) — add it so -shell-init includes it", name)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no *KeybindingScript / *HelpersScript emitters found — regex or layout changed")
	}
}
