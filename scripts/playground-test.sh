#!/usr/bin/env bash
# Headless end-to-end test of the WASM playground (help-at-cursor button,
# Alt-h keydown, share links) via cmd/ssql-playground/test-harness.html.
#
#   make playground && scripts/playground-test.sh
#
# Requires google-chrome and python3. Exits non-zero on any FAIL.
set -euo pipefail
cd "$(dirname "$0")/.."

[ -f cmd/ssql-playground/ssql-playground.wasm ] || {
    echo "cmd/ssql-playground/ssql-playground.wasm missing — run 'make playground' first" >&2
    exit 1
}

chrome=$(command -v google-chrome || command -v chromium || command -v google-chrome-stable) || {
    echo "no chrome/chromium found" >&2
    exit 1
}

port="${PLAYGROUND_TEST_PORT:-8933}"
python3 -m http.server "$port" --directory cmd/ssql-playground >/dev/null 2>&1 &
srv=$!
trap 'kill "$srv" 2>/dev/null || true' EXIT
sleep 1

results=$("$chrome" --headless=new --disable-gpu --no-sandbox \
    --virtual-time-budget=60000 --timeout=120000 \
    --dump-dom "http://localhost:$port/test-harness.html" 2>/dev/null |
    sed -n 's/.*<pre id="results">//;/^DONE$/,/<\/pre>/p' | sed 's,</pre>.*,,')

if [ -z "$results" ]; then
    echo "harness did not finish (no DONE marker)" >&2
    exit 1
fi
echo "$results"
if echo "$results" | grep -q '^FAIL'; then
    exit 1
fi
echo "playground e2e: OK"
