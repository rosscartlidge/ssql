#!/bin/bash
# Read what the FAQ was asked and what it answered: both instances' logs,
# merged by time, one block per question (DFC169).
#
#   scripts/faq/review.sh                  # everything, newest last, in less
#   scripts/faq/review.sh -since today     # today (UTC); also YYYY-MM-DD, 6h, 3d
#   scripts/faq/review.sh -public -n 10    # the public instance's last 10
#   scripts/faq/review.sh -status unverified -grep join
#   scripts/faq/review.sh -f               # follow both logs as questions arrive
#   scripts/faq/review.sh -vote down       # the answers visitors marked 👎
#
#   -lan | -public      one instance only (default: both)
#   -since WHEN         questions at or after WHEN
#   -status REGEX       statuses matching REGEX (verified, unverified, illustration, ...)
#   -grep REGEX         questions, pipelines or replies matching REGEX (case-insensitive)
#   -vote up|down|any   answers with that vote (a visitor's last one); any: voted at all
#   -n N                the last N only
#   -brief              no replies: time, status, question, pipeline, failure
#   -f                  follow (filters apply; -since and -n do not)
#
# A vote is a line of its own ({time, id, vote, question}) naming the
# answer's id; it is shown on the answer, and as it arrives with -f.
#
# The public log is the ssql-faq user's; it is read directly when you are in
# the ssql-faq group, through sg when the group is newer than your login,
# else through sudo.
set -euo pipefail

LAN_LOG=${XDG_STATE_HOME:-$HOME/.local/state}/ssql-faq/questions.jsonl
PUBLIC_LOG=/var/lib/ssql-faq/.local/state/ssql-faq/questions.jsonl

which=both since="" status="" grep="" vote="" n="" brief=false follow=false
while [ $# -gt 0 ]; do
	case $1 in
	-lan) which=lan ;;
	-public) which=public ;;
	-since) since=${2:?-since needs a value}; shift ;;
	-status) status=${2:?-status needs a value}; shift ;;
	-grep) grep=${2:?-grep needs a value}; shift ;;
	-vote) vote=${2:?-vote needs up, down or any}; shift ;;
	-n) n=${2:?-n needs a number}; shift ;;
	-brief) brief=true ;;
	-f) follow=true ;;
	-h | -help | --help) sed -n '2,26p' "$0"; exit 0 ;;
	*) echo "review.sh: unknown argument $1 (try -help)" >&2; exit 2 ;;
	esac
	shift
done
case $vote in "" | up | down | any) ;; *) echo "review.sh: -vote takes up, down or any, not $vote" >&2; exit 2 ;; esac
[ -z "$n" ] || [[ $n =~ ^[0-9]+$ ]] || { echo "review.sh: -n takes a number, not $n" >&2; exit 2; }

cutoff=""
case $since in
"") ;;
today) cutoff=$(date -u +%FT00:00:00Z) ;;
yesterday) cutoff=$(date -u -d yesterday +%FT00:00:00Z) ;;
*[0-9]h) cutoff=$(date -u -d "-${since%h} hours" +%FT%TZ) ;;
*[0-9]d) cutoff=$(date -u -d "-${since%d} days" +%FT%TZ) ;;
*) cutoff=$(date -u -d "$since" +%FT%TZ) || { echo "review.sh: -since: cannot read $since" >&2; exit 2; } ;;
esac

# readpub CMD...: run CMD on the public log with whatever access works.
readpub() {
	if [ -x "$(dirname "$PUBLIC_LOG")" ]; then
		"$@"
	elif getent group ssql-faq | cut -d: -f4 | tr ',' '\n' | grep -qx "$(id -un)"; then
		sg ssql-faq -c "$(printf '%q ' "$@")"
	else
		sudo -u ssql-faq "$@"
	fi
}

# cat_logs: every line of the chosen logs, the rotated generation first,
# each marked with where it came from.
cat_logs() {
	if [ "$which" != public ]; then
		for f in "$LAN_LOG.1" "$LAN_LOG"; do [ -r "$f" ] && jq -c '. + {src: "lan"}' "$f"; done
	fi
	if [ "$which" != lan ]; then
		readpub sh -c 'for f in "$1.1" "$1"; do [ -r "$f" ] && cat "$f"; done; true' sh "$PUBLIC_LOG" |
			jq -c '. + {src: "public"}'
	fi
	true
}

