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
// "-sum" flag, and checks Ctrl-X h prints the flag's help inline in both
// emacs and vi modes, Alt-h too in emacs — and that in vi ESC h, typed in
// one write, is NOT help: ESC leaves insert mode there, and vi users type
// ESC h to move left. That is exactly why a pty is mandatory. TMUX is unset so the
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
    send("unset TMUX; export SSQL_POPUP=inline\n"); drain()
    send("bind 'set keyseq-timeout 1'\n"); drain()
    # A shell that sourced an older script holds ESC h in the vi maps;
    # sourcing this one must take it back.
    send("bind -m vi-insert -x '\"\\eh\": _ssql_help_at'\n"); drain()
    send('eval "$(ssql -help-keybinding)"\n'); drain()
    if vi:
        send("set -o vi\n"); drain()
    send("ssql group-by dept -sum")
    time.sleep(0.5)
    def fire(keys):
        os.write(fd, keys)
        time.sleep(0.9)
        out = drain().decode(errors="replace")
        return re.sub(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b[A-Za-z]', '', out)
    # Ctrl-X h is help in every keymap.
    # emacs: Alt-h (ESC h) too. vi: ESC h is ESC (leave insert mode)
    # then h (cursor left), however fast it is typed — an ESC-prefixed
    # binding in a vi keymap stole it (Ross, 2026-11-03, set -o vi).
    ok = True
    if vi:
        ok = "Sum field values" not in fire(b"\x1bh")
    else:
        got = fire(b"\x1bh")
        ok = "Sum field values" in got and "-sumh" not in got
    got = fire(b"\x18h")
    ok = ok and "Sum field values" in got and "-sumh" not in got
    os.write(fd, b"\x03\n"); time.sleep(0.2); os.close(fd)
    return ok

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
