package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestScriptStagesRunThisBinary: `generate go -script` / `-pipeline` run
// their `ssql` stages with the ssql that is running, not the first `ssql`
// on PATH. Here PATH has no ssql at all (only go and the system dirs); on
// the rig a stale /usr/local/bin/ssql ahead of /usr/bin ran the stages of
// a shipped script with the wrong version and the generated program had
// no pipeline body ("undefined: records").
func TestScriptStagesRunThisBinary(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.csv"), []byte("name,age,dept\nAlice,30,Eng\nBob,25,Sales\nCarol,42,Eng\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "s.ssql")
	if err := os.WriteFile(script, []byte("ssql from t.csv\n| ssql where -if age gt 25\n| ssql group-by dept -count n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(goBin)+":/usr/bin:/bin")
	if _, err := exec.LookPath("ssql"); err == nil {
		t.Skip("an ssql is on the restricted PATH; the test cannot tell which binary ran")
	}
	for _, args := range [][]string{
		{"generate", "go", "-script", script, "-mode", "record", "-run"},
		{"generate", "go", "-run", "-pipeline", "ssql from t.csv | ssql where -if age gt 25 | ssql group-by dept -count n"},
	} {
		c := exec.Command(bin, args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		if err != nil || !strings.Contains(string(out), `"dept":"Eng"`) || !strings.Contains(string(out), `"n":2`) {
			t.Errorf("%v: %v\n%s", args[2:], err, out)
		}
	}
}
