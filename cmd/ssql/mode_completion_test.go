package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestGenerateModeCompletion guards the `generate go -mode` value completer.
// It went unshipped originally (no Completer at all), so Tab offered nothing.
// This drives the real `-complete` protocol the shell uses.
func TestGenerateModeCompletion(t *testing.T) {
	bin := buildSSQLForTypedTest(t)

	// Empty word: offer the canonical modes.
	out, err := exec.Command(bin, "-complete", "4", "generate", "go", "-mode", "").CombinedOutput()
	if err != nil {
		t.Fatalf("-complete: %v\n%s", err, out)
	}
	got := strings.Fields(string(out))
	for _, want := range []string{"record", "typed"} {
		if !contains(got, want) {
			t.Errorf("-mode completion missing %q; got %v", want, got)
		}
	}
	// parallel is a deprecated alias — we deliberately do NOT advertise it.
	if contains(got, "parallel") {
		t.Errorf("-mode completion should not offer the deprecated alias 'parallel'; got %v", got)
	}

	// Prefix "t" narrows to typed.
	out, err = exec.Command(bin, "-complete", "4", "generate", "go", "-mode", "t").CombinedOutput()
	if err != nil {
		t.Fatalf("-complete prefix: %v\n%s", err, out)
	}
	if got := strings.Fields(string(out)); !contains(got, "typed") || contains(got, "record") {
		t.Errorf("-mode 't' completion = %v, want [typed]", got)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
