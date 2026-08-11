package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// optimisePtyDriver drives a real pty: spawns interactive bash, sources
// `ssql -optimise-keybinding`, types a pipeline with two `where`s, presses
// Ctrl-T, and checks the line was rewritten so the wheres merged — in
// both emacs and vi modes under a low keyseq-timeout. Prints
// "MODE: PASS|FAIL" per mode; "SKIP: <reason>" + exit 0 if no usable pty.
const optimisePtyDriver = `
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
    send("bind 'set keyseq-timeout 1'\n"); drain()
    send('eval "$(ssql -optimise-keybinding)"\n'); drain()
    if vi:
        send("set -o vi\n"); drain()
    send("ssql from csv %s | ssql where -if age gt 20 | ssql where -if dept eq eng | ssql limit 2" % csv)
    time.sleep(0.5)
    os.write(fd, b"\x14")                # Ctrl-T
    time.sleep(1.0)
    after = drain().decode(errors="replace")
    os.write(fd, b"\x03\n"); time.sleep(0.2); os.close(fd)
    # readline redraws the line by emitting CR; the FINAL line state is the
    # last CR-delimited segment that holds the pipeline (NOT the joined echo).
    segs = [re.sub(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b[A-Za-z]', '', s) for s in after.split("\r")]
    cand = [s for s in segs if "ssql from csv" in s and "where" in s]
    line = cand[-1] if cand else ""
    # PASS: the two wheres merged into one (count of "ssql where" == 1) and no literal ^T.
    return (line.count("ssql where") == 1) and ("dept eq eng" in line) and ("age gt 20" in line) and ("^T" not in line)

try:
    ok_e = run(False)
    ok_v = run(True)
except Exception as e:
    print("SKIP:", e); sys.exit(0)
print("emacs:", "PASS" if ok_e else "FAIL")
print("vi:", "PASS" if ok_v else "FAIL")
sys.exit(0 if (ok_e and ok_v) else 1)
`

// TestOptimiseKeybindingPTY exercises the in-situ optimise keybinding
// through a real pseudo-terminal in both emacs and vi modes under a low
// keyseq-timeout — the conditions a single-key bind must survive.
func TestOptimiseKeybindingPTY(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available — skipping real-pty optimise test")
	}
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "o.csv")
	if err := os.WriteFile(csv, []byte("name,age,dept,salary\nA,30,eng,100\nB,25,eng,80\n"), 0o644); err != nil {
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
	if err := os.WriteFile(driver, []byte(optimisePtyDriver), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(py, driver, binDir, csv).CombinedOutput()
	got := string(out)
	if strings.HasPrefix(strings.TrimSpace(got), "SKIP:") {
		t.Skipf("pty unavailable: %s", strings.TrimSpace(got))
	}
	if err != nil || !strings.Contains(got, "emacs: PASS") || !strings.Contains(got, "vi: PASS") {
		t.Fatalf("optimise keybinding failed in a real pty (err=%v):\n%s", err, got)
	}
}
