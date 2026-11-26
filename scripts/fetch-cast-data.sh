#!/bin/bash
# Rebuild cmd/ssql-playground/data/cast.csv: which actor was in which film,
# for a "six degrees of Kevin Bacon" graph (`ssql traverse -both`).
#
#   scripts/fetch-cast-data.sh            # fetch and write the file
#
# The source is Wikidata (CC0, public domain), through its SPARQL service:
# every film (instance of Q11424) with 60 or more Wikipedia language
# editions, plus every film Kevin Bacon (Q3454165) was in, with each film's
# cast members (P161). A film is written "Title (year)", the year its
# earliest release, so remakes stay apart.
#
# It is a sample, not the whole of cinema: some Bacon numbers come out
# larger than the real ones, and some actors are not connected at all.
# Two actors with the same name are one node.
#
# Wikidata now keeps many names as "mul" (multilingual) labels instead of
# "en", so both are asked for: asking for "en" alone lost Apollo 13 and
# Kevin Bacon himself (2026-11-26). One query for every decade would
# time out, so it asks a decade at a time, retrying each.
set -euo pipefail

root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
out=$root/cmd/ssql-playground/data/cast.csv
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

labels='
  OPTIONAL { ?film rdfs:label ?fen FILTER(LANG(?fen) = "en") }
  OPTIONAL { ?film rdfs:label ?fmul FILTER(LANG(?fmul) = "mul") }
  OPTIONAL { ?actor rdfs:label ?aen FILTER(LANG(?aen) = "en") }
  OPTIONAL { ?actor rdfs:label ?amul FILTER(LANG(?amul) = "mul") }
  BIND(COALESCE(?fen, ?fmul) AS ?filmLabel) BIND(COALESCE(?aen, ?amul) AS ?actorLabel)
  FILTER(BOUND(?filmLabel) && BOUND(?actorLabel))'

# query NAME SPARQL: fetch one result as CSV into $work/NAME.csv, retrying,
# and fail loudly when every try fails or times out.
query() {
	local name=$1 sparql=$2 try
	for try in 1 2 3 4; do
		if curl -sf --max-time 120 -G https://query.wikidata.org/sparql \
			-H 'Accept: text/csv' -A 'ssql-sample-data/1.0' \
			--data-urlencode "query=$sparql" -o "$work/$name.csv" &&
			! grep -q 'Exception' "$work/$name.csv" &&
			head -1 "$work/$name.csv" | grep -q '^film,filmLabel,year,actor,actorLabel'; then
			echo "  $name: $(($(wc -l <"$work/$name.csv") - 1)) rows" >&2
			return
		fi
		sleep $((try * 5))
	done
	echo "fetch-cast-data: $name failed after $try tries" >&2
	exit 1
}

echo "fetching from Wikidata:" >&2
for d in $(seq 1900 10 2020); do
	query "films_$d" "SELECT ?film ?filmLabel ?year ?actor ?actorLabel WHERE {
  ?film wdt:P31 wd:Q11424; wikibase:sitelinks ?sl; wdt:P161 ?actor.
  FILTER(?sl >= 60)
  ?film wdt:P577 ?date. BIND(YEAR(?date) AS ?year) FILTER(?year >= $d && ?year < $((d + 10)))
  $labels
}"
done
query bacon "SELECT ?film ?filmLabel ?year ?actor ?actorLabel WHERE {
  ?film wdt:P161 wd:Q3454165; wdt:P31 wd:Q11424; wdt:P161 ?actor.
  ?film wdt:P577 ?date. BIND(YEAR(?date) AS ?year)
  $labels
}"

{
	head -1 "$work/bacon.csv"
	for f in "$work"/*.csv; do tail -n +2 "$f"; done
} >"$work/all.csv"

# A film has a row per release date: keep the earliest year. Sorted, so a
# refetch diffs by what changed.
ssql from "$work/all.csv" |
	ssql group-by film filmLabel actor actorLabel -min year year |
	ssql update -set-expr movie 'filmLabel + " (" + string(year) + ")"' |
	ssql rename -as actorLabel actor_name |
	ssql include movie actor_name |
	ssql distinct |
	ssql rename -as actor_name actor |
	ssql sort movie actor |
	ssql to csv "$work/cast.csv"

# Fail loudly on a fetch that lost what the examples rely on.
for want in 'Kevin Bacon' 'Apollo 13 (1995)' 'Marilyn Monroe'; do
	grep -qF "$want" "$work/cast.csv" || { echo "fetch-cast-data: no '$want' in the result" >&2; exit 1; }
done
mv "$work/cast.csv" "$out"
echo "wrote $out: $(($(wc -l <"$out") - 1)) rows, $(wc -c <"$out") bytes" >&2
