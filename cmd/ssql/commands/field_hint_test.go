package commands

import (
	"os"
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

// TestFieldHintTokenInBrowserUI extends the consistency net to the
// browser side (DFC116 F6): ssql-ui.js matches the hint tokens as
// string literals (its FIELD_HINTS list and the value-token check).
// The bash side had this test; the JS side didn't — and a renamed
// token would degrade browser completion to a dead placeholder with
// every Go test green. Covers BOTH copies: the playground source and
// the wasm-embedded copy `make explore-wasm` syncs (a mismatch there
// means a stale embed shipping old tokens).
func TestFieldHintTokenInBrowserUI(t *testing.T) {
	for _, path := range []string{
		"../../ssql-playground/ssql-ui.js", // source of truth
		"../wasm/ssql-ui.js",               // embed copy (explore -wasm)
	} {
		js, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for _, tok := range []string{FieldHintToken, ValueHintToken} {
			if !strings.Contains(string(js), "'"+tok+"'") {
				t.Errorf("%s does not match the hint token '%s' — browser completion would treat it as a candidate, not a hint", path, tok)
			}
		}
	}
}
