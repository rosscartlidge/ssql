package commands

import (
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
)

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

// ExprArgAtCursor reports whether the cursor sits on an expression
// argument of an expression-bearing flag — so the Alt-h help can
// append the function reference. Derived from the builders'
// Arg(...).Expression() declarations via the completion engine's own
// cursor analysis (DFC116 F3): the hand-maintained (command, flag,
// argIdx) table this replaces silently missed any newly added expr
// flag. args is COMP_WORDS minus the program name; pos is the
// COMP_WORDS cursor index (program at 0) — the same shape
// autocli's -help-at / Complete use.
func ExprArgAtCursor(tree *cf.Command, args []string, pos int) bool {
	ctx, _ := tree.AnalyzeCursor(args, pos)
	return ctx.FlagSpec != nil && ctx.ArgIndex >= 0 &&
		ctx.ArgIndex < len(ctx.FlagSpec.ArgExpr) && ctx.FlagSpec.ArgExpr[ctx.ArgIndex]
}

// ValueSourceFile returns the data file feeding the cursor position, for
// field-VALUE sampling: the last token of the CompleteSource upstream
// that exists as a regular file, or "". This replaces the
// AUTOCLI_CACHE_FILE tab-completion dance for consumers that can see the
// whole pipeline (the Ctrl-O binding, the browser playground) — bash Tab
// can't, which is why the cache exists at all.
func ValueSourceFile(before string, tree *cf.Command) string {
	src := CompleteSource(before, tree)
	if src == "" {
		return ""
	}
	toks := tokenizeStage(src)
	for i := len(toks) - 1; i >= 0; i-- {
		if st, err := os.Stat(toks[i]); err == nil && st.Mode().IsRegular() {
			return toks[i]
		}
	}
	return ""
}

// CompleteSource returns the shell command whose `SSQL_MODE=schema` output
// should drive field-NAME completion at the cursor, or "" if none applies:
//   - cursor inside a process substitution → that procsub's internal upstream;
//   - cursor at a join right-side field slot → the join's procsub (right source);
//   - otherwise → the upstream pipeline feeding the current stage.
func CompleteSource(before string, tree *cf.Command) string {
	if p, ok := enclosingProcsub(before); ok {
		return upstreamOf(p)
	}
	segs := splitTopLevelPipes(before)
	stage := strings.TrimLeft(segs[len(segs)-1], " \t")
	// A field slot inside a `from` stage itself (-columns on parquet/
	// arrow): the fields come from the stage's OWN file — synthesize a
	// bare read of it (bare, so ALL columns are offered even after
	// some are picked). Found by Ross: `from parquet X -columns ^O`
	// fell through to upstreamOf, which is empty for a first stage.
	if f, fmtSub := fromOwnFileFieldSlot(stage, tree); f != "" {
		if fmtSub != "" {
			return "ssql from " + fmtSub + " " + f
		}
		return "ssql from " + f
	}
	if joinRightFieldSlot(stage) {
		if ps := firstProcsub(stage); ps != "" {
			return strings.TrimSpace(ps)
		}
		// Direct-file right side (v4.62's `join kind.csv`): the fields
		// come from that file, so the source is a synthesized read of
		// it. Without this the slot fell through to the UPSTREAM and
		// completed the left side's fields (found by Ross: `join
		// kind.csv -on a_kind ^O` offered shuffled.csv's fields).
		if f := joinRightFile(stage, tree); f != "" {
			return "ssql from " + f
		}
	}
	return upstreamOf(before)
}

// joinRightFile returns the join's direct-file right-side argument —
// the FILE positional the join builder declares, bound by the
// completion engine's parse (DFC116 F4) — or "" when the right side
// is a procsub or absent.
func joinRightFile(stage string, tree *cf.Command) string {
	args, pos, ok := stageCursorArgs(stage)
	if !ok {
		return ""
	}
	ctx, path := tree.AnalyzeCursor(args, pos)
	if len(path) != 1 || path[0] != "join" {
		return ""
	}
	f, _ := ctx.GlobalFlags["FILE"].(string)
	if f == "" || strings.HasPrefix(f, "-") || strings.HasPrefix(f, "<(") {
		return ""
	}
	if fi, ok := formatForPath(f); ok && fi.DirectAux {
		return f
	}
	return ""
}

// fromOwnFileFieldSlot reports the (file, format-subcommand) of a
// `from` stage whose cursor sits on a field slot answered by the
// stage's OWN file (e.g. parquet -columns). Derived (DFC116 F4): the
// cursor's flag spec declares FieldsFromFlag("FILE") and the engine's
// parse binds the FILE positional — this replaces a hand-walker that
// kept its own copy of from's flag arities (the valueFlags map),
// which silently misclassified any newly added value-taking flag.
func fromOwnFileFieldSlot(stage string, tree *cf.Command) (string, string) {
	args, pos, ok := stageCursorArgs(stage)
	if !ok {
		return "", ""
	}
	ctx, path := tree.AnalyzeCursor(args, pos)
	if len(path) == 0 || path[0] != "from" {
		return "", ""
	}
	if ctx.FlagSpec == nil || ctx.FlagSpec.FieldsFromFlag != "FILE" {
		return "", ""
	}
	file, _ := ctx.GlobalFlags["FILE"].(string)
	if file == "" {
		return "", ""
	}
	fmtSub := ""
	if len(path) > 1 {
		fmtSub = path[1]
	}
	return file, fmtSub
}

// stageCursorArgs converts a stage's text (ending at the cursor) into
// the argv/pos shape AnalyzeCursor takes: argv minus the program
// word; pos = the COMP_WORDS cursor index (a trailing space means the
// cursor is on a new empty word).
func stageCursorArgs(stage string) ([]string, int, bool) {
	toks := tokenizeStage(stage)
	if len(toks) < 2 || toks[0] != "ssql" {
		return nil, 0, false
	}
	pos := len(toks) - 1
	if strings.HasSuffix(stage, " ") || strings.HasSuffix(stage, "\t") {
		pos = len(toks)
	}
	return toks[1:], pos, true
}
