#!/bin/bash
# bench-engines.sh — the engine table of doc/performance.md §1a, repeatable.
#
# Runs the same seven pipelines three ways over one Parquet file — the
# typed Go program (`generate go -build`), DuckDB (`generate sql | duckdb`)
# and Apache DataFusion through the generated Rust program (`generate rust
# -build`) — best of N runs each, whole-process wall and peak RSS, output
# to /dev/null, and prints the table in the doc's format. The 2026-10-27
# table (DFC157 §4, DFC158 §1) came from one-off scripts in a session
# scratchpad; this is the same method in the repo, so a change can be
# measured in one command and a release note's numbers reproduced.
#
#   scripts/bench-engines.sh FILE.parquet [options]
#
#   -k COL     the partition / group key column           (default a_kind)
#   -s COL     a string column, the window's order key     (default a_name)
#   -v COL     a numeric column: counted, summed, cast     (default relationship)
#   -k2 COL    a second key for the cube                   (default z_kind)
#   -s2 COL    a second column for the filtered write      (default z_name)
#   -eq VALUE  the filtered write's `where -if V eq VALUE` (default 7)
#   -csv FILE  the same rows as CSV (default: written once beside the cache)
#   -join DIR  the 10 M-row join corpus (data/depts/regions/cities.csv;
#              default /tmp/ssql-typed-scale when it exists; the typed/
#              benchmarks generate it, see doc/performance.md "Reproducing")
#   -engines L comma list of typed,duckdb,rust (default: all that are installed)
#   -runs N    runs per cell, best reported                 (default 3)
#   -timeout S cap per run in seconds                       (default 1800)
#   -o FILE    also write the table to FILE
#
# Environment: SSQL names the ssql binary (default: built from this checkout
# into the cache dir); SSQL_MODULE_DIR is the module the typed programs
# compile against (default: this checkout); DUCKDB the duckdb binary
# (default: on PATH, then ~/.local/bin/duckdb); cargo is found as
# `generate rust` finds it (PATH, ~/.cargo/bin, SSQL_CARGO).
#
# Measure on an idle machine: a gate run competing for the cores inflated a
# 1.55 s read to 2.35 s (DFC158 §2.6). This is a measurement, not a gate —
# the scale gate (SSQL_SCALE=1) is the gate.
set -u

usage() { sed -n '2,/^set -u/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; exit 2; }

K=a_kind S=a_name V=relationship K2=z_kind S2=z_name EQ=7
CSV="" JOIN="" ENGINES="" RUNS=3 TIMEOUT=1800 OUT=""
PARQUET=""
while [ $# -gt 0 ]; do
  case "$1" in
    -k) K=$2; shift 2 ;;
    -s) S=$2; shift 2 ;;
    -v) V=$2; shift 2 ;;
    -k2) K2=$2; shift 2 ;;
    -s2) S2=$2; shift 2 ;;
    -eq) EQ=$2; shift 2 ;;
    -csv) CSV=$2; shift 2 ;;
    -join) JOIN=$2; shift 2 ;;
    -engines) ENGINES=$2; shift 2 ;;
    -runs) RUNS=$2; shift 2 ;;
    -timeout) TIMEOUT=$2; shift 2 ;;
    -o) OUT=$2; shift 2 ;;
    -h|--help) usage ;;
    -*) echo "unknown option $1" >&2; usage ;;
    *) [ -n "$PARQUET" ] && { echo "one Parquet file, got '$PARQUET' and '$1'" >&2; usage; }; PARQUET=$1; shift ;;
  esac
done
[ -n "$PARQUET" ] || usage
[ -f "$PARQUET" ] || { echo "no such file: $PARQUET" >&2; exit 2; }
PARQUET=$(readlink -f "$PARQUET")
[ -x /usr/bin/time ] || { echo "needs GNU time at /usr/bin/time (apt install time)" >&2; exit 2; }

