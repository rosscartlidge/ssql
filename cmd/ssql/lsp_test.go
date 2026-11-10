package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// lspClient drives `ssql lsp` over its stdio, as an editor does.
type lspClient struct {
	t      *testing.T
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	nextID int
	notes  []map[string]any // notifications received while waiting for replies
}

func startLSP(t *testing.T, bin, dir string) *lspClient {
	t.Helper()
	cmd := exec.Command(bin, "lsp")
	cmd.Dir = dir
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &lspClient{t: t, cmd: cmd, in: in, out: bufio.NewReader(out)}
	t.Cleanup(func() {
		c.send(map[string]any{"jsonrpc": "2.0", "method": "exit"})
		in.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			t.Error("ssql lsp did not exit after exit")
		}
	})
	c.request("initialize", map[string]any{"rootUri": "file://" + dir})
	c.send(map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}})
	return c
}

func (c *lspClient) send(m map[string]any) {
	data, _ := json.Marshal(m)
	fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(data), data)
}

func (c *lspClient) read() map[string]any {
	c.t.Helper()
	n := -1
	for {
		line, err := c.out.ReadString('\n')
		if err != nil {
			c.t.Fatalf("reading from ssql lsp: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			n, _ = strconv.Atoi(v)
		}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.out, body); err != nil {
		c.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		c.t.Fatalf("%v: %s", err, body)
	}
	return m
}

func (c *lspClient) request(method string, params any) any {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	for {
		m := c.read()
		if m["id"] == nil {
			c.notes = append(c.notes, m)
			continue
		}
		if int(m["id"].(float64)) != id {
			continue
		}
		if e, ok := m["error"]; ok {
			c.t.Fatalf("%s: %v", method, e)
		}
		return m["result"]
	}
}

func (c *lspClient) open(uri, text string) {
	c.send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": text, "version": 1, "languageId": "sh"},
	}})
}

// diagnostics waits for the check of uri's version 1 to be published.
func (c *lspClient) diagnostics(uri string) []any {
	c.t.Helper()
	for {
		for i, n := range c.notes {
			if n["method"] == "textDocument/publishDiagnostics" {
				p := n["params"].(map[string]any)
				if p["uri"] == uri {
					c.notes = append(c.notes[:i], c.notes[i+1:]...)
					return p["diagnostics"].([]any)
				}
			}
		}
		c.notes = append(c.notes, c.read())
	}
}

func (c *lspClient) at(method, uri, text string, off int) any {
	c.t.Helper()
	line, char := lspPos(text, off)
	return c.request(method, map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": char},
	})
}

// lspPos is the LSP position of byte offset off (the texts here are
// ASCII, so a character is a byte).
func lspPos(text string, off int) (int, int) {
	line := strings.Count(text[:off], "\n")
	return line, off - (strings.LastIndex(text[:off], "\n") + 1)
}

func labels(result any) []string {
	var out []string
	items, _ := result.([]any)
	for _, it := range items {
		out = append(out, it.(map[string]any)["label"].(string))
	}
	return out
}

func hoverText(result any) string {
	m, _ := result.(map[string]any)
	if m == nil {
		return ""
	}
	return m["contents"].(map[string]any)["value"].(string)
}

// shellWords are the words of shell text as [start, end) byte offsets:
// runs of anything but blanks, `|`, `\`-newline and the `<(` / `)` of a
// process substitution, a quoted run belonging to its word.
func shellWords(text string) [][2]int {
	var words [][2]int
	start := -1
	var quote byte
	end := func(i int) {
		if start >= 0 {
			words = append(words, [2]int{start, i})
			start = -1
		}
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			continue
		case c == '\'' || c == '"':
			quote = c
		case c == ' ' || c == '\t' || c == '\n' || c == '|' || c == ')':
			end(i)
			continue
		case c == '\\' && i+1 < len(text) && text[i+1] == '\n':
			end(i)
			continue
		case c == '<' && i+1 < len(text) && text[i+1] == '(':
			end(i)
			i++
			continue
		}
		if start < 0 {
			start = i
		}
	}
	end(len(text))
	return words
}

