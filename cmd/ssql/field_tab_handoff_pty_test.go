package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tabHandoffPtyDriver verifies the Tab→Ctrl-O hand-off end to end: at a field
// position Tab can't resolve (downstream of a pipe), bash completion inserts
// the "Use-Ctrl-O" placeholder; pressing Ctrl-O then deletes that placeholder
// and completes from the live upstream schema. Drives a real pty in emacs and
// vi under a low keyseq-timeout. Both the completion script and the field
// keybinding are sourced. Prints "MODE: PASS|FAIL" per mode; "SKIP" + exit 0
// if no usable pty.
const tabHandoffPtyDriver = `
import os, pty, time, select, re, sys
binDir, csv = sys.argv[1], sys.argv[2]

def seg(after, needle):
    segs = [re.sub(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b[A-Za-z]', '', s) for s in after.split("\r")]
    cand = [s for s in segs if needle in s]
    return cand[-1] if cand else ""

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
    send('eval "$(ssql -completion-script)"\n'); drain()
    send('eval "$(ssql -field-keybinding)"\n'); drain()
    if vi:
        send("set -o vi\n"); drain()
    # Cursor at an empty field position, downstream of a pipe.
    send("ssql from csv %s | ssql group-by " % csv)
    time.sleep(0.4)
    os.write(fd, b"\t")                  # Tab → inserts the placeholder
    time.sleep(0.7)
    after_tab = drain().decode(errors="replace")
    os.write(fd, b"\x0f")                # Ctrl-O → strip placeholder + complete
    time.sleep(0.9)
    after_co = drain().decode(errors="replace")
    os.write(fd, b"\x03\n"); time.sleep(0.2); os.close(fd)
    tab_line = seg(after_tab, "group-by")
    co_line  = seg(after_co, "group-by")
    # PASS: Tab inserted the placeholder; Ctrl-O removed it and completed 'dept'.
    return ("Use-Ctrl-O" in tab_line) and ("Use-Ctrl-O" not in co_line) and ("dept" in co_line)

try:
    ok_e = run(False)
    ok_v = run(True)
except Exception as e:
    print("SKIP:", e); sys.exit(0)
print("emacs:", "PASS" if ok_e else "FAIL")
print("vi:", "PASS" if ok_v else "FAIL")
sys.exit(0 if (ok_e and ok_v) else 1)
`

// TestFieldTabHandoffPTY exercises the Tab→Ctrl-O field-completion hand-off
// through a real pty in both emacs and vi modes.
func TestFieldTabHandoffPTY(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available — skipping real-pty tab-handoff test")
	}
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "d.csv")
	if err := os.WriteFile(csv, []byte("dept\neng\nsales\n"), 0o644); err != nil {
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
	if err := os.WriteFile(driver, []byte(tabHandoffPtyDriver), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(py, driver, binDir, csv).CombinedOutput()
	got := string(out)
	if strings.HasPrefix(strings.TrimSpace(got), "SKIP:") {
		t.Skipf("pty unavailable: %s", strings.TrimSpace(got))
	}
	if err != nil || !strings.Contains(got, "emacs: PASS") || !strings.Contains(got, "vi: PASS") {
		t.Fatalf("Tab→Ctrl-O hand-off failed in a real pty (err=%v):\n%s", err, got)
	}
}