REPO=$(cd "$(dirname "$0")/.." && pwd)
CACHE=${XDG_CACHE_HOME:-$HOME/.cache}/ssql/bench/$(echo "$PARQUET" | md5sum | cut -c1-12)
mkdir -p "$CACHE"
export SSQL_MODULE_DIR=${SSQL_MODULE_DIR:-$REPO}

# The ssql under test: SSQL, else this checkout built into the cache.
if [ -z "${SSQL:-}" ]; then
  SSQL=$CACHE/ssql
  (cd "$REPO" && go build -ldflags "-s -w" -o "$SSQL" ./cmd/ssql) || { echo "building ssql from $REPO failed" >&2; exit 1; }
fi
SSQL=$(readlink -f "$SSQL")
VERSION=$("$SSQL" version 2>/dev/null | head -1)

DUCKDB=${DUCKDB:-$(command -v duckdb || true)}
[ -z "$DUCKDB" ] && [ -x "$HOME/.local/bin/duckdb" ] && DUCKDB=$HOME/.local/bin/duckdb
CARGO=${SSQL_CARGO:-$(command -v cargo || true)}
[ -z "$CARGO" ] && [ -x "$HOME/.cargo/bin/cargo" ] && CARGO=$HOME/.cargo/bin/cargo

if [ -z "$ENGINES" ]; then
  ENGINES=typed
  [ -n "$DUCKDB" ] && ENGINES=$ENGINES,duckdb
  [ -n "$CARGO" ] && ENGINES=$ENGINES,rust
fi
has() { case ",$ENGINES," in *,"$1",*) return 0 ;; *) return 1 ;; esac; }
has duckdb && [ -z "$DUCKDB" ] && { echo "duckdb not found (DUCKDB=… or ~/.local/bin/duckdb)" >&2; exit 2; }
has rust && [ -z "$CARGO" ] && { echo "cargo not found (rustup; ~/.cargo/bin/cargo)" >&2; exit 2; }

# The CSV twin of the Parquet file, written once.
if [ -z "$CSV" ]; then
  CSV=$CACHE/rows.csv
  if [ ! -s "$CSV" ] || [ "$PARQUET" -nt "$CSV" ]; then
    echo "writing $CSV from the Parquet file (once)" >&2
    "$SSQL" from parquet "$PARQUET" | "$SSQL" to csv > "$CSV" || { rm -f "$CSV"; echo "writing the CSV failed" >&2; exit 1; }
  fi
fi
CSV=$(readlink -f "$CSV")
[ -z "$JOIN" ] && [ -d /tmp/ssql-typed-scale ] && [ -f /tmp/ssql-typed-scale/data.csv ] && JOIN=/tmp/ssql-typed-scale

# ---- the pipelines ------------------------------------------------------
# name@@label@@pipeline (the label is the table's first column; @@ because a label may hold an escaped |)
PIPES=()
PIPES+=("cube@@\`group-by $K $V $K2 -count -cube\` (the README cube)@@$SSQL from parquet $PARQUET | $SSQL group-by $K $V $K2 -count n -cube | $SSQL to csv")
PIPES+=("groupby_parquet@@\`group-by $V -count\`, Parquet@@$SSQL from parquet $PARQUET | $SSQL group-by $V -count n | $SSQL to csv")
PIPES+=("groupby_csv@@\`group-by $V -count\`, the same rows as CSV@@$SSQL from csv $CSV | $SSQL group-by $V -count n | $SSQL to csv")
PIPES+=("filter_write@@\`where -if $V eq $EQ \\| include $K $S $S2 \\| to csv\`@@$SSQL from parquet $PARQUET | $SSQL where -if $V eq $EQ | $SSQL include $K $S $S2 | $SSQL to csv")
if [ -n "$JOIN" ]; then
  PIPES+=("join3@@10 M rows, \`where age gt 30\`, three chained \`join -using\`, \`count\`@@$SSQL from csv $JOIN/data.csv | $SSQL where -if age gt 30 | $SSQL join $JOIN/depts.csv -using dept_id | $SSQL join $JOIN/regions.csv -using region_id | $SSQL join $JOIN/cities.csv -using city_id | $SSQL count")
