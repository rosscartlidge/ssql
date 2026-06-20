package commands

// OptimiseKeybindingScript is the bash `bind -x` keybinding emitted by
// `ssql -optimise-keybinding`. Install once:
//
//	eval "$(ssql -optimise-keybinding)"   # add to ~/.bashrc
//
// then, with an ssql pipeline on the line, press Ctrl-T to replace it in
// place with its optimised form — merged adjacent `where` clauses,
// `sort … | limit N` → `top`, filters pushed into `from ssh`, column
// projection, and so on:
//
//	ssql from x.csv | ssql where -if a gt 1 | ssql where -if b eq y | ssql sort -desc s | ssql limit 5  <C-t>
//	  → ssql from x.csv | ssql where -if b eq y -if a gt 1 | ssql top 5 -field s
//
// The rewrite is `ssql generate ssql` (the pipeline optimiser) run over
// the line under SSQL_MODE=record. It reads no data — it operates on the
// codegen fragment stream — so it's near-instant even on huge files.
// Reversible with readline undo (Ctrl-_ in emacs, u in vi). A single key,
// not a chord, so it's robust under a low keyseq-timeout (see the
// -field-keybinding rationale).
const OptimiseKeybindingScript = `# ssql in-situ pipeline optimiser — install with:
#   eval "$(ssql -optimise-keybinding)"
# Then, with an ssql pipeline on the line, press Ctrl-T to rewrite it to
# its optimised form. Undo with Ctrl-_ (emacs) or u (vi). Rebind below.

_ssql_optimise_pipeline() {
    # Only touch lines that look like an ssql pipeline.
    [[ "$READLINE_LINE" == *ssql* ]] || return
    local opt
    opt=$( (export SSQL_MODE=record; eval "$READLINE_LINE") 2>/dev/null \
           | command ssql generate ssql 2>/dev/null )
    if [[ -n "$opt" && "$opt" != "$READLINE_LINE" ]]; then
        READLINE_LINE="$opt"
        READLINE_POINT=${#READLINE_LINE}
    fi
}

# Bind in every keymap — a single key, no keyseq-timeout dependency.
bind -m emacs -x '"\C-t": _ssql_optimise_pipeline'
bind -m vi-insert -x '"\C-t": _ssql_optimise_pipeline'
bind -m vi-command -x '"\C-t": _ssql_optimise_pipeline'
`
