package commands

// SchemaCompletionScript is the pipeline-aware bash completion wrapper,
// emitted after autocli's `_autocli_complete` by `ssql -completion-script`.
//
// On TAB at a value position inside a pipeline, it computes the fields
// flowing in from the upstream stages by running them under
// SSQL_MODE=schema — which transforms a schema header instead of data,
// so it's near-zero cost — and pipes the result through
// `ssql generate schema` to get a plain field list for compgen. So
// `ssql from data.csv | ssql rename -as name person | ssql group-by <TAB>`
// offers `person` and the other surviving fields. Flag completion and
// the single-command case fall through to autocli's completer, which
// (since autocli v4.8.0) already completes fields from a referenced
// file via FieldsFromFlag.
const SchemaCompletionScript = `# ssql pipeline-aware tab completion (SSQL_MODE=schema).
# Loaded alongside autocli's completer by:  eval "$(ssql -completion-script)"

_ssql_schema_complete() {
    local cur="${COMP_WORDS[COMP_CWORD]}"

    # bash scopes COMP_WORDS to the simple command under the cursor (the
    # stage after the last pipe), so the upstream pipeline lives only in
    # COMP_LINE. Take everything up to the cursor, then everything before
    # the last top-level '|' — that's the upstream to run under
    # SSQL_MODE=schema. Augment only when there IS an upstream pipe and
    # we're completing a value (flags stay with autocli).
    local line="${COMP_LINE:0:$COMP_POINT}"
    if [[ "$line" == *"|"* && "$cur" != -* && "$cur" != +* ]]; then
        local upstream="${line%|*}"
        # Strip trailing whitespace left by the split.
        upstream="${upstream%"${upstream##*[![:space:]]}"}"
        local fields
        fields=$( (export SSQL_MODE=schema; eval "$upstream") 2>/dev/null \
                  | command ssql generate schema 2>/dev/null )
        if [[ -n "$fields" ]]; then
            COMPREPLY=( $(compgen -W "$fields" -- "$cur") )
            return 0
        fi
    fi

    # Fall back to autocli's completer for flags and the single-command case.
    _autocli_complete
}
complete -F _ssql_schema_complete ssql
`
