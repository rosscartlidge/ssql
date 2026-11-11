package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// codePtyDriver drives a real pty: spawns interactive bash, sources
// `ssql -code-keybinding`, types an ssql pipeline, presses Alt-g (emacs) or
// Ctrl-X g (vi, where ESC leaves insert mode and is never bound), and
// checks the generated Go was shown, under a low keyseq-timeout; this is
// why a pty is mandatory. TMUX is
// unset so the binding takes the inline branch we can scrape. Prints "MODE:
// PASS|FAIL" per mode; "SKIP: <reason>" + exit 0 if no usable pty.
const codePtyDriver = `
import os, pty, time, select, re, sys
binDir, csv = sys.argv[1], sys.argv[2]

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
    send("unset TMUX; export SSQL_POPUP=inline\n"); drain()
    send("bind 'set keyseq-timeout 1'\n"); drain()
    send('eval "$(ssql -code-keybinding)"\n'); drain()
    if vi:
        send("set -o vi\n"); drain()
    send("ssql from csv %s | ssql to table" % csv)
    time.sleep(0.5)
    # Alt-g in emacs; Ctrl-X g in vi, where ESC leaves insert mode
    os.write(fd, b"\x18g" if vi else b"\x1bg")
    time.sleep(0.9)
    after = drain().decode(errors="replace")
    os.write(fd, b"\x03\n"); time.sleep(0.2); os.close(fd)
    clean = re.sub(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b[A-Za-z]', '', after)
    # PASS: the generated Go was shown and 'g' did not self-insert into the
    # line (which would leave "to tableg").
    return ("package main" in clean) and ("to tableg" not in clean)

try:
    ok_e = run(False)
    ok_v = run(True)
except Exception as e:
    print("SKIP:", e); sys.exit(0)
print("emacs:", "PASS" if ok_e else "FAIL")
print("vi:", "PASS" if ok_v else "FAIL")
sys.exit(0 if (ok_e and ok_v) else 1)
`

// TestCodeKeybindingPTY exercises the show-generated-code keybinding through a
// real pseudo-terminal in both emacs and vi modes under a low keyseq-timeout —
// the conditions an ESC-prefixed single key (Alt-g) must survive.
func TestCodeKeybindingPTY(t *testing.T) {
	t.Parallel()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available — skipping real-pty code test")
	}
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "k.csv")
	if err := os.WriteFile(csv, []byte("name,age\nA,30\nB,25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(binDir, "ssql")); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, "driver.py")
	if err := os.WriteFile(driver, []byte(codePtyDriver), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(py, driver, binDir, csv).CombinedOutput()
	got := string(out)
	if strings.HasPrefix(strings.TrimSpace(got), "SKIP:") {
		t.Skipf("pty unavailable: %s", strings.TrimSpace(got))
	}
	if err != nil || !strings.Contains(got, "emacs: PASS") || !strings.Contains(got, "vi: PASS") {
		t.Fatalf("code keybinding failed in a real pty (err=%v):\n%s", err, got)
	}
}
