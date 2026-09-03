package main

// `from csv FILE -last N` must produce exactly what `| limit -last N`
// produces — the seek path is an optimisation, never a semantics
// change. Pinned for csv and tsv through the built binary.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFromLastEqualsLimitLast(t *testing.T) {
	bin := buildSSQLForTypedTest(t)
	dir := t.TempDir()
	var csv, tsv strings.Builder
	csv.WriteString("id,name,score\n")
	tsv.WriteString("id\tname\tscore\n")
	for i := 1; i <= 3000; i++ {
		csv.WriteString(strings.Repeat("n", i%7) + "," + "x,")
		csv.WriteString(strings.TrimSpace(strings.Repeat(" ", 0)) + itoa(i) + "\n")
		tsv.WriteString(itoa(i) + "\tx\t" + itoa(i*2) + "\n")
	}
	// fix csv: id first
	lines := strings.Split(strings.TrimSpace(csv.String()), "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = itoa(i) + ",x," + itoa(i*3)
	}
	cp := filepath.Join(dir, "d.csv")
	tp := filepath.Join(dir, "d.tsv")
	os.WriteFile(cp, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	os.WriteFile(tp, []byte(tsv.String()), 0o644)
	for _, c := range []struct{ fmt, path string }{{"csv", cp}, {"tsv", tp}} {
		for _, n := range []string{"1", "7", "2999", "5000"} {
			a, err := exec.Command("bash", "-c", bin+" from "+c.fmt+" "+c.path+" -last "+n+" | "+bin+" to csv").Output()
			if err != nil {
				t.Fatalf("%s -last %s: %v", c.fmt, n, err)
			}
			b, err := exec.Command("bash", "-c", bin+" from "+c.fmt+" "+c.path+" | "+bin+" limit -last "+n+" | "+bin+" to csv").Output()
			if err != nil {
				t.Fatalf("%s limit -last %s: %v", c.fmt, n, err)
			}
			if string(a) != string(b) {
				t.Errorf("%s n=%s: from -last differs from limit -last\n--- from -last:\n%s\n--- limit -last:\n%s", c.fmt, n, a, b)
			}
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