// TestLSPMatchesOneLine is the position map's oracle against the
// commands' own protocols: at every word of a pipeline laid out by
// generate pretty, the server's completion (at the word's end) and hover
// (at its start) are the answers it gives at the same word of the
// pipeline on one line. The layouts' breaks, indentation, trailing
// pipes and process substitutions over lines are exactly where a map
// goes wrong.
func TestLSPMatchesOneLine(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	for name, body := range prettyFixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := startLSP(t, bin, dir)
	for _, pc := range prettyCases {
		for _, width := range []string{"40", "80"} {
			cmd := exec.Command(bin, "generate", "pretty", "-width", width, "-pipeline", pc.pipeline)
			cmd.Dir = dir
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s: generate pretty: %v", pc.name, err)
			}
			pretty, one := string(out), pc.pipeline+"\n"
			pw, ow := shellWords(pretty), shellWords(one)
			if len(pw) != len(ow) {
				t.Fatalf("%s/%s: %d words laid out, %d on one line", pc.name, width, len(pw), len(ow))
			}
			uriP := fmt.Sprintf("file://%s/%s-%s-pretty.sh", dir, strings.ReplaceAll(pc.name, " ", "_"), width)
			uriO := fmt.Sprintf("file://%s/%s-%s-one.sh", dir, strings.ReplaceAll(pc.name, " ", "_"), width)
			c.open(uriP, pretty)
			c.open(uriO, one)
			for k := range pw {
				word := pretty[pw[k][0]:pw[k][1]]
				if one[ow[k][0]:ow[k][1]] != word {
					t.Fatalf("%s/%s: word %d is %q laid out, %q on one line", pc.name, width, k, word, one[ow[k][0]:ow[k][1]])
				}
				// As sets: autocli lists some candidates (command names) in
				// no fixed order.
				got := labels(c.at("textDocument/completion", uriP, pretty, pw[k][1]))
				want := labels(c.at("textDocument/completion", uriO, one, ow[k][1]))
				slices.Sort(got)
				slices.Sort(want)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s/%s: completion at the end of %q (word %d): %v laid out, %v on one line", pc.name, width, word, k, got, want)
				}
				gh := hoverText(c.at("textDocument/hover", uriP, pretty, pw[k][0]))
				wh := hoverText(c.at("textDocument/hover", uriO, one, ow[k][0]))
				if gh != wh {
					t.Errorf("%s/%s: hover at %q (word %d) differs laid out and on one line:\n%.200s\n---\n%.200s", pc.name, width, word, k, gh, wh)
				}
			}
		}
	}
}

