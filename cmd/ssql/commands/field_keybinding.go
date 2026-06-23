package commands

import cf "github.com/rosscartlidge/autocli/v4"

// FieldHintToken is what Tab inserts at a field position it can't resolve
// itself (the cross-pipe case): a self-documenting, shell-metacharacter-free
// placeholder telling the user to press Ctrl-O. The Ctrl-O binding
// (_ssql_complete_field) recognises this exact token, deletes it, and
// completes from the live upstream schema. Single source of truth — wired into
// autocli's FieldCompleter via init() and matched literally in
// FieldKeybindingScript (TestFieldHintTokenConsistent enforces the match).
const FieldHintToken = "Use-Ctrl-O"

func init() {
	// Make autocli's field-name hint the actionable Ctrl-O placeholder rather
	// than the bare "<FIELD>". Runs before any completion executes.
	cf.FieldNameHint = FieldHintToken
}

// FieldKeybindingScript is the bash `bind -x` keybinding emitted by
// `ssql -field-keybinding`. Users install it once:
//
//	eval "$(ssql -field-keybinding)"      # add to ~/.bashrc
//
// and then, at a field position inside a pipeline, press Ctrl-O:
//
//	ssql from data.csv | ssql rename -as name person | ssql group-by <C-o>
//	  → completes from  person dept …  (the schema flowing in from the upstream)
//
// A single key (not a chord) on purpose: a two-key chord depends on
// readline's keyseq-timeout, which vi users routinely lower for snappy
// Esc — so the chord intermittently self-inserts. Ctrl-O has no timeout
// dependency. (fzf uses single keys for the same reason.)
//
// Why a keybinding and not Tab completion: bash scopes a completion
// function's COMP_LINE/COMP_WORDS to the command under the cursor, so a
// `complete -F` function cannot see the upstream pipeline. A `bind -x`
// binding can — READLINE_LINE holds the entire line. The binding runs the
// upstream under SSQL_MODE=schema (which transforms a schema header
// instead of data, so it reads only file headers — near-instant) and
// completes the field name from the result. See
// doc/research/bash-pipeline-completion-options.md.
const FieldKeybindingScript = `# ssql field-completion keybinding — install with:
#   eval "$(ssql -field-keybinding)"
# Then inside a pipeline, at a field position, press Ctrl-O.
# Rebind elsewhere by changing the bind lines at the bottom.

# Longest common prefix of the arguments.
_ssql_lcp() {
    local prefix="$1" s
    shift
    for s in "$@"; do
        while [[ "$s" != "$prefix"* ]]; do
            prefix="${prefix%?}"
            [[ -z "$prefix" ]] && { printf ''; return; }
        done
    done
    printf '%s' "$prefix"
}

_ssql_complete_field() {
    # READLINE_LINE is the whole line; take everything up to the cursor.
    local line="${READLINE_LINE:0:$READLINE_POINT}"
    # The partial word under the cursor (after the last space or pipe).
    local partial="${line##*[ |]}"
    # When Tab can't resolve field names itself (the cross-pipe case) it
    # inserts the "Use-Ctrl-O" placeholder; bash appends a space after that
    # unique completion. Detect the placeholder right before the cursor in
    # either form, delete it, and complete for real from the upstream schema.
    local _ph="Use-Ctrl-O" _cut=0
    if [[ "$partial" == "$_ph" ]]; then
        _cut=${#_ph}                       # cursor sits right after it
    elif [[ -z "$partial" && "$line" == *"$_ph " ]]; then
        _cut=$(( ${#_ph} + 1 ))            # …followed by bash's trailing space
    fi
    if (( _cut > 0 )); then
        READLINE_LINE="${READLINE_LINE:0:$((READLINE_POINT-_cut))}${READLINE_LINE:$READLINE_POINT}"
        READLINE_POINT=$(( READLINE_POINT - _cut ))
        line="${READLINE_LINE:0:$READLINE_POINT}"
        partial=""
    fi
    # Ask ssql which command's schema feeds this cursor position. This is
    # paren-aware: it handles a pipe INSIDE a process substitution, the cursor
    # being inside a <(…), and a join's right-side field (which comes from the
    # join's <(…) source, not the upstream pipeline). See cursor_context.go.
    local src
    src=$(command ssql -complete-source "$line" 2>/dev/null)
    [[ -n "$src" ]] || return

    local fields
    fields=$( (export SSQL_MODE=schema; eval "$src") 2>/dev/null \
              | command ssql generate schema 2>/dev/null )
    [[ -n "$fields" ]] || return

    # Keep the fields that match what's typed so far.
    local matches=() f
    while IFS= read -r f; do
        [[ -n "$f" && "$f" == "$partial"* ]] && matches+=("$f")
    done <<< "$fields"
    local n=${#matches[@]}
    (( n == 0 )) && return

    local insert
    if (( n == 1 )); then
        insert="${matches[0]:${#partial}} "        # unique → complete + space
    else
        local lcp
        lcp="$(_ssql_lcp "${matches[@]}")"
        insert="${lcp:${#partial}}"                # extend to common prefix
        printf '\n%s\n' "${matches[*]}"            # …and list the candidates
    fi

    if [[ -n "$insert" ]]; then
        READLINE_LINE="${READLINE_LINE:0:$READLINE_POINT}${insert}${READLINE_LINE:$READLINE_POINT}"
        READLINE_POINT=$(( READLINE_POINT + ${#insert} ))
    fi
}

# Bind in every keymap so it works whether you are in emacs or vi mode
# (a bare bind installs only into the keymap active when sourced).
bind -m emacs -x '"\C-o": _ssql_complete_field'
bind -m vi-insert -x '"\C-o": _ssql_complete_field'
bind -m vi-command -x '"\C-o": _ssql_complete_field'
`
