package commands

import (
	"strings"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

// TestFieldHintTokenConsistent locks the three places the field-hint token
// must agree: the constant, autocli's runtime hint (wired in init), and the
// literal the Ctrl-O bash function matches. It also enforces that the token is
// shell-safe so Tab inserts it cleanly (no %q backslash-mangling, one word).
func TestFieldHintTokenConsistent(t *testing.T) {
	if cf.FieldNameHint != FieldHintToken {
		t.Errorf("init() did not wire cf.FieldNameHint: got %q, want %q", cf.FieldNameHint, FieldHintToken)
	}
	if !strings.Contains(FieldKeybindingScript, FieldHintToken) {
		t.Errorf("FieldKeybindingScript does not match/strip the hint token %q", FieldHintToken)
	}
	// Must be a single word with no shell metacharacters, so bash inserts it
	// verbatim (printf %q leaves it untouched) and ${line##*[ |]} captures it whole.
	if strings.ContainsAny(FieldHintToken, " \t\"'$`<>|&;()*?[]{}\\") {
		t.Errorf("FieldHintToken %q contains shell-unsafe characters", FieldHintToken)
	}

	// Same contract for the value token…
	if cf.FieldValueHint != ValueHintToken {
		t.Errorf("init() did not wire cf.FieldValueHint: got %q, want %q", cf.FieldValueHint, ValueHintToken)
	}
	if !strings.Contains(FieldKeybindingScript, ValueHintToken) {
		t.Errorf("FieldKeybindingScript does not match/strip the value token %q", ValueHintToken)
	}
	if strings.ContainsAny(ValueHintToken, " \t\"'$`<>|&;()*?[]{}\\") {
		t.Errorf("ValueHintToken %q contains shell-unsafe characters", ValueHintToken)
	}
	// …and the suffix hazard the cleanup loop orders around: the value token
	// ends with the field token, so the script MUST test the value token
	// first or placeholder deletion leaves "Values-" behind.
	if !strings.HasSuffix(ValueHintToken, FieldHintToken) {
		t.Errorf("ValueHintToken %q no longer ends with FieldHintToken %q — revisit the cleanup ordering comment", ValueHintToken, FieldHintToken)
	}
	if strings.Index(FieldKeybindingScript, `"Values-Use-Ctrl-O" "Use-Ctrl-O"`) < 0 {
		t.Errorf("placeholder cleanup must test the value token before the field token")
	}
}
