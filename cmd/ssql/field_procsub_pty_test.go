package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// procsubPtyDriver verifies Ctrl-O field completion across a process
// substitution — the case the old `${line%|*}` split couldn't handle. With the
// cursor at a join's RIGHT-side field (`-on <left> <RIGHT>`), Ctrl-O must
// complete from the join's `<(ssql from …)>` source, not the upstream pipeline.
// Driven through a real pty in emacs and vi under a low keyseq-timeout. Prints
// "MODE: PASS|FAIL"; "SKIP" + exit 0 if no usable pty.
const procsubPtyDriver = `
import os, pty, time, select, re, sys
binDir, left, right = sys.argv[1], sys.argv[2], sys.argv[3]

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
    send('eval "$(ssql -field-keybinding)"\n'); drain()
    if vi:
        send("set -o vi\n"); drain()
    # Cursor at the RIGHT field of -on; the schema must come from the procsub.
    send("ssql from %s | ssql join <(ssql from %s) -on lkey " % (left, right))
    time.sleep(0.4)
    os.write(fd, b"\x0f")                # Ctrl-O
    time.sleep(0.9)
    after = drain().decode(errors="replace")
    os.write(fd, b"\x03\n"); time.sleep(0.2); os.close(fd)
    segs = [re.sub(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b[A-Za-z]', '', s) for s in after.split("\r")]
    cand = [s for s in segs if "-on lkey" in s]
    line = cand[-1] if cand else ""
    # PASS: the right field "rkey" (the only column in the procsub source) was
    # completed in, and the left/upstream column "lkey" did not leak as a value.
    return ("-on lkey rkey" in line) and ("lval" not in line)

try:
    ok_e = run(False)
    ok_v = run(True)
except Exception as e:
    print("SKIP:", e); sys.exit(0)
print("emacs:", "PASS" if ok_e else "FAIL")
print("vi:", "PASS" if ok_v else "FAIL")
sys.exit(0 if (ok_e and ok_v) else 1)
`

// TestFieldProcsubPTY exercises Ctrl-O completing a join right-side field from
// its process-substitution source, through a real pty in emacs and vi.
func TestFieldProcsubPTY(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available — skipping real-pty procsub test")
	}
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	if err := os.WriteFile(left, []byte("lkey,lval\n1,a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	right := filepath.Join(dir, "right.csv")
	if err := os.WriteFile(right, []byte("rkey\n1\n"), 0o644); err != nil {
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
	if err := os.WriteFile(driver, []byte(procsubPtyDriver), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(py, driver, binDir, left, right).CombinedOutput()
	got := string(out)
	if strings.HasPrefix(strings.TrimSpace(got), "SKIP:") {
		t.Skipf("pty unavailable: %s", strings.TrimSpace(got))
	}
	if err != nil || !strings.Contains(got, "emacs: PASS") || !strings.Contains(got, "vi: PASS") {
		t.Fatalf("Ctrl-O procsub right-field completion failed in a real pty (err=%v):\n%s", err, got)
	}
}
