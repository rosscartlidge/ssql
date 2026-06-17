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

    # Start of the ssql command under the cursor: the word after the last
    # top-level pipe before COMP_CWORD.
    local i cur_cmd_start=0
    for ((i=0; i<COMP_CWORD; i++)); do
        [[ "${COMP_WORDS[i]}" == "|" ]] && cur_cmd_start=$((i+1))
    done

    # Augment only when there IS an upstream stage and we're completing a
    # value (not a flag — flags stay with autocli).
    if [[ $cur_cmd_start -gt 0 && "$cur" != -* && "$cur" != +* ]]; then
        # Upstream = the words before the last pipe.
        local upstream="${COMP_WORDS[*]:0:$((cur_cmd_start-1))}"
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
