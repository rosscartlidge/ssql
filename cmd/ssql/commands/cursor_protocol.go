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
		rest := args[2:]
		help, herr := root().HelpAt(rest, pos)
		if herr != nil {
			return "", fmt.Sprintf("%v\n", herr), 1, true
		}
		// Writing an expression is hard without knowing the functions.
		// On an expression arg: the cursor on (or inside the call of) a
		// known function gets that function's entry; anywhere else in the
		// expression gets the whole reference. The word arrives cut at
		// the cursor, so the function under it is the trailing
		// identifier, or the innermost unclosed call around it.
		if ExprArgAtCursor(root(), rest, pos) {
			ref := FunctionsReference
			// pos counts the program name at 0; rest starts after it.
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
	return "", "", 0, false
}