if [ -t 1 ]; then B=$'\e[1m' D=$'\e[2m' R=$'\e[0m' G=$'\e[32m' Y=$'\e[33m' X=$'\e[31m'; else B="" D="" R="" G="" Y="" X=""; fi

# The filter and the layout, shared by the merged and the follow views.
# clean strips control characters (but tab and newline) from what a
# visitor or the model wrote: a question carrying ANSI or OSC escapes
# would otherwise reach this terminal (-f writes it unfiltered; less -R
# passes colours and OSC 8 links). DFC169 §12.16.
DEFS='def clean: if type == "string" then gsub("[\u0001-\u0008\u000b-\u001f\u007f-\u009f]"; "\ufffd") else . end;'
FILTER='
	select($cutoff == "" or .time >= $cutoff)
	| select($status == "" or (.status | test($status)))
	| select($grep == "" or ([.question, .pipeline // "", .reply // ""] | any(test($grep; "i"))))
	| select($vote == "" or (.voted // "") == $vote or ($vote == "any" and (.voted // "") != ""))'
FORMAT='
	(if .status == "verified" then $G elif .status == "unverified" or .status == "refused" or .status == "error" then $X else $Y end) as $c
	| "\($B)── \(.time | sub("T"; " ") | sub("Z"; "")) \(.src)\(if .follow_up then " follow-up" else "" end)  \($c)\(.status)\($R)\($D)  \(.seconds * 10 | round / 10)s"
	  + (if .corrections > 0 then ", \(.corrections) correction\(if .corrections > 1 then "s" else "" end)" else "" end)
	  + (if .revised then ", revised" else "" end)
	  + (if .voted == "up" then "  👍" elif .voted == "down" then "  👎" else "" end)
	  + (if (.pasted_cols // 0) > 0 then ", pasted \(.pasted_cols) cols \(.pasted_rows // 0) rows" else "" end)
	  + "\($R)\n\($B)Q:\($R) \(.question | clean)"
	  + (if (.pipeline // "") != "" then "\n\($B)pipeline:\($R) \(.pipeline | clean)" else "" end)
	  + (if (.failure // "") != "" then "\n\($X)failure:\($R) \(.failure | clean)" else "" end)
	  + (if $brief or (.reply // "") == "" then "" else "\n\($B)A:\($R)\n\(.reply | clean | split("\n") | map("  " + .) | join("\n"))" end)
	  + "\n"'
$follow && cutoff=""
args=(--arg cutoff "$cutoff" --arg status "$status" --arg grep "$grep" --arg vote "$vote" --argjson brief "$brief"
	--arg B "$B" --arg D "$D" --arg R "$R" --arg G "$G" --arg Y "$Y" --arg X "$X")

if $follow; then
	{
		[ "$which" = public ] || tail -n0 -F "$LAN_LOG" 2>/dev/null | jq --unbuffered -c '. + {src: "lan"}' &
		[ "$which" = lan ] || readpub tail -n0 -F "$PUBLIC_LOG" 2>/dev/null | jq --unbuffered -c '. + {src: "public"}' &
		wait
	} | jq --unbuffered -r "${args[@]}" "$DEFS
		if has(\"vote\") then
			select(\$vote == \"\" or \$vote == \"any\" or .vote == \$vote)
			| \"\(\$B)── \(.time | sub(\"T\"; \" \") | sub(\"Z\"; \"\")) \(.src)  \(if .vote == \"up\" then \"👍\" else \"👎\" end)\(\$R) on: \(.question | clean)\n\"
		else $FILTER | $FORMAT end"
	exit
fi

out=$(cat_logs | jq -s -r "${args[@]}" --arg n "$n" "$DEFS
	sort_by(.time)
	| (map(select(has(\"vote\"))) | map({key: .id, value: .vote}) | from_entries) as \$votes
	| map(select(has(\"vote\") | not) | . + {voted: \$votes[.id // \"\"]})
	| map($FILTER)
	| if \$n == \"\" then . else .[-(\$n | tonumber):] end
	| (.[] | $FORMAT),
	  \"\(length) question\(if length == 1 then \"\" else \"s\" end): \" + (group_by(.status) | map(\"\(length) \(.[0].status)\") | join(\", \"))
	  + ([.[] | select(.voted == \"up\")] | length | if . > 0 then \"; \(.) 👍\" else \"\" end)
	  + ([.[] | select(.voted == \"down\")] | length | if . > 0 then \"; \(.) 👎\" else \"\" end)")
if [ -t 1 ]; then printf '%s\n' "$out" | less -R +G; else printf '%s\n' "$out"; fi
