package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// helpPagerPtyDriver: outside tmux, a long Alt-h answer (the function
// reference, from inside an -if-expr argument) goes to $PAGER — the
// framework-free "popup" for readers without tmux (DFC127 §3) — while a
// short one (a flag's help) still prints inline. PAGER is a script that
// records its stdin, so the driver can see where the text went. The pty
// is 24 rows so the reference (well over 24 lines) needs the pager.
const helpPagerPtyDriver = `
import os, pty, time, select, re, sys, fcntl, termios, struct
binDir, pagerLog = sys.argv[1], sys.argv[2]
# A pager that records its stdin. PAGER is word-split by the shell, so it
# must be a plain path, not a quoted command.
pagerScript = pagerLog + ".sh"
open(pagerScript, "w").write("#!/bin/sh\ncat > %s\n" % pagerLog)
os.chmod(pagerScript, 0o755)

def run(line, keys):
    pid, fd = pty.fork()
    if pid == 0:
        os.environ.pop("TMUX", None)
        os.environ["PAGER"] = pagerScript
        os.execvp("bash", ["bash", "--norc", "--noprofile", "-i"])
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 100, 0, 0))
    def send(s): os.write(fd, s.encode()); time.sleep(0.4)
    def drain():
        o = b""
        while select.select([fd], [], [], 0.3)[0]:
            try: o += os.read(fd, 4096)
            except OSError: break
        return o
    time.sleep(0.6); drain()
    send("export PATH=%s:$PATH\n" % binDir); drain()
    send("unset TMUX; unset SSQL_POPUP; export LINES=24\n"); drain()
    send("bind 'set keyseq-timeout 1'\n"); drain()
    send('eval "$(ssql -help-keybinding)"\n'); drain()
    send(line)
    time.sleep(0.5)
    os.write(fd, keys)
    time.sleep(1.2)
    after = drain().decode(errors="replace")
    os.write(fd, b"\x03\n"); time.sleep(0.2); os.close(fd)
    return re.sub(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b[A-Za-z]', '', after)

try:
    open(pagerLog, "w").close()
    long_out = run("ssql from x.csv | ssql where -if-expr 'salary > ", b"\x1bh")
    long_pager = open(pagerLog).read()
    open(pagerLog, "w").close()
    short_out = run("ssql group-by dept -sum", b"\x1bh")
    short_pager = open(pagerLog).read()
except Exception as e:
    print("SKIP:", e); sys.exit(0)
ok_long = ("EXPRESSION FUNCTIONS" in long_pager) and ("EXPRESSION FUNCTIONS" not in long_out)
ok_short = ("Sum field values" in short_out) and (short_pager.strip() == "")
print("long-to-pager:", "PASS" if ok_long else "FAIL")
print("short-inline:", "PASS" if ok_short else "FAIL")
if not (ok_long and ok_short):
    print("--- long inline:", long_out[-400:]); print("--- long pager:", long_pager[:200])
    print("--- short inline:", short_out[-300:]); print("--- short pager:", short_pager[:200])
sys.exit(0 if (ok_long and ok_short) else 1)
`

// TestHelpPagerPTY: without tmux, Alt-h's long answers open in the pager
// and short ones print inline — through a real 24-row pty.
func TestHelpPagerPTY(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available — skipping real-pty pager test")
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
	if err := os.WriteFile(driver, []byte(helpPagerPtyDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(py, driver, binDir, filepath.Join(dir, "pager.log")).CombinedOutput()
	got := string(out)
	if strings.HasPrefix(strings.TrimSpace(got), "SKIP:") {
		t.Skipf("pty unavailable: %s", strings.TrimSpace(got))
	}
	if err != nil || !strings.Contains(got, "long-to-pager: PASS") || !strings.Contains(got, "short-inline: PASS") {
		t.Fatalf("pager fallback failed in a real pty (err=%v):\n%s", err, got)
	}
}
