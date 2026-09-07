#!/usr/bin/env bash
# codelab-run.sh — DFC125: execute every ```bash block of a CLI codelab
# (default doc/cli-codelab.md) in a throwaway copy of the codelab data,
# failing on non-zero exit or empty stdout. A block whose FIRST line is
#   # codelab: skip — <reason>
# is skipped and the reason printed.
#
# It runs in two places:
#   - In the repository (make doc-test, TestCodelabRuns): builds ssql from
#     the checkout and reads the doc from doc/.
#   - Next to the data `ssql codelab` wrote (it ships with the fixtures):
#     uses the `ssql` on your PATH and, when no doc is given or beside
#     it, fetches the codelab for that ssql's version from GitHub — so
#     `./codelab-run.sh` is a self-test of your install.
# Usage: codelab-run.sh [-v] [DOC.md]     (SSQL_BIN=/path/to/ssql overrides)
set -o pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT=""
[[ -f "$HERE/../../go.mod" && -d "$HERE/../../cmd/ssql" ]] && ROOT="$(cd "$HERE/../.." && pwd)"
DOC=""
VERBOSE=
for arg in "$@"; do
  case "$arg" in
    -v) VERBOSE=1 ;;
    *) DOC="$arg" ;;
  esac
done
BIN_DIR="$(mktemp -d)"
WORK="$(mktemp -d)"
trap 'chmod -R u+w "$BIN_DIR" "$WORK" 2>/dev/null; rm -rf "$BIN_DIR" "$WORK"' EXIT

# The binary: SSQL_BIN, else a fresh build when inside the repo, else PATH.
if [[ -n "${SSQL_BIN:-}" ]]; then
  ln -s "$(command -v "$SSQL_BIN" || echo "$SSQL_BIN")" "$BIN_DIR/ssql"
elif [[ -n "$ROOT" ]]; then
  (cd "$ROOT" && go build -o "$BIN_DIR/ssql" ./cmd/ssql) || { echo "codelab-run: build failed"; exit 1; }
elif command -v ssql >/dev/null; then
  ln -s "$(command -v ssql)" "$BIN_DIR/ssql"
else
  echo "codelab-run: no ssql on PATH (go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest, then add \$HOME/go/bin to PATH)"; exit 1
fi
export PATH="$BIN_DIR:$PATH"

# The doc: argument, else the checkout's, else one beside this script,
# else the copy tagged with the installed version.
if [[ -z "$DOC" ]]; then
  if [[ -n "$ROOT" ]]; then DOC="$ROOT/doc/cli-codelab.md"
  elif [[ -f "$HERE/cli-codelab.md" ]]; then DOC="$HERE/cli-codelab.md"
  else
    ver="$(ssql version | sed -n 's/^ssql v\([0-9.]*\).*/\1/p')"
    DOC="$WORK/cli-codelab.md"
    url="https://raw.githubusercontent.com/rosscartlidge/ssql/v$ver/doc/cli-codelab.md"
    curl -fsSL "$url" -o "$DOC" || { echo "codelab-run: could not fetch $url (pass the doc path as an argument)"; exit 1; }
    echo "codelab-run: running doc/cli-codelab.md as of v$ver against $(ssql version)"
  fi
fi
[[ -f "$DOC" ]] || { echo "codelab-run: no such doc: $DOC"; exit 1; }

# Blocks run in a throwaway COPY of the fixtures: examples that write
# files (to csv, tee, "create sample data") must never touch the
# checked-in data — the first baseline run overwrote employees.csv.
# The copy is made the way a reader makes it — `ssql codelab DIR` writes
# the fixtures embedded in the binary — so the runner also proves the
# tutorial's own setup step.
ssql codelab "$WORK" >/dev/null || { echo "codelab-run: ssql codelab failed"; exit 1; }
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
  # set -e: a block is a sequence of commands and EVERY one must succeed —
  # without it a trailing `echo "Compare …"` masked a failing pipeline
  # (sabotage: `-kernel moving-average` passed the gate).
  out="$(cd "$WORK" && timeout 120 bash -c "set -e -o pipefail; $block" 2>&1)"; rc=$?
  # 141 = an upstream stage killed by SIGPIPE because a downstream
  # `limit` (or `head`) closed the pipe — ordinary Unix behaviour that
  # pipefail surfaces and an interactive user never sees. Only reached
  # when the data outgrows the pipe buffer, which is why small fixtures
  # never showed it.
  [[ $rc -eq 141 ]] && rc=0
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
echo "codelab-run: $(basename "$DOC"): $pass passed, $fail failed, $skipped skipped (of $n blocks)"
[[ $fail -eq 0 ]]
