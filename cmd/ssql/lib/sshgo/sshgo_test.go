package sshgo

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ssql v4.40.0 (build: 17eb3325, gpu: no)", "v4.40.0"},
		{"v4.41.0-dev", "v4.41.0-dev"},
		{"  ssql  v5.0.0\n", "v5.0.0"},
		{"some weird output", "some weird output"}, // fallback to raw
	}
	for _, c := range cases {
		got := parseVersion(c.in)
		if got != c.want {
			t.Errorf("parseVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPrefixWriter(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		want   string
	}{
		{
			"single line",
			[]string{"hello\n"},
			"node1: hello\n",
		},
		{
			"two lines in one write",
			[]string{"a\nb\n"},
			"node1: a\nnode1: b\n",
		},
		{
			"two lines split across writes",
			[]string{"a\n", "b\n"},
			"node1: a\nnode1: b\n",
		},
		{
			"no trailing newline",
			[]string{"abc"},
			"node1: abc",
		},
		{
			"split mid-line",
			[]string{"hel", "lo\n"},
			"node1: hello\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			pw := &prefixWriter{prefix: "node1: ", w: &buf}
			for _, w := range c.writes {
				n, err := pw.Write([]byte(w))
				if err != nil {
					t.Fatalf("write %q: %v", w, err)
				}
				if n != len(w) {
					t.Errorf("write %q: returned %d, want %d", w, n, len(w))
				}
			}
			if got := buf.String(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Integration-style test: probe localhost. Skipped if SSH to
// localhost isn't set up.
func TestProbeLocalhost(t *testing.T) {
	// Quick check: can we ssh to localhost in BatchMode? If not, skip.
	probe, err := Probe("localhost")
	if err != nil {
		t.Skipf("ssh to localhost unavailable (%v) — skipping", err)
	}
	t.Logf("localhost: %s", probe)
	// Don't assert anything specific — just that it didn't crash
	// and the timestamp got set.
	if probe.ProbedAt.IsZero() {
		t.Errorf("ProbedAt should be set after a successful probe")
	}
	// If probe found ssql, the version should at least start with 'v'.
	if probe.HasSSQL && !strings.HasPrefix(probe.SSQLVer, "v") {
		t.Errorf("expected version to start with 'v', got %q", probe.SSQLVer)
	}
}
