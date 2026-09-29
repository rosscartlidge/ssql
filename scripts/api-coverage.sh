#!/bin/bash
# api-coverage.sh — every exported function and method of the ssql and typed
# packages must carry a SIGNATURE LINE in its reference doc
# (doc/api-reference.md, doc/typed-reference.md), so the references cannot
# drift silently. A function counts when a line starts `func Name` (inside a
# code block, indented or not); a method when a line starts
# `func (x *Type) Method` — a bare mention of the name in prose does not.
# (2026-09-29: the earlier word-match form let `Mode` and `SortRecords` pass
# with no signature at all; ~130 root and 23 typed exports were missing
# entirely before the check existed.) Names listed in
# doc/api-reference-exclude.txt are exempt (standard-library interface
# plumbing the docs describe by the interface, not the method).
# Exit 1 with the missing names when any are found.
set -u
cd "$(dirname "$0")/.."
exclude=doc/api-reference-exclude.txt
excluded() { grep -qxF "$1" "$exclude" 2>/dev/null; }
check() {
    local pkgdir=$1 doc=$2 label=$3 missing=""
    local files
    files=$(ls "$pkgdir"/*.go | grep -v _test.go)
    # top-level functions: a line `func Name` or `func Name[`
    for f in $(grep -hoE '^func [A-Z][A-Za-z0-9]*' $files | sed 's/^func //' | sort -u); do
        excluded "$label.$f" && continue
        grep -qE "^[[:space:]]*func $f\b" "$doc" || missing="$missing $f"
    done
    # methods on exported receivers: a line `func (r Type) Method` / `func (r *Type[K]) Method`
    for m in $(grep -hoE '^func \([a-z]+ \*?[A-Z][A-Za-z0-9]*(\[[^]]*\])?\) [A-Z][A-Za-z0-9]*' $files \
               | sed -E 's/^func \([a-z]+ \*?([A-Z][A-Za-z0-9]*)(\[[^]]*\])?\) /\1./' | sort -u); do
        excluded "$label.$m" && continue
        local T=${m%.*} M=${m#*.}
        grep -qE "^[[:space:]]*func \([a-z]+ \*?$T(\[[^]]*\])?\) $M\b" "$doc" || missing="$missing $m"
    done
    if [[ -n "$missing" ]]; then
        echo "$label: no signature line in $doc for:$missing"
        return 1
    fi
    echo "$label: every export has a signature line in $doc"
}
status=0
check . doc/api-reference.md ssql || status=1
check typed doc/typed-reference.md typed || status=1
exit $status
