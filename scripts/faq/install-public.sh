#!/bin/bash
# The public FAQ instance (DFC169 §11.4): its own Unix user, its own copy of
# the docs, a hardened user unit on 127.0.0.1:8485. Tailscale Funnel is a
# separate step this script never takes.
#
#   scripts/faq/install-public.sh stage      # as you: build and stage (no sudo)
#   sudo scripts/faq/install-public.sh install STAGE  # as root: user, files, unit, start
#   scripts/faq/install-public.sh status     # is it up, what has it answered
#
# Run `stage` then `install` again to update: the app tree is replaced
# whole, the user's log and caches are kept.
#
# What the ssql-faq user gets: a home at /var/lib/ssql-faq holding its
# caches, its question log and its unit; a root-owned, read-only app tree
# (the faq binary, the slim ssql, docsearch, and the committed user docs:
# no journals, no claude/ notes, no research docs). It cannot read your
# home (mode 750, and the unit makes /home inaccessible anyway).
set -euo pipefail

USER_NAME=ssql-faq
HOME_DIR=/var/lib/ssql-faq
APP=$HOME_DIR/app
UNIT=ssql-faq-public.service
STAGE=${XDG_CACHE_HOME:-$HOME/.cache}/ssql-faq-public-stage

repo() { git -C "$(dirname "$0")" rev-parse --show-toplevel; }

stage() {
	local r
	r=$(repo)
	if [ -n "$(git -C "$r" status --porcelain -- doc README.md CHANGELOG.md cmd/ssql-playground/data scripts/faq scripts/docsearch)" ]; then
		echo "uncommitted changes in the docs or the faq: the public copy is built from HEAD; commit first" >&2
		exit 1
	fi
	rm -rf "$STAGE"
	mkdir -p "$STAGE/app/bin" "$STAGE/app/repo/doc/research"
	# The committed user docs and sample data only.
	git -C "$r" archive HEAD README.md CHANGELOG.md go.mod 'doc/*.md' doc/codelab-data cmd/ssql-playground/data |
		tar -x -C "$STAGE/app/repo"
	# A pathspec's * crosses /, so doc/*.md brought the research docs too.
	find "$STAGE/app/repo/doc" -mindepth 2 -name '*.md' -delete
	mkdir -p "$STAGE/app/repo/doc/research" # docsearch finds its root by go.mod and doc/research
	cp "$r/.docsearch-cache.jsonl" "$STAGE/app/repo/" 2>/dev/null || true
	(cd "$r" && CGO_ENABLED=0 go build -tags slim -ldflags "-s -w" -o "$STAGE/app/bin/ssql" ./cmd/ssql)
	(cd "$r/scripts/faq" && go build -o "$STAGE/app/bin/ssql-faq" .)
	(cd "$r/scripts/docsearch" && go build -o "$STAGE/app/bin/docsearch" .)
	cp "$r/scripts/faq/$UNIT" "$STAGE/"
	git -C "$r" rev-parse --short=8 HEAD >"$STAGE/app/REVISION"
	# Embed the copy's chunks now, as you, so the service's first question
	# does not: the service sees the app tree read-only.
	(cd "$STAGE/app/repo" && "$STAGE/app/bin/docsearch" -k 1 warm-up >/dev/null)
	echo "staged $(cat "$STAGE/app/REVISION") in $STAGE; now: sudo $0 install $STAGE"
}

install_root() {
	local src=${1:?usage: sudo $0 install STAGE_DIR}
	[ "$(id -u)" = 0 ] || { echo "install needs root: sudo $0 install $src" >&2; exit 1; }
	[ -x "$src/app/bin/ssql-faq" ] || { echo "$src is not a staged tree; run $0 stage first" >&2; exit 1; }
	if ! getent passwd "$USER_NAME" >/dev/null; then
		useradd --system --user-group --create-home --home-dir "$HOME_DIR" --shell /usr/sbin/nologin "$USER_NAME"
	fi
	local uid
	uid=$(id -u "$USER_NAME")
	# The app tree: root's, readable, never writable by the service.
	rm -rf "$APP.new"
	cp -a "$src/app" "$APP.new"
	chown -R root:root "$APP.new"
	chmod -R u=rwX,go=rX "$APP.new"
	rm -rf "$APP.old"
	[ -d "$APP" ] && mv "$APP" "$APP.old"
	mv "$APP.new" "$APP"
	rm -rf "$APP.old"
	# What the service may write, and its unit.
	install -d -o "$USER_NAME" -g "$USER_NAME" -m 700 "$HOME_DIR/.cache" "$HOME_DIR/.local" "$HOME_DIR/.local/state" "$HOME_DIR/.config" "$HOME_DIR/.config/systemd" "$HOME_DIR/.config/systemd/user"
	install -o "$USER_NAME" -g "$USER_NAME" -m 644 "$src/$UNIT" "$HOME_DIR/.config/systemd/user/$UNIT"
	loginctl enable-linger "$USER_NAME"
	for _ in $(seq 1 20); do [ -S "/run/user/$uid/bus" ] && break; sleep 0.5; done
	systemctl --user -M "$USER_NAME@" daemon-reload
	systemctl --user -M "$USER_NAME@" enable "$UNIT"
	systemctl --user -M "$USER_NAME@" restart "$UNIT"
	echo "started $UNIT as $USER_NAME (app $(cat "$APP/REVISION")); check: $0 status"
}

status() {
	systemctl --user -M "$USER_NAME@" status "$UNIT" --no-pager -n 5 2>/dev/null ||
		sudo systemctl --user -M "$USER_NAME@" status "$UNIT" --no-pager -n 5 || true
	echo "health (today's answered and refused questions):"
	curl -s --max-time 5 http://127.0.0.1:8485/api/health && echo
	echo "today's answers by status (the log is ssql-faq's own, so this asks sudo):"
	local log=$HOME_DIR/.local/state/ssql-faq/questions.jsonl
	local lines
	if ! lines=$(sudo -u "$USER_NAME" sh -c "test ! -e '$log' || cat '$log'"); then
		echo "  could not read the log (sudo refused)"
	elif [ -z "$lines" ]; then
		echo "  nothing asked yet"
	else
		printf '%s\n' "$lines" |
			ssql from jsonl | ssql where -if time ge "$(date -u +%F)" | ssql group-by status -count n | ssql to table || true
	fi
	echo "Funnel (on: tailscale funnel --bg --https=8443 8485; off: tailscale funnel --https=8443 off):"
	tailscale funnel status 2>&1 | sed 's/^/  /'
}

case ${1:-} in
stage) stage ;;
install) install_root "${2:-}" ;;
status) status ;;
*)
	sed -n '2,13p' "$0"
	exit 2
	;;
esac
