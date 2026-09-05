#!/usr/bin/env bash
# codelab-go-run.sh — DFC125 for the Go codelabs: execute a Go codelab's
# code blocks against the CURRENT checkout, failing on any compile or
# run error or empty stdout.
#
#   ```go blocks containing `package main` are complete programs: each is
#       written to its own package directory in a throwaway module whose
#       go.mod `replace`s github.com/rosscartlidge/ssql/v4 with this
#       checkout, then `go run`. Programs share one working directory
#       (the module root) in document order, so a fixture written by an
#       earlier block is there for a later one.
#   ```go blocks WITHOUT `package main` are fragments: they must at least
#       parse as Go (gofmt -e), either as top-level declarations or as
#       statements inside a function.
#   ```bash blocks run in the same working directory (fixture heredocs,
#       `go run` of a generated file, …). A block whose FIRST line is
#         # codelab: skip — <reason>
#       is skipped and the reason printed.
#
# Usage: scripts/codelab-go-run.sh [-v] DOC.md
set -o pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DOC=""
VERBOSE=
for arg in "$@"; do
  case "$arg" in
    -v) VERBOSE=1 ;;
    *) DOC="$arg" ;;
  esac
done
[[ -n "$DOC" && -f "$DOC" ]] || { echo "usage: codelab-go-run.sh [-v] DOC.md"; exit 1; }
WORK="$(mktemp -d)"
trap 'chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT

# The throwaway module: same Go version as the repo, ssql resolved to the
# checkout, dependency checksums borrowed from the repo's go.sum so tidy
# needs the module cache only (no network when the checkout builds).
GOVER="$(sed -n 's/^go \([0-9.]*\).*/\1/p' "$ROOT/go.mod" | head -1)"
cat > "$WORK/go.mod" <<EOF
module codelab

go $GOVER

require github.com/rosscartlidge/ssql/v4 v4.0.0

replace github.com/rosscartlidge/ssql/v4 => $ROOT
EOF
cp "$ROOT/go.sum" "$WORK/go.sum"
# A package that imports ssql keeps the require alive through tidy
# (tidy drops a require nothing imports yet — the programs come later).
mkdir -p "$WORK/keep"
printf 'package keep\n\nimport (\n\t_ "github.com/rosscartlidge/ssql/v4"\n\t_ "github.com/rosscartlidge/ssql/v4/typed"\n)\n' > "$WORK/keep/keep.go"
(cd "$WORK" && GOFLAGS=-mod=mod go mod tidy >/dev/null 2>&1) || { echo "codelab-go-run: go mod tidy failed"; exit 1; }

pass=0; fail=0; skipped=0; n=0; prog=0
block=""; lang=""; inblock=0; startline=0; lineno=0

report_fail() { # $1=label $2=output
  fail=$((fail+1)); echo "FAIL  block $n (line $startline) $1:"
  printf '%s\n' "$block" | head -40 | sed 's/^/    | /'
  printf '%s\n' "$2" | grep -v '^go: downloading' | tail -12 | sed 's/^/    > /'
}

run_program() {
  prog=$((prog+1))
  local dir="p$(printf '%02d' $prog)" out rc
  mkdir -p "$WORK/$dir"
  printf '%s' "$block" > "$WORK/$dir/main.go"
  out="$(cd "$WORK" && timeout 300 go run "./$dir" 2>&1)"; rc=$?
  if [[ $rc -ne 0 ]]; then report_fail "program exit $rc" "$out"
  elif [[ -z "$out" ]]; then report_fail "program: no output" ""
  else pass=$((pass+1)); [[ -n "$VERBOSE" ]] && echo "ok    block $n (line $startline): program $dir → $(printf '%s' "$out" | head -1 | cut -c1-60)"
  fi
}

run_fragment() {
  local f="$WORK/frag.go" out imports="" body="$block"
  # A fragment may open with an import (single line or block) followed
  # by statements — split it so the import stays at top level.
  if [[ "$body" == import\ \(* ]]; then
    imports="$(printf '%s' "$body" | sed -n '1,/^)/p')"; body="$(printf '%s' "$body" | sed '1,/^)/d')"
  elif [[ "$body" == import\ * ]]; then
    imports="$(printf '%s' "$body" | sed -n '1p')"; body="$(printf '%s' "$body" | sed '1d')"
  fi
  # Try as top-level declarations, then as statements inside a function.
  printf 'package p\n\n%s\n\n%s\n' "$imports" "$body" > "$f"
  if gofmt -e "$f" >/dev/null 2>&1; then pass=$((pass+1)); [[ -n "$VERBOSE" ]] && echo "ok    block $n (line $startline): fragment (decls)"; return; fi
  printf 'package p\n\n%s\n\nfunc _() {\n%s\n}\n' "$imports" "$body" > "$f"
  if out="$(gofmt -e "$f" 2>&1 >/dev/null)"; then pass=$((pass+1)); [[ -n "$VERBOSE" ]] && echo "ok    block $n (line $startline): fragment (stmts)"; return; fi
  report_fail "fragment does not parse" "$out"
}

run_bash() {
  local first out rc
  first="$(printf '%s\n' "$block" | sed -n '1p')"
  if [[ "$first" =~ ^#\ codelab:\ skip ]]; then
    skipped=$((skipped+1)); [[ -n "$VERBOSE" ]] && echo "SKIP  block $n (line $startline): ${first#\# codelab: skip}"; return
  fi
  out="$(cd "$WORK" && timeout 300 bash -c "set -e -o pipefail; $block" 2>&1)"; rc=$?   # set -e: every command must succeed
  [[ $rc -eq 141 ]] && rc=0   # SIGPIPE from a downstream limit/head — see codelab-run.sh
  if [[ $rc -ne 0 ]]; then report_fail "bash exit $rc" "$out"
  elif [[ -z "$out" ]]; then report_fail "bash: no output" ""
  else pass=$((pass+1)); [[ -n "$VERBOSE" ]] && echo "ok    block $n (line $startline): $(printf '%s' "$block" | head -1 | cut -c1-70)"
  fi
}

run_block() {
  n=$((n+1))
  case "$lang" in
    go) if grep -q '^package main' <<<"$block"; then run_program; else run_fragment; fi ;;
    bash) run_bash ;;
  esac
}

while IFS= read -r line; do
  lineno=$((lineno+1))
  if [[ $inblock -eq 0 && ( "$line" == '```go' || "$line" == '```bash' ) ]]; then inblock=1; lang="${line#\`\`\`}"; block=""; startline=$lineno; continue; fi
  if [[ $inblock -eq 0 && "$line" == '```'* ]]; then inblock=2; continue; fi   # other fence: pass through
  if [[ $inblock -eq 2 && "$line" == '```' ]]; then inblock=0; continue; fi
  if [[ $inblock -eq 1 && "$line" == '```' ]]; then inblock=0; run_block; continue; fi
  if [[ $inblock -eq 1 ]]; then block+="$line"$'\n'; fi
done < "$DOC"
echo "codelab-go-run: $(basename "$DOC"): $pass passed, $fail failed, $skipped skipped (of $n blocks; $prog programs)"
[[ $fail -eq 0 ]]
