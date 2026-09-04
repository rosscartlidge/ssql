#!/usr/bin/env bash
# codelab-run.sh — DFC125: execute every ```bash block of doc/cli-codelab.md
# in doc/codelab-data against a freshly built ssql, failing on non-zero
# exit or empty stdout. A block whose FIRST line is
#   # codelab: skip — <reason>
# is skipped and the reason printed. Usage: scripts/codelab-run.sh [-v]
set -o pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DOC="$ROOT/doc/cli-codelab.md"
DATA="$ROOT/doc/codelab-data"
VERBOSE=${1:-}
BIN_DIR="$(mktemp -d)"
WORK="$(mktemp -d)"
trap 'chmod -R u+w "$BIN_DIR" "$WORK" 2>/dev/null; rm -rf "$BIN_DIR" "$WORK"' EXIT
(cd "$ROOT" && go build -o "$BIN_DIR/ssql" ./cmd/ssql) || { echo "codelab-run: build failed"; exit 1; }
export PATH="$BIN_DIR:$PATH"
# Blocks run in a throwaway COPY of the fixtures: examples that write
# files (to csv, tee, "create sample data") must never touch the
# checked-in data — the first baseline run overwrote employees.csv.
cp -r "$DATA"/. "$WORK"/
# (HOME is left alone: blocks that run `go` need the real module cache,
# and non-interactive bash -c never writes history or rc files anyway.)

pass=0; fail=0; skipped=0; n=0
block=""; inblock=0; startline=0; lineno=0
run_block() {
  n=$((n+1))
  local first
  first="$(printf '%s\n' "$block" | sed -n '1p')"
  if [[ "$first" =~ ^#\ codelab:\ skip ]]; then
    skipped=$((skipped+1))
    [[ -n "$VERBOSE" ]] && echo "SKIP  block $n (line $startline): ${first#\# codelab: skip}"
    return
  fi
  local out rc
  out="$(cd "$WORK" && timeout 120 bash -c "set -o pipefail; $block" 2>&1)"; rc=$?
  if [[ $rc -ne 0 ]]; then
    fail=$((fail+1)); echo "FAIL  block $n (line $startline) exit $rc:"; printf '%s\n' "$block" | sed 's/^/    | /'; printf '%s\n' "$out" | tail -8 | sed 's/^/    > /'
  elif [[ -z "$out" ]]; then
    fail=$((fail+1)); echo "FAIL  block $n (line $startline): no output"; printf '%s\n' "$block" | sed 's/^/    | /'
  else
    pass=$((pass+1)); [[ -n "$VERBOSE" ]] && echo "ok    block $n (line $startline): $(printf '%s' "$block" | head -1 | cut -c1-70)"
  fi
}
while IFS= read -r line; do
  lineno=$((lineno+1))
  if [[ $inblock -eq 0 && "$line" == '```bash' ]]; then inblock=1; block=""; startline=$lineno; continue; fi
  if [[ $inblock -eq 1 && "$line" == '```' ]]; then inblock=0; run_block; continue; fi
  if [[ $inblock -eq 1 ]]; then block+="$line"$'\n'; fi
done < "$DOC"
echo "codelab-run: $pass passed, $fail failed, $skipped skipped (of $n blocks)"
[[ $fail -eq 0 ]]
