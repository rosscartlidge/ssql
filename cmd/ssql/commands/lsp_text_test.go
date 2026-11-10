package commands

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// TestLogicalCommands: the joined lines are what the shell reads. The
// oracle is bash itself: each case's document and its joined commands,
// one to a line, print the same.
func TestLogicalCommands(t *testing.T) {
	cases := []struct {
		name, doc string
		want      []string
		notBash   bool // bash itself refuses the document
	}{
		{"continuation and trailing pipe", "echo one \\\n  two |\n  tr a-z A-Z\n", []string{"echo one two | tr a-z A-Z"}, false},
		{"continuation without a blank joins the word", "echo ab\\\ncd\n", []string{"echo abcd"}, false},
		{"escaped backslash ends the line", "echo a\\\\\necho b\n", []string{"echo a\\\\", "echo b"}, false},
		{"procsub over lines", "cat <(echo a |\n  tr a A)\necho next\n", []string{"cat <(echo a | tr a A)", "echo next"}, false},
		{"quoted newline stays", "echo 'a\nb' | cat\n", []string{"echo 'a\nb' | cat"}, false},
		{"double-quoted newline stays", "echo \"a\nb\" | cat\n", []string{"echo \"a\nb\" | cat"}, false},
		{"CRLF", "echo a \\\r\n  b\r\necho c\r\n", []string{"echo a b", "echo c"}, true}, // bash reads `b\r`; an editor on a CRLF file still completes
		{"comments and blank lines", "# head\necho a # tail\n\n  echo b\n", []string{"echo a ", "echo b"}, false},
		{"and-list continues", "true &&\n  echo x\n", []string{"true && echo x"}, false},
		{"or-list continues", "false ||\n  echo y\n", []string{"false || echo y"}, false},
		{"quoted pipe at the end of a line ends it", "echo 'a |'\necho b\n", []string{"echo 'a |'", "echo b"}, false},
		{"no trailing newline", "echo z", []string{"echo z"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, l := range logicalCommands(c.doc) {
				got = append(got, l.joined)
				if len(l.toDoc) != len(l.joined)+1 {
					t.Fatalf("toDoc has %d entries for %d joined bytes", len(l.toDoc), len(l.joined))
				}
				// Every joined byte other than an inserted blank is the doc's.
				for j := 0; j < len(l.joined); j++ {
					if d := c.doc[l.toDoc[j]]; d != l.joined[j] && l.joined[j] != ' ' {
						t.Errorf("joined[%d] = %q maps to doc %q", j, l.joined[j], d)
					}
				}
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
			bash := func(script string) string {
				out, err := exec.Command("bash", "-c", script).CombinedOutput()
				if err != nil {
					t.Fatalf("bash %q: %v\n%s", script, err, out)
				}
				return string(out)
			}
			if c.notBash {
				return
			}
			if a, b := bash(c.doc), bash(strings.Join(got, "\n")); a != b {
				t.Errorf("bash reads the document as %q and the joined lines as %q", a, b)
			}
		})
	}
}

// TestLogicalCommandsCursor: a cursor anywhere in a continuation line's
// indentation is after the blank that joins it, and a cursor on a blank
// line between commands is in neither.
func TestLogicalCommandsCursor(t *testing.T) {
	doc := "ssql from csv a.csv |\n  ssql where -if x eq 1 \\\n    -if y eq 2\n\nssql from csv b.csv\n"
	cmds := logicalCommands(doc)
	if len(cmds) != 2 {
		t.Fatalf("%d commands", len(cmds))
	}
	l := cmds[0]
	for _, c := range []struct {
		at, want string // the cursor sits before the first occurrence of at
	}{
		{"ssql where", "ssql from csv a.csv | "},
		{"  ssql where", "ssql from csv a.csv | "},
		{"-if y", "ssql from csv a.csv | ssql where -if x eq 1 "},
		{"eq 2", "ssql from csv a.csv | ssql where -if x eq 1 -if y "},
	} {
		off := strings.Index(doc, c.at)
		if got := l.joined[:l.joinedAt(off)]; got != c.want {
			t.Errorf("before %q: %q, want %q", c.at, got, c.want)
		}
	}
	if _, ok := logicalAt(cmds, strings.Index(doc, "\n\n")+1); ok {
		t.Error("a blank line between commands is in a command")
	}
	if got, ok := logicalAt(cmds, len(doc)-1); !ok || got.joined != "ssql from csv b.csv" {
		t.Errorf("the cursor at the end of the last line: %q %v", got.joined, ok)
	}
}

// TestLSPPositions: LSP characters are UTF-16 code units, so a character
// outside the BMP counts two, and the offset and position round-trip.
func TestLSPPositions(t *testing.T) {
	doc := "ssql where -if name eq é |\n  ssql where -if tag eq 😀x\r\nend"
	for _, c := range []struct {
		line, char int
		at         string
	}{
		{0, 0, "ssql where -if name"},
		{0, 23, "é"},
		{0, 24, " |"},
		{1, 24, "😀"},
		{1, 26, "x"},
		{2, 0, "end"},
	} {
		off := lspOffset(doc, c.line, c.char)
		if !strings.HasPrefix(doc[off:], c.at) {
			t.Errorf("(%d,%d) is at %q, want %q", c.line, c.char, doc[off:], c.at)
		}
		if l, ch := lspPosition(doc, off); l != c.line || ch != c.char {
			t.Errorf("offset %d is (%d,%d), want (%d,%d)", off, l, ch, c.line, c.char)
		}
	}
	if off := lspOffset(doc, 1, 99); !strings.HasPrefix(doc[off:], "\r\n") {
		t.Errorf("a character past the end of a CRLF line is at %q", doc[off:])
	}
}

// TestShellWordValue: a word's value is what bash passes for it — the
// oracle is bash's own printf — and a word whose value depends on an
// expansion is not known.
func TestShellWordValue(t *testing.T) {
	known := []string{
		`plain`, `'single quoted'`, `"double quoted"`, `a'b'"c"d`,
		`'salary > 100 && dept == "eng"'`, `"say \"hi\""`, `"back\\slash"`,
		`"keep \n"`, `esc\ aped`, `'it'\''s'`, `-if`, `x=1`, `"a|b"`, `''`,
	}
	for _, w := range known {
		got, ok := shellWordValue(w)
		if !ok {
			t.Errorf("%s: not known", w)
			continue
		}
		want, err := exec.Command("bash", "-c", "printf '%s' "+w).Output()
		if err != nil {
			t.Fatalf("bash %s: %v", w, err)
		}
		if got != string(want) {
			t.Errorf("%s: %q, bash passes %q", w, got, want)
		}
	}
	for _, w := range []string{`$HOME`, `"$x"`, "`date`", `*.csv`, `a?`, `[ab]`, `~/x`, `'open`, `"open`} {
		if v, ok := shellWordValue(w); ok {
			t.Errorf("%s: known as %q, but its value is the shell's", w, v)
		}
	}
}

// TestDocStagesFromText: the stages and arguments bash would pass, a
// process substitution a nested pipeline; any unknown word, or a stage
// that is not ssql, gives no document.
func TestDocStagesFromText(t *testing.T) {
	st, ok := docStagesFromText(`ssql from csv a.csv | ssql join <(ssql from csv 'b c.csv' | ssql where -if x eq 1) -using k | ssql update -set-expr y 'x + 1'`)
	if !ok {
		t.Fatal("not converted")
	}
	if got := renderPipelineDoc(st); got != `ssql from csv a.csv | ssql join <(ssql from csv 'b c.csv' | ssql where -if x eq 1) -using k | ssql update -set-expr y 'x + 1'` {
		t.Errorf("rendered back as %s", got)
	}
	for _, text := range []string{
		"ssql from csv $f | ssql to csv",
		"cat a.csv | ssql from csv -",
		"ssql from csv a.csv | ssql join <(cat b.csv) -using k",
	} {
		if _, ok := docStagesFromText(text); ok {
			t.Errorf("%q converted", text)
		}
	}
}
