package commands

// EditKeybindingScript is the bash binding emitted by
// `ssql -edit-keybinding`. Install once:
//
//	eval "$(ssql -edit-keybinding)"        # add to ~/.bashrc
//
// It takes over bash's own editor key — Ctrl-X Ctrl-E in emacs mode, v in
// vi command mode — so the editor opens with the ssql pipeline laid out
// by `generate pretty`, one stage to a line within 80 columns (DFC167).
// Bash still does the rest: it opens $VISUAL (else $EDITOR) on its
// bash-fc temp file and runs the file when the editor exits, and history
// keeps the pipeline as one line that runs as it stands (checked in a pty,
// 2026-11-10).
//
// The rewrite is `ssql -pretty-line` (PrettyLine): text around the ssql
// pipeline stays where it was, and a line that is not an ssql pipeline,
// is half typed, or holds a `$(…)` (describing the pipeline runs it under
// bash) opens unchanged, exactly as bash's own key does.
var EditKeybindingScript = `# ssql editor key: the pipeline laid out one stage to a line — install with:
#   eval "$(ssql -edit-keybinding)"
# Then press Ctrl-X Ctrl-E (emacs) or v (vi command mode) as usual: the
# editor ($VISUAL, else $EDITOR) opens with the ssql pipeline laid out, and
# bash runs the file when you save and quit. Other lines open unchanged.

_ssql_pretty_line() {
    [[ "$READLINE_LINE" == *ssql* ]] || return
    local pretty
    pretty=$(command ssql -pretty-line "$READLINE_LINE" 2>/dev/null) || return
    if [[ -n "$pretty" ]]; then
        READLINE_LINE="$pretty"
        READLINE_POINT=${#READLINE_LINE}
    fi
}

# Generated from the KeyBindings table: the key is a macro of the rewrite
# (a bind -x function) and bash's own editor command, so bash still opens
# the editor and runs the result.
` + KeyBindLines("_ssql_pretty_line")
