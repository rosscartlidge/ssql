package commands

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Shell layout for the language server (DFC167 §3.2). An editor holds a
// pipeline as multi-line shell text — laid out by `generate pretty`, or
// typed that way in a script — while every cursor protocol the commands
// serve (-complete, -help-at-cursor, -complete-source, -value-sample)
// takes the one-line text up to the cursor, as the bash bindings pass
// it. These helpers map between the two. They know the shell's LAYOUT
// only — `\`-newline, a newline after `|`, `&&` or `||`, an open `(` or
// quote, a `#` comment — never a command's grammar, which stays with the
// commands (DFC115).

// shellLogical is one logical command of a document: the doc text from
// start to end, joined onto one line as bash reads it, with the doc
// offset of each joined byte.
type shellLogical struct {
	start, end int    // byte offsets in the document
	joined     string // the command on one line
	toDoc      []int  // toDoc[j]: doc offset of joined[j]; len(joined)+1 entries, the last is end
	comment    bool   // a # comment was dropped from the joined text
}

// joinedAt is the joined offset of a cursor at doc offset off: the number
// of joined bytes that come before it, so joined[:joinedAt(off)] is the
// line up to the cursor.
func (l shellLogical) joinedAt(off int) int {
	return sort.Search(len(l.joined), func(j int) bool { return l.toDoc[j] >= off })
}

// docAt is the doc offset of joined offset j (the end of the command for
// j at or past its end).
func (l shellLogical) docAt(j int) int {
	if j >= len(l.joined) {
		return l.end
	}
	return l.toDoc[j]
}

// logicalCommands splits shell text into its logical commands. A command
// continues past a newline that is quoted, that follows an unquoted
// `\` (both are dropped, and the indentation that follows becomes one
// space when either side had one: bash's own reading), that follows a
// `|`, `&&` or `||`, or that falls inside an open `(`; at any other
// newline it ends. Blank lines and `#` comments belong to no command.
func logicalCommands(text string) []shellLogical {
	var out []shellLogical
	var cur shellLogical
	var joined []byte
	in := false
	var quote byte
	depth := 0
	add := func(c byte, at int) {
		joined = append(joined, c)
		cur.toDoc = append(cur.toDoc, at)
	}
	lastSig := func() string { // the last two non-blank joined bytes
		j := len(joined)
		for j > 0 && (joined[j-1] == ' ' || joined[j-1] == '\t') {
			j--
		}
		return string(joined[max(0, j-2):j])
	}
	endsBlank := func() bool {
		return len(joined) > 0 && (joined[len(joined)-1] == ' ' || joined[len(joined)-1] == '\t')
	}
	// skipIndent moves past the blanks at i; true when there were any.
	skipIndent := func(i int) (int, bool) {
		j := i
		for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
			j++
		}
		return j, j > i
	}
	finish := func(end int) {
		cur.end = end
		cur.joined = string(joined)
		cur.toDoc = append(cur.toDoc, end)
		out = append(out, cur)
		cur, joined, in, quote, depth = shellLogical{}, nil, false, 0, 0
	}
	newlineAt := func(i int) (int, bool) { // the length of a newline at i: \n or \r\n
		if text[i] == '\n' {
			return 1, true
		}
		if text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n' {
			return 2, true
		}
		return 0, false
	}
	skipComment := func(i int) int {
		for i < len(text) && text[i] != '\n' && !(text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n') {
			i++
		}
		return i
	}
	for i := 0; i < len(text); {
		c := text[i]
		if !in {
			switch {
			case c == ' ' || c == '\t' || c == '\n' || c == '\r':
				i++
			case c == '#':
				i = skipComment(i)
			default:
				in = true
				cur.start = i
			}
			continue
		}
		switch {
		case quote == '\'':
			add(c, i)
			if c == '\'' {
				quote = 0
			}
			i++
		case c == '\\':
			if i+1 < len(text) {
				if n, ok := newlineAt(i + 1); ok {
					// A line continuation: bash drops the backslash and
					// the newline; blanks on either side still separate
					// words, and become one.
					j, blankAfter := skipIndent(i + 1 + n)
					if blankAfter && !endsBlank() {
						add(' ', i)
					}
					i = j
					continue
				}
			}
			add(c, i)
			if i+1 < len(text) {
				add(text[i+1], i+1)
			}
			i += 2
		case quote == '"':
			add(c, i)
			if c == '"' {
				quote = 0
			}
			i++
		case c == '\'' || c == '"':
			quote = c
			add(c, i)
			i++
		case c == '(':
			depth++
			add(c, i)
			i++
		case c == ')':
			depth = max(0, depth-1)
			add(c, i)
			i++
		case c == '#' && (len(joined) == 0 || endsBlank()):
			cur.comment = true
			i = skipComment(i)
		default:
			n, nl := newlineAt(i)
			if !nl {
				add(c, i)
				i++
				continue
			}
			if s := lastSig(); depth > 0 || (len(s) > 0 && s[len(s)-1] == '|') || s == "&&" {
				// The command goes on: the newline reads as a blank.
				if !endsBlank() {
					add(' ', i)
				}
				j, _ := skipIndent(i + n)
				i = j
				continue
			}
			finish(i)
			i += n
		}
	}
	if in {
		finish(len(text))
	}
	return out
}

