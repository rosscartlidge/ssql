package main

// DFC109: Record→typed re-entry for `from ssh`. These tests stub the
// `ssh` client with a PATH-prepended script that executes the "remote"
// command locally (mapping /usr/bin/ssql to the test build), so the
// whole path — generate-time schema sampling, struct synthesis, the
// FromRecords* conversion, and the generated program's runtime ssh —
// runs without a real remote. The differential assertion is the
// N-way doctrine: every lane must produce byte-identical output.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sshStubDir writes a fake `ssh` that runs the remote command locally
// with /usr/bin/ssql mapped to the test binary, and returns the dir.
func sshStubDir(t *testing.T, bin string) string {
	t.Helper()
	dir := t.TempDir()
	stub := fmt.Sprintf(`#!/bin/bash
cmd="${@: -1}"
cmd=$(printf '%%s' "$cmd" | sed "s|/usr/bin/ssql|%s|g")
exec bash -c "$cmd"
`, bin)
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFromSSHTypedReentry(t *testing.T) {
	bin := corpusBin(t)
	data := corpusData(t)
	t.Setenv("PATH", sshStubDir(t, bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The stub executes "remote" commands locally, inheriting this
	// process's env — scrub the mode vars so only the per-invocation
	// export below selects codegen (mirrors a clean remote shell).
	t.Setenv("SSQL_MODE", "")
	t.Setenv("SSQLGO", "")

	cases := []struct {
		name     string
		pipeline string // {{.bin}} / {{.data}} placeholders
	}{
		{
			name:     "plain",
			pipeline: `{{.bin}} from ssh testhost {{.data}}/employees.csv | {{.bin}} group-by dept -count n | {{.bin}} sort dept`,
		},
		{
			name:     "pushdown",
			pipeline: `{{.bin}} from ssh testhost {{.data}}/employees.csv -- where -if age gt 30 + group-by dept -count n | {{.bin}} sort dept`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmdline := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data).Replace(c.pipeline)

			// Exec lane — the oracle.
			execOut, err := exec.Command("bash", "-c", cmdline).Output()
			if err != nil {
				t.Fatalf("exec lane: %v", err)
			}

			for _, mode := range []string{"record", "typed", "parallel"} {
				t.Run(mode, func(t *testing.T) {
					full := "export SSQL_MODE=" + mode + " && " + cmdline + " | " + bin + " generate go"
					src, err := exec.Command("bash", "-c", full).CombinedOutput()
					if err != nil {
						t.Fatalf("generate go (mode=%s): %v\n%s", mode, err, src)
					}
					if mode != "record" {
						if !strings.Contains(string(src), "typed.FromRecords") {
							t.Fatalf("mode=%s: generated source has no FromRecords boundary:\n%s", mode, src)
						}
					}
					got := goRunGenerated(t, string(src))
					if got != string(execOut) {
						t.Errorf("mode=%s output differs from exec lane\n--- exec:\n%s--- %s:\n%s",
							mode, execOut, mode, got)
					}
				})
			}
		})
	}
}

// TestFromSSHTypedSamplingFailureIsLoud pins decision 3 of DFC109: a
// user who asked for typed must get a generate-time error, not a
// silent Record-mode program, when the remote schema can't be sampled.
func TestFromSSHTypedSamplingFailureIsLoud(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"),
		[]byte("#!/bin/bash\necho 'connection refused' >&2\nexit 255\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	full := "export SSQL_MODE=typed && " + bin + " from ssh downhost /data/x.csv | " + bin + " generate go"
	out, err := exec.Command("bash", "-c", full).CombinedOutput()
	if err == nil {
		t.Fatalf("expected generate-time failure, got success:\n%s", out)
	}
	for _, want := range []string{"sampling remote schema", "SSQL_MODE=record"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("error output missing %q:\n%s", want, out)
		}
	}
}
