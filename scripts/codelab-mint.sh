#!/usr/bin/env bash
# codelab-mint.sh — DFC126: do the CLI codelab from scratch, as a novice
# would, in a fresh LXD container: launch ubuntu:24.04, follow the Setup
# section VERBATIM as the unprivileged `ubuntu` user (apt-get Go, go
# install @latest, PATH line, ssql codelab), run the self-test runner the
# data ships with, drive the interactive keys through a real pty, and try
# the blocks the runner skips (generate go -run). Prints a report; exits
# non-zero if any step fails. Nothing from this checkout goes into the
# container — the point is to test what a reader gets from the proxy.
#
# Usage: scripts/codelab-mint.sh [-k] [-b BINARY] [NAME]
#   -k keeps the container; -b BINARY pushes a local build over the
#   installed one after Setup (so a pre-release binary and the runner it
#   embeds can be exercised before the tag exists — the install step
#   itself still tests the published release).
# Requires: lxd with the ubuntu: remote, network in containers. ~6 min.
set -o pipefail
KEEP=; NAME=ssql-codelab; LOCALBIN=
while (( $# )); do
  case "$1" in
    -k) KEEP=1 ;;
    -b) LOCALBIN="$2"; shift ;;
    *) NAME="$1" ;;
  esac
  shift
done
say() { printf '\n== %s\n' "$*"; }
fail=0
lxc delete -f "$NAME" 2>/dev/null
say "launch ubuntu:24.04 as $NAME"
lxc launch ubuntu:24.04 "$NAME" >/dev/null || exit 1
for i in $(seq 1 60); do lxc exec "$NAME" -- getent hosts archive.ubuntu.com >/dev/null 2>&1 && break; sleep 2; done
run() { lxc exec "$NAME" -- su - ubuntu -c "$1"; }   # login shell of the novice user

say "Setup step 1 — Go, ssql, PATH (verbatim from doc/cli-codelab.md)"
run 'set -e
t0=$(date +%s); sudo apt-get update -qq >/dev/null; sudo apt-get install -y golang-go >/dev/null 2>&1; echo "apt-get golang-go: $(( $(date +%s) - t0 )) s, $(go version)"
t0=$(date +%s); go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest 2>&1 | grep -v "^go: downloading" ; echo "go install: $(( $(date +%s) - t0 )) s"
echo '"'"'export PATH="$PATH:$HOME/go/bin"'"'"' >> ~/.bashrc
export PATH="$PATH:$HOME/go/bin"
ssql version
echo "module cache: $(du -sh ~/go/pkg/mod | cut -f1); toolchains: $(ls -d ~/go/pkg/mod/golang.org/toolchain@* 2>/dev/null | xargs -n1 basename | tr "\n" " ")"' || fail=1

if [[ -n "$LOCALBIN" ]]; then
  say "pushing local build $LOCALBIN over the installed ssql (pre-release check)"
  lxc file push "$LOCALBIN" "$NAME/home/ubuntu/go/bin/ssql" >/dev/null 2>&1 || fail=1
  run 'export PATH="$PATH:$HOME/go/bin"; ssql version'
fi

say "Setup steps 2–4 — data, tmux, completion"
run 'set -e; export PATH="$PATH:$HOME/go/bin"
ssql codelab | tail -4
tmux -V
bash -ic '"'"'eval "$(ssql -shell-init)"; complete -p ssql; echo "ssql key bindings: $(bind -X 2>/dev/null | grep -c _ssql)"'"'"' 2>&1 | grep -v "job control"' || fail=1

say "Interactive keys through a real pty (no tmux: inline mode)"
lxc file push "$(dirname "$0")/codelab-mint-keys.py" "$NAME/home/ubuntu/keys.py" >/dev/null 2>&1
run 'python3 ~/keys.py' || fail=1

say "The runner the data ships with: every block against the installed ssql"
run 'export PATH="$PATH:$HOME/go/bin"; cd ~/ssql-codelab && ./codelab-run.sh 2>&1 | tail -4' || fail=1

say "The signal-processing codelab the same way (python3 generates its signals)"
run 'export PATH="$PATH:$HOME/go/bin"; python3 --version; cd ~/ssql-codelab && ./codelab-run.sh signal 2>&1 | tail -4' || fail=1

say "Blocks the runner skips: generate go -run (first and second run)"
run 'export PATH="$PATH:$HOME/go/bin"; cd ~/ssql-codelab
for i in 1 2; do t0=$(date +%s); ssql generate go -run -pipeline "ssql from employees.csv | ssql where -if dept eq Engineering | ssql group-by city -count n | ssql to table" 2>&1 | grep -v "^go: downloading" | tail -3; echo "run $i: $(( $(date +%s) - t0 )) s"; done' || fail=1

if [[ -z "$KEEP" ]]; then lxc delete -f "$NAME"; else say "container $NAME kept (lxc exec $NAME -- su - ubuntu)"; fi
say "codelab-mint: $([[ $fail -eq 0 ]] && echo PASS || echo FAIL)"
exit $fail
