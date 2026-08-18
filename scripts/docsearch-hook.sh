#!/bin/sh
# Claude Code UserPromptSubmit hook: surface prior art from doc/,
# claude/ and journal/ for every substantive user prompt, so sessions
# see relevant DFCs/journal entries without having to remember to
# search. Registered in .claude/settings.local.json (gitignored) —
# re-register there if it ever goes missing:
#
#   "hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command",
#     "command": "$CLAUDE_PROJECT_DIR/scripts/docsearch-hook.sh"}]}]}
#
# Guards: skips short prompts (<6 words) and slash commands; hybrid
# search with a timeout, falling back to lexical; caps output. Output
# on exit 0 is injected as context — keep it a hint, not a payload.

dir="${CLAUDE_PROJECT_DIR:-$(dirname "$0")/..}"

prompt=$(python3 -c 'import json,sys
try: print(json.load(sys.stdin).get("prompt",""))
except Exception: pass' 2>/dev/null)

case "$prompt" in
  ""|/*) exit 0 ;;
esac
[ "$(printf '%s' "$prompt" | wc -w)" -lt 6 ] && exit 0

hits=$(timeout 10 "$dir/scripts/docsearch.sh" -k 3 "$prompt" 2>/dev/null)
[ -z "$hits" ] && hits=$(timeout 3 "$dir/scripts/docsearch.sh" -lexical -k 3 "$prompt" 2>/dev/null)
[ -z "$hits" ] && exit 0

echo "Possibly-related prior art (docsearch; ignore if off-topic):"
printf '%s\n' "$hits" | head -8
