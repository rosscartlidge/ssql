package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestGeneratePretty: `generate pretty` writes the pipeline as written,
// one stage to a line, within -width columns, as a bash pipeline. The
// oracle that it changes nothing is the pipeline document: the pretty
// text, run back through -pipeline and -script, gives the stages the
// same argv as the original (`generate json`). It also runs under bash
// to the same output, lays itself out unchanged, and every line over
// the width is one the warning names.
func TestGeneratePretty(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	for name, body := range map[string]string{
		"emp.csv":  "id,name,dept,salary\n1,ann,eng,100\n2,bob,ops,90\n3,cy,eng,120\n",
		"dept.csv": "dept,floor\neng,3\nops,1\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(t *testing.T, args ...string) (stdout, stderr string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		var o, e strings.Builder
		cmd.Stdout, cmd.Stderr = &o, &e
		if err := cmd.Run(); err != nil {
			t.Fatalf("ssql %s: %v\n%s", strings.Join(args, " "), err, e.String())
		}
		return o.String(), e.String()
	}
	bash := func(t *testing.T, script string) string {
		t.Helper()
		cmd := exec.Command("bash", "-c", "set -o pipefail\n"+script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+":"+os.Getenv("PATH"), "SSQL_MODE=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bash: %v\n%s\n%s", err, script, out)
		}
		return string(out)
	}

	cases := []struct {
		name, pipeline string
		stages         int // top-level stages
	}{
		{"short", "ssql from csv emp.csv | ssql to csv", 2},
		{"join clauses", "ssql from csv emp.csv | ssql join dept.csv -on dept dept -as floor floor_a - -on dept dept -as floor floor_b | ssql include id name dept salary floor_a floor_b | ssql where -if salary gt 50 -if floor_a ge 1 | ssql to csv", 5},
		{"process substitution", `ssql from csv emp.csv | ssql join <(ssql from csv dept.csv | ssql where -if floor gt 0 | ssql update -set-expr label 'dept + "-" + string(floor)') -using dept | ssql update -set-expr bonus 'salary * 0.1 + (salary > 100 ? 5 : 0)' -set-expr band 'salary >= 110 ? "high" : (salary >= 95 ? "mid" : "low")' | ssql sort -desc salary | ssql to csv`, 5},
		{"union", "ssql from csv emp.csv | ssql union -file <(ssql from csv emp.csv | ssql where -if dept eq ops | ssql update -set name renamed_for_the_union_branch) | ssql sort id name | ssql to csv", 4},
		{"long expression", `ssql from csv emp.csv | ssql where -if-expr 'salary > 95 && dept == "eng" && name != "zed" && id > 0 && id < 1000' | ssql to csv`, 3},
	}
	warned := regexp.MustCompile(`line (\d+) is (\d+) columns`)
	for _, c := range cases {
		wantDoc, _ := run(t, "generate", "json", "-compact", "-pipeline", c.pipeline)
		wantOut := bash(t, c.pipeline)
		for _, width := range []int{40, 60, 80} {
			t.Run(c.name+"/"+strconv.Itoa(width), func(t *testing.T) {
				pretty, stderr := run(t, "generate", "pretty", "-width", strconv.Itoa(width), "-pipeline", c.pipeline)
				lines := strings.Split(strings.TrimSuffix(pretty, "\n"), "\n")

				// Within the width, except the lines the warning names.
				named := map[int]bool{}
				for _, m := range warned.FindAllStringSubmatch(stderr, -1) {
					n, _ := strconv.Atoi(m[1])
					named[n] = true
				}
				for i, l := range lines {
					over := len([]rune(l)) > width
					if over != named[i+1] {
						t.Errorf("line %d is %d columns (width %d), warned %v:\n%s\nstderr: %s", i+1, len([]rune(l)), width, named[i+1], pretty, stderr)
					}
				}
				// A line over the width holds only the argument the warning
				// names (no break could have shortened it).
				for _, m := range regexp.MustCompile(`line (\d+) is \d+ columns: (.*) is longer than a line`).FindAllStringSubmatch(stderr, -1) {
					n, _ := strconv.Atoi(m[1])
					word := strings.TrimSuffix(m[2], "...")
					rest := strings.TrimSpace(lines[n-1])
					rest = strings.TrimSuffix(strings.TrimSuffix(rest, " \\"), " |")
					if !strings.HasPrefix(rest, word) {
						t.Errorf("line %d runs over with more than the one argument:\n%s", n, lines[n-1])
					}
				}

				// One stage to a line: the first at column 0, the rest at 2.
				if !strings.HasPrefix(lines[0], "ssql ") {
					t.Errorf("the first stage opens the first line:\n%s", pretty)
				}
				n := 0
				for _, l := range lines {
					if strings.HasPrefix(l, "  ssql ") {
						n++
					}
				}
				if n != c.stages-1 {
					t.Errorf("%d stage lines after the first, want %d:\n%s", n, c.stages-1, pretty)
				}

				// The same stages, word for word, through -pipeline and -script.
				if got, _ := run(t, "generate", "json", "-compact", "-pipeline", pretty); got != wantDoc {
					t.Errorf("-pipeline: the stages differ\n got %s\nwant %s\n%s", got, wantDoc, pretty)
				}
				script := filepath.Join(dir, "p.sh")
				if err := os.WriteFile(script, []byte(pretty), 0o644); err != nil {
					t.Fatal(err)
				}
				if got, _ := run(t, "generate", "json", "-compact", "-script", script); got != wantDoc {
					t.Errorf("-script: the stages differ\n got %s\nwant %s", got, wantDoc)
				}
				// Laid out again, unchanged.
				if again, _ := run(t, "generate", "pretty", "-width", strconv.Itoa(width), "-pipeline", pretty); again != pretty {
					t.Errorf("not stable:\n%s\nthen\n%s", pretty, again)
				}
				// Bash runs it to the same output.
				if got := bash(t, pretty); got != wantOut {
					t.Errorf("bash output differs\n got %q\nwant %q", got, wantOut)
				}
			})
		}
	}

	t.Run("as written", func(t *testing.T) {
		// No -tz the source pinned for itself; a join file stays a file.
		out, _ := run(t, "generate", "pretty", "-pipeline", "ssql from csv emp.csv | ssql join dept.csv -using dept | ssql to csv")
		want := "ssql from csv emp.csv |\n  ssql join dept.csv -using dept |\n  ssql to csv\n"
		if out != want {
			t.Errorf("got\n%s\nwant\n%s", out, want)
		}
	})
	t.Run("narrow width refused", func(t *testing.T) {
		cmd := exec.Command(bin, "generate", "pretty", "-width", "20", "-pipeline", "ssql from csv emp.csv | ssql to csv")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "at least 40") {
			t.Errorf("-width 20: %v\n%s", err, out)
		}
	})
}
