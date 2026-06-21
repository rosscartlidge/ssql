package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// helpPtyDriver drives a real pty: spawns interactive bash, sources
// `ssql -help-keybinding`, types an ssql command with the cursor on the
// "-sum" flag, presses Alt-h, and checks the flag's help was printed inline
// — in both emacs and vi modes under a low keyseq-timeout. Alt-h is an
// ESC-prefixed key, so vi mode (where ESC also leaves insert mode) is the
// real test; this is exactly why a pty is mandatory. TMUX is unset so the
// binding takes the inline-print branch we can scrape. Prints
// "MODE: PASS|FAIL" per mode; "SKIP: <reason>" + exit 0 if no usable pty.
const helpPtyDriver = `
import os, pty, time, select, re, sys
binDir = sys.argv[1]

def run(vi):
    pid, fd = pty.fork()
    if pid == 0:
        os.environ.pop("TMUX", None)
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
    send("unset TMUX\n"); drain()
    send("bind 'set keyseq-timeout 1'\n"); drain()
    send('eval "$(ssql -help-keybinding)"\n'); drain()
    if vi:
        send("set -o vi\n"); drain()
    send("ssql group-by dept -sum")
    time.sleep(0.5)
    os.write(fd, b"\x1bh")               # Alt-h (ESC h)
    time.sleep(0.9)
    after = drain().decode(errors="replace")
    os.write(fd, b"\x03\n"); time.sleep(0.2); os.close(fd)
    clean = re.sub(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b[A-Za-z]', '', after)
    # PASS: the -sum flag's help printed (inline), and 'h' did not self-insert
    # into the line (which would leave "-sumh").
    return ("Sum field values" in clean) and ("-sumh" not in clean)

try:
    ok_e = run(False)
    ok_v = run(True)
except Exception as e:
    print("SKIP:", e); sys.exit(0)
print("emacs:", "PASS" if ok_e else "FAIL")
print("vi:", "PASS" if ok_v else "FAIL")
sys.exit(0 if (ok_e and ok_v) else 1)
`

// TestHelpKeybindingPTY exercises the help-at-cursor keybinding through a
// real pseudo-terminal in both emacs and vi modes under a low keyseq-timeout
// — the conditions an ESC-prefixed single key (Alt-h) must survive.
func TestHelpKeybindingPTY(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available — skipping real-pty help test")
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
	if err := os.WriteFile(driver, []byte(helpPtyDriver), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(py, driver, binDir).CombinedOutput()
	got := string(out)
	if strings.HasPrefix(strings.TrimSpace(got), "SKIP:") {
		t.Skipf("pty unavailable: %s", strings.TrimSpace(got))
	}
	if err != nil || !strings.Contains(got, "emacs: PASS") || !strings.Contains(got, "vi: PASS") {
		t.Fatalf("help keybinding failed in a real pty (err=%v):\n%s", err, got)
	}
}