// TestLSP: what the server answers, by kind. Completion of a command, a
// flag, a field flowing in from upstream (through a join), a field of a
// join's file, a value from the upstream's sample, and a field inside an
// expression; hover on a flag and on a command; formatting; diagnostics
// for a refused flag and an unknown field, none for a good pipeline, a
// hint and no check for one that reaches another host, and no run of a
// $(…) whatever is asked.
func TestLSP(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	for name, body := range prettyFixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := startLSP(t, bin, dir)
	uri := "file://" + dir + "/doc.sh"
	marker := filepath.Join(dir, "substituted")
	doc := strings.Join([]string{
		"ssql from csv emp.csv |",                                     // 0
		"  ssql join dept.csv -using dept |",                          // 1
		"  ssql where -if floor gt 1 -if dept eq eng |",               // 2
		"  ssql update -set-expr senior 'salary > 95 && floor > 1' |", // 3
		"  ssql sort -desc salary |",                                  // 4
		"  ssql to csv",                                               // 5
		"",                                                            // 6
		"ssql from csv emp.csv | ssql where -if nosuch eq 1 | ssql to csv",                       // 7
		"ssql from csv emp.csv | ssql sort -bogus x",                                             // 8
		"ssql from ssh host1 /data/x.csv | ssql where -if nosuch eq 1",                           // 9
		`ssql from csv emp.csv | ssql where -if name eq "$(touch ` + marker + `)" | ssql to csv`, // 10
		"",
	}, "\n")
	c.open(uri, doc)
	lineStart := func(n int) int {
		off := 0
		for i := 0; i < n; i++ {
			off += strings.IndexByte(doc[off:], '\n') + 1
		}
		return off
	}
	at := func(line int, after string) int { // the offset just past after on line
		s := lineStart(line)
		i := strings.Index(doc[s:], after)
		if i < 0 {
			t.Fatalf("%q not on line %d", after, line)
		}
		return s + i + len(after)
	}
	has := func(what string, got []string, want ...string) {
		t.Helper()
		for _, w := range want {
			found := false
			for _, g := range got {
				found = found || g == w
			}
			if !found {
				t.Errorf("%s: %q missing from %v", what, w, got)
			}
		}
	}

	// Where a stage starts: at the end of a line ending in ` |`, with the
	// blank it needs, and on a word ssql begins with.
	eol := c.at("textDocument/completion", uri, doc, at(0, "emp.csv |"))
	if items, _ := eol.([]any); len(items) != 1 || items[0].(map[string]any)["textEdit"].(map[string]any)["newText"] != " ssql " {
		t.Errorf("after a pipe at the end of a line: %v, want ssql with a blank before it", eol)
	}
	has("ssql begun", labels(c.at("textDocument/completion", uri, doc, at(4, "  ss"))), "ssql")
	has("a command", labels(c.at("textDocument/completion", uri, doc, at(4, "ssql so"))), "sort")
	has("a flag", labels(c.at("textDocument/completion", uri, doc, at(4, "-de"))), "-desc")
	// floor comes from the join's file: the field flowed in from upstream.
	has("an upstream field", labels(c.at("textDocument/completion", uri, doc, at(2, "-if fl"))), "floor")
	has("a field after the join", labels(c.at("textDocument/completion", uri, doc, at(4, "-desc "))), "floor", "salary", "name")
	has("a value", labels(c.at("textDocument/completion", uri, doc, at(2, "dept eq "))), "eng", "ops")
	has("a field in an expression", labels(c.at("textDocument/completion", uri, doc, at(3, "'salary > 95 && fl"))), "floor")

	if h := hoverText(c.at("textDocument/hover", uri, doc, at(4, "-de"))); !strings.Contains(h, "-desc") {
		t.Errorf("hover on -desc: %q", h)
	}
	if h := hoverText(c.at("textDocument/hover", uri, doc, at(4, "so"))); !strings.HasPrefix(h, "ssql sort") {
		t.Errorf("hover on sort is not sort's help: %.80q", h)
	}

	edits, _ := c.request("textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": uri}}).([]any)
	var newTexts []string
	for _, e := range edits {
		newTexts = append(newTexts, e.(map[string]any)["newText"].(string))
	}
	// Lines 7 and 9 are laid out (describing `from ssh` connects to
	// nothing); 0–5 are already, 8 is refused and 10 holds a $(…).
	if len(newTexts) != 2 || !strings.HasPrefix(newTexts[0], "ssql from csv emp.csv |\n  ssql where -if nosuch eq 1 |\n") ||
		!strings.HasPrefix(newTexts[1], "ssql from ssh host1 /data/x.csv |\n") {
		t.Errorf("formatting: edits for lines 7 and 9 only; got %q", newTexts)
	}

	diags := c.diagnostics(uri)
	byLine := map[int]string{}
	for _, d := range diags {
		m := d.(map[string]any)
		r := m["range"].(map[string]any)["start"].(map[string]any)
		byLine[int(r["line"].(float64))] = fmt.Sprintf("%v %v", m["severity"], m["message"])
	}
	for line, want := range map[int]string{
		7: `1 ssql where: where references unknown field(s): nosuch`,
		8: `1 ssql sort: flag -bogus: unknown flag`,
		9: `4 fields are not checked while you type`,
	} {
		if !strings.HasPrefix(byLine[line], want) {
			t.Errorf("line %d: diagnostic %q, want %q…", line, byLine[line], want)
		}
	}
	if len(diags) != 3 {
		t.Errorf("%d diagnostics, want 3 (lines 7, 8, 9): %v", len(diags), byLine)
	}
	// Line 10's $(…) runs only when bash runs the line.
	c.at("textDocument/completion", uri, doc, at(10, "-if name eq "))
	if _, err := os.Stat(marker); err == nil {
		t.Error("the server ran a command substitution")
	}
}
