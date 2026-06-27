package commands

// RunKeybindingScript is the bash `bind -x` keybinding emitted by
// `ssql -run-keybinding`. Install once:
//
//	eval "$(ssql -run-keybinding)"          # add to ~/.bashrc
//
// then, with an ssql pipeline on the line, press Alt-r to compile it as
// typed Go and run it — the compiled-native result without losing the line
// you're editing:
//
//	ssql from big.csv | ssql where -if age gt 25 | ssql to table<Alt-r>
//	  → (compiles the typed pipeline, runs it, output streams below the prompt)
//
// Unlike the other action keys (Ctrl-O field completion, Ctrl-T optimise,
// Alt-h/Alt-H help), this one is NOT instant and DOES read data: it runs a
// real `go build` (a second or more) and executes the program against the
// data. The win is speed on large inputs — the compiled typed/parallel form
// is far faster than the interpreted pipeline. It reuses `ssql generate go
// -run` (generate typed Go → go build -mod=mod in a temp dir → exec), so it
// needs a Go toolchain on PATH and the ssql module in the build cache
// (present after `go install …/cmd/ssql@vX.Y.Z`); without them it prints a
// clear error. Output streams inline; readline redraws your line underneath.
const RunKeybindingScript = `# ssql convert-to-typed-and-run keybinding — install with:
#   eval "$(ssql -run-keybinding)"
# Then, with an ssql pipeline on the line, press Alt-r. Rebind below.

` + ssqlPopupFunc + `
_ssql_typed_run() {
    # Only act on lines that look like an ssql pipeline.
    [[ "$READLINE_LINE" == *ssql* ]] || return
    # Compiling takes a moment and reads data — say so before the pause.
    printf '\n[ssql: compiling typed pipeline and running…]\n'
    # Generate typed Go from the line and run it (go build + exec). The
    # program's stdout (the result) STREAMS to the terminal; its stderr (generate
    # /compile/runtime errors) goes to a temp file. On failure, show the error in
    # a popup instead of an inline error wall. readline redraws the line after.
    local errf; errf=$(mktemp) || { (export SSQL_MODE=typed; eval "$READLINE_LINE") | command ssql generate go -run; return; }
    (export SSQL_MODE=typed; eval "$READLINE_LINE") 2>"$errf" \
        | command ssql generate go -run 2>>"$errf"
    local rc=${PIPESTATUS[1]}
    if (( rc != 0 )); then
        _ssql_show_help "ssql: pipeline failed (could not generate, compile, or run)

$(_ssql_clean_err "$(<"$errf")")"
    fi
    rm -f "$errf"
}

# Bind in every keymap — a single key, no keyseq-timeout dependency.
bind -m emacs -x '"\er": _ssql_typed_run'
bind -m vi-insert -x '"\er": _ssql_typed_run'
bind -m vi-command -x '"\er": _ssql_typed_run'
`
