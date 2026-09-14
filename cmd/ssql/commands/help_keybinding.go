package commands

// HelpKeybindingScript is the bash `bind -x` keybinding emitted by
// `ssql -help-keybinding`. Install once:
//
//	eval "$(ssql -help-keybinding)"        # add to ~/.bashrc
//
// then, with the cursor on (or inside the arguments of) any ssql command,
// press Alt-h to see contextual help for the flag / command under the
// cursor — what it does and what arguments it expects:
//
//	ssql from data.csv | ssql group-by dept -sum salary<Alt-h>
//	  → -sum, -s  FIELD RESULT
//	      Sum field values across each group …
//
// The third member of the READLINE_LINE action family (Ctrl-O field
// completion, Ctrl-T optimise, Alt-h help). A single key, not a chord, so
// it's robust under a low keyseq-timeout (see the -field-keybinding
// rationale). The help text comes from `ssql -help-at`, the autocli
// help-at-cursor protocol flag — it reads no data, just the command tree.
//
// This emitter also binds Alt-H (Alt-Shift-h) to a cheat-sheet of the whole
// ssql key-binding family, so users can rediscover the keys at the prompt.
// (Alt-? would collide with readline's default possible-completions.)
//
// Display: inside tmux it pops a transient `display-popup` overlay (the
// command line is untouched); otherwise it prints inline below the prompt
// and readline redraws the line underneath.
var HelpKeybindingScript = `# ssql help-at-cursor keybinding — install with:
#   eval "$(ssql -help-keybinding)"
# Then, with the cursor on an ssql command/flag, press Alt-h. Rebind below.

` + ssqlPopupFunc + `
_ssql_help_at() {
    # The current pipeline stage at the cursor, paren-aware (a pipe inside a
    # <(…) process substitution is not a stage boundary, and inside a procsub
    # the stage is within it). See cursor_context.go.
    local before="${READLINE_LINE:0:$READLINE_POINT}"
    # Only act on ssql stages (paren-aware: a pipe inside <(…) is not a
    # stage boundary — see cursor_context.go).
    local stage
    stage=$(command ssql -cursor-stage "$before" 2>/dev/null)
    [[ "$stage" == ssql* ]] || return

    # ssql finds the word under the cursor itself, quote-aware: an -if-expr
    # with spaces is one word (bash's read -ra would split it and lose the
    # expression argument). Capture stderr+exit so a failure surfaces in the
    # popup rather than flashing nothing.
    local help rc
    help=$(command ssql -help-at-cursor "$before" 2>&1)
    rc=$?
    if (( rc != 0 )) || [[ -z "$help" ]]; then
        _ssql_show_help "ssql: no help available here

${help:-<no output>}"
        return
    fi
    _ssql_show_help "$help"
}

# Alt-H: cheat-sheet of the whole ssql key-binding family, for rediscovery.
# The list below is generated from the KeyBindings table (commands/
# keybindings.go) — keep it the source of truth, do not hand-edit here.
_ssql_help_keys() {
    _ssql_show_help "` + KeyBindingsHelp() + `"
}

# Bind in every keymap — single keys, no keyseq-timeout dependency.
bind -m emacs -x '"\eh": _ssql_help_at'
bind -m vi-insert -x '"\eh": _ssql_help_at'
bind -m vi-command -x '"\eh": _ssql_help_at'
bind -m emacs -x '"\eH": _ssql_help_keys'
bind -m vi-insert -x '"\eH": _ssql_help_keys'
bind -m vi-command -x '"\eH": _ssql_help_keys'
`

// ssqlPopupFunc is the shared bash `_ssql_show_help` function embedded by the
// help (-help-keybinding) and code-view (-code-keybinding) bindings. It shows
// text in a tmux `display-popup` (clamped to the client size so it never
// errors "width/height too large"; less reads via stdin redirect so the prompt
// is a bare ":") when inside tmux, and inline below the prompt otherwise. Both
// emitters embed it so each `eval` is self-contained; sourcing both just
// redefines it identically.
const ssqlPopupFunc = `# Show text in a tmux popup when inside tmux; otherwise in the pager when
# it would not fit below the prompt (and the terminal is interactive); else
# inline. SSQL_POPUP=inline forces inline, SSQL_POPUP=pager forces the pager.
_ssql_show_help() {
    [[ -n "$1" ]] || return
    if [[ -n "$TMUX" ]]; then
        local tmpf
        tmpf=$(mktemp) || { printf '\n%s\n' "$1"; return; }
        printf '%s\n' "$1" > "$tmpf"
        # Clamp the popup to the client size — tmux errors "width/height too
        # large" if the popup is bigger than the terminal (small windows).
        local w=84 h=24 cw ch
        cw=$(tmux display-message -p '#{client_width}' 2>/dev/null)
        ch=$(tmux display-message -p '#{client_height}' 2>/dev/null)
        [[ "$cw" =~ ^[0-9]+$ ]] && (( w > cw )) && w=$cw
        [[ "$ch" =~ ^[0-9]+$ ]] && (( h > ch )) && h=$ch
        # Redirect the temp file into the pager's stdin (rather than passing it
        # as an argument) so less shows its bare ":" prompt — as it does for
        # piped input — instead of the ugly /tmp/tmp.XXXX path.
        tmux display-popup -w "$w" -h "$h" -E "\${PAGER:-less -R} < '$tmpf'; rm -f '$tmpf'"
    elif [[ "${SSQL_POPUP:-auto}" != inline && -t 1 && -t 0 ]] && _ssql_needs_pager "$1"; then
        # No tmux: the pager (less) uses the alternate screen, so a long
        # answer — the function reference, generated Go — reads like a popup
        # and 'q' restores the line exactly as it was. Short answers stay
        # inline, where they are quicker to read than a pager.
        local tmpf
        tmpf=$(mktemp) || { printf '\n%s\n' "$1"; return; }
        printf '%s\n' "$1" > "$tmpf"
        ${PAGER:-less -R} < "$tmpf"
        rm -f "$tmpf"
    else
        printf '\n%s\n' "$1"
    fi
}

# Would this text scroll the prompt away? (more lines than the terminal has,
# less a margin) — or the user asked for the pager outright.
_ssql_needs_pager() {
    [[ "${SSQL_POPUP:-auto}" == pager ]] && return 0
    local n rows
    n=$(printf '%s\n' "$1" | wc -l)
    rows=$LINES
    [[ "$rows" =~ ^[0-9]+$ && rows -gt 0 ]] || rows=$(tput lines 2>/dev/null || echo 24)
    (( n > rows - 3 ))
}

# Strip the redundant per-stage re-reports a codegen error accumulates — each
# downstream stage echoes the upstream failure — leaving the distinct real
# messages (so a pipeline with two real errors shows both, not a wall of
# duplicates). Falls back to the first line if everything was filtered.
_ssql_clean_err() {
    local cleaned
    cleaned=$(grep -v -e 'reading code fragments: code generation failed' \
                      -e 'assembling code fragments: no code fragments' <<< "$1")
    if [[ -n "$cleaned" ]]; then
        printf '%s' "$cleaned"
    else
        local first; IFS= read -r first <<< "$1"; printf '%s' "$first"
    fi
}
`
