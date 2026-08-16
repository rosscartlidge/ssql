#!/usr/bin/env bash
# Headless e2e for `to explore -wasm`: generates an explorer from a tiny
# fixture, then drives the embedded engine shim (window.ssqlEngine) with
# every op type the UI can emit — where/sort/limit/group_by/compute/
# window/distinct — asserting REAL-engine results (DFC107).
#
#   scripts/explore-test.sh          # builds ssql, generates, drives
set -euo pipefail
cd "$(dirname "$0")/.."

chrome=$(command -v google-chrome || command -v chromium || command -v google-chrome-stable) || { echo "no chrome" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"; kill "${srv:-0}" 2>/dev/null || true' EXIT

go build -o "$tmp/ssql" ./cmd/ssql
printf 'name,dept,salary\nAlice,Eng,95000\nBob,Sales,65000\nCarol,Eng,105000\nDave,Sales,70000\n' > "$tmp/exp.csv"
"$tmp/ssql" from csv "$tmp/exp.csv" | "$tmp/ssql" to explore -wasm "$tmp/exp_explore.html" >/dev/null
cp scripts/explore-harness.html "$tmp/check.html"

port="${EXPLORE_TEST_PORT:-8938}"
python3 -m http.server "$port" --directory "$tmp" >/dev/null 2>&1 &
srv=$!
sleep 1

results=$("$chrome" --headless=new --disable-gpu --no-sandbox \
    --virtual-time-budget=90000 --timeout=120000 \
    --dump-dom "http://localhost:$port/check.html" 2>/dev/null |
    sed -n 's/.*<pre id="results">//;/^DONE$/,/<\/pre>/p' | sed 's,</pre>.*,,')
[ -n "$results" ] || { echo "harness did not finish" >&2; exit 1; }
echo "$results"
echo "$results" | grep -q '^FAIL' && exit 1
echo "explore e2e: OK"