// logicalAt is the logical command holding doc offset off — the cursor
// may sit just past its last byte — and false when off is on a blank
// line, in a comment, or between commands.
func logicalAt(cmds []shellLogical, off int) (shellLogical, bool) {
	for _, c := range cmds {
		if off >= c.start && off <= c.end {
			return c, true
		}
	}
	return shellLogical{}, false
}

// lspOffset is the byte offset of an LSP position: a zero-based line and
// a character counted in UTF-16 code units, the protocol's default
// encoding. A character past the end of its line is the line's end.
func lspOffset(text string, line, char int) int {
	off := 0
	for l := 0; l < line; l++ {
		i := indexByteFrom(text, off, '\n')
		if i < 0 {
			return len(text)
		}
		off = i + 1
	}
	for units := 0; off < len(text) && text[off] != '\n' && !(text[off] == '\r' && off+1 < len(text) && text[off+1] == '\n'); {
		r, size := utf8.DecodeRuneInString(text[off:])
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > char {
			break
		}
		units += w
		off += size
	}
	return off
}

// lspPosition is the LSP position (line, UTF-16 character) of a byte
// offset.
func lspPosition(text string, off int) (line, char int) {
	off = min(off, len(text))
	start := 0
	for i := 0; i < off; i++ {
		if text[i] == '\n' {
			line++
			start = i + 1
		}
	}
	for _, r := range text[start:off] {
		if r >= 0x10000 {
			char += 2
		} else {
			char++
		}
	}
	return line, char
}

func indexByteFrom(s string, from int, b byte) int {
	for i := from; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// shellWordValue is the argument bash passes for one word of a stage, and
// false when the word's value depends on more than its quoting: an
// expansion (`$`, a backtick), a glob, a leading `~`. Single quotes are
// literal; double quotes keep their text, with `\` escaping only " \ $ `
// and newline; an unquoted `\` makes the next byte literal. A diagnostic
// on a word whose value is not known would be a guess, so such a stage
// is not checked.
func shellWordValue(tok string) (string, bool) {
	var b strings.Builder
	if strings.HasPrefix(tok, "~") {
		return "", false
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		switch c {
		case '\'':
			j := strings.IndexByte(tok[i+1:], '\'')
			if j < 0 {
				return "", false
			}
			b.WriteString(tok[i+1 : i+1+j])
			i += j + 1
		case '"':
			i++
			for ; i < len(tok) && tok[i] != '"'; i++ {
				switch d := tok[i]; {
				case d == '$' || d == '`':
					return "", false
				case d == '\\' && i+1 < len(tok) && strings.IndexByte("\"\\$`\n", tok[i+1]) >= 0:
					i++
					b.WriteByte(tok[i])
				default:
					b.WriteByte(d)
				}
			}
			if i >= len(tok) {
				return "", false
			}
		case '\\':
			if i+1 >= len(tok) {
				return "", false
			}
			i++
			b.WriteByte(tok[i])
		case '$', '`', '*', '?', '[':
			return "", false
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

// docStagesFromText is the pipeline document of shell text holding only
// ssql stages — the stages and their arguments as bash would pass them,
// a `<(…)` a nested pipeline — and false when any word's value is not
// known from its text (shellWordValue) or a stage is not ssql. It reads
// shell layout only: what the arguments mean is for the commands to say
// (checkPipelineDoc, checkPipelineFields).
func docStagesFromText(text string) ([]docStage, bool) {
	var stages []docStage
	for _, seg := range splitTopLevelPipes(text) {
		toks := tokenizeStage(strings.TrimSpace(seg))
		if len(toks) < 2 || (toks[0] != "ssql" && toks[0] != "ssql_gpu") {
			return nil, false
		}
		var st docStage
		for _, tok := range toks[1:] {
			if strings.HasPrefix(tok, "<(") && strings.HasSuffix(tok, ")") {
				nested, ok := docStagesFromText(tok[2 : len(tok)-1])
				if !ok {
					return nil, false
				}
				st = append(st, docArg{Nested: nested})
				continue
			}
			v, ok := shellWordValue(tok)
			if !ok {
				return nil, false
			}
			st = append(st, docArg{Text: v})
		}
		stages = append(stages, st)
	}
	return stages, len(stages) > 0
}
