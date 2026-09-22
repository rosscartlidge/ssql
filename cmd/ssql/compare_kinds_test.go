package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `-if` literal that is not of the field's kind, or an `-if-field` over
// two kinds, cannot be compared: it is an error in every lane, never false
// (exec until v4.106.0) and never a comparison of the number's text
// (record codegen guessed the kind from the literal and filtered `age > 0`
// for `age gt abc`). The valid mixed spellings stay valid: a fractional
// literal on an int column compares as a number, a numeric-looking literal
// on a text column compares as text.
func TestMixedKindComparisonsAreLoud(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "mk.csv")
	os.WriteFile(csvPath, []byte("id,age,name,code,ok\n1,30,bob,007,true\n2,5,amy,12,false\n3,41,cal,x9,true\n"), 0o644)

	lanes := map[string]string{"exec": "", "record": "record", "typed": "typed"}
	run := func(t *testing.T, mode, stage string) (string, string, error) {
		t.Helper()
		pipeline := bin + " from csv " + csvPath + " | " + bin + " " + stage + " | " + bin + " to csv"
		if mode != "" {
			pipeline = "export SSQL_MODE=" + mode + " && (" + pipeline + ") | " + bin + " generate go -run"
		}
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+pipeline)
		cmd.Env = append(os.Environ(), "SSQL_MODULE_DIR="+mustRepoRoot(t))
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		return out.String(), errb.String(), err
	}

	loud := map[string]string{
		"where -if age gt abc":             `"abc" is not`,
		"where +if age gt abc":             `"abc" is not`,
		"where -if age gt 3O":              `"3O" is not`,
		"where -if ok eq maybe":            `"maybe" is not`,
		"where -if age contains 3":         "needs a text field",
		"update -if age lt x -set name z":  `"x" is not`,
		"where -if-field age gt name":      "cannot be compared",
		"where -if-field age contains name": "needs two text fields",
	}
	for stage, want := range loud {
		for lane, mode := range lanes {
			out, stderr, err := run(t, mode, stage)
			if err == nil || !strings.Contains(stderr, want) {
				t.Errorf("%s [%s]: want failure containing %q, got err=%v\nstdout: %.200s\nstderr: %.300s", stage, lane, want, err, out, stderr)
			}
		}
	}

	valid := map[string]string{
		"where -if age gt 29.5":     "id,age,name,code,ok\n1,30,bob,007,true\n3,41,cal,x9,true\n",
		"where -if code gt 10":      "id,age,name,code,ok\n2,5,amy,12,false\n3,41,cal,x9,true\n", // text: "12" > "10", "x9" > "10", "007" < "10"
		"where -if name gt 5":       "id,age,name,code,ok\n1,30,bob,007,true\n2,5,amy,12,false\n3,41,cal,x9,true\n",
		"where -if code startswith 0": "id,age,name,code,ok\n1,30,bob,007,true\n",
	}
	for stage, want := range valid {
		for lane, mode := range lanes {
			out, stderr, err := run(t, mode, stage)
			if err != nil {
				t.Errorf("%s [%s]: %v\n%s", stage, lane, err, stderr)
				continue
			}
			if equivNormaliseCSV(out) != equivNormaliseCSV(want) {
				t.Errorf("%s [%s]:\n got %q\nwant %q", stage, lane, out, want)
			}
		}
	}

	// An empty literal against a non-text field is false, not an error:
	// no data rows, in every lane. (A generated record program writes no
	// header for an empty result, exec writes the schema's: known shape.)
	for lane, mode := range lanes {
		out, stderr, err := run(t, mode, "where -if age eq ''")
		if err != nil || strings.Count(strings.TrimSpace(out), "\n") > 0 {
			t.Errorf("age eq '' [%s]: want success with no rows, got %v\n%s\n%s", lane, err, out, stderr)
		}
	}
}

// equivNormaliseCSV sorts columns within each row by header so the record
// lane's alphabetical column order compares equal to exec's.
func equivNormaliseCSV(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	header := strings.Split(lines[0], ",")
	idx := make([]int, len(header))
	sorted := append([]string(nil), header...)
	sortStrings(sorted)
	for i, h := range sorted {
		for j, o := range header {
			if o == h {
				idx[i] = j
			}
		}
	}
	var out []string
	for _, l := range lines {
		cells := strings.Split(l, ",")
		row := make([]string, len(idx))
		for i, j := range idx {
			if j < len(cells) {
				row[i] = cells[j]
			}
		}
		out = append(out, strings.Join(row, ","))
	}
	return strings.Join(out, "\n")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
