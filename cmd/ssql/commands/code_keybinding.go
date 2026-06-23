package commands

// CodeKeybindingScript is the bash `bind -x` keybinding emitted by
// `ssql -code-keybinding`. Install once:
//
//	eval "$(ssql -code-keybinding)"         # add to ~/.bashrc
//
// then, with an ssql pipeline on the line, press Alt-g to view the typed Go
// it generates — without running it:
//
//	ssql from data.csv | ssql where -if age gt 25 | ssql to table<Alt-g>
//	  → (the generated `package main … ssql.ReadCSV… Where… ` Go, in a popup)
//
// The inspection sibling of Alt-r (run): Alt-g shows the *same* typed Go that
// Alt-r would compile and run, so you can peek before firing — peek with
// Alt-g, run with Alt-r. Unlike Alt-r it reads no data and does not compile:
// it runs the line under SSQL_MODE=typed to emit the codegen fragment stream
// and pipes that through `ssql generate go`, so it's near-instant even on
// huge files. Display reuses the shared popup (tmux `display-popup`, or inline
// when not in tmux); the generated program scrolls in the pager.
const CodeKeybindingScript = `# ssql show-generated-code keybinding — install with:
#   eval "$(ssql -code-keybinding)"
# Then, with an ssql pipeline on the line, press Alt-g. Rebind below.

` + ssqlPopupFunc + `
_ssql_show_go() {
    # Only act on lines that look like an ssql pipeline.
    [[ "$READLINE_LINE" == *ssql* ]] || return
    # Generate (don't run) the typed Go for the line — codegen only, no data.
    local code
    code=$( (export SSQL_MODE=typed; eval "$READLINE_LINE") 2>/dev/null \
            | command ssql generate go 2>/dev/null )
    [[ -n "$code" ]] || return
    _ssql_show_help "$code"
}

# Bind in every keymap — a single key, no keyseq-timeout dependency.
bind -m emacs -x '"\eg": _ssql_show_go'
bind -m vi-insert -x '"\eg": _ssql_show_go'
bind -m vi-command -x '"\eg": _ssql_show_go'
`