fi
PIPES+=("decimal@@\`cast -type $V decimal:2 \\| group-by $K -sum\`@@$SSQL from parquet $PARQUET | $SSQL cast -type $V decimal:2 | $SSQL group-by $K -sum $V total | $SSQL to csv")
PIPES+=("window@@\`window -partition $K -order $S -sum $V\` then \`group-by $K -max\`@@$SSQL from parquet $PARQUET | $SSQL window -partition $K -order $S -sum $V running | $SSQL group-by $K -max running m | $SSQL to csv")

# ---- builds -------------------------------------------------------------
# One program or SQL file per pipeline and engine, under the cache; the
# builds are not timed.
build_typed() { # name pipeline
  "$SSQL" generate go -build "$CACHE/$1.typed" -pipeline "$2" > /dev/null 2> "$CACHE/$1.typed.err"
}
build_rust() { # name pipeline
  "$SSQL" generate rust -build "$CACHE/$1.rust" -pipeline "$2" > /dev/null 2> "$CACHE/$1.rust.err"
}
build_sql() { # name pipeline
  (export SSQL_MODE=record; eval "$2") 2> "$CACHE/$1.sql.err" | "$SSQL" generate sql -dialect duckdb > "$CACHE/$1.duck.sql" 2>> "$CACHE/$1.sql.err"
  [ -s "$CACHE/$1.duck.sql" ]
}

# measure CMD... → "WALL RSS_KB ROWS" of the best run (by wall), or "fail".
measure() {
  local best="" bestrss="" rows="" w r
  for _ in $(seq 1 "$RUNS"); do
    /usr/bin/time -f "%e %M" -o "$CACHE/time.tmp" timeout "$TIMEOUT" "$@" > "$CACHE/out.tmp" 2> "$CACHE/run.err"
    read -r w r < <(tail -1 "$CACHE/time.tmp")
    case "$w" in ''|*[!0-9.]*) echo fail; return ;; esac
    grep -q '^Command exited with non-zero' "$CACHE/time.tmp" && { echo fail; return; }
    rows=$(wc -l < "$CACHE/out.tmp")
    if [ -z "$best" ] || awk -v a="$w" -v b="$best" 'BEGIN{exit !(a < b)}'; then best=$w; bestrss=$r; fi
  done
  echo "$best $bestrss $rows"
}

fmt_cell() { # "WALL RSS ROWS" | fail | skip:reason
  case "$1" in
    fail) echo "failed" ;;
    skip:*) echo "—" ;;
    *) set -- $1; printf '%s s' "$1" ;;
  esac
}
fmt_rss() {
  case "$1" in
    fail|skip:*) echo "—" ;;
    *) set -- $1; awk -v kb="$2" 'BEGIN{ if (kb >= 1048576) printf "%.1f GB", kb/1048576; else printf "%.0f MB", kb/1024 }' ;;
  esac
}

echo "ssql: $VERSION; parquet: $PARQUET; csv: $CSV; join: ${JOIN:-none}; engines: $ENGINES; runs: $RUNS" >&2
if has rust; then
  echo "rust: the first build compiles DataFusion once (minutes); later builds relink" >&2
fi

