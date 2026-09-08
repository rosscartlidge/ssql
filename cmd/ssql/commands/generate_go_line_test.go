package commands

import "testing"

// TestGeneratedGoLineIsFullVersion: the temp module's go directive must be
// a full x.y.z — a bare minor makes an older stock Go look for a
// toolchain that does not exist ("download go1.23: toolchain not
// available"), which every Ubuntu 24.04 go-install user hit on the
// codelab's first `generate go -run`.
func TestGeneratedGoLineIsFullVersion(t *testing.T) {
	got := generatedGoLine()
	if !fullGoVersionRe.MatchString(got) {
		t.Fatalf("generatedGoLine() = %q, want x.y.z", got)
	}
	if got < "1.26.0" {
		t.Fatalf("generatedGoLine() = %q, below the module's floor 1.26", got)
	}
}
