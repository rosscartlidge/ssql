package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// editPtyDriver drives a real pty: interactive bash sources `ssql
// -edit-keybinding`, with VISUAL a script that copies the file bash hands
// it (numbered, in order) and changes nothing. In emacs (Ctrl-X Ctrl-E)
// and vi (Esc v) it presses the editor key on three lines: the ssql
// pipeline, a line that is not ssql, and a pipeline holding a $(…). After
// the first it presses Up and Enter, so the history entry runs again. It
// prints one JSON object per mode: the screen text after each step.
const editPtyDriver = `
import os, pty, time, select, re, sys, json
binDir, editor, lines = sys.argv[1], sys.argv[2], json.loads(sys.argv[3])

def run(vi):
    pid, fd = pty.fork()
    if pid == 0:
        os.environ.pop("TMUX", None)
        os.environ["VISUAL"] = editor
        os.environ["PS1"] = "$ "
        os.execvp("bash", ["bash", "--norc", "--noprofile", "-i"])
    def send(s, t=0.4): os.write(fd, s.encode()); time.sleep(t)
    def drain():
        o = b""
        while select.select([fd], [], [], 0.5)[0]:
            try: o += os.read(fd, 4096)
            except OSError: break
        return re.sub(r'\x1b\[[0-9;?]*[A-Za-z]|\x1b[A-Za-z=>]', '', o.decode(errors="replace")).replace("\r", "")
    time.sleep(0.6); drain()
    send("export PATH=%s:$PATH\n" % binDir); drain()
    send("bind 'set keyseq-timeout 1'\n"); drain()
    send('eval "$(ssql -edit-keybinding)"\n'); drain()
    if vi:
        send("set -o vi\n"); drain()
    def key():
        if vi:
            send("\x1b", 0.3); send("v", 2.5)
        else:
            send("\x18\x05", 2.5)
    steps = {}
    send(lines[0]); key(); steps["pipeline"] = drain()
    if vi:
        send("\x1bk", 0.5)
    else:
        send("\x1b[A", 0.5)
    send("\r", 2.0); steps["history"] = drain()
    send(lines[1]); key(); steps["plain"] = drain()
    send(lines[2]); key(); steps["unsafe"] = drain()
    os.write(fd, b"exit\n"); time.sleep(0.2); os.close(fd)
    return steps

try:
    out = {"emacs": run(False), "vi": run(True)}
except Exception as e:
    print("SKIP:", e); sys.exit(0)
print(json.dumps(out))
`

// TestEditKeybindingPTY: bash's editor key opens the ssql pipeline laid
// out by generate pretty, bash runs the file on exit to the one-liner's
// output, and Up then Enter runs the history entry again (DFC167 §2.2).
// A line that is not ssql, and a pipeline holding a $(…), reach the editor
// unchanged, and the $(…) never runs before the editor opens.
func TestEditKeybindingPTY(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available — skipping real-pty editor-key test")
	}
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "o.csv")
	if err := os.WriteFile(csv, []byte("name,age,dept,salary\nAnn,30,eng,100\nBob,25,ops,80\nCy,19,eng,70\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(binDir, "ssql")); err != nil {
		t.Fatal(err)
	}
	copies := filepath.Join(dir, "copies")
	if err := os.MkdirAll(copies, 0o755); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(dir, "editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/bash\nn=$(ls "+copies+" | wc -l)\ncp \"$1\" "+copies+"/$((n+1)).txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "substituted")
	pipeline := "ssql from csv " + csv + " | ssql where -if age gt 20 | ssql include name dept | ssql sort name | ssql to csv"
	lines := []string{
		pipeline,
		"echo plain-line-unchanged",
		"ssql from csv " + csv + " | ssql where -if name eq \"$(touch " + marker + "; echo Ann)\" | ssql to csv",
	}
	want, err := exec.Command(bin, "generate", "pretty", "-pipeline", pipeline).Output()
	if err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, "driver.py")
	if err := os.WriteFile(driver, []byte(editPtyDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	arg, _ := json.Marshal(lines)
	out, err := exec.Command(py, driver, binDir, editor, string(arg)).CombinedOutput()
	got := strings.TrimSpace(string(out))
	if strings.HasPrefix(got, "SKIP:") {
		t.Skipf("pty unavailable: %s", got)
	}
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, got)
	}
	var screens map[string]map[string]string
	if err := json.Unmarshal([]byte(got), &screens); err != nil {
		t.Fatalf("driver output: %v\n%s", err, got)
	}

	// Each mode opened the editor three times; bash's temp file holds the
	// line it was given, newline-terminated.
	for i, mode := range []string{"emacs", "vi"} {
		file := func(n int) string {
			b, err := os.ReadFile(filepath.Join(copies, strings.TrimSpace(string(rune('0'+i*3+n)))+".txt"))
			if err != nil {
				t.Fatalf("%s: editor copy %d: %v", mode, i*3+n, err)
			}
			return string(b)
		}
		s := screens[mode]
		if f := file(1); f != string(want) {
			t.Errorf("%s: the editor got\n%s\nwant the pretty layout\n%s", mode, f, want)
		}
		rows := "name,dept\nAnn,eng\nBob,ops\n"
		if !strings.Contains(s["pipeline"], rows) {
			t.Errorf("%s: bash did not run the edited pipeline to its output:\n%s", mode, s["pipeline"])
		}
		if !strings.Contains(s["history"], rows) {
			t.Errorf("%s: Up then Enter did not run the history entry again:\n%s", mode, s["history"])
		}
		if f := file(2); f != lines[1]+"\n" {
			t.Errorf("%s: a line that is not ssql reached the editor as %q", mode, f)
		}
		if f := file(3); f != lines[2]+"\n" {
			t.Errorf("%s: a pipeline holding $(…) reached the editor as %q, want it unchanged", mode, f)
		}
	}
	// The $(…) runs once per mode, when bash runs the file — never when the
	// key is pressed. A rewrite that described the pipeline would have run
	// it before the editor opened, and the editor copy would show the
	// pipeline laid out.
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("bash never ran the third line's file: %v", err)
	}
}
