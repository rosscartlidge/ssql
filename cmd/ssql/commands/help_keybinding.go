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
const HelpKeybindingScript = `# ssql help-at-cursor keybinding — install with:
#   eval "$(ssql -help-keybinding)"
# Then, with the cursor on an ssql command/flag, press Alt-h. Rebind below.

# Show help text: a tmux popup when inside tmux, inline otherwise.
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
    else
        printf '\n%s\n' "$1"
    fi
}

_ssql_help_at() {
    # Text up to the cursor; the current pipeline stage is after the last pipe.
    local before="${READLINE_LINE:0:$READLINE_POINT}"
    local stage="${before##*|}"
    # Trim leading whitespace.
    stage="${stage#"${stage%%[![:space:]]*}"}"
    # Only act on ssql stages.
    [[ "$stage" == ssql* || "$stage" == ssql ]] || return

    # Split the stage into words (no globbing). words[0] is "ssql".
    local -a words
    read -ra words <<< "$stage"
    (( ${#words[@]} >= 1 )) || return

    # COMP_WORDS-style position: index of the word under the cursor, counting
    # "ssql" as 0. A trailing space means the cursor is on a new empty word.
    local pos
    if [[ "$stage" =~ [[:space:]]$ ]]; then
        pos=${#words[@]}
    else
        pos=$(( ${#words[@]} - 1 ))
    fi

    # args = the stage words minus the program name.
    local -a args=("${words[@]:1}")
    local help
    help=$(command ssql -help-at "$pos" "${args[@]}" 2>/dev/null)
    _ssql_show_help "$help"
}

# Alt-H: cheat-sheet of the whole ssql key-binding family, for rediscovery.
_ssql_help_keys() {
    _ssql_show_help "ssql key bindings

  Ctrl-O   complete a field name from the upstream pipeline schema
  Ctrl-T   optimise the ssql pipeline on the line, in place
  Alt-r    compile the pipeline as typed Go and run it
  Alt-h    help for the flag / command under the cursor
  Alt-H    show this list

Run 'ssql' with no arguments to see how to enable each binding."
}

# Bind in every keymap — single keys, no keyseq-timeout dependency.
bind -m emacs -x '"\eh": _ssql_help_at'
bind -m vi-insert -x '"\eh": _ssql_help_at'
bind -m vi-command -x '"\eh": _ssql_help_at'
bind -m emacs -x '"\eH": _ssql_help_keys'
bind -m vi-insert -x '"\eH": _ssql_help_keys'
bind -m vi-command -x '"\eH": _ssql_help_keys'
`
