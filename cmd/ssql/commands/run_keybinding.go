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
//	  → (compiles the typed pipeline, runs it, output streams below the prompt,
//	     then a "[ssql: compiled in …, ran in …]" line reports how fast it ran)
//
// Unlike the other action keys (Ctrl-O field completion, Ctrl-T optimise,
// Alt-h/Alt-H help), this one is NOT instant and DOES read data: it runs a
// real `go build` (a second or more) and executes the program against the
// data. The win is speed on large inputs — the compiled typed/parallel form
// is far faster than the interpreted pipeline. It compiles ONLY the run of
// ssql stages (`ssql -split-pipeline`) with `ssql generate go -build`, then
// runs the program inside the rest of the line as typed — `cat x | ssql … |
// less` pages the compiled output, `… > out.txt` writes it — so it needs a
// Go toolchain on PATH and the ssql module in the build cache (present
// after `go install …/cmd/ssql@vX.Y.Z`); without them it prints a clear
// error. Output streams wherever the line sends it; readline redraws your
// line underneath.
const RunKeybindingScript = `# ssql convert-to-typed-and-run keybinding — install with:
#   eval "$(ssql -run-keybinding)"
# Then, with an ssql pipeline on the line, press Alt-r. Rebind below.

` + ssqlPopupFunc + `
_ssql_typed_run() {
    # Only act on lines that look like an ssql pipeline.
    [[ "$READLINE_LINE" == *ssql* ]] || return
    # The line is a SHELL pipeline: compile only its run of ssql stages, and
    # keep what is before and after them (a producer feeding stdin; a pager,
    # a head, a redirection) exactly where they were typed.
    local -a parts
    local splitf; splitf=$(mktemp) || return
    if ! command ssql -split-pipeline "$READLINE_LINE" >"$splitf" 2>&1; then
        _ssql_show_help "ssql: cannot compile this line

$(<"$splitf")"; rm -f "$splitf"; return
    fi
    mapfile -t parts <"$splitf"; rm -f "$splitf"
    local prefix="${parts[0]}" seg="${parts[1]}" suffix="${parts[2]}"
    # Compiling takes a moment and reads data — say so before the pause.
    printf '\n[ssql: compiling typed pipeline and running…]\n'
    # Generate typed Go from the ssql stages and build it into a temp binary.
    # Stage stderr and generate/compile errors go to a temp file; on failure
    # they show in a popup instead of an inline error wall.
    local errf bin t0 t1 t2
    errf=$(mktemp) && bin=$(mktemp) || return
    t0=$(date +%s%N)
    (export SSQL_MODE=typed; eval "$seg") 2>"$errf" \
        | command ssql generate go -build "$bin" 2>>"$errf"
    local rc=${PIPESTATUS[1]}
    if (( rc != 0 )); then
        _ssql_show_help "ssql: pipeline failed (could not generate or compile)

$(_ssql_clean_err "$(<"$errf")")"
        rm -f "$errf" "$bin"; return
    fi
    t1=$(date +%s%N)
    # Run the program in the user's own pipeline: prefix | program suffix.
    # Its stdout STREAMS wherever the line sends it; readline redraws after.
    eval "$prefix \"$bin\" $suffix" 2>>"$errf"
    rc=$?
    t2=$(date +%s%N)
    if (( rc != 0 )); then
        _ssql_show_help "ssql: pipeline failed (exit $rc)

$(_ssql_clean_err "$(<"$errf")")"
    else
        printf '[ssql: compiled in %d.%03ds, ran in %d.%03ds]\n' \
            $(( (t1 - t0) / 1000000000 )) $(( (t1 - t0) / 1000000 % 1000 )) \
            $(( (t2 - t1) / 1000000000 )) $(( (t2 - t1) / 1000000 % 1000 ))
        [[ -s "$errf" ]] && printf '%s\n' "$(<"$errf")"
    fi
    rm -f "$errf" "$bin"
}

# Bind in every keymap — a single key, no keyseq-timeout dependency.
bind -m emacs -x '"\er": _ssql_typed_run'
bind -m vi-insert -x '"\er": _ssql_typed_run'
bind -m vi-command -x '"\er": _ssql_typed_run'
`
