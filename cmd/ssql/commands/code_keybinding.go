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
    # On failure (an unsupported flag, a typed-mode-only limitation, a bad
    # operator, …) show the error in the popup instead of silently doing
    # nothing. The clean message goes to a failing STAGE's stderr; generate-go's
    # own stderr just re-wraps it. Capture them separately (a stage's stderr
    # must stay OUT of the fragment stream) and prefer the stage error.
    local out rc e1 e2 msg
    e1=$(mktemp) && e2=$(mktemp) || { _ssql_show_help "ssql: cannot generate Go (mktemp failed)"; return; }
    out=$( (export SSQL_MODE=typed; eval "$READLINE_LINE") 2>"$e1" \
           | command ssql generate go 2>"$e2" )
    rc=$?
    msg=$(<"$e1"); [[ -z "$msg" ]] && msg=$(<"$e2")
    rm -f "$e1" "$e2"
    # Keep the distinct real errors, drop the downstream re-reports.
    msg=$(_ssql_clean_err "$msg")
    if (( rc != 0 )) || [[ -z "$out" ]]; then
        _ssql_show_help "ssql: cannot generate Go for this pipeline

${msg:-<no output — is this a complete ssql pipeline?>}"
        return
    fi
    _ssql_show_help "$out"
}

# Bind in every keymap — a single key, no keyseq-timeout dependency.
bind -m emacs -x '"\eg": _ssql_show_go'
bind -m vi-insert -x '"\eg": _ssql_show_go'
bind -m vi-command -x '"\eg": _ssql_show_go'
`
