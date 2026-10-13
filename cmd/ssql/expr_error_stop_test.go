package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An expression that fails on one record ends the stage's output at that
// record and names it; a null operand is not a failure. Before 2026-10-13
// `update` kept passing the records after the failing one WITHOUT the
// field (an Error line above a full table, with blanks from row 7 on),
// `where` printed "Error evaluating expression", dropped the row and
// exited 0, and date() of a null was the error. Found on the DFC148
// train data: `update -set-expr time 'date(departure_time)'` where a
// station has no departure.
func TestExprErrorStopsTheStream(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	for name, body := range map[string]string{
		"bad.jsonl": `{"id":1,"x":"2023-05-15T00:13:00Z"}` + "\n" + `{"id":2,"x":"nope"}` + "\n" + `{"id":3,"x":"2023-05-15T01:47:00Z"}` + "\n",
		"nil.jsonl": `{"id":1,"x":"2023-05-15T00:13:00Z"}` + "\n" + `{"id":2,"x":null}` + "\n" + `{"id":3,"x":"2023-05-15T01:47:00Z"}` + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// stage runs `from F | STAGE | to csv` and returns the output and the
	// stage's own exit status.
	stage := func(file, stageArgs string) (string, int) {
		c := exec.Command("bash", "-c", bin+" from jsonl "+file+" | "+bin+" "+stageArgs+" | "+bin+" to csv; exit ${PIPESTATUS[1]}")
		c.Dir = dir
		out, err := c.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	update := `update -set-expr t 'date(x)'`
	where := `where -if-expr 'date(x) > date("2023-01-01")'`

	// A bad value: exit 1, the record named, the output ends before it.
	out, code := stage("bad.jsonl", update)
	if code != 1 || !strings.Contains(out, `record 2: evaluating expression for field "t"`) || !strings.Contains(out, "invalid date nope") {
		t.Errorf("update on a bad date: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "1,2023-05-15T00:13:00Z,2023-05-15T00:13:00Z") || strings.Contains(out, "01:47:00Z") {
		t.Errorf("update on a bad date: the output must hold row 1 and stop before row 2:\n%s", out)
	}
	out, code = stage("bad.jsonl", where)
	if code != 1 || !strings.Contains(out, "record 2: evaluating expression") || !strings.Contains(out, "invalid date nope") || strings.Contains(out, "01:47:00Z") {
		t.Errorf("where on a bad date: exit %d\n%s", code, out)
	}
	out, code = stage("bad.jsonl", `update -if-expr 'date(x) > date("2023-01-01")' -set t yes`)
	if code != 1 || !strings.Contains(out, "evaluating -if-expr") || !strings.Contains(out, "invalid date nope") {
		t.Errorf("update -if-expr on a bad date: exit %d\n%s", code, out)
	}

	// A null: no error, the value absent, the comparison no match.
	out, code = stage("nil.jsonl", update)
	if code != 0 || strings.Contains(out, "Error") || out != "id,x,t\n1,2023-05-15T00:13:00Z,2023-05-15T00:13:00Z\n2,,\n3,2023-05-15T01:47:00Z,2023-05-15T01:47:00Z\n" {
		t.Errorf("update on a null: exit %d\n%q", code, out)
	}
	out, code = stage("nil.jsonl", where)
	if code != 0 || out != "id,x\n1,2023-05-15T00:13:00Z\n3,2023-05-15T01:47:00Z\n" {
		t.Errorf("where on a null: exit %d\n%q", code, out)
	}
	// hour() of a null was the empty STRING (a mixed column, and
	// `where -if h eq ""` matched it). Now a nil slot: no value, no match.
	c := exec.Command("bash", "-c", bin+" from jsonl nil.jsonl | "+bin+" update -set-expr h 'hour(x)' | "+bin+" where -if h eq '' | "+bin+" count")
	c.Dir = dir
	if o, err := c.CombinedOutput(); err != nil || strings.TrimSpace(string(o)) != "0" {
		t.Errorf("hour of a null must have no value (it was \"\"): %v\n%q", err, o)
	}

	// The record program agrees on the bad date: loud, not a silent drop.
	o, err := runGeneratedPipeline(t, bin, dir, "record", bin+" from jsonl bad.jsonl | "+bin+" "+where+" | "+bin+" to csv")
	if err == nil || !strings.Contains(o, "invalid date nope") || strings.Contains(o, "goroutine ") {
		t.Errorf("record where on a bad date: %v\n%s", err, o)
	}
}
