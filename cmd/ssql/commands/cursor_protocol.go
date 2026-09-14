package commands

import (
	"fmt"
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
)

// HandleCursorProtocol implements the argv→stdout cursor-context protocol
// flags shared by the bash keybindings (Ctrl-O field completion, Alt-h
// help-at-cursor) and the browser playground's Help button:
//
//	-complete-source BEFORE   → upstream pipeline for schema-driven completion
//	-cursor-stage    BEFORE   → current pipeline stage at the cursor
//	-help-at POS ARGS...      → autocli help for the word at POS
//	-help-at-cursor BEFORE    → the same for the word under the cursor, BEFORE
//	                            being the line up to it (stage + words found here)
//	-split-pipeline LINE      → prefix / ssql pipeline / suffix, one per line
//
// args is os.Args[1:]; root builds the command tree (called lazily — only
// -help-at needs it). Returns handled=false when args don't start with a
// protocol flag, so callers fall through to normal execution.
func HandleCursorProtocol(args []string, root func() *cf.Command) (stdout, stderr string, code int, handled bool) {
	if len(args) < 2 {
		return "", "", 0, false
	}
	switch args[0] {
	case "-complete-source":
		return CompleteSource(args[1], root()), "", 0, true
	case "-cursor-stage":
		return CursorTopLevelStage(args[1]), "", 0, true
	case "-value-source":
		return ValueSourceFile(args[1], root()), "", 0, true
	case "-split-pipeline":
		// Three lines: the shell prefix (ending in `|`, or empty), the
		// contiguous ssql pipeline, the shell suffix (starting with `|` or
		// a redirection, or empty). Used by the Alt-r / Alt-g bindings.
		prefix, segment, suffix, err := SplitPipeline(args[1])
		if err != nil {
			return "", fmt.Sprintf("ssql: %v\n", err), 1, true
		}
		return prefix + "\n" + segment + "\n" + suffix + "\n", "", 0, true
	case "-help-at":
		pos, err := strconv.Atoi(args[1])
		if err != nil {
			return "", fmt.Sprintf("invalid position: %s\n", args[1]), 1, true
		}
		return helpAtWords(root(), args[2:], pos)
	case "-help-at-cursor":
		// The whole line up to the cursor. The stage at the cursor is found
		// paren-aware and split into words QUOTE-aware here — the Alt-h
		// binding used to split with bash's `read -ra`, which ignores
		// quotes, so an -if-expr containing a space was never recognised as
		// an expression argument and Alt-h showed only the flag.
		before := args[1]
		stage := CursorTopLevelStage(before)
		toks, openQuote := tokenizeStageOpen(stage)
		if len(toks) == 0 || (toks[0] != "ssql" && toks[0] != "ssql_gpu") {
			return "", "not an ssql stage\n", 1, true
		}
		pos := len(toks) - 1
		// A trailing space starts a new, empty word the cursor sits on —
		// unless the cursor is still inside an unclosed quote, where the
		// space is part of the expression being typed.
		if !openQuote && (strings.HasSuffix(stage, " ") || strings.HasSuffix(stage, "\t")) {
			toks = append(toks, "")
			pos = len(toks) - 1
		}
		return helpAtWords(root(), toks[1:], pos)
	}
	return "", "", 0, false
}

// helpAtWords is the shared body of -help-at and -help-at-cursor: autocli's
// help for the word at pos (program name at 0; rest starts after it), and,
// on an expression argument, the function reference — the entry for the
// function the cursor is on or inside (exprFunctionCandidates on the word,
// which arrives cut at the cursor), else the whole list.
func helpAtWords(tree *cf.Command, rest []string, pos int) (string, string, int, bool) {
	help, herr := tree.HelpAt(rest, pos)
	if herr != nil {
		return "", fmt.Sprintf("%v\n", herr), 1, true
	}
	if ExprArgAtCursor(tree, rest, pos) {
		ref := FunctionsReference
		if pos >= 1 && pos-1 < len(rest) {
			for _, name := range exprFunctionCandidates(rest[pos-1]) {
				if entry, ok := FunctionEntry(name); ok {
					ref = entry + "\n(Alt-h elsewhere in the expression, or `ssql functions`, for the full reference)\n"
					break
				}
			}
		}
		help = strings.TrimRight(help, "\n") + "\n\n" + ref
	}
	return help, "", 0, true
}
