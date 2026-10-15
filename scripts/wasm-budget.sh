#!/bin/bash
# wasm-budget.sh FILE MAX_MB — the size gate for the slim wasm (DFC151).
#
# Prints FILE's gzipped size and fails when it is over MAX_MB. A .gz is
# measured as it is; anything else is gzipped at -9 (what `make
# explore-wasm` embeds and what GitHub Pages serves, give or take its
# own level). The ceiling is a generous absolute number, never a stored
# baseline (DFC113's rule for the scale gate): it catches a dependency
# that drifts back in, not a kilobyte of growth.
set -euo pipefail
file="$1"; max_mb="$2"
case "$file" in
  *.gz) bytes=$(wc -c < "$file") ;;
  *)    bytes=$(gzip -9 -c "$file" | wc -c) ;;
esac
max_bytes=$(( max_mb * 1048576 ))
printf '%s: %.2f MB gzipped (budget %s MB)\n' "$file" "$(echo "$bytes / 1048576" | bc -l)" "$max_mb"
if [ "$bytes" -gt "$max_bytes" ]; then
  echo "wasm-budget: $file is over the ${max_mb} MB gzipped ceiling — run TestSlimBuildDependencies to see what came back in (DFC151)" >&2
  exit 1
fi
