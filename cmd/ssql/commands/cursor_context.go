package commands

import "strings"

// Cursor-context analysis for the interactive keybindings (Ctrl-O field
// completion, Alt-h help-at-cursor). The bash bindings used to split the line
// on the last "|" with `${line%|*}` / `${line##*|}`, which is NOT paren-aware:
// a "|" inside a process substitution `<(ssql … | ssql …)` was mistaken for a
// top-level pipe, producing a malformed upstream. These helpers do the split
// in Go (paren-aware, unit-tested) and the bindings call them via the
// `-cursor-stage` / `-complete-source` protocol flags.

// splitTopLevelPipes splits s on "|" at paren depth 0, so pipes inside
// <(...) / (...) are left intact. Always returns at least one segment.
func splitTopLevelPipes(s string) []string {
	var segs []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '|':
			if depth == 0 {
				segs = append(segs, s[start:i])
				start = i + 1
			}
		}
	}
	return append(segs, s[start:])
}

// enclosingProcsub returns the content of the innermost UNCLOSED "<(" in s
// (text up to the cursor) and true — i.e. the cursor is inside a process
// substitution — or "" and false when the cursor is at top level.
func enclosingProcsub(s string) (string, bool) {
	type frame struct {
		start  int
		isProc bool
	}
	var stack []frame
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			stack = append(stack, frame{i + 1, i > 0 && s[i-1] == '<'})
		case ')':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	for j := len(stack) - 1; j >= 0; j-- {
		if stack[j].isProc {
			return s[stack[j].start:], true
		}
	}
	return "", false
}

// firstProcsub returns the content of the first complete <(...) in s, or "".
func firstProcsub(s string) string {
	i := strings.Index(s, "<(")
	if i < 0 {
		return ""
	}
	depth := 0
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[i+2 : j]
			}
		}
	}
	return ""
}

// tokenizeStage splits a stage on whitespace at paren depth 0, keeping a
// <(...) process substitution as a single token.
func tokenizeStage(s string) []string {
	var toks []string
	var cur strings.Builder
	depth := 0
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '(':
			depth++
			cur.WriteByte(c)
		case c == ')':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case (c == ' ' || c == '\t') && depth == 0:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return toks
}

// joinRightFieldSlot reports whether the cursor (end of stage) sits at a join
// "right-side" field — the 2nd argument of -on (`-on <left> <RIGHT>`) or the
// 1st argument of -as (`-as <RIGHT> <new-name>`). Those fields come from the
// join's right source (the <(…)> FILE), not the upstream pipeline. Clause
// separators (+ / -) reset the per-clause flag state.
func joinRightFieldSlot(stage string) bool {
	toks := tokenizeStage(stage)
	ji := -1
	for i, t := range toks {
		if t == "join" {
			ji = i
			break
		}
	}
	if ji < 0 || ji+1 > len(toks) {
		return false
	}
	// Completed tokens after "join", excluding the partial under the cursor.
	consider := toks[ji+1:]
	if !strings.HasSuffix(stage, " ") && !strings.HasSuffix(stage, "\t") && len(consider) > 0 {
		consider = consider[:len(consider)-1]
	}
	flag := ""
	seen := 0 // positional args seen for the current flag, before the cursor
	for _, t := range consider {
		switch {
		case t == "+" || t == "-":
			flag, seen = "", 0
		case len(t) > 1 && t[0] == '-':
			flag, seen = t, 0
		default:
			seen++
		}
	}
	return (flag == "-on" && seen == 1) || (flag == "-as" && seen == 0)
}

// upstreamOf returns everything before the last top-level pipe of s (the full
// upstream pipeline whose schema flows into the final stage), or "" if there
// is no top-level pipe.
func upstreamOf(s string) string {
	segs := splitTopLevelPipes(s)
	if len(segs) < 2 {
		return ""
	}
	return strings.TrimSpace(strings.Join(segs[:len(segs)-1], "|"))
}

// CursorTopLevelStage returns the current pipeline stage at the cursor —
// paren-aware. If the cursor is inside a process substitution, the stage is the
// current stage WITHIN that procsub. Leading whitespace is trimmed; a trailing
// space is preserved (the bindings use it to tell "on a new empty word").
func CursorTopLevelStage(before string) string {
	scope := before
	if p, ok := enclosingProcsub(before); ok {
		scope = p
	}
	segs := splitTopLevelPipes(scope)
	return strings.TrimLeft(segs[len(segs)-1], " \t")
}

// ExprArgAtCursor reports whether the cursor sits on an expression argument of
// an expression-bearing flag — so the Alt-h help can append the function
// reference (writing an expression is hard without knowing the functions).
// args is the COMP_WORDS-style slice MINUS the program name (args[0] is the
// command), and pos is the COMP_WORDS index of the cursor (program at 0), the
// same shape autocli's -help-at / Complete use. Expression slots:
//
//	where    -if-expr|-x  <EXPR>
//	update   -if-expr|-x  <EXPR>            -set-expr|-e <field> <EXPR>
//	group-by -expr|-e     <EXPR> <name>     -stream-expr <init> <every> <final> <name>
func ExprArgAtCursor(args []string, pos int) bool {
	if len(args) == 0 {
		return false
	}
	cmd := args[0]
	isExprSlot := func(flag string, argIdx int) bool {
		switch cmd {
		case "where":
			return (flag == "-if-expr" || flag == "-x") && argIdx == 0
		case "update":
			if (flag == "-if-expr" || flag == "-x") && argIdx == 0 {
				return true
			}
			return (flag == "-set-expr" || flag == "-e") && argIdx == 1
		case "group-by":
			if (flag == "-expr" || flag == "-e") && argIdx == 0 {
				return true
			}
			return flag == "-stream-expr" && argIdx >= 0 && argIdx <= 2
		}
		return false
	}
	// Walk the completed args before the cursor word (at index pos-1), tracking
	// the current flag and how many positionals it has consumed.
	end := pos - 1
	if end > len(args) {
		end = len(args)
	}
	flag := ""
	argIdx := -1
	for i := 1; i < end; i++ {
		t := args[i]
		switch {
		case t == "+" || t == "-": // clause separators reset the flag
			flag, argIdx = "", -1
		case len(t) > 1 && t[0] == '-':
			flag, argIdx = t, -1
		default:
			argIdx++
		}
	}
	// The cursor word occupies the next positional slot of the current flag.
	return flag != "" && isExprSlot(flag, argIdx+1)
}

// CompleteSource returns the shell command whose `SSQL_MODE=schema` output
// should drive field-NAME completion at the cursor, or "" if none applies:
//   - cursor inside a process substitution → that procsub's internal upstream;
//   - cursor at a join right-side field slot → the join's procsub (right source);
//   - otherwise → the upstream pipeline feeding the current stage.
func CompleteSource(before string) string {
	if p, ok := enclosingProcsub(before); ok {
		return upstreamOf(p)
	}
	segs := splitTopLevelPipes(before)
	stage := strings.TrimLeft(segs[len(segs)-1], " \t")
	if joinRightFieldSlot(stage) {
		if ps := firstProcsub(stage); ps != "" {
			return strings.TrimSpace(ps)
		}
	}
	return upstreamOf(before)
}
