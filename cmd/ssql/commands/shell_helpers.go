package commands

// ShellHelpersScript is the bash text emitted by `ssql -shell-helpers`.
// Users source it once in their ~/.bashrc:
//
//	eval "$(ssql -shell-helpers)"
//
// The script defines `ssqlgen` — a thin wrapper around the
// `(export SSQLGO=...; <pipeline>) | ssql generate go` pattern,
// taking the pipeline as a single quoted string. All `generate go`
// flags (-run, -build, -optimise, -explain, OUTPUT) pass through
// after the pipeline argument.
//
// Usage examples (after sourcing):
//
//	# Default (typed mode):
//	ssqlgen 'ssql from data.csv | ssql where -if age gt 25 | ssql to csv'
//
//	# Record mode, then run:
//	ssqlgen -record 'ssql from x.csv | ssql to csv' -run
//
//	# Pass through generate-go flags:
//	ssqlgen 'ssql from x.csv | ssql to table' -optimise -build /tmp/myprog
//
// The `command ssql` invocation inside the function ensures we use
// the actual `ssql` binary even when ssqlgen itself is being treated
// as a function alias of `ssql` in some contexts.
const ShellHelpersScript = `# ssql shell helpers — install with:
#   eval "$(ssql -shell-helpers)"

# ssqlgen — wrap a code-generation pipeline with one less ceremony layer.
# Usage:
#   ssqlgen [-record|-typed|-parallel] 'PIPELINE' [generate-go-flags...]
#
# Examples:
#   ssqlgen 'ssql from data.csv | ssql where -if age gt 25 | ssql to csv'
#   ssqlgen -record 'ssql from x.csv | ssql to csv' -run
#   ssqlgen 'ssql from x.csv | ssql to table' -optimise -build /tmp/prog
ssqlgen() {
    local mode=typed
    case "$1" in
        -record|-typed|-parallel) mode="${1#-}"; shift ;;
    esac
    if [ $# -lt 1 ]; then
        echo "ssqlgen: missing pipeline argument" >&2
        echo "Usage: ssqlgen [-record|-typed|-parallel] 'PIPELINE' [generate-go-flags...]" >&2
        return 2
    fi
    local script="$1"; shift
    (export SSQLGO="$mode"; eval "$script") | command ssql generate go "$@"
}
`
