#!/bin/bash
# api-coverage.sh — every exported function and method of the ssql and typed
# packages must be mentioned in its reference doc (doc/api-reference.md,
# doc/typed-reference.md), so the references cannot drift silently
# (2026-09-29: ~130 root and 23 typed exports were missing before this
# check existed). Names listed in doc/api-reference-exclude.txt are exempt
# (deliberately undocumented: heap/sort interface plumbing, test hooks).
# Exit 1 with the missing names when any are found.
set -u
cd "$(dirname "$0")/.."
exclude=doc/api-reference-exclude.txt
excluded() { grep -qxF "$1" "$exclude" 2>/dev/null; }
check() {
    local pkgdir=$1 doc=$2 label=$3 missing=""
    local files
    files=$(ls "$pkgdir"/*.go | grep -v _test.go)
    # top-level functions
    for f in $(grep -hoE '^func [A-Z][A-Za-z0-9]*' $files | sed 's/^func //' | sort -u); do
        excluded "$label.$f" && continue
        grep -qE "\b$f\b" "$doc" || missing="$missing $f"
    done
    # methods on exported receivers: Type.Method
    for m in $(grep -hoE '^func \([a-z]+ \*?[A-Z][A-Za-z0-9]*(\[[^]]*\])?\) [A-Z][A-Za-z0-9]*' $files \
               | sed -E 's/^func \([a-z]+ \*?([A-Z][A-Za-z0-9]*)(\[[^]]*\])?\) /\1./' | sort -u); do
        excluded "$label.$m" && continue
        grep -qE "\b${m#*.}\b" "$doc" || missing="$missing $m"
    done
    if [[ -n "$missing" ]]; then
        echo "$label: not mentioned in $doc:$missing"
        return 1
    fi
    echo "$label: every export is mentioned in $doc"
}
status=0
check . doc/api-reference.md ssql || status=1
check typed doc/typed-reference.md typed || status=1
exit $status
