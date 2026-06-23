package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runPtyDriver drives a real pty: spawns interactive bash, sources
// `ssql -run-keybinding`, types an ssql pipeline, presses Alt-r, and checks
// the binding fired — in both emacs and vi modes under a low keyseq-timeout.
// Alt-r is ESC-prefixed, so vi mode (where ESC also leaves insert mode) is
// the real test; this is why a pty is mandatory. The assertion is on the
// compile notice (always printed when the binding fires), NOT the compiled
// output — so the test doesn't hinge on a Go toolchain / module resolution
// being available in the pty. Prints "MODE: PASS|FAIL" per mode; "SKIP:
// <reason>" + exit 0 if no usable pty.
const runPtyDriver = `
import os, pty, time, select, re, sys
binDir = sys.argv[1]

def run(vi):
    pid, fd = pty.fork()
    if pid == 0:
        os.execvp("bash", ["bash", "--norc", "--noprofile", "-i"])
    def send(s): os.write(fd, s.encode()); time.sleep(0.4)
    def drain():
        o = b""
        while select.select([fd], [], [], 0.3)[0]:
            try: o += os.read(fd, 4096)
            except OSError: break
        return o
    time.sleep(0.6); drain()
    send("export PATH=%s:$PATH\n" % binDir); drain()
    send("bind 'set keyseq-timeout 1'\n"); drain()
    send('eval "$(ssql -run-keybinding)"\n'); drain()
    if vi:
        send("set -o vi\n"); drain()
    send("ssql version | ssql to table")
    time.sleep(0.5)
    os.write(fd, b"\x1br")               # Alt-r (ESC r)
    time.sleep(1.2)
    after = drain().decode(errors="replace")
    os.write(fd, b"\x03\n"); time.sleep(0.2); os.close(fd)
    clean = re.sub(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b[A-Za-z]', '', after)
    # PASS: the compile notice printed (binding fired) and 'r' did not
    # self-insert into the line (which would leave "to tabler").
    return ("compiling typed pipeline" in clean) and ("to tabler" not in clean)

try:
    ok_e = run(False)
    ok_v = run(True)
except Exception as e:
    print("SKIP:", e); sys.exit(0)
print("emacs:", "PASS" if ok_e else "FAIL")
print("vi:", "PASS" if ok_v else "FAIL")
sys.exit(0 if (ok_e and ok_v) else 1)
`

// TestRunKeybindingPTY exercises the convert-to-typed-and-run keybinding
// through a real pseudo-terminal in both emacs and vi modes under a low
// keyseq-timeout — the conditions an ESC-prefixed single key (Alt-r) must
// survive.
func TestRunKeybindingPTY(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available — skipping real-pty run test")
	}
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(binDir, "ssql")); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, "driver.py")
	if err := os.WriteFile(driver, []byte(runPtyDriver), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(py, driver, binDir).CombinedOutput()
	got := string(out)
	if strings.HasPrefix(strings.TrimSpace(got), "SKIP:") {
		t.Skipf("pty unavailable: %s", strings.TrimSpace(got))
	}
	if err != nil || !strings.Contains(got, "emacs: PASS") || !strings.Contains(got, "vi: PASS") {
		t.Fatalf("run keybinding failed in a real pty (err=%v):\n%s", err, got)
	}
}