declare -A R   # R[name,engine] = "WALL RSS ROWS" | fail | skip:reason
for entry in "${PIPES[@]}"; do
  name=${entry%%@@*}; rest=${entry#*@@}; label=${rest%%@@*}; pipe=${rest#*@@}
  echo "== $name" >&2
  if has typed; then
    if build_typed "$name" "$pipe"; then R[$name,typed]=$(measure "$CACHE/$name.typed")
    else R[$name,typed]="skip:$(tail -1 "$CACHE/$name.typed.err")"; fi
    echo "   typed  ${R[$name,typed]}" >&2
  fi
  if has duckdb; then
    if build_sql "$name" "$pipe"; then R[$name,duckdb]=$(measure bash -c "'$DUCKDB' -csv < '$CACHE/$name.duck.sql'")
    else R[$name,duckdb]="skip:$(tail -1 "$CACHE/$name.sql.err")"; fi
    echo "   duckdb ${R[$name,duckdb]}" >&2
  fi
  if has rust; then
    if build_rust "$name" "$pipe"; then R[$name,rust]=$(measure "$CACHE/$name.rust")
    else R[$name,rust]="skip:$(tail -1 "$CACHE/$name.rust.err")"; fi
    echo "   rust   ${R[$name,rust]}" >&2
  fi
done

# ---- the table ----------------------------------------------------------
{
  echo "Machine: $(nproc) cores, $(free -g | awk '/Mem/{print $2}') GB; $(date +%F); $VERSION; best of $RUNS, whole-process wall, output to /dev/null."
  echo
  hdr="| Pipeline |"; sep="|---|"
  has rust && { hdr="$hdr DataFusion (generated Rust) |"; sep="$sep---:|"; }
  has typed && { hdr="$hdr ssql \`generate go\` (typed) |"; sep="$sep---:|"; }
  has duckdb && { hdr="$hdr DuckDB $("$DUCKDB" --version 2>/dev/null | awk '{print $1}') |"; sep="$sep---:|"; }
  echo "$hdr"; echo "$sep"
  for entry in "${PIPES[@]}"; do
    name=${entry%%@@*}; rest=${entry#*@@}; label=${rest%%@@*}
    row="| $label |"
    has rust && row="$row $(fmt_cell "${R[$name,rust]}") |"
    has typed && row="$row $(fmt_cell "${R[$name,typed]}") |"
    has duckdb && row="$row $(fmt_cell "${R[$name,duckdb]}") |"
    echo "$row"
  done
  echo
  echo "Peak RSS (same order):"
  echo
  for entry in "${PIPES[@]}"; do
    name=${entry%%@@*}; rest=${entry#*@@}; label=${rest%%@@*}
    cells=""
    has rust && cells="$cells $(fmt_rss "${R[$name,rust]}") /"
    has typed && cells="$cells $(fmt_rss "${R[$name,typed]}") /"
    has duckdb && cells="$cells $(fmt_rss "${R[$name,duckdb]}") /"
    echo "- $name:${cells% /}"
  done
  # Output row counts that disagree between engines are worth a look (every
  # sink is `to csv` with a header, DuckDB's -csv included; the join row ends
  # in `count`, whose one number each engine prints its own way, so it is
  # not compared — `count | to csv` does not compile in typed mode yet, TODO).
  for entry in "${PIPES[@]}"; do
    name=${entry%%@@*}
    [ "$name" = join3 ] && continue
    counts=""
    for e in rust typed duckdb; do
      has "$e" || continue
      v=${R[$name,$e]}
      case "$v" in fail|skip:*) continue ;; esac
      set -- $v; counts="$counts $e=$3"
    done
    n=$(echo "$counts" | tr ' ' '\n' | sed 's/.*=//' | sort -u | grep -c .)
    [ "$n" -gt 1 ] && echo "- $name: OUTPUT ROW COUNTS DIFFER:$counts"
  done
  for entry in "${PIPES[@]}"; do
    name=${entry%%@@*}
    for e in rust typed duckdb; do
      has "$e" || continue
      case "${R[$name,$e]}" in skip:*) echo "- $name/$e not measured: ${R[$name,$e]#skip:}" ;; esac
    done
  done
} > "$CACHE/table.md"
cat "$CACHE/table.md"
[ -n "$OUT" ] && cp "$CACHE/table.md" "$OUT"
exit 0
