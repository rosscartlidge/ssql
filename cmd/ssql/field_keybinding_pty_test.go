package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ptyDriver drives a REAL pty: spawns interactive bash, sources
// `ssql -field-keybinding`, types a pipeline, presses the trigger key,
// and reports whether the field completed. It tests both emacs and vi
// editing modes with a low keyseq-timeout — the exact conditions that
// broke earlier (a chord self-inserts under a low timeout; a vi-only or
// emacs-only bind misses the other keymap). A `bash -c` / hand-fed
// COMP_* test cannot catch these; only a real terminal can.
//
// It prints one "MODE: PASS|FAIL" line per mode and exits non-zero on any
// FAIL. If it can't even set up a pty/bash, it prints "SKIP: <reason>"
// and exits 0 so CI without a usable pty doesn't hard-fail.
const ptyDriver = `
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
    send("unset TMUX\n"); drain()
    send("bind 'set keyseq-timeout 1'\n"); drain()      # low timeout: single key must not care
    send('eval "$(ssql -field-keybinding)"\n'); drain()
    if vi:
        send("set -o vi\n"); drain()
    send("ssql from csv %s | ssql group-by na" % csv)
    time.sleep(0.5)                                       # human-paced gap before the key
    os.write(fd, b"\x0f")                                 # Ctrl-O
    time.sleep(0.9)
    after = drain().decode(errors="replace")
    os.write(fd, b"\x03\n"); time.sleep(0.2); os.close(fd)
    # readline redraws via CR; take the FINAL CR-delimited line, not the
    # joined echo (which would also contain the pre-completion text).
    segs = [re.sub(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b[A-Za-z]', '', s) for s in after.split("\r")]
    cand = [s for s in segs if "group-by na" in s]
    line = cand[-1] if cand else ""
    # PASS = the partial 'na' completed to 'name' and no literal ^O leaked in.
    return ("name " in line) and ("^O" not in line) and ("\x0f" not in line)

try:
    ok_e = run(False)
    ok_v = run(True)
except Exception as e:
    print("SKIP:", e); sys.exit(0)
print("emacs:", "PASS" if ok_e else "FAIL")
print("vi:", "PASS" if ok_v else "FAIL")
sys.exit(0 if (ok_e and ok_v) else 1)
`

// TestFieldKeybindingPTY exercises the field-completion keybinding through
// a real pseudo-terminal, in both emacs and vi modes under a low
// keyseq-timeout. Guards against the regressions hit during development:
// wrong/missing keymap, and chord self-insert under a snappy-Esc timeout.
func TestFieldKeybindingPTY(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available — skipping real-pty completion test")
	}
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "p.csv")
	if err := os.WriteFile(csv, []byte("name,dept,salary\nA,x,1\n"), 0o644); err != nil {
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
	if err := os.WriteFile(driver, []byte(ptyDriver), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(py, driver, binDir, csv).CombinedOutput()
	got := string(out)
	if strings.HasPrefix(strings.TrimSpace(got), "SKIP:") {
		t.Skipf("pty unavailable: %s", strings.TrimSpace(got))
	}
	if err != nil || !strings.Contains(got, "emacs: PASS") || !strings.Contains(got, "vi: PASS") {
		t.Fatalf("field keybinding failed in a real pty (err=%v):\n%s", err, got)
	}
}
